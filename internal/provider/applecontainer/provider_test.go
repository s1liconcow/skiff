package applecontainer

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/spec"
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

func TestStatefulPlanUsesStableAppleMemberAndVolumeIDs(t *testing.T) {
	t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", "31000")
	graph := compileAppleStatefulGraph(t)
	plan, err := New().Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Provider != Name || plan.Service != "ledger" || plan.Env != "prod" {
		t.Fatalf("unexpected plan header: %+v", plan)
	}
	var member provider.PlannedChange
	var volume provider.PlannedChange
	for _, change := range plan.Resources {
		switch {
		case change.Kind == ir.ResourceKindStatefulMember && change.ProviderID == "skiff-prod-ledger-m0-g1":
			member = change
		case change.Kind == ir.ResourceKindStatefulVolume && change.ProviderID == "skiff-prod-ledger-m0-data":
			volume = change
		}
	}
	if member.ProviderID == "" || volume.ProviderID == "" {
		t.Fatalf("plan missing stable member/volume IDs: %+v", plan.Resources)
	}
	var desired appleStatefulMemberDesired
	if err := canonical.UnmarshalStrict(member.Desired, &desired); err != nil {
		t.Fatalf("decode member desired: %v", err)
	}
	if desired.Ordinal != 0 || desired.MemberOrdinal != 0 || desired.HostPorts["admin"] != 31000 || desired.HostPorts["health"] != 31001 {
		t.Fatalf("unexpected member desired: %+v", desired)
	}
	if member.Tags[ir.TagStatefulGroup] != "ledger" || member.Tags[ir.TagMemberOrdinal] != "0" || member.Tags[ir.TagStatefulRecipe] != "tiny-stateful" {
		t.Fatalf("missing stateful tags: %+v", member.Tags)
	}
}

func TestApplyStatefulGroupStartsContainersAndPersistsRuntime(t *testing.T) {
	ctx := context.Background()
	t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", "31000")
	graph := compileAppleStatefulGraph(t)
	store := memory.New()
	container, calls := recordingContainerCLI(t)
	now := time.Date(2026, 5, 20, 1, 0, 0, 0, time.UTC)
	p := New(WithStateStore(store), WithContainerCLI(container), WithClock(func() time.Time { return now }))
	plan, err := p.Plan(ctx, graph)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	result, err := p.ApplyStatefulGroup(ctx, graph, plan)
	if err != nil {
		t.Fatalf("apply stateful group: %v", err)
	}
	if len(result.ResourceIDs) != 4 {
		t.Fatalf("resource IDs = %+v", result.ResourceIDs)
	}
	body := string(mustRead(t, calls))
	for _, want := range []string{
		"volume create --opt size=1048576 skiff-prod-ledger-m0-data",
		"run --name skiff-prod-ledger-m0-g1 --detach --user 0 --entrypoint sh",
		"-p 127.0.0.1:31000:8081",
		"-p 127.0.0.1:31001:8080",
		"-e SKIFF_STATEFUL_MEMBER=0",
		"-v skiff-prod-ledger-m0-data:/data",
		"docker.io/library/busybox@sha256:" + strings.Repeat("a", 64),
		"-c sleep 3600",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("container calls missing %q:\n%s", want, body)
		}
	}
	runtimeKey, err := paths.StatefulProviderRuntime("ledger", Name)
	if err != nil {
		t.Fatal(err)
	}
	runtimeObj, err := store.Get(ctx, runtimeKey)
	if err != nil {
		t.Fatalf("runtime doc: %v", err)
	}
	var runtime appleStatefulRuntime
	if err := canonical.UnmarshalStrict(runtimeObj.Body, &runtime); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}
	if runtime.Group != "ledger" || len(runtime.Members) != 2 || runtime.Members[1].VolumeName != "skiff-prod-ledger-m1-data" {
		t.Fatalf("unexpected runtime: %+v", runtime)
	}
	resourceKey, err := paths.ProviderResource(Name, applePathSafe(ir.ResourceKindStatefulMember), "skiff-prod-ledger-m0-g1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, resourceKey); err != nil {
		t.Fatalf("member resource record: %v", err)
	}
}

