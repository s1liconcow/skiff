package spec

func ApplyDefaults(doc *Document) {
	if doc == nil {
		return
	}
	if !usesWorkloadDefaults(doc.Kind) {
		return
	}
	if doc.Runtime.ShutdownGrace == "" {
		doc.Runtime.ShutdownGrace = "30s"
	}
	if doc.Runtime.Health.Interval == "" {
		doc.Runtime.Health.Interval = "10s"
	}
	if doc.Runtime.Health.Timeout == "" {
		doc.Runtime.Health.Timeout = "2s"
	}
	if doc.Runtime.Health.Type == "" {
		switch {
		case doc.Runtime.Health.Path != "":
			doc.Runtime.Health.Type = "http"
		case len(doc.Runtime.Health.Command) > 0:
			doc.Runtime.Health.Type = "exec"
		}
	}
	if doc.Runtime.Health.Port == 0 {
		doc.Runtime.Health.Port = doc.Runtime.Port
	}
	if doc.Runtime.Logs.Format == "" {
		doc.Runtime.Logs.Format = "text"
	}
	doc.Runtime.Logs.Enabled = true
	doc.Runtime.Metrics.Enabled = true
	if doc.Runtime.Metrics.Path == "" {
		doc.Runtime.Metrics.Path = "/metrics"
	}
	if doc.Machine.Size == "" {
		doc.Machine.Size = "small"
	}
	if doc.Machine.Arch == "" {
		doc.Machine.Arch = "x86_64"
	}
	if doc.Rollout.Strategy == "" {
		doc.Rollout.Strategy = "rolling"
	}
	if doc.Rollout.BatchSize == 0 {
		doc.Rollout.BatchSize = 1
	}
	if doc.Rollout.HealthGracePeriod == "" {
		doc.Rollout.HealthGracePeriod = "60s"
	}
	if doc.Kind == "" || doc.Kind == KindService || doc.Kind == KindWorker {
		if doc.Scale.Min == 0 {
			doc.Scale.Min = 1
		}
		if doc.Scale.Max == 0 {
			doc.Scale.Max = doc.Scale.Min
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
