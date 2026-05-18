package index

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	skiffevents "github.com/s1liconcow/skiff/internal/events"
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

	statefulMetas, err := store.List(ctx, "stateful/", objstore.ListOptions{Limit: opts.ListLimit})
	if err != nil {
		return Snapshot{}, err
	}
	for _, meta := range statefulMetas {
		switch {
		case strings.HasSuffix(meta.Key, "/control.json") && strings.Contains(meta.Key, "/members/"):
			addStatefulMemberControl(ctx, store, meta.Key, &snapshot)
		case strings.HasSuffix(meta.Key, "/control.json"):
			addStatefulGroupControl(ctx, store, meta.Key, &snapshot)
		case strings.HasSuffix(meta.Key, "/record.json") && strings.Contains(meta.Key, "/backups/"):
			addStatefulBackupRecord(ctx, store, meta.Key, &snapshot)
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

func addStatefulGroupControl(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	group, ok := statefulGroupFromControlKey(key)
	if !ok {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "MALFORMED_STATEFUL_GROUP_CONTROL_KEY", Summary: "stateful group control key does not match stateful/<group>/control.json", Key: key})
		return
	}
	var control schema.StatefulGroupControl
	if !readObject(ctx, store, key, &control, "MALFORMED_STATEFUL_GROUP_CONTROL", snapshot) {
		return
	}
	if control.Group == "" {
		control.Group = group
	}
	summary := StatefulGroupSummary{
		Group:     control.Group,
		Env:       control.Env,
		Replicas:  control.Replicas,
		Lease:     cloneLease(control.Lease),
		UpdatedAt: control.UpdatedAt,
	}
	for _, member := range control.Members {
		summary.Members = append(summary.Members, StatefulMemberSummary{
			Member:             member.Member,
			Generation:         member.Generation,
			ExpectedGeneration: member.Generation,
			ReleaseID:          member.ReleaseID,
			ReleaseManifestKey: member.ReleaseManifestKey,
			RuntimeManifestKey: member.RuntimeManifestKey,
			InstanceID:         member.InstanceID,
			ExpectedInstanceID: member.InstanceID,
			VolumeID:           member.VolumeID,
			ExpectedVolumeID:   member.VolumeID,
			DNSName:            member.DNSName,
			ExpectedDNSName:    member.DNSName,
			Phase:              member.Phase,
			ExpectedPhase:      member.Phase,
		})
	}
	if control.Operation != nil {
		summary.OperationID = control.Operation.ID
		summary.OperationKind = control.Operation.Kind
		summary.OperationState = control.Operation.State
	}
	upsertStatefulGroup(snapshot, summary)
}

func addStatefulMemberControl(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	group, member, ok := statefulMemberFromControlKey(key)
	if !ok {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "MALFORMED_STATEFUL_MEMBER_CONTROL_KEY", Summary: "stateful member control key does not match stateful/<group>/members/<member>/control.json", Key: key})
		return
	}
	var control schema.StatefulMemberControl
	if !readObject(ctx, store, key, &control, "MALFORMED_STATEFUL_MEMBER_CONTROL", snapshot) {
		return
	}
	if control.Group == "" {
		control.Group = group
	}
	if control.Member != member {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "STATEFUL_MEMBER_KEY_MISMATCH", Summary: "stateful member control ordinal does not match object key", Key: key})
		control.Member = member
	}
	upsertStatefulMember(snapshot, control.Group, StatefulMemberSummary{
		Member:             control.Member,
		Env:                control.Env,
		Zone:               control.Zone,
		Generation:         control.Generation,
		ReleaseID:          control.ReleaseID,
		ReleaseManifestKey: control.ReleaseManifestKey,
		RuntimeManifestKey: control.RuntimeManifestKey,
		InstanceID:         control.InstanceID,
		VolumeID:           control.VolumeID,
		DNSName:            control.DNSName,
		Phase:              control.Phase,
		Lease:              cloneLease(control.Lease),
		ProviderOperations: append([]schema.ProviderOperationRef(nil), control.ProviderOperations...),
		UpdatedAt:          control.UpdatedAt,
	})
}