func TestStatefulReplacementMovesAppleVolumeToNewContainer(t *testing.T) {
	ctx := context.Background()
	t.Setenv("SKIFF_APPLE_STATEFUL_PORT_BASE", "31000")
	graph := compileAppleStatefulGraph(t)
	store := memory.New()
	container, calls := recordingContainerCLI(t)
	p := New(WithStateStore(store), WithContainerCLI(container))
	plan, err := p.Plan(ctx, graph)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := p.ApplyStatefulGroup(ctx, graph, plan); err != nil {
		t.Fatalf("apply stateful group: %v", err)
	}
	if err := os.WriteFile(calls, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := provider.StatefulMemberRef{Group: "ledger", Env: "prod", Member: 1}
	if _, err := p.FenceInstance(ctx, provider.FenceInstanceRequest{Ref: ref, InstanceID: "skiff-prod-ledger-m1-g1"}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	if _, err := p.DetachVolume(ctx, provider.DetachVolumeRequest{Ref: ref, InstanceID: "skiff-prod-ledger-m1-g1", VolumeID: "skiff-prod-ledger-m1-data"}); err != nil {
		t.Fatalf("detach: %v", err)
	}
	launched, err := p.LaunchReplacement(ctx, provider.LaunchReplacementRequest{Ref: ref, PreviousID: "skiff-prod-ledger-m1-g1", VolumeID: "skiff-prod-ledger-m1-data", Generation: 2})
	if err != nil {
		t.Fatalf("launch replacement: %v", err)
	}
	if launched.InstanceID != "skiff-prod-ledger-m1-g2" {
		t.Fatalf("replacement ID = %q", launched.InstanceID)
	}
	if _, err := p.AttachVolume(ctx, provider.AttachVolumeRequest{Ref: ref, InstanceID: launched.InstanceID, VolumeID: "skiff-prod-ledger-m1-data"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := p.UpdateMemberDNS(ctx, provider.UpdateMemberDNSRequest{Ref: ref, DNSName: "ledger-1.local", InstanceID: launched.InstanceID}); err != nil {
		t.Fatalf("dns: %v", err)
	}
	body := string(mustRead(t, calls))
	for _, want := range []string{
		"stop --time 2 skiff-prod-ledger-m1-g1",
		"delete --force skiff-prod-ledger-m1-g1",
		"run --name skiff-prod-ledger-m1-g2 --detach --user 0 --entrypoint sh",
		"-v skiff-prod-ledger-m1-data:/data",
		"-c sleep 3600",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("replacement calls missing %q:\n%s", want, body)
		}
	}
	runtime, err := p.loadStatefulRuntime(ctx, "ledger")
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	member, _, ok := runtime.member(1)
	if !ok || member.ContainerName != "skiff-prod-ledger-m1-g2" || member.VolumeName != "skiff-prod-ledger-m1-data" {
		t.Fatalf("runtime did not move member volume: %+v", runtime)
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

func compileAppleStatefulGraph(t *testing.T) *ir.Graph {
	t.Helper()
	doc, result, err := spec.Parse([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: ledger
  env: prod
stateful:
  replicas: 2
  members:
    - ordinal: 0
      dnsName: ledger-0.local
    - ordinal: 1
      dnsName: ledger-1.local
  volume:
    size: 1Mi
    mountPath: /data
    encrypted: true
  recipe:
    name: tiny-stateful
    config:
      artifact:
        type: oci
        ref: docker.io/library/busybox@sha256:`+strings.Repeat("a", 64)+`
      runtime:
        command:
          - sh
          - -c
          - sleep 3600
        ports:
          health: 8080
          admin: 8081
        health:
          path: /healthz
          port: 8080
  update:
    strategy: ordered
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if !result.OK {
		t.Fatalf("spec invalid: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile graph: %v", err)
	}
	return graph
}

func recordingContainerCLI(t *testing.T) (string, string) {
	t.Helper()
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
	return container, calls
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
