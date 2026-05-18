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
	Pricing         *PricingEstimate `json:"pricing,omitempty"`
	PricingSetup    *PricingSetup    `json:"pricing_setup,omitempty"`
	Infrastructure  *InfraEstimate   `json:"infrastructure,omitempty"`
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

type PricingSetup struct {
	Status             string               `json:"status"`
	Summary            string               `json:"summary"`
	Provider           string               `json:"provider,omitempty"`
	Region             string               `json:"region,omitempty"`
	ConfigPath         string               `json:"config_path"`
	UpdateCommand      string               `json:"update_command"`
	AutoDetectNextRun  bool                 `json:"auto_detect_next_run"`
	NextRunSummary     string               `json:"next_run_summary,omitempty"`
	RecommendedActions []PricingSetupAction `json:"recommended_actions,omitempty"`
}

type PricingSetupAction struct {
	ID            string `json:"id"`
	Command       string `json:"command"`
	Mutating      bool   `json:"mutating"`
	Safety        string `json:"safety,omitempty"`
	Reversibility string `json:"reversibility,omitempty"`
	Summary       string `json:"summary,omitempty"`
}

const (
	PricingCatalogSchemaVersion = "skiff.pricing/v1alpha1"
	DefaultPricingConfigPath    = ".skiff-pricing.json"
	DefaultMonthlyHours         = 730.0

	PricingSchemeOnDemand                = "on_demand"
	PricingSchemeRI1yrStandardNoUpfront  = "ri_1yr_standard_no_upfront"
	PricingSchemeRI3yrStandardNoUpfront  = "ri_3yr_standard_no_upfront"
	PricingSchemeRI3yrStandardAllUpfront = "ri_3yr_standard_all_upfront"
	PricingOfferingClassStandard         = "standard"
	PricingPurchaseOptionNoUpfront       = "No Upfront"
	PricingPurchaseOptionAllUpfront      = "All Upfront"
	PricingLeaseContractLength1yr        = "1yr"
	PricingLeaseContractLength3yr        = "3yr"

	StorageKindEBSVolumeGBMonth   = "ebs_volume_gb_month"
	StorageKindEBSSnapshotGBMonth = "ebs_snapshot_gb_month"
)

type PricingOptions struct {
	MonthlyHours float64
}

type PricingCatalog struct {
	SchemaVersion   string            `json:"schema_version,omitempty"`
	Provider        string            `json:"provider"`
	Region          string            `json:"region"`
	Currency        string            `json:"currency"`
	Source          string            `json:"source,omitempty"`
	PublicationDate string            `json:"publication_date,omitempty"`
	Version         string            `json:"version,omitempty"`
	Items           []InstancePricing `json:"items"`
	StorageRates    []StoragePricing  `json:"storage_rates,omitempty"`
}

type InstancePricing struct {
	MachineSize  string        `json:"machine_size,omitempty"`
	InstanceType string        `json:"instance_type"`
	VCPU         int           `json:"vcpu"`
	MemoryGB     float64       `json:"memory_gb"`
	Rates        []PricingRate `json:"rates"`
}

type PricingRate struct {
	Scheme             string  `json:"scheme"`
	Summary            string  `json:"summary"`
	Currency           string  `json:"currency"`
	HourlyUSD          float64 `json:"hourly_usd"`
	UpfrontUSD         float64 `json:"upfront_usd,omitempty"`
	EffectiveHourlyUSD float64 `json:"effective_hourly_usd"`
	TermHours          int     `json:"term_hours,omitempty"`
}

type StoragePricing struct {
	Kind         string  `json:"kind"`
	ResourceType string  `json:"resource_type,omitempty"`
	Unit         string  `json:"unit"`
	UnitPriceUSD float64 `json:"unit_price_usd"`
	Summary      string  `json:"summary,omitempty"`
}

type PricingScheme struct {
	ID                  string
	Summary             string
	LeaseContractLength string
	OfferingClass       string
	PurchaseOption      string
}

