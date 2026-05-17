package release

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

var DefaultPromotionRequiredChecks = []string{"tests", "contract", "policy", "scan"}

type PromotionRequest struct {
	Service           string
	FromEnv           string
	ToEnv             string
	CandidateID       string
	OperationID       string
	ApprovalID        string
	RequiredChecks    []string
	MinStableDuration time.Duration
	DryRun            bool
	Actor             schema.Actor
	TraceID           string
}

type PromotionRequirement struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

type PromotionResult struct {
	OK                 bool                      `json:"ok"`
	DryRun             bool                      `json:"dry_run,omitempty"`
	Service            string                    `json:"service"`
	FromEnv            string                    `json:"from_env"`
	ToEnv              string                    `json:"to_env"`
	CandidateID        string                    `json:"candidate_id"`
	ReleaseID          string                    `json:"release_id,omitempty"`
	OperationID        string                    `json:"operation_id,omitempty"`
	ApprovalID         string                    `json:"approval_id,omitempty"`
	TraceID            string                    `json:"trace_id,omitempty"`
	Artifact           schema.ArtifactRef        `json:"artifact"`
	Candidate          *schema.ReleaseCandidate  `json:"candidate,omitempty"`
	Requirements       []PromotionRequirement    `json:"requirements"`
	OperationIntent    *schema.OperationIntent   `json:"operation_intent,omitempty"`
	OperationControl   *schema.OperationControl  `json:"operation_control,omitempty"`
	Events             []events.Event            `json:"events,omitempty"`
	PlanMarkdown       string                    `json:"plan_markdown,omitempty"`
	NextCommands       []string                  `json:"next_commands,omitempty"`
	RecommendedActions []PromotionRecommendation `json:"recommended_actions,omitempty"`
}

