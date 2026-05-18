package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/artifact"
	skiffaws "github.com/s1liconcow/skiff/internal/aws"
	"github.com/s1liconcow/skiff/internal/config"
	skiffevents "github.com/s1liconcow/skiff/internal/events"
	stateindex "github.com/s1liconcow/skiff/internal/index"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/s3"
	"github.com/s1liconcow/skiff/internal/ops"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/skiffd"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const containerReadyCommand = "./skiff-container-artifact-ready"

const (
	appleDirectContext      = "local-apple-vms"
	appleSkiffdServeContext = "local-apple-skiffd-server"
	appleAPIContext         = "local-apple-skiffd"
)

func TestAppleContextArtifactsRenderFilledConfigAndEnv(t *testing.T) {
	resetSkiffEnv(t)
	report := newE2EReport(t, "apple-container", "caddy-web", "prod", "tr_apple_context")
	artifacts := writeAppleContextArtifacts(t, report, rustFSHarness{
		endpoint:  "http://127.0.0.1:19000",
		bucket:    "skiff-demo-bucket",
		accessKey: "local-access",
		secretKey: "local-secret",
	}, "s3://skiff-demo-bucket", appleContextOptions{APIURL: "http://127.0.0.1:18080", CaddyContainer: "skiff-demo-caddy", SkiffdPID: 12345, SkiffdLogPath: "/tmp/skiffd.log"})
	useAppleContext(t, artifacts, appleAPIContext)

	configBody, err := os.ReadFile(artifacts.configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	envBody, err := os.ReadFile(artifacts.envPath)
	if err != nil {
		t.Fatalf("read generated env: %v", err)
	}
	for _, want := range []string{
		"name: local-apple-vms",
		"name: local-apple-skiffd-server",
		"name: local-apple-skiffd",
		"state: \"s3://skiff-demo-bucket\"",
		"apiURL: \"http://127.0.0.1:18080\"",
	} {
		if !strings.Contains(string(configBody), want) {
			t.Fatalf("generated config missing %q:\n%s", want, string(configBody))
		}
	}
	for _, want := range []string{
		"export SKIFF_AWS_ENDPOINT='http://127.0.0.1:19000'",
		"export SKIFF_APPLE_CADDY_CONTAINER='skiff-demo-caddy'",
		"export SKIFF_APPLE_SKIFFD_PID='12345'",
		"export SKIFF_CONFIG='" + artifacts.configPath + "'",
		"export SKIFF_CONTEXT='local-apple-vms'",
	} {
		if !strings.Contains(string(envBody), want) {
			t.Fatalf("generated env missing %q:\n%s", want, string(envBody))
		}
	}
	if report.ConfigPath != artifacts.configPath || report.EnvPath != artifacts.envPath || report.APIContext != appleAPIContext {
		t.Fatalf("report did not record context artifacts: %+v", report)
	}
}

func TestAppleContainerRustFSCaddyRollout(t *testing.T) {
	resetSkiffEnv(t)
	if !appleContainerE2EEnabled() {
		t.Skip("set SKIFF_APPLE_CONTAINER_E2E=1 to run the Apple container/RustFS/Caddy e2e")
	}
	containerPath, err := exec.LookPath("container")
	if err != nil {
		t.Skip("Apple container CLI is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	firstImage := pinnedImageForE2E(t, ctx, "SKIFF_E2E_CADDY_IMAGE", "docker.io/library/caddy:2-alpine")
	secondImage := strings.TrimSpace(os.Getenv("SKIFF_E2E_CADDY_NEXT_IMAGE"))
	if secondImage == "" {
		secondImage = firstImage
	} else {
		secondImage = pinnedImageForE2E(t, ctx, "SKIFF_E2E_CADDY_NEXT_IMAGE", secondImage)
	}

	cli := appleContainerCLI{path: containerPath}
	persist := appleContainerPersistEnabled()
	runID := fmt.Sprintf("skiff-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	rustfsPort := freePort(t)
	caddyPort := freePort(t)
	rustfs := startRustFSContainer(t, ctx, cli, runID, rustfsPort, persist)
	configureRustFSEnv(t, rustfs)

	store := rustfsObjectStore(t, ctx, rustfs)
	statePath := filepath.Join(t.TempDir(), "runner-state.json")
	rootDir := filepath.Join(t.TempDir(), "workloads")
	service := "caddy-web"
	env := "prod"
	stateURI := "s3://" + rustfs.bucket
	traceID := "tr_apple_container_e2e"
	firstOperationID := "op_apple_container_01"
	secondOperationID := "op_apple_container_02"
	canaryOperationID := "op_apple_container_canary"
	releaseID := "rel_01JAPPLECADDY01"
	nextReleaseID := "rel_01JAPPLECADDY02"
	canaryReleaseID := "rel_01JAPPLECADDY03"
	signer := testSigner(t)
	verifier := verifierFor(t, signer)
	actor := schema.Actor{ID: "e2e", Type: "agent"}
	report := newE2EReport(t, "apple-container", service, env, traceID)
	if persist {
		report.CleanupStatus = "Apple containers will be left running for demo inspection"
	} else {
		report.CleanupStatus = "Apple containers and RustFS volume registered with test cleanup"
	}
	defer writeE2EReport(t, report)

	firstRuntime := caddyRuntimeManifest(service, env, releaseID, caddyPort, "2026-05-18T00:00:00Z")
	firstArtifact := ociArtifact(t, firstImage)
	publishSignedRelease(t, ctx, store, firstArtifact, firstRuntime)
	controlKey := createDesiredServiceControl(t, ctx, store, service, env, releaseID)
	report.addObjectPath(controlKey)
	reportReleaseObjects(t, report, service, releaseID)
	createAppleResourceRecords(t, ctx, store, report, service, env, runID)
	createAppleOperation(t, ctx, store, report, service, env, firstOperationID, releaseID, schema.OperationRunning, traceID, runID+"-rollout-01")
	setDesiredReleaseForOperation(t, ctx, store, service, releaseID, "", firstOperationID, schema.OperationRunning, traceID, actor)

	events := &collectingRunnerSink{}
	objectEvents := &objectStateRunnerSink{
		store: store,
		inner: events,
		actor: actor,
		seed:  runID,
	}
	preparer := &appleContainerPreparer{
		inner: runner.WorkloadArtifactPreparer{
			RootDir:   rootDir,
			OCIPuller: appleContainerPuller{cli: cli},
		},
	}
	systemd := &appleContainerSystemd{
		cli:           cli,
		containerName: runID + "-caddy",
		hostPort:      caddyPort,
		preparer:      preparer,
	}
	if !persist {
		t.Cleanup(func() { systemd.cleanup(context.Background()) })
	}
	report.addProviderID(rustfs.name)
	report.addProviderID(rustfs.volume)
	report.addProviderID(systemd.containerName)
	contexts := writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{CaddyContainer: systemd.containerName})
	useAppleContext(t, contexts, appleDirectContext)

	bootstrap := runBootstrap(t, ctx, store, verifier, statePath, service, env, stateURI, controlKey, traceID, objectEvents)
	if bootstrap.ReleaseID != releaseID {
		t.Fatalf("bootstrap release = %q, want %q", bootstrap.ReleaseID, releaseID)
	}
	runCaddyLifecycle(t, ctx, firstRuntime, firstArtifact, statePath, objectEvents, systemd, preparer, traceID, &bootstrap.Identity)
	assertHTTPStatus(t, caddyPort, "/", http.StatusOK)
	completeAppleOperation(t, ctx, store, firstOperationID, service, releaseID, traceID)
	markStableRelease(t, ctx, store, service, releaseID, firstOperationID, traceID, actor)
	assertRunnerStatus(t, runner.FileStateStore{Path: statePath}, releaseID)
	assertRenderedUnit(t, systemd.unitBody, service, env, releaseID)
	assertReleaseObjectsAreImmutable(t, ctx, store, service, releaseID)

	secondRuntime := caddyRuntimeManifest(service, env, nextReleaseID, caddyPort, "2026-05-18T00:01:00Z")
	secondArtifact := ociArtifact(t, secondImage)
	publishSignedRelease(t, ctx, store, secondArtifact, secondRuntime)
	reportReleaseObjects(t, report, service, nextReleaseID)
	createAppleOperation(t, ctx, store, report, service, env, secondOperationID, nextReleaseID, schema.OperationRunning, traceID, runID+"-rollout-02")
	setDesiredReleaseForOperation(t, ctx, store, service, nextReleaseID, releaseID, secondOperationID, schema.OperationRunning, traceID, actor)

	nextBootstrap := runBootstrap(t, ctx, store, verifier, statePath, service, env, stateURI, controlKey, traceID, objectEvents)
	if nextBootstrap.ReleaseID != nextReleaseID {
		t.Fatalf("next bootstrap release = %q, want %q", nextBootstrap.ReleaseID, nextReleaseID)
	}
	nextLifecycle := runCaddyLifecycle(t, ctx, secondRuntime, secondArtifact, statePath, objectEvents, systemd, preparer, traceID, &nextBootstrap.Identity)
	if nextLifecycle.Status.ReleaseID != nextReleaseID {
		t.Fatalf("lifecycle release = %q, want %q", nextLifecycle.Status.ReleaseID, nextReleaseID)
	}
	assertHTTPStatus(t, caddyPort, "/", http.StatusOK)
	completeAppleOperation(t, ctx, store, secondOperationID, service, nextReleaseID, traceID)
	markStableRelease(t, ctx, store, service, nextReleaseID, secondOperationID, traceID, actor)
	assertRunnerStatus(t, runner.FileStateStore{Path: statePath}, nextReleaseID)
	assertRenderedUnit(t, systemd.unitBody, service, env, nextReleaseID)
	assertReleaseObjectsAreImmutable(t, ctx, store, service, nextReleaseID)
	assertStaleServiceControlCASRejected(t, ctx, store, service, traceID, actor)
	appendAppleAuditRecord(t, ctx, store, report, service, secondOperationID, traceID, actor)
	verifyReleaseViaCLI(t, ctx, store, report, service, env, nextReleaseID, signer, traceID)
	assertDirectStatusViaRustFS(t, report, stateURI, service, env, nextReleaseID, secondOperationID, traceID)
	assertDirectEventsViaRustFS(t, report, stateURI, service, env, traceID)
	assertDirectDoctorViaRustFS(t, report, stateURI, service, env, traceID)
	assertDirectOpsViaRustFS(t, report, stateURI, service, env, secondOperationID, traceID)

	localSkiffd := startLocalAppleSkiffd(t, ctx, store, report, rustfs, stateURI, env, traceID, systemd.containerName, persist)
	contexts = writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{APIURL: localSkiffd.url, CaddyContainer: systemd.containerName, SkiffdPID: report.SkiffdPID, SkiffdLogPath: report.SkiffdLogPath})
	useAppleContext(t, contexts, appleAPIContext)
	assertSkiffdStatusViaAPI(t, report, localSkiffd.url, stateURI, service, env, nextReleaseID, secondOperationID, traceID)
	createFakeCanaryResourceRecords(t, ctx, store, report, service, env)
	useAppleContext(t, contexts, appleDirectContext)
	canary := runRollingCanaryDeployViaDirectCLI(t, report, stateURI, service, env, secondImage, canaryReleaseID, canaryOperationID, traceID)
	report.addOperationID(canary.Result.OperationID)
	report.addSagaID(canary.Result.SagaID)
	reportReleaseObjects(t, report, service, canaryReleaseID)
	reportSagaObjects(t, report, canary.Result.SagaID)
	useAppleContext(t, contexts, appleAPIContext)
	assertSkiffdStatusViaAPI(t, report, localSkiffd.url, stateURI, service, env, canaryReleaseID, canaryOperationID, traceID)
	assertSkiffdDoctorViaAPI(t, report, localSkiffd.url, stateURI, service, env, traceID)
	assertSkiffdCanarySagaViaAPI(t, ctx, localSkiffd.url, canary.Result.SagaID, traceID)
	assertSkiffdSagaEventStream(t, ctx, localSkiffd.url, canary.Result.SagaID, traceID)

	assertRunnerEventCoverage(t, events.events, releaseID, nextReleaseID)
	report.fact("apple_container_e2e", "validated RustFS S3 object state, signed OCI releases, runner bootstrap/lifecycle, CAS control updates, immutable release objects, direct status/events/doctor/ops, local skiffd API monitoring, rolling canary saga, audit object, and release verify")
}

func appleContainerE2EEnabled() bool {
	return os.Getenv("SKIFF_APPLE_CONTAINER_E2E") == "1" || os.Getenv("SKIFF_CONTAINER_E2E") == "1"
}

func appleContainerPersistEnabled() bool {
	return os.Getenv("SKIFF_APPLE_CONTAINER_PERSIST") == "1" || os.Getenv("SKIFF_APPLE_CONTAINER_KEEPALIVE") == "1"
}

func pinnedImageForE2E(t *testing.T, ctx context.Context, name, fallback string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if value == "" {
		t.Fatalf("%s must name an OCI image", name)
	}
	value = strings.TrimPrefix(value, "oci://")
	if _, ok := digestFromImageRef(value); ok {
		return value
	}
	pinned, err := resolveRegistryDigest(ctx, value)
	if err != nil {
		t.Fatalf("%s=%q must be digest-pinned or resolvable from its registry: %v", name, value, err)
	}
	t.Logf("resolved %s=%s to %s", name, value, pinned)
	return pinned
}

func startRustFSContainer(t *testing.T, ctx context.Context, cli appleContainerCLI, runID string, port int, persist bool) rustFSHarness {
	t.Helper()
	image := envDefault("SKIFF_E2E_RUSTFS_IMAGE", "docker.io/rustfs/rustfs:latest")
	accessKey := envDefault("SKIFF_E2E_RUSTFS_ACCESS_KEY", "skiffe2eaccess")
	secretKey := envDefault("SKIFF_E2E_RUSTFS_SECRET_KEY", "skiffe2esecret")
	volume := runID + "-rustfs-data"
	name := runID + "-rustfs"

	if _, err := cli.run(ctx, "volume", "create", "--opt", "size=268435456", volume); err != nil {
		t.Fatalf("create RustFS volume: %v", err)
	}
	harness := rustFSHarness{
		cli:       cli,
		name:      name,
		volume:    volume,
		endpoint:  fmt.Sprintf("http://127.0.0.1:%d", port),
		bucket:    strings.ReplaceAll(runID, "_", "-"),
		accessKey: accessKey,
		secretKey: secretKey,
	}
	if !persist {
		t.Cleanup(func() { harness.cleanup(context.Background()) })
	}

	if _, err := cli.run(ctx,
		"run",
		"--name", name,
		"--detach",
		"--user", "root",
		"-p", fmt.Sprintf("127.0.0.1:%d:9000", port),
		"-e", "RUSTFS_ACCESS_KEY="+accessKey,
		"-e", "RUSTFS_SECRET_KEY="+secretKey,
		"-v", volume+":/data",
		image,
		"--address", ":9000",
		"--access-key", accessKey,
		"--secret-key", secretKey,
		"/data",
	); err != nil {
		t.Fatalf("start RustFS container: %v", err)
	}
	return harness
}

func rustfsObjectStore(t *testing.T, ctx context.Context, rustfs rustFSHarness) *s3store.Store {
	t.Helper()
	client, err := s3store.NewHTTPClient(s3store.HTTPClientOptions{
		Region:         "us-east-1",
		Endpoint:       rustfs.endpoint,
		ForcePathStyle: true,
		Credentials: skiffaws.Credentials{
			AccessKeyID:     rustfs.accessKey,
			SecretAccessKey: rustfs.secretKey,
		},
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	waitForRustFSBucket(t, ctx, rustfs, client, rustfs.bucket)
	store, err := s3store.New(s3store.Options{Bucket: rustfs.bucket, Client: client})
	if err != nil {
		t.Fatalf("create S3 store: %v", err)
	}
	return store
}

func configureRustFSEnv(t *testing.T, rustfs rustFSHarness) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", rustfs.accessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", rustfs.secretKey)
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	t.Setenv("SKIFF_AWS_ENDPOINT", rustfs.endpoint)
	t.Setenv("SKIFF_AWS_S3_PATH_STYLE", "true")
}

type appleContextArtifacts struct {
	configPath string
	envPath    string
}

type appleContextOptions struct {
	APIURL         string
	CaddyContainer string
	SkiffdPID      int
	SkiffdLogPath  string
}

func writeAppleContextArtifacts(t *testing.T, report *e2eReport, rustfs rustFSHarness, stateURI string, opts appleContextOptions) appleContextArtifacts {
	t.Helper()
	if report == nil {
		t.Fatal("report is required")
	}
	if err := os.MkdirAll(report.reportDir, 0o755); err != nil {
		t.Fatalf("create e2e report dir: %v", err)
	}
	configPath := filepath.Join(report.reportDir, report.RunID+".skiffconfig")
	envPath := filepath.Join(report.reportDir, report.RunID+".env")
	if err := os.WriteFile(configPath, []byte(appleSkiffConfig(stateURI, opts.APIURL)), 0o600); err != nil {
		t.Fatalf("write Apple Skiff config: %v", err)
	}
	if err := os.WriteFile(envPath, []byte(appleContextEnv(configPath, rustfs, opts)), 0o600); err != nil {
		t.Fatalf("write Apple context env: %v", err)
	}
	report.StateURI = stateURI
	report.APIURL = opts.APIURL
	report.ConfigPath = configPath
	report.EnvPath = envPath
	report.DirectContext = appleDirectContext
	if opts.APIURL != "" {
		report.APIContext = appleAPIContext
	}
	report.SkiffdPID = opts.SkiffdPID
	report.SkiffdLogPath = opts.SkiffdLogPath
	report.RecommendedNextCommands = []string{
		"source " + shellQuote(envPath),
		"skiff config get-contexts",
		"SKIFF_CONTEXT=" + appleDirectContext + " skiff status " + report.Service,
		"SKIFF_CONTEXT=" + appleDirectContext + " skiff events --scope service --service " + report.Service,
	}
	if opts.APIURL != "" {
		report.RecommendedNextCommands = append(report.RecommendedNextCommands,
			"SKIFF_CONTEXT="+appleAPIContext+" skiff tui "+report.Service+" --read-only",
			"make demo-apple-down",
		)
	}
	report.fact("skiff_context", "wrote "+configPath+" and "+envPath)
	return appleContextArtifacts{configPath: configPath, envPath: envPath}
}

func useAppleContext(t *testing.T, artifacts appleContextArtifacts, contextName string) {
	t.Helper()
	t.Setenv("SKIFF_CONFIG", artifacts.configPath)
	t.Setenv("SKIFF_CONTEXT", contextName)
}

func appleSkiffConfig(stateURI, apiURL string) string {
	body := fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: %s
contexts:
  - name: %s
    context:
      mode: direct
      env: prod
      provider: fake
      region: local
      state: %s
  - name: %s
    context:
      mode: skiffd
      env: prod
      provider: fake
      region: local
      state: %s
`, appleDirectContext, appleDirectContext, strconv.Quote(stateURI), appleSkiffdServeContext, strconv.Quote(stateURI))
	if apiURL != "" {
		body += fmt.Sprintf(`  - name: %s
    context:
      mode: api
      env: prod
      provider: fake
      region: local
      state: %s
      apiURL: %s
`, appleAPIContext, strconv.Quote(stateURI), strconv.Quote(apiURL))
	}
	return body
}

func appleContextEnv(configPath string, rustfs rustFSHarness, opts appleContextOptions) string {
	lines := []string{
		"export AWS_ACCESS_KEY_ID=" + shellQuote(rustfs.accessKey),
		"export AWS_SECRET_ACCESS_KEY=" + shellQuote(rustfs.secretKey),
		"export AWS_REGION=us-east-1",
		"export AWS_DEFAULT_REGION=us-east-1",
		"export SKIFF_AWS_ENDPOINT=" + shellQuote(rustfs.endpoint),
		"export SKIFF_AWS_S3_PATH_STYLE=true",
		"export SKIFF_CONFIG=" + shellQuote(configPath),
		"export SKIFF_CONTEXT=" + shellQuote(appleDirectContext),
		"export SKIFF_APPLE_RUSTFS_CONTAINER=" + shellQuote(rustfs.name),
		"export SKIFF_APPLE_RUSTFS_VOLUME=" + shellQuote(rustfs.volume),
	}
	if opts.CaddyContainer != "" {
		lines = append(lines, "export SKIFF_APPLE_CADDY_CONTAINER="+shellQuote(opts.CaddyContainer))
	}
	if opts.APIURL != "" {
		lines = append(lines, "export SKIFF_APPLE_SKIFFD_URL="+shellQuote(opts.APIURL))
	}
	if opts.SkiffdPID > 0 {
		lines = append(lines, "export SKIFF_APPLE_SKIFFD_PID="+shellQuote(strconv.Itoa(opts.SkiffdPID)))
	}
	if opts.SkiffdLogPath != "" {
		lines = append(lines, "export SKIFF_APPLE_SKIFFD_LOG="+shellQuote(opts.SkiffdLogPath))
	}
	return strings.Join(lines, "\n") + "\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func waitForRustFSBucket(t *testing.T, ctx context.Context, rustfs rustFSHarness, client *s3store.HTTPClient, bucket string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		err := client.CreateBucket(ctx, bucket)
		if err == nil || bucketAlreadyExists(err) {
			return
		}
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatalf("wait for RustFS bucket: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("RustFS did not accept bucket create for %q: %v\nlogs:\n%s", bucket, lastErr, rustfs.logs(context.Background()))
}

func bucketAlreadyExists(err error) bool {
	var apiErr *s3store.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusConflict ||
		apiErr.Code == "BucketAlreadyExists" ||
		apiErr.Code == "BucketAlreadyOwnedByYou"
}

func caddyRuntimeManifest(service, env, releaseID string, port int, createdAt string) schema.RuntimeManifest {
	return schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       service,
		Env:           env,
		ReleaseID:     releaseID,
		Command:       []string{containerReadyCommand},
		HealthCheck: &schema.HealthCheck{
			Type:     "http",
			Path:     "/",
			Port:     port,
			Interval: "250ms",
			Timeout:  "2s",
		},
		CreatedAt: createdAt,
	}
}

func ociArtifact(t *testing.T, image string) schema.ArtifactRef {
	t.Helper()
	image = strings.TrimPrefix(strings.TrimSpace(image), "oci://")
	digest, ok := digestFromImageRef(image)
	if !ok {
		t.Fatalf("image %q is not digest pinned", image)
	}
	return schema.ArtifactRef{
		Type:   artifact.TypeOCI,
		URI:    "oci://" + image,
		Digest: digest,
	}
}

func digestFromImageRef(image string) (string, bool) {
	_, digest, ok := strings.Cut(image, "@sha256:")
	if !ok || len(digest) < 64 {
		return "", false
	}
	hex := digest[:64]
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return "sha256:" + hex, true
}

func resolveRegistryDigest(ctx context.Context, image string) (string, error) {
	parsed, err := parseRegistryImage(image)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	digest, err := fetchManifestDigest(ctx, client, parsed, "")
	if err != nil {
		return "", err
	}
	return parsed.display + "@" + digest, nil
}

type registryImage struct {
	registryHost string
	registryAPI  string
	repository   string
	tag          string
	display      string
}

func parseRegistryImage(image string) (registryImage, error) {
	image = strings.TrimSpace(strings.TrimPrefix(image, "oci://"))
	if image == "" {
		return registryImage{}, errors.New("image reference is required")
	}
	if strings.Contains(image, "://") {
		return registryImage{}, fmt.Errorf("image reference %q must not include a URL scheme", image)
	}
	if strings.Contains(image, "@sha256:") {
		return registryImage{}, fmt.Errorf("image reference %q is already pinned", image)
	}
	name := image
	tag := "latest"
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		if colon := strings.LastIndex(name, ":"); colon > slash {
			tag = name[colon+1:]
			name = name[:colon]
		}
	} else if colon := strings.LastIndex(name, ":"); colon >= 0 {
		tag = name[colon+1:]
		name = name[:colon]
	}
	parts := strings.Split(name, "/")
	registryHost := "docker.io"
	repository := name
	if len(parts) > 1 && isExplicitRegistry(parts[0]) {
		registryHost = parts[0]
		repository = strings.Join(parts[1:], "/")
	}
	if registryHost == "docker.io" && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	if repository == "" || tag == "" {
		return registryImage{}, fmt.Errorf("image reference %q is invalid", image)
	}
	registryAPI := registryHost
	if registryAPI == "docker.io" {
		registryAPI = "registry-1.docker.io"
	}
	return registryImage{
		registryHost: registryHost,
		registryAPI:  registryAPI,
		repository:   repository,
		tag:          tag,
		display:      registryHost + "/" + repository + ":" + tag,
	}, nil
}

func isExplicitRegistry(value string) bool {
	return value == "localhost" || strings.Contains(value, ".") || strings.Contains(value, ":")
}

func fetchManifestDigest(ctx context.Context, client *http.Client, image registryImage, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL(image), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && token == "" {
		nextToken, err := fetchBearerToken(ctx, client, resp.Header.Get("WWW-Authenticate"))
		if err != nil {
			return "", err
		}
		return fetchManifestDigest(ctx, client, image, nextToken)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("registry returned %d for %s", resp.StatusCode, manifestURL(image))
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if !isSHA256Digest(digest) {
		return "", fmt.Errorf("registry did not return a sha256 Docker-Content-Digest for %s", image.display)
	}
	return digest, nil
}

func manifestURL(image registryImage) string {
	u := url.URL{
		Scheme: "https",
		Host:   image.registryAPI,
		Path:   "/v2/" + image.repository + "/manifests/" + image.tag,
	}
	return u.String()
}

func fetchBearerToken(ctx context.Context, client *http.Client, challenge string) (string, error) {
	params, err := parseBearerChallenge(challenge)
	if err != nil {
		return "", err
	}
	realm := params.Get("realm")
	if realm == "" {
		return "", errors.New("registry auth challenge did not include a realm")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	query := tokenURL.Query()
	for _, key := range []string{"service", "scope"} {
		if value := params.Get(key); value != "" {
			query.Set(key, value)
		}
	}
	tokenURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("registry token endpoint returned %d", resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", errors.New("registry token response did not include a token")
}

func parseBearerChallenge(challenge string) (url.Values, error) {
	challenge = strings.TrimSpace(challenge)
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return nil, fmt.Errorf("unsupported registry auth challenge %q", challenge)
	}
	challenge = strings.TrimSpace(challenge[len("Bearer "):])
	values := url.Values{}
	for _, part := range strings.Split(challenge, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		values.Set(strings.ToLower(strings.TrimSpace(key)), strings.Trim(strings.TrimSpace(value), `"`))
	}
	return values, nil
}

func isSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, c := range strings.TrimPrefix(value, "sha256:") {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func reportReleaseObjects(t *testing.T, report *e2eReport, service, releaseID string) {
	t.Helper()
	releaseKey, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	report.addObjectPath(releaseKey)
	report.addObjectPath(runtimeKey)
}

func reportSagaObjects(t *testing.T, report *e2eReport, sagaID string) {
	t.Helper()
	for _, keyFunc := range []func(string) (string, error){paths.SagaIntent, paths.SagaGraph, paths.SagaControl} {
		key, err := keyFunc(sagaID)
		if err != nil {
			t.Fatal(err)
		}
		report.addObjectPath(key)
	}
}

func createAppleResourceRecords(t *testing.T, ctx context.Context, store objstore.ObjectStore, report *e2eReport, service, env, runID string) {
	t.Helper()
	resources := []struct {
		kind string
		name string
		id   string
	}{
		{kind: "autoscaling-group", name: service + "-asg", id: runID + "-asg"},
		{kind: "target-group", name: service + "-tg", id: runID + "-tg"},
		{kind: "log-group", name: service + "-logs", id: runID + "-logs"},
		{kind: "metric-config", name: service + "-metrics", id: runID + "-metrics"},
	}
	for _, resource := range resources {
		key, err := paths.LogicalResource(resource.kind, resource.name)
		if err != nil {
			t.Fatal(err)
		}
		record := schema.ResourceRecord{
			SchemaVersion: schema.Version,
			Logical:       schema.ResourceLogicalRef{Kind: resource.kind, Name: resource.name},
			Provider:      schema.ResourceProviderRef{Provider: "apple-container", Kind: resource.kind, ID: resource.id},
			Service:       service,
			Env:           env,
			Ownership:     &schema.ResourceOwnership{Mode: "managed", ManagedBy: "skiff-e2e"},
			Tags: map[string]string{
				"skiff.dev/service": service,
				"skiff.dev/env":     env,
				"skiff.dev/managed": "true",
				"skiff.dev/graph":   "apple-container-e2e",
			},
			ObservedAt: canonical.Time(fixedNow()),
		}
		createJSON(t, ctx, store, key, record)
		report.addObjectPath(key)
		report.addProviderID(resource.id)
	}
}

func createFakeCanaryResourceRecords(t *testing.T, ctx context.Context, store objstore.ObjectStore, report *e2eReport, service, env string) {
	t.Helper()
	id := "fake-target-group-" + service
	record := schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "target-group", Name: "target-group:" + service},
		Provider:      schema.ResourceProviderRef{Provider: fakeprovider.Name, Kind: "target-group", ID: id},
		Service:       service,
		Env:           env,
		Ownership:     &schema.ResourceOwnership{Mode: "managed", ManagedBy: "skiff-e2e"},
		Tags: map[string]string{
			"skiff.dev/service": service,
			"skiff.dev/env":     env,
			"skiff.dev/managed": "true",
			"skiff.dev/graph":   "apple-container-canary-e2e",
		},
		ObservedAt: canonical.Time(fixedNow()),
	}
	key, err := paths.ProviderResource(fakeprovider.Name, "target-group", id)
	if err != nil {
		t.Fatal(err)
	}
	createJSON(t, ctx, store, key, record)
	report.addObjectPath(key)
	report.addProviderID(id)
}

