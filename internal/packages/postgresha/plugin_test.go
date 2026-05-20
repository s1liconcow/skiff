package postgresha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/s1liconcow/skiff/pkg/pluginapi"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

func TestPrimarySwitchoverFailbackAgainstAdminEndpoints(t *testing.T) {
	cluster := newTestCluster(t)
	params := cluster.params(t, map[string]any{
		"release_id":     "rel_20260520",
		"candidate":      "1",
		"return_primary": true,
	})
	steps := []string{
		"package.primary_switchover.verify_cluster_healthy",
		"package.primary_switchover.verify_candidate_caught_up",
		"package.primary_switchover.move_primary",
		"package.primary_switchover.update_old_primary",
		"package.primary_switchover.verify_old_primary_caught_up",
		"package.primary_switchover.optional_failback",
		"package.primary_switchover.update_candidate",
		"package.primary_switchover.verify_final_topology",
	}
	for _, kind := range steps {
		resp := runStep(t, cluster, kind, params)
		if resp.Status != sagaapi.StepStatusSucceeded {
			t.Fatalf("%s status = %s failure=%+v summary=%s", kind, resp.Status, resp.Failure, resp.Summary)
		}
	}
	states := cluster.snapshot()
	if states[0].Role != "primary" || states[1].Role != "replica" || states[0].Lag != 0 || states[1].Lag != 0 {
		t.Fatalf("unexpected final topology: %+v", states)
	}
	if cluster.promotions[1] != 1 || cluster.promotions[0] != 1 || cluster.stepdowns[0] != 1 || cluster.stepdowns[1] != 1 {
		t.Fatalf("expected switchover and failback mutations, promotions=%+v stepdowns=%+v", cluster.promotions, cluster.stepdowns)
	}
}

func TestReplicaLagBlocksPlannedSwitchoverBeforeMutation(t *testing.T) {
	cluster := newTestCluster(t)
	cluster.setLag(1, 4096, "replica-lag-too-high")
	params := cluster.params(t, map[string]any{
		"release_id":              "rel_20260520",
		"candidate":               "1",
		"return_primary":          true,
		"maxReplicaLagBytes":      0,
		"package_specific_reason": "ignored by test helper before JSON marshal",
	})
	deleteJSONField(t, &params, "package_specific_reason")

	resp := runStep(t, cluster, "package.primary_switchover.verify_candidate_caught_up", params)
	if resp.Status != sagaapi.StepStatusFailed || resp.Failure == nil || resp.Failure.Code != "POSTGRES_HA_REPLICA_LAG_UNSAFE" {
		t.Fatalf("unexpected lag gate response: %+v", resp)
	}
	resp = runStep(t, cluster, "package.primary_switchover.move_primary", params)
	if resp.Status != sagaapi.StepStatusFailed || resp.Failure == nil || resp.Failure.Code != "POSTGRES_HA_REPLICA_LAG_UNSAFE" {
		t.Fatalf("planned switchover should fail before promotion: %+v", resp)
	}
	if len(cluster.promotions) != 0 || len(cluster.stepdowns) != 0 {
		t.Fatalf("unsafe switchover mutated cluster: promotions=%+v stepdowns=%+v", cluster.promotions, cluster.stepdowns)
	}
}

func TestCommandEnvelopeExecutesPackageStep(t *testing.T) {
	cluster := newTestCluster(t)
	params := cluster.params(t, map[string]any{
		"release_id": "rel_20260520",
		"candidate":  "1",
	})
	request := packageStepRequest("postgres.verify_replica_lag", params)
	body, err := json.Marshal(map[string]any{
		"hook":    pluginapi.HookPackageStep,
		"request": request,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), bytes.NewReader(body), &stdout, &stderr, Options{Client: cluster.client()})
	if code != 0 {
		t.Fatalf("Execute exit=%d stderr=%s", code, stderr.String())
	}
	var resp sagaapi.PackageStepResultResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Status != sagaapi.StepStatusSucceeded {
		t.Fatalf("unexpected command response: %+v", resp)
	}
}

func TestCommandEnvelopeHandlesDoctorChecksHook(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"hook": pluginapi.HookDoctorChecks,
		"request": pluginapi.DoctorChecksRequest{
			Manifest: pluginapi.Manifest{
				APIVersion: pluginapi.APIVersion,
				Kind:       pluginapi.KindPlugin,
				Name:       "postgres-ha",
				Version:    "1.0.0",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), bytes.NewReader(body), &stdout, &stderr, Options{})
	if code != 0 {
		t.Fatalf("Execute exit=%d stderr=%s", code, stderr.String())
	}
	var resp pluginapi.DoctorChecksResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if len(resp.Diagnostics) != 0 || len(resp.Findings) != 0 {
		t.Fatalf("unexpected doctor response: %+v", resp)
	}
}

