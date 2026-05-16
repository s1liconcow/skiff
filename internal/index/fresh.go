package index

import (
	"context"
	"errors"
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
