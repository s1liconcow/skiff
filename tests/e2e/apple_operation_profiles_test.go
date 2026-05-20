package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider/applecontainer"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	packagestep "github.com/s1liconcow/skiff/internal/saga/steps/package"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

func TestOpsemAppleOperationProfilesE2E(t *testing.T) {
	resetSkiffEnv(t)
	if os.Getenv("SKIFF_OPSEM_PROFILES_E2E") != "1" {
		t.Skip("set SKIFF_OPSEM_PROFILES_E2E=1 and SKIFF_E2E_OPSEM_IMAGE to run the live operation profile e2e")
	}
	imageName := strings.TrimSpace(os.Getenv("SKIFF_E2E_OPSEM_IMAGE"))
	if imageName == "" {
		t.Skip("SKIFF_E2E_OPSEM_IMAGE must point to a skiff-opsem OCI image built from tests/fixtures/opsem/Dockerfile")
	}
	containerPath, err := exec.LookPath("container")
	if err != nil {
		t.Skip("Apple container CLI is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	image := pinnedImageForE2E(t, ctx, "SKIFF_E2E_OPSEM_IMAGE", imageName)
	cli := appleContainerCLI{path: containerPath}
	persist := appleContainerPersistEnabled()
	runID := fmt.Sprintf("skiff-opsem-profiles-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	rustfs := startRustFSContainer(t, ctx, cli, runID, freePort(t), persist)
	configureRustFSEnv(t, rustfs)
	store := rustfsObjectStore(t, ctx, rustfs)
	stateURI := "s3://" + rustfs.bucket
	env := "prod"
	traceID := "tr_opsem_profiles_e2e"
	report := newE2EReport(t, "opsem-apple-operation-profiles", "opsem-profiles", env, traceID)
	defer writeE2EReport(t, report)

	contexts := writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{})
	useAppleContext(t, contexts, appleDirectContext)
	if persist {
		report.CleanupStatus = "opsem operation profile Apple containers and RustFS state left running for inspection"
	} else {
		report.CleanupStatus = "opsem operation profile Apple containers, volumes, and RustFS state registered with test cleanup"
	}

	lockfile := filepath.Join(report.reportDir, "skiff.lock.json")
	cacheRoot := filepath.Join(report.reportDir, "package-cache")
	lockOpsemProfilePackages(t, report, lockfile, cacheRoot)

	scenarios := []opsemProfileScenario{
		{
			Mode:       "primary-replica",
			Service:    "opsem-primary",
			Fixture:    "opsem-primary-replica",
			Profile:    "primary-switchover-update",
			Operation:  "op_opsem_primary",
			Saga:       "saga_opsem_primary",
			Expected:   schema.SagaSucceeded,
			ParamPairs: []string{"release_id=rel_primary", "candidate=1", "return_primary=true"},
		},
		{
			Mode:       "raft-groups",
			Service:    "opsem-raft",
			Fixture:    "opsem-raft-groups",
			Profile:    "raft-group-rolling-update",
			Operation:  "op_opsem_raft",
			Saga:       "saga_opsem_raft",
			Expected:   schema.SagaSucceeded,
			WaitKind:   "package.raft_group.update_followers",
			ParamPairs: []string{"release_id=rel_raft", "group_selector={}", "leader_policy=transfer"},
		},
		{
			Mode:          "partition-isr",
			Service:       "opsem-partition",
			Fixture:       "opsem-partition-isr",
			Profile:       "partition-quorum-rolling-update",
			Operation:     "op_opsem_partition",
			Saga:          "saga_opsem_partition",
			Expected:      schema.SagaFailed,
			UnsafeFailure: "isr-below-min",
			ParamPairs:    []string{"release_id=rel_partition", "partition_selector={}", "min_in_sync=2"},
		},
		{
			Mode:       "slot-cluster",
			Service:    "opsem-slot",
			Fixture:    "opsem-slot-cluster",
			Profile:    "slot-aware-failover-update",
			Operation:  "op_opsem_slot",
			Saga:       "saga_opsem_slot",
			Expected:   schema.SagaSucceeded,
			ParamPairs: []string{"release_id=rel_slot", "slot_selector={}"},
		},
		{
			Mode:       "shard-cluster",
			Service:    "opsem-shard",
			Fixture:    "opsem-shard-cluster",
			Profile:    "shard-allocation-rolling-update",
			Operation:  "op_opsem_shard",
			Saga:       "saga_opsem_shard",
			Expected:   schema.SagaSucceeded,
			ParamPairs: []string{"release_id=rel_shard", "shard_selector={}", "rebalance_timeout=1m"},
		},
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.Mode, func(t *testing.T) {
			portBase := reserveAppleStatefulPortBase(t, 3, 1)
			t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", strconv.Itoa(portBase))
			specPath := writeOpsemStatefulSpecMode(t, report.reportDir, scenario.Service, env, image, scenario.Mode)
			if !persist {
				t.Cleanup(func() { cleanupAppleStatefulGroup(context.Background(), cli, env, scenario.Service, 3, 3) })
			}

			applyOp := "op_apply_" + strings.ReplaceAll(scenario.Mode, "-", "_")
			var applied appleStatefulApplyOutput
			decodeCLIJSON(t, runSkiffCLI(t, report,
				"stateful", "apply", specPath,
				"--direct",
				"--state", stateURI,
				"--env", env,
				"--provider", applecontainer.Name,
				"--region", "local",
				"--operation-id", applyOp,
				"--format", "json",
				"--trace-id", traceID,
			), &applied)
			if !applied.OK || len(applied.Result.MemberControls) != 3 {
				t.Fatalf("unexpected opsem apply output: %+v", applied)
			}
			report.addOperationID(applyOp)
			for _, resource := range applied.Result.ProviderResources {
				report.addProviderID(resource.ProviderID)
			}
			waitForOpsemMode(t, ctx, portBase, scenario.Mode)
			if scenario.UnsafeFailure != "" {
				opsemPost(t, ctx, opsemMemberPort(portBase, 1), "/admin/fail", map[string]string{"type": scenario.UnsafeFailure})
				report.fact("opsem_unsafe_state", scenario.Service+" injected "+scenario.UnsafeFailure+" before profile execution")
			}

			planOut := runOpsemProfilePlan(t, report, scenario, lockfile, cacheRoot, traceID)
			if planOut.WouldWrite || planOut.Package == nil || planOut.Package.Digest == "" || len(planOut.Profile.Steps) == 0 {
				t.Fatalf("operation profile plan did not resolve package and steps: %+v", planOut)
			}
			runOut := runOpsemProfileRun(t, report, scenario, lockfile, cacheRoot, stateURI, env, traceID)
			if !runOut.WouldWrite || runOut.Package == nil || runOut.Package.Digest == "" {
				t.Fatalf("operation profile run did not write package-backed operation: %+v", runOut)
			}
			report.addOperationID(runOut.OperationID)
			report.addSagaID(runOut.SagaID)
			for _, key := range runOut.Paths {
				report.addObjectPath(key)
			}

			graph := assertRenderedPackageGraph(t, ctx, store, scenario.Saga)
			runner := &opsemSemanticRunner{
				service:  scenario.Service,
				mode:     scenario.Mode,
				portBase: portBase,
				waitKind: scenario.WaitKind,
				waited:   map[string]bool{},
			}
			execution := executeOpsemProfileSaga(t, ctx, store, graph, scenario.Saga, runner)
			if execution.Status != scenario.Expected {
				t.Fatalf("saga status = %s, want %s; execution=%+v", execution.Status, scenario.Expected, execution)
			}
			if scenario.UnsafeFailure != "" && len(runner.mutations) != 0 {
				t.Fatalf("unsafe scenario mutated live members before failing: %+v", runner.mutations)
			}
			recordOperationProfileCompletion(t, ctx, store, scenario.Service, scenario.Operation, scenario.Saga)
			assertOperationProfileObjects(t, ctx, store, scenario, runOut, report)
			assertDirectOperationProfileInspect(t, report, scenario, stateURI, env, traceID)
			report.fact("opsem_profile_"+scenario.Mode, fmt.Sprintf("profile %s finished with saga status %s", scenario.Profile, execution.Status))
		})
	}

	localSkiffd := startLocalAppleSkiffd(t, ctx, store, report, rustfs, stateURI, env, traceID, "", persist, nil)
	contexts = writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{APIURL: localSkiffd.url, SkiffdPID: report.SkiffdPID, SkiffdLogPath: report.SkiffdLogPath})
	useAppleContext(t, contexts, appleAPIContext)
	for _, scenario := range scenarios {
		assertAPIOperationProfileInspect(t, report, scenario, traceID)
	}
	report.fact("opsem_profile_skiffd", "validated local skiffd operation and saga inspect endpoints for package-backed operation profiles")
}

