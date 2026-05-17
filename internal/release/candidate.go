package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Manager struct {
	Store      objstore.ObjectStore
	Clock      func() time.Time
	Authorizer authz.Authorizer
}

type CandidateCreateRequest struct {
	CandidateID string
	Service     string
	Env         string
	ReleaseID   string
	Artifact    schema.ArtifactRef
	Git         schema.GitMetadata
	CI          schema.CIMetadata
	Checks      []schema.EvidenceCheck
	SBOM        []schema.EvidenceRef
	Provenance  []schema.EvidenceRef
	Actor       schema.Actor
	TraceID     string
	Annotations map[string]string
}

type CandidateDocument struct {
	Key       string                  `json:"key"`
	Meta      objstore.ObjectMeta     `json:"meta"`
	Candidate schema.ReleaseCandidate `json:"candidate"`
}

type CandidateCreateResult struct {
	OK        bool                    `json:"ok"`
	Candidate schema.ReleaseCandidate `json:"candidate"`
	Key       string                  `json:"key"`
	TraceID   string                  `json:"trace_id,omitempty"`
	Events    []events.Event          `json:"events,omitempty"`
}

type CandidateError struct {
	Code    string
	Summary string
	Err     error
}

func (e *CandidateError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return e.Summary
	}
	return e.Summary + ": " + e.Err.Error()
}

