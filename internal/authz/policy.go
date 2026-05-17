package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/auth"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Action string

const (
	ActionRead     Action = "read"
	ActionPlan     Action = "plan"
	ActionDeploy   Action = "deploy"
	ActionRollback Action = "rollback"
	ActionApprove  Action = "approve"
	ActionDebug    Action = "debug"
	ActionRotate   Action = "rotate"
	ActionRestore  Action = "restore"
	ActionFailover Action = "failover"
	ActionGC       Action = "gc"
)

type Request struct {
	Actor        schema.Actor  `json:"actor"`
	Action       Action        `json:"action"`
	Target       schema.Target `json:"target"`
	Env          string        `json:"env,omitempty"`
	Service      string        `json:"service,omitempty"`
	Risk         schema.Risk   `json:"risk,omitempty"`
	ApprovalID   string        `json:"approval_id,omitempty"`
	ApprovalRole string        `json:"approval_role,omitempty"`
	DryRun       bool          `json:"dry_run,omitempty"`
	PlanOnly     bool          `json:"plan_only,omitempty"`
	TraceID      string        `json:"trace_id,omitempty"`
}

type Decision struct {
	Allowed          bool          `json:"allowed"`
	Action           Action        `json:"action"`
	Actor            schema.Actor  `json:"actor"`
	Target           schema.Target `json:"target"`
	Env              string        `json:"env,omitempty"`
	Service          string        `json:"service,omitempty"`
	Risk             schema.Risk   `json:"risk,omitempty"`
	Mutating         bool          `json:"mutating"`
	RequiresApproval bool          `json:"requires_approval,omitempty"`
	ApprovalRole     string        `json:"approval_role,omitempty"`
	ApprovalID       string        `json:"approval_id,omitempty"`
	BreakGlass       bool          `json:"break_glass,omitempty"`
	Reasons          []string      `json:"reasons,omitempty"`
	Denials          []string      `json:"denials,omitempty"`
}

type Authorizer interface {
	Authorize(ctx context.Context, req Request) (Decision, error)
}

type DefaultPolicy struct{}

type DeniedError struct {
	Decision Decision
}

func (e DeniedError) Error() string {
	if len(e.Decision.Denials) > 0 {
		return strings.Join(e.Decision.Denials, "; ")
	}
	return "authorization denied"
}

func (DefaultPolicy) Authorize(ctx context.Context, req Request) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	req = normalizeRequest(req)
	decision := Decision{
		Allowed:    true,
		Action:     req.Action,
		Actor:      req.Actor,
		Target:     req.Target,
		Env:        req.Env,
		Service:    req.Service,
		Risk:       req.Risk,
		Mutating:   isMutating(req.Action),
		ApprovalID: req.ApprovalID,
	}
	deny := func(summary string) (Decision, error) {
		decision.Allowed = false
		decision.Denials = append(decision.Denials, summary)
		return decision, DeniedError{Decision: decision}
	}
	if !knownAction(req.Action) {
		return deny(fmt.Sprintf("unknown action %q", req.Action))
	}
	if req.DryRun || req.PlanOnly || req.Action == ActionRead || req.Action == ActionPlan {
		decision.Reasons = append(decision.Reasons, "read and plan operations are allowed without approval")
		return decision, nil
	}
	if req.Actor.ID == "" {
		return deny("actor id is required")
	}
	if auth.IsBreakGlass(req.Actor) {
		decision.BreakGlass = true
		decision.Reasons = append(decision.Reasons, "break-glass actor is allowed and must be audited")
		return decision, nil
	}
	if req.Action == ActionApprove {
		if !auth.HasRole(req.Actor, "approver") {
			return deny("actor does not have approver role")
		}
		decision.Reasons = append(decision.Reasons, "actor may approve operations")
		return decision, nil
	}
	if highRiskProduction(req) {
		decision.RequiresApproval = true
		decision.ApprovalRole = approvalRole(req)
		if req.ApprovalID == "" {
			return deny("approval required from role " + decision.ApprovalRole)
		}
		if !validApprovalID(req.ApprovalID) {
			return deny("approval id must start with approval_ or break-glass:")
		}
		decision.Reasons = append(decision.Reasons, "approval context present for high-risk production operation")
	}
	if req.Actor.Type == auth.ActorAgent && highRiskProduction(req) && req.ApprovalID == "" {
		return deny("agents may plan high-risk operations but need approval context to execute them")
	}
	if req.Actor.Type == auth.ActorWorker && decision.Mutating {
		return deny("worker actors cannot initiate mutating production operations")
	}
	decision.Reasons = append(decision.Reasons, "actor is authorized by default Skiff policy")
	return decision, nil
}

