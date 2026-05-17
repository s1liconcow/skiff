package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStatelessServiceReleaseGateFakeProvider(t *testing.T) {
	clearSkiffEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	const (
		service    = "http-hello"
		env        = "prod"
		oldRelease = "rel_http_old"
		newRelease = "rel_http_new"
		deployOp   = "op_http_deploy"
		rollbackOp = "op_http_rollback"
		traceID    = "tr_http_e2e"
	)
	seedStatelessService(t, store, service, env, oldRelease)

	resources := &statelessResourceManager{}
	rollouts := &statelessRolloutClient{}
	obs := statelessObservability{}
	oldDeployProvider := newDeployProvider
	oldRolloutProvider := newRolloutProvider
	oldRollbackProvider := newRollbackProvider
	oldLogsProvider := newLogsProvider
	oldMetricsProvider := newMetricsProviderForCLI
	newDeployProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return aws.NewFromConfig(cfg, aws.WithStateStore(store), aws.WithClients(aws.Clients{ServiceResources: resources, Rollouts: rollouts}))
	}
	newRolloutProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return aws.NewFromConfig(cfg, aws.WithStateStore(store), aws.WithClients(aws.Clients{Rollouts: rollouts}))
	}
	newRollbackProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return aws.NewFromConfig(cfg, aws.WithStateStore(store), aws.WithClients(aws.Clients{Rollouts: rollouts}))
	}
	newLogsProvider = func(cfg config.Config) (logsProvider, error) { return obs, nil }
	newMetricsProviderForCLI = func(cfg config.Config) (metricsProvider, error) { return obs, nil }
	t.Cleanup(func() {
		newDeployProvider = oldDeployProvider
		newRolloutProvider = oldRolloutProvider
		newRollbackProvider = oldRollbackProvider
		newLogsProvider = oldLogsProvider
		newMetricsProviderForCLI = oldMetricsProvider
	})

	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")
	stateURI := "file://" + dir
	signingSeed := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("H", ed25519.SeedSize)))

	runSkiff(t, "validate", specPath, "--format", "json", "--trace-id", traceID)

	var plan planOutput
	decodeJSON(t, runSkiff(t, "plan", specPath, "--provider", "aws", "--region", "us-west-2", "--state", stateURI, "--format", "json", "--trace-id", traceID), &plan)
	if !plan.OK || plan.Plan.Service != service || len(plan.Plan.Resources) == 0 {
		t.Fatalf("unexpected plan output: %+v", plan)
	}

	var deployed deployOutput
	decodeJSON(t, runSkiff(t,
		"deploy", specPath,
		"--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2",
		"--release-id", newRelease,
		"--operation-id", deployOp,
		"--signing-seed-base64", signingSeed,
		"--format", "json",
		"--trace-id", traceID,
	), &deployed)
	if !deployed.OK || !deployed.Result.OK || deployed.Result.ReleaseID != newRelease {
		t.Fatalf("unexpected deploy output: %+v", deployed)
	}
	afterDeploy := mustServiceControl(t, store, service)
	if afterDeploy.StableRelease != oldRelease || afterDeploy.DesiredRelease != newRelease {
		t.Fatalf("stable release changed before rollout success: %+v", afterDeploy)
	}

	rolloutProvider, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithStateStore(store), aws.WithClients(aws.Clients{Rollouts: rollouts}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (deploy.Deployer{Store: store, Provider: rolloutProvider}).StartRollout(ctx, deploy.StartRolloutRequest{
		Service:     service,
		Env:         env,
		OperationID: deployOp,
		ReleaseID:   newRelease,
		TraceID:     traceID,
		Actor:       schema.Actor{ID: "release-gate", Type: "agent"},
	}); err != nil {
		t.Fatalf("start rollout: %v", err)
	}

	var rollout rolloutWatchOutput
	decodeJSON(t, runSkiff(t,
		"rollout", "watch",
		"--service", service,
		"--operation", deployOp,
		"--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2",
		"--format", "json",
		"--trace-id", traceID,
	), &rollout)
	if !rollout.OK || rollout.Status.Status != "succeeded" {
		t.Fatalf("unexpected rollout output: %+v", rollout)
	}
	afterRollout := mustServiceControl(t, store, service)
	if afterRollout.StableRelease != newRelease || afterRollout.DesiredRelease != newRelease {
		t.Fatalf("stable release not updated after rollout success: %+v", afterRollout)
	}

	var status statusOutput
	decodeJSON(t, runSkiff(t, "status", service, "--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2", "--format", "json", "--trace-id", traceID), &status)
	if !status.OK || len(status.Status.Services) != 1 || status.Status.Services[0].Health != "nominal" {
		t.Fatalf("unexpected status output: %+v", status)
	}

	var logs logsOutput
	decodeJSON(t, runSkiff(t, "logs", service, "--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2", "--format", "json", "--trace-id", traceID), &logs)
	if !logs.OK || len(logs.Entries) == 0 {
		t.Fatalf("unexpected logs output: %+v", logs)
	}

	var metrics metricsOutput
	decodeJSON(t, runSkiff(t, "metrics", service, "--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2", "--format", "json", "--trace-id", traceID), &metrics)
	if !metrics.OK || len(metrics.Series) == 0 {
		t.Fatalf("unexpected metrics output: %+v", metrics)
	}

	var doctor doctorOutput
	decodeJSON(t, runSkiff(t, "doctor", service, "--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2", "--format", "json", "--trace-id", traceID), &doctor)
	if !doctor.OK {
		t.Fatalf("unexpected doctor output: %+v", doctor)
	}
	for _, finding := range doctor.Doctor.Findings {
		if finding.Severity == "critical" {
			t.Fatalf("doctor returned critical finding after success: %+v", doctor.Doctor.Findings)
		}
	}

	var rolledBack rollbackOutput
	decodeJSON(t, runSkiff(t,
		"rollback", service,
		"--to", oldRelease,
		"--operation-id", rollbackOp,
		"--saga-id", "saga_http_rollback",
		"--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", "us-west-2",
		"--format", "json",
		"--trace-id", traceID,
	), &rolledBack)
	if !rolledBack.OK || !rolledBack.Result.OK || rolledBack.Result.ToRelease != oldRelease {
		t.Fatalf("unexpected rollback output: %+v", rolledBack)
	}
	afterRollback := mustServiceControl(t, store, service)
	if afterRollback.DesiredRelease != oldRelease || afterRollback.StableRelease != oldRelease {
		t.Fatalf("rollback did not restore previous stable release: %+v", afterRollback)
	}

	assertGoldenEventTypes(t, store, service)
}