type PromotionRecommendation struct {
	ID            string               `json:"id"`
	Command       string               `json:"command"`
	Mutating      bool                 `json:"mutating"`
	Safety        string               `json:"safety,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
}

func (m Manager) Promote(ctx context.Context, req PromotionRequest) (*PromotionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.Store == nil {
		return nil, candidateError("PROMOTION_INVALID", "object store is required", nil)
	}
	now := m.now()
	req = normalizePromotionRequest(req, now)
	if err := validatePromotionRequest(req); err != nil {
		return nil, err
	}

	doc, err := m.ReadCandidate(ctx, req.Service, req.CandidateID)
	if err != nil {
		return nil, err
	}
	candidate := doc.Candidate
	result := &PromotionResult{
		OK:          true,
		DryRun:      req.DryRun,
		Service:     req.Service,
		FromEnv:     req.FromEnv,
		ToEnv:       req.ToEnv,
		CandidateID: req.CandidateID,
		ReleaseID:   candidate.ReleaseID,
		OperationID: req.OperationID,
		ApprovalID:  req.ApprovalID,
		TraceID:     req.TraceID,
		Artifact:    candidate.Artifact,
		Candidate:   &candidate,
		NextCommands: []string{
			fmt.Sprintf("skiff deploy <spec> --release-id %s --format json --trace-id %s", firstNonEmpty(candidate.ReleaseID, "<release-id>"), req.TraceID),
			fmt.Sprintf("skiff status %s --format json --trace-id %s", req.Service, req.TraceID),
			fmt.Sprintf("skiff events --scope operation --service %s --operation %s --format json --trace-id %s", req.Service, req.OperationID, req.TraceID),
		},
	}
	result.Requirements = m.evaluatePromotionRequirements(ctx, req, candidate, now)
	for _, requirement := range result.Requirements {
		if !requirement.OK {
			result.OK = false
		}
	}
	result.RecommendedActions = promotionRecommendations(req, candidate, result.OK)
	result.PlanMarkdown = promotionMarkdown(*result)
	if !result.OK || req.DryRun {
		return result, nil
	}

	intent := schema.NewOperationIntent(req.OperationID, req.Service, req.ToEnv, "release.promote", schema.Target{Kind: "service", Name: req.Service}, req.Actor, req.TraceID, canonical.Time(now))
	intent.Risk = promotionRisk(req.ToEnv)
	intent.Reversibility = schema.Compensatable
	intent.Summary = fmt.Sprintf("promote candidate %s from %s to %s", req.CandidateID, req.FromEnv, req.ToEnv)
	intent.Params = rawJSON(map[string]string{
		"candidate_id":    req.CandidateID,
		"from_env":        req.FromEnv,
		"to_env":          req.ToEnv,
		"release_id":      candidate.ReleaseID,
		"artifact_digest": candidate.Artifact.Digest,
		"approval_id":     req.ApprovalID,
	})
	if err := m.createPromotionOperation(ctx, req, candidate, intent, now, result); err != nil {
		return result, err
	}
	result.PlanMarkdown = promotionMarkdown(*result)
	return result, nil
}

func (m Manager) createPromotionOperation(ctx context.Context, req PromotionRequest, candidate schema.ReleaseCandidate, intent schema.OperationIntent, now time.Time, result *PromotionResult) error {
	intentKey, err := paths.OperationIntent(req.Service, req.OperationID)
	if err != nil {
		return err
	}
	intentBody, err := canonical.Marshal(intent)
	if err != nil {
		return err
	}
	if _, err := m.Store.Create(ctx, intentKey, intentBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		return err
	}
	result.OperationIntent = &intent
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   req.OperationID,
		Service:       req.Service,
		Env:           req.ToEnv,
		Status:        schema.OperationSucceeded,
		StepResults: []schema.StepResultRef{{
			StepID:      "validate-promotion",
			Kind:        "release.promote.validate",
			Status:      "succeeded",
			CompletedAt: canonical.Time(now),
			Result: rawJSON(map[string]string{
				"candidate_id":    req.CandidateID,
				"artifact_digest": candidate.Artifact.Digest,
			}),
		}},
		UpdatedAt: canonical.Time(now),
		TraceID:   req.TraceID,
	}
	controlBody, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	controlKey, err := paths.OperationControl(req.Service, req.OperationID)
	if err != nil {
		return err
	}
	if _, err := m.Store.Create(ctx, controlKey, controlBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		return err
	}
	result.OperationControl = &control

	log, err := events.NewLog(events.Options{Store: m.Store, Clock: m.now})
	if err != nil {
		return err
	}
	event := events.NewOperationEvent(req.Service, req.OperationID, "release.promote.requested", intent.Summary, m.now(), req.TraceID+"promotion-requested")
	event.TraceID = req.TraceID
	event.Actor = &req.Actor
	event.Facts = []schema.Fact{
		{Type: "candidate_id", Message: req.CandidateID},
		{Type: "from_env", Message: req.FromEnv},
		{Type: "to_env", Message: req.ToEnv},
		{Type: "artifact_digest", Message: candidate.Artifact.Digest},
	}
	if _, err := log.Append(ctx, event); err == nil {
		result.Events = append(result.Events, event)
	}
	audit := events.NewAuditRecord(req.Actor, schema.Target{Kind: "service", Name: req.Service}, "release.promote", intent.Summary, req.TraceID, m.now(), req.OperationID+"audit")
	audit.Risk = intent.Risk
	audit.Data = rawJSON(map[string]string{
		"operation_id":    req.OperationID,
		"candidate_id":    req.CandidateID,
		"from_env":        req.FromEnv,
		"to_env":          req.ToEnv,
		"release_id":      candidate.ReleaseID,
		"artifact_digest": candidate.Artifact.Digest,
		"approval_id":     req.ApprovalID,
	})
	_, _ = log.AppendAudit(ctx, audit)
	return nil
}

func (m Manager) evaluatePromotionRequirements(ctx context.Context, req PromotionRequest, candidate schema.ReleaseCandidate, now time.Time) []PromotionRequirement {
	checks := candidateCheckMap(candidate)
	requirements := []PromotionRequirement{
		requirement("candidate_env", candidate.Env == req.FromEnv, "candidate environment matches --from", "CANDIDATE_ENV_MISMATCH", fmt.Sprintf("candidate env %q does not match from env %q", candidate.Env, req.FromEnv)),
		requirement("artifact_digest", isSHA256Digest(candidate.Artifact.Digest), "candidate artifact is digest-pinned", "ARTIFACT_DIGEST_REQUIRED", "candidate artifact digest must use sha256:<64 hex chars>"),
	}
	for _, name := range req.RequiredChecks {
		status := checks[name]
		requirements = append(requirements, requirement("check_"+name, status == "passed", "check "+name+" passed", "PROMOTION_EVIDENCE_MISSING", fmt.Sprintf("check %q status is %q, expected passed", name, firstNonEmpty(status, "<missing>"))))
	}
	if isProductionEnv(req.ToEnv) {
		requirements = append(requirements, requirement("approval", req.ApprovalID != "", "approval context is present for production promotion", "APPROVAL_REQUIRED", "production promotions require --approval-id"))
	}
	if req.MinStableDuration > 0 {
		requirements = append(requirements, m.stableDurationRequirement(ctx, req, candidate, now))
	}
	return requirements
}

func (m Manager) stableDurationRequirement(ctx context.Context, req PromotionRequest, candidate schema.ReleaseCandidate, now time.Time) PromotionRequirement {
	if candidate.ReleaseID == "" {
		return requirement("stable_duration", false, "source release was stable for required duration", "RELEASE_ID_REQUIRED", "candidate must include release_id to validate stable duration")
	}
	doc, err := state.NewClient(m.Store, state.WithClock(clockFunc(m.now))).GetServiceControl(ctx, req.Service)
	if err != nil {
		return PromotionRequirement{ID: "stable_duration", OK: false, Code: "SERVICE_CONTROL_REQUIRED", Summary: "source service control is required to validate stable duration", Detail: err.Error()}
	}
	if doc.Control.Env != req.FromEnv {
		return requirement("stable_duration", false, "source release was stable for required duration", "SOURCE_ENV_MISMATCH", fmt.Sprintf("service control env %q does not match from env %q", doc.Control.Env, req.FromEnv))
	}
	if doc.Control.StableRelease != candidate.ReleaseID {
		return requirement("stable_duration", false, "source release was stable for required duration", "SOURCE_RELEASE_NOT_STABLE", fmt.Sprintf("stable release is %q, expected %q", doc.Control.StableRelease, candidate.ReleaseID))
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, doc.Control.UpdatedAt)
	if err != nil {
		return PromotionRequirement{ID: "stable_duration", OK: false, Code: "INVALID_STABLE_TIME", Summary: "source stable time could not be parsed", Detail: err.Error()}
	}
	age := now.Sub(updatedAt.UTC())
	return requirement("stable_duration", age >= req.MinStableDuration, "source release was stable for required duration", "STABLE_DURATION_NOT_MET", fmt.Sprintf("stable for %s, required %s", age.Round(time.Second), req.MinStableDuration))
}

func normalizePromotionRequest(req PromotionRequest, now time.Time) PromotionRequest {
	req.Service = strings.TrimSpace(req.Service)
	req.FromEnv = strings.TrimSpace(req.FromEnv)
	req.ToEnv = strings.TrimSpace(req.ToEnv)
	req.CandidateID = strings.TrimSpace(req.CandidateID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	req.RequiredChecks = normalizeRequiredChecks(req.RequiredChecks)
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff-cli", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(now, req.Service+"promote")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(now, req.TraceID+req.CandidateID)
	}
	return req
}

func validatePromotionRequest(req PromotionRequest) error {
	if err := paths.ValidateName("service", req.Service); err != nil {
		return candidateError("PROMOTION_INVALID", err.Error(), err)
	}
	if err := paths.ValidateName("from", req.FromEnv); err != nil {
		return candidateError("PROMOTION_INVALID", err.Error(), err)
	}
	if err := paths.ValidateName("to", req.ToEnv); err != nil {
		return candidateError("PROMOTION_INVALID", err.Error(), err)
	}
	if err := paths.ValidateID("candidate", req.CandidateID); err != nil {
		return candidateError("PROMOTION_INVALID", err.Error(), err)
	}
	if err := paths.ValidateID("operation", req.OperationID); err != nil {
		return candidateError("PROMOTION_INVALID", err.Error(), err)
	}
	if req.FromEnv == req.ToEnv {
		return candidateError("PROMOTION_INVALID", "--from and --to must be different environments", nil)
	}
	return nil
}

func normalizeRequiredChecks(checks []string) []string {
	if len(checks) == 0 {
		checks = DefaultPromotionRequiredChecks
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		check = strings.TrimSpace(strings.ToLower(check))
		if check == "" || seen[check] {
			continue
		}
		seen[check] = true
		out = append(out, check)
	}
	return out
}

func candidateCheckMap(candidate schema.ReleaseCandidate) map[string]string {
	out := map[string]string{}
	for _, check := range candidate.Checks {
		name := strings.TrimSpace(strings.ToLower(check.Name))
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(strings.ToLower(check.Status))
	}
	return out
}

func requirement(id string, ok bool, summary, code, detail string) PromotionRequirement {
	req := PromotionRequirement{ID: id, OK: ok, Summary: summary}
	if !ok {
		req.Code = code
		req.Detail = detail
	}
	return req
}

func promotionRisk(env string) schema.Risk {
	if isProductionEnv(env) {
		return schema.RiskHigh
	}
	return schema.RiskMedium
}

func isProductionEnv(env string) bool {
	env = strings.TrimSpace(strings.ToLower(env))
	return env == "prod" || env == "production" || strings.HasPrefix(env, "prod-")
}

func promotionRecommendations(req PromotionRequest, candidate schema.ReleaseCandidate, ok bool) []PromotionRecommendation {
	if !ok {
		return []PromotionRecommendation{{
			ID:       "inspect_candidate",
			Command:  fmt.Sprintf("skiff release candidate show %s --service %s --format json --trace-id %s", req.CandidateID, req.Service, req.TraceID),
			Mutating: false,
		}}
	}
	return []PromotionRecommendation{{
		ID:            "deploy_promoted_release",
		Command:       fmt.Sprintf("skiff deploy <spec> --release-id %s --format json --trace-id %s", firstNonEmpty(candidate.ReleaseID, "<release-id>"), req.TraceID),
		Mutating:      true,
		Safety:        "compensatable",
		Risk:          promotionRisk(req.ToEnv),
		Reversibility: schema.Compensatable,
	}}
}

func promotionMarkdown(result PromotionResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Promotion Plan\n\n")
	fmt.Fprintf(&b, "- Service: `%s`\n", result.Service)
	fmt.Fprintf(&b, "- From: `%s`\n", result.FromEnv)
	fmt.Fprintf(&b, "- To: `%s`\n", result.ToEnv)
	fmt.Fprintf(&b, "- Candidate: `%s`\n", result.CandidateID)
	if result.ReleaseID != "" {
		fmt.Fprintf(&b, "- Release: `%s`\n", result.ReleaseID)
	}
	fmt.Fprintf(&b, "- Artifact digest: `%s`\n\n", result.Artifact.Digest)
	fmt.Fprintf(&b, "## Requirements\n\n")
	for _, req := range result.Requirements {
		mark := " "
		if req.OK {
			mark = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s", mark, req.Summary)
		if !req.OK && req.Detail != "" {
			fmt.Fprintf(&b, " - %s", req.Detail)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "\n## Next Commands\n\n")
	for _, command := range result.NextCommands {
		fmt.Fprintf(&b, "```bash\n%s\n```\n", command)
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time {
	return f()
}
