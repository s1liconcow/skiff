package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/compiler"
	skiffcost "github.com/s1liconcow/skiff/internal/cost"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

type costExplainOutput struct {
	OK      bool             `json:"ok"`
	TraceID string           `json:"trace_id,omitempty"`
	Result  skiffcost.Result `json:"result"`
}

type costPricingUpdateOutput struct {
	OK              bool     `json:"ok"`
	TraceID         string   `json:"trace_id,omitempty"`
	Path            string   `json:"path"`
	Provider        string   `json:"provider"`
	Region          string   `json:"region"`
	Source          string   `json:"source,omitempty"`
	PublicationDate string   `json:"publication_date,omitempty"`
	Version         string   `json:"version,omitempty"`
	Items           int      `json:"items"`
	DatabaseItems   int      `json:"database_items,omitempty"`
	StorageRates    int      `json:"storage_rates"`
	Schemes         []string `json:"schemes,omitempty"`
}

type optionalFloatFlag struct {
	value float64
	set   bool
}

func (f *optionalFloatFlag) Set(value string) error {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	f.value = parsed
	f.set = true
	return nil
}

func (f *optionalFloatFlag) String() string {
	if !f.set {
		return ""
	}
	return strconv.FormatFloat(f.value, 'f', -1, 64)
}

func (f *optionalFloatFlag) Value() *float64 {
	if !f.set {
		return nil
	}
	value := f.value
	return &value
}

type optionalIntFlag struct {
	value int
	set   bool
}

func (f *optionalIntFlag) Set(value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	f.value = parsed
	f.set = true
	return nil
}

func (f *optionalIntFlag) String() string {
	if !f.set {
		return ""
	}
	return strconv.Itoa(f.value)
}

func (f *optionalIntFlag) Value() *int {
	if !f.set {
		return nil
	}
	value := f.value
	return &value
}

type pricingSchemeFlags struct {
	values []skiffcost.PricingScheme
}

func (f *pricingSchemeFlags) Set(value string) error {
	scheme, err := skiffcost.ParsePricingScheme(value)
	if err != nil {
		return err
	}
	f.values = append(f.values, scheme)
	return nil
}

func (f *pricingSchemeFlags) String() string {
	if f == nil || len(f.values) == 0 {
		return ""
	}
	return strings.Join(skiffcost.PricingSchemeIDs(f.values), ",")
}

func (f *pricingSchemeFlags) ValuesOrDefault() []skiffcost.PricingScheme {
	if f == nil || len(f.values) == 0 {
		return skiffcost.DefaultPricingSchemes()
	}
	return append([]skiffcost.PricingScheme(nil), f.values...)
}

func (f *pricingSchemeFlags) Values() []skiffcost.PricingScheme {
	if f == nil || len(f.values) == 0 {
		return nil
	}
	return append([]skiffcost.PricingScheme(nil), f.values...)
}

func (f *pricingSchemeFlags) SetExplicitly() bool {
	return f != nil && len(f.values) > 0
}

func runCost(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCostUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "explain":
		return runCostExplain(binary, args[1:], root, stdout, stderr)
	case "pricing":
		return runCostPricing(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printCostUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "cost", root.Format, root.TraceID, fmt.Errorf("unknown cost command %q", args[0]), stdout, stderr)
	}
}

