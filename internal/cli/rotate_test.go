package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRotateSecretJSONCreatesSagaAndStopsBeforePromotionApproval(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	oldProvider := newRotateProvider
	newRotateProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return fakeprovider.New(fakeprovider.WithStateStore(store)), nil
	}
	t.Cleanup(func() { newRotateProvider = oldProvider })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "secret", "secret://managed-database/orders-db/connection-url",
		"--consumers", "orders-api,orders-worker",
		"--canary-consumer", "orders-api",
		"--database", "orders-db",
		"--disable-after", "48h",
		"--direct",
		"--state", "file://" + dir,
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_cli",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("rotate output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_rotate_cli" || got.Result.Status != schema.SagaRunning || got.Result.NextAction != "approve_or_reject" {
		t.Fatalf("unexpected rotate output: %+v", got)
	}
	if len(got.Result.CurrentSteps) != 1 || got.Result.CurrentSteps[0] != "approve-promotion" {
		t.Fatalf("rotation should wait at promotion approval: %+v", got.Result.CurrentSteps)
	}
}

func TestRotateSecretDryRunJSONIncludesApprovalAndDelayedDisable(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "secret", "secret://app/api-key",
		"--consumers", "orders-api,orders-worker",
		"--dry-run",
		"--direct",
		"--state", "memory://rotate-dry-run",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_plan",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got rotateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.Result.Plan == nil {
		t.Fatalf("dry-run did not include plan")
	}
	var sawCanary, sawApproval, sawDelayedDisable bool
	for _, node := range got.Result.Plan.Graph.Nodes {
		switch node.ID {
		case "canary-consumer":
			sawCanary = node.Kind == "service.canary_with_secret"
		case "approve-promotion":
			sawApproval = node.Kind == "approval.manual"
		case "schedule-disable-old":
			sawDelayedDisable = node.Kind == "credential.disable_old"
		}
	}
	if !sawCanary || !sawApproval || !sawDelayedDisable {
		t.Fatalf("rotation plan missing canary, approval, or delayed disable: %+v", got.Result.Plan.Graph.Nodes)
	}
}

func TestRotateSecretProductionRequiresApproval(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rotate", "secret", "secret://app/api-key",
		"--consumers", "orders-api",
		"--direct",
		"--state", "memory://rotate-prod-approval",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_rotate_prod",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d; stderr = %s stdout = %s", code, ExitUserError, stderr.String(), stdout.String())
	}
	var got commandErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || !strings.Contains(got.Summary, "approval required") {
		t.Fatalf("unexpected approval error: %+v", got)
	}
}