func runSkiff(t *testing.T, args ...string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run("skiff", args, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("skiff %s exit=%d stderr=%s stdout=%s", strings.Join(args, " "), code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("skiff %s stderr=%s", strings.Join(args, " "), stderr.String())
	}
	return stdout.Bytes()
}

func decodeJSON[T any](t *testing.T, body []byte, out *T) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, string(body))
	}
}

func seedStatelessService(t *testing.T, store objstore.ObjectStore, service, env, releaseID string) {
	t.Helper()
	control := schema.NewServiceControl(service, env, canonical.Time(statelessFlowNow()), schema.Actor{ID: "seed", Type: "agent"})
	control.DesiredRelease = releaseID
	control.StableRelease = releaseID
	if _, err := state.NewClient(store).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatalf("create service control: %v", err)
	}
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	releaseManifest := schema.ReleaseManifest{
		SchemaVersion:      schema.Version,
		Service:            service,
		Env:                env,
		ReleaseID:          releaseID,
		RuntimeManifestKey: runtimeKey,
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "registry.example.com/skiff/http-hello@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		CreatedAt: canonical.Time(statelessFlowNow()),
	}
	body, err := canonical.Marshal(releaseManifest)
	if err != nil {
		t.Fatal(err)
	}
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("create old release: %v", err)
	}
}