func createAppleOperation(t *testing.T, ctx context.Context, store objstore.ObjectStore, report *e2eReport, service, env, operationID, releaseID string, status schema.OperationStatus, traceID, providerID string) {
	t.Helper()
	params, err := json.Marshal(map[string]string{"release_id": releaseID})
	if err != nil {
		t.Fatal(err)
	}
	intent := schema.NewOperationIntent(operationID, service, env, "deploy", schema.Target{Kind: "service", Name: service}, schema.Actor{ID: "e2e", Type: "agent"}, traceID, canonical.Time(fixedNow()))
	intent.Risk = schema.RiskLow
	intent.Reversibility = schema.Reversible
	intent.Summary = "apple container rollout to " + releaseID
	intent.Params = params
	intentKey, err := paths.OperationIntent(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	createJSON(t, ctx, store, intentKey, intent)
	report.addObjectPath(intentKey)

	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   operationID,
		Service:       service,
		Env:           env,
		Status:        status,
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider:    "apple-container",
			Kind:        "container-rollout",
			ID:          providerID,
			ObservedAt:  canonical.Time(fixedNow()),
			Description: "Apple container rollout for " + releaseID,
		}},
		UpdatedAt: canonical.Time(fixedNow()),
		TraceID:   traceID,
	}
	controlKey, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	createJSON(t, ctx, store, controlKey, control)
	report.addObjectPath(controlKey)
	report.addOperationID(operationID)
	report.addProviderID(providerID)
	appendOperationEvent(t, ctx, store, service, operationID, "operation.started", "operation started for "+releaseID, traceID)
}