func runCostExplain(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" cost explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "Skiff YAML or JSON service spec file")
	provider := fs.String("provider", root.Provider, "cloud provider for pricing")
	region := fs.String("region", root.Region, "cloud region for pricing")
	awsPricing := fs.Bool("aws-pricing", false, "fetch fresh AWS EC2/RDS Price List data and include compute estimates")
	awsPricingFile := fs.String("aws-pricing-file", "", "read AWS EC2 Price List JSON from this file instead of fetching fresh data")
	awsRDSPricingFile := fs.String("aws-rds-pricing-file", "", "read AWS RDS Price List JSON from this file instead of fetching fresh data")
	pricingConfig := fs.String("pricing-config", skiffcost.DefaultPricingConfigPath, "local pricing catalog config file")
	monthlyHours := fs.Float64("monthly-hours", skiffcost.DefaultMonthlyHours, "hours per month for monthly cost estimates")
	window := fs.String("window", "", "observation window for supplied metrics")
	var cpuP95 optionalFloatFlag
	var memoryP95 optionalFloatFlag
	var requestCount optionalFloatFlag
	var requestRPS optionalFloatFlag
	var unhealthyTargets optionalIntFlag
	var warmCapacity optionalIntFlag
	var logMBPerHour optionalFloatFlag
	var pricingSchemes pricingSchemeFlags
	fs.Var(&cpuP95, "cpu-p95", "observed CPU p95 percent")
	fs.Var(&memoryP95, "memory-p95", "observed memory p95 percent")
	fs.Var(&requestCount, "request-count", "observed request count over --window")
	fs.Var(&requestRPS, "request-rps", "observed request rate in requests per second")
	fs.Var(&unhealthyTargets, "unhealthy-targets", "observed unhealthy load balancer target count")
	fs.Var(&warmCapacity, "warm-capacity", "operator target for always-warm replica count")
	fs.Var(&logMBPerHour, "log-mb-per-hour", "observed log volume in MB per hour")
	fs.Var(&pricingSchemes, "pricing-scheme", "pricing scheme to include; repeatable. Supported: on-demand, ri-1yr-standard-no-upfront, ri-3yr-standard-no-upfront, ri-3yr-standard-all-upfront")

	flagArgs, positionals, err := splitCostExplainArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	_ = noColor
	_ = yes

	service := ""
	if len(positionals) == 1 {
		service = positionals[0]
	}
	if *filePath == "" {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, errors.New("--file is required until provider cost metrics integration is available"), stdout, stderr)
	}
	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	if service != "" && service != doc.Metadata.Name {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, fmt.Errorf("service %q does not match spec metadata.name %q", service, doc.Metadata.Name), stdout, stderr)
	}
	resolvedProvider := firstNonEmptyCLI(*provider, doc.Metadata.Labels["provider"], aws.Name)
	resolvedRegion := firstNonEmptyCLI(*region, doc.Metadata.Labels["region"])
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	input := skiffcost.InputFromGraph(graph)
	signals := skiffcost.ObservedSignals{
		CPUP95Percent:       cpuP95.Value(),
		MemoryP95Percent:    memoryP95.Value(),
		RequestCount:        requestCount.Value(),
		RequestRateRPS:      requestRPS.Value(),
		UnhealthyTargets:    unhealthyTargets.Value(),
		WarmCapacity:        warmCapacity.Value(),
		LogMegabytesPerHour: logMBPerHour.Value(),
		Window:              strings.TrimSpace(*window),
	}
	if err := validateCostExplainSignals(signals); err != nil {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, err, stdout, stderr)
	}
	input.Signals = signals
	result := skiffcost.Analyze(input)
	pricingLoad, err := loadCostPricing(input, graph, resolvedProvider, resolvedRegion, *pricingConfig, *awsPricingFile, *awsRDSPricingFile, pricingSchemes.Values(), *awsPricing)
	if err != nil {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, err, stdout, stderr)
	}
	if pricingLoad.Loaded {
		result, err = skiffcost.AnalyzeWithPricing(input, pricingLoad.Catalog, skiffcost.PricingOptions{MonthlyHours: *monthlyHours})
		if err != nil {
			return writeClientCommandError(binary, "cost explain", *format, *traceID, err, stdout, stderr)
		}
		infra := skiffcost.EstimateInfrastructure(graph, pricingLoad.Catalog, skiffcost.PricingOptions{MonthlyHours: *monthlyHours})
		result.Infrastructure = &infra
		if pricingLoad.ConfigPath != "" && skiffcost.MissingManagedDatabasePricing(graph, pricingLoad.Catalog) {
			result.PricingSetup = incompletePricingSetup(binary, resolvedProvider, firstNonEmptyCLI(resolvedRegion, pricingLoad.Catalog.Region), pricingLoad.ConfigPath)
		}
	} else if pricingLoad.MissingConfig {
		result.PricingSetup = missingPricingSetup(binary, resolvedProvider, resolvedRegion, pricingLoad.ConfigPath)
	}
	switch *format {
	case "human", "text":
		printCostExplain(stdout, result)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(costExplainOutput{OK: true, TraceID: *traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s cost explain: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	case "json-pretty":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(costExplainOutput{OK: true, TraceID: *traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s cost explain: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "cost explain", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitCostExplainArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"cpu-p95":              true,
		"aws-rds-pricing-file": true,
		"aws-pricing-file":     true,
		"file":                 true,
		"format":               true,
		"log-mb-per-hour":      true,
		"memory-p95":           true,
		"monthly-hours":        true,
		"pricing-config":       true,
		"pricing-scheme":       true,
		"provider":             true,
		"region":               true,
		"request-count":        true,
		"request-rps":          true,
		"trace-id":             true,
		"unhealthy-targets":    true,
		"warm-capacity":        true,
		"window":               true,
	}
	return splitArgs(args, valueFlags)
}

func validateCostExplainSignals(signals skiffcost.ObservedSignals) error {
	if err := validatePercent("cpu-p95", signals.CPUP95Percent); err != nil {
		return err
	}
	if err := validatePercent("memory-p95", signals.MemoryP95Percent); err != nil {
		return err
	}
	for name, value := range map[string]*float64{
		"request-count":   signals.RequestCount,
		"request-rps":     signals.RequestRateRPS,
		"log-mb-per-hour": signals.LogMegabytesPerHour,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("--%s must be non-negative", name)
		}
	}
	for name, value := range map[string]*int{
		"unhealthy-targets": signals.UnhealthyTargets,
		"warm-capacity":     signals.WarmCapacity,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("--%s must be non-negative", name)
		}
	}
	return nil
}

type costPricingLoad struct {
	Catalog       skiffcost.PricingCatalog
	Loaded        bool
	MissingConfig bool
	ConfigPath    string
}

func loadCostPricing(input skiffcost.Input, graph *ir.Graph, provider, region, pricingConfigPath, awsPricingFile, awsRDSPricingFile string, schemes []skiffcost.PricingScheme, liveFetch bool) (costPricingLoad, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = aws.Name
	}
	if provider != aws.Name {
		return costPricingLoad{}, fmt.Errorf("pricing provider %q is not supported; expected aws", provider)
	}
	if strings.TrimSpace(awsPricingFile) != "" || strings.TrimSpace(awsRDSPricingFile) != "" || liveFetch {
		if strings.TrimSpace(region) == "" {
			return costPricingLoad{}, errors.New("--region is required when AWS pricing is requested")
		}
		var catalogs []skiffcost.PricingCatalog
		if strings.TrimSpace(awsPricingFile) != "" || liveFetch {
			catalog, err := aws.LoadEC2Pricing(nilContext(), aws.EC2PricingOptions{
				Region:     region,
				SourcePath: strings.TrimSpace(awsPricingFile),
				Machines:   costPricingMachines(input.Shape),
				Schemes:    schemesOrDefault(schemes),
			})
			if err != nil {
				return costPricingLoad{}, err
			}
			catalogs = append(catalogs, catalog)
		}
		if strings.TrimSpace(awsRDSPricingFile) != "" || liveFetch {
			catalog, err := aws.LoadRDSPricing(nilContext(), aws.RDSPricingOptions{
				Region:     region,
				SourcePath: strings.TrimSpace(awsRDSPricingFile),
				Databases:  costPricingDatabases(graph),
				Schemes:    schemesOrDefault(schemes),
			})
			if err != nil {
				return costPricingLoad{}, err
			}
			catalogs = append(catalogs, catalog)
		}
		return costPricingLoad{Catalog: skiffcost.MergePricingCatalogs(catalogs...), Loaded: true}, nil
	}
	pricingConfigPath = strings.TrimSpace(pricingConfigPath)
	if pricingConfigPath == "" {
		pricingConfigPath = skiffcost.DefaultPricingConfigPath
	}
	catalog, err := skiffcost.LoadPricingCatalogFile(pricingConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return costPricingLoad{MissingConfig: true, ConfigPath: pricingConfigPath}, nil
		}
		return costPricingLoad{}, err
	}
	if catalog.Provider != "" && catalog.Provider != provider {
		return costPricingLoad{}, fmt.Errorf("pricing config provider %q does not match requested provider %q", catalog.Provider, provider)
	}
	if strings.TrimSpace(region) != "" && strings.TrimSpace(catalog.Region) != "" && catalog.Region != region {
		return costPricingLoad{}, fmt.Errorf("pricing config region %q does not match requested/spec region %q; run cost pricing update for %s or pass a matching --pricing-config", catalog.Region, region, region)
	}
	return costPricingLoad{Catalog: skiffcost.FilterPricingCatalogSchemes(catalog, schemes), Loaded: true, ConfigPath: pricingConfigPath}, nil
}

