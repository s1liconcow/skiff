package cost

import (
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/ir"
)

func TestAnalyzeOverprovisionedService(t *testing.T) {
	cpu := 18.0
	mem := 41.0
	warm := 8
	result := Analyze(Input{
		Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "medium", MinReplicas: 12, MaxReplicas: 24, LogsEnabled: true, MetricsEnabled: true},
		Signals: ObservedSignals{
			CPUP95Percent:    &cpu,
			MemoryP95Percent: &mem,
			WarmCapacity:     &warm,
		},
	})
	assertRecommendation(t, result.Recommendations, "cost.shape.downsize", ConfidenceHigh)
	assertRecommendation(t, result.Recommendations, "cost.replicas.reduce_min", ConfidenceMedium)
}

func TestAnalyzeUnderprovisionedService(t *testing.T) {
	cpu := 86.0
	mem := 68.0
	unhealthy := 2
	result := Analyze(Input{
		Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "small", MinReplicas: 3, MaxReplicas: 3},
		Signals: ObservedSignals{
			CPUP95Percent:    &cpu,
			MemoryP95Percent: &mem,
			UnhealthyTargets: &unhealthy,
		},
	})
	assertRecommendation(t, result.Recommendations, "cost.shape.upsize", ConfidenceHigh)
	assertRecommendation(t, result.Recommendations, "cost.warm_capacity.add_headroom", ConfidenceMedium)
}

func TestAnalyzeNoisyLogs(t *testing.T) {
	logs := 1400.0
	result := Analyze(Input{
		Shape:   ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "small", MinReplicas: 2, MaxReplicas: 4},
		Signals: ObservedSignals{LogMegabytesPerHour: &logs},
	})
	rec := findRecommendation(result.Recommendations, "cost.logs.noisy")
	if rec == nil {
		t.Fatalf("missing noisy log recommendation: %+v", result.Recommendations)
	}
	if rec.Severity != SeverityMedium || rec.Confidence != ConfidenceHigh {
		t.Fatalf("unexpected log recommendation: %+v", *rec)
	}
}

func TestPlanWarningsFlagExpensiveDefaults(t *testing.T) {
	warnings := PlanWarnings(Input{Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "large", MinReplicas: 8, MaxReplicas: 8}})
	assertRecommendation(t, warnings, "cost.plan.high_min_replicas", ConfidenceLow)
	assertRecommendation(t, warnings, "cost.plan.large_shape", ConfidenceLow)
	assertRecommendation(t, warnings, "cost.plan.fixed_warm_capacity", ConfidenceLow)
}

func TestAnalyzeWithPricingAddsEC2Estimates(t *testing.T) {
	cpu := 18.0
	mem := 41.0
	warm := 8
	result, err := AnalyzeWithPricing(Input{
		Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "medium", MinReplicas: 12, MaxReplicas: 24},
		Signals: ObservedSignals{
			CPUP95Percent:    &cpu,
			MemoryP95Percent: &mem,
			WarmCapacity:     &warm,
		},
	}, fakePricingCatalog(), PricingOptions{MonthlyHours: 730})
	if err != nil {
		t.Fatalf("AnalyzeWithPricing: %v", err)
	}
	if result.Pricing == nil {
		t.Fatalf("missing pricing estimate")
	}
	if result.Pricing.InstanceType != "t3.medium" || len(result.Pricing.Schemes) != 2 {
		t.Fatalf("unexpected pricing estimate: %+v", result.Pricing)
	}
	if got, want := result.Pricing.Schemes[0].MinMonthlyUSD, 364.416; got != want {
		t.Fatalf("min monthly = %v, want %v", got, want)
	}
	rec := findRecommendation(result.Recommendations, "cost.shape.downsize")
	if rec == nil || !strings.Contains(rec.EstimatedImpact, "on_demand saves about $182.21/month") {
		t.Fatalf("downsize impact was not priced: %+v", rec)
	}
}

func TestEstimateInfrastructureIncludesStatefulStorageAndSupport(t *testing.T) {
	graph := &ir.Graph{
		Service: "orders-stream",
		Env:     "prod",
		Resources: ir.Resources{
			StatefulGroups: []ir.StatefulGroup{{Replicas: 3}},
			StatefulMembers: []ir.StatefulMember{
				{Ordinal: 0},
				{Ordinal: 1},
				{Ordinal: 2},
			},
			StatefulVolumes: []ir.StatefulVolume{
				{Size: "250Gi", Type: "gp3"},
				{Size: "250Gi", Type: "gp3"},
				{Size: "250Gi", Type: "gp3"},
			},
			StatefulDNS: []ir.StatefulDNS{
				{DNSName: "orders-stream-0.example.com"},
				{DNSName: "orders-stream-1.example.com"},
				{DNSName: "orders-stream-2.example.com"},
			},
			StatefulRecipes:  []ir.StatefulRecipe{{Metrics: ir.AppMetrics{Enabled: true}, HealthCheck: ir.HealthCheck{Port: 8222}}},
			SnapshotPolicies: []ir.SnapshotPolicy{{Enabled: true}},
		},
	}
	estimate := EstimateInfrastructure(graph, fakePricingCatalog(), PricingOptions{MonthlyHours: 730})
	for _, id := range []string{"compute.instances.on_demand", "storage.ebs.gp3", "storage.ebs_snapshots", "network.route53_records", "observability.logs", "observability.metrics"} {
		if !hasInfraLineItem(estimate.LineItems, id) {
			t.Fatalf("missing line item %s in %+v", id, estimate.LineItems)
		}
	}
	if len(estimate.Totals) == 0 || estimate.Totals[0].MonthlyUSD != 143.052 {
		t.Fatalf("unexpected totals: %+v", estimate.Totals)
	}
	if len(estimate.Scenarios) != 3 {
		t.Fatalf("scenarios = %+v", estimate.Scenarios)
	}
	if estimate.Scenarios[0].Name != "low" || estimate.Scenarios[0].SnapshotDataGB != 187.5 || estimate.Scenarios[0].Totals[0].MonthlyUSD != 114.927 {
		t.Fatalf("unexpected low scenario: %+v", estimate.Scenarios[0])
	}
	if estimate.Scenarios[2].Name != "high" || estimate.Scenarios[2].Totals[0].MonthlyUSD != 143.052 {
		t.Fatalf("unexpected high scenario: %+v", estimate.Scenarios[2])
	}
}