func appendOperationEvent(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, operationID, eventType, summary, traceID string) {
	t.Helper()
	id := skiffevents.NewID(fixedNow(), operationID+eventType+summary)
	key, err := paths.OperationEvent(service, operationID, id)
	if err != nil {
		t.Fatal(err)
	}
	event := schema.Event{
		SchemaVersion: schema.Version,
		ID:            id,
		Time:          canonical.Time(fixedNow()),
		TraceID:       traceID,
		Subject:       schema.Target{Kind: "operation", Name: operationID},
		Type:          eventType,
		Severity:      "info",
		Actor:         &schema.Actor{ID: "e2e", Type: "agent"},
		Summary:       summary,
	}
	createJSON(t, ctx, store, key, event)
}

func setDesiredReleaseForOperation(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, releaseID, stableReleaseID, operationID string, status schema.OperationStatus, traceID string, actor schema.Actor) {
	t.Helper()
	client := state.NewClient(store, state.WithClock(testClock{}))
	current, err := client.GetServiceControl(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	next := current.Control
	next.DesiredRelease = releaseID
	next.StableRelease = stableReleaseID
	next.TraceID = traceID
	next.UpdatedBy = actor
	next.Operation = &schema.ActiveOperation{ID: operationID, Kind: "deploy", State: string(status), Step: "rollout"}
	if _, err := client.UpdateServiceControlCAS(ctx, current, next); err != nil {
		t.Fatal(err)
	}
}

func completeAppleOperation(t *testing.T, ctx context.Context, store objstore.ObjectStore, operationID, service, releaseID, traceID string) {
	t.Helper()
	opStore := ops.NewStore(store, ops.WithClock(fixedNow))
	current, err := opStore.GetControl(ctx, service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(map[string]string{"release_id": releaseID})
	if err != nil {
		t.Fatal(err)
	}
	next := current.Control
	next.Status = schema.OperationSucceeded
	next.TraceID = traceID
	next.StepResults = append(next.StepResults, schema.StepResultRef{
		StepID:      "runner-lifecycle",
		Kind:        "runner.lifecycle",
		Status:      "succeeded",
		Result:      result,
		CompletedAt: canonical.Time(fixedNow()),
	})
	if _, err := opStore.UpdateControlCAS(ctx, current, next); err != nil {
		t.Fatal(err)
	}
	appendOperationEvent(t, ctx, store, service, operationID, "operation.completed", "operation completed for "+releaseID, traceID)
}

func markStableRelease(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, releaseID, operationID, traceID string, actor schema.Actor) {
	t.Helper()
	setDesiredReleaseForOperation(t, ctx, store, service, releaseID, releaseID, operationID, schema.OperationSucceeded, traceID, actor)
}

func assertStaleServiceControlCASRejected(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, traceID string, actor schema.Actor) {
	t.Helper()
	client := state.NewClient(store, state.WithClock(testClock{}))
	current, err := client.GetServiceControl(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	next := current.Control
	next.TraceID = traceID + "_cas_probe"
	next.UpdatedBy = actor
	if _, err := client.UpdateServiceControlCAS(ctx, current, next); err != nil {
		t.Fatalf("service control CAS probe: %v", err)
	}
	stale := next
	stale.TraceID = traceID + "_stale"
	_, err = client.UpdateServiceControlCAS(ctx, current, stale)
	if !errors.Is(err, state.ErrPreconditionFailed) && !errors.Is(err, objstore.ErrPreconditionFailed) && !errors.Is(err, objstore.ErrConflict) {
		t.Fatalf("stale service control CAS error = %v, want precondition failure", err)
	}
}

func appendAppleAuditRecord(t *testing.T, ctx context.Context, store objstore.ObjectStore, report *e2eReport, service, operationID, traceID string, actor schema.Actor) {
	t.Helper()
	id := skiffevents.NewID(fixedNow(), traceID+operationID+"audit")
	key, err := paths.AuditEventForTime(fixedNow(), id)
	if err != nil {
		t.Fatal(err)
	}
	record := schema.AuditRecord{
		SchemaVersion: schema.Version,
		ID:            id,
		Time:          canonical.Time(fixedNow()),
		Actor:         actor,
		TraceID:       traceID,
		Target:        schema.Target{Kind: "service", Name: service},
		OperationID:   operationID,
		Risk:          schema.RiskLow,
		Summary:       "apple container rollout completed for " + service,
	}
	createJSON(t, ctx, store, key, record)
	report.addObjectPath(key)
}

func verifyReleaseViaCLI(t *testing.T, ctx context.Context, store objstore.ObjectStore, report *e2eReport, service, env, releaseID string, signer *signing.LocalSigner, traceID string) {
	t.Helper()
	releaseKey, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	releaseObj, err := store.Get(ctx, releaseKey)
	if err != nil {
		t.Fatal(err)
	}
	runtimeObj, err := store.Get(ctx, runtimeKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	releasePath := filepath.Join(dir, "release.json")
	runtimePath := filepath.Join(dir, "runtime-manifest.json")
	if err := os.WriteFile(releasePath, releaseObj.Body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, runtimeObj.Body, 0o644); err != nil {
		t.Fatal(err)
	}
	publicKeyArg := signer.KeyID() + "=" + base64.StdEncoding.EncodeToString(signer.PublicKey())
	var verified localVerifyOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"release", "verify", releasePath,
		"--runtime-manifest", runtimePath,
		"--public-key", publicKeyArg,
		"--service", service,
		"--env", env,
		"--format", "json",
		"--trace-id", traceID,
	), &verified)
	if !verified.OK || !verified.Result.OK {
		t.Fatalf("unexpected release verify output: %+v", verified)
	}
}

func runBootstrap(t *testing.T, ctx context.Context, store *s3store.Store, verifier *signing.LocalVerifier, statePath, service, env, stateURI, controlKey, traceID string, eventSink runner.EventSink) *runner.BootstrapResult {
	t.Helper()
	bootstrap, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config: config.Config{
			Mode:        config.ModeRunner,
			Env:         env,
			Provider:    "aws",
			Region:      "us-west-2",
			StateBucket: stateURI,
			Service:     service,
			ControlKey:  controlKey,
		},
		Store:    store,
		Verifier: verifier,
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-apple-container-e2e",
		}},
		StateStore: runner.FileStateStore{Path: statePath},
		EventSink:  eventSink,
		TraceID:    traceID,
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v (cause: %v)", err, errors.Unwrap(err))
	}
	if !bootstrap.Verification.OK {
		t.Fatalf("release verification failed: %+v", bootstrap.Verification)
	}
	return bootstrap
}

