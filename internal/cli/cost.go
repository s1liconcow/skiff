package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/compiler"
	skiffcost "github.com/s1liconcow/skiff/internal/cost"
	"github.com/s1liconcow/skiff/internal/spec"
)

type costExplainOutput struct {
	OK      bool             `json:"ok"`
	TraceID string           `json:"trace_id,omitempty"`
	Result  skiffcost.Result `json:"result"`
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

func runCost(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCostUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "explain":
		return runCostExplain(binary, args[1:], root, stdout, stderr)
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

	format := fs.String("format", root.Format, "output format: human or json")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "Skiff YAML or JSON service spec file")
	window := fs.String("window", "", "observation window for supplied metrics")
	var cpuP95 optionalFloatFlag
	var memoryP95 optionalFloatFlag
	var requestCount optionalFloatFlag
	var requestRPS optionalFloatFlag
	var unhealthyTargets optionalIntFlag
	var warmCapacity optionalIntFlag
	var logMBPerHour optionalFloatFlag
	fs.Var(&cpuP95, "cpu-p95", "observed CPU p95 percent")
	fs.Var(&memoryP95, "memory-p95", "observed memory p95 percent")
	fs.Var(&requestCount, "request-count", "observed request count over --window")
	fs.Var(&requestRPS, "request-rps", "observed request rate in requests per second")
	fs.Var(&unhealthyTargets, "unhealthy-targets", "observed unhealthy load balancer target count")
	fs.Var(&warmCapacity, "warm-capacity", "operator target for always-warm replica count")
	fs.Var(&logMBPerHour, "log-mb-per-hour", "observed log volume in MB per hour")

	flagArgs, positionals, err := splitCostExplainArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "cost explain", *format, *traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
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
	default:
		return writeClientCommandError(binary, "cost explain", *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitCostExplainArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"cpu-p95":           true,
		"file":              true,
		"format":            true,
		"log-mb-per-hour":   true,
		"memory-p95":        true,
		"request-count":     true,
		"request-rps":       true,
		"trace-id":          true,
		"unhealthy-targets": true,
		"warm-capacity":     true,
		"window":            true,
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
	fmt.Fprintln(w, "  explain  Explain service shape, replica, warm-capacity, and log-volume recommendations")
}
