package artifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	TypeBinary  = "binary"
	TypeTarball = "tarball"
	TypeOCI     = "oci"

	DefaultRootDir = "/opt/skiff/workloads"
	markerName     = ".skiff-artifact.json"
)

var (
	ErrInvalidArtifact = errors.New("artifact: invalid artifact")
	ErrDigestMismatch  = errors.New("artifact: digest mismatch")
	ErrUnsafeArchive   = errors.New("artifact: unsafe archive")
)

type Fetcher interface {
	Fetch(ctx context.Context, uri string) ([]byte, error)
}

type OCIPuller interface {
	PullOCI(ctx context.Context, ref, dest string) error
}

type Preparer struct {
	RootDir   string
	Fetcher   Fetcher
	OCIPuller OCIPuller
}

type Request struct {
	Service         string
	Env             string
	ReleaseID       string
	Artifact        schema.ArtifactRef
	RuntimeManifest schema.RuntimeManifest
}

type Result struct {
	ReleaseDir       string            `json:"release_dir"`
	CurrentLink      string            `json:"current_link"`
	WorkingDirectory string            `json:"working_directory"`
	Command          []string          `json:"command"`
	EnvVars          map[string]string `json:"env_vars,omitempty"`
}

type marker struct {
	SchemaVersion string             `json:"schema_version"`
	Service       string             `json:"service"`
	Env           string             `json:"env"`
	ReleaseID     string             `json:"release_id"`
	Artifact      schema.ArtifactRef `json:"artifact"`
}

func (p Preparer) Prepare(ctx context.Context, req Request) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := p.RootDir
	if root == "" {
		root = DefaultRootDir
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	releaseDir := ReleaseDir(root, req.Service, req.ReleaseID)
	if ok, result, err := p.existingResult(ctx, req, root, releaseDir); ok || err != nil {
		return result, err
	}

	parent := filepath.Dir(releaseDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(parent, "."+filepath.Base(releaseDir)+".tmp-")
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tempDir)
		}
	}()

	switch req.Artifact.Type {
	case TypeBinary:
		err = p.prepareBinary(ctx, req, tempDir)
	case TypeTarball:
		err = p.prepareTarball(ctx, req, tempDir)
	case TypeOCI:
		err = p.prepareOCI(ctx, req, tempDir)
	default:
		err = fmt.Errorf("%w: unsupported artifact type %q", ErrInvalidArtifact, req.Artifact.Type)
	}
	if err != nil {
		return nil, err
	}
	if _, err := resolveCommand(req.Artifact.Type, req.Artifact.URI, tempDir, req.RuntimeManifest.Command); err != nil {
		return nil, err
	}
	if err := writeMarker(tempDir, req); err != nil {
		return nil, err
	}
	if err := os.Rename(tempDir, releaseDir); err != nil {
		if _, statErr := os.Stat(releaseDir); statErr == nil {
			committed = true
			_ = os.RemoveAll(tempDir)
			return p.existingOrError(ctx, req, root, releaseDir)
		}
		return nil, err
	}
	committed = true
	return p.result(ctx, req, root, releaseDir)
}

func ReleaseDir(root, service, releaseID string) string {
	return filepath.Join(root, service, "releases", releaseID)
}

func CurrentLink(root, service string) string {
	return filepath.Join(root, service, "current")
}

func validateRequest(req Request) error {
	if err := paths.ValidateName("service", req.Service); err != nil {
		return err
	}
	if err := paths.ValidateName("env", req.Env); err != nil {
		return err
	}
	if err := paths.ValidateID("release", req.ReleaseID); err != nil {
		return err
	}
	if req.RuntimeManifest.Service != "" && req.RuntimeManifest.Service != req.Service {
		return fmt.Errorf("%w: runtime manifest service %q does not match %q", ErrInvalidArtifact, req.RuntimeManifest.Service, req.Service)
	}
	if req.RuntimeManifest.Env != "" && req.RuntimeManifest.Env != req.Env {
		return fmt.Errorf("%w: runtime manifest env %q does not match %q", ErrInvalidArtifact, req.RuntimeManifest.Env, req.Env)
	}
	if req.RuntimeManifest.ReleaseID != "" && req.RuntimeManifest.ReleaseID != req.ReleaseID {
		return fmt.Errorf("%w: runtime manifest release %q does not match %q", ErrInvalidArtifact, req.RuntimeManifest.ReleaseID, req.ReleaseID)
	}
	return ValidateReference(req.Env, req.Artifact)
}