func runCaddyLifecycle(t *testing.T, ctx context.Context, runtimeManifest schema.RuntimeManifest, artifactRef schema.ArtifactRef, statePath string, eventSink runner.EventSink, systemd *appleContainerSystemd, preparer *appleContainerPreparer, traceID string, identity *runner.Identity) *runner.LifecycleResult {
	t.Helper()
	lifecycle, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest:  runtimeManifest,
		Artifact:         artifactRef,
		StateStore:       runner.FileStateStore{Path: statePath},
		EventSink:        eventSink,
		Systemd:          systemd,
		ArtifactPreparer: preparer,
		HealthChecker:    runner.ProbeHealthChecker{HTTPClient: &http.Client{Timeout: time.Second}},
		TraceID:          traceID,
		Identity:         identity,
		HealthAttempts:   80,
		HealthInterval:   250 * time.Millisecond,
		Now:              fixedNow,
	})
	if err != nil {
		t.Fatalf("RunLifecycle returned error: %v (cause: %v)", err, errors.Unwrap(err))
	}
	if lifecycle.Status.State != runner.StateServing || lifecycle.Status.Health != runner.HealthHealthy {
		t.Fatalf("unexpected lifecycle status: %+v", lifecycle.Status)
	}
	return lifecycle
}

func assertHTTPStatus(t *testing.T, port int, path string, want int) {
	t.Helper()
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + path)
	if err != nil {
		t.Fatalf("http request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("http status = %d, want %d", resp.StatusCode, want)
	}
}

