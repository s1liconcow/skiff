package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Store struct {
	store objstore.ObjectStore
	clock func() time.Time
}

type Option func(*Store)

func NewStore(store objstore.ObjectStore, opts ...Option) *Store {
	s := &Store{
		store: store,
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithClock(clock func() time.Time) Option {
	return func(s *Store) {
		if clock != nil {
			s.clock = clock
		}
	}
}

func Create(ctx context.Context, store objstore.ObjectStore, req CreateRequest) (*Documents, error) {
	return NewStore(store).Create(ctx, req)
}

func Inspect(ctx context.Context, store objstore.ObjectStore, sagaID string) (*InspectResult, error) {
	return NewStore(store).Inspect(ctx, sagaID)
}

func (s *Store) Create(ctx context.Context, req CreateRequest) (*Documents, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	now := canonical.Time(s.clock().UTC())
	intent := req.Intent
	graph := req.Graph
	control := req.Control
	normalizeIntent(&intent, now)
	normalizeGraph(&graph, intent.SagaID, now)
	normalizeControl(&control, intent.SagaID, now)
	if err := validateCreateRequest(intent, graph, control); err != nil {
		return nil, err
	}
	intentDoc, err := s.CreateIntent(ctx, intent)
	if err != nil {
		return nil, err
	}
	graphDoc, err := s.CreateGraph(ctx, graph)
	if err != nil {
		return nil, err
	}
	controlDoc, err := s.CreateControl(ctx, control)
	if err != nil {
		return nil, err
	}
	return &Documents{Intent: intentDoc, Graph: graphDoc, Control: controlDoc}, nil
}

func (s *Store) CreateIntent(ctx context.Context, intent schema.SagaIntent) (*IntentDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	normalizeIntent(&intent, canonical.Time(s.clock().UTC()))
	if err := validateIntent(intent); err != nil {
		return nil, err
	}
	key, err := paths.SagaIntent(intent.SagaID)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(intent)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.Create(ctx, key, body, putOptions("saga_intent", intent.SagaID))
	if err != nil {
		return nil, err
	}
	return &IntentDocument{Key: key, ETag: meta.ETag, Meta: *meta, Intent: intent}, nil
}

func (s *Store) CreateGraph(ctx context.Context, graph schema.SagaGraph) (*GraphDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	normalizeGraph(&graph, graph.SagaID, canonical.Time(s.clock().UTC()))
	if err := validateGraph(graph); err != nil {
		return nil, err
	}
	key, err := paths.SagaGraph(graph.SagaID)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(graph)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.Create(ctx, key, body, putOptions("saga_graph", graph.SagaID))
	if err != nil {
		return nil, err
	}
	return &GraphDocument{Key: key, ETag: meta.ETag, Meta: *meta, Graph: graph}, nil
}

func (s *Store) CreateControl(ctx context.Context, control schema.SagaControl) (*ControlDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	normalizeControl(&control, control.SagaID, canonical.Time(s.clock().UTC()))
	if err := validateControl(control); err != nil {
		return nil, err
	}
	key, err := paths.SagaControl(control.SagaID)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.Create(ctx, key, body, putOptions("saga_control", control.SagaID))
	if err != nil {
		return nil, err
	}
	return &ControlDocument{Key: key, ETag: meta.ETag, Meta: *meta, Control: control}, nil
}

func (s *Store) GetIntent(ctx context.Context, sagaID string) (*IntentDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.SagaIntent(sagaID)
	if err != nil {
		return nil, err
	}
	var intent schema.SagaIntent
	obj, err := getStrict(ctx, s.store, key, &intent)
	if err != nil {
		return nil, err
	}
	if err := validateIntent(intent); err != nil {
		return nil, err
	}
	if intent.SagaID != sagaID {
		return nil, fmt.Errorf("saga intent %q names saga %q", key, intent.SagaID)
	}
	meta := objectMetaFromObject(obj)
	return &IntentDocument{Key: key, ETag: meta.ETag, Meta: meta, Intent: intent}, nil
}

func (s *Store) GetGraph(ctx context.Context, sagaID string) (*GraphDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.SagaGraph(sagaID)
	if err != nil {
		return nil, err
	}
	var graph schema.SagaGraph
	obj, err := getStrict(ctx, s.store, key, &graph)
	if err != nil {
		return nil, err
	}
	if err := validateGraph(graph); err != nil {
		return nil, err
	}
	if graph.SagaID != sagaID {
		return nil, fmt.Errorf("saga graph %q names saga %q", key, graph.SagaID)
	}
	meta := objectMetaFromObject(obj)
	return &GraphDocument{Key: key, ETag: meta.ETag, Meta: meta, Graph: graph}, nil
}

func (s *Store) GetControl(ctx context.Context, sagaID string) (*ControlDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.SagaControl(sagaID)
	if err != nil {
		return nil, err
	}
	var control schema.SagaControl
	obj, err := getStrict(ctx, s.store, key, &control)
	if err != nil {
		return nil, err
	}
	if err := validateControl(control); err != nil {
		return nil, err
	}
	if control.SagaID != sagaID {
		return nil, fmt.Errorf("saga control %q names saga %q", key, control.SagaID)
	}
	meta := objectMetaFromObject(obj)
	return &ControlDocument{Key: key, ETag: meta.ETag, Meta: meta, Control: control}, nil
}

func (s *Store) UpdateControlCAS(ctx context.Context, current *ControlDocument, next schema.SagaControl) (*ControlDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, errors.New("current saga control document is required")
	}
	normalizeControl(&next, current.Control.SagaID, canonical.Time(s.clock().UTC()))
	if next.SagaID != current.Control.SagaID {
		return nil, fmt.Errorf("cannot change saga from %q to %q", current.Control.SagaID, next.SagaID)
	}
	if err := validateControl(next); err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(next)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.CompareAndSwap(ctx, current.Key, current.ETag, body, putOptions("saga_control", next.SagaID))
	if err != nil {
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			return nil, fmt.Errorf("%w: saga control ETag is stale", state.ErrPreconditionFailed)
		}
		return nil, err
	}
	return &ControlDocument{Key: current.Key, ETag: meta.ETag, Meta: *meta, Control: next}, nil
}