func (e *CandidateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (m Manager) CreateCandidate(ctx context.Context, req CandidateCreateRequest) (*CandidateCreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.Store == nil {
		return nil, candidateError("RELEASE_CANDIDATE_INVALID", "object store is required", nil)
	}
	now := m.now()
	req = normalizeCandidateRequest(req, now)
	candidate := schema.ReleaseCandidate{
		SchemaVersion: schema.Version,
		CandidateID:   req.CandidateID,
		Service:       req.Service,
		Env:           req.Env,
		ReleaseID:     req.ReleaseID,
		Artifact:      req.Artifact,
		Git:           req.Git,
		CI:            req.CI,
		Checks:        normalizeChecks(req.Checks),
		SBOM:          append([]schema.EvidenceRef(nil), req.SBOM...),
		Provenance:    append([]schema.EvidenceRef(nil), req.Provenance...),
		CreatedAt:     canonical.Time(now),
		CreatedBy:     req.Actor,
		TraceID:       req.TraceID,
		Annotations:   cloneStringMap(req.Annotations),
	}
	if err := validateCandidate(candidate); err != nil {
		return nil, err
	}
	key, err := paths.ReleaseCandidate(candidate.Service, candidate.CandidateID)
	if err != nil {
		return nil, candidateError("RELEASE_CANDIDATE_INVALID", err.Error(), err)
	}
	body, err := canonical.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	meta, err := m.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	if err != nil {
		code := "RELEASE_CANDIDATE_CREATE_FAILED"
		if errors.Is(err, objstore.ErrAlreadyExists) {
			code = "RELEASE_CANDIDATE_EXISTS"
		}
		return nil, candidateError(code, "release candidate object could not be created", err)
	}
	result := &CandidateCreateResult{
		OK:        true,
		Candidate: candidate,
		Key:       key,
		TraceID:   candidate.TraceID,
	}
	log, err := events.NewLog(events.Options{Store: m.Store, Clock: m.now})
	if err != nil {
		return result, err
	}
	event := events.NewServiceEvent(candidate.Service, "release_candidate.created", "release candidate "+candidate.CandidateID+" created", m.now(), candidate.TraceID+candidate.CandidateID+"candidate")
	event.TraceID = candidate.TraceID
	event.Actor = &candidate.CreatedBy
	event.Facts = []schema.Fact{
		{Type: "candidate_id", Message: candidate.CandidateID},
		{Type: "release_id", Message: candidate.ReleaseID},
		{Type: "artifact_digest", Message: candidate.Artifact.Digest},
	}
	if _, err := log.Append(ctx, event); err == nil {
		result.Events = append(result.Events, event)
	}
	audit := events.NewAuditRecord(candidate.CreatedBy, schema.Target{Kind: "service", Name: candidate.Service}, "release_candidate.create", "created release candidate "+candidate.CandidateID, candidate.TraceID, m.now(), candidate.CandidateID+"audit")
	audit.Risk = schema.RiskLow
	audit.Data = rawJSON(map[string]string{
		"candidate_id":    candidate.CandidateID,
		"candidate_key":   meta.Key,
		"artifact_digest": candidate.Artifact.Digest,
		"release_id":      candidate.ReleaseID,
	})
	_, _ = log.AppendAudit(ctx, audit)
	return result, nil
}

func (m Manager) ReadCandidate(ctx context.Context, service, candidateID string) (*CandidateDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.Store == nil {
		return nil, candidateError("RELEASE_CANDIDATE_INVALID", "object store is required", nil)
	}
	key, err := paths.ReleaseCandidate(service, candidateID)
	if err != nil {
		return nil, candidateError("RELEASE_CANDIDATE_INVALID", err.Error(), err)
	}
	object, err := m.Store.Get(ctx, key)
	if err != nil {
		code := "RELEASE_CANDIDATE_READ_FAILED"
		summary := "release candidate object could not be read"
		if errors.Is(err, objstore.ErrNotFound) {
			code = "RELEASE_CANDIDATE_NOT_FOUND"
			summary = "release candidate object was not found"
		}
		return nil, candidateError(code, summary, err)
	}
	var candidate schema.ReleaseCandidate
	if err := canonical.UnmarshalStrict(object.Body, &candidate); err != nil {
		return nil, candidateError("INVALID_RELEASE_CANDIDATE", "release candidate object is not valid", err)
	}
	if err := validateCandidate(candidate); err != nil {
		return nil, err
	}
	if candidate.Service != service {
		return nil, candidateError("INVALID_RELEASE_CANDIDATE", fmt.Sprintf("candidate names service %q, expected %q", candidate.Service, service), nil)
	}
	return &CandidateDocument{
		Key:       key,
		Meta:      metaFromObject(object),
		Candidate: candidate,
	}, nil
}

func normalizeCandidateRequest(req CandidateCreateRequest, now time.Time) CandidateCreateRequest {
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	req.CandidateID = strings.TrimSpace(req.CandidateID)
	req.ReleaseID = strings.TrimSpace(req.ReleaseID)
	req.Artifact.Type = strings.TrimSpace(req.Artifact.Type)
	req.Artifact.URI = strings.TrimSpace(req.Artifact.URI)
	req.Artifact.Digest = strings.TrimSpace(req.Artifact.Digest)
	if req.Artifact.Type == "" {
		req.Artifact.Type = "oci"
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff-cli", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(now, req.Service+"candidate")
	}
	if req.CandidateID == "" {
		req.CandidateID = "cand_" + events.NewID(now, req.TraceID+req.Service)
	}
	return req
}

func validateCandidate(candidate schema.ReleaseCandidate) error {
	if candidate.SchemaVersion != schema.Version {
		return candidateError("INVALID_RELEASE_CANDIDATE", fmt.Sprintf("unsupported release candidate schema version %q", candidate.SchemaVersion), nil)
	}
	if err := paths.ValidateID("candidate", candidate.CandidateID); err != nil {
		return candidateError("RELEASE_CANDIDATE_INVALID", err.Error(), err)
	}
	if err := paths.ValidateName("service", candidate.Service); err != nil {
		return candidateError("RELEASE_CANDIDATE_INVALID", err.Error(), err)
	}
	if err := paths.ValidateName("env", candidate.Env); err != nil {
		return candidateError("RELEASE_CANDIDATE_INVALID", err.Error(), err)
	}
	if candidate.ReleaseID != "" {
		if err := paths.ValidateID("release", candidate.ReleaseID); err != nil {
			return candidateError("RELEASE_CANDIDATE_INVALID", err.Error(), err)
		}
	}
	if candidate.Artifact.URI == "" {
		return candidateError("RELEASE_CANDIDATE_INVALID", "artifact URI is required", nil)
	}
	if !isSHA256Digest(candidate.Artifact.Digest) {
		return candidateError("RELEASE_CANDIDATE_INVALID", "artifact digest must use sha256:<64 hex chars>", nil)
	}
	if candidate.CreatedAt == "" {
		return candidateError("RELEASE_CANDIDATE_INVALID", "created_at is required", nil)
	}
	if candidate.CreatedBy.ID == "" {
		return candidateError("RELEASE_CANDIDATE_INVALID", "created_by.id is required", nil)
	}
	return nil
}

func normalizeChecks(checks []schema.EvidenceCheck) []schema.EvidenceCheck {
	if len(checks) == 0 {
		return nil
	}
	out := append([]schema.EvidenceCheck(nil), checks...)
	for i := range out {
		out[i].Name = strings.TrimSpace(strings.ToLower(out[i].Name))
		out[i].Status = strings.TrimSpace(strings.ToLower(out[i].Status))
		out[i].URL = strings.TrimSpace(out[i].URL)
		out[i].Summary = strings.TrimSpace(out[i].Summary)
		out[i].CompletedAt = strings.TrimSpace(out[i].CompletedAt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Status < out[j].Status
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func isSHA256Digest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, c := range value[len("sha256:"):] {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

func (m Manager) now() time.Time {
	if m.Clock != nil {
		return m.Clock().UTC()
	}
	return time.Now().UTC()
}

func candidateError(code, summary string, err error) error {
	return &CandidateError{Code: code, Summary: summary, Err: err}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func rawJSON(value any) []byte {
	body, _ := json.Marshal(value)
	return body
}