func assertRunnerStatus(t *testing.T, store runner.StateStore, releaseID string) {
	t.Helper()
	status := readStatus(t, store)
	if !status.OK || status.State != runner.StateServing || status.Health != runner.HealthHealthy || status.ReleaseID != releaseID {
		t.Fatalf("unexpected runner status: %+v", status)
	}
	if status.Identity == nil || status.Identity.InstanceID != "i-apple-container-e2e" {
		t.Fatalf("runner identity not preserved in status: %+v", status.Identity)
	}
}

func assertRenderedUnit(t *testing.T, unitBody, service, env, releaseID string) {
	t.Helper()
	for _, want := range []string{
		"Description=Skiff workload " + env + "/" + service + " release " + releaseID,
		"ExecStart=",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"CapabilityBoundingSet=",
	} {
		if !strings.Contains(unitBody, want) {
			t.Fatalf("rendered unit missing %q:\n%s", want, unitBody)
		}
	}
}

func assertReleaseObjectsAreImmutable(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, releaseID string) {
	t.Helper()
	for _, keyFunc := range []func(string, string) (string, error){paths.ReleaseManifest, paths.RuntimeManifest} {
		key, err := keyFunc(service, releaseID)
		if err != nil {
			t.Fatal(err)
		}
		obj, err := store.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Create(ctx, key, obj.Body, objstore.PutOptions{ContentType: canonical.ContentType})
		if !errors.Is(err, objstore.ErrAlreadyExists) {
			t.Fatalf("create-only object %s duplicate err = %v, want already exists", key, err)
		}
	}
}

