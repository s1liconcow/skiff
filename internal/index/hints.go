package index

import (
	"context"
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Hint struct {
	Key string
}

func (i *Index) ApplyHint(ctx context.Context, hint Hint) (Snapshot, error) {
	return i.RefreshKey(ctx, hint.Key)
}

func (i *Index) RefreshKey(ctx context.Context, key string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	current := i.current.Load()
	next := CloneSnapshot(current)
	next.Generation++
	if next.Generation <= 0 {
		next.Generation = 1
	}
	next.RefreshedAt = i.clock().UTC()

	switch {
	case isServiceControlKey(key):
		removeService(&next, serviceNameFromKey(key))
		addServiceControl(ctx, i.store, key, &next)
	case isOperationControlKey(key):
		service, operation := operationNamesFromKey(key)
		removeOperation(&next, service, operation)
		addOperationControl(ctx, i.store, key, &next)
	case isSagaControlKey(key):
		removeSaga(&next, sagaNameFromKey(key))
		addSagaControl(ctx, i.store, key, &next)
	case isStatefulGroupControlKey(key):
		removeStatefulGroup(&next, statefulGroupNameFromKey(key))
		addStatefulGroupControl(ctx, i.store, key, &next)
	case isStatefulMemberControlKey(key):
		group, member := statefulMemberNamesFromKey(key)
		removeStatefulMember(&next, group, member)
		addStatefulMemberControl(ctx, i.store, key, &next)
	case isStatefulBackupRecordKey(key):
		group, backup := statefulBackupNamesFromKey(key)
		removeStatefulBackup(&next, group, backup)
		addStatefulBackupRecord(ctx, i.store, key, &next)
	case strings.HasPrefix(key, "resources/") && strings.HasSuffix(key, ".json"):
		removeResourceByKeyShape(&next, key)
		addResourceRecord(ctx, i.store, key, &next)
	case isEventKey(key):
		addEventByKey(ctx, i.store, key, &next)
	default:
		next.Findings = append(next.Findings, Finding{Code: "UNKNOWN_HINT_KEY", Summary: "hint key is not indexed", Key: key})
	}
	sortSnapshot(&next)
	trimRecentEvents(&next, i.recentEventsLimit)
	i.current.Store(next)
	return CloneSnapshot(next), nil
}

func addEventByKey(ctx context.Context, store objstore.ObjectStore, key string, snapshot *Snapshot) {
	obj, err := store.Get(ctx, key)
	if err != nil {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "OBJECT_READ_FAILED", Summary: err.Error(), Key: key})
		return
	}
	var event schema.Event
	if err := canonical.UnmarshalStrict(obj.Body, &event); err != nil {
		snapshot.Findings = append(snapshot.Findings, Finding{Code: "MALFORMED_EVENT", Summary: err.Error(), Key: key})
		return
	}
	filtered := snapshot.RecentEvents[:0]
	for _, item := range snapshot.RecentEvents {
		if item.ID != event.ID {
			filtered = append(filtered, item)
		}
	}
	snapshot.RecentEvents = append(filtered, event)
}

func removeService(snapshot *Snapshot, service string) {
	filtered := snapshot.Services[:0]
	for _, item := range snapshot.Services {
		if item.Service != service {
			filtered = append(filtered, item)
		}
	}
	snapshot.Services = filtered
}

func removeSaga(snapshot *Snapshot, saga string) {
	filtered := snapshot.Sagas[:0]
	for _, item := range snapshot.Sagas {
		if item.SagaID != saga {
			filtered = append(filtered, item)
		}
	}
	snapshot.Sagas = filtered
}

func removeOperation(snapshot *Snapshot, service, operation string) {
	filtered := snapshot.Operations[:0]
	for _, item := range snapshot.Operations {
		if item.Service != service || item.OperationID != operation {
			filtered = append(filtered, item)
		}
	}
	snapshot.Operations = filtered
}

func removeStatefulGroup(snapshot *Snapshot, group string) {
	filtered := snapshot.StatefulGroups[:0]
	for _, item := range snapshot.StatefulGroups {
		if item.Group != group {
			filtered = append(filtered, item)
		}
	}
	snapshot.StatefulGroups = filtered
}