func (s *Store) AppendEvent(ctx context.Context, sagaID string, event schema.Event) (*EventDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	normalizeEvent(&event, sagaID, canonical.Time(s.clock().UTC()))
	if err := validateEvent(sagaID, event); err != nil {
		return nil, err
	}
	key, err := paths.SagaEvent(sagaID, event.ID)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(event)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.Create(ctx, key, body, putOptions("saga_event", sagaID))
	if err != nil {
		return nil, err
	}
	return &EventDocument{Key: key, ETag: meta.ETag, Meta: *meta, Event: event}, nil
}

func (s *Store) CreateStepResult(ctx context.Context, result schema.StepResult) (*StepResultDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	normalizeStepResult(&result)
	if err := validateStepResult(result); err != nil {
		return nil, err
	}
	key, err := paths.SagaStepResult(result.SagaID, result.StepID)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(result)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.Create(ctx, key, body, putOptions("saga_step_result", result.SagaID))
	if err != nil {
		return nil, err
	}
	return &StepResultDocument{Key: key, ETag: meta.ETag, Meta: *meta, Result: result}, nil
}

func (s *Store) Inspect(ctx context.Context, sagaID string) (*InspectResult, error) {
	intent, err := s.GetIntent(ctx, sagaID)
	if err != nil {
		return nil, err
	}
	graph, err := s.GetGraph(ctx, sagaID)
	if err != nil {
		return nil, err
	}
	control, err := s.GetControl(ctx, sagaID)
	if err != nil {
		return nil, err
	}
	pathsOut := map[string]string{
		"intent":  intent.Key,
		"graph":   graph.Key,
		"control": control.Key,
	}
	eventPrefix, err := paths.SagaEventsPrefix(sagaID)
	if err == nil {
		pathsOut["events"] = eventPrefix
	}
	nodes := make([]NodeSummary, 0, len(graph.Graph.Nodes))
	for _, node := range graph.Graph.Nodes {
		nodes = append(nodes, NodeSummary{
			ID:            node.ID,
			Kind:          node.Kind,
			Requires:      append([]string(nil), node.Requires...),
			Risk:          node.Risk,
			Reversibility: node.Reversibility,
		})
	}
	return &InspectResult{
		SagaID:        sagaID,
		Kind:          intent.Intent.Kind,
		Target:        intent.Intent.Target,
		Status:        control.Control.Status,
		CurrentSteps:  append([]string(nil), control.Control.CurrentSteps...),
		Risk:          intent.Intent.Risk,
		Reversibility: intent.Intent.Reversibility,
		UpdatedAt:     control.Control.UpdatedAt,
		TraceID:       firstNonEmpty(control.Control.TraceID, intent.Intent.TraceID),
		Intent:        intent.Intent,
		Graph:         graph.Graph,
		Control:       control.Control,
		Nodes:         nodes,
		Paths:         pathsOut,
	}, nil
}