func assertDirectStatusViaRustFS(t *testing.T, report *e2eReport, stateURI, service, env, releaseID, operationID, traceID string) {
	t.Helper()
	var status appleStatusOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"status", service,
		"--format", "json",
		"--trace-id", traceID,
	), &status)
	if !status.OK || status.Status.Source != "direct" || status.Status.StateBucket != stateURI {
		t.Fatalf("unexpected direct status metadata: %+v", status)
	}
	if len(status.Status.Services) != 1 {
		t.Fatalf("status services = %d, want 1: %+v", len(status.Status.Services), status.Status.Services)
	}
	got := status.Status.Services[0]
	if got.Service != service || got.DesiredRelease != releaseID || got.StableRelease != releaseID || got.OperationID != operationID || got.OperationState != string(schema.OperationSucceeded) || got.Health != "nominal" {
		t.Fatalf("unexpected direct service status: %+v", got)
	}
	for _, kind := range []string{"autoscaling-group", "target-group", "log-group", "metric-config"} {
		if !statusHasResource(got.Resources, kind) {
			t.Fatalf("direct status missing resource kind %q: %+v", kind, got.Resources)
		}
	}
}

func assertDirectEventsViaRustFS(t *testing.T, report *e2eReport, stateURI, service, env, traceID string) {
	t.Helper()
	var listed appleEventsOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"events",
		"--scope", "service",
		"--service", service,
		"--format", "json",
		"--trace-id", traceID,
	), &listed)
	if !listed.OK || listed.Result.Source != "direct" || len(listed.Result.Events) == 0 {
		t.Fatalf("unexpected direct events output: %+v", listed)
	}
	if !hasSchemaEvent(listed.Result.Events, "workload.state", "Serving") {
		t.Fatalf("direct events did not include a Serving workload state: %+v", listed.Result.Events)
	}
	if !hasSchemaEvent(listed.Result.Events, "operation.completed", "") {
		t.Fatalf("direct events did not include operation completion: %+v", listed.Result.Events)
	}
}

func assertDirectDoctorViaRustFS(t *testing.T, report *e2eReport, stateURI, service, env, traceID string) {
	t.Helper()
	var doctor appleDoctorOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"doctor", service,
		"--format", "json",
		"--trace-id", traceID,
	), &doctor)
	if !doctor.OK || doctor.Doctor.Source != "direct" || doctor.Doctor.Service != service || doctor.Doctor.Health != "nominal" {
		t.Fatalf("unexpected direct doctor output: %+v", doctor)
	}
	for _, finding := range doctor.Doctor.Findings {
		if finding.Severity == "critical" {
			t.Fatalf("doctor returned critical finding: %+v", doctor.Doctor.Findings)
		}
	}
	if len(doctor.Doctor.Facts) == 0 || len(doctor.Doctor.RecommendedActions) != 0 {
		t.Fatalf("unexpected doctor facts/actions: %+v", doctor.Doctor)
	}
}

func assertDirectOpsViaRustFS(t *testing.T, report *e2eReport, stateURI, service, env, operationID, traceID string) {
	t.Helper()
	var list appleOpsListOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"ops", "list",
		"--all",
		"--service", service,
		"--format", "json",
		"--trace-id", traceID,
	), &list)
	if !list.OK || !hasOperationSummary(list.Operations, operationID, string(schema.OperationSucceeded)) {
		t.Fatalf("unexpected direct ops list output: %+v", list)
	}

	var inspect appleOpsInspectOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"ops", "inspect", operationID,
		"--service", service,
		"--format", "json",
		"--trace-id", traceID,
	), &inspect)
	if !inspect.OK || inspect.Result.OperationID != operationID || inspect.Result.Status != string(schema.OperationSucceeded) || inspect.Result.Risk != string(schema.RiskLow) || inspect.Result.Reversibility != string(schema.Reversible) || len(inspect.Result.ProviderOperations) == 0 || len(inspect.Result.StepResults) == 0 {
		t.Fatalf("unexpected direct ops inspect output: %+v", inspect)
	}
}

func startLocalSkiffd(t *testing.T, ctx context.Context, store objstore.ObjectStore, stateURI, env, traceID string) localSkiffdHarness {
	t.Helper()
	idx, err := stateindex.New(store, stateindex.Options{Clock: fixedNow})
	if err != nil {
		t.Fatalf("create skiffd index: %v", err)
	}
	if _, err := idx.Rebuild(ctx); err != nil {
		t.Fatalf("initial skiffd index rebuild: %v", err)
	}
	server, err := skiffd.New(skiffd.Options{
		Config: config.Config{
			Mode:        config.ModeAPI,
			Env:         env,
			Provider:    fakeprovider.Name,
			Region:      "local",
			StateBucket: stateURI,
		},
		ObjectStore: store,
		Index:       idx,
		Provider:    fakeprovider.New(fakeprovider.WithStateStore(store)),
		Clock:       fixedNow,
	})
	if err != nil {
		t.Fatalf("create skiffd server: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for local skiffd: %v", err)
	}
	serverCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(serverCtx, listener)
	}()
	harness := localSkiffdHarness{
		url:    "http://" + listener.Addr().String(),
		cancel: cancel,
		done:   done,
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("local skiffd stopped with error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("local skiffd did not stop")
		}
	})
	waitForSkiffdReady(t, ctx, harness.url, traceID)
	return harness
}

func startLocalAppleSkiffd(t *testing.T, ctx context.Context, store objstore.ObjectStore, report *e2eReport, rustfs rustFSHarness, stateURI, env, traceID, caddyContainer string, persist bool) localSkiffdHarness {
	t.Helper()
	if !persist {
		report.CleanupStatus = "Apple containers and in-process skiffd stopped when test exits"
		return startLocalSkiffd(t, ctx, store, stateURI, env, traceID)
	}
	return startPersistentAppleSkiffd(t, ctx, report, rustfs, stateURI, traceID, caddyContainer)
}

func startPersistentAppleSkiffd(t *testing.T, ctx context.Context, report *e2eReport, rustfs rustFSHarness, stateURI, traceID, caddyContainer string) localSkiffdHarness {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	apiURL := "http://" + addr
	artifacts := writeAppleContextArtifacts(t, report, rustfs, stateURI, appleContextOptions{
		APIURL:         apiURL,
		CaddyContainer: caddyContainer,
	})
	root := repoRootForTest(t)
	binPath := filepath.Join(report.reportDir, report.RunID+"-skiffd")
	build := exec.CommandContext(ctx, "go", "build", "-o", binPath, "./cmd/skiffd")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build persistent skiffd: %v\n%s", err, strings.TrimSpace(string(output)))
	}

	logPath := filepath.Join(report.reportDir, report.RunID+"-skiffd.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open persistent skiffd log: %v", err)
	}
	cmd := exec.Command(binPath,
		"serve",
		"--addr", addr,
		"--config", artifacts.configPath,
		"--context", appleSkiffdServeContext,
		"--format", "json",
		"--trace-id", traceID,
	)
	cmd.Dir = root
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start persistent skiffd: %v", err)
	}
	_ = logFile.Close()
	report.SkiffdPID = cmd.Process.Pid
	report.SkiffdLogPath = logPath
	report.CleanupStatus = "Apple containers and skiffd left running; stop them with make demo-apple-down"
	waitForSkiffdReady(t, ctx, apiURL, traceID)
	return localSkiffdHarness{url: apiURL}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}