func ValidateReference(env string, ref schema.ArtifactRef) error {
	ref.Type = strings.TrimSpace(ref.Type)
	ref.URI = strings.TrimSpace(ref.URI)
	if ref.Type == "" {
		return fmt.Errorf("%w: artifact type is required", ErrInvalidArtifact)
	}
	if ref.URI == "" {
		return fmt.Errorf("%w: artifact URI is required", ErrInvalidArtifact)
	}
	switch ref.Type {
	case TypeBinary, TypeTarball, TypeOCI:
	default:
		return fmt.Errorf("%w: unsupported artifact type %q", ErrInvalidArtifact, ref.Type)
	}
	if !security.IsSHA256Digest(ref.Digest) {
		return fmt.Errorf("%w: artifact digest must be sha256:<64 hex chars>", ErrInvalidArtifact)
	}
	if ref.Type == TypeOCI {
		uriDigest := digestFromOCIURI(ref.URI)
		if isProductionEnv(env) && uriDigest == "" {
			return fmt.Errorf("%w: production OCI artifacts must be pinned with @sha256:", ErrInvalidArtifact)
		}
		if uriDigest != "" && uriDigest != ref.Digest {
			return fmt.Errorf("%w: OCI URI digest %s does not match artifact digest %s", ErrInvalidArtifact, uriDigest, ref.Digest)
		}
	}
	return nil
}

func verifyDigest(body []byte, want string) error {
	if !security.IsSHA256Digest(want) {
		return fmt.Errorf("%w: artifact digest must be sha256:<64 hex chars>", ErrInvalidArtifact)
	}
	got := security.DigestBytes(body)
	if got != want {
		return fmt.Errorf("%w: got %s want %s", ErrDigestMismatch, got, want)
	}
	return nil
}

func (p Preparer) fetcher() Fetcher {
	if p.Fetcher != nil {
		return p.Fetcher
	}
	return FileFetcher{}
}

func (p Preparer) existingResult(ctx context.Context, req Request, root, releaseDir string) (bool, *Result, error) {
	if _, err := os.Stat(releaseDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil, nil
		}
		return false, nil, err
	}
	result, err := p.existingOrError(ctx, req, root, releaseDir)
	return true, result, err
}

func (p Preparer) existingOrError(ctx context.Context, req Request, root, releaseDir string) (*Result, error) {
	if err := readAndValidateMarker(releaseDir, req); err != nil {
		return nil, err
	}
	return p.result(ctx, req, root, releaseDir)
}

func (p Preparer) result(ctx context.Context, req Request, root, releaseDir string) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command, err := resolveCommand(req.Artifact.Type, req.Artifact.URI, releaseDir, req.RuntimeManifest.Command)
	if err != nil {
		return nil, err
	}
	current := CurrentLink(root, req.Service)
	if err := updateCurrentLink(current, releaseDir); err != nil {
		return nil, err
	}
	return &Result{
		ReleaseDir:       releaseDir,
		CurrentLink:      current,
		WorkingDirectory: releaseDir,
		Command:          command,
		EnvVars:          cloneStringMap(req.RuntimeManifest.EnvVars),
	}, nil
}

func writeMarker(releaseDir string, req Request) error {
	body, err := canonical.Marshal(marker{
		SchemaVersion: "skiff.artifact/v1",
		Service:       req.Service,
		Env:           req.Env,
		ReleaseID:     req.ReleaseID,
		Artifact:      req.Artifact,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(releaseDir, markerName), body, 0o644)
}

func readAndValidateMarker(releaseDir string, req Request) error {
	body, err := os.ReadFile(filepath.Join(releaseDir, markerName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: existing release directory %q is missing artifact marker", ErrInvalidArtifact, releaseDir)
		}
		return err
	}
	var got marker
	if err := canonical.UnmarshalStrict(body, &got); err != nil {
		return err
	}
	if got.SchemaVersion != "skiff.artifact/v1" ||
		got.Service != req.Service ||
		got.Env != req.Env ||
		got.ReleaseID != req.ReleaseID ||
		got.Artifact != req.Artifact {
		return fmt.Errorf("%w: existing release directory %q was prepared for a different artifact", ErrInvalidArtifact, releaseDir)
	}
	return nil
}

func resolveCommand(artifactType, uri, releaseDir string, command []string) ([]string, error) {
	out := append([]string(nil), command...)
	if len(out) == 0 {
		if artifactType != TypeBinary {
			return nil, fmt.Errorf("%w: runtime command is required for %s artifacts", ErrInvalidArtifact, artifactType)
		}
		out = []string{"./" + artifactFileName(uri)}
	}
	if err := validateCommandPath(releaseDir, out[0]); err != nil {
		return nil, err
	}
	return out, nil
}

func validateCommandPath(releaseDir, command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("%w: command path is required", ErrInvalidArtifact)
	}
	if filepath.IsAbs(command) {
		if err := ensureInside(releaseDir, command); err != nil {
			return err
		}
		_, err := os.Stat(command)
		return err
	}
	candidate := filepath.Join(releaseDir, command)
	if err := ensureInside(releaseDir, candidate); err != nil {
		return err
	}
	if _, err := os.Stat(candidate); err != nil {
		return fmt.Errorf("%w: command %q was not found in prepared artifact: %v", ErrInvalidArtifact, command, err)
	}
	return nil
}

func updateCurrentLink(current, releaseDir string) error {
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		return err
	}
	_ = os.Remove(current)
	target, err := filepath.Rel(filepath.Dir(current), releaseDir)
	if err != nil {
		target = releaseDir
	}
	return os.Symlink(target, current)
}

func ensureInside(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return fmt.Errorf("%w: path %q escapes %q", ErrUnsafeArchive, candidate, root)
	}
	return nil
}

func isProductionEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
