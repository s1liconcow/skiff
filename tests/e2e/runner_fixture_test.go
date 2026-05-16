package e2e_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRunnerServesSignedReleaseFixture(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	statePath := filepath.Join(t.TempDir(), "runner-state.json")
	rootDir := filepath.Join(t.TempDir(), "workloads")
	service := "hello-service"
	env := "prod"
	releaseID := "rel_01JE2E"
	traceID := "tr_e2e"
	port := freePort(t)

	artifactRef := helloArtifact(t)
	runtimeManifest := schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       service,
		Env:           env,
		ReleaseID:     releaseID,
		Command:       []string{"./hello.sh", "--port", strconv.Itoa(port)},
		EnvVars: map[string]string{
			"SKIFF_E2E_HELPER": "1",
			"TEST_BINARY":      os.Args[0],
		},
		HealthCheck: &schema.HealthCheck{
			Type:     "http",
			Path:     "/healthz",
			Port:     port,
			Interval: "20ms",
			Timeout:  "1s",
		},
		CreatedAt: "2026-05-18T00:00:00Z",
	}
	publishSignedRelease(t, ctx, store, artifactRef, runtimeManifest)
	controlKey := createDesiredServiceControl(t, ctx, store, service, env, releaseID)

	events := &collectingRunnerSink{}
	bootstrap, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config: config.Config{
			Mode:        config.ModeRunner,
			Env:         env,
			Provider:    "aws",
			Region:      "us-west-2",
			StateBucket: "memory://e2e",
			Service:     service,
			ControlKey:  controlKey,
		},
		Store:    store,
		Verifier: verifierFor(t, testSigner(t)),
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-local-e2e",
		}},
		StateStore: runner.FileStateStore{Path: statePath},
		EventSink:  events,
		TraceID:    traceID,
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if bootstrap.ReleaseID != releaseID || !bootstrap.Verification.OK {
		t.Fatalf("unexpected bootstrap result: %+v", bootstrap)
	}

	preparer := &capturingPreparer{inner: runner.WorkloadArtifactPreparer{RootDir: rootDir}}
	systemd := &processSystemd{t: t, preparer: preparer}
	t.Cleanup(systemd.cleanup)
	lifecycle, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest:  runtimeManifest,
		Artifact:         artifactRef,
		StateStore:       runner.FileStateStore{Path: statePath},
		EventSink:        events,
		Systemd:          systemd,
		ArtifactPreparer: preparer,
		HealthChecker:    runner.ProbeHealthChecker{HTTPClient: &http.Client{Timeout: time.Second}},
		TraceID:          traceID,
		Identity:         &bootstrap.Identity,
		HealthAttempts:   20,
		HealthInterval:   20 * time.Millisecond,
		Now:              fixedNow,
	})
	if err != nil {
		t.Fatalf("RunLifecycle returned error: %v", err)
	}
	if lifecycle.Status.State != runner.StateServing || lifecycle.Status.Health != runner.HealthHealthy {
		t.Fatalf("unexpected lifecycle status: %+v", lifecycle.Status)
	}
	assertHealth(t, port)

	status := readStatus(t, runner.FileStateStore{Path: statePath})
	if status.State != runner.StateServing || status.Health != runner.HealthHealthy || status.ReleaseID != releaseID {
		t.Fatalf("unexpected status endpoint result: %+v", status)
	}

	wantStates := []runner.State{
		runner.StateBooting,
		runner.StateFetchingManifest,
		runner.StateVerifyingRelease,
		runner.StatePreparingArtifact,
		runner.StatePreparingArtifact,
		runner.StateRenderingConfig,
		runner.StateStartingWorkload,
		runner.StateWaitingForHealth,
		runner.StateServing,
	}
	if got := eventStates(events.events); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("runner states = %v, want %v", got, wantStates)
	}
}

