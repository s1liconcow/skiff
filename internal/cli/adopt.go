package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/adopt"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
)

type adoptTerraformOutput struct {
	OK      bool               `json:"ok"`
	TraceID string             `json:"trace_id,omitempty"`
	Result  adopt.RecordResult `json:"result"`
}

func runAdopt(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAdoptUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "terraform":
		return runAdoptTerraform(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printAdoptUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "adopt", root.Format, root.TraceID, fmt.Errorf("unknown adopt command %q", args[0]), stdout, stderr)
	}
}

func runAdoptTerraform(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" adopt terraform", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name for mappings that omit it")
	ownershipMode := fs.String("ownership-mode", adopt.OwnershipTerraformInfraSkiffRelease, "resource ownership mode: direct, terraform-infra-skiff-release, or external")

	flagArgs, positionals, err := splitAdoptTerraformArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "adopt terraform", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "adopt terraform", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writeClientCommandError(binary, "adopt terraform", *flags.format, *flags.traceID, errors.New("terraform output or mapping path is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "adopt terraform", *flags.format, *flags.traceID, errors.New("adopt terraform requires --direct mode"), stdout, stderr)
	}
	env := firstNonEmptyString(*flags.env, loaded.Config.Env)
	mapping, err := adopt.LoadTerraformMapping(positionals[0], adopt.LoadOptions{
		Service:       *service,
		Env:           env,
		OwnershipMode: *ownershipMode,
	})
	if err != nil {
		return writeClientCommandError(binary, "adopt terraform", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "adopt terraform", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := adopt.RecordTerraform(nilContext(), store, mapping, adopt.RecordOptions{})
	if err != nil {
		return writeClientError(binary, "adopt terraform", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeAdoptTerraformResult(binary, *flags.format, *flags.traceID, *result, stdout, stderr)
}

func writeAdoptTerraformResult(binary, format, traceID string, result adopt.RecordResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "adopted %d Terraform resource(s) for %s/%s\n", len(result.Resources), result.Env, result.Service)
		for _, resource := range result.Resources {
			fmt.Fprintf(stdout, "- %s %s -> %s\n", resource.Kind, resource.LogicalID, resource.ProviderID)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(adoptTerraformOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s adopt terraform: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "adopt terraform", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitAdoptTerraformArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":        true,
		"config":         true,
		"env":            true,
		"format":         true,
		"mode":           true,
		"ownership-mode": true,
		"provider":       true,
		"region":         true,
		"service":        true,
		"state":          true,
		"state-bucket":   true,
		"trace-id":       true,
	}
	return splitArgs(args, valueFlags)
}

func printAdoptUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s adopt terraform <skiff_resources.json> --direct --state <uri> [flags]\n", binary)
}