type opsemProfileScenario struct {
	Mode          string
	Service       string
	Fixture       string
	Profile       string
	Operation     string
	Saga          string
	Expected      schema.SagaStatus
	UnsafeFailure string
	WaitKind      string
	ParamPairs    []string
}

type opsemOpsProfileOutput struct {
	OK          bool                      `json:"ok"`
	TraceID     string                    `json:"trace_id,omitempty"`
	Target      string                    `json:"target"`
	Operation   string                    `json:"operation"`
	OperationID string                    `json:"operation_id"`
	SagaID      string                    `json:"saga_id"`
	WouldWrite  bool                      `json:"would_write"`
	Package     *schema.PackageProvenance `json:"package,omitempty"`
	Profile     struct {
		Name  string `json:"name"`
		Steps []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"steps"`
	} `json:"profile"`
	Paths map[string]string `json:"paths,omitempty"`
}

type opsemSagaInspectOutput struct {
	OK     bool                    `json:"ok"`
	Result sagastate.InspectResult `json:"result"`
}

func lockOpsemProfilePackages(t *testing.T, report *e2eReport, lockfile, cacheRoot string) {
	t.Helper()
	root := repoRootForTest(t)
	for _, fixture := range []string{
		"opsem-primary-replica",
		"opsem-raft-groups",
		"opsem-partition-isr",
		"opsem-slot-cluster",
		"opsem-shard-cluster",
	} {
		runSkiffCLI(t, report,
			"pkg", "add", "file://"+filepath.Join(root, "tests", "fixtures", "packages", fixture),
			"--lockfile", lockfile,
			"--cache", cacheRoot,
			"--format", "json",
			"--trace-id", "tr_pkg_"+strings.ReplaceAll(fixture, "-", "_"),
		)
		report.fact("opsem_package_fixture", "locked "+fixture+" into "+lockfile)
	}
}

func runOpsemProfilePlan(t *testing.T, report *e2eReport, scenario opsemProfileScenario, lockfile, cacheRoot, traceID string) opsemOpsProfileOutput {
	t.Helper()
	args := []string{
		"ops", "plan", scenario.Service, scenario.Profile,
		"--lockfile", lockfile,
		"--cache", cacheRoot,
		"--operation-id", scenario.Operation,
		"--saga-id", scenario.Saga,
		"--target-kind", "StatefulGroup",
		"--format", "json",
		"--trace-id", traceID,
	}
	args = appendOpsemProfileParams(args, scenario.ParamPairs)
	var out opsemOpsProfileOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, args...), &out)
	if !out.OK || out.OperationID != scenario.Operation || out.SagaID != scenario.Saga || out.Operation != scenario.Profile {
		t.Fatalf("unexpected opsem profile plan: %+v", out)
	}
	return out
}

func runOpsemProfileRun(t *testing.T, report *e2eReport, scenario opsemProfileScenario, lockfile, cacheRoot, stateURI, env, traceID string) opsemOpsProfileOutput {
	t.Helper()
	args := []string{
		"ops", "run", scenario.Service, scenario.Profile,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--lockfile", lockfile,
		"--cache", cacheRoot,
		"--operation-id", scenario.Operation,
		"--saga-id", scenario.Saga,
		"--target-kind", "StatefulGroup",
		"--yes",
		"--format", "json",
		"--trace-id", traceID,
	}
	args = appendOpsemProfileParams(args, scenario.ParamPairs)
	var out opsemOpsProfileOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, args...), &out)
	if !out.OK || out.OperationID != scenario.Operation || out.SagaID != scenario.Saga || out.Operation != scenario.Profile {
		t.Fatalf("unexpected opsem profile run: %+v", out)
	}
	return out
}

func appendOpsemProfileParams(args []string, params []string) []string {
	for _, param := range params {
		args = append(args, "--param", param)
	}
	return args
}

func waitForOpsemMode(t *testing.T, ctx context.Context, portBase int, mode string) {
	t.Helper()
	for member := 0; member < 3; member++ {
		state := opsemState(t, ctx, opsemMemberPort(portBase, member))
		if state.Mode != mode || state.Member != member || state.Generation != 1 {
			t.Fatalf("unexpected opsem member %d state: %+v", member, state)
		}
	}
}

func opsemMemberPort(portBase, member int) int {
	return portBase + member*100
}

func assertRenderedPackageGraph(t *testing.T, ctx context.Context, store objstore.ObjectStore, sagaID string) schema.SagaGraph {
	t.Helper()
	graph, err := sagastate.NewStore(store).GetGraph(ctx, sagaID)
	if err != nil {
		t.Fatalf("get saga graph: %v", err)
	}
	if len(graph.Graph.Nodes) == 0 {
		t.Fatalf("saga graph has no nodes: %+v", graph.Graph)
	}
	for _, node := range graph.Graph.Nodes {
		if !strings.HasPrefix(node.Kind, "package.") {
			t.Fatalf("saga node %s kind = %s, want package step", node.ID, node.Kind)
		}
	}
	return graph.Graph
}

func executeOpsemProfileSaga(t *testing.T, ctx context.Context, store objstore.ObjectStore, graph schema.SagaGraph, sagaID string, runner *opsemSemanticRunner) *sagastate.ExecutionResult {
	t.Helper()
	sagas := sagastate.NewStore(store)
	executor := &sagastate.Executor{
		Store:   sagas,
		Steps:   opsemPackageStepMap(t, graph, runner),
		Owner:   "opsem-operation-profile-e2e",
		EventID: sequentialEventID(sagaID),
	}
	first, err := executor.Execute(ctx, sagaID)
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if runner.waitKind == "" {
		return first
	}
	if len(first.WaitingSteps) == 0 {
		t.Fatalf("saga did not stop on waiting step for %s: %+v", runner.waitKind, first)
	}
	control, err := sagas.GetControl(ctx, sagaID)
	if err != nil {
		t.Fatalf("get waiting saga control: %v", err)
	}
	if !controlHasWaitingProviderOperation(control.Control, runner.waitKind) {
		t.Fatalf("waiting provider operation was not persisted in saga control: %+v", control.Control.StepResults)
	}
	second, err := executor.Execute(ctx, sagaID)
	if err != nil {
		t.Fatalf("resume saga after waiting step: %v", err)
	}
	if second.Status != schema.SagaSucceeded {
		t.Fatalf("resumed saga status = %s, want succeeded: %+v", second.Status, second)
	}
	return second
}

func opsemPackageStepMap(t *testing.T, graph schema.SagaGraph, runner *opsemSemanticRunner) map[string]steps.Step {
	t.Helper()
	out := make(map[string]steps.Step)
	manifest := pluginapi.Manifest{
		APIVersion: pluginapi.APIVersion,
		Kind:       pluginapi.KindPlugin,
		Name:       "opsem-semantic-e2e",
		Version:    "1.0.0",
	}
	for _, node := range graph.Nodes {
		if _, exists := out[node.Kind]; exists {
			continue
		}
		step, err := packagestep.New(manifest, opsemPackageStepCapability(node.Kind), runner.run)
		if err != nil {
			t.Fatalf("create package step %s: %v", node.Kind, err)
		}
		out[node.Kind] = step
	}
	return out
}

func opsemPackageStepCapability(kind string) sagaapi.PackageStepCapability {
	return sagaapi.PackageStepCapability{
		Kind:    kind,
		Summary: "opsem semantic package step",
		Params: map[string]sagaapi.ParamSchema{
			"profile_kind":       {Type: sagaapi.ParamString, Required: true},
			"release_id":         {Type: sagaapi.ParamString, Required: true},
			"candidate":          {Type: sagaapi.ParamString},
			"return_primary":     {Type: sagaapi.ParamBoolean},
			"group_selector":     {Type: sagaapi.ParamObject},
			"leader_policy":      {Type: sagaapi.ParamString},
			"partition_selector": {Type: sagaapi.ParamObject},
			"min_in_sync":        {Type: sagaapi.ParamInteger},
			"slot_selector":      {Type: sagaapi.ParamObject},
			"shard_selector":     {Type: sagaapi.ParamObject},
			"rebalance_timeout":  {Type: sagaapi.ParamString},
		},
		Risk:          sagaapi.RiskMedium,
		Reversibility: sagaapi.Compensatable,
	}
}

func sequentialEventID(sagaID string) func() string {
	counter := 0
	return func() string {
		counter++
		return fmt.Sprintf("evt_%s_%03d", sagaID, counter)
	}
}

func controlHasWaitingProviderOperation(control schema.SagaControl, kind string) bool {
	for _, result := range control.StepResults {
		if result.Kind == kind && result.Status == string(steps.StatusWaiting) && len(result.ProviderOperations) > 0 {
			return true
		}
	}
	return false
}

func recordOperationProfileCompletion(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, operationID, sagaID string) {
	t.Helper()
	opStore := opsstate.NewStore(store)
	sagaControl, err := sagastate.NewStore(store).GetControl(ctx, sagaID)
	if err != nil {
		t.Fatalf("get saga control: %v", err)
	}
	current, err := opStore.GetControl(ctx, service, operationID)
	if err != nil {
		t.Fatalf("get operation control: %v", err)
	}
	next := current.Control
	next.Status = operationStatusForProfileSaga(sagaControl.Control.Status)
	next.StepResults = append([]schema.StepResultRef(nil), sagaControl.Control.StepResults...)
	next.ProviderOperations = providerOperationsFromStepRefs(sagaControl.Control.StepResults)
	if _, err := opStore.UpdateControlCAS(ctx, current, next); err != nil {
		t.Fatalf("update operation control from saga result: %v", err)
	}
}

func operationStatusForProfileSaga(status schema.SagaStatus) schema.OperationStatus {
	switch status {
	case schema.SagaSucceeded:
		return schema.OperationSucceeded
	case schema.SagaFailed:
		return schema.OperationFailed
	case schema.SagaCanceled:
		return schema.OperationCanceled
	case schema.SagaRunning, schema.SagaCompensating:
		return schema.OperationRunning
	default:
		return schema.OperationPending
	}
}

func providerOperationsFromStepRefs(refs []schema.StepResultRef) []schema.ProviderOperationRef {
	out := make([]schema.ProviderOperationRef, 0)
	seen := map[string]struct{}{}
	for _, ref := range refs {
		for _, op := range ref.ProviderOperations {
			key := op.Provider + "/" + op.Kind + "/" + op.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, op)
		}
	}
	return out
}

func assertOperationProfileObjects(t *testing.T, ctx context.Context, store objstore.ObjectStore, scenario opsemProfileScenario, runOut opsemOpsProfileOutput, report *e2eReport) {
	t.Helper()
	required := map[string]string{
		"operation_intent":  mustOperationIntentPath(t, scenario.Service, scenario.Operation),
		"operation_control": mustOperationControlPath(t, scenario.Service, scenario.Operation),
		"saga_intent":       mustSagaIntentPath(t, scenario.Saga),
		"saga_graph":        mustSagaGraphPath(t, scenario.Saga),
		"saga_control":      mustSagaControlPath(t, scenario.Saga),
	}
	for name, key := range required {
		assertObjectExists(t, ctx, store, key, name)
		report.addObjectPath(key)
	}
	for name, key := range runOut.Paths {
		assertObjectExists(t, ctx, store, key, name)
		report.addObjectPath(key)
	}
	assertObjectPrefixNonEmpty(t, ctx, store, mustOperationEventsPrefix(t, scenario.Service, scenario.Operation), "operation events")
	assertObjectPrefixNonEmpty(t, ctx, store, mustSagaEventsPrefix(t, scenario.Saga), "saga events")
	assertObjectPrefixNonEmpty(t, ctx, store, "audit/", "audit records")
}

func assertDirectOperationProfileInspect(t *testing.T, report *e2eReport, scenario opsemProfileScenario, stateURI, env, traceID string) {
	t.Helper()
	wantStatus := string(operationStatusForProfileSaga(scenario.Expected))
	var operation appleOpsInspectOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"ops", "inspect", scenario.Operation,
		"--service", scenario.Service,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--format", "json",
		"--trace-id", traceID,
	), &operation)
	if !operation.OK || operation.Result.Status != wantStatus || len(operation.Result.StepResults) == 0 {
		t.Fatalf("unexpected direct operation inspect for %s: %+v", scenario.Service, operation)
	}
	var saga opsemSagaInspectOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"ops", "inspect", scenario.Saga,
		"--direct",
		"--state", stateURI,
		"--env", env,
		"--provider", applecontainer.Name,
		"--region", "local",
		"--format", "json",
		"--trace-id", traceID,
	), &saga)
	if !saga.OK || saga.Result.Status != scenario.Expected || len(saga.Result.Nodes) == 0 {
		t.Fatalf("unexpected direct saga inspect for %s: %+v", scenario.Service, saga)
	}
}

func assertAPIOperationProfileInspect(t *testing.T, report *e2eReport, scenario opsemProfileScenario, traceID string) {
	t.Helper()
	wantStatus := string(operationStatusForProfileSaga(scenario.Expected))
	var operation appleOpsInspectOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"ops", "inspect", scenario.Operation,
		"--service", scenario.Service,
		"--format", "json",
		"--trace-id", traceID,
	), &operation)
	if !operation.OK || operation.Result.Status != wantStatus || len(operation.Result.StepResults) == 0 {
		t.Fatalf("unexpected API operation inspect for %s: %+v", scenario.Service, operation)
	}
	var saga opsemSagaInspectOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"ops", "inspect", scenario.Saga,
		"--format", "json",
		"--trace-id", traceID,
	), &saga)
	if !saga.OK || saga.Result.Status != scenario.Expected || len(saga.Result.Nodes) == 0 {
		t.Fatalf("unexpected API saga inspect for %s: %+v", scenario.Service, saga)
	}
}

func assertObjectExists(t *testing.T, ctx context.Context, store objstore.ObjectStore, key, label string) {
	t.Helper()
	if _, err := store.Get(ctx, key); err != nil {
		t.Fatalf("%s object %s missing: %v", label, key, err)
	}
}

func assertObjectPrefixNonEmpty(t *testing.T, ctx context.Context, store objstore.ObjectStore, prefix, label string) {
	t.Helper()
	objects, err := store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list %s prefix %s: %v", label, prefix, err)
	}
	if len(objects) == 0 {
		t.Fatalf("%s prefix %s has no objects", label, prefix)
	}
}

func mustOperationIntentPath(t *testing.T, service, operation string) string {
	t.Helper()
	key, err := paths.OperationIntent(service, operation)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustOperationControlPath(t *testing.T, service, operation string) string {
	t.Helper()
	key, err := paths.OperationControl(service, operation)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustOperationEventsPrefix(t *testing.T, service, operation string) string {
	t.Helper()
	key, err := paths.OperationEventsPrefix(service, operation)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustSagaIntentPath(t *testing.T, saga string) string {
	t.Helper()
	key, err := paths.SagaIntent(saga)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustSagaGraphPath(t *testing.T, saga string) string {
	t.Helper()
	key, err := paths.SagaGraph(saga)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustSagaControlPath(t *testing.T, saga string) string {
	t.Helper()
	key, err := paths.SagaControl(saga)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustSagaEventsPrefix(t *testing.T, saga string) string {
	t.Helper()
	key, err := paths.SagaEventsPrefix(saga)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type opsemSemanticRunner struct {
	service   string
	mode      string
	portBase  int
	waitKind  string
	waited    map[string]bool
	mutations []string
}

func (r *opsemSemanticRunner) run(ctx context.Context, hook pluginapi.Hook, request any, response any) error {
	if hook != pluginapi.HookPackageStep {
		return fmt.Errorf("unexpected hook %s", hook)
	}
	req, ok := request.(pluginapi.PackageStepRequest)
	if !ok {
		return fmt.Errorf("unexpected package step request %T", request)
	}
	if req.Phase != sagaapi.StepPhaseRun {
		return assignOpsemPackageResponse(response, r.success(req, "phase "+string(req.Phase)+" accepted"))
	}
	if r.waitKind == req.Kind && !r.waited[req.Context.StepID] {
		r.waited[req.Context.StepID] = true
		return assignOpsemPackageResponse(response, sagaapi.PackageStepResultResponse{
			Status:  sagaapi.StepStatusWaiting,
			Summary: req.Kind + " waiting on live provider operation",
			ProviderOperations: []sagaapi.ProviderOperationRef{{
				Provider:    applecontainer.Name,
				Kind:        "opsem.semantic_step",
				ID:          "opsem-wait-" + sanitizeProviderID(req.Context.StepID),
				ObservedAt:  time.Now().UTC().Format(time.RFC3339Nano),
				Description: "opsem semantic step waiting for direct resume",
			}},
		})
	}
	result, err := r.handleRun(ctx, req)
	if err != nil {
		result = r.failed(req, "OPSEM_SEMANTIC_CHECK_FAILED", err.Error())
	}
	return assignOpsemPackageResponse(response, result)
}

func (r *opsemSemanticRunner) handleRun(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	switch req.Kind {
	case "package.primary_switchover.verify_cluster_healthy":
		return r.verifyNoFailures(ctx, req)
	case "package.primary_switchover.verify_candidate_caught_up":
		candidate, err := candidateOrdinal(req.Params)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		state, err := r.state(ctx, candidate)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if state.Lag != 0 || state.Role != "replica" {
			return sagaapi.PackageStepResultResponse{}, fmt.Errorf("candidate %d is not caught up replica: %+v", candidate, state)
		}
		return r.success(req, "candidate is caught up"), nil
	case "package.primary_switchover.move_primary":
		candidate, err := candidateOrdinal(req.Params)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := r.post(ctx, candidate, "/admin/promote", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := r.post(ctx, 0, "/admin/stepdown", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		r.mutations = append(r.mutations, req.Kind)
		return r.success(req, "primary moved to candidate"), nil
	case "package.primary_switchover.update_old_primary", "package.primary_switchover.verify_old_primary_caught_up", "package.primary_switchover.update_candidate":
		return r.catchUpAll(ctx, req)
	case "package.primary_switchover.optional_failback":
		params := opsemStepParams(req.Params)
		if params.ReturnPrimary {
			candidate, err := parseOrdinal(params.Candidate)
			if err != nil {
				return sagaapi.PackageStepResultResponse{}, err
			}
			if err := r.post(ctx, 0, "/admin/promote", nil); err != nil {
				return sagaapi.PackageStepResultResponse{}, err
			}
			if err := r.post(ctx, candidate, "/admin/stepdown", nil); err != nil {
				return sagaapi.PackageStepResultResponse{}, err
			}
			r.mutations = append(r.mutations, req.Kind)
		}
		return r.success(req, "failback policy applied"), nil
	case "package.primary_switchover.verify_final_topology":
		state, err := r.state(ctx, 0)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if state.Role != "primary" {
			return sagaapi.PackageStepResultResponse{}, fmt.Errorf("member 0 role = %s, want primary", state.Role)
		}
		return r.verifyNoFailures(ctx, req)
	case "package.raft_group.verify_quorum", "package.raft_group.verify_final_quorum":
		return r.verifyQuorum(ctx, req)
	case "package.raft_group.update_followers", "package.raft_group.verify_followers_caught_up", "package.raft_group.update_previous_leader":
		return r.catchUpAll(ctx, req)
	case "package.raft_group.transfer_leader":
		if err := r.post(ctx, 1, "/admin/promote", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := r.post(ctx, 0, "/admin/stepdown", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		r.mutations = append(r.mutations, req.Kind)
		return r.success(req, "raft leader transferred"), nil
	case "package.partition_quorum.verify_quorum", "package.partition_quorum.verify_in_sync_replicas", "package.partition_quorum.verify_final":
		return r.verifyPartitionISR(ctx, req)
	case "package.partition_quorum.update_non_leaders", "package.partition_quorum.move_leaders", "package.partition_quorum.update_previous_leaders":
		return r.catchUpAll(ctx, req)
	case "package.slot_aware.verify_coverage", "package.slot_aware.verify_slot_health", "package.slot_aware.verify_final_coverage":
		return r.verifySlotCoverage(ctx, req)
	case "package.slot_aware.failover_replicas":
		if err := r.post(ctx, 1, "/admin/promote", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		r.mutations = append(r.mutations, req.Kind)
		return r.success(req, "slot replica promoted without dropping coverage"), nil
	case "package.slot_aware.update_remaining_members":
		return r.catchUpAll(ctx, req)
	case "package.shard_allocation.verify_allocation", "package.shard_allocation.verify_final":
		return r.verifyShardHealth(ctx, req)
	case "package.shard_allocation.relocate_primaries":
		if err := r.post(ctx, 0, "/admin/drain", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		r.mutations = append(r.mutations, req.Kind)
		return r.success(req, "primary shards relocated away from draining member"), nil
	case "package.shard_allocation.update_holders":
		return r.catchUpAll(ctx, req)
	case "package.shard_allocation.wait_for_rebalance":
		for member := 0; member < 3; member++ {
			if err := r.post(ctx, member, "/admin/recover", nil); err != nil {
				return sagaapi.PackageStepResultResponse{}, err
			}
		}
		r.mutations = append(r.mutations, req.Kind)
		return r.verifyShardHealth(ctx, req)
	default:
		return sagaapi.PackageStepResultResponse{}, fmt.Errorf("unhandled opsem package step %s", req.Kind)
	}
}

func (r *opsemSemanticRunner) verifyNoFailures(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	states, err := r.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	for _, state := range states {
		if len(state.Failures) != 0 {
			return sagaapi.PackageStepResultResponse{}, fmt.Errorf("member %d has failures: %+v", state.Member, state.Failures)
		}
	}
	return r.success(req, "all members have no injected failures"), nil
}

func (r *opsemSemanticRunner) verifyQuorum(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	states, err := r.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	for _, state := range states {
		if !state.Quorum.Healthy || state.Quorum.Available < state.Quorum.Required {
			return sagaapi.PackageStepResultResponse{}, fmt.Errorf("member %d would lose quorum: %+v", state.Member, state.Quorum)
		}
	}
	return r.success(req, "raft quorum remains available"), nil
}

func (r *opsemSemanticRunner) verifyPartitionISR(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	params := opsemStepParams(req.Params)
	minInSync := params.MinInSync
	if minInSync <= 0 {
		minInSync = 2
	}
	states, err := r.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	for _, state := range states {
		for _, partition := range state.Partitions {
			required := partition.MinISR
			if minInSync > required {
				required = minInSync
			}
			if len(partition.ISR) < required {
				return sagaapi.PackageStepResultResponse{}, fmt.Errorf("member %d partition ISR %v below required %d", state.Member, partition.ISR, required)
			}
		}
	}
	return r.success(req, "partition ISR is safe"), nil
}

func (r *opsemSemanticRunner) verifySlotCoverage(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	states, err := r.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	for _, state := range states {
		if !state.Slots.CoverageOK || len(state.Slots.Missing) != 0 {
			return sagaapi.PackageStepResultResponse{}, fmt.Errorf("member %d slot coverage missing: %+v", state.Member, state.Slots)
		}
	}
	return r.success(req, "slot ownership remains complete"), nil
}

func (r *opsemSemanticRunner) verifyShardHealth(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	states, err := r.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	for _, state := range states {
		for _, shard := range state.Shards {
			if shard.Health != "green" && shard.Health != "yellow" {
				return sagaapi.PackageStepResultResponse{}, fmt.Errorf("member %d shard %s health = %s", state.Member, shard.Name, shard.Health)
			}
		}
	}
	return r.success(req, "shard allocation health is acceptable"), nil
}

func (r *opsemSemanticRunner) catchUpAll(ctx context.Context, req pluginapi.PackageStepRequest) (sagaapi.PackageStepResultResponse, error) {
	for member := 0; member < 3; member++ {
		if err := r.post(ctx, member, "/admin/catch-up", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
	}
	r.mutations = append(r.mutations, req.Kind)
	return r.success(req, "members caught up"), nil
}

func (r *opsemSemanticRunner) states(ctx context.Context) ([]opsemStateBody, error) {
	out := make([]opsemStateBody, 0, 3)
	for member := 0; member < 3; member++ {
		state, err := r.state(ctx, member)
		if err != nil {
			return nil, err
		}
		if state.Mode != r.mode {
			return nil, fmt.Errorf("member %d mode = %s, want %s", member, state.Mode, r.mode)
		}
		out = append(out, state)
	}
	return out, nil
}

func (r *opsemSemanticRunner) state(ctx context.Context, member int) (opsemStateBody, error) {
	return opsemHTTPState(ctx, opsemMemberPort(r.portBase, member))
}

func (r *opsemSemanticRunner) post(ctx context.Context, member int, path string, body any) error {
	return opsemHTTPPost(ctx, opsemMemberPort(r.portBase, member), path, body)
}

func (r *opsemSemanticRunner) success(req pluginapi.PackageStepRequest, summary string) sagaapi.PackageStepResultResponse {
	result := map[string]any{
		"ok":      true,
		"service": r.service,
		"mode":    r.mode,
		"kind":    req.Kind,
	}
	body, _ := json.Marshal(result)
	return sagaapi.PackageStepResultResponse{
		Status:  sagaapi.StepStatusSucceeded,
		Result:  body,
		Summary: summary,
		ProviderOperations: []sagaapi.ProviderOperationRef{{
			Provider:    applecontainer.Name,
			Kind:        "opsem.semantic_step",
			ID:          "opsem-" + sanitizeProviderID(req.Context.StepID),
			ObservedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			Description: summary,
		}},
	}
}

func (r *opsemSemanticRunner) failed(req pluginapi.PackageStepRequest, code, summary string) sagaapi.PackageStepResultResponse {
	return sagaapi.PackageStepResultResponse{
		Status:  sagaapi.StepStatusFailed,
		Summary: summary,
		Failure: &sagaapi.StepFailure{
			Code:    code,
			Summary: summary,
		},
		ProviderOperations: []sagaapi.ProviderOperationRef{{
			Provider:    applecontainer.Name,
			Kind:        "opsem.semantic_step",
			ID:          "opsem-failed-" + sanitizeProviderID(req.Context.StepID),
			ObservedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			Description: summary,
		}},
	}
}

type decodedOpsemStepParams struct {
	Candidate     string `json:"candidate"`
	ReturnPrimary bool   `json:"return_primary"`
	MinInSync     int    `json:"min_in_sync"`
}

func opsemStepParams(raw json.RawMessage) decodedOpsemStepParams {
	var params decodedOpsemStepParams
	_ = json.Unmarshal(raw, &params)
	return params
}

func candidateOrdinal(raw json.RawMessage) (int, error) {
	params := opsemStepParams(raw)
	return parseOrdinal(params.Candidate)
}

func parseOrdinal(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("candidate member is required")
	}
	if ordinal, err := strconv.Atoi(value); err == nil {
		return ordinal, nil
	}
	if index := strings.LastIndex(value, "-"); index >= 0 && index < len(value)-1 {
		if ordinal, err := strconv.Atoi(value[index+1:]); err == nil {
			return ordinal, nil
		}
	}
	return 0, fmt.Errorf("candidate %q does not include a member ordinal", value)
}

func assignOpsemPackageResponse(response any, value sagaapi.PackageStepResultResponse) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if response == nil {
		return nil
	}
	return json.Unmarshal(body, response)
}

func sanitizeProviderID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(".", "-", "_", "-", "/", "-", " ", "-")
	value = replacer.Replace(value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "step"
	}
	return value
}

func opsemHTTPState(ctx context.Context, port int) (opsemStateBody, error) {
	body, err := opsemHTTPRequest(ctx, http.MethodGet, port, "/admin/state", nil, http.StatusOK)
	if err != nil {
		return opsemStateBody{}, err
	}
	var out opsemStateBody
	if err := json.Unmarshal(body, &out); err != nil {
		return opsemStateBody{}, err
	}
	return out, nil
}

func opsemHTTPPost(ctx context.Context, port int, path string, body any) error {
	_, err := opsemHTTPRequest(ctx, http.MethodPost, port, path, body, http.StatusOK)
	return err
}

func opsemHTTPRequest(ctx context.Context, method string, port int, path string, body any, want int) ([]byte, error) {
	var rawBody []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rawBody = raw
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		var reader io.Reader
		if rawBody != nil {
			reader = bytes.NewReader(rawBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("content-type", "application/json")
		}
		resp, err := client.Do(req)
		if err == nil {
			raw, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == want {
				return raw, nil
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("status %d body %q", resp.StatusCode, string(raw))
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("opsem %s %s on port %d failed: %w", method, path, port, lastErr)
}