func (s *Store) requireStore() error {
	if s == nil || s.store == nil {
		return errors.New("saga object store is required")
	}
	return nil
}

func normalizeIntent(intent *schema.SagaIntent, now string) {
	if intent.SchemaVersion == "" {
		intent.SchemaVersion = schema.Version
	}
	if intent.CreatedAt == "" {
		intent.CreatedAt = now
	}
}

func normalizeGraph(graph *schema.SagaGraph, sagaID, now string) {
	if graph.SchemaVersion == "" {
		graph.SchemaVersion = schema.Version
	}
	if graph.SagaID == "" {
		graph.SagaID = sagaID
	}
	if graph.CreatedAt == "" {
		graph.CreatedAt = now
	}
}

func normalizeControl(control *schema.SagaControl, sagaID, now string) {
	if control.SchemaVersion == "" {
		control.SchemaVersion = schema.Version
	}
	if control.SagaID == "" {
		control.SagaID = sagaID
	}
	if control.Status == "" {
		control.Status = schema.SagaPending
	}
	if control.UpdatedAt == "" {
		control.UpdatedAt = now
	}
}

func normalizeEvent(event *schema.Event, sagaID, now string) {
	if event.SchemaVersion == "" {
		event.SchemaVersion = schema.Version
	}
	if event.Time == "" {
		event.Time = now
	}
	if event.Subject.Kind == "" {
		event.Subject.Kind = "saga"
	}
	if event.Subject.Name == "" {
		event.Subject.Name = sagaID
	}
}

func normalizeStepResult(result *schema.StepResult) {
	if result.SchemaVersion == "" {
		result.SchemaVersion = schema.Version
	}
}

func validateCreateRequest(intent schema.SagaIntent, graph schema.SagaGraph, control schema.SagaControl) error {
	if err := validateIntent(intent); err != nil {
		return err
	}
	if graph.SagaID != intent.SagaID {
		return fmt.Errorf("saga graph names saga %q, want %q", graph.SagaID, intent.SagaID)
	}
	if control.SagaID != intent.SagaID {
		return fmt.Errorf("saga control names saga %q, want %q", control.SagaID, intent.SagaID)
	}
	if err := validateGraph(graph); err != nil {
		return err
	}
	return validateControl(control)
}

