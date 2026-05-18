package release

import (
	"context"
	"testing"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestListManifestsReturnsReleaseObjectsNewestFirst(t *testing.T) {
	store := memory.New()
	createReleaseManifest(t, store, "payments-api", "rel_green", "2026-05-18T20:00:00Z")
	createReleaseManifest(t, store, "payments-api", "rel_blue", "2026-05-18T21:00:00Z")
	createReleaseManifest(t, store, "orders-api", "rel_other", "2026-05-18T22:00:00Z")
	createRuntimeManifest(t, store, "payments-api", "rel_blue")

	docs, err := (Manager{Store: store}).ListManifests(context.Background(), ManifestListOptions{Service: "payments-api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 releases, got %d: %+v", len(docs), docs)
	}
	if docs[0].Manifest.ReleaseID != "rel_blue" || docs[1].Manifest.ReleaseID != "rel_green" {
		t.Fatalf("unexpected order: %+v", docs)
	}
}

func TestListManifestsAppliesLimit(t *testing.T) {
	store := memory.New()
	createReleaseManifest(t, store, "payments-api", "rel_green", "2026-05-18T20:00:00Z")
	createReleaseManifest(t, store, "payments-api", "rel_blue", "2026-05-18T21:00:00Z")

	docs, err := (Manager{Store: store}).ListManifests(context.Background(), ManifestListOptions{Service: "payments-api", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Manifest.ReleaseID != "rel_blue" {
		t.Fatalf("unexpected limited releases: %+v", docs)
	}
}

func createReleaseManifest(t *testing.T, store objstore.ObjectStore, service, releaseID, createdAt string) {
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
		Signatures:            []schema.Signature{{KeyID: "local-test", Algorithm: "ed25519", Signature: "sig", SignedAt: createdAt}},
	}
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	createReleaseJSON(t, store, key, manifest)
}

func createRuntimeManifest(t *testing.T, store objstore.ObjectStore, service, releaseID string) {
	t.Helper()
	manifest := schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       service,
		Env:           "prod",
		ReleaseID:     releaseID,
		CreatedAt:     "2026-05-18T21:00:00Z",
	}
	key, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	createReleaseJSON(t, store, key, manifest)
}

func createReleaseJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}
