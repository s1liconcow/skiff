package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestReleaseListJSONReadsImmutableManifests(t *testing.T) {
	clearSkiffEnv(t)
	store := memory.New()
	createCLIReleaseManifest(t, store, "caddy-web", "rel_green", "2026-05-18T20:00:00Z")
	createCLIReleaseManifest(t, store, "caddy-web", "rel_blue", "2026-05-18T21:00:00Z")
	restoreStore := openReleaseObjectStore
	openReleaseObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	t.Cleanup(func() { openReleaseObjectStore = restoreStore })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"release", "list",
		"--service", "caddy-web",
		"--direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "memory://release-list",
		"--format", "json",
		"--trace-id", "tr_release_list",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out releaseListOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.TraceID != "tr_release_list" || out.Service != "caddy-web" {
		t.Fatalf("unexpected envelope: %+v", out)
	}
	if len(out.Releases) != 2 {
		t.Fatalf("expected 2 releases, got %+v", out.Releases)
	}
	if out.Releases[0].Release.ReleaseID != "rel_blue" || out.Releases[1].Release.ReleaseID != "rel_green" {
		t.Fatalf("unexpected release order: %+v", out.Releases)
	}
}

func createCLIReleaseManifest(t *testing.T, store objstore.ObjectStore, service, releaseID, createdAt string) {
	t.Helper()
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion:         schema.Version,
		Service:               service,
		Env:                   "prod",
		ReleaseID:             releaseID,
		Artifact:              schema.ArtifactRef{Type: "tarball", URI: "file:///tmp/" + releaseID + ".tar.gz", Digest: "sha256:" + releaseID},
		RuntimeManifestKey:    runtimeKey,
		RuntimeManifestDigest: "sha256:runtime-" + releaseID,
		CreatedAt:             createdAt,
	}
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	createCLIJSON(t, store, key, manifest)
}
