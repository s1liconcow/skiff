package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

func TestBuiltInProfilesValidateAndExplain(t *testing.T) {
	profiles := BuiltInProfiles()
	if len(profiles) != 7 {
		t.Fatalf("built-in profile count = %d, want 7", len(profiles))
	}
	seen := map[sagaapi.ProfileKind]bool{}
	for _, profile := range profiles {
		if err := ValidateProfile(profile); err != nil {
			t.Fatalf("ValidateProfile(%s): %v", profile.Kind, err)
		}
		explanation, err := ExplainProfile(profile)
		if err != nil {
			t.Fatalf("ExplainProfile(%s): %v", profile.Kind, err)
		}
		if explanation.Risk == "" || explanation.Reversibility == "" || len(explanation.Steps) == 0 {
			t.Fatalf("explanation omitted risk, reversibility, or steps: %+v", explanation)
		}
		seen[profile.Kind] = true
	}
	for _, kind := range BuiltInProfileKinds() {
		if !seen[kind] {
			t.Fatalf("built-in profile kind %q missing", kind)
		}
	}
}

func TestRenderPrimarySwitchoverGraphIncludesProvenanceAndDeterministicOutput(t *testing.T) {
	profile := mustBuiltInProfile(t, sagaapi.ProfilePrimarySwitchoverUpdate)
	req := renderRequest(profile, map[string]json.RawMessage{
		"release_id":     raw(`"rel_20260520"`),
		"candidate":      raw(`"member-2"`),
		"return_primary": raw(`true`),
	})
	first, err := RenderProfile(req)
	if err != nil {
		t.Fatalf("RenderProfile: %v", err)
	}
	second, err := RenderProfile(req)
	if err != nil {
		t.Fatalf("RenderProfile second pass: %v", err)
	}
	firstGraph := mustCanonical(t, first.Graph)
	secondGraph := mustCanonical(t, second.Graph)
	if !bytes.Equal(firstGraph, secondGraph) {
		t.Fatalf("rendered graph is not deterministic\nfirst:  %s\nsecond: %s", firstGraph, secondGraph)
	}
	wantOrder := []string{
		"verify_cluster_healthy",
		"verify_candidate_caught_up",
		"move_primary_to_candidate",
		"update_old_primary",
		"verify_old_primary_caught_up",
		"optional_failback",
		"update_candidate",
		"verify_final_topology",
	}
	if got := nodeIDs(first.Graph.Nodes); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("node order = %#v, want %#v", got, wantOrder)
	}
	if first.Intent.Package == nil || first.Graph.Package == nil {
		t.Fatalf("package provenance missing from intent or graph")
	}
	if first.Graph.Package.Digest != packageDigest() || first.Graph.Package.LockfileDigest != lockfileDigest() {
		t.Fatalf("graph package provenance = %+v", first.Graph.Package)
	}
	if first.Intent.Risk != schema.RiskHigh || first.Intent.Reversibility != schema.PartiallyReversible {
		t.Fatalf("intent risk/reversibility = %s/%s", first.Intent.Risk, first.Intent.Reversibility)
	}
	var switchoverParams struct {
		ReleaseID     string `json:"release_id"`
		Candidate     string `json:"candidate"`
		ReturnPrimary bool   `json:"return_primary"`
	}
	if err := json.Unmarshal(first.Graph.Nodes[2].Params, &switchoverParams); err != nil {
		t.Fatalf("unmarshal switchover params: %v", err)
	}
	if switchoverParams.ReleaseID != "rel_20260520" || switchoverParams.Candidate != "member-2" || !switchoverParams.ReturnPrimary {
		t.Fatalf("switchover params not substituted: %+v", switchoverParams)
	}
}

func TestRenderKafkaPartitionQuorumUpdateGraph(t *testing.T) {
	profile := mustBuiltInProfile(t, sagaapi.ProfilePartitionQuorumRollingUpdate)
	rendered, err := RenderProfile(renderRequest(profile, map[string]json.RawMessage{
		"release_id":         raw(`"rel_20260520"`),
		"partition_selector": raw(`{"topic":"orders"}`),
		"min_in_sync":        raw(`3`),
	}))
	if err != nil {
		t.Fatalf("RenderProfile: %v", err)
	}
	want := []string{
		"verify_partition_quorum",
		"update_non_leader_replicas",
		"verify_isr_after_update",
		"move_partition_leaders",
		"update_previous_leaders",
		"verify_partition_quorum_final",
	}
	if got := nodeIDs(rendered.Graph.Nodes); !reflect.DeepEqual(got, want) {
		t.Fatalf("node order = %#v, want %#v", got, want)
	}
	var params struct {
		PartitionSelector map[string]string `json:"partition_selector"`
		MinInSync         int               `json:"min_in_sync"`
	}
	if err := json.Unmarshal(rendered.Graph.Nodes[0].Params, &params); err != nil {
		t.Fatalf("unmarshal partition params: %v", err)
	}
	if params.PartitionSelector["topic"] != "orders" || params.MinInSync != 3 {
		t.Fatalf("partition params not substituted: %+v", params)
	}
}