func mustServiceControl(t *testing.T, store objstore.ObjectStore, service string) schema.ServiceControl {
	t.Helper()
	doc, err := state.NewClient(store).GetServiceControl(context.Background(), service)
	if err != nil {
		t.Fatalf("get service control: %v", err)
	}
	return doc.Control
}

func assertGoldenEventTypes(t *testing.T, store objstore.ObjectStore, service string) {
	t.Helper()
	metas, err := store.List(context.Background(), "services/"+service+"/operations/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list operation events: %v", err)
	}
	var events []struct {
		ID   string `json:"id"`
		Time string `json:"time"`
		Type string `json:"type"`
	}
	for _, meta := range metas {
		if !strings.Contains(meta.Key, "/events/") {
			continue
		}
		obj, err := store.Get(context.Background(), meta.Key)
		if err != nil {
			t.Fatalf("get %s: %v", meta.Key, err)
		}
		var event struct {
			ID   string `json:"id"`
			Time string `json:"time"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(obj.Body, &event); err != nil {
			t.Fatalf("decode %s: %v", meta.Key, err)
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time == events[j].Time {
			return events[i].ID < events[j].ID
		}
		return events[i].Time < events[j].Time
	})
	actual := make([]string, 0, len(events))
	for _, event := range events {
		actual = append(actual, event.Type)
	}
	var golden struct {
		EventTypes []string `json:"event_types"`
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "events", "stateless_deploy.json"))
	if err != nil {
		t.Fatalf("read golden events: %v", err)
	}
	if err := json.Unmarshal(body, &golden); err != nil {
		t.Fatalf("decode golden events: %v", err)
	}
	if !reflect.DeepEqual(actual, golden.EventTypes) {
		t.Fatalf("event type sequence mismatch\nactual: %#v\nwant:   %#v", actual, golden.EventTypes)
	}
}

func statelessFlowNow() time.Time {
	return time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC)
}

type statelessResourceManager struct{}

func (m *statelessResourceManager) PlanResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.ResourcePlan, error) {
	return &aws.ResourcePlan{Action: provider.ActionCreate, Summary: "create " + desired.Summary}, nil
}

func (m *statelessResourceManager) ApplyResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.AppliedResource, error) {
	return &aws.AppliedResource{
		Kind:        desired.Kind,
		LogicalID:   desired.LogicalID,
		Name:        desired.Name,
		ProviderID:  desired.Kind + "/" + desired.Name,
		Status:      "configured",
		Tags:        desired.Tags,
		Fingerprint: desired.Fingerprint,
	}, nil
}

type statelessRolloutClient struct {
	starts int
}

func (c *statelessRolloutClient) StartInstanceRefresh(ctx context.Context, req aws.StartInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	c.starts++
	return &aws.InstanceRefresh{ID: "ir-stateless-" + req.ReleaseID, AutoScalingGroupName: req.AutoScalingGroupName, Status: "Pending", StartedAt: statelessFlowNow()}, nil
}

func (c *statelessRolloutClient) DescribeInstanceRefresh(ctx context.Context, req aws.DescribeInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	return &aws.InstanceRefresh{ID: req.InstanceRefreshID, AutoScalingGroupName: req.AutoScalingGroupName, Status: "Successful", UpdatedAt: statelessFlowNow()}, nil
}

type statelessObservability struct{}

func (statelessObservability) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return &provider.LogsResult{Entries: []provider.LogEntry{{
		Timestamp: statelessFlowNow(),
		Message:   "hello request served",
		Source:    "http-hello",
		Fields:    map[string]string{"service": req.Service, "release": req.ReleaseID},
	}}}, nil
}

func (statelessObservability) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	name := "http_requests_total"
	if len(req.Names) > 0 {
		name = req.Names[0]
	}
	return &provider.MetricsResult{Series: []provider.MetricSeries{{
		Name:   name,
		Source: "http-hello",
		Unit:   "Count",
		Points: []provider.MetricPoint{{Timestamp: statelessFlowNow(), Value: 1}},
	}}}, nil
}
