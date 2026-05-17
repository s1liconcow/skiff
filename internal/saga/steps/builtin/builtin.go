package builtin

import (
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/saga/steps/check"
	"github.com/s1liconcow/skiff/internal/saga/steps/database"
	"github.com/s1liconcow/skiff/internal/saga/steps/multiregion"
	"github.com/s1liconcow/skiff/internal/saga/steps/secret"
	"github.com/s1liconcow/skiff/internal/saga/steps/service"
	sagatime "github.com/s1liconcow/skiff/internal/saga/steps/time"
)

type Options struct {
	Store    objstore.ObjectStore
	Provider provider.Provider
	Metrics  check.MetricsClient
	Binary   string
}

func New(opts Options) map[string]steps.Step {
	metrics := opts.Metrics
	if metrics == nil {
		metrics = opts.Provider
	}
	items := []steps.Step{
		check.Preflight{Store: opts.Store, Provider: opts.Provider},
		check.ServiceHealthy{Provider: opts.Provider},
		check.TargetHealth{Provider: opts.Provider},
		check.MetricsGate{Client: metrics},
		approval.Manual{Binary: opts.Binary},
		approval.ChangeWindow{Binary: opts.Binary},
		service.Stage{Store: opts.Store, Provider: opts.Provider},
		service.MarkStable{Store: opts.Store, Provider: opts.Provider},
		sagatime.Sleep{},
	}
	if trafficProvider, ok := opts.Provider.(provider.TrafficOperations); ok {
		items = append(items, service.TrafficShift{Shifter: trafficProvider})
	}
	if databaseProvider, ok := opts.Provider.(provider.DatabaseOperations); ok {
		items = append(items, database.New(databaseProvider)...)
	}
	if secretProvider, ok := opts.Provider.(provider.SecretOperations); ok {
		items = append(items, secret.New(secretProvider)...)
	}
	items = append(items, multiregion.New(opts.Provider)...)
	out := make(map[string]steps.Step, len(items))
	for _, item := range items {
		out[item.Kind()] = item
	}
	return out
}
