package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/s1liconcow/skiff/internal/cicd"
)

type ciGenerateOutput struct {
	OK       bool     `json:"ok"`
	TraceID  string   `json:"trace_id,omitempty"`
	Target   string   `json:"target"`
	FileName string   `json:"file_name"`
	Path     string   `json:"path,omitempty"`
	Commands []string `json:"commands"`
	Content  string   `json:"content"`
}

func runCI(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCIUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "generate":
		return runCIGenerate(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printCIUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s ci: unknown command %q\n", binary, args[0])
		printCIUsage(stderr, binary)
		return ExitUserError
	}
}

func runCIGenerate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" ci generate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	outPath := fs.String("out", "", "write generated template to this path")
	service := fs.String("service", "payments-api", "Skiff service name")
	specPath := fs.String("spec", "skiff.yaml", "Skiff spec path used by the pipeline")
	stateURI := fs.String("state", "s3://skiff-state-prod", "object-state bucket URI")
	provider := fs.String("provider", defaultString(root.Provider, "aws"), "cloud provider name")
	region := fs.String("region", defaultString(root.Region, "us-west-2"), "cloud provider region")
	stagingEnv := fs.String("staging-env", "staging", "staging environment name")
	prodEnv := fs.String("prod-env", "prod", "production environment name")
	imageRepo := fs.String("image-repo", "", "OCI image repository")
	installCommand := fs.String("install-command", "curl -fsSL https://get.skiff.dev | sh", "command that installs skiff in CI")
	skiffBinary := fs.String("skiff-binary", binary, "skiff binary command name")

	flagArgs, positionals, err := splitCIGenerateArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ci generate", *format, *traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "ci generate", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writeClientCommandError(binary, "ci generate", *format, *traceID, errors.New("target is required: github-actions, gitlab, or buildkite"), stdout, stderr)
	}
	_ = noColor
	_ = yes

	generated, err := cicd.Generate(positionals[0], cicd.Options{
		Service:         *service,
		SpecPath:        *specPath,
		StateURI:        *stateURI,
		Provider:        *provider,
		Region:          *region,
		StagingEnv:      *stagingEnv,
		ProductionEnv:   *prodEnv,
		ImageRepository: *imageRepo,
		InstallCommand:  *installCommand,
		SkiffBinary:     *skiffBinary,
	})
	if err != nil {
		return writeClientCommandError(binary, "ci generate", *format, *traceID, err, stdout, stderr)
	}
	if *outPath != "" {
		if err := writeGeneratedTemplate(*outPath, generated.Content); err != nil {
			return writeClientCommandError(binary, "ci generate", *format, *traceID, err, stdout, stderr)
		}
	}
	switch *format {
	case "human", "text":
		if *outPath == "" {
			_, _ = io.WriteString(stdout, generated.Content)
			return ExitSuccess
		}
		fmt.Fprintf(stdout, "generated %s CI template: %s\n", generated.Target, *outPath)
		return ExitSuccess
	case "json":
		out := ciGenerateOutput{
			OK:       true,
			TraceID:  *traceID,
			Target:   generated.Target,
			FileName: generated.FileName,
			Path:     *outPath,
			Commands: generated.Commands,
			Content:  generated.Content,
		}
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintf(stderr, "%s ci generate: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ci generate", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeGeneratedTemplate(path, content string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("--out path is required")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func splitCIGenerateArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"format":          true,
		"image-repo":      true,
		"install-command": true,
		"no-color":        false,
		"out":             true,
		"prod-env":        true,
		"provider":        true,
		"region":          true,
		"service":         true,
		"skiff-binary":    true,
		"spec":            true,
		"staging-env":     true,
		"state":           true,
		"trace-id":        true,
		"yes":             false,
	}
	return splitArgs(args, valueFlags)
}

func printCIUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s ci generate <github-actions|gitlab|buildkite> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  generate   Generate CI/CD pipeline templates")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Generate flags:")
	fmt.Fprintln(w, "  --out <path>")
	fmt.Fprintln(w, "  --service <name> --spec <path> --state <uri>")
	fmt.Fprintln(w, "  --provider <provider> --region <region>")
	fmt.Fprintln(w, "  --staging-env <env> --prod-env <env>")
	fmt.Fprintln(w, "  --image-repo <repository>")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
}