func missingPricingSetup(binary, provider, region, pricingConfigPath string) *skiffcost.PricingSetup {
	if strings.TrimSpace(pricingConfigPath) == "" {
		pricingConfigPath = skiffcost.DefaultPricingConfigPath
	}
	regionArg := strings.TrimSpace(region)
	if regionArg == "" {
		regionArg = "<aws-region>"
	}
	command := fmt.Sprintf("%s cost pricing update --region %s --out %s", shellArg(binary), shellArg(regionArg), shellArg(pricingConfigPath))
	autoDetect := pricingConfigPath == skiffcost.DefaultPricingConfigPath
	next := "rerun cost explain; Skiff will automatically load .skiff-pricing.json from the current directory"
	if !autoDetect {
		next = fmt.Sprintf("rerun cost explain with --pricing-config %s", shellArg(pricingConfigPath))
	}
	summary := fmt.Sprintf("pricing config %s was not found", pricingConfigPath)
	if strings.TrimSpace(region) == "" {
		summary += "; pass a region to refresh public AWS pricing"
	}
	return &skiffcost.PricingSetup{
		Status:            "missing_config",
		Summary:           summary,
		Provider:          provider,
		Region:            strings.TrimSpace(region),
		ConfigPath:        pricingConfigPath,
		UpdateCommand:     command,
		AutoDetectNextRun: autoDetect,
		NextRunSummary:    next,
		RecommendedActions: []skiffcost.PricingSetupAction{{
			ID:            "generate_pricing_config",
			Command:       command,
			Mutating:      true,
			Safety:        "local_file_only",
			Reversibility: "reversible",
			Summary:       "write a local pricing catalog from public AWS EC2 and RDS pricing data",
		}},
	}
}

