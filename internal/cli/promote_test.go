package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestReleaseCandidateCreateJSONWritesCandidate(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"release", "candidate", "create",
		"--direct",
		"--state", "file://" + root,
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--service", "payments-api",
		"--candidate-id", "cand_cli",
		"--release-id", "rel_cli",
		"--artifact-uri", "registry.example.com/payments-api@sha256:" + strings.TrimPrefix(cliDigest(), "sha256:"),
		"--artifact-digest", cliDigest(),
		"--check", "tests=passed",
		"--check", "contract=passed",
		"--check", "policy=passed",
		"--check", "scan=passed",
		"--format", "json",
		"--trace-id", "tr_candidate_cli",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got releaseCandidateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode candidate output: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_candidate_cli" || got.Result.Candidate.CandidateID != "cand_cli" {
		t.Fatalf("unexpected output: %+v", got)
	}
	path := filepath.Join(root, "services", "payments-api", "candidates", "cand_cli", "candidate.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("candidate object not written: %v", err)
	}
}

func TestPromoteMarkdownRendersPlan(t *testing.T) {
	clearSkiffEnv(t)
	root := seedPromotionCLIState(t, "staging", "rel_cli")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"release", "promote", "payments-api",
		"--direct",
		"--state", "file://" + root,
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--from", "staging",
		"--to", "dev",
		"--candidate", "cand_cli",
		"--dry-run",
		"--format", "markdown",
		"--trace-id", "tr_promote_md",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{"# Promotion Plan", "- [x] check tests passed", "skiff deploy <spec> --release-id rel_cli", cliDigest()} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("markdown missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestPromoteJSONReportsMissingApproval(t *testing.T) {
	clearSkiffEnv(t)
	root := seedPromotionCLIState(t, "staging", "rel_cli")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"release", "promote", "payments-api",
		"--direct",
		"--state", "file://" + root,
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--from", "staging",
		"--to", "prod",
		"--candidate", "cand_cli",
		"--format", "json",
		"--trace-id", "tr_promote_json",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}
	var got promoteOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode promote output: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "PROMOTION_REQUIREMENTS_FAILED" || !cliHasRequirement(got.Result.Requirements, "APPROVAL_REQUIRED") {
		t.Fatalf("unexpected promote output: %+v", got)
	}
}

func seedPromotionCLIState(t *testing.T, env, stableRelease string) string {
	t.Helper()
	root := t.TempDir()
	writeStateObject(t, root, "services/payments-api/candidates/cand_cli/candidate.json", cliCandidate(env, stableRelease))
	control := schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            env,
		DesiredRelease: stableRelease,
		StableRelease:  stableRelease,
		Version:        1,
		UpdatedAt:      canonical.Time(time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)),
		UpdatedBy:      schema.Actor{ID: "seed", Type: "test"},
	}
	writeStateObject(t, root, "services/payments-api/control.json", control)
	return root
}

func cliCandidate(env, releaseID string) schema.ReleaseCandidate {
	return schema.ReleaseCandidate{
		SchemaVersion: schema.Version,
		CandidateID:   "cand_cli",
		Service:       "payments-api",
		Env:           env,
		ReleaseID:     releaseID,
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "registry.example.com/payments-api@sha256:" + strings.TrimPrefix(cliDigest(), "sha256:"),
			Digest: cliDigest(),
		},
		Checks: []schema.EvidenceCheck{
			{Name: "tests", Status: "passed"},
			{Name: "contract", Status: "passed"},
			{Name: "policy", Status: "passed"},
			{Name: "scan", Status: "passed"},
		},
		CreatedAt: "2026-05-17T00:00:00Z",
		CreatedBy: schema.Actor{ID: "ci", Type: "ci"},
	}
}

func cliDigest() string {
	return "sha256:" + strings.Repeat("b", 64)
}

func cliHasRequirement(requirements []release.PromotionRequirement, code string) bool {
	for _, requirement := range requirements {
		if requirement.Code == code {
			return true
		}
	}
	return false
}
