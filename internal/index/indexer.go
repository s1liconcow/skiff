package index

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const DefaultRecentEventsLimit = 200

type Options struct {
	RecentEventsLimit int
	ListLimit         int32
	Clock             func() time.Time
}

type Index struct {
	store             objstore.ObjectStore
	recentEventsLimit int
	listLimit         int32
	clock             func() time.Time
	current           AtomicSnapshot
}

func New(store objstore.ObjectStore, opts Options) (*Index, error) {
	if store == nil {
		return nil, errors.New("index object store is required")
	}
	limit := opts.RecentEventsLimit
	if limit <= 0 {
		limit = DefaultRecentEventsLimit
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	idx := &Index{
		store:             store,
		recentEventsLimit: limit,
		listLimit:         opts.ListLimit,
		clock:             clock,
	}
	idx.current.Store(Snapshot{})
	return idx, nil
}

func (i *Index) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return i.current.Load(), nil
}

func (i *Index) Rebuild(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	current := i.current.Load()
	next, err := BuildSnapshot(ctx, i.store, BuildOptions{
		Now:               i.clock(),
		Generation:        current.Generation + 1,
		RecentEventsLimit: i.recentEventsLimit,
		ListLimit:         i.listLimit,
	})
	if err != nil {
		return Snapshot{}, err
	}
	i.current.Store(next)
	return CloneSnapshot(next), nil
}

func (i *Index) RunRefreshLoop(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("refresh interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := i.Rebuild(ctx); err != nil {
				return err
			}
		}
	}
}

type BuildOptions struct {
	Now               time.Time
	Generation        int64
	RecentEventsLimit int
	ListLimit         int32
}

func BuildSnapshot(ctx context.Context, store objstore.ObjectStore, opts BuildOptions) (Snapshot, error) {
	if store == nil {
		return Snapshot{}, errors.New("object store is required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	limit := opts.RecentEventsLimit
	if limit <= 0 {
		limit = DefaultRecentEventsLimit
	}
	snapshot := Snapshot{
		Ready:          true,
		Generation:     opts.Generation,
		RefreshedAt:    now.UTC(),
		LastFullScanAt: now.UTC(),
	}
	if snapshot.Generation <= 0 {
		snapshot.Generation = 1
	}

	serviceMetas, err := store.List(ctx, "services/", objstore.ListOptions{Limit: opts.ListLimit})
	if err != nil {
		return Snapshot{}, err
	}
	for _, meta := range serviceMetas {
		switch {
		case strings.HasSuffix(meta.Key, "/control.json") && strings.Contains(meta.Key, "/operations/"):
			addOperationControl(ctx, store, meta.Key, &snapshot)
		case strings.HasSuffix(meta.Key, "/control.json"):
			addServiceControl(ctx, store, meta.Key, &snapshot)
		}
	}

	sagaMetas, err := store.List(ctx, "sagas/", objstore.ListOptions{Limit: opts.ListLimit})
	if err != nil {
		return Snapshot{}, err
	}
	for _, meta := range sagaMetas {
		if strings.HasSuffix(meta.Key, "/control.json") {
			addSagaControl(ctx, store, meta.Key, &snapshot)
		}
	}

	resourceMetas, err := store.List(ctx, "resources/", objstore.ListOptions{Limit: opts.ListLimit})
	if err != nil {
		return Snapshot{}, err
	}
	for _, meta := range resourceMetas {
		if strings.HasSuffix(meta.Key, ".json") {
			addResourceRecord(ctx, store, meta.Key, &snapshot)
		}
	}

	events, findings, err := eventsFromObjectStore(ctx, store, limit, opts.ListLimit)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.RecentEvents = events
	snapshot.Findings = append(snapshot.Findings, findings...)
	sortSnapshot(&snapshot)
	return snapshot, nil
}

func addServiceControl(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	service, ok := serviceFromControlKey(key)
	if !ok {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "MALFORMED_SERVICE_CONTROL_KEY", Summary: "service control key does not match services/<service>/control.json", Key: key})
		return
	}
	var control schema.ServiceControl
	if !readObject(ctx, store, key, &control, "MALFORMED_SERVICE_CONTROL", snapshot) {
		return
	}
	if control.Service == "" {
		control.Service = service
	}
	summary := ServiceSummary{
		Service:        control.Service,
		Env:            control.Env,
		DesiredRelease: control.DesiredRelease,
		StableRelease:  control.StableRelease,
		UpdatedAt:      control.UpdatedAt,
	}
	if control.Operation != nil {
		summary.OperationID = control.Operation.ID
		summary.OperationKind = control.Operation.Kind
		summary.OperationState = control.Operation.State
	}
	snapshot.Services = append(snapshot.Services, summary)
}

func addOperationControl(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	var control schema.OperationControl
	if !readObject(ctx, store, key, &control, "MALFORMED_OPERATION_CONTROL", snapshot) {
		return
	}
	snapshot.Operations = append(snapshot.Operations, OperationSummary{
		OperationID:        control.OperationID,
		Service:            control.Service,
		Env:                control.Env,
		Status:             control.Status,
		UpdatedAt:          control.UpdatedAt,
		TraceID:            control.TraceID,
		ProviderOperations: append([]schema.ProviderOperationRef(nil), control.ProviderOperations...),
	})
}