func addStatefulBackupRecord(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	group, backup, ok := statefulBackupFromRecordKey(key)
	if !ok {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "MALFORMED_STATEFUL_BACKUP_RECORD_KEY", Summary: "stateful backup record key does not match stateful/<group>/backups/<backup>/record.json", Key: key})
		return
	}
	var record struct {
		SchemaVersion     string                      `json:"schema_version"`
		BackupID          string                      `json:"backup_id"`
		Group             string                      `json:"group"`
		Env               string                      `json:"env,omitempty"`
		Member            int                         `json:"member"`
		VolumeID          string                      `json:"volume_id"`
		SnapshotID        string                      `json:"snapshot_id"`
		Provider          string                      `json:"provider,omitempty"`
		ProviderID        string                      `json:"provider_id,omitempty"`
		ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
		RecipeBackup      *struct {
			OK      bool              `json:"ok"`
			Summary string            `json:"summary,omitempty"`
			Facts   map[string]string `json:"facts,omitempty"`
		} `json:"recipe_backup,omitempty"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
		ExpiresAt string `json:"expires_at,omitempty"`
	}
	if !readObject(ctx, store, key, &record, "MALFORMED_STATEFUL_BACKUP_RECORD", snapshot) {
		return
	}
	if record.Group == "" {
		record.Group = group
	}
	if record.BackupID == "" {
		record.BackupID = backup
	}
	recipeStatus := ""
	recipeSummary := ""
	if record.RecipeBackup != nil {
		recipeStatus = "unhealthy"
		if record.RecipeBackup.OK {
			recipeStatus = "ok"
		}
		recipeSummary = record.RecipeBackup.Summary
	}
	upsertStatefulBackup(snapshot, record.Group, StatefulBackupSummary{
		BackupID:          record.BackupID,
		Member:            record.Member,
		VolumeID:          record.VolumeID,
		SnapshotID:        record.SnapshotID,
		Provider:          record.Provider,
		ProviderID:        record.ProviderID,
		ProviderOperation: record.ProviderOperation,
		Status:            record.Status,
		RecipeStatus:      recipeStatus,
		RecipeSummary:     recipeSummary,
		CreatedAt:         record.CreatedAt,
		ExpiresAt:         record.ExpiresAt,
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
		event, err := decodeIndexedEvent(obj.Body)
		if err != nil {
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

func decodeIndexedEvent(body []byte) (schema.Event, error) {
	var event schema.Event
	if err := canonical.UnmarshalStrict(body, &event); err == nil {
		return event, nil
	}
	var logged skiffevents.Event
	if err := canonical.UnmarshalStrict(body, &logged); err != nil {
		return schema.Event{}, err
	}
	return schema.Event{
		SchemaVersion: logged.SchemaVersion,
		ID:            logged.ID,
		Time:          logged.Time,
		TraceID:       logged.TraceID,
		Subject:       subjectFromIndexedEventScope(logged.Scope),
		Type:          logged.Type,
		Severity:      logged.Severity,
		Actor:         logged.Actor,
		Summary:       logged.Summary,
		Facts:         logged.Facts,
		Data:          logged.Data,
	}, nil
}

func subjectFromIndexedEventScope(scope skiffevents.Scope) schema.Target {
	switch scope.Kind {
	case skiffevents.ScopeOperation:
		return schema.Target{Kind: "operation", Name: scope.Operation}
	case skiffevents.ScopeSaga:
		return schema.Target{Kind: "saga", Name: scope.Saga}
	default:
		return schema.Target{Kind: "service", Name: scope.Service}
	}
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
	sort.Slice(snapshot.StatefulGroups, func(i, j int) bool {
		if snapshot.StatefulGroups[i].Group == snapshot.StatefulGroups[j].Group {
			return snapshot.StatefulGroups[i].Env < snapshot.StatefulGroups[j].Env
		}
		return snapshot.StatefulGroups[i].Group < snapshot.StatefulGroups[j].Group
	})
	for i := range snapshot.StatefulGroups {
		sort.Slice(snapshot.StatefulGroups[i].Members, func(a, b int) bool {
			return snapshot.StatefulGroups[i].Members[a].Member < snapshot.StatefulGroups[i].Members[b].Member
		})
		sort.Slice(snapshot.StatefulGroups[i].Backups, func(a, b int) bool {
			if snapshot.StatefulGroups[i].Backups[a].Member == snapshot.StatefulGroups[i].Backups[b].Member {
				return snapshot.StatefulGroups[i].Backups[a].CreatedAt > snapshot.StatefulGroups[i].Backups[b].CreatedAt
			}
			return snapshot.StatefulGroups[i].Backups[a].Member < snapshot.StatefulGroups[i].Backups[b].Member
		})
	}
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

func statefulGroupFromControlKey(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "stateful" || parts[2] != "control.json" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func statefulMemberFromControlKey(key string) (string, int, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[0] != "stateful" || parts[2] != "members" || parts[4] != "control.json" || parts[1] == "" {
		return "", 0, false
	}
	member, err := strconv.Atoi(parts[3])
	if err != nil || member < 0 {
		return "", 0, false
	}
	return parts[1], member, true
}

func statefulBackupFromRecordKey(key string) (string, string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[0] != "stateful" || parts[2] != "backups" || parts[4] != "record.json" || parts[1] == "" || parts[3] == "" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func upsertStatefulGroup(snapshot *Snapshot, next StatefulGroupSummary) {
	for i := range snapshot.StatefulGroups {
		if snapshot.StatefulGroups[i].Group != next.Group {
			continue
		}
		current := snapshot.StatefulGroups[i]
		next.Members = mergeStatefulMembers(current.Members, next.Members)
		next.Backups = append(next.Backups, current.Backups...)
		snapshot.StatefulGroups[i] = next
		return
	}
	snapshot.StatefulGroups = append(snapshot.StatefulGroups, next)
}

func upsertStatefulMember(snapshot *Snapshot, group string, next StatefulMemberSummary) {
	for i := range snapshot.StatefulGroups {
		if snapshot.StatefulGroups[i].Group == group {
			snapshot.StatefulGroups[i].Members = mergeStatefulMembers(snapshot.StatefulGroups[i].Members, []StatefulMemberSummary{next})
			if snapshot.StatefulGroups[i].Env == "" {
				snapshot.StatefulGroups[i].Env = next.Env
			}
			return
		}
	}
	snapshot.StatefulGroups = append(snapshot.StatefulGroups, StatefulGroupSummary{Group: group, Env: next.Env, Members: []StatefulMemberSummary{next}})
}

func upsertStatefulBackup(snapshot *Snapshot, group string, next StatefulBackupSummary) {
	for i := range snapshot.StatefulGroups {
		if snapshot.StatefulGroups[i].Group == group {
			snapshot.StatefulGroups[i].Backups = append(snapshot.StatefulGroups[i].Backups, next)
			return
		}
	}
	snapshot.StatefulGroups = append(snapshot.StatefulGroups, StatefulGroupSummary{Group: group, Backups: []StatefulBackupSummary{next}})
}

func mergeStatefulMembers(existing, incoming []StatefulMemberSummary) []StatefulMemberSummary {
	out := append([]StatefulMemberSummary(nil), existing...)
	for _, next := range incoming {
		found := false
		for i := range out {
			if out[i].Member == next.Member {
				out[i] = mergeStatefulMember(out[i], next)
				found = true
				break
			}
		}
		if !found {
			out = append(out, next)
		}
	}
	return out
}

func mergeStatefulMember(left, right StatefulMemberSummary) StatefulMemberSummary {
	if right.Env != "" || right.Zone != "" || right.UpdatedAt != "" || len(right.ProviderOperations) > 0 || right.Lease != nil {
		right.ExpectedGeneration = firstNonZeroInt64(right.ExpectedGeneration, left.ExpectedGeneration)
		right.ExpectedInstanceID = firstNonEmptyString(right.ExpectedInstanceID, left.ExpectedInstanceID)
		right.ExpectedVolumeID = firstNonEmptyString(right.ExpectedVolumeID, left.ExpectedVolumeID)
		right.ExpectedDNSName = firstNonEmptyString(right.ExpectedDNSName, left.ExpectedDNSName)
		right.ExpectedPhase = firstNonEmptyString(right.ExpectedPhase, left.ExpectedPhase)
		right.Role = firstNonEmptyString(right.Role, left.Role)
		right.RecipeStatus = firstNonEmptyString(right.RecipeStatus, left.RecipeStatus)
		right.RecipeSummary = firstNonEmptyString(right.RecipeSummary, left.RecipeSummary)
		if right.Generation == 0 {
			right.Generation = left.Generation
		}
		if right.InstanceID == "" {
			right.InstanceID = left.InstanceID
		}
		if right.VolumeID == "" {
			right.VolumeID = left.VolumeID
		}
		if right.DNSName == "" {
			right.DNSName = left.DNSName
		}
		if right.Phase == "" {
			right.Phase = left.Phase
		}
		if right.ReleaseID == "" {
			right.ReleaseID = left.ReleaseID
		}
		if right.ReleaseManifestKey == "" {
			right.ReleaseManifestKey = left.ReleaseManifestKey
		}
		if right.RuntimeManifestKey == "" {
			right.RuntimeManifestKey = left.RuntimeManifestKey
		}
		return right
	}
	return left
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneLease(lease *schema.Lease) *schema.Lease {
	if lease == nil {
		return nil
	}
	out := *lease
	return &out
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