func validateIntent(intent schema.SagaIntent) error {
	if intent.SchemaVersion != schema.Version {
		return fmt.Errorf("unsupported saga intent schema version %q", intent.SchemaVersion)
	}
	if err := paths.ValidateID("saga", intent.SagaID); err != nil {
		return err
	}
	if strings.TrimSpace(intent.Kind) == "" {
		return errors.New("saga kind is required")
	}
	if strings.TrimSpace(intent.Target.Kind) == "" || strings.TrimSpace(intent.Target.Name) == "" {
		return errors.New("saga target kind and name are required")
	}
	if strings.TrimSpace(intent.Actor.ID) == "" || strings.TrimSpace(intent.Actor.Type) == "" {
		return errors.New("saga actor id and type are required")
	}
	if strings.TrimSpace(intent.TraceID) == "" {
		return errors.New("saga trace id is required")
	}
	if err := validateRisk(intent.Risk); err != nil {
		return err
	}
	if err := validateReversibility(intent.Reversibility); err != nil {
		return err
	}
	return nil
}

func validateGraph(graph schema.SagaGraph) error {
	if graph.SchemaVersion != schema.Version {
		return fmt.Errorf("unsupported saga graph schema version %q", graph.SchemaVersion)
	}
	if err := paths.ValidateID("saga", graph.SagaID); err != nil {
		return err
	}
	if len(graph.Nodes) == 0 {
		return errors.New("saga graph must contain at least one node")
	}
	seen := make(map[string]schema.SagaNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if err := validateNode(node); err != nil {
			return err
		}
		if _, exists := seen[node.ID]; exists {
			return fmt.Errorf("duplicate saga node %q", node.ID)
		}
		seen[node.ID] = node
	}
	for _, node := range graph.Nodes {
		for _, required := range node.Requires {
			if _, exists := seen[required]; !exists {
				return fmt.Errorf("saga node %q requires missing node %q", node.ID, required)
			}
		}
	}
	for _, edge := range graph.Edges {
		if _, exists := seen[edge.From]; !exists {
			return fmt.Errorf("saga edge from missing node %q", edge.From)
		}
		if _, exists := seen[edge.To]; !exists {
			return fmt.Errorf("saga edge to missing node %q", edge.To)
		}
	}
	return nil
}

func validateNode(node schema.SagaNode) error {
	if err := paths.ValidateID("node", node.ID); err != nil {
		return err
	}
	if strings.TrimSpace(node.Kind) == "" {
		return fmt.Errorf("saga node %q kind is required", node.ID)
	}
	if node.Risk != "" {
		if err := validateRisk(node.Risk); err != nil {
			return fmt.Errorf("saga node %q: %w", node.ID, err)
		}
	}
	if node.Reversibility != "" {
		if err := validateReversibility(node.Reversibility); err != nil {
			return fmt.Errorf("saga node %q: %w", node.ID, err)
		}
	}
	if node.Retry != nil && node.Retry.MaxAttempts < 0 {
		return fmt.Errorf("saga node %q retry max attempts must not be negative", node.ID)
	}
	if node.Compensate != nil && strings.TrimSpace(node.Compensate.Kind) == "" {
		return fmt.Errorf("saga node %q compensation kind is required", node.ID)
	}
	if len(node.Params) > 0 && !json.Valid(node.Params) {
		return fmt.Errorf("saga node %q params must be valid JSON", node.ID)
	}
	return nil
}

func validateControl(control schema.SagaControl) error {
	if control.SchemaVersion != schema.Version {
		return fmt.Errorf("unsupported saga control schema version %q", control.SchemaVersion)
	}
	if err := paths.ValidateID("saga", control.SagaID); err != nil {
		return err
	}
	if err := validateSagaStatus(control.Status); err != nil {
		return err
	}
	for _, step := range control.CurrentSteps {
		if err := paths.ValidateID("current_step", step); err != nil {
			return err
		}
	}
	for _, result := range control.StepResults {
		if err := validateStepResultRef(result); err != nil {
			return err
		}
	}
	return nil
}