func incompletePricingSetup(binary, provider, region, pricingConfigPath string) *skiffcost.PricingSetup {
	if strings.TrimSpace(pricingConfigPath) == "" {
		pricingConfigPath = skiffcost.DefaultPricingConfigPath
	}
	regionArg := strings.TrimSpace(region)
	if regionArg == "" {
		regionArg = "<aws-region>"
	}
	command := fmt.Sprintf("%s cost pricing update --region %s --out %s", shellArg(binary), shellArg(regionArg), shellArg(pricingConfigPath))
	autoDetect := pricingConfigPath == skiffcost.DefaultPricingConfigPath
	next := "rerun cost explain; Skiff will automatically load the refreshed .skiff-pricing.json from the current directory"
	if !autoDetect {
		next = fmt.Sprintf("rerun cost explain with --pricing-config %s", shellArg(pricingConfigPath))
	}
	return &skiffcost.PricingSetup{
		Status:            "incomplete_config",
		Summary:           fmt.Sprintf("pricing config %s does not include matching RDS rates for this stack", pricingConfigPath),
		Provider:          provider,
		Region:            strings.TrimSpace(region),
		ConfigPath:        pricingConfigPath,
		UpdateCommand:     command,
		AutoDetectNextRun: autoDetect,
		NextRunSummary:    next,
		RecommendedActions: []skiffcost.PricingSetupAction{{
			ID:            "refresh_pricing_config",
			Command:       command,
			Mutating:      true,
			Safety:        "local_file_only",
			Reversibility: "reversible",
			Summary:       "refresh the local pricing catalog so it includes public AWS RDS rates",
		}},
	}
}

func schemesOrDefault(schemes []skiffcost.PricingScheme) []skiffcost.PricingScheme {
	if len(schemes) == 0 {
		return skiffcost.DefaultPricingSchemes()
	}
	return schemes
}

func costPricingMachines(shape skiffcost.ServiceShape) []ir.Machine {
	machines := make([]ir.Machine, 0, len(skiffcost.KnownShapes())+1)
	seen := map[string]struct{}{}
	for _, known := range skiffcost.KnownShapes() {
		machines = append(machines, ir.Machine{Size: known.Name})
		seen[known.Name] = struct{}{}
	}
	size := strings.TrimSpace(shape.MachineSize)
	if size == "" {
		size = "small"
	}
	if _, ok := seen[size]; !ok {
		machines = append(machines, ir.Machine{Size: size, Arch: shape.MachineArch})
	}
	return machines
}