func Explain(policy Authorizer, ctx context.Context, req Request) Decision {
	if policy == nil {
		policy = DefaultPolicy{}
	}
	decision, err := policy.Authorize(ctx, req)
	if err != nil {
		var denied DeniedError
		if errors.As(err, &denied) {
			return denied.Decision
		}
		req = normalizeRequest(req)
		return Decision{Allowed: false, Action: req.Action, Actor: req.Actor, Target: req.Target, Env: req.Env, Service: req.Service, Risk: req.Risk, Denials: []string{err.Error()}}
	}
	return decision
}

func MustAuthorize(ctx context.Context, policy Authorizer, req Request) (Decision, error) {
	if policy == nil {
		policy = DefaultPolicy{}
	}
	decision, err := policy.Authorize(ctx, req)
	if err != nil {
		return decision, err
	}
	if !decision.Allowed {
		return decision, DeniedError{Decision: decision}
	}
	return decision, nil
}

func normalizeRequest(req Request) Request {
	req.Actor = auth.Normalize(req.Actor)
	req.Action = Action(strings.TrimSpace(strings.ToLower(string(req.Action))))
	req.Env = strings.TrimSpace(strings.ToLower(req.Env))
	req.Service = strings.TrimSpace(req.Service)
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	req.ApprovalRole = strings.TrimSpace(req.ApprovalRole)
	if req.Target.Kind == "" {
		req.Target.Kind = "service"
	}
	if req.Target.Name == "" {
		req.Target.Name = req.Service
	}
	if req.Risk == "" {
		req.Risk = defaultRisk(req.Action)
	}
	return req
}

func knownAction(action Action) bool {
	switch action {
	case ActionRead, ActionPlan, ActionDeploy, ActionRollback, ActionApprove, ActionDebug, ActionRotate, ActionRestore, ActionFailover, ActionGC:
		return true
	default:
		return false
	}
}

func isMutating(action Action) bool {
	switch action {
	case ActionDeploy, ActionRollback, ActionApprove, ActionDebug, ActionRotate, ActionRestore, ActionFailover, ActionGC:
		return true
	default:
		return false
	}
}

func defaultRisk(action Action) schema.Risk {
	switch action {
	case ActionDebug, ActionRotate, ActionRestore, ActionFailover, ActionGC:
		return schema.RiskHigh
	case ActionDeploy, ActionRollback:
		return schema.RiskMedium
	default:
		return schema.RiskLow
	}
}

func highRiskProduction(req Request) bool {
	if !isProductionEnv(req.Env) {
		return false
	}
	if req.Risk == schema.RiskHigh || req.Risk == schema.RiskCritical {
		return true
	}
	switch req.Action {
	case ActionDebug, ActionRotate, ActionRestore, ActionFailover, ActionGC:
		return true
	default:
		return false
	}
}

func approvalRole(req Request) string {
	if req.ApprovalRole != "" {
		return req.ApprovalRole
	}
	switch req.Action {
	case ActionRestore:
		return "database-admin"
	case ActionDebug, ActionRotate:
		return "security-admin"
	case ActionFailover, ActionRollback:
		return "operator"
	case ActionGC:
		return "platform-admin"
	default:
		return "approver"
	}
}

func validApprovalID(value string) bool {
	return strings.HasPrefix(value, "approval_") || strings.HasPrefix(value, "break-glass:")
}

func isProductionEnv(env string) bool {
	env = strings.TrimSpace(strings.ToLower(env))
	return env == "prod" || env == "production" || strings.HasPrefix(env, "prod-")
}
