package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/file"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestSagaInspectJSONReadsDirectObjectState(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	sagas := sagastate.NewStore(store, sagastate.WithClock(func() time.Time {
		return time.Date(2026, 5, 16, 23, 55, 0, 0, time.UTC)
	}))
	if _, err := sagas.Create(context.Background(), sagastate.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Kind:          "rollback",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID:       "tr_saga",
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
			Summary:       "rollback payments-api",
			CreatedAt:     "2026-05-16T23:55:00Z",
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Nodes: []schema.SagaNode{{
				ID:            "point-release",
				Kind:          "service.point_release",
				Risk:          schema.RiskMedium,
				Reversibility: schema.Reversible,
			}},
			CreatedAt: "2026-05-16T23:55:00Z",
		},
		Control: schema.SagaControl{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Status:        schema.SagaRunning,
			CurrentSteps:  []string{"point-release"},
			UpdatedAt:     "2026-05-16T23:55:00Z",
			TraceID:       "tr_saga",
		},
	}); err != nil {
		t.Fatalf("create saga: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "inspect", "saga_01JABC",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_cli_saga",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got sagaInspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("saga inspect output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cli_saga" || got.Result.SagaID != "saga_01JABC" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Result.Status != schema.SagaRunning || got.Result.Risk != schema.RiskMedium || got.Result.Reversibility != schema.Reversible {
		t.Fatalf("unexpected saga summary: %+v", got.Result)
	}
	if len(got.Result.CurrentSteps) != 1 || got.Result.CurrentSteps[0] != "point-release" {
		t.Fatalf("current steps missing: %+v", got.Result.CurrentSteps)
	}
}

func TestSagaSkeletonCommandsReturnJSON(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "resume", "saga_01JABC",
		"--format", "json",
		"--trace-id", "tr_saga_skeleton",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got sagaCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("saga skeleton output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_saga_skeleton" || got.Command != "resume" || got.Saga != "saga_01JABC" || got.Implemented {
		t.Fatalf("unexpected skeleton output: %+v", got)
	}
}
