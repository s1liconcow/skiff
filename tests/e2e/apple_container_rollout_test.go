package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/s1liconcow/skiff/internal/objstore/s3"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const containerReadyCommand = "./skiff-container-artifact-ready"

func TestAppleContainerRustFSCaddyRollout(t *testing.T) {
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
	runID := fmt.Sprintf("skiff-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	rustfsPort := freePort(t)
	caddyPort := freePort(t)
	rustfs := startRustFSContainer(t, ctx, cli, runID, rustfsPort)

	store := rustfsObjectStore(t, ctx, rustfs)
	statePath := filepath.Join(t.TempDir(), "runner-state.json")
	rootDir := filepath.Join(t.TempDir(), "workloads")
	service := "caddy-web"
	env := "prod"
	traceID := "tr_apple_container_e2e"
	releaseID := "rel_01JAPPLECADDY01"
	nextReleaseID := "rel_01JAPPLECADDY02"
	signer := testSigner(t)
	verifier := verifierFor(t, signer)

	firstRuntime := caddyRuntimeManifest(service, env, releaseID, caddyPort, "2026-05-18T00:00:00Z")
	firstArtifact := ociArtifact(t, firstImage)
	publishSignedRelease(t, ctx, store, firstArtifact, firstRuntime)
	controlKey := createDesiredServiceControl(t, ctx, store, service, env, releaseID)

	events := &collectingRunnerSink{}
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
	t.Cleanup(func() { systemd.cleanup(context.Background()) })

	bootstrap := runBootstrap(t, ctx, store, verifier, statePath, service, env, controlKey, traceID)
	if bootstrap.ReleaseID != releaseID {
		t.Fatalf("bootstrap release = %q, want %q", bootstrap.ReleaseID, releaseID)
	}
	runCaddyLifecycle(t, ctx, firstRuntime, firstArtifact, statePath, events, systemd, preparer, traceID, &bootstrap.Identity)
	assertHTTPStatus(t, caddyPort, "/", http.StatusOK)

	secondRuntime := caddyRuntimeManifest(service, env, nextReleaseID, caddyPort, "2026-05-18T00:01:00Z")
	secondArtifact := ociArtifact(t, secondImage)
	publishSignedRelease(t, ctx, store, secondArtifact, secondRuntime)
	rollDesiredRelease(t, ctx, store, service, nextReleaseID, releaseID, traceID)

	nextBootstrap := runBootstrap(t, ctx, store, verifier, statePath, service, env, controlKey, traceID)
	if nextBootstrap.ReleaseID != nextReleaseID {
		t.Fatalf("next bootstrap release = %q, want %q", nextBootstrap.ReleaseID, nextReleaseID)
	}
	nextLifecycle := runCaddyLifecycle(t, ctx, secondRuntime, secondArtifact, statePath, events, systemd, preparer, traceID, &nextBootstrap.Identity)
	if nextLifecycle.Status.ReleaseID != nextReleaseID {
		t.Fatalf("lifecycle release = %q, want %q", nextLifecycle.Status.ReleaseID, nextReleaseID)
	}
	assertHTTPStatus(t, caddyPort, "/", http.StatusOK)
}

func appleContainerE2EEnabled() bool {
	return os.Getenv("SKIFF_APPLE_CONTAINER_E2E") == "1" || os.Getenv("SKIFF_CONTAINER_E2E") == "1"
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

func startRustFSContainer(t *testing.T, ctx context.Context, cli appleContainerCLI, runID string, port int) rustFSHarness {
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
	t.Cleanup(func() { harness.cleanup(context.Background()) })

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

func rollDesiredRelease(t *testing.T, ctx context.Context, store *s3store.Store, service, releaseID, stableReleaseID, traceID string) {
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
	next.UpdatedBy = schema.Actor{ID: "e2e", Type: "agent"}
	if _, err := client.UpdateServiceControlCAS(ctx, current, next); err != nil {
		t.Fatal(err)
	}
}

func runBootstrap(t *testing.T, ctx context.Context, store *s3store.Store, verifier *signing.LocalVerifier, statePath, service, env, controlKey, traceID string) *runner.BootstrapResult {
	t.Helper()
	bootstrap, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config: config.Config{
			Mode:        config.ModeRunner,
			Env:         env,
			Provider:    "aws",
			Region:      "us-west-2",
			StateBucket: "s3://skiff-e2e-rustfs",
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
		EventSink:  &collectingRunnerSink{},
		TraceID:    traceID,
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if !bootstrap.Verification.OK {
		t.Fatalf("release verification failed: %+v", bootstrap.Verification)
	}
	return bootstrap
}

func runCaddyLifecycle(t *testing.T, ctx context.Context, runtimeManifest schema.RuntimeManifest, artifactRef schema.ArtifactRef, statePath string, events *collectingRunnerSink, systemd *appleContainerSystemd, preparer *appleContainerPreparer, traceID string, identity *runner.Identity) *runner.LifecycleResult {
	t.Helper()
	lifecycle, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest:  runtimeManifest,
		Artifact:         artifactRef,
		StateStore:       runner.FileStateStore{Path: statePath},
		EventSink:        events,
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
		t.Fatalf("RunLifecycle returned error: %v", err)
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

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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
