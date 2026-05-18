package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type releaseCandidateOutput struct {
	OK      bool                          `json:"ok"`
	TraceID string                        `json:"trace_id,omitempty"`
	Result  release.CandidateCreateResult `json:"result"`
}

type releaseCandidateShowOutput struct {
	OK        bool                    `json:"ok"`
	TraceID   string                  `json:"trace_id,omitempty"`
	Candidate schema.ReleaseCandidate `json:"candidate"`
	Key       string                  `json:"key"`
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func runReleaseCandidate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printReleaseCandidateUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "create":
		return runReleaseCandidateCreate(binary, args[1:], root, stdout, stderr)
	case "show":
		return runReleaseCandidateShow(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printReleaseCandidateUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s release candidate: unknown command %q\n", binary, args[0])
		printReleaseCandidateUsage(stderr, binary)
		return ExitUserError
	}
}

func runReleaseCandidateCreate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" release candidate create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	candidateID := fs.String("candidate-id", "", "candidate ID to create")
	service := fs.String("service", "", "service name")
	env := fs.String("candidate-env", "", "candidate source environment")
	releaseID := fs.String("release-id", "", "source release ID")
	artifactType := fs.String("artifact-type", "oci", "artifact package type")
	artifactURI := fs.String("artifact-uri", "", "artifact URI")
	artifactDigest := fs.String("artifact-digest", "", "artifact digest, sha256:<hex>")
	gitRepo := fs.String("git-repo", "", "source git repository")
	gitSHA := fs.String("git-sha", "", "source git commit SHA")
	gitRef := fs.String("git-ref", "", "source git ref")
	ciProvider := fs.String("ci-provider", "", "CI provider")
	ciRunID := fs.String("ci-run-id", "", "CI run ID")
	ciRunURL := fs.String("ci-run-url", "", "CI run URL")
	actorID := fs.String("actor", "skiff-cli", "actor ID creating the candidate")
	actorType := fs.String("actor-type", "ci", "actor type creating the candidate")
	var checks stringListFlag
	var sbom stringListFlag
	var provenance stringListFlag
	var annotations stringListFlag
	fs.Var(&checks, "check", "evidence check as name=status; may be repeated")
	fs.Var(&sbom, "sbom", "SBOM URI; may be repeated")
	fs.Var(&provenance, "provenance", "provenance URI; may be repeated")
	fs.Var(&annotations, "annotation", "annotation as key=value; may be repeated")

	flagArgs, positionals, err := splitReleaseCandidateArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "release-candidate-create", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "release-candidate-create", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "release-candidate-create", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *candidateID == "" {
		*candidateID = positionals[0]
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if *env == "" {
		*env = loaded.Config.Env
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "release-candidate-create", *flags.format, *flags.traceID, errors.New("release candidate create currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "release-candidate-create", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	parsedChecks, err := parseEvidenceChecks(checks)
	if err != nil {
		return writeClientCommandError(binary, "release-candidate-create", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := (release.Manager{Store: store}).CreateCandidate(nilContext(), release.CandidateCreateRequest{
		CandidateID: *candidateID,
		Service:     *service,
		Env:         *env,
		ReleaseID:   *releaseID,
		Artifact:    schema.ArtifactRef{Type: *artifactType, URI: *artifactURI, Digest: *artifactDigest},
		Git:         schema.GitMetadata{Repo: *gitRepo, SHA: *gitSHA, Ref: *gitRef},
		CI:          schema.CIMetadata{Provider: *ciProvider, RunID: *ciRunID, RunURL: *ciRunURL},
		Checks:      parsedChecks,
		SBOM:        evidenceRefs(sbom),
		Provenance:  evidenceRefs(provenance),
		Actor:       schema.Actor{ID: *actorID, Type: *actorType},
		TraceID:     *flags.traceID,
		Annotations: parseAnnotations(annotations),
	})
	if err != nil {
		return writeClientCommandError(binary, "release-candidate-create", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "release candidate %s created\n", result.Candidate.CandidateID)
		fmt.Fprintf(stdout, "artifact: %s\n", result.Candidate.Artifact.Digest)
		if result.Candidate.ReleaseID != "" {
			fmt.Fprintf(stdout, "release: %s\n", result.Candidate.ReleaseID)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(releaseCandidateOutput{OK: result.OK, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s release candidate create: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "release-candidate-create", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runReleaseCandidateShow(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" release candidate show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	candidateID := fs.String("candidate-id", "", "candidate ID to inspect")
	service := fs.String("service", "", "service name")

	flagArgs, positionals, err := splitReleaseCandidateArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "release-candidate-show", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "release-candidate-show", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "release-candidate-show", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *candidateID == "" {
		*candidateID = positionals[0]
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "release-candidate-show", *flags.format, *flags.traceID, errors.New("release candidate show currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "release-candidate-show", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	doc, err := (release.Manager{Store: store}).ReadCandidate(nilContext(), *service, *candidateID)
	if err != nil {
		return writeClientCommandError(binary, "release-candidate-show", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "candidate: %s\n", doc.Candidate.CandidateID)
		fmt.Fprintf(stdout, "service: %s\n", doc.Candidate.Service)
		fmt.Fprintf(stdout, "env: %s\n", doc.Candidate.Env)
		fmt.Fprintf(stdout, "artifact: %s\n", doc.Candidate.Artifact.Digest)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(releaseCandidateShowOutput{OK: true, TraceID: *flags.traceID, Candidate: doc.Candidate, Key: doc.Key}); err != nil {
			fmt.Fprintf(stderr, "%s release candidate show: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "release-candidate-show", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitReleaseCandidateArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"actor":           true,
		"actor-type":      true,
		"annotation":      true,
		"api-url":         true,
		"artifact-digest": true,
		"artifact-type":   true,
		"artifact-uri":    true,
		"candidate-env":   true,
		"candidate-id":    true,
		"check":           true,
		"ci-provider":     true,
		"ci-run-id":       true,
		"ci-run-url":      true,
		"config":          true,
		"env":             true,
		"format":          true,
		"git-ref":         true,
		"git-repo":        true,
		"git-sha":         true,
		"mode":            true,
		"provenance":      true,
		"provider":        true,
		"region":          true,
		"release-id":      true,
		"sbom":            true,
		"service":         true,
		"state":           true,
		"state-bucket":    true,
		"trace-id":        true,
	}
	return splitArgs(args, valueFlags)
}

func parseEvidenceChecks(values []string) ([]schema.EvidenceCheck, error) {
	checks := make([]schema.EvidenceCheck, 0, len(values))
	for _, value := range values {
		name, status, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(status) == "" {
			return nil, fmt.Errorf("--check %q must use name=status", value)
		}
		checks = append(checks, schema.EvidenceCheck{Name: strings.TrimSpace(name), Status: strings.TrimSpace(status)})
	}
	return checks, nil
}

func evidenceRefs(values []string) []schema.EvidenceRef {
	if len(values) == 0 {
		return nil
	}
	refs := make([]schema.EvidenceRef, 0, len(values))
	for _, value := range values {
		refs = append(refs, schema.EvidenceRef{URI: strings.TrimSpace(value)})
	}
	return refs
}

func parseAnnotations(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func printReleaseCandidateUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s release candidate <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  create     Create immutable release candidate evidence")
	fmt.Fprintln(w, "  show       Inspect release candidate evidence")
}
