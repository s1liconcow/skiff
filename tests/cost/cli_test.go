package cost_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/cli"
	"github.com/s1liconcow/skiff/internal/cost"
)

func TestCostExplainJSON(t *testing.T) {
	specPath := writeServiceSpec(t, "medium", 12, 24)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--cpu-p95", "18",
		"--memory-p95", "41",
		"--request-count", "10300000",
		"--warm-capacity", "8",
		"--log-mb-per-hour", "1400",
		"--format", "json",
		"--trace-id", "tr_cost",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got struct {
		OK      bool        `json:"ok"`
		TraceID string      `json:"trace_id"`
		Result  cost.Result `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode cost explain: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cost" || got.Result.Service != "payments-api" || got.Result.Shape.Name != "medium" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	for _, id := range []string{"cost.shape.downsize", "cost.replicas.reduce_min", "cost.logs.noisy"} {
		if !hasRecommendation(got.Result.Recommendations, id) {
			t.Fatalf("missing recommendation %s in %+v", id, got.Result.Recommendations)
		}
	}
}

func TestCostExplainJSONWithAWSPricing(t *testing.T) {
	specPath := writeServiceSpec(t, "medium", 12, 24)
	pricingPath := writeAWSPricingFixture(t)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--region", "us-east-1",
		"--aws-pricing-file", pricingPath,
		"--pricing-scheme", "on-demand",
		"--pricing-scheme", "ri-3yr-standard-all-upfront",
		"--cpu-p95", "18",
		"--memory-p95", "41",
		"--warm-capacity", "8",
		"--format", "json",
		"--trace-id", "tr_cost_pricing",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK      bool        `json:"ok"`
		TraceID string      `json:"trace_id"`
		Result  cost.Result `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode cost explain: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cost_pricing" || got.Result.Pricing == nil {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Result.Pricing.InstanceType != "t3.medium" || got.Result.Pricing.PublicationDate != "2026-05-14T21:07:47Z" {
		t.Fatalf("unexpected pricing estimate: %+v", got.Result.Pricing)
	}
	if got.Result.Pricing.Schemes[0].Scheme != cost.PricingSchemeOnDemand || got.Result.Pricing.Schemes[0].MinMonthlyUSD != 364.416 {
		t.Fatalf("unexpected on-demand scheme: %+v", got.Result.Pricing.Schemes)
	}
	rec := findRecommendation(got.Result.Recommendations, "cost.shape.downsize")
	if rec == nil || !strings.Contains(rec.EstimatedImpact, "on_demand saves about $182.21/month") {
		t.Fatalf("missing priced recommendation impact: %+v", rec)
	}
}