func validateEvent(sagaID string, event schema.Event) error {
	if event.SchemaVersion != schema.Version {
		return fmt.Errorf("unsupported saga event schema version %q", event.SchemaVersion)
	}
	if err := paths.ValidateID("saga", sagaID); err != nil {
		return err
	}
	if err := paths.ValidateID("event", event.ID); err != nil {
		return err
	}
	if event.Subject.Kind != "saga" || event.Subject.Name != sagaID {
		return fmt.Errorf("saga event subject must be saga %q", sagaID)
	}
	if strings.TrimSpace(event.Type) == "" {
		return errors.New("saga event type is required")
	}
	if strings.TrimSpace(event.Summary) == "" {
		return errors.New("saga event summary is required")
	}
	return nil
}

func validateStepResult(result schema.StepResult) error {
	if result.SchemaVersion != schema.Version {
		return fmt.Errorf("unsupported step result schema version %q", result.SchemaVersion)
	}
	if err := paths.ValidateID("saga", result.SagaID); err != nil {
		return err
	}
	if err := paths.ValidateID("step", result.StepID); err != nil {
		return err
	}
	if strings.TrimSpace(result.Kind) == "" {
		return errors.New("step result kind is required")
	}
	if strings.TrimSpace(result.Status) == "" {
		return errors.New("step result status is required")
	}
	if len(result.Result) > 0 && !json.Valid(result.Result) {
		return errors.New("step result payload must be valid JSON")
	}
	return nil
}

func validateStepResultRef(result schema.StepResultRef) error {
	if err := paths.ValidateID("step", result.StepID); err != nil {
		return err
	}
	if strings.TrimSpace(result.Kind) == "" {
		return errors.New("step result kind is required")
	}
	if strings.TrimSpace(result.Status) == "" {
		return errors.New("step result status is required")
	}
	if len(result.Result) > 0 && !json.Valid(result.Result) {
		return errors.New("step result payload must be valid JSON")
	}
	if result.Failure != nil && strings.TrimSpace(result.Failure.Code) == "" {
		return errors.New("step failure code is required")
	}
	return nil
}

func validateRisk(risk schema.Risk) error {
	switch risk {
	case schema.RiskLow, schema.RiskMedium, schema.RiskHigh, schema.RiskCritical:
		return nil
	case "":
		return errors.New("risk is required")
	default:
		return fmt.Errorf("unsupported risk %q", risk)
	}
}

func validateReversibility(value schema.Reversibility) error {
	switch value {
	case schema.Reversible, schema.Compensatable, schema.PartiallyReversible, schema.Irreversible:
		return nil
	case "":
		return errors.New("reversibility is required")
	default:
		return fmt.Errorf("unsupported reversibility %q", value)
	}
}

func validateSagaStatus(status schema.SagaStatus) error {
	switch status {
	case schema.SagaPending, schema.SagaRunning, schema.SagaCompensating, schema.SagaSucceeded, schema.SagaFailed, schema.SagaCanceled:
		return nil
	default:
		return fmt.Errorf("unsupported saga status %q", status)
	}
}

func getStrict(ctx context.Context, store objstore.ObjectStore, key string, out any) (*objstore.Object, error) {
	obj, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := canonical.UnmarshalStrict(obj.Body, out); err != nil {
		return nil, err
	}
	return obj, nil
}

func putOptions(kind, sagaID string) objstore.PutOptions {
	return objstore.PutOptions{
		ContentType: canonical.ContentType,
		Metadata: map[string]string{
			"schema_version": schema.Version,
			"kind":           kind,
			"saga_id":        sagaID,
		},
	}
}

func objectMetaFromObject(obj *objstore.Object) objstore.ObjectMeta {
	return objstore.ObjectMeta{
		Key:         obj.Key,
		ETag:        obj.ETag,
		VersionID:   obj.VersionID,
		Size:        obj.Size,
		ContentType: obj.ContentType,
		Metadata:    obj.Metadata,
		CreatedAt:   obj.CreatedAt,
		UpdatedAt:   obj.UpdatedAt,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
