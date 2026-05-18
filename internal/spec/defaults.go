package spec

func ApplyDefaults(doc *Document) {
	if doc == nil {
		return
	}
	switch doc.Kind {
	case KindManagedDatabase:
		applyManagedDatabaseDefaults(doc.ManagedDatabase)
	case KindStatefulGroup:
		applyStatefulGroupDefaults(doc.Metadata.Name, doc.StatefulGroup)
	case KindStack:
		if doc.Stack != nil {
			for i := range doc.Stack.Services {
				applyWorkloadDefaults(&doc.Stack.Services[i].Runtime, &doc.Stack.Services[i].Machine, &doc.Stack.Services[i].Rollout, &doc.Stack.Services[i].Scale, KindService)
			}
			for i := range doc.Stack.Databases {
				applyManagedDatabaseDefaults(&doc.Stack.Databases[i].ManagedDatabase)
			}
			for i := range doc.Stack.ObjectStores {
				applyStackObjectStoreDefaults(&doc.Stack.ObjectStores[i])
			}
		}
	case KindMultiRegionStack:
		applyMultiRegionDefaults(doc.MultiRegion)
	}
	if usesWorkloadDefaults(doc.Kind) {
		applyWorkloadDefaults(&doc.Runtime, &doc.Machine, &doc.Rollout, &doc.Scale, doc.Kind)
	}
}

func applyMultiRegionDefaults(stack *MultiRegionStack) {
	if stack == nil {
		return
	}
	applyWorkloadDefaults(&stack.Service.Runtime, &stack.Service.Machine, &stack.Service.Rollout, &stack.Service.Scale, KindService)
	applyManagedDatabaseDefaults(&stack.Database.ManagedDatabase)
	if stack.Binding.From == "" {
		stack.Binding.From = stack.Service.Name
	}
	if stack.Binding.To == "" {
		stack.Binding.To = stack.Database.Name
	}
	if stack.Binding.As == "" {
		stack.Binding.As = "DATABASE_URL"
	}
	if stack.TrafficPolicy.Mode == "" {
		stack.TrafficPolicy.Mode = "weighted-dns"
	}
	if stack.DatabaseReplication.Mode == "" {
		stack.DatabaseReplication.Mode = "async"
	}
	if stack.DatabaseReplication.MaxReplicaLag == "" {
		stack.DatabaseReplication.MaxReplicaLag = "30s"
	}
	if stack.FailoverPolicy.MaxReplicaLag == "" {
		stack.FailoverPolicy.MaxReplicaLag = stack.DatabaseReplication.MaxReplicaLag
	}
	if stack.FailoverPolicy.Failback == "" {
		stack.FailoverPolicy.Failback = "plan-required"
	}
	stack.FailoverPolicy.RequireApproval = true
}

func applyWorkloadDefaults(runtime *Runtime, machine *Machine, rollout *Rollout, scale *Scale, kind Kind) {
	if runtime.ShutdownGrace == "" {
		runtime.ShutdownGrace = "30s"
	}
	if runtime.Health.Interval == "" {
		runtime.Health.Interval = "10s"
	}
	if runtime.Health.Timeout == "" {
		runtime.Health.Timeout = "2s"
	}
	if runtime.Health.Type == "" {
		switch {
		case runtime.Health.Path != "":
			runtime.Health.Type = "http"
		case len(runtime.Health.Command) > 0:
			runtime.Health.Type = "exec"
		}
	}
	if runtime.Health.Port == 0 {
		runtime.Health.Port = runtime.Port
	}
	if runtime.Logs.Format == "" {
		runtime.Logs.Format = "text"
	}
	runtime.Logs.Enabled = true
	runtime.Metrics.Enabled = true
	if runtime.Metrics.Path == "" {
		runtime.Metrics.Path = "/metrics"
	}
	if machine.Size == "" {
		machine.Size = "small"
	}
	if machine.Arch == "" {
		machine.Arch = "x86_64"
	}
	if rollout.Strategy == "" {
		rollout.Strategy = "rolling"
	}
	if rollout.BatchSize == 0 {
		rollout.BatchSize = 1
	}
	if rollout.HealthGracePeriod == "" {
		rollout.HealthGracePeriod = "60s"
	}
	if kind == "" || kind == KindService || kind == KindWorker {
		if scale.Min == 0 {
			scale.Min = 1
		}
		if scale.Max == 0 {
			scale.Max = scale.Min
		}
	}
}

func usesWorkloadDefaults(kind Kind) bool {
	switch kind {
	case "", KindService, KindWorker, KindJob:
		return true
	default:
		return false
	}
}

func applyStatefulGroupDefaults(name string, group *StatefulGroup) {
	if group == nil {
		return
	}
	if group.Volume.Type == "" {
		group.Volume.Type = "gp3"
	}
	if group.Volume.MountPath == "" {
		group.Volume.MountPath = "/var/lib/skiff/state"
	}
	group.Volume.Encrypted = true
	if group.Identity.HostnamePrefix == "" {
		group.Identity.HostnamePrefix = name
	}
	if group.Update.Strategy == "" {
		group.Update.Strategy = "ordered"
	}
}

func applyManagedDatabaseDefaults(db *ManagedDatabase) {
	if db == nil {
		return
	}
	if db.Size == "" {
		db.Size = "small"
	}
	if db.Storage.SizeGB == 0 {
		db.Storage.SizeGB = 20
	}
	if db.Storage.Type == "" {
		db.Storage.Type = "gp3"
	}
	db.Storage.Encrypted = true
	db.Backups.Enabled = true
	if db.Backups.RetentionDays == 0 {
		db.Backups.RetentionDays = 7
	}
	if db.Network.SubnetGroupRef == "" {
		db.Network.Private = true
	}
}

func applyStackObjectStoreDefaults(store *StackObjectStore) {
	if store == nil {
		return
	}
	if store.Access == "" {
		store.Access = "read-write"
	}
	store.Versioned = true
	store.Encrypted = true
}