func TestEstimateInfrastructureIncludesManagedDatabase(t *testing.T) {
	graph := &ir.Graph{
		Service: "orders",
		Env:     "prod",
		Resources: ir.Resources{
			ManagedDatabases: []ir.ManagedDatabase{
				{
					Meta:    ir.ResourceMeta{LogicalID: "managed-database:orders-db", Name: "skiff-prod-orders-db"},
					Engine:  "postgres",
					Size:    "small",
					Storage: ir.DatabaseStorage{SizeGB: 20, Type: "gp3", Encrypted: true},
					Backups: ir.DatabaseBackups{Enabled: true, RetentionDays: 7},
				},
			},
		},
	}
	estimate := EstimateInfrastructure(graph, fakePricingCatalog(), PricingOptions{MonthlyHours: 730})
	for _, id := range []string{"database.rds_instance.on_demand.managed-database.orders-db", "database.rds_storage.managed-database.orders-db", "database.rds_backups.managed-database.orders-db"} {
		if !hasInfraLineItem(estimate.LineItems, id) {
			t.Fatalf("missing line item %s in %+v", id, estimate.LineItems)
		}
	}
	if len(estimate.Totals) == 0 || estimate.Totals[0].MonthlyUSD != 13.98 {
		t.Fatalf("unexpected totals: %+v", estimate.Totals)
	}
}

func assertRecommendation(t *testing.T, recs []Recommendation, id, confidence string) {
	t.Helper()
	rec := findRecommendation(recs, id)
	if rec == nil {
		t.Fatalf("missing recommendation %s in %+v", id, recs)
	}
	if rec.Confidence != confidence {
		t.Fatalf("%s confidence = %q, want %q", id, rec.Confidence, confidence)
	}
	if rec.Summary == "" || len(rec.Evidence) == 0 {
		t.Fatalf("%s lacks summary or evidence: %+v", id, *rec)
	}
}

func hasInfraLineItem(items []InfraLineItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func findRecommendation(recs []Recommendation, id string) *Recommendation {
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

func fakePricingCatalog() PricingCatalog {
	return PricingCatalog{
		Provider:        "aws",
		Region:          "us-east-1",
		Currency:        "USD",
		PublicationDate: "2026-05-14T21:07:47Z",
		Items: []InstancePricing{
			{
				MachineSize:  "small",
				InstanceType: "t3.small",
				VCPU:         2,
				MemoryGB:     2,
				Rates: []PricingRate{
					{Scheme: PricingSchemeOnDemand, Summary: "On-Demand", Currency: "USD", HourlyUSD: 0.0208, EffectiveHourlyUSD: 0.0208},
					{Scheme: PricingSchemeRI3yrStandardAllUpfront, Summary: "3yr Standard RI All Upfront", Currency: "USD", UpfrontUSD: 206, EffectiveHourlyUSD: 206.0 / 26280.0, TermHours: 26280},
				},
			},
			{
				MachineSize:  "medium",
				InstanceType: "t3.medium",
				VCPU:         2,
				MemoryGB:     4,
				Rates: []PricingRate{
					{Scheme: PricingSchemeOnDemand, Summary: "On-Demand", Currency: "USD", HourlyUSD: 0.0416, EffectiveHourlyUSD: 0.0416},
					{Scheme: PricingSchemeRI3yrStandardAllUpfront, Summary: "3yr Standard RI All Upfront", Currency: "USD", UpfrontUSD: 411, EffectiveHourlyUSD: 411.0 / 26280.0, TermHours: 26280},
				},
			},
		},
		StorageRates: []StoragePricing{
			{Kind: StorageKindEBSVolumeGBMonth, ResourceType: "gp3", Unit: "GB-Mo", UnitPriceUSD: 0.08},
			{Kind: StorageKindEBSSnapshotGBMonth, Unit: "GB-Mo", UnitPriceUSD: 0.05},
			{Kind: StorageKindRDSVolumeGBMonth, Engine: "postgres", ResourceType: "gp3", Unit: "GB-Mo", UnitPriceUSD: 0.115},
			{Kind: StorageKindRDSBackupGBMonth, Engine: "postgres", ResourceType: "backup", Unit: "GB-Mo", UnitPriceUSD: 0.095},
		},
		DatabaseItems: []DatabaseInstancePricing{
			{
				Engine:           "postgres",
				Size:             "small",
				InstanceClass:    "db.t4g.micro",
				DeploymentOption: "Single-AZ",
				VCPU:             2,
				MemoryGB:         1,
				Rates: []PricingRate{
					{Scheme: PricingSchemeOnDemand, Summary: "On-Demand", Currency: "USD", HourlyUSD: 0.016, EffectiveHourlyUSD: 0.016},
					{Scheme: PricingSchemeRI3yrStandardAllUpfront, Summary: "3yr Standard RI All Upfront", Currency: "USD", UpfrontUSD: 199, EffectiveHourlyUSD: 199.0 / 26280.0, TermHours: 26280},
				},
			},
		},
	}
}
