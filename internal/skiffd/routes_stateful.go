package skiffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/config"
	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
	eventstream "github.com/s1liconcow/skiff/internal/events"
	cloudprovider "github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
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

type statefulOrderedUpdateRequest struct {
	SagaID             string       `json:"saga_id,omitempty"`
	OperationID        string       `json:"operation_id,omitempty"`
	Group              string       `json:"group"`
	Env                string       `json:"env,omitempty"`
	ReleaseID          string       `json:"release_id"`
	ReleaseManifestKey string       `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string       `json:"runtime_manifest_key,omitempty"`
	Members            []int        `json:"members,omitempty"`
	MaxUnavailable     int          `json:"max_unavailable,omitempty"`
	Recipe             string       `json:"recipe,omitempty"`
	Run                *bool        `json:"run,omitempty"`
	Actor              schema.Actor `json:"actor,omitempty"`
}

type statefulBackupRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	BackupID    string       `json:"backup_id,omitempty"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Members     []int        `json:"members,omitempty"`
	Member      *int         `json:"member,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	Retention   string       `json:"retention,omitempty"`
	Run         *bool        `json:"run,omitempty"`
	PlanOnly    bool         `json:"plan_only,omitempty"`
	Actor       schema.Actor `json:"actor,omitempty"`
}

type statefulRestoreRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	RestoreID   string       `json:"restore_id,omitempty"`
	BackupID    string       `json:"backup_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      *int         `json:"member"`
	Mode        string       `json:"mode,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	ApprovalID  string       `json:"approval_id,omitempty"`
	Run         *bool        `json:"run,omitempty"`
	PlanOnly    bool         `json:"plan_only,omitempty"`
	Actor       schema.Actor `json:"actor,omitempty"`
}

func (s *Server) handleStatefulGroups(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	group, member, ok := parseStatefulGroupPath(r.URL.Path)
	if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "stateful group route must be /v1/stateful/groups or /v1/stateful/groups/<group>", nil)
		return
	}
	if queryGroup := strings.TrimSpace(r.URL.Query().Get("group")); queryGroup != "" {
		group = queryGroup
	}
	status, freshness, err := s.statefulStatusFromRequest(r, group)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}
	if group != "" && len(status.StatefulGroups) == 0 {
		writeError(w, r, http.StatusNotFound, "STATEFUL_GROUP_NOT_FOUND", "stateful group "+group+" was not found", nil)
		return
	}
	if member != nil {
		found := false
		for _, candidate := range status.StatefulGroups[0].Members {
			if candidate.Member == *member {
				writeJSON(w, http.StatusOK, map[string]any{
					"ok":             true,
					"trace_id":       traceIDFromContext(r.Context()),
					"request_id":     requestIDFromContext(r.Context()),
					"freshness":      freshness,
					"index":          freshness,
					"stateful_group": status.StatefulGroups[0],
					"member":         candidate,
				})
				found = true
				break
			}
		}
		if !found {
			writeError(w, r, http.StatusNotFound, "STATEFUL_MEMBER_NOT_FOUND", fmt.Sprintf("stateful member %s/%d was not found", group, *member), nil)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"trace_id":        traceIDFromContext(r.Context()),
		"request_id":      requestIDFromContext(r.Context()),
		"freshness":       freshness,
		"index":           freshness,
		"stateful_groups": status.StatefulGroups,
	})
}

func (s *Server) handleStatefulStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	if group == "" {
		group = strings.TrimSpace(r.URL.Query().Get("service"))
	}
	status, freshness, err := s.statefulStatusFromRequest(r, group)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"freshness":  freshness,
		"index":      freshness,
		"status":     status,
	})
}

func (s *Server) handleStatefulDoctor(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	if group == "" {
		group = strings.TrimSpace(r.URL.Query().Get("service"))
	}
	status, _, err := s.statefulStatusFromRequest(r, group)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}
	result, err := servicedoctor.Diagnose(r.Context(), status, servicedoctor.Options{
		Service: group,
		TraceID: traceIDFromContext(r.Context()),
		Binary:  "skiff",
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DOCTOR_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"doctor":     result,
	})
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

func (s *Server) handleStatefulUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	var body statefulOrderedUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "decode stateful update request: "+err.Error(), nil)
		return
	}
	actor := requestActor(r.Context(), body.Actor)
	req := templates.StatefulOrderedUpdateRequest{
		SagaID:             body.SagaID,
		OperationID:        body.OperationID,
		Group:              strings.TrimSpace(body.Group),
		Env:                firstNonEmpty(body.Env, s.cfg.Env),
		ReleaseID:          body.ReleaseID,
		ReleaseManifestKey: body.ReleaseManifestKey,
		RuntimeManifestKey: body.RuntimeManifestKey,
		Members:            append([]int(nil), body.Members...),
		MaxUnavailable:     body.MaxUnavailable,
		Recipe:             body.Recipe,
		Actor:              actor,
		TraceID:            traceIDFromContext(r.Context()),
		CreatedAt:          s.clock(),
	}
	req = templates.NormalizeStatefulOrderedUpdateRequest(req)
	if len(req.Members) == 0 {
		members, err := s.orderedUpdateMembersFromControl(r.Context(), req.Group)
		if err != nil {
			writeStatefulSagaError(w, r, err)
			return
		}
		req.Members = members
	}
	createReq, err := templates.StatefulOrderedUpdate(req)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	result, err := s.createAndMaybeRunStatefulSaga(r.Context(), createReq, req.SagaID, runRequested(body.Run, true))
	if err != nil {
		writeStatefulSagaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "trace_id": traceIDFromContext(r.Context()), "request_id": requestIDFromContext(r.Context()), "result": result})
}

func (s *Server) handleStatefulBackup(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	var body statefulBackupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "decode stateful backup request: "+err.Error(), nil)
		return
	}
	members := append([]int(nil), body.Members...)
	if body.Member != nil {
		members = append(members, *body.Member)
	}
	req := templates.StatefulBackupRequest{
		SagaID:      body.SagaID,
		OperationID: body.OperationID,
		BackupID:    body.BackupID,
		Group:       strings.TrimSpace(body.Group),
		Env:         firstNonEmpty(body.Env, s.cfg.Env),
		Members:     members,
		Reason:      body.Reason,
		Retention:   body.Retention,
		Actor:       requestActor(r.Context(), body.Actor),
		TraceID:     traceIDFromContext(r.Context()),
		CreatedAt:   s.clock(),
	}
	createReq, err := templates.StatefulBackup(req)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if body.PlanOnly {
		req = templates.NormalizeStatefulBackupRequest(req)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trace_id": traceIDFromContext(r.Context()), "request_id": requestIDFromContext(r.Context()), "result": statefulSagaAPIResult{SagaID: req.SagaID, OperationID: req.OperationID, Group: req.Group, Env: req.Env, Status: schema.SagaPending, NextAction: "create_saga"}})
		return
	}
	result, err := s.createAndMaybeRunStatefulSaga(r.Context(), createReq, templates.NormalizeStatefulBackupRequest(req).SagaID, runRequested(body.Run, true))
	if err != nil {
		writeStatefulSagaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "trace_id": traceIDFromContext(r.Context()), "request_id": requestIDFromContext(r.Context()), "result": result})
}

func (s *Server) handleStatefulRestore(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	var body statefulRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "decode stateful restore request: "+err.Error(), nil)
		return
	}
	if body.Member == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "group, backup_id, and member are required", nil)
		return
	}
	req := templates.StatefulRestoreRequest{
		SagaID:      body.SagaID,
		OperationID: body.OperationID,
		RestoreID:   body.RestoreID,
		BackupID:    body.BackupID,
		Group:       strings.TrimSpace(body.Group),
		Env:         firstNonEmpty(body.Env, s.cfg.Env),
		Member:      *body.Member,
		Mode:        body.Mode,
		Reason:      body.Reason,
		ApprovalID:  body.ApprovalID,
		Actor:       requestActor(r.Context(), body.Actor),
		TraceID:     traceIDFromContext(r.Context()),
		CreatedAt:   s.clock(),
	}
	createReq, err := templates.StatefulRestore(req)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if body.PlanOnly {
		req = templates.NormalizeStatefulRestoreRequest(req)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trace_id": traceIDFromContext(r.Context()), "request_id": requestIDFromContext(r.Context()), "result": statefulSagaAPIResult{SagaID: req.SagaID, OperationID: req.OperationID, Group: req.Group, Env: req.Env, Member: req.Member, Status: schema.SagaPending, NextAction: "create_saga"}})
		return
	}
	result, err := s.createAndMaybeRunStatefulSaga(r.Context(), createReq, templates.NormalizeStatefulRestoreRequest(req).SagaID, runRequested(body.Run, true))
	if err != nil {
		writeStatefulSagaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "trace_id": traceIDFromContext(r.Context()), "request_id": requestIDFromContext(r.Context()), "result": result})
}

func (s *Server) handleStatefulSagaAction(w http.ResponseWriter, r *http.Request) {
	sagaID, action, ok := parseStatefulSagaAction(r.URL.Path)
	if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "stateful saga route must be /v1/stateful/sagas/<saga>/resume", nil)
		return
	}
	if action == "watch" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.handleStatefulSagaWatch(w, r, sagaID)
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
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

func (s *Server) handleStatefulSagaWatch(w http.ResponseWriter, r *http.Request, sagaID string) {
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	query := urlCopy.Query()
	query.Set("scope", string(eventstream.ScopeSaga))
	query.Set("saga", sagaID)
	urlCopy.RawQuery = query.Encode()
	clone.URL = &urlCopy
	s.handleEventsStream(w, clone)
}

func (s *Server) createAndMaybeRunStatefulReplacement(ctx context.Context, req templates.StatefulReplaceMemberRequest, run bool) (*statefulSagaAPIResult, error) {
	req = templates.NormalizeStatefulReplaceMemberRequest(req)
	createReq, err := templates.StatefulReplaceMember(req)
	if err != nil {
		return nil, err
	}
	return s.createAndMaybeRunStatefulSaga(ctx, createReq, req.SagaID, run)
}

func (s *Server) createAndMaybeRunStatefulSaga(ctx context.Context, createReq sagastate.CreateRequest, sagaID string, run bool) (*statefulSagaAPIResult, error) {
	store := sagastate.NewStore(s.store)
	if _, err := store.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	var err error
	if run {
		execution, err = s.executeStatefulSaga(ctx, sagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := store.Inspect(ctx, sagaID)
	if err != nil {
		return nil, err
	}
	result := statefulSagaResultFromInspect(*inspect, execution)
	return &result, nil
}

func (s *Server) executeStatefulSaga(ctx context.Context, sagaID string) (*sagastate.ExecutionResult, error) {
	cloud, _ := s.provider.(cloudprovider.Provider)
	return (&sagastate.Executor{
		Store: sagastate.NewStore(s.store),
		Steps: builtin.New(builtin.Options{Store: s.store, Provider: cloud, Binary: "skiff"}),
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

func (s *Server) orderedUpdateMembersFromControl(ctx context.Context, group string) ([]int, error) {
	doc, err := state.NewClient(s.store).GetStatefulGroupControl(ctx, group)
	if err != nil {
		return nil, err
	}
	members := make([]int, 0, len(doc.Control.Members))
	for _, member := range doc.Control.Members {
		members = append(members, member.Member)
	}
	if len(members) == 0 {
		for i := 0; i < doc.Control.Replicas; i++ {
			members = append(members, i)
		}
	}
	return members, nil
}

func (s *Server) statefulStatusFromRequest(r *http.Request, group string) (servicestatus.Result, freshness, error) {
	read, err := s.snapshotForRequest(r)
	if err != nil {
		return servicestatus.Result{}, freshness{}, err
	}
	if !read.snapshot.Ready {
		return servicestatus.Result{}, read.freshness, errors.New("index has not completed initial load")
	}
	status := servicestatus.FromSnapshot(read.snapshot, servicestatus.Options{
		Mode:        config.ModeAPI,
		Env:         s.cfg.Env,
		Provider:    s.cfg.Provider,
		Region:      s.cfg.Region,
		StateBucket: redactURI(s.cfg.StateBucket),
		Service:     group,
		Source:      "api",
		Freshness:   servicestatus.FreshnessFromIndex(read.freshness),
		Now:         s.clock().UTC(),
	})
	return status, read.freshness, nil
}

func parseStatefulGroupPath(path string) (string, *int, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/stateful/groups"), "/")
	if rest == "" {
		return "", nil, true
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] != "" {
		return parts[0], nil, true
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "members" {
		member, err := strconv.Atoi(parts[2])
		if err != nil || member < 0 {
			return "", nil, false
		}
		return parts[0], &member, true
	}
	return "", nil, false
}

func requestActor(ctx context.Context, provided schema.Actor) schema.Actor {
	if provided.ID != "" {
		if provided.Type == "" {
			provided.Type = "user"
		}
		return provided
	}
	actor := actorFromContext(ctx)
	if actor.Type == "" {
		actor.Type = "user"
	}
	return actor
}

func runRequested(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
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
