package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	terraformrender "github.com/s1liconcow/skiff/internal/terraform"
)

type terraformGenerateOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Result  terraformGenerateResult `json:"result"`
}

type terraformGenerateResult struct {
	Service       string            `json:"service"`
	Env           string            `json:"env"`
	Provider      string            `json:"provider"`
	OutDir        string            `json:"out_dir"`
	Files         map[string]string `json:"files"`
	ResourceCount int               `json:"resource_count"`
}

func runTerraform(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTerraformUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "generate":
		return runTerraformGenerate(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printTerraformUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "terraform", root.Format, root.TraceID, fmt.Errorf("unknown terraform command %q", args[0]), stdout, stderr)
	}
}

func runTerraformGenerate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" terraform generate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	outDir := fs.String("out", "", "directory for generated Terraform module")
	filePath := fs.String("file", "", "Skiff spec file")
	releaseID := fs.String("release-id", "desired", "release ID placeholder for runner user-data")
	ownershipMode := fs.String("ownership-mode", "terraform-infra-skiff-release", "resource ownership mode emitted for adoption")

	flagArgs, positionals, err := splitTerraformGenerateArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "terraform generate", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, errors.New("spec file is required"), stdout, stderr)
	}
	if *outDir == "" {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, errors.New("--out is required"), stdout, stderr)
	}
	if *flags.provider != "" && *flags.provider != "aws" {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, errors.New("terraform generate currently supports --provider aws"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	resources, err := aws.LowerService(graph, aws.LowerOptions{
		Region:      *flags.region,
		StateBucket: firstNonEmptyString(*flags.stateBucket, *flags.state),
		ReleaseID:   *releaseID,
	})
	if err != nil {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	module, err := terraformrender.RenderAWSService(resources, terraformrender.Options{
		Region:        *flags.region,
		StateBucket:   firstNonEmptyString(*flags.stateBucket, *flags.state),
		OwnershipMode: *ownershipMode,
	})
	if err != nil {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	files := map[string]string{}
	names := make([]string, 0, len(module.Files))
	for name := range module.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(*outDir, name)
		if err := os.WriteFile(path, []byte(module.Files[name]), 0o644); err != nil {
			return writeClientCommandError(binary, "terraform generate", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		files[name] = path
	}
	result := terraformGenerateResult{
		Service:       resources.Service,
		Env:           resources.Env,
		Provider:      aws.Name,
		OutDir:        *outDir,
		Files:         files,
		ResourceCount: len(module.Mapping.Resources),
	}
	return writeTerraformGenerateResult(binary, *flags.format, *flags.traceID, result, stdout, stderr)
}

func writeTerraformGenerateResult(binary, format, traceID string, result terraformGenerateResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "terraform module for %s/%s written to %s\n", result.Env, result.Service, result.OutDir)
		names := make([]string, 0, len(result.Files))
		for name := range result.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(stdout, "- %s\n", result.Files[name])
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(terraformGenerateOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s terraform generate: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "terraform generate", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitTerraformGenerateArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":        true,
		"config":         true,
		"env":            true,
		"file":           true,
		"format":         true,
		"mode":           true,
		"out":            true,
		"ownership-mode": true,
		"provider":       true,
		"region":         true,
		"release-id":     true,
		"state":          true,
		"state-bucket":   true,
		"trace-id":       true,
	}
	return splitArgs(args, valueFlags)
}

func printTerraformUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s terraform generate <skiff.yaml> --out <dir> [flags]\n", binary)
}
