package artifact_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/artifact"
	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestBinaryArtifactPreparedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "payments-api")
	body := []byte("#!/bin/sh\necho ok\n")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	req := requestFixture(artifact.TypeBinary, src, security.DigestBytes(body))
	req.RuntimeManifest.Command = nil

	preparer := artifact.Preparer{RootDir: root}
	result, err := preparer.Prepare(ctx, req)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if result.WorkingDirectory != artifact.ReleaseDir(root, req.Service, req.ReleaseID) {
		t.Fatalf("working directory = %q", result.WorkingDirectory)
	}
	if len(result.Command) != 1 || result.Command[0] != "./payments-api" {
		t.Fatalf("command = %#v, want default binary command", result.Command)
	}
	info, err := os.Stat(filepath.Join(result.WorkingDirectory, "payments-api"))
	if err != nil {
		t.Fatalf("prepared binary missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("prepared binary is not executable: %s", info.Mode().Perm())
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.Prepare(ctx, req); err != nil {
		t.Fatalf("second Prepare returned error: %v", err)
	}
}

func TestTarballArtifactPreparedAndVerified(t *testing.T) {
	ctx := context.Background()
	tarball := tarGz(t, tarEntry{Name: "bin/server", Mode: 0o755, Body: "#!/bin/sh\necho ok\n"})
	src := writeFixture(t, "release.tar.gz", tarball)
	root := t.TempDir()
	req := requestFixture(artifact.TypeTarball, src, security.DigestBytes(tarball))
	req.RuntimeManifest.Command = []string{"./bin/server", "serve"}

	result, err := (artifact.Preparer{RootDir: root}).Prepare(ctx, req)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.WorkingDirectory, "bin", "server")); err != nil {
		t.Fatalf("prepared tarball file missing: %v", err)
	}
	if result.Command[0] != "./bin/server" || result.Command[1] != "serve" {
		t.Fatalf("command = %#v", result.Command)
	}
	if _, err := os.Lstat(result.CurrentLink); err != nil {
		t.Fatalf("current symlink missing: %v", err)
	}
}

func TestArtifactWrongDigestFails(t *testing.T) {
	body := []byte("real artifact")
	src := writeFixture(t, "app", body)
	req := requestFixture(artifact.TypeBinary, src, security.DigestBytes([]byte("different")))
	_, err := (artifact.Preparer{RootDir: t.TempDir()}).Prepare(context.Background(), req)
	if !errors.Is(err, artifact.ErrDigestMismatch) {
		t.Fatalf("Prepare error = %v, want digest mismatch", err)
	}
}

func TestTarballPathTraversalFailsSafely(t *testing.T) {
	tarball := tarGz(t, tarEntry{Name: "../escape", Mode: 0o644, Body: "bad"})
	src := writeFixture(t, "bad.tar.gz", tarball)
	req := requestFixture(artifact.TypeTarball, src, security.DigestBytes(tarball))
	req.RuntimeManifest.Command = []string{"./escape"}
	_, err := (artifact.Preparer{RootDir: t.TempDir()}).Prepare(context.Background(), req)
	if !errors.Is(err, artifact.ErrUnsafeArchive) {
		t.Fatalf("Prepare error = %v, want unsafe archive", err)
	}
}

func TestTarballMissingCommandFails(t *testing.T) {
	tarball := tarGz(t, tarEntry{Name: "bin/server", Mode: 0o755, Body: "#!/bin/sh\n"})
	src := writeFixture(t, "release.tar.gz", tarball)
	req := requestFixture(artifact.TypeTarball, src, security.DigestBytes(tarball))
	req.RuntimeManifest.Command = nil
	_, err := (artifact.Preparer{RootDir: t.TempDir()}).Prepare(context.Background(), req)
	if !errors.Is(err, artifact.ErrInvalidArtifact) {
		t.Fatalf("Prepare error = %v, want invalid artifact", err)
	}
}

func TestProductionMutableOCIRejected(t *testing.T) {
	req := requestFixture(artifact.TypeOCI, "oci://ghcr.io/acme/payments-api:latest", "sha256:"+strings.Repeat("a", 64))
	_, err := (artifact.Preparer{RootDir: t.TempDir()}).Prepare(context.Background(), req)
	if !errors.Is(err, artifact.ErrInvalidArtifact) || !strings.Contains(err.Error(), "@sha256") {
		t.Fatalf("Prepare error = %v, want mutable OCI rejection", err)
	}
}

func TestOCIArtifactPreparedWithInjectedPuller(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	req := requestFixture(artifact.TypeOCI, "oci://ghcr.io/acme/payments-api@sha256:"+strings.Repeat("a", 64), digest)
	req.RuntimeManifest.Command = []string{"./app"}
	result, err := (artifact.Preparer{
		RootDir:   t.TempDir(),
		OCIPuller: fakeOCIPuller{},
	}).Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.WorkingDirectory, "app")); err != nil {
		t.Fatalf("OCI puller output missing: %v", err)
	}
}

func requestFixture(artifactType, uri, digest string) artifact.Request {
	return artifact.Request{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_01JART",
		Artifact: schema.ArtifactRef{
			Type:   artifactType,
			URI:    uri,
			Digest: digest,
		},
		RuntimeManifest: schema.RuntimeManifest{
			SchemaVersion: schema.Version,
			Service:       "payments-api",
			Env:           "prod",
			ReleaseID:     "rel_01JART",
			Command:       []string{"./app"},
			EnvVars:       map[string]string{"PORT": "8080"},
			CreatedAt:     "2026-05-18T00:00:00Z",
		},
	}
}

type fakeOCIPuller struct{}

func (fakeOCIPuller) PullOCI(ctx context.Context, ref, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "app"), []byte("#!/bin/sh\n"), 0o755)
}

type tarEntry struct {
	Name string
	Mode int64
	Body string
}

func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		body := []byte(entry.Body)
		if err := tw.WriteHeader(&tar.Header{
			Name: entry.Name,
			Mode: entry.Mode,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFixture(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
