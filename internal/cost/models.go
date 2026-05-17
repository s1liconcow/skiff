package cost

type ServiceShape struct {
	Service        string `json:"service"`
	Env            string `json:"env"`
	MachineSize    string `json:"machine_size"`
	MachineArch    string `json:"machine_arch,omitempty"`
	MinReplicas    int    `json:"min_replicas"`
	MaxReplicas    int    `json:"max_replicas"`
	LogsEnabled    bool   `json:"logs_enabled"`
	MetricsEnabled bool   `json:"metrics_enabled"`
}

type ObservedSignals struct {
	CPUP95Percent       *float64 `json:"cpu_p95_percent,omitempty"`
	MemoryP95Percent    *float64 `json:"memory_p95_percent,omitempty"`
	RequestCount        *float64 `json:"request_count,omitempty"`
	RequestRateRPS      *float64 `json:"request_rate_rps,omitempty"`
	UnhealthyTargets    *int     `json:"unhealthy_targets,omitempty"`
	WarmCapacity        *int     `json:"warm_capacity,omitempty"`
	LogMegabytesPerHour *float64 `json:"log_megabytes_per_hour,omitempty"`
	Window              string   `json:"window,omitempty"`
}

type Input struct {
	Shape   ServiceShape    `json:"shape"`
	Signals ObservedSignals `json:"signals,omitempty"`
}

type ShapeInfo struct {
	Name         string  `json:"name"`
	VCPU         int     `json:"vcpu"`
	MemoryGB     float64 `json:"memory_gb"`
	RelativeCost int     `json:"relative_cost"`
}

type ScaleInfo struct {
	MinReplicas int `json:"min_replicas"`
	MaxReplicas int `json:"max_replicas"`
}

type Result struct {
	Service         string           `json:"service"`
	Env             string           `json:"env"`
	Shape           ShapeInfo        `json:"shape"`
	Scale           ScaleInfo        `json:"scale"`
	Observations    []Evidence       `json:"observations,omitempty"`
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	Limitations     []string         `json:"limitations,omitempty"`
}

type Recommendation struct {
	ID              string     `json:"id"`
	Category        string     `json:"category"`
	Severity        string     `json:"severity"`
	Summary         string     `json:"summary"`
	Confidence      string     `json:"confidence"`
	EstimatedImpact string     `json:"estimated_impact,omitempty"`
	Evidence        []Evidence `json:"evidence,omitempty"`
}

type Evidence struct {
	Metric  string `json:"metric"`
	Value   string `json:"value"`
	Unit    string `json:"unit,omitempty"`
	Summary string `json:"summary,omitempty"`
}
