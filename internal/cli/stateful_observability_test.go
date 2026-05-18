package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/agent"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
	stateruntime "github.com/s1liconcow/skiff/internal/stateful"
)

func TestStatefulStatusDoctorSolveDirectJSON(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedStatefulObservabilityControls(t, store, state.StatefulMemberFailed, "", "vol-0")
	seedStatefulBackupRecord(t, store, "backup_old", "2026-05-16T12:00:00Z")

	var statusOut, statusErr bytes.Buffer
	code := Run("skiff", []string{
		"stateful", "status", "orders-stream",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_stateful_status",
	}, &statusOut, &statusErr)
	if code != ExitSuccess {
		t.Fatalf("status exit code = %d, stderr = %s, stdout = %s", code, statusErr.String(), statusOut.String())
	}
	var status statefulStatusOutput
	if err := json.Unmarshal(statusOut.Bytes(), &status); err != nil {
		t.Fatalf("stateful status output is not valid JSON: %v\n%s", err, statusOut.String())
	}
	if !status.OK || status.TraceID != "tr_stateful_status" || status.Result.Group != "orders-stream" || status.Result.Health != "degraded" {
		t.Fatalf("unexpected status output: %+v", status)
	}
	if len(status.Result.Members) != 1 || status.Result.Members[0].Health != "degraded" || len(status.Result.Backups) != 1 || !status.Result.Backups[0].Stale {
		t.Fatalf("stateful status missing member/backup detail: %+v", status.Result)
	}

	var doctorOut, doctorErr bytes.Buffer
	code = Run("skiff", []string{
		"stateful", "doctor", "orders-stream",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_stateful_doctor_cli",
	}, &doctorOut, &doctorErr)
	if code != ExitSuccess {
		t.Fatalf("doctor exit code = %d, stderr = %s, stdout = %s", code, doctorErr.String(), doctorOut.String())
	}
	var diagnosis statefulDoctorOutput
	if err := json.Unmarshal(doctorOut.Bytes(), &diagnosis); err != nil {
		t.Fatalf("stateful doctor output is not valid JSON: %v\n%s", err, doctorOut.String())
	}
	if !diagnosis.OK || diagnosis.TraceID != "tr_stateful_doctor_cli" || diagnosis.Doctor.Health != "degraded" {
		t.Fatalf("unexpected doctor output: %+v", diagnosis)
	}
	if !doctorHasFinding(diagnosis, "STATEFUL_MEMBER_NOT_READY") || !doctorHasAction(diagnosis, "orders-stream_stateful_replace_member", true) || !doctorHasAction(diagnosis, "orders-stream_stateful_snapshot_member", true) {
		t.Fatalf("stateful doctor missing findings/actions: %+v", diagnosis.Doctor)
	}

	var solveOut, solveErr bytes.Buffer
	code = Run("skiff", []string{
		"stateful", "solve", "orders-stream",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_stateful_solve_cli",
	}, &solveOut, &solveErr)
	if code != ExitSuccess {
		t.Fatalf("solve exit code = %d, stderr = %s, stdout = %s", code, solveErr.String(), solveOut.String())
	}
	var graph statefulSolveOutput
	if err := json.Unmarshal(solveOut.Bytes(), &graph); err != nil {
		t.Fatalf("stateful solve output is not valid JSON: %v\n%s", err, solveOut.String())
	}
	if !graph.OK || graph.TraceID != "tr_stateful_solve_cli" || graph.Status != agent.StatusApprovalRequired {
		t.Fatalf("unexpected solve graph: %+v", graph)
	}
	replace := actionStepByOperation(graph, "stateful.replace_member")
	if replace == nil || replace.APIOperation.Target.Kind != "stateful-member" || replace.APIOperation.Params["member"] != "0" || !replace.RequiresApproval || strings.Contains(replace.Command, "--yes") {
		t.Fatalf("stateful solve replacement step = %+v", replace)
	}
}