func removeStatefulMember(snapshot *Snapshot, group string, member int) {
	for i := range snapshot.StatefulGroups {
		if snapshot.StatefulGroups[i].Group != group {
			continue
		}
		filtered := snapshot.StatefulGroups[i].Members[:0]
		for _, item := range snapshot.StatefulGroups[i].Members {
			if item.Member != member {
				filtered = append(filtered, item)
			}
		}
		snapshot.StatefulGroups[i].Members = filtered
		return
	}
}

func removeStatefulBackup(snapshot *Snapshot, group, backup string) {
	for i := range snapshot.StatefulGroups {
		if snapshot.StatefulGroups[i].Group != group {
			continue
		}
		filtered := snapshot.StatefulGroups[i].Backups[:0]
		for _, item := range snapshot.StatefulGroups[i].Backups {
			if item.BackupID != backup {
				filtered = append(filtered, item)
			}
		}
		snapshot.StatefulGroups[i].Backups = filtered
		return
	}
}

func removeResourceByKeyShape(snapshot *Snapshot, key string) {
	// Resource hints do not carry a durable key in the public summary. Keep the
	// old entry until the refreshed record is appended; a full rebuild dedupes.
	_ = snapshot
	_ = key
}

func trimRecentEvents(snapshot *Snapshot, limit int) {
	if limit <= 0 || len(snapshot.RecentEvents) <= limit {
		return
	}
	snapshot.RecentEvents = snapshot.RecentEvents[len(snapshot.RecentEvents)-limit:]
}

func isServiceControlKey(key string) bool {
	_, ok := serviceFromControlKey(key)
	return ok
}

func isSagaControlKey(key string) bool {
	_, ok := sagaFromControlKey(key)
	return ok
}

func isStatefulGroupControlKey(key string) bool {
	_, ok := statefulGroupFromControlKey(key)
	return ok
}

func isStatefulMemberControlKey(key string) bool {
	_, _, ok := statefulMemberFromControlKey(key)
	return ok
}

func isStatefulBackupRecordKey(key string) bool {
	_, _, ok := statefulBackupFromRecordKey(key)
	return ok
}

func isOperationControlKey(key string) bool {
	parts := strings.Split(key, "/")
	return len(parts) == 5 && parts[0] == "services" && parts[2] == "operations" && parts[4] == "control.json" && parts[1] != "" && parts[3] != ""
}

func serviceNameFromKey(key string) string {
	service, _ := serviceFromControlKey(key)
	return service
}

func sagaNameFromKey(key string) string {
	saga, _ := sagaFromControlKey(key)
	return saga
}

func operationNamesFromKey(key string) (string, string) {
	parts := strings.Split(key, "/")
	if len(parts) != 5 {
		return "", ""
	}
	return parts[1], parts[3]
}

func statefulGroupNameFromKey(key string) string {
	group, _ := statefulGroupFromControlKey(key)
	return group
}

func statefulMemberNamesFromKey(key string) (string, int) {
	group, member, _ := statefulMemberFromControlKey(key)
	return group, member
}

func statefulBackupNamesFromKey(key string) (string, string) {
	group, backup, _ := statefulBackupFromRecordKey(key)
	return group, backup
}

func KeyForService(service string) string {
	return serviceKey(service)
}

func KeyForStatefulGroup(group string) string {
	return "stateful/" + group + "/control.json"
}

func KeyForStatefulMember(group string, member int) string {
	return fmt.Sprintf("stateful/%s/members/%d/control.json", group, member)
}

func KeyForStatefulBackup(group, backup string) string {
	return "stateful/" + group + "/backups/" + backup + "/record.json"
}

func KeyForSaga(saga string) string {
	return sagaKey(saga)
}

func KeyForOperation(service, operation string) string {
	return operationKey(service, operation)
}

func HintForKey(key string) Hint {
	return Hint{Key: key}
}

func errUnknownFreshKind(kind string) error {
	return fmt.Errorf("unknown fresh kind %q", kind)
}
