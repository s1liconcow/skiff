package artifact

import (
	"context"
	"os"
	"path/filepath"
)

func (p Preparer) prepareBinary(ctx context.Context, req Request, dest string) error {
	body, err := p.fetcher().Fetch(ctx, req.Artifact.URI)
	if err != nil {
		return err
	}
	if err := verifyDigest(body, req.Artifact.Digest); err != nil {
		return err
	}
	name := artifactFileName(req.Artifact.URI)
	path := filepath.Join(dest, name)
	if err := os.WriteFile(path, body, 0o755); err != nil {
		return err
	}
	return nil
}

func artifactFileName(uri string) string {
	base := filepath.Base(uri)
	if base == "." || base == "/" || base == "" {
		return "artifact"
	}
	return base
}