func TestCostExplainStatefulInfrastructurePricing(t *testing.T) {
	specPath := writeStatefulSpec(t)
	pricingPath := writeAWSPricingFixture(t)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain",
		"--file", specPath,
		"--aws-pricing-file", pricingPath,
		"--pricing-scheme", "on-demand",
		"--pricing-scheme", "ri-3yr-standard-all-upfront",
		"--format", "json",
		"--trace-id", "tr_stateful_cost",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK      bool        `json:"ok"`
		TraceID string      `json:"trace_id"`
		Result  cost.Result `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode cost explain: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_stateful_cost" || got.Result.Scale.MinReplicas != 3 {
		t.Fatalf("unexpected stateful envelope: %+v", got)
	}
	if got.Result.Infrastructure == nil {
		t.Fatalf("missing infrastructure estimate: %+v", got.Result)
	}
	if len(got.Result.Infrastructure.Totals) != 2 {
		t.Fatalf("unexpected totals: %+v", got.Result.Infrastructure.Totals)
	}
	if got.Result.Infrastructure.Totals[0].PricingScheme != cost.PricingSchemeOnDemand || got.Result.Infrastructure.Totals[0].MonthlyUSD != 143.052 {
		t.Fatalf("unexpected on-demand total: %+v", got.Result.Infrastructure.Totals)
	}
	if len(got.Result.Infrastructure.Scenarios) != 3 {
		t.Fatalf("unexpected scenarios: %+v", got.Result.Infrastructure.Scenarios)
	}
	if got.Result.Infrastructure.Scenarios[0].Name != "low" || got.Result.Infrastructure.Scenarios[0].Totals[0].MonthlyUSD != 114.927 {
		t.Fatalf("unexpected low scenario: %+v", got.Result.Infrastructure.Scenarios[0])
	}
	if got.Result.Infrastructure.Scenarios[2].Name != "high" || got.Result.Infrastructure.Scenarios[2].Totals[0].MonthlyUSD != 143.052 {
		t.Fatalf("unexpected high scenario: %+v", got.Result.Infrastructure.Scenarios[2])
	}
	for _, id := range []string{"storage.ebs.gp3", "storage.ebs_snapshots", "network.route53_records", "observability.logs", "observability.metrics"} {
		if !hasLineItem(got.Result.Infrastructure.LineItems, id) {
			t.Fatalf("missing infrastructure line item %s in %+v", id, got.Result.Infrastructure.LineItems)
		}
	}
}

func TestCostExplainWithoutPricingConfigDoesNotFetchPricing(t *testing.T) {
	t.Chdir(t.TempDir())
	specPath := writeStatefulSpec(t)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain",
		"--file", specPath,
		"--pricing-scheme", "on-demand",
		"--format", "json",
		"--trace-id", "tr_no_pricing_config",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK      bool        `json:"ok"`
		TraceID string      `json:"trace_id"`
		Result  cost.Result `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode cost explain: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_no_pricing_config" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Result.Pricing != nil || got.Result.Infrastructure != nil {
		t.Fatalf("pricing should not be loaded without config or explicit AWS pricing: %+v", got.Result)
	}
	if got.Result.PricingSetup == nil {
		t.Fatalf("missing pricing setup guidance: %+v", got.Result)
	}
	if got.Result.PricingSetup.ConfigPath != ".skiff-pricing.json" ||
		!got.Result.PricingSetup.AutoDetectNextRun ||
		!strings.Contains(got.Result.PricingSetup.UpdateCommand, "skiff cost pricing update --region us-east-1 --out .skiff-pricing.json") {
		t.Fatalf("unexpected pricing setup: %+v", got.Result.PricingSetup)
	}
}

