package readiness_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStatefulGroupFakeProviderReleaseGate(t *testing.T) {
	clearSkiffEnv(t)
	stateDir := t.TempDir()
	stateURI := "file://" + stateDir
	jetstreamSpec := filepath.Join("..", "..", "examples", "stateful", "jetstream", "skiff.yaml")
	singleSpec := filepath.Join("..", "..", "examples", "stateful", "single-member", "skiff.yaml")

	var validated statefulGateValidateOutput
	runStatefulGateJSON(t, &validated, "validate", jetstreamSpec, "--format", "json", "--trace-id", "tr_stateful_gate_validate_jetstream")
	if !validated.OK || !validated.Result.OK || validated.Result.Kind != "StatefulGroup" || validated.Result.Name != "orders-stream" {
		t.Fatalf("unexpected JetStream validation output: %+v", validated)
	}

	var planned statefulGatePlanOutput
	runStatefulGateJSON(t, &planned, "stateful", "plan", jetstreamSpec, "--provider", fakeprovider.Name, "--region", "local", "--state", stateURI, "--format", "json", "--trace-id", "tr_stateful_gate_plan")
	if !planned.OK || planned.Plan.Service != "orders-stream" || !statefulGateHasPlanKind(planned, ir.ResourceKindStatefulGroup) || !statefulGateHasPlanKind(planned, ir.ResourceKindStatefulMember) || !statefulGateHasPlanKind(planned, ir.ResourceKindStatefulVolume) {
		t.Fatalf("unexpected JetStream plan output: %+v", planned)
	}

	var explained statefulGateExplainOutput
	runStatefulGateJSON(t, &explained, "explain", jetstreamSpec, "--provider", "aws", "--format", "json", "--trace-id", "tr_stateful_gate_explain")
	if !explained.OK || explained.Result.Service != "orders-stream" || !statefulGateHasCloudPrimitive(explained, "stateful member VM") || !statefulGateHasCloudPrimitive(explained, "durable block volume") {
		t.Fatalf("unexpected JetStream explain output: %+v", explained)
	}

	runStatefulGateJSON(t, &validated, "validate", singleSpec, "--format", "json", "--trace-id", "tr_stateful_gate_validate_single")
	if !validated.OK || !validated.Result.OK || validated.Result.Name != "ledger-stream" {
		t.Fatalf("unexpected single-member validation output: %+v", validated)
	}

	var applied statefulGateApplyOutput
	runStatefulGateJSON(t, &applied, "stateful", "apply", singleSpec, "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--operation-id", "op_stateful_gate_apply", "--format", "json", "--trace-id", "tr_stateful_gate_apply")
	if !applied.OK || !applied.Result.OK || applied.Result.Group != "ledger-stream" || applied.Result.OperationID != "op_stateful_gate_apply" || len(applied.Result.MemberControls) != 1 {
		t.Fatalf("unexpected stateful apply output: %+v", applied)
	}
	seedStatefulGateProviderIDs(t, stateDir, "ledger-stream", 0, "i-ledger-0", "vol-ledger-0")

	var inspected statefulGateInspectOutput
	runStatefulGateJSON(t, &inspected, "stateful", "inspect", "ledger-stream", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_inspect")
	if !inspected.OK || inspected.Result.Group != "ledger-stream" || len(inspected.Result.MemberControls) != 1 || inspected.Result.MemberControls[0].InstanceID != "i-ledger-0" {
		t.Fatalf("unexpected stateful inspect output: %+v", inspected)
	}

	var status statefulGateStatusOutput
	runStatefulGateJSON(t, &status, "stateful", "status", "ledger-stream", "--fresh", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_status")
	if !status.OK || status.Result.Group != "ledger-stream" || status.Result.Health != "nominal" || len(status.Result.Members) != 1 {
		t.Fatalf("unexpected stateful status output: %+v", status)
	}

	var doctor statefulGateDoctorOutput
	runStatefulGateJSON(t, &doctor, "stateful", "doctor", "ledger-stream", "--fresh", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_doctor")
	if !doctor.OK || doctor.Doctor.Health == "" {
		t.Fatalf("unexpected stateful doctor output: %+v", doctor)
	}

	var logs statefulGateLogsOutput
	runStatefulGateJSON(t, &logs, "stateful", "logs", "ledger-stream", "--member", "0", "--since", "20m", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_logs")
	if !logs.OK || len(logs.Entries) != 1 || logs.Entries[0].Source != "i-ledger-0" {
		t.Fatalf("unexpected stateful logs output: %+v", logs)
	}

	var metrics statefulGateMetricsOutput
	runStatefulGateJSON(t, &metrics, "stateful", "metrics", "ledger-stream", "--member", "0", "--metric", "request_count", "--since", "20m", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_metrics")
	if !metrics.OK || len(metrics.Series) != 1 || len(metrics.Series[0].Points) != 1 {
		t.Fatalf("unexpected stateful metrics output: %+v", metrics)
	}

	var updated statefulGateSagaOutput
	runStatefulGateJSON(t, &updated, "saga", "start", "stateful.ordered_update", "ledger-stream", "--group", "ledger-stream", "--release-id", "rel_stateful_gate", "--members", "0", "--operation-id", "op_stateful_gate_update", "--saga-id", "saga_stateful_gate_update", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_update")
	if !updated.OK || updated.Result.Status != "succeeded" || updated.Result.Group != "ledger-stream" || updated.Result.ReleaseID != "rel_stateful_gate" {
		t.Fatalf("unexpected ordered update output: %+v", updated)
	}

	var replaced statefulGateReplaceOutput
	runStatefulGateJSON(t, &replaced, "stateful", "replace-member", "ledger-stream", "--member", "0", "--reason", "release gate replacement", "--yes", "--operation-id", "op_stateful_gate_replace", "--saga-id", "saga_stateful_gate_replace", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_replace")
	if !replaced.OK || replaced.Result.Status != "succeeded" || replaced.Result.Member != 0 || replaced.Result.NextAction != "complete" {
		t.Fatalf("unexpected replacement output: %+v", replaced)
	}

	var backupPlan statefulGateBackupRestoreOutput
	runStatefulGateJSON(t, &backupPlan, "stateful", "backup", "plan", "ledger-stream", "--members", "0", "--backup-id", "backup_stateful_gate_plan", "--operation-id", "op_stateful_gate_backup_plan", "--saga-id", "saga_stateful_gate_backup_plan", "--retention", "24h", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_backup_plan")
	if !backupPlan.OK || backupPlan.Result.NextAction != "create_saga" || backupPlan.Result.Plan == nil {
		t.Fatalf("unexpected backup plan output: %+v", backupPlan)
	}

	var backup statefulGateBackupRestoreOutput
	runStatefulGateJSON(t, &backup, "stateful", "snapshot", "ledger-stream", "--member", "0", "--backup-id", "backup_stateful_gate", "--operation-id", "op_stateful_gate_backup", "--saga-id", "saga_stateful_gate_backup", "--retention", "24h", "--reason", "release gate snapshot", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_backup")
	if !backup.OK || backup.Result.Status != "succeeded" || backup.Result.BackupID != "backup_stateful_gate" {
		t.Fatalf("unexpected backup output: %+v", backup)
	}

	var restorePlan statefulGateBackupRestoreOutput
	runStatefulGateJSON(t, &restorePlan, "stateful", "restore", "plan", "ledger-stream", "--member", "0", "--backup-id", "backup_stateful_gate", "--restore-id", "restore_stateful_gate_plan", "--operation-id", "op_stateful_gate_restore_plan", "--saga-id", "saga_stateful_gate_restore_plan", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_restore_plan")
	if !restorePlan.OK || restorePlan.Result.NextAction != "create_saga" || restorePlan.Result.Plan == nil {
		t.Fatalf("unexpected restore plan output: %+v", restorePlan)
	}

	var restore statefulGateBackupRestoreOutput
	runStatefulGateJSON(t, &restore, "stateful", "restore", "apply", "ledger-stream", "--member", "0", "--backup-id", "backup_stateful_gate", "--restore-id", "restore_stateful_gate", "--operation-id", "op_stateful_gate_restore", "--saga-id", "saga_stateful_gate_restore", "--reason", "release gate restore", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_restore")
	if !restore.OK || restore.Result.Status != "running" || restore.Result.NextAction != "approve_or_reject" {
		t.Fatalf("unexpected restore apply output: %+v", restore)
	}
	runStatefulGateJSON(t, &statefulGateApprovalOutput{}, "saga", "approve", "saga_stateful_gate_restore", "--step", "approve-restore", "--reason", "release gate approval", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_restore_approve")
	runStatefulGateJSON(t, &statefulGateGenericSagaOutput{}, "saga", "resume", "saga_stateful_gate_restore", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_stateful_gate_restore_resume")

	store, err := file.New(stateDir)
	if err != nil {
		t.Fatalf("open release gate store: %v", err)
	}
	restoreKey, err := paths.StatefulRestoreRecord("ledger-stream", "restore_stateful_gate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), restoreKey); err != nil {
		t.Fatalf("restore record missing after approved resume: %v", err)
	}
}

func runStatefulGateJSON[T any](t *testing.T, out *T, args ...string) {
	t.Helper()
	if err := runSkiffJSON(out, args...); err != nil {
		t.Fatal(err)
	}
}

func seedStatefulGateProviderIDs(t *testing.T, dir, group string, member int, instanceID, volumeID string) {
	t.Helper()
	ctx := context.Background()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	stateClient := state.NewClient(store)
	memberDoc, err := stateClient.GetStatefulMemberControl(ctx, group, member)
	if err != nil {
		t.Fatalf("get member control: %v", err)
	}
	nextMember := memberDoc.Control
	nextMember.InstanceID = instanceID
	nextMember.VolumeID = volumeID
	nextMember.Phase = state.StatefulMemberReady
	if nextMember.Generation <= 0 {
		nextMember.Generation = 1
	}
	if _, err := stateClient.UpdateStatefulMemberControlCAS(ctx, memberDoc, nextMember); err != nil {
		t.Fatalf("update member control: %v", err)
	}

	groupDoc, err := stateClient.GetStatefulGroupControl(ctx, group)
	if err != nil {
		t.Fatalf("get group control: %v", err)
	}
	nextGroup := groupDoc.Control
	for i := range nextGroup.Members {
		if nextGroup.Members[i].Member != member {
			continue
		}
		nextGroup.Members[i].InstanceID = instanceID
		nextGroup.Members[i].VolumeID = volumeID
		nextGroup.Members[i].Phase = state.StatefulMemberReady
		nextGroup.Members[i].Generation = nextMember.Generation
	}
	if len(nextGroup.Members) == 0 {
		nextGroup.Members = []schema.StatefulMemberSummary{{
			Member:     member,
			Generation: nextMember.Generation,
			InstanceID: instanceID,
			VolumeID:   volumeID,
			DNSName:    nextMember.DNSName,
			Phase:      state.StatefulMemberReady,
		}}
	}
	if _, err := stateClient.UpdateStatefulGroupControlCAS(ctx, groupDoc, nextGroup); err != nil {
		t.Fatalf("update group control: %v", err)
	}
}

func statefulGateHasPlanKind(output statefulGatePlanOutput, kind string) bool {
	for _, resource := range output.Plan.Resources {
		if resource.Kind == kind {
			return true
		}
	}
	return false
}

func statefulGateHasCloudPrimitive(output statefulGateExplainOutput, primitive string) bool {
	for _, resource := range output.Result.Resources {
		if resource.CloudPrimitive == primitive {
			return true
		}
	}
	return false
}

type statefulGateValidateOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OK   bool   `json:"ok"`
		Kind string `json:"kind"`
		Name string `json:"name"`
		Env  string `json:"env"`
	} `json:"result"`
}

type statefulGatePlanOutput struct {
	OK   bool `json:"ok"`
	Plan struct {
		Service   string `json:"service"`
		Env       string `json:"env"`
		Resources []struct {
			Kind       string `json:"kind"`
			LogicalID  string `json:"logical_id"`
			ProviderID string `json:"provider_id"`
		} `json:"resources"`
	} `json:"plan"`
}

type statefulGateExplainOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Service   string `json:"service"`
		Env       string `json:"env"`
		Resources []struct {
			Kind           string `json:"kind"`
			CloudPrimitive string `json:"cloud_primitive"`
			Name           string `json:"name"`
		} `json:"resources"`
	} `json:"result"`
}

type statefulGateApplyOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OK             bool   `json:"ok"`
		Group          string `json:"group"`
		OperationID    string `json:"operation_id"`
		MemberControls []struct {
			Member int    `json:"member"`
			Phase  string `json:"phase"`
		} `json:"member_controls"`
	} `json:"result"`
}

type statefulGateInspectOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Group          string `json:"group"`
		MemberControls []struct {
			Member     int    `json:"member"`
			InstanceID string `json:"instance_id"`
			VolumeID   string `json:"volume_id"`
			Phase      string `json:"phase"`
		} `json:"member_controls"`
	} `json:"result"`
}

type statefulGateStatusOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Group   string `json:"group"`
		Health  string `json:"health"`
		Members []struct {
			Member     int    `json:"member"`
			Health     string `json:"health"`
			InstanceID string `json:"instance_id"`
		} `json:"members"`
	} `json:"result"`
}

type statefulGateDoctorOutput struct {
	OK     bool `json:"ok"`
	Doctor struct {
		Health string `json:"health"`
	} `json:"doctor"`
}

type statefulGateLogsOutput struct {
	OK      bool `json:"ok"`
	Entries []struct {
		Source  string `json:"source"`
		Message string `json:"message"`
	} `json:"entries"`
}

type statefulGateMetricsOutput struct {
	OK     bool `json:"ok"`
	Series []struct {
		Name   string `json:"name"`
		Points []struct {
			Value float64 `json:"value"`
		} `json:"points"`
	} `json:"series"`
}

type statefulGateSagaOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		SagaID     string `json:"saga_id"`
		Group      string `json:"group"`
		ReleaseID  string `json:"release_id"`
		Status     string `json:"status"`
		NextAction string `json:"next_action"`
	} `json:"result"`
}

type statefulGateReplaceOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		SagaID     string `json:"saga_id"`
		Group      string `json:"group"`
		Member     int    `json:"member"`
		Status     string `json:"status"`
		NextAction string `json:"next_action"`
	} `json:"result"`
}

type statefulGateBackupRestoreOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		SagaID     string `json:"saga_id"`
		Group      string `json:"group"`
		BackupID   string `json:"backup_id"`
		RestoreID  string `json:"restore_id"`
		Status     string `json:"status"`
		NextAction string `json:"next_action"`
		Plan       any    `json:"plan"`
	} `json:"result"`
}

type statefulGateApprovalOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		SagaID string `json:"saga_id"`
		Status string `json:"status"`
	} `json:"result"`
}

type statefulGateGenericSagaOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		SagaID string `json:"saga_id"`
		Status string `json:"status"`
	} `json:"result"`
}

func (o statefulGatePlanOutput) String() string {
	return fmt.Sprintf("%s/%s resources=%d", o.Plan.Env, o.Plan.Service, len(o.Plan.Resources))
}
