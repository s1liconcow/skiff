package applecontainer

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestLogsReadAppleContainerOutput(t *testing.T) {
	dir := t.TempDir()
	container := filepath.Join(dir, "container")
	if err := os.WriteFile(container, []byte(`#!/usr/bin/env bash
if [[ "$1" != "logs" || "$2" != "skiff-demo-caddy" ]]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
printf '%s\n' '2026-05-18T01:25:40.044109Z caddy serving request'
printf '%s\n' 'plain caddy line'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 18, 1, 26, 0, 0, time.UTC)
	logs, err := New(
		WithContainerCLI(container),
		WithCaddyContainer("skiff-demo-caddy"),
		WithClock(func() time.Time { return now }),
	).Logs(context.Background(), provider.LogsRequest{
		Service: "caddy-web",
		Env:     "prod",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(logs.Entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(logs.Entries), logs.Entries)
	}
	if logs.Entries[0].Source != "skiff-demo-caddy" || logs.Entries[0].Timestamp.Format(time.RFC3339Nano) != "2026-05-18T01:25:40.044109Z" || logs.Entries[0].Message != "caddy serving request" {
		t.Fatalf("unexpected parsed entry: %+v", logs.Entries[0])
	}
	if logs.Entries[1].Timestamp != now || logs.Entries[1].Message != "plain caddy line" {
		t.Fatalf("unexpected fallback entry: %+v", logs.Entries[1])
	}
}

func TestLogsRequireGeneratedDemoContainer(t *testing.T) {
	_, err := New(WithCaddyContainer("")).Logs(context.Background(), provider.LogsRequest{Service: "caddy-web", Env: "prod"})
	if err == nil || !strings.Contains(err.Error(), "SKIFF_APPLE_CADDY_CONTAINER") {
		t.Fatalf("logs err = %v, want generated env hint", err)
	}
}

func TestInspectResourceReportsUnhealthyWhenDemoCaddyHealthCannotBeRead(t *testing.T) {
	inspection, err := New(
		WithCaddyContainer("skiff-demo-caddy"),
		WithCaddyURL("://bad-url"),
	).InspectResource(context.Background(), provider.ResourceRef{Service: "caddy-web", Env: "prod", Kind: "target-group"})
	if err != nil {
		t.Fatalf("inspect resource: %v", err)
	}
	if inspection.Status != "unhealthy" || inspection.ProviderID != "skiff-demo-caddy" {
		t.Fatalf("unexpected inspection: %+v", inspection)
	}
}

func TestInspectResourceUsesStoredFakeHealthForNonTarballRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	service := "caddy-web"
	env := "prod"
	releaseID := "rel_oci"
	createDemoServiceControl(t, ctx, store, service, env, releaseID)
	createDemoReleaseWithArtifact(t, ctx, store, service, env, releaseID, schema.ArtifactRef{
		Type:   "oci",
		URI:    "oci://docker.io/library/caddy@sha256:" + strings.Repeat("a", 64),
		Digest: "sha256:" + strings.Repeat("a", 64),
	}, schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       service,
		Env:           env,
		ReleaseID:     releaseID,
		Command:       []string{"./skiff-container-artifact-ready"},
		HealthCheck:   &schema.HealthCheck{Type: "http", Path: "/", Port: 80},
		CreatedAt:     canonical.Time(time.Date(2026, 5, 18, 1, 0, 0, 0, time.UTC)),
	})
	createDemoFakeTargetRecord(t, ctx, store, service, env, "fake-target-group-caddy-web")

	inspection, err := New(
		WithStateStore(store),
		WithCaddyContainer("skiff-demo-caddy"),
		WithCaddyURL("://bad-url"),
	).InspectResource(ctx, provider.ResourceRef{Service: service, Env: env, Kind: "target-group"})
	if err != nil {
		t.Fatalf("inspect resource: %v", err)
	}
	if inspection.Status != "configured" || inspection.ProviderID != "fake-target-group-caddy-web" {
		t.Fatalf("unexpected inspection: %+v", inspection)
	}
}

func TestStartRolloutRestartsCaddyForTarballRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	service := "caddy-web"
	env := "prod"
	releaseID := "rel_quickstart_green"
	tarball := writeDemoTarball(t, map[string]string{
		"index.html": "<h1>GREEN</h1>\n",
		"healthz":    "ok\n",
	})
	digest := security.DigestBytes(mustRead(t, tarball))
	runtime := schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       service,
		Env:           env,
		ReleaseID:     releaseID,
		Command:       []string{"./index.html"},
		HealthCheck:   &schema.HealthCheck{Type: "http", Path: "/healthz", Port: 80},
		CreatedAt:     canonical.Time(time.Date(2026, 5, 18, 1, 0, 0, 0, time.UTC)),
	}
	createDemoReleaseWithArtifact(t, ctx, store, service, env, releaseID, schema.ArtifactRef{Type: "tarball", URI: "file://" + tarball, Digest: digest}, runtime)

	dir := t.TempDir()
	calls := filepath.Join(dir, "calls.log")
	container := filepath.Join(dir, "container")
	if err := os.WriteFile(container, []byte(`#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SKIFF_TEST_CONTAINER_CALLS"
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKIFF_TEST_CONTAINER_CALLS", calls)

	rollout, err := New(
		WithStateStore(store),
		WithContainerCLI(container),
		WithCaddyContainer("skiff-demo-caddy"),
		WithCaddyImage("docker.io/library/caddy@sha256:test"),
		WithCaddyURL("http://127.0.0.1:18080"),
		WithWorkloadRoot(filepath.Join(dir, "workloads")),
	).StartRollout(ctx, provider.RolloutRequest{Service: service, Env: env, ReleaseID: releaseID, OperationID: "op_green"})
	if err != nil {
		t.Fatalf("start rollout: %v", err)
	}
	if rollout == nil || rollout.ID == "" {
		t.Fatalf("missing rollout: %+v", rollout)
	}
	body := string(mustRead(t, calls))
	for _, want := range []string{
		"stop --time 2 skiff-demo-caddy",
		"delete --force skiff-demo-caddy",
		"run --name skiff-demo-caddy --detach -p 127.0.0.1:18080:80",
		"-v " + filepath.Join(dir, "workloads", service, "releases", releaseID) + ":/srv",
		"caddy file-server --root /srv --listen :80",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("container calls missing %q:\n%s", want, body)
		}
	}
}

func createDemoServiceControl(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, env, releaseID string) {
	t.Helper()
	control := schema.NewServiceControl(service, env, canonical.Time(time.Date(2026, 5, 18, 1, 0, 0, 0, time.UTC)), schema.Actor{ID: "test", Type: "agent"})
	control.DesiredRelease = releaseID
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatal(err)
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func createDemoReleaseWithArtifact(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, env, releaseID string, artifactRef schema.ArtifactRef, runtime schema.RuntimeManifest) {
	t.Helper()
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeBody, err := canonical.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, runtimeKey, runtimeBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
	releaseKey, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	release := schema.ReleaseManifest{
		SchemaVersion:      schema.Version,
		Service:            service,
		Env:                env,
		ReleaseID:          releaseID,
		Artifact:           artifactRef,
		RuntimeManifestKey: runtimeKey,
		CreatedAt:          canonical.Time(time.Date(2026, 5, 18, 1, 0, 0, 0, time.UTC)),
	}
	releaseBody, err := canonical.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, releaseKey, releaseBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func createDemoFakeTargetRecord(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, env, id string) {
	t.Helper()
	key, err := paths.ProviderResource(fakeprovider.Name, "target-group", id)
	if err != nil {
		t.Fatal(err)
	}
	record := schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "target-group", Name: "target-group:" + service},
		Provider:      schema.ResourceProviderRef{Provider: fakeprovider.Name, Kind: "target-group", ID: id},
		Service:       service,
		Env:           env,
		ObservedAt:    canonical.Time(time.Date(2026, 5, 18, 1, 0, 0, 0, time.UTC)),
	}
	body, err := canonical.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func writeDemoTarball(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "site.tar")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := tar.NewWriter(file)
	for name, body := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