type testCluster struct {
	t          *testing.T
	mu         sync.Mutex
	states     map[int]adminState
	promotions map[int]int
	stepdowns  map[int]int
}

func newTestCluster(t *testing.T) *testCluster {
	t.Helper()
	c := &testCluster{
		t:          t,
		states:     map[int]adminState{},
		promotions: map[int]int{},
		stepdowns:  map[int]int{},
	}
	for i := 0; i < 3; i++ {
		role := "replica"
		if i == 0 {
			role = "primary"
		}
		c.states[i] = adminState{
			Mode:     "primary-replica",
			Member:   i,
			Members:  3,
			Role:     role,
			Term:     1,
			Leader:   0,
			Lag:      0,
			Failures: map[string]string{},
		}
	}
	return c
}

func (c *testCluster) client() *http.Client {
	return &http.Client{Transport: c}
}

func (c *testCluster) RoundTrip(r *http.Request) (*http.Response, error) {
	memberValue := strings.TrimPrefix(r.URL.Host, "member-")
	member, err := strconv.Atoi(memberValue)
	if err != nil {
		return nil, fmt.Errorf("unexpected test member host %q", r.URL.Host)
	}
	status, body := c.handleMember(member, r)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(payload)),
		Request:    r,
	}, nil
}

func (c *testCluster) handleMember(member int, r *http.Request) (int, any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/state":
		return http.StatusOK, c.states[member]
	case r.Method == http.MethodPost && r.URL.Path == "/admin/promote":
		state := c.states[member]
		state.Role = "primary"
		state.Leader = member
		state.Term++
		c.states[member] = state
		c.promotions[member]++
		return http.StatusOK, adminMutationResponse{OK: true, Action: "promote", State: state}
	case r.Method == http.MethodPost && r.URL.Path == "/admin/stepdown":
		state := c.states[member]
		state.Role = "replica"
		state.Term++
		c.states[member] = state
		c.stepdowns[member]++
		return http.StatusOK, adminMutationResponse{OK: true, Action: "stepdown", State: state}
	case r.Method == http.MethodPost && r.URL.Path == "/admin/catch-up":
		state := c.states[member]
		state.Lag = 0
		state.Failures = map[string]string{}
		c.states[member] = state
		return http.StatusOK, adminMutationResponse{OK: true, Action: "catch-up", State: state}
	default:
		return http.StatusNotFound, map[string]string{"error": "not found"}
	}
}

func (c *testCluster) params(t *testing.T, values map[string]any) json.RawMessage {
	t.Helper()
	urls := map[string]string{}
	for member := range c.states {
		urls[strconv.Itoa(member)] = "http://member-" + strconv.Itoa(member)
	}
	values["member_admin_urls"] = urls
	if _, ok := values["maxReplicaLagBytes"]; !ok {
		values["maxReplicaLagBytes"] = 0
	}
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return body
}

func (c *testCluster) setLag(member int, lag int64, failure string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.states[member]
	state.Lag = lag
	if state.Failures == nil {
		state.Failures = map[string]string{}
	}
	if failure != "" {
		state.Failures[failure] = "injected"
	}
	c.states[member] = state
}

func (c *testCluster) snapshot() map[int]adminState {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int]adminState, len(c.states))
	for k, v := range c.states {
		out[k] = v
	}
	return out
}

func runStep(t *testing.T, cluster *testCluster, kind string, params json.RawMessage) sagaapi.PackageStepResultResponse {
	t.Helper()
	resp, ok := RunPackageStep(context.Background(), packageStepRequest(kind, params), Options{Client: cluster.client()}).(sagaapi.PackageStepResultResponse)
	if !ok {
		t.Fatalf("unexpected response type")
	}
	return resp
}

func packageStepRequest(kind string, params json.RawMessage) pluginapi.PackageStepRequest {
	return pluginapi.PackageStepRequest{
		Manifest: pluginapi.Manifest{
			APIVersion: pluginapi.APIVersion,
			Kind:       pluginapi.KindPlugin,
			Name:       "postgres-ha",
			Version:    "1.0.0",
		},
		PackageStepRequest: sagaapi.PackageStepRequest{
			SchemaVersion: sagaapi.PackageStepSchemaVersion,
			Phase:         sagaapi.StepPhaseRun,
			Kind:          kind,
			Context: sagaapi.PackageStepContext{
				Target:      "payments-db",
				Service:     "payments-db",
				Env:         "test",
				OperationID: "op_postgres_test",
				SagaID:      "saga_postgres_test",
				StepID:      strings.ReplaceAll(kind, ".", "_"),
				TraceID:     "tr_postgres_test",
			},
			Params: params,
		},
	}
}

func deleteJSONField(t *testing.T, raw *json.RawMessage, field string) {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal(*raw, &values); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	delete(values, field)
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("re-marshal params: %v", err)
	}
	*raw = body
}