func waitForSkiffdReady(t *testing.T, ctx context.Context, baseURL, traceID string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var lastBody string
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, skiffdEndpoint(t, baseURL, "/readyz", url.Values{"format": {"json"}, "trace_id": {traceID}}), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			lastBody = string(body)
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode == http.StatusOK {
				return
			} else {
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for local skiffd readiness: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("local skiffd did not become ready: %v\n%s", lastErr, lastBody)
}

func runRollingCanaryDeployViaDirectCLI(t *testing.T, report *e2eReport, stateURI, service, env, image, releaseID, operationID, traceID string) localCanaryOutput {
	t.Helper()
	specPath := writeAppleCanarySpec(t, service, env, image)
	var canary localCanaryOutput
	body := runSkiffCLI(t, report,
		"deploy", specPath,
		"--canary",
		"--canary-stages", "10,50,100",
		"--canary-bake", "0s",
		"--canary-metric", "request_count",
		"--canary-threshold", "1",
		"--release-id", releaseID,
		"--operation-id", operationID,
		"--key-id", "apple-canary",
		"--format", "json",
		"--trace-id", traceID,
	)
	decodeCLIJSON(t, body, &canary)
	if !canary.OK || canary.Result.SagaID == "" || canary.Result.OperationID != operationID || canary.Result.Status != string(schema.SagaSucceeded) {
		t.Fatalf("unexpected canary output: %+v\n%s", canary, string(body))
	}
	return canary
}

func writeAppleCanarySpec(t *testing.T, service, env, image string) string {
	t.Helper()
	image = strings.TrimPrefix(strings.TrimSpace(image), "oci://")
	if _, ok := digestFromImageRef(image); !ok {
		t.Fatalf("canary image %q is not digest pinned", image)
	}
	spec := fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: %s
  env: %s
artifact:
  type: oci
  ref: %s
runtime:
  port: 80
  command:
    - %s
  health:
    path: /
  logs:
    enabled: true
    format: json
  metrics:
    enabled: true
    path: /metrics
machine:
  size: small
scale:
  min: 2
  max: 2
network:
  ingress:
    type: private
`, strconv.Quote(service), strconv.Quote(env), strconv.Quote(image), strconv.Quote(containerReadyCommand))
	path := filepath.Join(t.TempDir(), "canary-skiff.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertSkiffdStatusViaAPI(t *testing.T, report *e2eReport, apiURL, stateURI, service, env, releaseID, operationID, traceID string) {
	t.Helper()
	var status appleStatusOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"status", service,
		"--fresh",
		"--format", "json",
		"--trace-id", traceID,
	), &status)
	if !status.OK || status.Status.Source != "api" || status.Status.StateBucket != stateURI {
		t.Fatalf("unexpected skiffd status metadata: %+v", status)
	}
	if len(status.Status.Services) != 1 {
		t.Fatalf("skiffd status services = %d, want 1: %+v", len(status.Status.Services), status.Status.Services)
	}
	got := status.Status.Services[0]
	if got.Service != service || got.DesiredRelease != releaseID || got.StableRelease != releaseID || got.OperationID != operationID || got.OperationState != string(schema.OperationSucceeded) || got.Health != "nominal" {
		t.Fatalf("unexpected skiffd service status: %+v", got)
	}
}

func assertSkiffdDoctorViaAPI(t *testing.T, report *e2eReport, apiURL, stateURI, service, env, traceID string) {
	t.Helper()
	var doctor appleDoctorOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"doctor", service,
		"--fresh",
		"--format", "json",
		"--trace-id", traceID,
	), &doctor)
	if !doctor.OK || doctor.Doctor.Source != "api" || doctor.Doctor.Service != service || doctor.Doctor.Health != "nominal" {
		t.Fatalf("unexpected skiffd doctor output: %+v", doctor)
	}
	for _, finding := range doctor.Doctor.Findings {
		if finding.Severity == "critical" {
			t.Fatalf("skiffd doctor returned critical finding: %+v", doctor.Doctor.Findings)
		}
	}
}

func assertSkiffdCanarySagaViaAPI(t *testing.T, ctx context.Context, apiURL, sagaID, traceID string) {
	t.Helper()
	var body struct {
		OK    bool `json:"ok"`
		Sagas []struct {
			SagaID string `json:"saga_id"`
			Status string `json:"status"`
		} `json:"sagas"`
	}
	getSkiffdJSON(t, ctx, apiURL, "/v1/sagas", traceID, url.Values{
		"format": {"json"},
		"fresh":  {"true"},
		"saga":   {sagaID},
	}, &body)
	if !body.OK || len(body.Sagas) != 1 || body.Sagas[0].SagaID != sagaID || body.Sagas[0].Status != string(schema.SagaSucceeded) {
		t.Fatalf("unexpected skiffd saga response: %+v", body)
	}
}

func assertSkiffdSagaEventStream(t *testing.T, ctx context.Context, apiURL, sagaID, traceID string) {
	t.Helper()
	endpoint := skiffdEndpoint(t, apiURL, "/v1/events/stream", url.Values{
		"scope": {"saga"},
		"saga":  {sagaID},
		"limit": {"50"},
		"once":  {"true"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Skiff-Trace-Id", traceID)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("read skiffd event stream: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read skiffd event stream body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("skiffd event stream HTTP %d:\n%s", resp.StatusCode, body)
	}
	text := string(body)
	if !strings.Contains(text, "event: skiff.event") || !strings.Contains(text, `"subject":{"kind":"saga","name":`+strconv.Quote(sagaID)) || !strings.Contains(text, `"type":"saga.step.succeeded"`) {
		t.Fatalf("skiffd event stream did not replay canary saga events:\n%s", text)
	}
}

func getSkiffdJSON(t *testing.T, ctx context.Context, apiURL, path, traceID string, query url.Values, out any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skiffdEndpoint(t, apiURL, path, query), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Skiff-Trace-Id", traceID)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("skiffd GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read skiffd GET %s: %v", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("skiffd GET %s returned HTTP %d:\n%s", path, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode skiffd GET %s: %v\n%s", path, err, body)
	}
}

func skiffdEndpoint(t *testing.T, baseURL, path string, query url.Values) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := parsed.ResolveReference(&url.URL{Path: path})
	values := endpoint.Query()
	for key, entries := range query {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	endpoint.RawQuery = values.Encode()
	return endpoint.String()
}

func assertRunnerEventCoverage(t *testing.T, events []runner.StateEvent, releases ...string) {
	t.Helper()
	if countRunnerState(events, runner.StateBooting, "") < len(releases) {
		t.Fatalf("runner events missing boot coverage: %+v", eventStates(events))
	}
	if countRunnerState(events, runner.StateFetchingManifest, "") < len(releases) {
		t.Fatalf("runner events missing manifest fetch coverage: %+v", eventStates(events))
	}
	required := []runner.State{
		runner.StateVerifyingRelease,
		runner.StatePreparingArtifact,
		runner.StateRenderingConfig,
		runner.StateStartingWorkload,
		runner.StateWaitingForHealth,
		runner.StateServing,
	}
	for _, releaseID := range releases {
		for _, state := range required {
			if countRunnerState(events, state, releaseID) == 0 {
				t.Fatalf("runner events missing state %s for release %s: %+v", state, releaseID, events)
			}
		}
	}
}

func countRunnerState(events []runner.StateEvent, state runner.State, releaseID string) int {
	count := 0
	for _, event := range events {
		if event.State != state {
			continue
		}
		if releaseID != "" && event.ReleaseID != releaseID {
			continue
		}
		count++
	}
	return count
}

func statusHasResource(resources []appleStatusResource, kind string) bool {
	for _, resource := range resources {
		if resource.Kind == kind || resource.LogicalKind == kind {
			return true
		}
	}
	return false
}

func hasSchemaEvent(events []schema.Event, eventType, text string) bool {
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		if text == "" || strings.Contains(event.Summary, text) {
			return true
		}
		for _, fact := range event.Facts {
			if strings.Contains(fact.Message, text) {
				return true
			}
		}
	}
	return false
}

func hasOperationSummary(operations []appleOperationSummary, operationID, status string) bool {
	for _, operation := range operations {
		if operation.OperationID == operationID && operation.Status == status && len(operation.ProviderOperations) > 0 {
			return true
		}
	}
	return false
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

type appleStatusOutput struct {
	OK     bool `json:"ok"`
	Status struct {
		Source      string `json:"source"`
		StateBucket string `json:"state_bucket"`
		Services    []struct {
			Service        string                `json:"service"`
			DesiredRelease string                `json:"desired_release"`
			StableRelease  string                `json:"stable_release"`
			OperationID    string                `json:"operation_id"`
			OperationState string                `json:"operation_state"`
			Health         string                `json:"health"`
			Resources      []appleStatusResource `json:"resources"`
		} `json:"services"`
	} `json:"status"`
}

type appleStatusResource struct {
	Kind        string `json:"kind"`
	LogicalKind string `json:"logical_kind"`
	ProviderID  string `json:"provider_id"`
}

type appleEventsOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Source string         `json:"source"`
		Events []schema.Event `json:"events"`
	} `json:"result"`
}

type appleDoctorOutput struct {
	OK     bool `json:"ok"`
	Doctor struct {
		Service            string `json:"service"`
		Source             string `json:"source"`
		Health             string `json:"health"`
		Facts              []any  `json:"facts"`
		RecommendedActions []any  `json:"recommended_actions"`
		Findings           []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
		} `json:"findings"`
	} `json:"doctor"`
}

type appleOpsListOutput struct {
	OK         bool                    `json:"ok"`
	Operations []appleOperationSummary `json:"operations"`
}

type appleOperationSummary struct {
	OperationID        string                        `json:"operation_id"`
	Status             string                        `json:"status"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations"`
}

type appleOpsInspectOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OperationID        string                        `json:"operation_id"`
		Status             string                        `json:"status"`
		Risk               string                        `json:"risk"`
		Reversibility      string                        `json:"reversibility"`
		ProviderOperations []schema.ProviderOperationRef `json:"provider_operations"`
		StepResults        []schema.StepResultRef        `json:"step_results"`
	} `json:"result"`
}