func TestE2EHelperProcess(t *testing.T) {
	if os.Getenv("SKIFF_E2E_HELPER") != "1" {
		return
	}
	port := ""
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
			break
		}
	}
	if port == "" {
		fmt.Fprintln(os.Stderr, "--port is required")
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func helloArtifact(t *testing.T) schema.ArtifactRef {
	t.Helper()
	path := filepath.Join("..", "fixtures", "hello-service", "hello.sh")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return schema.ArtifactRef{
		Type:   "binary",
		URI:    path,
		Digest: security.DigestBytes(body),
	}
}

func publishSignedRelease(t *testing.T, ctx context.Context, store objstore.ObjectStore, artifactRef schema.ArtifactRef, runtimeManifest schema.RuntimeManifest) {
	t.Helper()
	runtimeKey, err := paths.RuntimeManifest(runtimeManifest.Service, runtimeManifest.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDigest, err := release.RuntimeManifestDigest(runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion:         schema.Version,
		Service:               runtimeManifest.Service,
		Env:                   runtimeManifest.Env,
		ReleaseID:             runtimeManifest.ReleaseID,
		Artifact:              artifactRef,
		RuntimeManifestKey:    runtimeKey,
		RuntimeManifestDigest: runtimeDigest,
		CreatedAt:             runtimeManifest.CreatedAt,
		ExpiresAt:             "2026-06-18T00:00:00Z",
	}
	signed, err := release.SignManifest(ctx, manifest, testSigner(t), schema.Actor{ID: "e2e", Type: "agent"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	createJSON(t, ctx, store, runtimeKey, runtimeManifest)
	releaseKey, err := paths.ReleaseManifest(runtimeManifest.Service, runtimeManifest.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	createJSON(t, ctx, store, releaseKey, signed)
}

func createDesiredServiceControl(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, env, releaseID string) string {
	t.Helper()
	control := schema.NewServiceControl(service, env, canonical.Time(fixedNow()), schema.Actor{ID: "e2e", Type: "agent"})
	control.DesiredRelease = releaseID
	doc, err := state.NewClient(store, state.WithClock(testClock{})).CreateServiceControl(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	return doc.Key
}

func createJSON(t *testing.T, ctx context.Context, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

type capturingPreparer struct {
	inner  runner.WorkloadArtifactPreparer
	result *runner.ArtifactResult
}

func (p *capturingPreparer) PrepareArtifact(ctx context.Context, req runner.ArtifactRequest) (*runner.ArtifactResult, error) {
	result, err := p.inner.PrepareArtifact(ctx, req)
	if err != nil {
		return nil, err
	}
	p.result = result
	return result, nil
}

type processSystemd struct {
	t        testing.TB
	preparer *capturingPreparer
	cmd      *exec.Cmd
	unitBody string
}

func (s *processSystemd) WriteUnit(ctx context.Context, name string, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.unitBody = string(contents)
	return nil
}

func (s *processSystemd) DaemonReload(ctx context.Context) error {
	return ctx.Err()
}

func (s *processSystemd) StartUnit(ctx context.Context, name string) error {
	return s.RestartUnit(ctx, name)
}

func (s *processSystemd) RestartUnit(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.preparer.result == nil {
		return fmt.Errorf("artifact was not prepared")
	}
	command := append([]string(nil), s.preparer.result.Command...)
	if len(command) == 0 {
		return fmt.Errorf("prepared command is empty")
	}
	if !filepath.IsAbs(command[0]) {
		command[0] = filepath.Join(s.preparer.result.WorkingDirectory, command[0])
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = s.preparer.result.WorkingDirectory
	cmd.Env = os.Environ()
	for key, value := range s.preparer.result.EnvVars {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	return nil
}

func (s *processSystemd) StopUnit(ctx context.Context, name string) error {
	s.cleanup()
	return ctx.Err()
}

func (s *processSystemd) cleanup() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Kill()
	_, _ = s.cmd.Process.Wait()
	s.cmd = nil
}

type collectingRunnerSink struct {
	events []runner.StateEvent
}

func (s *collectingRunnerSink) EmitRunnerEvent(ctx context.Context, event runner.StateEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func eventStates(events []runner.StateEvent) []runner.State {
	out := make([]runner.State, 0, len(events))
	for _, event := range events {
		out = append(out, event.State)
	}
	return out
}

func readStatus(t *testing.T, store runner.StateStore) runner.RunnerStatus {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	runner.NewStatusHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var status runner.RunnerStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func assertHealth(t *testing.T, port int) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") || strings.Contains(err.Error(), "permission denied") {
			t.Skipf("loopback listen is not permitted in this sandbox: %v", err)
		}
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func testSigner(t *testing.T) *signing.LocalSigner {
	t.Helper()
	signer, err := signing.NewLocalSignerFromSeed("e2e-test", []byte(strings.Repeat("E", ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func verifierFor(t *testing.T, signer *signing.LocalSigner) *signing.LocalVerifier {
	t.Helper()
	verifier, err := signing.NewLocalVerifier(map[string]ed25519.PublicKey{signer.KeyID(): signer.PublicKey()})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

type testClock struct{}

func (testClock) Now() time.Time {
	return fixedNow()
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
}
