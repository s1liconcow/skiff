package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

func (s *Store) List(ctx context.Context, opts ListOptions) ([]Summary, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	prefix := "services/"
	if opts.Service != "" {
		if err := paths.ValidateName("service", opts.Service); err != nil {
			return nil, err
		}
		prefix = "services/" + opts.Service + "/operations/"
	}
	metas, err := s.store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0)
	seen := make(map[string]struct{})
	for _, meta := range metas {
		service, operationID, ok := parseOperationControlKey(meta.Key)
		if !ok {
			continue
		}
		if opts.Service != "" && service != opts.Service {
			continue
		}
		doc, err := s.GetControl(ctx, service, operationID)
		if err != nil {
			return nil, err
		}
		if !opts.IncludeTerminal && terminalStatus(doc.Control.Status) {
			continue
		}
		summary := summaryFromControl(*doc)
		if intent, err := s.GetIntent(ctx, service, operationID); err == nil {
			summary.Kind = intent.Intent.Kind
			summary.IntentKey = intent.Key
		}
		out = append(out, summary)
		seen[operationKey(service, operationID)] = struct{}{}
	}
	sagaItems, err := s.listSagaOperationSummaries(ctx, opts, seen)
	if err != nil {
		return nil, err
	}
	out = append(out, sagaItems...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service == out[j].Service {
			if out[i].UpdatedAt == out[j].UpdatedAt {
				return out[i].OperationID < out[j].OperationID
			}
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].Service < out[j].Service
	})
	if opts.Limit > 0 && opts.Limit < len(out) {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (s *Store) listSagaOperationSummaries(ctx context.Context, opts ListOptions, seen map[string]struct{}) ([]Summary, error) {
	metas, err := s.store.List(ctx, "sagas/", objstore.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0)
	for _, meta := range metas {
		sagaID, ok := parseSagaIntentKey(meta.Key)
		if !ok {
			continue
		}
		intentObj, err := s.store.Get(ctx, meta.Key)
		if err != nil {
			return nil, err
		}
		var intent schema.SagaIntent
		if err := canonical.UnmarshalStrict(intentObj.Body, &intent); err != nil {
			return nil, fmt.Errorf("decode saga intent %q: %w", meta.Key, err)
		}
		params := sagaOperationParams(intent)
		if params.OperationID == "" {
			continue
		}
		service := firstNonEmpty(params.Service, intent.Target.Name)
		if service == "" {
			continue
		}
		if opts.Service != "" && service != opts.Service {
			continue
		}
		key := operationKey(service, params.OperationID)
		if _, ok := seen[key]; ok {
			continue
		}
		controlKey, err := paths.SagaControl(sagaID)
		if err != nil {
			return nil, err
		}
		controlObj, err := s.store.Get(ctx, controlKey)
		if err != nil {
			return nil, err
		}
		var control schema.SagaControl
		if err := canonical.UnmarshalStrict(controlObj.Body, &control); err != nil {
			return nil, fmt.Errorf("decode saga control %q: %w", controlKey, err)
		}
		status := operationStatusFromSaga(control.Status)
		if !opts.IncludeTerminal && terminalStatus(status) {
			continue
		}
		out = append(out, Summary{
			OperationID: params.OperationID,
			Service:     service,
			Env:         params.Env,
			Kind:        operationKindFromSaga(intent.Kind),
			Status:      status,
			UpdatedAt:   control.UpdatedAt,
			TraceID:     firstNonEmpty(control.TraceID, intent.TraceID),
			Resumable:   status == schema.OperationPending || status == schema.OperationRunning,
			ControlKey:  controlKey,
			IntentKey:   meta.Key,
		})
		seen[key] = struct{}{}
	}
	return out, nil
}

func (s *Store) Inspect(ctx context.Context, service, operationID string) (*InspectResult, error) {
	control, err := s.GetControl(ctx, service, operationID)
	if err != nil {
		return nil, err
	}
	result := inspectFromControl(*control)
	intentKey, err := paths.OperationIntent(service, operationID)
	if err == nil {
		result.Paths["intent"] = intentKey
	}
	if eventsPrefix, err := paths.OperationEventsPrefix(service, operationID); err == nil {
		result.Paths["events"] = eventsPrefix
	}
	intent, err := s.GetIntent(ctx, service, operationID)
	if err == nil {
		result.Intent = &intent.Intent
		result.Kind = intent.Intent.Kind
		result.Target = intent.Intent.Target
		result.Risk = intent.Intent.Risk
		result.Reversibility = intent.Intent.Reversibility
		result.TraceID = firstNonEmpty(result.TraceID, intent.Intent.TraceID)
	}
	return &result, nil
}

func (s *Store) GetIntent(ctx context.Context, service, operationID string) (*IntentDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		return nil, err
	}
	obj, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var intent schema.OperationIntent
	if err := canonical.UnmarshalStrict(obj.Body, &intent); err != nil {
		return nil, fmt.Errorf("decode operation intent %q: %w", key, err)
	}
	if intent.OperationID != operationID {
		return nil, fmt.Errorf("operation intent %q names operation %q", key, intent.OperationID)
	}
	if intent.Service != service {
		return nil, fmt.Errorf("operation intent %q names service %q", key, intent.Service)
	}
	meta := objectMetaFromObject(obj)
	return &IntentDocument{Key: key, ETag: meta.ETag, Meta: meta, Intent: intent}, nil
}