type localSkiffdHarness struct {
	url    string
	cancel context.CancelFunc
	done   chan error
}

type rustFSHarness struct {
	cli       appleContainerCLI
	name      string
	volume    string
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
}

func (h rustFSHarness) cleanup(ctx context.Context) {
	_, _ = h.cli.run(ctx, "stop", "--time", "2", h.name)
	_, _ = h.cli.run(ctx, "delete", "--force", h.name)
	_, _ = h.cli.run(ctx, "volume", "delete", h.volume)
}

func (h rustFSHarness) logs(ctx context.Context) string {
	output, err := h.cli.run(ctx, "logs", h.name)
	if err != nil {
		if len(output) > 0 {
			return strings.TrimSpace(string(output)) + "\n" + err.Error()
		}
		return err.Error()
	}
	return strings.TrimSpace(string(output))
}

type objectStateRunnerSink struct {
	store objstore.ObjectStore
	inner runner.EventSink
	actor schema.Actor
	seed  string
	seq   int
}

func (s *objectStateRunnerSink) EmitRunnerEvent(ctx context.Context, event runner.StateEvent) error {
	if s.inner != nil {
		if err := s.inner.EmitRunnerEvent(ctx, event); err != nil {
			return err
		}
	}
	if s.store == nil || event.Service == "" {
		return nil
	}
	observedAt := fixedNow()
	if event.Time != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, event.Time); err == nil {
			observedAt = parsed
		}
	}
	s.seq++
	id := skiffevents.NewID(observedAt, fmt.Sprintf("%s:%06d:%s:%s:%s:%s:%s", s.seed, s.seq, event.Service, event.Env, event.ReleaseID, event.State, event.Summary))
	key, err := paths.ServiceEvent(event.Service, id)
	if err != nil {
		return err
	}
	summary := "workload state " + string(event.State)
	if event.ReleaseID != "" {
		summary += " for release " + event.ReleaseID
	}
	stateEvent := schema.Event{
		SchemaVersion: schema.Version,
		ID:            id,
		Time:          canonical.Time(observedAt),
		TraceID:       event.TraceID,
		Subject:       schema.Target{Kind: "service", Name: event.Service},
		Type:          "workload.state",
		Severity:      "info",
		Actor:         &s.actor,
		Summary:       summary,
		Facts: []schema.Fact{
			{Type: "state", Message: string(event.State)},
			{Type: "health", Message: string(event.Health)},
			{Type: "unit", Message: event.UnitName},
		},
	}
	if event.ReleaseID != "" {
		stateEvent.Facts = append(stateEvent.Facts, schema.Fact{Type: "release_id", Message: event.ReleaseID})
	}
	if event.Identity != nil && event.Identity.InstanceID != "" {
		stateEvent.Facts = append(stateEvent.Facts, schema.Fact{Type: "instance_id", Message: event.Identity.InstanceID})
	}
	body, err := canonical.Marshal(stateEvent)
	if err != nil {
		return err
	}
	_, err = s.store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

type appleContainerPreparer struct {
	inner    runner.WorkloadArtifactPreparer
	result   *runner.ArtifactResult
	artifact schema.ArtifactRef
}

func (p *appleContainerPreparer) PrepareArtifact(ctx context.Context, req runner.ArtifactRequest) (*runner.ArtifactResult, error) {
	result, err := p.inner.PrepareArtifact(ctx, req)
	if err != nil {
		return nil, err
	}
	p.result = result
	p.artifact = req.Artifact
	return result, nil
}

type appleContainerPuller struct {
	cli appleContainerCLI
}

func (p appleContainerPuller) PullOCI(ctx context.Context, ref, dest string) error {
	if _, err := p.cli.run(ctx, "image", "pull", "--progress", "none", ref); err != nil {
		return err
	}
	readyPath := filepath.Join(dest, strings.TrimPrefix(containerReadyCommand, "./"))
	return os.WriteFile(readyPath, []byte("#!/bin/sh\nexit 0\n"), 0o755)
}

type appleContainerSystemd struct {
	cli           appleContainerCLI
	containerName string
	hostPort      int
	preparer      *appleContainerPreparer
	unitBody      string
}

func (s *appleContainerSystemd) WriteUnit(ctx context.Context, name string, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.unitBody = string(contents)
	return nil
}

func (s *appleContainerSystemd) DaemonReload(ctx context.Context) error {
	return ctx.Err()
}

func (s *appleContainerSystemd) StartUnit(ctx context.Context, name string) error {
	return s.RestartUnit(ctx, name)
}

func (s *appleContainerSystemd) RestartUnit(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.preparer == nil || s.preparer.result == nil {
		return errors.New("artifact was not prepared")
	}
	ref := strings.TrimPrefix(s.preparer.artifact.URI, "oci://")
	if ref == "" {
		return errors.New("prepared OCI image reference is empty")
	}
	s.cleanup(ctx)
	_, err := s.cli.run(ctx,
		"run",
		"--name", s.containerName,
		"--detach",
		"-p", fmt.Sprintf("127.0.0.1:%d:80", s.hostPort),
		ref,
	)
	return err
}

func (s *appleContainerSystemd) StopUnit(ctx context.Context, name string) error {
	s.cleanup(ctx)
	return ctx.Err()
}

func (s *appleContainerSystemd) cleanup(ctx context.Context) {
	_, _ = s.cli.run(ctx, "stop", "--time", "2", s.containerName)
	_, _ = s.cli.run(ctx, "delete", "--force", s.containerName)
}

type appleContainerCLI struct {
	path string
}

func (c appleContainerCLI) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.path, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return output, err
		}
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
