package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/spec"
)

var sha256DigestPattern = regexp.MustCompile(`^sha256:[A-Fa-f0-9]{64}$`)

type contractTestOutput struct {
	OK      bool               `json:"ok"`
	Code    string             `json:"code,omitempty"`
	Summary string             `json:"summary,omitempty"`
	TraceID string             `json:"trace_id,omitempty"`
	Result  contractTestResult `json:"result"`
}

type contractTestResult struct {
	OK             bool            `json:"ok"`
	Service        string          `json:"service,omitempty"`
	Env            string          `json:"env,omitempty"`
	ArtifactURI    string          `json:"artifact_uri,omitempty"`
	ArtifactDigest string          `json:"artifact_digest,omitempty"`
	Checks         []contractCheck `json:"checks"`
}

type contractCheck struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
}

func runContract(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printContractUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "test":
		return runContractTest(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printContractUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s contract: unknown command %q\n", binary, args[0])
		printContractUsage(stderr, binary)
		return ExitUserError
	}
}

func runContractTest(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" contract test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	artifactURI := fs.String("artifact-uri", "", "artifact URI to verify")
	artifactDigest := fs.String("artifact-digest", "", "artifact digest, sha256:<hex>")
	allowUnknown := fs.Bool("allow-unknown-fields", false, "accept unknown fields for compatibility checks")

	flagArgs, positionals, err := splitContractTestArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "contract test", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "contract test", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "contract test", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeSpecError(binary, "CONTRACT_INVALID", *flags.format, *flags.traceID, errors.New("spec file is required"), nil, stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{AllowUnknownFields: *allowUnknown})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	result := spec.Validate(*doc)
	if !result.OK {
		return writeSpecError(binary, "SPEC_INVALID", *flags.format, *flags.traceID, errors.New("spec validation failed"), result.Diagnostics, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *flags.format, *flags.traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	contract := buildContractTestResult(*doc, *artifactURI, *artifactDigest)
	if graph != nil {
		contract.Service = graph.Service
		contract.Env = graph.Env
	}
	return writeContractTestResult(binary, *flags.format, *flags.traceID, contract, stdout, stderr)
}

func buildContractTestResult(doc spec.Document, artifactURI, artifactDigest string) contractTestResult {
	contract := contractTestResult{OK: true}
	checks := []contractCheck{
		{ID: "spec_valid", OK: true, Summary: "spec decoded and validated"},
		{ID: "compile_valid", OK: true, Summary: "spec compiled to provider-neutral IR"},
	}
	if doc.Artifact != nil {
		contract.ArtifactURI = strings.TrimSpace(doc.Artifact.Ref)
		contract.ArtifactDigest = strings.TrimSpace(doc.Artifact.Digest)
	}
	if strings.TrimSpace(artifactURI) != "" {
		contract.ArtifactURI = strings.TrimSpace(artifactURI)
	}
	if strings.TrimSpace(artifactDigest) != "" {
		contract.ArtifactDigest = strings.TrimSpace(artifactDigest)
	}
	if contract.ArtifactDigest == "" {
		contract.ArtifactDigest = digestFromArtifactURI(contract.ArtifactURI)
	}
	immutable := strings.Contains(contract.ArtifactURI, "@sha256:") || sha256DigestPattern.MatchString(contract.ArtifactDigest)
	digestOK := sha256DigestPattern.MatchString(contract.ArtifactDigest)
	checks = append(checks,
		contractCheck{ID: "artifact_immutable", OK: immutable, Summary: "artifact reference is immutable"},
		contractCheck{ID: "artifact_digest", OK: digestOK, Summary: "artifact digest is a sha256 digest"},
	)
	for _, check := range checks {
		if !check.OK {
			contract.OK = false
			break
		}
	}
	contract.Checks = checks
	return contract
}

func digestFromArtifactURI(uri string) string {
	_, digest, ok := strings.Cut(strings.TrimSpace(uri), "@")
	if !ok {
		return ""
	}
	return strings.TrimSpace(digest)
}

func writeContractTestResult(binary, format, traceID string, result contractTestResult, stdout, stderr io.Writer) int {
	exitCode := ExitSuccess
	code := ""
	summary := ""
	if !result.OK {
		exitCode = ExitUserError
		code = "CONTRACT_FAILED"
		summary = "contract checks failed"
	}
	switch format {
	case "human", "text":
		if result.OK {
			fmt.Fprintf(stdout, "contract ok for %s/%s\n", result.Service, result.Env)
			return exitCode
		}
		fmt.Fprintln(stdout, "contract checks failed")
		for _, check := range result.Checks {
			state := "ok"
			if !check.OK {
				state = "failed"
			}
			fmt.Fprintf(stdout, "- %s %s\n", state, check.Summary)
		}
		return exitCode
	case "json":
		if err := json.NewEncoder(stdout).Encode(contractTestOutput{OK: result.OK, Code: code, Summary: summary, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s contract test: %v\n", binary, err)
			return ExitInternalError
		}
		return exitCode
	default:
		return writeClientCommandError(binary, "contract test", format, traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitContractTestArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"allow-unknown-fields": false,
		"api":                  false,
		"api-url":              true,
		"artifact-digest":      true,
		"artifact-uri":         true,
		"config":               true,
		"direct":               false,
		"env":                  true,
		"file":                 true,
		"format":               true,
		"mode":                 true,
		"no-color":             false,
		"provider":             true,
		"region":               true,
		"state":                true,
		"state-bucket":         true,
		"trace-id":             true,
		"yes":                  false,
	}
	return splitArgs(args, valueFlags)
}

func printContractUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s contract test <skiff.yaml> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  test       Validate CI contract and immutable artifact evidence")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Test flags:")
	fmt.Fprintln(w, "  --artifact-uri <uri>")
	fmt.Fprintln(w, "  --artifact-digest sha256:<hex>")
	fmt.Fprintln(w, "  --format human|json")
}
