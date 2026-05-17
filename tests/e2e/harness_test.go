package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/cli"
)

type e2eReport struct {
	Mode                    string       `json:"mode"`
	RunID                   string       `json:"run_id"`
	TraceID                 string       `json:"trace_id"`
	Service                 string       `json:"service,omitempty"`
	Env                     string       `json:"env,omitempty"`
	StartedAt               string       `json:"started_at"`
	FinishedAt              string       `json:"finished_at,omitempty"`
	Facts                   []reportFact `json:"facts,omitempty"`
	Commands                []string     `json:"commands,omitempty"`
	ObjectPaths             []string     `json:"object_paths,omitempty"`
	ProviderIDs             []string     `json:"provider_ids,omitempty"`
	OperationIDs            []string     `json:"operation_ids,omitempty"`
	SagaIDs                 []string     `json:"saga_ids,omitempty"`
	CleanupStatus           string       `json:"cleanup_status,omitempty"`
	RecommendedNextCommands []string     `json:"recommended_next_commands,omitempty"`
}

type reportFact struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func newE2EReport(t *testing.T, mode, service, env, traceID string) *e2eReport {
	t.Helper()
	runID := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	return &e2eReport{
		Mode:      mode,
		RunID:     runID,
		TraceID:   traceID,
		Service:   service,
		Env:       env,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		RecommendedNextCommands: []string{
			"skiff status " + service + " --direct --format json --trace-id " + traceID,
			"skiff events --scope service --service " + service + " --direct --format json --trace-id " + traceID,
		},
	}
}

func (r *e2eReport) fact(kind, message string) {
	r.Facts = append(r.Facts, reportFact{Type: kind, Message: message})
}

func (r *e2eReport) addObjectPath(path string) {
	if path != "" {
		r.ObjectPaths = appendUnique(r.ObjectPaths, path)
	}
}

func (r *e2eReport) addProviderID(id string) {
	if id != "" {
		r.ProviderIDs = appendUnique(r.ProviderIDs, id)
	}
}

func (r *e2eReport) addOperationID(id string) {
	if id != "" {
		r.OperationIDs = appendUnique(r.OperationIDs, id)
	}
}

func (r *e2eReport) addSagaID(id string) {
	if id != "" {
		r.SagaIDs = appendUnique(r.SagaIDs, id)
	}
}

func runSkiffCLI(t *testing.T, report *e2eReport, args ...string) []byte {
	t.Helper()
	if report != nil {
		report.Commands = append(report.Commands, "skiff "+strings.Join(args, " "))
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", args, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("skiff %s exit=%d stderr=%s stdout=%s", strings.Join(args, " "), code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("skiff %s stderr=%s", strings.Join(args, " "), stderr.String())
	}
	return stdout.Bytes()
}

func decodeCLIJSON[T any](t *testing.T, body []byte, out *T) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, string(body))
	}
}

func writeE2EReport(t *testing.T, report *e2eReport) {
	t.Helper()
	if report == nil {
		return
	}
	report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	dir := strings.TrimSpace(os.Getenv("SKIFF_E2E_REPORT_DIR"))
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create e2e report dir: %v", err)
	}
	path := filepath.Join(dir, report.RunID+".json")
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encode e2e report: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write e2e report: %v", err)
	}
	t.Logf("wrote e2e report %s", path)
}

func resetSkiffEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SKIFF_ENV",
		"SKIFF_PROVIDER",
		"SKIFF_REGION",
		"SKIFF_STATE_BUCKET",
		"SKIFF_KMS_KEY",
		"SKIFF_AUTH_MODE",
		"SKIFF_LOG_LEVEL",
		"SKIFF_MODE",
		"SKIFF_API_URL",
		"SKIFF_SERVICE",
		"SKIFF_CONTROL_KEY",
		"SKIFF_RELEASE_ID",
	} {
		t.Setenv(key, "")
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