type PricingEstimate struct {
	Provider        string                  `json:"provider"`
	Region          string                  `json:"region"`
	Currency        string                  `json:"currency"`
	Source          string                  `json:"source,omitempty"`
	PublicationDate string                  `json:"publication_date,omitempty"`
	Version         string                  `json:"version,omitempty"`
	MachineSize     string                  `json:"machine_size"`
	InstanceType    string                  `json:"instance_type"`
	VCPU            int                     `json:"vcpu"`
	MemoryGB        float64                 `json:"memory_gb"`
	MinReplicas     int                     `json:"min_replicas"`
	MaxReplicas     int                     `json:"max_replicas"`
	MonthlyHours    float64                 `json:"monthly_hours"`
	Schemes         []PricingSchemeEstimate `json:"schemes"`
}

type PricingSchemeEstimate struct {
	Scheme             string  `json:"scheme"`
	Summary            string  `json:"summary"`
	HourlyUSD          float64 `json:"hourly_usd"`
	UpfrontUSD         float64 `json:"upfront_usd,omitempty"`
	EffectiveHourlyUSD float64 `json:"effective_hourly_usd"`
	TermHours          int     `json:"term_hours,omitempty"`
	MinHourlyUSD       float64 `json:"min_hourly_usd"`
	MaxHourlyUSD       float64 `json:"max_hourly_usd"`
	MinMonthlyUSD      float64 `json:"min_monthly_usd"`
	MaxMonthlyUSD      float64 `json:"max_monthly_usd"`
	MinAnnualUSD       float64 `json:"min_annual_usd"`
	MaxAnnualUSD       float64 `json:"max_annual_usd"`
	MinUpfrontUSD      float64 `json:"min_upfront_usd,omitempty"`
	MaxUpfrontUSD      float64 `json:"max_upfront_usd,omitempty"`
	MinTermUSD         float64 `json:"min_term_usd,omitempty"`
	MaxTermUSD         float64 `json:"max_term_usd,omitempty"`
}

type InfraEstimate struct {
	Provider        string          `json:"provider"`
	Region          string          `json:"region"`
	Currency        string          `json:"currency"`
	Source          string          `json:"source,omitempty"`
	PublicationDate string          `json:"publication_date,omitempty"`
	Version         string          `json:"version,omitempty"`
	MonthlyHours    float64         `json:"monthly_hours"`
	LineItems       []InfraLineItem `json:"line_items"`
	Totals          []InfraTotal    `json:"totals,omitempty"`
	Scenarios       []UsageScenario `json:"scenarios,omitempty"`
	Notes           []string        `json:"notes,omitempty"`
}

type InfraLineItem struct {
	ID              string  `json:"id"`
	Category        string  `json:"category"`
	Kind            string  `json:"kind"`
	Name            string  `json:"name,omitempty"`
	PricingScheme   string  `json:"pricing_scheme,omitempty"`
	Quantity        float64 `json:"quantity,omitempty"`
	Unit            string  `json:"unit,omitempty"`
	UnitPriceUSD    float64 `json:"unit_price_usd,omitempty"`
	MonthlyUSD      float64 `json:"monthly_usd,omitempty"`
	AnnualUSD       float64 `json:"annual_usd,omitempty"`
	TermUSD         float64 `json:"term_usd,omitempty"`
	Estimated       bool    `json:"estimated"`
	Summary         string  `json:"summary"`
	IncludedInTotal bool    `json:"included_in_total"`
}

type InfraTotal struct {
	PricingScheme string  `json:"pricing_scheme"`
	MonthlyUSD    float64 `json:"monthly_usd"`
	AnnualUSD     float64 `json:"annual_usd"`
	TermUSD       float64 `json:"term_usd,omitempty"`
}

type UsageScenario struct {
	Name                string       `json:"name"`
	Summary             string       `json:"summary"`
	AssumedReplicas     int          `json:"assumed_replicas,omitempty"`
	SnapshotDataGB      float64      `json:"snapshot_data_gb,omitempty"`
	SnapshotDataPercent float64      `json:"snapshot_data_percent,omitempty"`
	Totals              []InfraTotal `json:"totals,omitempty"`
	Assumptions         []string     `json:"assumptions,omitempty"`
}
