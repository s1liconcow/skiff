package spec

func ApplyDefaults(doc *Document) {
	if doc == nil {
		return
	}
	switch doc.Kind {
	case KindManagedDatabase:
		applyManagedDatabaseDefaults(doc.ManagedDatabase)
	case KindStack:
		if doc.Stack != nil {
			for i := range doc.Stack.Services {
				applyWorkloadDefaults(&doc.Stack.Services[i].Runtime, &doc.Stack.Services[i].Machine, &doc.Stack.Services[i].Rollout, &doc.Stack.Services[i].Scale, KindService)
			}
			for i := range doc.Stack.Databases {
				applyManagedDatabaseDefaults(&doc.Stack.Databases[i].ManagedDatabase)
			}
		}
	}
	if usesWorkloadDefaults(doc.Kind) {
		applyWorkloadDefaults(&doc.Runtime, &doc.Machine, &doc.Rollout, &doc.Scale, doc.Kind)
	}
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
