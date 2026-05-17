package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRollbackJSONDirectMode(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedCLIRollbackState(t, store)

	oldProvider := newRollbackProvider
	defer func() { newRollbackProvider = oldProvider }()
	newRollbackProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return aws.NewFromConfig(cfg, aws.WithStateStore(store), aws.WithClients(aws.Clients{Rollouts: &cliFakeRolloutClient{
			start:    &aws.InstanceRefresh{ID: "ir-cli", Status: "Pending", StartedAt: cliRollbackNow()},
			describe: &aws.InstanceRefresh{ID: "ir-cli", Status: "Successful", UpdatedAt: cliRollbackNow()},
		}}))
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rollback", "payments-api",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--to", "previous-stable",
		"--operation-id", "op_cli_rollback",
		"--saga-id", "saga_cli_rollback",
		"--format", "json",
		"--trace-id", "tr_cli_rollback",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got rollbackOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("rollback output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cli_rollback" || !got.Result.OK || got.Result.ToRelease != "rel_good" {
		t.Fatalf("unexpected rollback output: %+v", got)
	}
	control, err := state.NewClient(store).GetServiceControl(context.Background(), "payments-api")
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.DesiredRelease != "rel_good" || control.Control.StableRelease != "rel_good" {
		t.Fatalf("service control not rolled back: %+v", control.Control)
	}
}

func TestRollbackFailedJSONIncludesRecommendedActions(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedCLIRollbackState(t, store)

	oldProvider := newRollbackProvider
	defer func() { newRollbackProvider = oldProvider }()
	newRollbackProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return aws.NewFromConfig(cfg, aws.WithStateStore(store), aws.WithClients(aws.Clients{Rollouts: &cliFakeRolloutClient{
			start:    &aws.InstanceRefresh{ID: "ir-cli-failed", Status: "Pending", StartedAt: cliRollbackNow()},
			describe: &aws.InstanceRefresh{ID: "ir-cli-failed", Status: "Failed", UpdatedAt: cliRollbackNow()},
		}}))
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rollback", "payments-api",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--operation-id", "op_cli_rollback_failed",
		"--saga-id", "saga_cli_rollback_failed",
		"--format", "json",
		"--trace-id", "tr_cli_rollback_failed",
	}, &stdout, &stderr)
	if code != ExitRolloutFailed {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got rollbackErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("rollback error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "ROLLBACK_FAILED" || got.TraceID != "tr_cli_rollback_failed" || got.Result == nil || got.Result.OK {
		t.Fatalf("unexpected rollback error output: %+v", got)
	}
	if len(got.RecommendedActions) == 0 {
		t.Fatalf("missing recommended actions: %+v", got)
	}
	for _, action := range got.RecommendedActions {
		if action.Command == "" || action.Mutating {
			t.Fatalf("recommended action should be non-mutating with command: %+v", action)
		}
	}
}

type cliFakeRolloutClient struct {
	start       *aws.InstanceRefresh
	describe    *aws.InstanceRefresh
	describeReq aws.DescribeInstanceRefreshRequest
}

func (c *cliFakeRolloutClient) StartInstanceRefresh(ctx context.Context, req aws.StartInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	return c.start, nil
}

func (c *cliFakeRolloutClient) DescribeInstanceRefresh(ctx context.Context, req aws.DescribeInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	c.describeReq = req
	return c.describe, nil
}

func seedCLIRollbackState(t *testing.T, store objstore.ObjectStore) {
	t.Helper()
	control := schema.NewServiceControl("payments-api", "prod", canonical.Time(cliRollbackNow()), schema.Actor{ID: "seed", Type: "agent"})
	control.DesiredRelease = "rel_bad"
	control.StableRelease = "rel_good"
	if _, err := state.NewClient(store).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatal(err)
	}
	runtimeKey, err := paths.RuntimeManifest("payments-api", "rel_good")
	if err != nil {
		t.Fatal(err)
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion:      schema.Version,
		Service:            "payments-api",
		Env:                "prod",
		ReleaseID:          "rel_good",
		RuntimeManifestKey: runtimeKey,
		CreatedAt:          canonical.Time(cliRollbackNow()),
	}
	body, err := canonical.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	key, err := paths.ReleaseManifest("payments-api", "rel_good")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func cliRollbackNow() time.Time {
	return time.Date(2026, 5, 17, 0, 20, 0, 0, time.UTC)
}