func costPricingDatabases(graph *ir.Graph) []aws.RDSDatabaseTarget {
	targets := aws.DefaultCostDatabases()
	if graph == nil {
		return targets
	}
	seen := map[string]struct{}{}
	for _, target := range targets {
		seen[target.Engine+"|"+target.Size+"|"+target.InstanceClass] = struct{}{}
	}
	for _, db := range graph.Resources.ManagedDatabases {
		size := strings.TrimSpace(db.Size)
		if size == "" {
			size = "small"
		}
		instanceClass := aws.DatabaseInstanceClassForSize(size)
		target := aws.RDSDatabaseTarget{Engine: db.Engine, Size: size, InstanceClass: instanceClass, DeploymentOption: "Single-AZ"}
		key := target.Engine + "|" + target.Size + "|" + target.InstanceClass
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	return targets
}

func shellArg(value string) string {
	if value == "" {
		return "''"
	}
	if strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+-./:=@<>") == "" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runCostPricing(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCostPricingUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "update":
		return runCostPricingUpdate(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printCostPricingUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "cost pricing", root.Format, root.TraceID, fmt.Errorf("unknown cost pricing command %q", args[0]), stdout, stderr)
	}
}

func runCostPricingUpdate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" cost pricing update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	provider := fs.String("provider", firstNonEmptyCLI(root.Provider, aws.Name), "cloud provider")
	region := fs.String("region", root.Region, "AWS region to refresh")
	outPath := fs.String("out", skiffcost.DefaultPricingConfigPath, "pricing config file to write")
	awsPricingFile := fs.String("aws-pricing-file", "", "read AWS EC2 Price List JSON from this file instead of fetching fresh data")
	awsRDSPricingFile := fs.String("aws-rds-pricing-file", "", "read AWS RDS Price List JSON from this file instead of fetching fresh data")
	var pricingSchemes pricingSchemeFlags
	fs.Var(&pricingSchemes, "pricing-scheme", "pricing scheme to include; repeatable")
	valueFlags := map[string]bool{
		"aws-rds-pricing-file": true,
		"aws-pricing-file":     true,
		"format":               true,
		"out":                  true,
		"pricing-scheme":       true,
		"provider":             true,
		"region":               true,
		"trace-id":             true,
	}
	flagArgs, positionals, err := splitArgs(args, valueFlags)
	if err != nil {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 0 {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[0]), stdout, stderr)
	}
	_ = noColor
	if strings.TrimSpace(*provider) != aws.Name {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, fmt.Errorf("pricing provider %q is not supported; expected aws", *provider), stdout, stderr)
	}
	if strings.TrimSpace(*region) == "" {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, errors.New("--region is required"), stdout, stderr)
	}
	ec2Catalog, err := aws.LoadEC2Pricing(nilContext(), aws.EC2PricingOptions{
		Region:     strings.TrimSpace(*region),
		SourcePath: strings.TrimSpace(*awsPricingFile),
		Machines:   aws.DefaultCostMachines(),
		Schemes:    pricingSchemes.ValuesOrDefault(),
	})
	if err != nil {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, err, stdout, stderr)
	}
	var catalogs []skiffcost.PricingCatalog
	catalogs = append(catalogs, ec2Catalog)
	if strings.TrimSpace(*awsRDSPricingFile) != "" || strings.TrimSpace(*awsPricingFile) == "" {
		rdsCatalog, err := aws.LoadRDSPricing(nilContext(), aws.RDSPricingOptions{
			Region:     strings.TrimSpace(*region),
			SourcePath: strings.TrimSpace(*awsRDSPricingFile),
			Databases:  aws.DefaultCostDatabases(),
			Schemes:    pricingSchemes.ValuesOrDefault(),
		})
		if err != nil {
			return writeClientCommandError(binary, "cost pricing update", *format, *traceID, err, stdout, stderr)
		}
		catalogs = append(catalogs, rdsCatalog)
	}
	catalog := skiffcost.MergePricingCatalogs(catalogs...)
	if err := skiffcost.WritePricingCatalogFile(*outPath, catalog); err != nil {
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, err, stdout, stderr)
	}
	output := costPricingUpdateOutput{
		OK:              true,
		TraceID:         *traceID,
		Path:            *outPath,
		Provider:        catalog.Provider,
		Region:          catalog.Region,
		Source:          catalog.Source,
		PublicationDate: catalog.PublicationDate,
		Version:         catalog.Version,
		Items:           len(catalog.Items),
		DatabaseItems:   len(catalog.DatabaseItems),
		StorageRates:    len(catalog.StorageRates),
		Schemes:         skiffcost.PricingSchemeIDs(pricingSchemes.ValuesOrDefault()),
	}
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "wrote pricing config %s\n", *outPath)
		fmt.Fprintf(stdout, "provider: %s\nregion: %s\npublished: %s\nitems: %d instance shapes, %d database classes, %d storage rates\n", catalog.Provider, catalog.Region, catalog.PublicationDate, len(catalog.Items), len(catalog.DatabaseItems), len(catalog.StorageRates))
		fmt.Fprintln(stdout, "note: public AWS rates were written; edit this file or pass another --pricing-config for negotiated/private rates")
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(output); err != nil {
			fmt.Fprintf(stderr, "%s cost pricing update: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	case "json-pretty":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			fmt.Fprintf(stderr, "%s cost pricing update: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "cost pricing update", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func validatePercent(name string, value *float64) error {
	if value == nil {
		return nil
	}
	if *value < 0 || *value > 100 {
		return fmt.Errorf("--%s must be between 0 and 100", name)
	}
	return nil
}

func printCostExplain(w io.Writer, result skiffcost.Result) {
	fmt.Fprintf(w, "cost advisor for %s/%s\n", result.Service, result.Env)
	fmt.Fprintf(w, "shape: %s", result.Shape.Name)
	if result.Shape.VCPU > 0 || result.Shape.MemoryGB > 0 {
		fmt.Fprintf(w, " (%d vCPU, %.1f GiB)", result.Shape.VCPU, result.Shape.MemoryGB)
	}
	fmt.Fprintf(w, "\nreplicas: min %d, max %d\n", result.Scale.MinReplicas, result.Scale.MaxReplicas)
	if result.Pricing != nil {
		printPricingEstimate(w, *result.Pricing)
	}
	if result.PricingSetup != nil {
		printPricingSetup(w, *result.PricingSetup)
	}
	if result.Infrastructure != nil {
		printInfrastructureEstimate(w, *result.Infrastructure)
	}
	if len(result.Observations) > 0 {
		fmt.Fprintln(w, "evidence:")
		for _, item := range result.Observations {
			unit := ""
			if item.Unit != "" {
				unit = " " + item.Unit
			}
			fmt.Fprintf(w, "- %s: %s%s", item.Metric, item.Value, unit)
			if item.Summary != "" {
				fmt.Fprintf(w, " (%s)", item.Summary)
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w, "recommendations:")
	for _, rec := range result.Recommendations {
		fmt.Fprintf(w, "- %s [%s confidence]: %s\n", rec.ID, rec.Confidence, rec.Summary)
		if rec.EstimatedImpact != "" {
			fmt.Fprintf(w, "  impact: %s\n", rec.EstimatedImpact)
		}
	}
	if len(result.Limitations) > 0 {
		fmt.Fprintln(w, "limitations:")
		for _, limitation := range result.Limitations {
			fmt.Fprintf(w, "- %s\n", limitation)
		}
	}
}

func printCostUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s cost <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  explain  Explain service shape, replica, warm-capacity, log-volume, and infrastructure cost recommendations")
	fmt.Fprintln(w, "  pricing  Manage local pricing catalogs")
}

func printCostPricingUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s cost pricing <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  update  Refresh a local pricing config file from public AWS EC2 and RDS Price List data")
}

func printPricingEstimate(w io.Writer, pricing skiffcost.PricingEstimate) {
	fmt.Fprintf(w, "pricing: %s %s %s", pricing.Provider, pricing.Region, pricing.InstanceType)
	if pricing.PublicationDate != "" {
		fmt.Fprintf(w, " published %s", pricing.PublicationDate)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "compute estimate: %.0f hours/month, min %d replicas, max %d replicas\n", pricing.MonthlyHours, pricing.MinReplicas, pricing.MaxReplicas)
	for _, scheme := range pricing.Schemes {
		fmt.Fprintf(w, "- %s: %s/effective instance-hour", scheme.Scheme, humanUSD(scheme.EffectiveHourlyUSD))
		if scheme.UpfrontUSD > 0 {
			fmt.Fprintf(w, " (%s upfront per instance)", humanUSD(scheme.UpfrontUSD))
		}
		fmt.Fprintf(w, "; min %s/month, max %s/month", humanUSD(scheme.MinMonthlyUSD), humanUSD(scheme.MaxMonthlyUSD))
		if scheme.TermHours > 0 {
			fmt.Fprintf(w, "; min term %s", humanUSD(scheme.MinTermUSD))
		}
		fmt.Fprintln(w)
	}
}

func printPricingSetup(w io.Writer, setup skiffcost.PricingSetup) {
	if setup.Status == "missing_config" {
		fmt.Fprintln(w, "pricing: not estimated")
	} else {
		fmt.Fprintln(w, "pricing: partially estimated")
	}
	if setup.Summary != "" {
		fmt.Fprintf(w, "pricing setup: %s\n", setup.Summary)
	}
	if setup.UpdateCommand != "" {
		fmt.Fprintln(w, "generate pricing config:")
		fmt.Fprintf(w, "  %s\n", setup.UpdateCommand)
	}
	if setup.NextRunSummary != "" {
		fmt.Fprintf(w, "next run: %s\n", setup.NextRunSummary)
	}
}

func humanUSD(value float64) string {
	return fmt.Sprintf("$%.2f", value)
}

func printInfrastructureEstimate(w io.Writer, infra skiffcost.InfraEstimate) {
	fmt.Fprintf(w, "infrastructure estimate: %s %s", infra.Provider, infra.Region)
	if infra.PublicationDate != "" {
		fmt.Fprintf(w, " published %s", infra.PublicationDate)
	}
	fmt.Fprintln(w)
	if len(infra.Totals) > 0 {
		fmt.Fprintln(w, "estimated totals:")
		for _, total := range infra.Totals {
			fmt.Fprintf(w, "- %s: %s/month, %s/year", total.PricingScheme, humanUSD(total.MonthlyUSD), humanUSD(total.AnnualUSD))
			if total.TermUSD > 0 {
				fmt.Fprintf(w, ", %s/term", humanUSD(total.TermUSD))
			}
			fmt.Fprintln(w)
		}
	}
	if len(infra.Scenarios) > 0 {
		fmt.Fprintln(w, "utilization scenarios:")
		for _, scenario := range infra.Scenarios {
			fmt.Fprintf(w, "- %s: %s", scenario.Name, scenario.Summary)
			if scenario.AssumedReplicas > 0 {
				fmt.Fprintf(w, "; %d replica(s)", scenario.AssumedReplicas)
			}
			if scenario.SnapshotDataGB > 0 {
				fmt.Fprintf(w, "; %.0f GB snapshots", scenario.SnapshotDataGB)
			}
			fmt.Fprintln(w)
			for _, total := range scenario.Totals {
				fmt.Fprintf(w, "  - %s: %s/month, %s/year", total.PricingScheme, humanUSD(total.MonthlyUSD), humanUSD(total.AnnualUSD))
				if total.TermUSD > 0 {
					fmt.Fprintf(w, ", %s/term", humanUSD(total.TermUSD))
				}
				fmt.Fprintln(w)
			}
		}
	}
	if len(infra.LineItems) > 0 {
		fmt.Fprintln(w, "infrastructure line items:")
		for _, item := range infra.LineItems {
			scheme := ""
			if item.PricingScheme != "" {
				scheme = " [" + item.PricingScheme + "]"
			}
			if item.Estimated {
				fmt.Fprintf(w, "- %s%s: %s/month", item.ID, scheme, humanUSD(item.MonthlyUSD))
				if item.Quantity > 0 && item.Unit != "" {
					fmt.Fprintf(w, " (%.0f %s", item.Quantity, item.Unit)
					if item.UnitPriceUSD > 0 {
						fmt.Fprintf(w, " at %s/%s", humanUSD(item.UnitPriceUSD), item.Unit)
					}
					fmt.Fprint(w, ")")
				}
				if item.Summary != "" {
					fmt.Fprintf(w, " - %s", item.Summary)
				}
				fmt.Fprintln(w)
				continue
			}
			fmt.Fprintf(w, "- %s%s: not estimated", item.ID, scheme)
			if item.Summary != "" {
				fmt.Fprintf(w, " - %s", item.Summary)
			}
			fmt.Fprintln(w)
		}
	}
	if len(infra.Notes) > 0 {
		fmt.Fprintln(w, "infrastructure notes:")
		for _, note := range infra.Notes {
			fmt.Fprintf(w, "- %s\n", note)
		}
	}
}
