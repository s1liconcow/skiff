package skiffd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
	stateindex "github.com/s1liconcow/skiff/internal/index"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

func TestStatefulReplaceMemberRouteCreatesAndRunsSaga(t *testing.T) {
	store := memory.New()
	seedSkiffdStatefulMember(t, store)
	server, err := New(Options{
		Config:      config.Config{Env: "dev", Provider: "fake"},
		ObjectStore: store,
		Index:       NewStaticIndex(Snapshot{Ready: true, Generation: 1, RefreshedAt: time.Now().UTC()}),
		Provider:    fake.New(),
		Clock:       func() time.Time { return time.Date(2026, 5, 18, 3, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	body := bytes.NewBufferString(`{"group":"orders-stream","member":0,"reason":"health failed"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/stateful/replace-member?format=json", body)
	req.Header.Set("X-Skiff-Trace-Id", "tr_skiffd_replace")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK     bool                  `json:"ok"`
		Result statefulSagaAPIResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if !got.OK || got.Result.Status != schema.SagaSucceeded || got.Result.NextAction != "complete" {
		t.Fatalf("unexpected response: %+v", got)
	}
	member, err := state.NewClient(store).GetStatefulMemberControl(context.Background(), "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.InstanceID != "fake-orders-stream-0-gen-2" || member.Control.Generation != 2 {
		t.Fatalf("member was not replaced by skiffd route: %+v", member.Control)
	}
}

func TestStatefulReplaceMemberRouteRequiresProdApproval(t *testing.T) {
	store := memory.New()
	server, err := New(Options{
		Config:      config.Config{Env: "prod", Provider: "fake"},
		ObjectStore: store,
		Index:       NewStaticIndex(Snapshot{Ready: true, Generation: 1, RefreshedAt: time.Now().UTC()}),
		Provider:    fake.New(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/stateful/replace-member?format=json", bytes.NewBufferString(`{"group":"orders-stream","member":0}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if got.Code != "APPROVAL_REQUIRED" || len(got.RecommendedActions) != 1 || !got.RecommendedActions[0].Mutating {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestStatefulStatusAndDoctorRoutesUseIndexedGroups(t *testing.T) {
	store := memory.New()
	server, err := New(Options{
		Config:      config.Config{Env: "prod", Provider: "aws", Region: "us-west-2", StateBucket: "s3://skiff-state-prod"},
		ObjectStore: store,
		Index: NewStaticIndex(Snapshot{
			Ready:       true,
			Generation:  9,
			RefreshedAt: time.Date(2026, 5, 18, 3, 45, 0, 0, time.UTC),
			StatefulGroups: []stateindex.StatefulGroupSummary{{
				Group:    "orders-stream",
				Env:      "prod",
				Replicas: 1,
				Members: []stateindex.StatefulMemberSummary{{
					Member:             0,
					Generation:         1,
					ExpectedGeneration: 2,
					InstanceID:         "i-old",
					ExpectedInstanceID: "i-new",
					VolumeID:           "vol-wrong",
					ExpectedVolumeID:   "vol-0",
					DNSName:            "",
					ExpectedDNSName:    "orders-stream-0.internal",
					Phase:              state.StatefulMemberFailed,
					UpdatedAt:          "2026-05-18T03:44:00Z",
				}},
			}},
		}),
		Provider: fake.New(),
		Clock:    func() time.Time { return time.Date(2026, 5, 18, 3, 46, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/v1/status?service=orders-stream&format=json", nil)
	statusReq.Header.Set("X-Skiff-Trace-Id", "tr_skiffd_stateful_status")
	statusRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status route = %d, body = %s", statusRec.Code, statusRec.Body.String())
	}
	var statusBody struct {
		OK     bool                 `json:"ok"`
		Status servicestatus.Result `json:"status"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status response: %v\n%s", err, statusRec.Body.String())
	}
	if !statusBody.OK || statusBody.Status.Mode != config.ModeAPI || len(statusBody.Status.StatefulGroups) != 1 || statusBody.Status.StatefulGroups[0].Health != "degraded" {
		t.Fatalf("unexpected status response: %+v", statusBody)
	}
	if !hasStatefulStatusFinding(statusBody.Status.StatefulGroups[0], "STATEFUL_RUNNER_STALE") || !hasStatefulStatusFinding(statusBody.Status.StatefulGroups[0], "STATEFUL_MEMBER_VOLUME_MISMATCH") {
		t.Fatalf("missing stateful status findings: %+v", statusBody.Status.StatefulGroups[0].Findings)
	}

	doctorReq := httptest.NewRequest(http.MethodGet, "/v1/doctor?service=orders-stream&format=json", nil)
	doctorReq.Header.Set("X-Skiff-Trace-Id", "tr_skiffd_stateful_doctor")
	doctorRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(doctorRec, doctorReq)
	if doctorRec.Code != http.StatusOK {
		t.Fatalf("doctor route = %d, body = %s", doctorRec.Code, doctorRec.Body.String())
	}
	var doctorBody struct {
		OK     bool                 `json:"ok"`
		Doctor servicedoctor.Result `json:"doctor"`
	}
	if err := json.Unmarshal(doctorRec.Body.Bytes(), &doctorBody); err != nil {
		t.Fatalf("decode doctor response: %v\n%s", err, doctorRec.Body.String())
	}
	if !doctorBody.OK || doctorBody.Doctor.TraceID != "tr_skiffd_stateful_doctor" || doctorBody.Doctor.Health != "degraded" {
		t.Fatalf("unexpected doctor response: %+v", doctorBody)
	}
	if !hasDoctorFinding(doctorBody.Doctor, "STATEFUL_RUNNER_STALE") || !hasDoctorAction(doctorBody.Doctor, "orders-stream_stateful_replace_member", true) {
		t.Fatalf("missing stateful doctor diagnostics: %+v", doctorBody.Doctor)
	}
}

func seedSkiffdStatefulMember(t *testing.T, store *memory.Store) {
	t.Helper()
	client := state.NewClient(store)
	ctx := context.Background()
	if _, err := client.CreateStatefulGroupControl(ctx, schema.StatefulGroupControl{
		Group: "orders-stream",
		Env:   "dev",
		Members: []schema.StatefulMemberSummary{
			{Member: 0, Generation: 1, InstanceID: "i-old", VolumeID: "vol-0", DNSName: "orders-stream-0.internal", Phase: state.StatefulMemberReady},
		},
		Replicas:  1,
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := client.CreateStatefulMemberControl(ctx, schema.StatefulMemberControl{
		Group:      "orders-stream",
		Env:        "dev",
		Member:     0,
		Zone:       "us-west-2a",
		InstanceID: "i-old",
		VolumeID:   "vol-0",
		DNSName:    "orders-stream-0.internal",
		Generation: 1,
		Phase:      state.StatefulMemberReady,
		UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
}

func hasStatefulStatusFinding(group servicestatus.StatefulGroup, code string) bool {
	for _, finding := range group.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasDoctorFinding(result servicedoctor.Result, code string) bool {
	for _, finding := range result.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasDoctorAction(result servicedoctor.Result, id string, mutating bool) bool {
	for _, action := range result.RecommendedActions {
		if action.ID == id && action.Mutating == mutating {
			return true
		}
	}
	return false
}
