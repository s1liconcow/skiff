package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	kubeimport "github.com/s1liconcow/skiff/internal/importer/kube"
)

type importKubeOutput struct {
	OK      bool              `json:"ok"`
	TraceID string            `json:"trace_id,omitempty"`
	Result  kubeimport.Result `json:"result"`
}

func runImport(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printImportUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "kube", "kubernetes":
		return runImportKube(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printImportUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeImportError(binary, "import", root.Format, root.TraceID, fmt.Errorf("unknown import command %q", args[0]), stdout, stderr)
	}
}

func runImportKube(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" import kube", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, yaml, markdown, or json")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "Kubernetes YAML or JSON manifest")
	env := fs.String("env", root.Env, "Skiff environment for generated service")
	name := fs.String("name", "", "Skiff service name override")

	flagArgs, positionals, err := splitImportKubeArgs(args)
	if err != nil {
		return writeImportError(binary, "import kube", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeImportError(binary, "import kube", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeImportError(binary, "import kube", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeImportError(binary, "import kube", *format, *traceID, errors.New("Kubernetes manifest file is required"), stdout, stderr)
	}
	_ = noColor
	_ = yes

	body, err := os.ReadFile(*filePath)
	if err != nil {
		return writeImportError(binary, "import kube", *format, *traceID, err, stdout, stderr)
	}
	objects, err := kubeimport.Parse(body)
	if err != nil {
		return writeImportError(binary, "import kube", *format, *traceID, err, stdout, stderr)
	}
	result, err := kubeimport.Convert(objects, kubeimport.Options{Name: *name, Env: *env})
	if err != nil {
		return writeImportError(binary, "import kube", *format, *traceID, err, stdout, stderr)
	}
	exit := ExitSuccess
	if !result.OK {
		exit = ExitUserError
	}
	switch *format {
	case "human", "text", "markdown":
		fmt.Fprint(stdout, result.MarkdownReport)
	case "yaml":
		fmt.Fprint(stdout, result.SkiffYAML)
	case "json":
		if err := json.NewEncoder(stdout).Encode(importKubeOutput{OK: result.OK, TraceID: *traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s import kube: %v\n", binary, err)
			return ExitInternalError
		}
	default:
		return writeImportError(binary, "import kube", *format, *traceID, errors.New(`unsupported format; expected "human", "yaml", "markdown", "json", or "json-pretty"`), stdout, stderr)
	}
	return exit
}

func splitImportKubeArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"env":      true,
		"file":     true,
		"format":   true,
		"name":     true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func writeImportError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(commandErrorOutput{
			OK:      false,
			Code:    "IMPORT_FAILED",
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "review_manifest", Command: binary + " import kube <manifest.yaml> --format markdown", Mutating: false},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return ExitUserError
}

func printImportUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s import kube <manifest.yaml> [flags]\n", binary)
}
