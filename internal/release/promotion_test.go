package release

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestPromoteValidatesEvidenceAndWritesOperationRecords(t *testing.T) {
	store := memory.New()
	manager := Manager{Store: store, Clock: releaseTestNow}
	if _, err := manager.CreateCandidate(context.Background(), candidateRequest()); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	seedServiceControl(t, store, "payments-api", "staging", "rel_01JCI", releaseTestNow().Add(-2*time.Hour))

	result, err := manager.Promote(context.Background(), PromotionRequest{
		Service:           "payments-api",
		FromEnv:           "staging",
		ToEnv:             "prod",
		CandidateID:       "cand_01JCI",
		OperationID:       "op_promote",
		ApprovalID:        "approval_123",
		MinStableDuration: time.Hour,
		Actor:             schema.Actor{ID: "ci", Type: "ci"},
		TraceID:           "tr_promote",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if !result.OK || result.Artifact.Digest != testDigest() || result.OperationIntent == nil || result.OperationControl == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.OperationIntent.Risk != schema.RiskHigh || result.OperationIntent.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected intent risk/reversibility: %+v", result.OperationIntent)
	}
	var params map[string]string
	if err := json.Unmarshal(result.OperationIntent.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params["artifact_digest"] != testDigest() || params["from_env"] != "staging" || params["to_env"] != "prod" {
		t.Fatalf("unexpected params: %+v", params)
	}
	intentKey, err := paths.OperationIntent("payments-api", "op_promote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), intentKey); err != nil {
		t.Fatalf("operation intent not written: %v", err)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: releaseTestNow})
	if err != nil {
		t.Fatal(err)
	}
	items, err := log.List(context.Background(), events.Scope{Kind: events.ScopeOperation, Service: "payments-api", Operation: "op_promote"}, events.ListOptions{})
	if err != nil {
		t.Fatalf("list operation events: %v", err)
	}
	if len(items) != 1 || items[0].Type != "release.promote.requested" {
		t.Fatalf("unexpected promotion events: %+v", items)
	}
	audits, err := store.List(context.Background(), "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) != 2 {
		t.Fatalf("audit count = %d, want candidate + promotion audit", len(audits))
	}
}

func TestPromoteBlocksMissingEvidenceWithoutOperationIntent(t *testing.T) {
	store := memory.New()
	req := candidateRequest()
	req.Checks = req.Checks[:3]
	manager := Manager{Store: store, Clock: releaseTestNow}
	if _, err := manager.CreateCandidate(context.Background(), req); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	result, err := manager.Promote(context.Background(), PromotionRequest{
		Service:     "payments-api",
		FromEnv:     "staging",
		ToEnv:       "dev",
		CandidateID: "cand_01JCI",
		OperationID: "op_promote_missing_evidence",
		Actor:       schema.Actor{ID: "ci", Type: "ci"},
		TraceID:     "tr_promote_missing",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result.OK || !hasRequirementCode(result.Requirements, "PROMOTION_EVIDENCE_MISSING") {
		t.Fatalf("expected missing evidence requirement failure: %+v", result.Requirements)
	}
	key, err := paths.OperationIntent("payments-api", "op_promote_missing_evidence")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), key); err == nil {
		t.Fatalf("operation intent was written despite failed requirements")
	}
}

func TestPromoteBlocksInsufficientStableDuration(t *testing.T) {
	store := memory.New()
	manager := Manager{Store: store, Clock: releaseTestNow}
	if _, err := manager.CreateCandidate(context.Background(), candidateRequest()); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	seedServiceControl(t, store, "payments-api", "staging", "rel_01JCI", releaseTestNow().Add(-5*time.Minute))

	result, err := manager.Promote(context.Background(), PromotionRequest{
		Service:           "payments-api",
		FromEnv:           "staging",
		ToEnv:             "dev",
		CandidateID:       "cand_01JCI",
		OperationID:       "op_promote_too_soon",
		MinStableDuration: time.Hour,
		Actor:             schema.Actor{ID: "ci", Type: "ci"},
		TraceID:           "tr_promote_too_soon",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result.OK || !hasRequirementCode(result.Requirements, "STABLE_DURATION_NOT_MET") {
		t.Fatalf("expected stable duration failure: %+v", result.Requirements)
	}
}

func TestPromoteBlocksSourceReleaseThatIsNotStable(t *testing.T) {
	store := memory.New()
	manager := Manager{Store: store, Clock: releaseTestNow}
	if _, err := manager.CreateCandidate(context.Background(), candidateRequest()); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	seedServiceControl(t, store, "payments-api", "staging", "rel_other", releaseTestNow().Add(-2*time.Hour))

	result, err := manager.Promote(context.Background(), PromotionRequest{
		Service:           "payments-api",
		FromEnv:           "staging",
		ToEnv:             "dev",
		CandidateID:       "cand_01JCI",
		OperationID:       "op_promote_unknown_release",
		MinStableDuration: time.Hour,
		Actor:             schema.Actor{ID: "ci", Type: "ci"},
		TraceID:           "tr_promote_unknown_release",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result.OK || !hasRequirementCode(result.Requirements, "SOURCE_RELEASE_NOT_STABLE") {
		t.Fatalf("expected source release not stable failure: %+v", result.Requirements)
	}
}

func TestPromotionMarkdownIncludesRequirementsAndCommands(t *testing.T) {
	store := memory.New()
	manager := Manager{Store: store, Clock: releaseTestNow}
	if _, err := manager.CreateCandidate(context.Background(), candidateRequest()); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	result, err := manager.Promote(context.Background(), PromotionRequest{
		Service:     "payments-api",
		FromEnv:     "staging",
		ToEnv:       "dev",
		CandidateID: "cand_01JCI",
		DryRun:      true,
		TraceID:     "tr_promote_markdown",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	for _, want := range []string{"# Promotion Plan", "- [x] check tests passed", "skiff deploy <spec> --release-id rel_01JCI", testDigest()} {
		if !strings.Contains(result.PlanMarkdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, result.PlanMarkdown)
		}
	}
}

func hasRequirementCode(requirements []PromotionRequirement, code string) bool {
	for _, requirement := range requirements {
		if requirement.Code == code {
			return true
		}
	}
	return false
}

func seedServiceControl(t *testing.T, store objstore.ObjectStore, service, env, stableRelease string, updatedAt time.Time) {
	t.Helper()
	control := schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        service,
		Env:            env,
		DesiredRelease: stableRelease,
		StableRelease:  stableRelease,
		Version:        1,
		UpdatedAt:      canonical.Time(updatedAt),
		UpdatedBy:      schema.Actor{ID: "seed", Type: "test"},
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		t.Fatalf("marshal service control: %v", err)
	}
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("create service control: %v", err)
	}
}
