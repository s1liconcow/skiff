package index

import (
	"context"
	"errors"

	"github.com/s1liconcow/skiff/internal/objstore"
)

type FreshRead struct {
	Snapshot  Snapshot
	Freshness Freshness
}

func (i *Index) FreshSnapshot(ctx context.Context) (FreshRead, error) {
	snapshot, err := BuildSnapshot(ctx, i.store, BuildOptions{
		Now:               i.clock(),
		Generation:        i.current.Load().Generation + 1,
		RecentEventsLimit: i.recentEventsLimit,
		ListLimit:         i.listLimit,
	})
	if err != nil {
		return FreshRead{}, err
	}
	return FreshRead{
		Snapshot:  snapshot,
		Freshness: FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"),
	}, nil
}

func (i *Index) FreshService(ctx context.Context, service string) (ServiceSummary, Freshness, error) {
	if service == "" {
		return ServiceSummary{}, Freshness{}, errors.New("service is required")
	}
	current := i.current.Load()
	snapshot := Snapshot{
		Ready:          true,
		Generation:     current.Generation + 1,
		RefreshedAt:    i.clock().UTC(),
		LastFullScanAt: current.LastFullScanAt,
	}
	addServiceControl(ctx, i.store, serviceKey(service), &snapshot)
	if len(snapshot.Services) == 0 {
		return ServiceSummary{}, FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), errors.New("service control not found or malformed")
	}
	return snapshot.Services[0], FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), nil
}

func (i *Index) FreshSaga(ctx context.Context, saga string) (SagaSummary, Freshness, error) {
	if saga == "" {
		return SagaSummary{}, Freshness{}, errors.New("saga is required")
	}
	current := i.current.Load()
	snapshot := Snapshot{
		Ready:          true,
		Generation:     current.Generation + 1,
		RefreshedAt:    i.clock().UTC(),
		LastFullScanAt: current.LastFullScanAt,
	}
	addSagaControl(ctx, i.store, sagaKey(saga), &snapshot)
	if len(snapshot.Sagas) == 0 {
		return SagaSummary{}, FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), errors.New("saga control not found or malformed")
	}
	return snapshot.Sagas[0], FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), nil
}

func (i *Index) FreshStatefulGroup(ctx context.Context, group string) (StatefulGroupSummary, Freshness, error) {
	if group == "" {
		return StatefulGroupSummary{}, Freshness{}, errors.New("stateful group is required")
	}
	current := i.current.Load()
	snapshot := Snapshot{
		Ready:          true,
		Generation:     current.Generation + 1,
		RefreshedAt:    i.clock().UTC(),
		LastFullScanAt: current.LastFullScanAt,
	}
	addStatefulGroupControl(ctx, i.store, KeyForStatefulGroup(group), &snapshot)
	memberPrefix := "stateful/" + group + "/members/"
	if metas, err := i.store.List(ctx, memberPrefix, listOptions(i.listLimit)); err == nil {
		for _, meta := range metas {
			if isStatefulMemberControlKey(meta.Key) {
				addStatefulMemberControl(ctx, i.store, meta.Key, &snapshot)
			}
		}
	}
	backupPrefix := "stateful/" + group + "/backups/"
	if metas, err := i.store.List(ctx, backupPrefix, listOptions(i.listLimit)); err == nil {
		for _, meta := range metas {
			if isStatefulBackupRecordKey(meta.Key) {
				addStatefulBackupRecord(ctx, i.store, meta.Key, &snapshot)
			}
		}
	}
	sortSnapshot(&snapshot)
	if len(snapshot.StatefulGroups) == 0 {
		return StatefulGroupSummary{}, FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), errors.New("stateful group control not found or malformed")
	}
	return snapshot.StatefulGroups[0], FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), nil
}

func (i *Index) FreshStatefulMember(ctx context.Context, group string, member int) (StatefulMemberSummary, Freshness, error) {
	if group == "" {
		return StatefulMemberSummary{}, Freshness{}, errors.New("stateful group is required")
	}
	if member < 0 {
		return StatefulMemberSummary{}, Freshness{}, errors.New("member ordinal must be non-negative")
	}
	current := i.current.Load()
	snapshot := Snapshot{
		Ready:          true,
		Generation:     current.Generation + 1,
		RefreshedAt:    i.clock().UTC(),
		LastFullScanAt: current.LastFullScanAt,
	}
	addStatefulMemberControl(ctx, i.store, KeyForStatefulMember(group, member), &snapshot)
	sortSnapshot(&snapshot)
	for _, item := range snapshot.StatefulGroups {
		if item.Group != group {
			continue
		}
		for _, candidate := range item.Members {
			if candidate.Member == member {
				return candidate, FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), nil
			}
		}
	}
	return StatefulMemberSummary{}, FreshnessFromSnapshot(snapshot, i.clock(), "direct_object_store"), errors.New("stateful member control not found or malformed")
}

func listOptions(limit int32) objstore.ListOptions {
	return objstore.ListOptions{Limit: limit}
}
