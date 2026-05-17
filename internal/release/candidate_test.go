package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestCreateCandidateWritesImmutableCandidateEventAndAudit(t *testing.T) {
	store := memory.New()
	manager := Manager{Store: store, Clock: releaseTestNow}
	result, err := manager.CreateCandidate(context.Background(), candidateRequest())
	if err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	if !result.OK || result.Candidate.CandidateID != "cand_01JCI" || result.Candidate.Artifact.Digest != testDigest() {
		t.Fatalf("unexpected result: %+v", result)
	}
	key, err := paths.ReleaseCandidate("payments-api", "cand_01JCI")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), key); err != nil {
		t.Fatalf("candidate object not written: %v", err)
	}
	if _, err := manager.CreateCandidate(context.Background(), candidateRequest()); !errors.Is(err, objstore.ErrAlreadyExists) && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second candidate create error = %v, want already exists", err)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: releaseTestNow})
	if err != nil {
		t.Fatal(err)
	}
	items, err := log.List(context.Background(), events.Scope{Kind: events.ScopeService, Service: "payments-api"}, events.ListOptions{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(items) != 1 || items[0].Type != "release_candidate.created" {
		t.Fatalf("unexpected events: %+v", items)
	}
	audits, err := store.List(context.Background(), "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit count = %d, want 1", len(audits))
	}
}

func TestCreateCandidateRequiresDigestPinnedArtifact(t *testing.T) {
	req := candidateRequest()
	req.Artifact.Digest = "latest"
	_, err := (Manager{Store: memory.New(), Clock: releaseTestNow}).CreateCandidate(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("CreateCandidate error = %v, want sha256 digest validation", err)
	}
}

func candidateRequest() CandidateCreateRequest {
	return CandidateCreateRequest{
		CandidateID: "cand_01JCI",
		Service:     "payments-api",
		Env:         "staging",
		ReleaseID:   "rel_01JCI",
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "registry.example.com/payments-api@sha256:" + strings.TrimPrefix(testDigest(), "sha256:"),
			Digest: testDigest(),
		},
		Git: schema.GitMetadata{Repo: "github.com/acme/payments", SHA: "abc123"},
		CI:  schema.CIMetadata{Provider: "github-actions", RunID: "123"},
		Checks: []schema.EvidenceCheck{
			{Name: "tests", Status: "passed"},
			{Name: "contract", Status: "passed"},
			{Name: "policy", Status: "passed"},
			{Name: "scan", Status: "passed"},
		},
		Actor:   schema.Actor{ID: "ci", Type: "ci"},
		TraceID: "tr_candidate",
	}
}

func releaseTestNow() time.Time {
	return time.Date(2026, 5, 17, 3, 0, 0, 0, time.UTC)
}

func testDigest() string {
	return "sha256:" + strings.Repeat("a", 64)
}