func TestStatefulLogsAndMetricsUseMemberControlProviderID(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedStatefulObservabilityControls(t, store, state.StatefulMemberReady, "i-member-0", "vol-0")
	now := time.Date(2026, 5, 17, 2, 0, 0, 0, time.UTC)
	logs := &fakeLogsProvider{result: &provider.LogsResult{Entries: []provider.LogEntry{{
		Timestamp: now,
		Source:    "i-member-0/stdout",
		Message:   "ready",
	}}}}
	restoreLogsTestHooks(t, logs)
	metrics := &fakeMetricsProvider{result: &provider.MetricsResult{Series: []provider.MetricSeries{{
		Name:   aws.MetricInstanceCPUUtilization,
		Source: "i-member-0",
		Points: []provider.MetricPoint{{Timestamp: now, Value: 12}},
	}}}}
	restoreMetricsTestHooks(t, metrics)

	var logsOut, logsErr bytes.Buffer
	code := Run("skiff", []string{
		"stateful", "logs", "orders-stream",
		"--member", "0",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--since", "2026-05-17T01:45:00Z",
		"--format", "json",
		"--trace-id", "tr_stateful_logs",
	}, &logsOut, &logsErr)
	if code != ExitSuccess {
		t.Fatalf("logs exit code = %d, stderr = %s, stdout = %s", code, logsErr.String(), logsOut.String())
	}
	if logs.req.Service != "orders-stream" || logs.req.Env != "prod" || logs.req.InstanceID != "i-member-0" {
		t.Fatalf("unexpected stateful logs provider request: %+v", logs.req)
	}
	var logsJSON logsOutput
	if err := json.Unmarshal(logsOut.Bytes(), &logsJSON); err != nil {
		t.Fatalf("stateful logs output is not valid JSON: %v\n%s", err, logsOut.String())
	}
	if !logsJSON.OK || logsJSON.TraceID != "tr_stateful_logs" || len(logsJSON.Entries) != 1 {
		t.Fatalf("unexpected stateful logs output: %+v", logsJSON)
	}

	var metricsOut, metricsErr bytes.Buffer
	code = Run("skiff", []string{
		"stateful", "metrics", "orders-stream",
		"--member", "0",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--metric", aws.MetricInstanceCPUUtilization,
		"--from", "2026-05-17T01:45:00Z",
		"--to", "2026-05-17T02:00:00Z",
		"--format", "json",
		"--trace-id", "tr_stateful_metrics",
	}, &metricsOut, &metricsErr)
	if code != ExitSuccess {
		t.Fatalf("metrics exit code = %d, stderr = %s, stdout = %s", code, metricsErr.String(), metricsOut.String())
	}
	if metrics.req.Service != "orders-stream" || metrics.req.Env != "prod" || metrics.req.InstanceID != "i-member-0" || len(metrics.req.Names) != 1 || metrics.req.Names[0] != aws.MetricInstanceCPUUtilization {
		t.Fatalf("unexpected stateful metrics provider request: %+v", metrics.req)
	}
	var metricsJSON metricsOutput
	if err := json.Unmarshal(metricsOut.Bytes(), &metricsJSON); err != nil {
		t.Fatalf("stateful metrics output is not valid JSON: %v\n%s", err, metricsOut.String())
	}
	if !metricsJSON.OK || metricsJSON.TraceID != "tr_stateful_metrics" || len(metricsJSON.Series) != 1 || metricsJSON.Series[0].Points[0].Value != 12 {
		t.Fatalf("unexpected stateful metrics output: %+v", metricsJSON)
	}
}

func seedStatefulObservabilityControls(t *testing.T, store objstore.ObjectStore, phase, instanceID, volumeID string) {
	t.Helper()
	client := state.NewClient(store)
	if _, err := client.CreateStatefulGroupControl(context.Background(), schema.StatefulGroupControl{
		Group: "orders-stream",
		Env:   "prod",
		Members: []schema.StatefulMemberSummary{{
			Member:     0,
			Generation: 1,
			InstanceID: instanceID,
			VolumeID:   volumeID,
			DNSName:    "",
			Phase:      phase,
		}},
		Replicas:  1,
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := client.CreateStatefulMemberControl(context.Background(), schema.StatefulMemberControl{
		Group:      "orders-stream",
		Env:        "prod",
		Member:     0,
		Zone:       "us-west-2a",
		InstanceID: instanceID,
		VolumeID:   volumeID,
		DNSName:    "",
		Generation: 1,
		Phase:      phase,
		UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
}

func seedStatefulBackupRecord(t *testing.T, store objstore.ObjectStore, backupID, expiresAt string) {
	t.Helper()
	body, err := canonical.Marshal(stateruntime.BackupRecord{
		SchemaVersion: schema.Version,
		BackupID:      backupID,
		Group:         "orders-stream",
		Env:           "prod",
		Member:        0,
		VolumeID:      "vol-0",
		SnapshotID:    "snap-old",
		Provider:      "aws",
		ProviderID:    "snap-old",
		ProviderOperation: schema.ProviderOperationRef{
			Provider:   "aws",
			Kind:       "ec2-create-snapshot",
			ID:         "snapshot/snap-old",
			ObservedAt: "2026-05-16T00:00:00Z",
		},
		Status:    stateruntime.BackupStatusAvailable,
		CreatedAt: "2026-05-16T00:00:00Z",
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("marshal backup record: %v", err)
	}
	if _, err := store.Create(context.Background(), "stateful/orders-stream/backups/"+backupID+"/record.json", body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("create backup record: %v", err)
	}
}

func doctorHasFinding(output statefulDoctorOutput, code string) bool {
	for _, finding := range output.Doctor.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func doctorHasAction(output statefulDoctorOutput, id string, mutating bool) bool {
	for _, action := range output.Doctor.RecommendedActions {
		if action.ID == id && action.Mutating == mutating {
			return true
		}
	}
	return false
}

func actionStepByOperation(output statefulSolveOutput, operation string) *agent.ActionStep {
	for i := range output.Steps {
		step := &output.Steps[i]
		if step.APIOperation != nil && step.APIOperation.Operation == operation {
			return step
		}
	}
	return nil
}