func (s *Store) GetControl(ctx context.Context, service, operationID string) (*ControlDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		return nil, err
	}
	obj, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return nil, fmt.Errorf("decode operation control %q: %w", key, err)
	}
	if control.SchemaVersion != schema.Version {
		return nil, fmt.Errorf("operation control %q has schema version %q", key, control.SchemaVersion)
	}
	if control.OperationID != operationID {
		return nil, fmt.Errorf("operation control %q names operation %q", key, control.OperationID)
	}
	if control.Service != service {
		return nil, fmt.Errorf("operation control %q names service %q", key, control.Service)
	}
	if control.Lease != nil {
		if _, err := leaseExpires(*control.Lease); err != nil {
			return nil, err
		}
	}
	meta := objectMetaFromObject(obj)
	return &ControlDocument{Key: key, ETag: meta.ETag, Meta: meta, Control: control}, nil
}

func (s *Store) UpdateControlCAS(ctx context.Context, current *ControlDocument, next schema.OperationControl) (*ControlDocument, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, errors.New("current operation control document is required")
	}
	if next.SchemaVersion == "" {
		next.SchemaVersion = schema.Version
	}
	next.OperationID = current.Control.OperationID
	next.Service = current.Control.Service
	if next.Env == "" {
		next.Env = current.Control.Env
	}
	next.UpdatedAt = canonical.Time(s.clock().UTC())
	body, err := canonical.Marshal(next)
	if err != nil {
		return nil, err
	}
	meta, err := s.store.CompareAndSwap(ctx, current.Key, current.ETag, body, objstore.PutOptions{ContentType: canonical.ContentType})
	if err != nil {
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			return nil, fmt.Errorf("%w: operation control ETag is stale", state.ErrPreconditionFailed)
		}
		return nil, err
	}
	return &ControlDocument{Key: current.Key, ETag: meta.ETag, Meta: *meta, Control: next}, nil
}

func (s *Store) requireStore() error {
	if s == nil || s.store == nil {
		return errors.New("object store is required")
	}
	return nil
}

func summaryFromControl(doc ControlDocument) Summary {
	return Summary{
		OperationID:        doc.Control.OperationID,
		Service:            doc.Control.Service,
		Env:                doc.Control.Env,
		Status:             doc.Control.Status,
		Lease:              cloneLease(doc.Control.Lease),
		ProviderOperations: append([]schema.ProviderOperationRef(nil), doc.Control.ProviderOperations...),
		UpdatedAt:          doc.Control.UpdatedAt,
		TraceID:            doc.Control.TraceID,
		Resumable:          resumableControl(doc.Control),
		ControlKey:         doc.Key,
	}
}

func inspectFromControl(doc ControlDocument) InspectResult {
	controlKey, _ := paths.OperationControl(doc.Control.Service, doc.Control.OperationID)
	return InspectResult{
		OperationID:        doc.Control.OperationID,
		Service:            doc.Control.Service,
		Env:                doc.Control.Env,
		Status:             doc.Control.Status,
		Lease:              cloneLease(doc.Control.Lease),
		ProviderOperations: append([]schema.ProviderOperationRef(nil), doc.Control.ProviderOperations...),
		StepResults:        append([]schema.StepResultRef(nil), doc.Control.StepResults...),
		UpdatedAt:          doc.Control.UpdatedAt,
		TraceID:            doc.Control.TraceID,
		Resumable:          resumableControl(doc.Control),
		Control:            doc.Control,
		Paths: map[string]string{
			"control": firstNonEmpty(controlKey, doc.Key),
		},
	}
}

func terminalStatus(status schema.OperationStatus) bool {
	switch status {
	case schema.OperationSucceeded, schema.OperationFailed, schema.OperationCanceled:
		return true
	default:
		return false
	}
}

func operationStatusFromSaga(status schema.SagaStatus) schema.OperationStatus {
	switch status {
	case schema.SagaPending:
		return schema.OperationPending
	case schema.SagaRunning, schema.SagaCompensating:
		return schema.OperationRunning
	case schema.SagaSucceeded:
		return schema.OperationSucceeded
	case schema.SagaFailed:
		return schema.OperationFailed
	case schema.SagaCanceled:
		return schema.OperationCanceled
	default:
		return schema.OperationRunning
	}
}

func operationKindFromSaga(kind string) string {
	switch kind {
	case "deployment.canary":
		return "canary-deploy"
	default:
		return kind
	}
}

func sagaOperationParams(intent schema.SagaIntent) struct {
	Service     string `json:"service"`
	Env         string `json:"env"`
	OperationID string `json:"operation_id"`
} {
	var params struct {
		Service     string `json:"service"`
		Env         string `json:"env"`
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(intent.Params, &params)
	return params
}

func operationKey(service, operationID string) string {
	return service + "\x00" + operationID
}

func resumableControl(control schema.OperationControl) bool {
	return !terminalStatus(control.Status) && len(control.ProviderOperations) > 0
}

func parseOperationControlKey(key string) (string, string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[0] != "services" || parts[2] != "operations" || parts[4] != "control.json" {
		return "", "", false
	}
	if parts[1] == "" || parts[3] == "" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func parseSagaIntentKey(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "sagas" || parts[2] != "intent.json" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
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

func cloneLease(lease *schema.Lease) *schema.Lease {
	if lease == nil {
		return nil
	}
	next := *lease
	return &next
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