func addSagaControl(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	saga, ok := sagaFromControlKey(key)
	if !ok {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "MALFORMED_SAGA_CONTROL_KEY", Summary: "saga control key does not match sagas/<saga>/control.json", Key: key})
		return
	}
	var control schema.SagaControl
	if !readObject(ctx, store, key, &control, "MALFORMED_SAGA_CONTROL", snapshot) {
		return
	}
	if control.SagaID == "" {
		control.SagaID = saga
	}
	snapshot.Sagas = append(snapshot.Sagas, SagaSummary{
		SagaID:       control.SagaID,
		Status:       control.Status,
		CurrentSteps: append([]string(nil), control.CurrentSteps...),
		UpdatedAt:    control.UpdatedAt,
		TraceID:      control.TraceID,
	})
}

func addResourceRecord(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	var record schema.ResourceRecord
	if !readObject(ctx, store, key, &record, "MALFORMED_RESOURCE_RECORD", snapshot) {
		return
	}
	snapshot.Resources = append(snapshot.Resources, ResourceSummary{
		Provider:    record.Provider.Provider,
		Kind:        record.Provider.Kind,
		ID:          record.Provider.ID,
		Service:     record.Service,
		Env:         record.Env,
		LogicalKind: record.Logical.Kind,
		LogicalName: record.Logical.Name,
		ObservedAt:  record.ObservedAt,
	})
}

func readObject(ctx context.Context, store objstore.ObjectStore, key string, out any, malformedCode string, snapshot *Snapshot) bool {
	obj, err := store.Get(ctx, key)
	if err != nil {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "OBJECT_READ_FAILED", Summary: err.Error(), Key: key})
		return false
	}
	if err := canonical.UnmarshalStrict(obj.Body, out); err != nil {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: malformedCode, Summary: err.Error(), Key: key})
		return false
	}
	return true
}

func eventsFromObjectStore(ctx context.Context, store objstore.ObjectStore, recentLimit int, listLimit int32) ([]schema.Event, []Finding, error) {
	var metas []objstore.ObjectMeta
	for _, prefix := range []string{"services/", "sagas/"} {
		listed, err := store.List(ctx, prefix, objstore.ListOptions{Limit: listLimit})
		if err != nil {
			return nil, nil, err
		}
		metas = append(metas, listed...)
	}

	events := make([]schema.Event, 0)
	findings := make([]Finding, 0)
	seen := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		if _, ok := seen[meta.Key]; ok {
			continue
		}
		seen[meta.Key] = struct{}{}
		if !isEventKey(meta.Key) {
			continue
		}
		obj, err := store.Get(ctx, meta.Key)
		if err != nil {
			return nil, nil, err
		}
		var event schema.Event
		if err := canonical.UnmarshalStrict(obj.Body, &event); err != nil {
			findings = append(findings, Finding{Code: "MALFORMED_EVENT", Summary: err.Error(), Key: meta.Key})
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time == events[j].Time {
			return events[i].ID < events[j].ID
		}
		return events[i].Time < events[j].Time
	})
	if recentLimit > 0 && recentLimit < len(events) {
		events = events[len(events)-recentLimit:]
	}
	return events, findings, nil
}

func sortSnapshot(snapshot *Snapshot) {
	sort.Slice(snapshot.Services, func(i, j int) bool {
		if snapshot.Services[i].Service == snapshot.Services[j].Service {
			return snapshot.Services[i].Env < snapshot.Services[j].Env
		}
		return snapshot.Services[i].Service < snapshot.Services[j].Service
	})
	sort.Slice(snapshot.Sagas, func(i, j int) bool {
		return snapshot.Sagas[i].SagaID < snapshot.Sagas[j].SagaID
	})
	sort.Slice(snapshot.Operations, func(i, j int) bool {
		if snapshot.Operations[i].Service == snapshot.Operations[j].Service {
			return snapshot.Operations[i].OperationID < snapshot.Operations[j].OperationID
		}
		return snapshot.Operations[i].Service < snapshot.Operations[j].Service
	})
	sort.Slice(snapshot.Resources, func(i, j int) bool {
		if snapshot.Resources[i].Provider == snapshot.Resources[j].Provider {
			if snapshot.Resources[i].Kind == snapshot.Resources[j].Kind {
				return snapshot.Resources[i].ID < snapshot.Resources[j].ID
			}
			return snapshot.Resources[i].Kind < snapshot.Resources[j].Kind
		}
		return snapshot.Resources[i].Provider < snapshot.Resources[j].Provider
	})
	sort.Slice(snapshot.RecentEvents, func(i, j int) bool {
		if snapshot.RecentEvents[i].Time == snapshot.RecentEvents[j].Time {
			return snapshot.RecentEvents[i].ID < snapshot.RecentEvents[j].ID
		}
		return snapshot.RecentEvents[i].Time < snapshot.RecentEvents[j].Time
	})
}

func serviceFromControlKey(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "services" || parts[2] != "control.json" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func sagaFromControlKey(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "sagas" || parts[2] != "control.json" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func isEventKey(key string) bool {
	return strings.Contains(key, "/events/") && strings.HasSuffix(key, ".json")
}

func serviceKey(service string) string {
	return "services/" + service + "/control.json"
}

func sagaKey(saga string) string {
	return "sagas/" + saga + "/control.json"
}

func operationKey(service, operation string) string {
	return fmt.Sprintf("services/%s/operations/%s/control.json", service, operation)
}
