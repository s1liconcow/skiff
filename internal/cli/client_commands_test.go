package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRootGlobalFlagsApplyToVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"--format", "json",
		"--trace-id", "tr_root_version",
		"--no-color",
		"version",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("JSON output contains ANSI escapes: %q", stdout.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Binary  string `json:"binary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("version output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_root_version" || got.Binary != "skiff" {
		t.Fatalf("unexpected version envelope: %+v", got)
	}
}

func TestStatusDirectModeReadsFileObjectStateJSON(t *testing.T) {
	root := t.TempDir()
	writeStateObject(t, root, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_02",
		StableRelease:  "rel_01",
		Version:        1,
		UpdatedAt:      "2026-05-16T20:00:00Z",
		UpdatedBy:      schema.Actor{ID: "agent-one", Type: "agent"},
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"status",
		"--format", "json",
		"--trace-id", "tr_status",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Status  struct {
			Mode        string `json:"mode"`
			StateBucket string `json:"state_bucket"`
			Services    []struct {
				Service        string `json:"service"`
				DesiredRelease string `json:"desired_release"`
			} `json:"services"`
			Freshness struct {
				Source string `json:"source"`
				Ready  bool   `json:"ready"`
			} `json:"freshness"`
		} `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("status output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_status" || got.Status.Mode != "direct" {
		t.Fatalf("unexpected status envelope: %+v", got)
	}
	if got.Status.StateBucket != "file://"+root {
		t.Fatalf("state_bucket = %q", got.Status.StateBucket)
	}
	if len(got.Status.Services) != 1 || got.Status.Services[0].Service != "payments-api" || got.Status.Services[0].DesiredRelease != "rel_02" {
		t.Fatalf("unexpected services: %+v", got.Status.Services)
	}
	if got.Status.Freshness.Source != "direct_object_store" || !got.Status.Freshness.Ready {
		t.Fatalf("unexpected freshness: %+v", got.Status.Freshness)
	}
}

func TestEventsDirectModeReadsFileObjectStateJSON(t *testing.T) {
	root := t.TempDir()
	writeStateObject(t, root, "services/payments-api/events/01JROOT.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JROOT",
		Time:          "2026-05-16T20:01:00Z",
		TraceID:       "tr_event",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "service.updated",
		Severity:      "info",
		Summary:       "service control updated",
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"events",
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--scope", "service",
		"--service", "payments-api",
		"--format", "json",
		"--trace-id", "tr_events",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Result  struct {
			Source string `json:"source"`
			Events []struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Summary string `json:"summary"`
			} `json:"events"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("events output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_events" || got.Result.Source != "direct" {
		t.Fatalf("unexpected events envelope: %+v", got)
	}
	if len(got.Result.Events) != 1 || got.Result.Events[0].ID != "01JROOT" || got.Result.Events[0].Summary != "service control updated" {
		t.Fatalf("unexpected events: %+v", got.Result.Events)
	}
}

func TestEventsWatchDirectModeEmitsJSONAndStopsOnContext(t *testing.T) {
	root := t.TempDir()
	writeStateObject(t, root, "services/payments-api/events/01JWATCH.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JWATCH",
		Time:          "2026-05-16T20:02:00Z",
		TraceID:       "tr_watch_event",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "service.updated",
		Severity:      "info",
		Summary:       "service control updated",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	oldContext := eventsWatchContext
	oldInterval := eventsWatchPollInterval
	eventsWatchContext = func() context.Context { return ctx }
	eventsWatchPollInterval = time.Hour
	t.Cleanup(func() {
		eventsWatchContext = oldContext
		eventsWatchPollInterval = oldInterval
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"events",
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--scope", "service",
		"--service", "payments-api",
		"--watch",
		"--format", "json",
		"--trace-id", "tr_events_watch",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got eventWatchOutput
	if err := json.Unmarshal(bytes.Split(stdout.Bytes(), []byte("\n"))[0], &got); err != nil {
		t.Fatalf("watch output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_events_watch" || got.Event == nil || got.Event.ID != "01JWATCH" || got.LastEventID != "01JWATCH" {
		t.Fatalf("unexpected watch output: %+v", got)
	}
}

func TestOpsWatchDirectModeEmitsOperationEvents(t *testing.T) {
	root := t.TempDir()
	writeStateObject(t, root, "services/payments-api/operations/op_01/events/01JOP.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JOP",
		Time:          "2026-05-16T20:04:00Z",
		TraceID:       "tr_op_event",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "operation.step",
		Severity:      "info",
		Summary:       "operation step completed",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	oldContext := eventsWatchContext
	oldInterval := eventsWatchPollInterval
	eventsWatchContext = func() context.Context { return ctx }
	eventsWatchPollInterval = time.Hour
	t.Cleanup(func() {
		eventsWatchContext = oldContext
		eventsWatchPollInterval = oldInterval
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "watch", "op_01",
		"--service", "payments-api",
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_ops_watch",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got eventWatchOutput
	if err := json.Unmarshal(bytes.Split(stdout.Bytes(), []byte("\n"))[0], &got); err != nil {
		t.Fatalf("ops watch output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.Event == nil || got.Event.ID != "01JOP" || got.LastEventID != "01JOP" {
		t.Fatalf("unexpected ops watch output: %+v", got)
	}
}

func TestStatusJSONConfigErrorIsAgentSafe(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"status",
		"--direct",
		"--format", "json",
		"--trace-id", "tr_bad_status",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("JSON output contains ANSI escapes: %q", stdout.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "CONFIG_INVALID" || got.TraceID != "tr_bad_status" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
}

func TestCompletionGeneration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"completion", "bash"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "complete -F _skiff_completion skiff") {
		t.Fatalf("unexpected completion script: %s", stdout.String())
	}
}

func TestExplainAWSJSON(t *testing.T) {
	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"explain",
		specPath,
		"--format", "json",
		"--trace-id", "tr_explain",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
		"--release-id", "rel_01JABC",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Result  struct {
			Provider  string `json:"provider"`
			Service   string `json:"service"`
			Resources []struct {
				CloudPrimitive string `json:"cloud_primitive"`
				Why            string `json:"why"`
			} `json:"resources"`
		} `json:"result"`
		AWS struct {
			LaunchTemplates []struct {
				UserData string `json:"user_data"`
			} `json:"launch_templates"`
		} `json:"aws"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("explain output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_explain" || got.Result.Provider != "aws" || got.Result.Service != "payments-api" {
		t.Fatalf("unexpected explain envelope: %+v", got)
	}
	primitives := map[string]bool{}
	for _, resource := range got.Result.Resources {
		primitives[resource.CloudPrimitive] = true
		if resource.Why == "" {
			t.Fatalf("resource missing why text: %+v", resource)
		}
	}
	for _, want := range []string{"IAM role", "EC2 launch template", "Auto Scaling Group", "load balancer target group", "load balancer listener rule", "EC2 security group", "CloudWatch log group"} {
		if !primitives[want] {
			t.Fatalf("missing primitive %q in %+v", want, primitives)
		}
	}
	if len(got.AWS.LaunchTemplates) != 1 || !strings.Contains(got.AWS.LaunchTemplates[0].UserData, `"state_bucket":"s3://skiff-state-prod"`) {
		t.Fatalf("explain output missing runner user-data state bucket: %+v", got.AWS.LaunchTemplates)
	}
}

func TestPlanAWSJSON(t *testing.T) {
	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"plan",
		specPath,
		"--format", "json",
		"--trace-id", "tr_plan",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Plan    struct {
			Provider  string `json:"provider"`
			Service   string `json:"service"`
			Resources []struct {
				Action      string          `json:"action"`
				Kind        string          `json:"kind"`
				Name        string          `json:"name"`
				Fingerprint string          `json:"fingerprint"`
				Desired     json.RawMessage `json:"desired"`
			} `json:"resources"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("plan output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_plan" || got.Plan.Provider != "aws" || got.Plan.Service != "payments-api" {
		t.Fatalf("unexpected plan envelope: %+v", got)
	}
	foundASG := false
	for _, resource := range got.Plan.Resources {
		if resource.Kind == "autoscaling-group" {
			foundASG = true
		}
		if resource.Action != "create" || resource.Name == "" || resource.Fingerprint == "" || len(resource.Desired) == 0 {
			t.Fatalf("invalid planned resource: %+v", resource)
		}
	}
	if !foundASG {
		t.Fatalf("plan missing Auto Scaling Group: %+v", got.Plan.Resources)
	}
}

func TestDeployDryRunJSON(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy",
		specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--dry-run",
		"--format", "json",
		"--trace-id", "tr_deploy_dry",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Result  struct {
			OK          bool   `json:"ok"`
			DryRun      bool   `json:"dry_run"`
			OperationID string `json:"operation_id"`
			ReleaseID   string `json:"release_id"`
			Plan        struct {
				Resources []struct {
					Action string `json:"action"`
					Kind   string `json:"kind"`
				} `json:"resources"`
			} `json:"plan"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("deploy output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_deploy_dry" || !got.Result.OK || !got.Result.DryRun || got.Result.OperationID == "" || got.Result.ReleaseID == "" {
		t.Fatalf("unexpected deploy dry-run envelope: %+v", got)
	}
	if len(got.Result.Plan.Resources) == 0 {
		t.Fatalf("dry-run deploy did not include plan resources")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry-run deploy wrote files under state root: %+v", entries)
	}
}

func TestDeployCanaryJSONCreatesAndRunsSaga(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	writeStateObject(t, root, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_old",
		StableRelease:  "rel_old",
		Version:        1,
		UpdatedAt:      "2026-05-17T03:30:00Z",
		UpdatedBy:      schema.Actor{ID: "agent-one", Type: "agent"},
	})
	fake := &fakeCanaryCLIProvider{}
	oldProvider := newSagaProvider
	newSagaProvider = func(config.Config, objstore.ObjectStore) (provider.Provider, error) {
		return fake, nil
	}
	defer func() { newSagaProvider = oldProvider }()

	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy",
		specPath,
		"--canary",
		"--release-id", "rel_new",
		"--canary-stages", "100",
		"--canary-bake", "0s",
		"--signing-seed-base64", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("D", ed25519.SeedSize))),
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_deploy_canary",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got canarySagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("deploy canary output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_deploy_canary" || got.Result.Status != schema.SagaSucceeded || got.Result.Stage != 100 {
		t.Fatalf("unexpected deploy canary output: %+v", got)
	}
	if len(fake.rollouts) != 1 || fake.rollouts[0].ReleaseID != "rel_new" {
		t.Fatalf("canary rollout was not started: %+v", fake.rollouts)
	}
}

func writeStateObject(t *testing.T, root, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	path := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
