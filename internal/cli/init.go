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
)

type initStackOutput struct {
	OK        bool     `json:"ok"`
	TraceID   string   `json:"trace_id,omitempty"`
	Recipe    string   `json:"recipe"`
	Name      string   `json:"name"`
	Directory string   `json:"directory"`
	Files     []string `json:"files"`
}

func runInit(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printInitUsage(stdout, binary)
		return ExitSuccess
	}
	if args[0] != "stack" {
		return writeClientCommandError(binary, "init", root.Format, root.TraceID, fmt.Errorf("unsupported init target %q; expected stack", args[0]), stdout, stderr)
	}
	return runInitStack(binary, args[1:], root, stdout, stderr)
}

func runInitStack(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printInitUsage(stdout, binary)
		return ExitSuccess
	}
	recipe := args[0]
	if recipe != "api-database" {
		return writeClientCommandError(binary, "init", root.Format, root.TraceID, fmt.Errorf("unsupported stack recipe %q; expected api-database", recipe), stdout, stderr)
	}

	fs := flag.NewFlagSet(binary+" init stack api-database", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human or json")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "overwrite existing generated files")
	dir := fs.String("dir", "", "directory to create")
	env := fs.String("env", firstNonEmptyCLI(root.Env, "prod"), "Skiff environment name")
	artifact := fs.String("artifact", "", "OCI artifact reference for the API service")
	overwrite := fs.Bool("overwrite", false, "overwrite existing generated files")

	flagArgs, positionals, err := splitInitStackArgs(args[1:])
	if err != nil {
		return writeClientCommandError(binary, "init", *format, *traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "init", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writeClientCommandError(binary, "init", *format, *traceID, errors.New("stack name is required"), stdout, stderr)
	}
	_ = noColor

	name := strings.TrimSpace(positionals[0])
	if name == "" {
		return writeClientCommandError(binary, "init", *format, *traceID, errors.New("stack name is required"), stdout, stderr)
	}
	targetDir := *dir
	if strings.TrimSpace(targetDir) == "" {
		targetDir = name
	}
	if *artifact == "" {
		*artifact = fmt.Sprintf("registry.example.com/%s-api@sha256:REPLACE_WITH_DIGEST", name)
	}
	files := map[string]string{
		filepath.Join(targetDir, "skiff.yaml"):                                apiDatabaseSpecTemplate(name, *env, *artifact),
		filepath.Join(targetDir, "main.go"):                                   apiDatabaseMainTemplate(name),
		filepath.Join(targetDir, ".github", "workflows", "skiff-release.yml"): apiDatabaseCITemplate(name),
	}
	written := make([]string, 0, len(files))
	for path, body := range files {
		if err := writeInitFile(path, body, *yes || *overwrite); err != nil {
			return writeClientCommandError(binary, "init", *format, *traceID, err, stdout, stderr)
		}
		written = append(written, path)
	}
	sortStrings(written)

	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "created api-database stack %s in %s\n", name, targetDir)
		for _, file := range written {
			fmt.Fprintf(stdout, "- %s\n", file)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(initStackOutput{OK: true, TraceID: *traceID, Recipe: recipe, Name: name, Directory: targetDir, Files: written}); err != nil {
			fmt.Fprintf(stderr, "%s init: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "init", *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func printInitUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s init stack api-database <name> [flags]\n\n", binary)
	fmt.Fprintln(w, "Generates an API service plus managed database stack template, example app, and CI workflow.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --dir <path> --env <env> --artifact <oci-ref> --overwrite --format human|json --no-color --yes --trace-id <id>")
}

func splitInitStackArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"artifact": true,
		"dir":      true,
		"env":      true,
		"format":   true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func writeInitFile(path, body string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; pass --overwrite or --yes", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func apiDatabaseSpecTemplate(name, env, artifact string) string {
	return fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: %s
  env: %s
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: %s
      runtime:
        port: 8080
        env:
          SKIFF_STACK: %s
        health:
          path: /healthz
      scale:
        min: 2
        max: 4
      network:
        ingress:
          type: internal-http
  databases:
    - name: db
      engine: postgres
      version: "16"
      size: small
      storage:
        sizeGB: 20
        type: gp3
        encrypted: true
      backups:
        enabled: true
        retentionDays: 7
      network:
        private: true
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`, name, env, artifact, name)
}

func apiDatabaseMainTemplate(name string) string {
	return fmt.Sprintf(`package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("DATABASE_URL") == "" {
			http.Error(w, "database binding missing", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintf(w, "%s api is bound to a managed database\n")
	})
	_ = http.ListenAndServe(":8080", nil)
}
`, name)
}

func apiDatabaseCITemplate(name string) string {
	return fmt.Sprintf(`name: skiff-release

on:
  push:
    branches: [ main ]

jobs:
  candidate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build and publish immutable artifact
        run: echo "publish registry.example.com/%s-api@sha256:${GITHUB_SHA}"
      - name: Record Skiff release candidate
        run: |
          skiff release candidate create %s-api \
            --env staging \
            --candidate cand-${GITHUB_RUN_ID} \
            --artifact registry.example.com/%s-api@sha256:${GITHUB_SHA} \
            --git-repo ${GITHUB_REPOSITORY} \
            --git-sha ${GITHUB_SHA} \
            --ci-provider github-actions \
            --ci-run-id ${GITHUB_RUN_ID} \
            --check tests=passed \
            --format json
`, name, name, name)
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