func TestRenderJetStreamRaftGroupUpdateGraph(t *testing.T) {
	profile := mustBuiltInProfile(t, sagaapi.ProfileRaftGroupRollingUpdate)
	rendered, err := RenderProfile(renderRequest(profile, map[string]json.RawMessage{
		"release_id":     raw(`"rel_20260520"`),
		"group_selector": raw(`{"stream":"ORDERS"}`),
		"leader_policy":  raw(`"transfer"`),
	}))
	if err != nil {
		t.Fatalf("RenderProfile: %v", err)
	}
	want := []string{
		"verify_raft_quorum",
		"update_followers",
		"verify_followers_caught_up",
		"transfer_raft_leader",
		"update_previous_leader",
		"verify_final_quorum",
	}
	if got := nodeIDs(rendered.Graph.Nodes); !reflect.DeepEqual(got, want) {
		t.Fatalf("node order = %#v, want %#v", got, want)
	}
	var params struct {
		GroupSelector map[string]string `json:"group_selector"`
		LeaderPolicy  string            `json:"leader_policy"`
	}
	if err := json.Unmarshal(rendered.Graph.Nodes[3].Params, &params); err != nil {
		t.Fatalf("unmarshal raft params: %v", err)
	}
	if params.GroupSelector["stream"] != "ORDERS" || params.LeaderPolicy != "transfer" {
		t.Fatalf("raft params not substituted: %+v", params)
	}
}

func TestInvalidParamsFailBeforeDurableWrites(t *testing.T) {
	profile := mustBuiltInProfile(t, sagaapi.ProfilePrimarySwitchoverUpdate)
	store := &countingObjectStore{}
	_, _, err := CreateProfileSaga(context.Background(), store, renderRequest(profile, map[string]json.RawMessage{
		"release_id": raw(`"rel_20260520"`),
	}))
	if err == nil {
		t.Fatalf("CreateProfileSaga succeeded with missing required candidate")
	}
	var validationErr ProfileValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want ProfileValidationError: %v", err, err)
	}
	if store.createCalls != 0 || store.casCalls != 0 {
		t.Fatalf("durable writes happened before param validation: create=%d cas=%d", store.createCalls, store.casCalls)
	}
}

func TestRenderRejectsMissingPackageDigests(t *testing.T) {
	profile := mustBuiltInProfile(t, sagaapi.ProfileRaftGroupRollingUpdate)
	req := renderRequest(profile, map[string]json.RawMessage{
		"release_id": raw(`"rel_20260520"`),
	})
	req.Package.Digest = ""
	req.Package.LockfileDigest = ""
	_, err := RenderProfile(req)
	if err == nil {
		t.Fatalf("RenderProfile succeeded without package provenance digests")
	}
	var validationErr ProfileValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want ProfileValidationError: %v", err, err)
	}
}

func mustBuiltInProfile(t *testing.T, kind sagaapi.ProfileKind) sagaapi.OperationProfile {
	t.Helper()
	profile, ok := BuiltInProfile(kind)
	if !ok {
		t.Fatalf("built-in profile %q missing", kind)
	}
	return profile
}

func renderRequest(profile sagaapi.OperationProfile, params map[string]json.RawMessage) ProfileRenderRequest {
	return ProfileRenderRequest{
		Profile: profile,
		SagaID:  "saga_01JABC",
		Target:  schema.Target{Kind: "StatefulGroup", Name: "orders"},
		Actor:   schema.Actor{ID: "test-agent", Type: "agent"},
		TraceID: "tr_01JABC",
		Params:  params,
		Package: schema.PackageProvenance{
			Name:           "orders-package",
			Ref:            "oci://example.test/orders-package",
			Version:        "1.2.3",
			Digest:         packageDigest(),
			ManifestDigest: manifestDigest(),
			LockfileDigest: lockfileDigest(),
		},
		CreatedAt: time.Date(2026, 5, 20, 3, 0, 0, 0, time.UTC),
	}
}

func raw(value string) json.RawMessage {
	return json.RawMessage(value)
}

func mustCanonical(t *testing.T, value any) []byte {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatalf("canonical marshal: %v", err)
	}
	return body
}

func nodeIDs(nodes []schema.SagaNode) []string {
	out := make([]string, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node.ID)
	}
	return out
}

func packageDigest() string {
	return "sha256:" + strings.Repeat("a", 64)
}

func manifestDigest() string {
	return "sha256:" + strings.Repeat("b", 64)
}

func lockfileDigest() string {
	return "sha256:" + strings.Repeat("c", 64)
}

type countingObjectStore struct {
	createCalls int
	casCalls    int
}

func (s *countingObjectStore) Get(context.Context, string) (*objstore.Object, error) {
	return nil, objstore.ErrNotFound
}

func (s *countingObjectStore) Head(context.Context, string) (*objstore.ObjectMeta, error) {
	return nil, objstore.ErrNotFound
}

func (s *countingObjectStore) Create(context.Context, string, []byte, objstore.PutOptions) (*objstore.ObjectMeta, error) {
	s.createCalls++
	return nil, objstore.ErrAlreadyExists
}

func (s *countingObjectStore) CompareAndSwap(context.Context, string, string, []byte, objstore.PutOptions) (*objstore.ObjectMeta, error) {
	s.casCalls++
	return nil, objstore.ErrPreconditionFailed
}

func (s *countingObjectStore) List(context.Context, string, objstore.ListOptions) ([]objstore.ObjectMeta, error) {
	return nil, nil
}