func TestCostPricingUpdateWritesConfigUsedByExplain(t *testing.T) {
	rawPricingPath := writeAWSPricingFixture(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	pricingConfigPath := filepath.Join(workDir, ".skiff-pricing.json")
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "pricing", "update",
		"--region", "us-east-1",
		"--aws-pricing-file", rawPricingPath,
		"--out", pricingConfigPath,
		"--format", "json",
		"--trace-id", "tr_pricing_update",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("update exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var updated struct {
		OK           bool   `json:"ok"`
		TraceID      string `json:"trace_id"`
		Path         string `json:"path"`
		Region       string `json:"region"`
		Items        int    `json:"items"`
		StorageRates int    `json:"storage_rates"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
		t.Fatalf("decode pricing update: %v\n%s", err, stdout.String())
	}
	if !updated.OK || updated.TraceID != "tr_pricing_update" || updated.Path != pricingConfigPath || updated.Region != "us-east-1" || updated.Items == 0 || updated.StorageRates == 0 {
		t.Fatalf("unexpected update envelope: %+v", updated)
	}
	catalog, err := cost.LoadPricingCatalogFile(pricingConfigPath)
	if err != nil {
		t.Fatalf("load written pricing config: %v", err)
	}
	if catalog.SchemaVersion != cost.PricingCatalogSchemaVersion || catalog.Region != "us-east-1" {
		t.Fatalf("unexpected pricing config: %+v", catalog)
	}

	specPath := writeStatefulSpec(t)
	stdout.Reset()
	stderr.Reset()
	code = cli.Run("skiff", []string{
		"cost", "explain",
		"--file", specPath,
		"--pricing-scheme", "on-demand",
		"--format", "json",
		"--trace-id", "tr_priced_from_config",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("explain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var explained struct {
		OK      bool        `json:"ok"`
		TraceID string      `json:"trace_id"`
		Result  cost.Result `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &explained); err != nil {
		t.Fatalf("decode pricing explain: %v\n%s", err, stdout.String())
	}
	if !explained.OK || explained.TraceID != "tr_priced_from_config" || explained.Result.Infrastructure == nil {
		t.Fatalf("unexpected explain envelope: %+v", explained)
	}
	if explained.Result.PricingSetup != nil {
		t.Fatalf("pricing setup should be empty after default config is auto-detected: %+v", explained.Result.PricingSetup)
	}
	if got := explained.Result.Infrastructure.Totals[0].MonthlyUSD; got != 143.052 {
		t.Fatalf("monthly total from config = %v, want 143.052", got)
	}
}

func TestCostExplainHuman(t *testing.T) {
	specPath := writeServiceSpec(t, "medium", 12, 24)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--cpu-p95", "18",
		"--memory-p95", "41",
		"--warm-capacity", "8",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"cost advisor for payments-api/prod",
		"shape: medium",
		"cpu_p95: 18.0 %",
		"cost.shape.downsize",
		"cost.replicas.reduce_min",
		"recommendations are relative shape and capacity guidance, not billing truth",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestCostExplainHumanShowsPricingSetupCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	specPath := writeStatefulSpec(t)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain",
		"--file", specPath,
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"pricing: not estimated",
		"generate pricing config:",
		"skiff cost pricing update --region us-east-1 --out .skiff-pricing.json",
		"next run: rerun cost explain; Skiff will automatically load .skiff-pricing.json from the current directory",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestCostExplainRejectsInvalidMetricJSON(t *testing.T) {
	specPath := writeServiceSpec(t, "small", 1, 2)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--cpu-p95", "120",
		"--format", "json",
		"--trace-id", "tr_bad_cost",
	}, &stdout, &stderr)
	if code != cli.ExitUserError {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Summary string `json:"summary"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode error JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.TraceID != "tr_bad_cost" || !strings.Contains(got.Summary, "--cpu-p95") {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
}

func TestPlanJSONIncludesAdvisorWarnings(t *testing.T) {
	specPath := writeServiceSpec(t, "large", 8, 8)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"plan", specPath,
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
		"--format", "json",
		"--trace-id", "tr_plan_cost",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK              bool                  `json:"ok"`
		TraceID         string                `json:"trace_id"`
		AdvisorWarnings []cost.Recommendation `json:"advisor_warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_plan_cost" {
		t.Fatalf("unexpected plan envelope: %+v", got)
	}
	for _, id := range []string{"cost.plan.high_min_replicas", "cost.plan.large_shape", "cost.plan.fixed_warm_capacity"} {
		if !hasRecommendation(got.AdvisorWarnings, id) {
			t.Fatalf("missing advisor warning %s in %+v", id, got.AdvisorWarnings)
		}
	}
}

func writeServiceSpec(t *testing.T, machine string, min, max int) string {
	t.Helper()
	body := strings.ReplaceAll(serviceSpecTemplate, "{{MACHINE}}", machine)
	body = strings.ReplaceAll(body, "{{MIN}}", strconv.Itoa(min))
	body = strings.ReplaceAll(body, "{{MAX}}", strconv.Itoa(max))
	path := filepath.Join(t.TempDir(), "skiff.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func hasRecommendation(recs []cost.Recommendation, id string) bool {
	for _, rec := range recs {
		if rec.ID == id {
			return true
		}
	}
	return false
}

func findRecommendation(recs []cost.Recommendation, id string) *cost.Recommendation {
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

func writeAWSPricingFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aws-ec2-pricing.json")
	if err := os.WriteFile(path, []byte(awsPricingFixture), 0o600); err != nil {
		t.Fatalf("write pricing fixture: %v", err)
	}
	return path
}

func writeStatefulSpec(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skiff.yaml")
	if err := os.WriteFile(path, []byte(statefulSpec), 0o600); err != nil {
		t.Fatalf("write stateful spec: %v", err)
	}
	return path
}

func hasLineItem(items []cost.InfraLineItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

const serviceSpecTemplate = `apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
runtime:
  port: 8080
  command:
    - ./app
    - serve
  health:
    path: /healthz
machine:
  size: {{MACHINE}}
scale:
  min: {{MIN}}
  max: {{MAX}}
network:
  ingress:
    type: private
`

const awsPricingFixture = `{
  "publicationDate": "2026-05-14T21:07:47Z",
  "version": "20260514210747",
  "products": {
    "SKU_SMALL": {
      "productFamily": "Compute Instance",
      "attributes": {
        "instanceType": "t3.small",
        "operatingSystem": "Linux",
        "tenancy": "Shared",
        "preInstalledSw": "NA",
        "capacitystatus": "Used",
        "operation": "RunInstances",
        "vcpu": "2",
        "memory": "2 GiB"
      }
    },
    "SKU_MEDIUM": {
      "productFamily": "Compute Instance",
      "attributes": {
        "instanceType": "t3.medium",
        "operatingSystem": "Linux",
        "tenancy": "Shared",
        "preInstalledSw": "NA",
        "capacitystatus": "Used",
        "operation": "RunInstances",
        "vcpu": "2",
        "memory": "4 GiB"
      }
    },
    "SKU_GP3": {
      "productFamily": "Storage",
      "attributes": {
        "volumeApiName": "gp3",
        "usagetype": "EBS:VolumeUsage.gp3",
        "locationType": "AWS Region"
      }
    },
    "SKU_SNAPSHOT": {
      "productFamily": "Storage Snapshot",
      "attributes": {
        "usagetype": "EBS:SnapshotUsage",
        "locationType": "AWS Region"
      }
    }
  },
  "terms": {
    "OnDemand": {
      "SKU_SMALL": {
        "SKU_SMALL.OD": {
          "priceDimensions": {
            "SKU_SMALL.OD.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0208000000"}}
          }
        }
      },
      "SKU_MEDIUM": {
        "SKU_MEDIUM.OD": {
          "priceDimensions": {
            "SKU_MEDIUM.OD.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0416000000"}}
          }
        }
      },
      "SKU_GP3": {
        "SKU_GP3.OD": {
          "priceDimensions": {
            "SKU_GP3.OD.GBMO": {"unit": "GB-Mo", "description": "$0.08 per GB-month of General Purpose (gp3) provisioned storage", "pricePerUnit": {"USD": "0.0800000000"}}
          }
        }
      },
      "SKU_SNAPSHOT": {
        "SKU_SNAPSHOT.OD": {
          "priceDimensions": {
            "SKU_SNAPSHOT.OD.GBMO": {"unit": "GB-Mo", "description": "$0.05 per GB-Month of snapshot data stored", "pricePerUnit": {"USD": "0.0500000000"}}
          }
        }
      }
    },
    "Reserved": {
      "SKU_SMALL": {
        "SKU_SMALL.RI3ALL": {
          "termAttributes": {
            "LeaseContractLength": "3yr",
            "OfferingClass": "standard",
            "PurchaseOption": "All Upfront"
          },
          "priceDimensions": {
            "SKU_SMALL.RI3ALL.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0000000000"}},
            "SKU_SMALL.RI3ALL.QTY": {"unit": "Quantity", "pricePerUnit": {"USD": "206"}}
          }
        }
      },
      "SKU_MEDIUM": {
        "SKU_MEDIUM.RI3ALL": {
          "termAttributes": {
            "LeaseContractLength": "3yr",
            "OfferingClass": "standard",
            "PurchaseOption": "All Upfront"
          },
          "priceDimensions": {
            "SKU_MEDIUM.RI3ALL.HRS": {"unit": "Hrs", "pricePerUnit": {"USD": "0.0000000000"}},
            "SKU_MEDIUM.RI3ALL.QTY": {"unit": "Quantity", "pricePerUnit": {"USD": "411"}}
          }
        }
      }
    }
  }
}`

const statefulSpec = `apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: orders-stream
  env: prod
  labels:
    region: us-east-1
stateful:
  replicas: 3
  members:
    - ordinal: 0
      zone: us-east-1a
      dnsName: orders-stream-0.state.prod.internal.example.com
    - ordinal: 1
      zone: us-east-1b
      dnsName: orders-stream-1.state.prod.internal.example.com
    - ordinal: 2
      zone: us-east-1c
      dnsName: orders-stream-2.state.prod.internal.example.com
  volume:
    size: 250Gi
    type: gp3
    mountPath: /var/lib/nats
    encrypted: true
  identity:
    dnsZoneRef: route53://Z0123456789EXAMPLE/prod.internal.example.com
    hostnamePrefix: orders-stream
  recipe:
    name: nats-jetstream
    config:
      runtime:
        ports:
          client: 4222
          monitoring: 8222
        health:
          path: /healthz
          port: 8222
        metrics:
          path: /metrics
          port: 8222
      snapshots:
        enabled: true
        interval: 15m
        retention: 7d
  update:
    strategy: ordered
`
