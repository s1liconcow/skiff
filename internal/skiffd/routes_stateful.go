package skiffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	cloudprovider "github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	statefulsteps "github.com/s1liconcow/skiff/internal/saga/steps/stateful"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type statefulReplaceMemberRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      *int         `json:"member"`
	Reason      string       `json:"reason,omitempty"`
	ApprovalID  string       `json:"approval_id,omitempty"`
	Run         *bool        `json:"run,omitempty"`
	Actor       schema.Actor `json:"actor,omitempty"`
}

type statefulSagaAPIResult struct {
	SagaID             string                     `json:"saga_id"`
	OperationID        string                     `json:"operation_id,omitempty"`
	Group              string                     `json:"group,omitempty"`
	Env                string                     `json:"env,omitempty"`
	Member             int                        `json:"member,omitempty"`
	Status             schema.SagaStatus          `json:"status"`
	NextAction         string                     `json:"next_action,omitempty"`
	CurrentSteps       []string                   `json:"current_steps,omitempty"`
	Facts              []schema.Fact              `json:"facts,omitempty"`
	RecommendedActions []recommendedAction        `json:"recommended_actions,omitempty"`
	Execution          *sagastate.ExecutionResult `json:"execution,omitempty"`
}

func (s *Server) handleStatefulReplaceMember(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	var body statefulReplaceMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "decode stateful replacement request: "+err.Error(), nil)
		return
	}
	body.Group = strings.TrimSpace(body.Group)
	body.Env = firstNonEmpty(body.Env, s.cfg.Env)
	if body.Group == "" || body.Member == nil || *body.Member < 0 {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "group and non-negative member are required", nil)
		return
	}
	if body.Env == "prod" && strings.TrimSpace(body.ApprovalID) == "" {
		writeError(w, r, http.StatusForbidden, "APPROVAL_REQUIRED", "production stateful replacement is high risk and requires approval", []recommendedAction{
			{ID: "submit_with_approval", Command: "POST /v1/stateful/replace-member with approval_id", Mutating: true},
		})
		return
	}
	run := true
	if body.Run != nil {
		run = *body.Run
	}
	actor := body.Actor
	if actor.ID == "" {
		actor = actorFromContext(r.Context())
	}
	if actor.Type == "" {
		actor.Type = "user"
	}
	req := templates.StatefulReplaceMemberRequest{
		SagaID:      body.SagaID,
		OperationID: body.OperationID,
		Group:       body.Group,
		Env:         body.Env,
		Member:      *body.Member,
		Reason:      body.Reason,
		Actor:       actor,
		TraceID:     traceIDFromContext(r.Context()),
		CreatedAt:   s.clock(),
	}
	result, err := s.createAndMaybeRunStatefulReplacement(r.Context(), req, run)
	if err != nil {
		writeStatefulSagaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"result":     result,
	})
}

func (s *Server) handleStatefulSagaAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	sagaID, action, ok := parseStatefulSagaAction(r.URL.Path)
	if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "stateful saga route must be /v1/stateful/sagas/<saga>/resume", nil)
		return
	}
	if action != "resume" {
		writeError(w, r, http.StatusBadRequest, "UNSUPPORTED_ACTION", "only resume is supported for stateful saga actions", nil)
		return
	}
	execution, err := s.executeStatefulSaga(r.Context(), sagaID)
	if err != nil {
		writeStatefulSagaError(w, r, err)
		return
	}
	inspect, err := sagastate.NewStore(s.store).Inspect(r.Context(), sagaID)
	if err != nil {
		writeStatefulSagaError(w, r, err)
		return
	}
	result := statefulSagaResultFromInspect(*inspect, execution)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"result":     result,
	})
}

func (s *Server) createAndMaybeRunStatefulReplacement(ctx context.Context, req templates.StatefulReplaceMemberRequest, run bool) (*statefulSagaAPIResult, error) {
	req = templates.NormalizeStatefulReplaceMemberRequest(req)
	createReq, err := templates.StatefulReplaceMember(req)
	if err != nil {
		return nil, err
	}
	store := sagastate.NewStore(s.store)
	if _, err := store.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	if run {
		execution, err = s.executeStatefulSaga(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := store.Inspect(ctx, req.SagaID)
	if err != nil {
		return nil, err
	}
	result := statefulSagaResultFromInspect(*inspect, execution)
	return &result, nil
}

func (s *Server) executeStatefulSaga(ctx context.Context, sagaID string) (*sagastate.ExecutionResult, error) {
	statefulProvider, _ := s.provider.(cloudprovider.StatefulOperations)
	items := statefulsteps.NewWithProvider(s.store, statefulProvider, nil)
	registered := make(map[string]steps.Step, len(items))
	for _, item := range items {
		registered[item.Kind()] = item
	}
	return (&sagastate.Executor{
		Store: sagastate.NewStore(s.store),
		Steps: registered,
		Owner: "skiffd",
	}).Execute(ctx, sagaID)
}

func statefulSagaResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) statefulSagaAPIResult {
	var params struct {
		Group       string `json:"group"`
		Env         string `json:"env"`
		Member      int    `json:"member"`
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := statefulSagaAPIResult{
		SagaID:       inspect.SagaID,
		OperationID:  params.OperationID,
		Group:        firstNonEmpty(params.Group, inspect.Target.Name),
		Env:          params.Env,
		Member:       params.Member,
		Status:       inspect.Status,
		CurrentSteps: append([]string(nil), inspect.CurrentSteps...),
		Facts: []schema.Fact{
			{Type: "stateful_member", Message: fmt.Sprintf("%s/%d", firstNonEmpty(params.Group, inspect.Target.Name), params.Member)},
			{Type: "operation", Message: params.OperationID},
		},
		Execution: execution,
	}
	switch inspect.Status {
	case schema.SagaSucceeded:
		result.NextAction = "complete"
	case schema.SagaFailed:
		result.NextAction = "inspect_failure"
	default:
		result.NextAction = "resume"
	}
	result.RecommendedActions = []recommendedAction{
		{ID: "inspect_saga", Command: fmt.Sprintf("GET /v1/sagas?saga=%s&format=json", inspect.SagaID), Mutating: false},
	}
	if result.Status != schema.SagaSucceeded {
		result.RecommendedActions = append(result.RecommendedActions, recommendedAction{ID: "resume_stateful_saga", Command: fmt.Sprintf("POST /v1/stateful/sagas/%s/resume?format=json", inspect.SagaID), Mutating: true})
	}
	return result
}

func parseStatefulSagaAction(path string) (string, string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/stateful/sagas/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func writeStatefulSagaError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "STATEFUL_SAGA_FAILED"
	if errors.Is(err, context.Canceled) {
		status = http.StatusRequestTimeout
		code = "TIMEOUT"
	}
	writeError(w, r, status, code, err.Error(), nil)
}

func actorFromContext(ctx context.Context) schema.Actor {
	actor, _ := ctx.Value(actorKey{}).(schema.Actor)
	if actor.ID == "" {
		return schema.Actor{ID: "skiffd", Type: "system"}
	}
	return actor
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
