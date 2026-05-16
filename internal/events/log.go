package events

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
)

var ErrDuplicate = errors.New("events: duplicate event id")

type Log struct {
	store objstore.ObjectStore
	clock func() time.Time
}

type Options struct {
	Store objstore.ObjectStore
	Clock func() time.Time
}

type ListOptions struct {
	Limit int
}

func NewLog(opts Options) (*Log, error) {
	if opts.Store == nil {
		return nil, errors.New("event log store is required")
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Log{store: opts.Store, clock: clock}, nil
}

func (l *Log) Append(ctx context.Context, event Event) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	event = normalizeEvent(event, l.clock())
	key, err := keyForEvent(event)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(event)
	if err != nil {
		return nil, err
	}
	meta, err := l.store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	if err != nil {
		if errors.Is(err, objstore.ErrAlreadyExists) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return meta, nil
}

func (l *Log) AppendAudit(ctx context.Context, record AuditRecord) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record = normalizeAudit(record, l.clock())
	t, err := time.Parse(time.RFC3339Nano, record.Time)
	if err != nil {
		return nil, fmt.Errorf("audit time: %w", err)
	}
	key, err := paths.AuditEventForTime(t, record.ID)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(record)
	if err != nil {
		return nil, err
	}
	meta, err := l.store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	if err != nil {
		if errors.Is(err, objstore.ErrAlreadyExists) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return meta, nil
}

func (l *Log) List(ctx context.Context, scope Scope, opts ListOptions) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix, err := prefixForScope(scope)
	if err != nil {
		return nil, err
	}
	metas, err := l.store.List(ctx, prefix, objstore.ListOptions{Limit: int32(opts.Limit)})
	if err != nil {
		return nil, err
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Key < metas[j].Key
	})
	events := make([]Event, 0, len(metas))
	for _, meta := range metas {
		object, err := l.store.Get(ctx, meta.Key)
		if err != nil {
			return nil, err
		}
		var event Event
		if err := canonical.UnmarshalStrict(object.Body, &event); err != nil {
			return nil, fmt.Errorf("decode event %q: %w", meta.Key, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func keyForEvent(event Event) (string, error) {
	switch event.Scope.Kind {
	case ScopeService:
		return paths.ServiceEvent(event.Scope.Service, event.ID)
	case ScopeOperation:
		return paths.OperationEvent(event.Scope.Service, event.Scope.Operation, event.ID)
	case ScopeSaga:
		return paths.SagaEvent(event.Scope.Saga, event.ID)
	default:
		return "", fmt.Errorf("unsupported event scope %q", event.Scope.Kind)
	}
}

func prefixForScope(scope Scope) (string, error) {
	switch scope.Kind {
	case ScopeService:
		return paths.ServiceEventsPrefix(scope.Service)
	case ScopeOperation:
		return paths.OperationEventsPrefix(scope.Service, scope.Operation)
	case ScopeSaga:
		return paths.SagaEventsPrefix(scope.Saga)
	default:
		return "", fmt.Errorf("unsupported event scope %q", scope.Kind)
	}
}

func normalizeEvent(event Event, now time.Time) Event {
	if event.SchemaVersion == "" {
		event.SchemaVersion = SchemaVersion
	}
	if event.ID == "" {
		event.ID = NewID(now, event.Scope.Service+event.Scope.Operation+event.Scope.Saga+event.Type+event.Summary)
	}
	if event.Time == "" {
		event.Time = canonical.Time(now)
	}
	event.Type = strings.TrimSpace(event.Type)
	event.Summary = strings.TrimSpace(event.Summary)
	return event
}

func normalizeAudit(record AuditRecord, now time.Time) AuditRecord {
	if record.SchemaVersion == "" {
		record.SchemaVersion = SchemaVersion
	}
	if record.ID == "" {
		record.ID = NewID(now, record.Actor.ID+record.Action+record.Target.Kind+record.Target.Name)
	}
	if record.Time == "" {
		record.Time = canonical.Time(now)
	}
	record.Action = strings.TrimSpace(record.Action)
	record.Summary = strings.TrimSpace(record.Summary)
	return record
}
