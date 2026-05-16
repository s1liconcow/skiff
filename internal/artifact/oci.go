package artifact

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type CommandOCIPuller struct {
	Tool string
}

func (p Preparer) prepareOCI(ctx context.Context, req Request, dest string) error {
	if digestFromOCIURI(req.Artifact.URI) == "" {
		return fmt.Errorf("%w: OCI artifacts must be pinned with @sha256:", ErrInvalidArtifact)
	}
	puller := p.OCIPuller
	if puller == nil {
		return fmt.Errorf("%w: OCI artifact preparation requires an OCI puller", ErrInvalidArtifact)
	}
	return puller.PullOCI(ctx, strings.TrimPrefix(req.Artifact.URI, "oci://"), dest)
}

func (p CommandOCIPuller) PullOCI(ctx context.Context, ref, dest string) error {
	tool := p.Tool
	if tool == "" {
		tool = "skopeo"
	}
	cmd := exec.CommandContext(ctx, tool, "copy", "docker://"+ref, "oci:"+dest+":latest")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func digestFromOCIURI(uri string) string {
	before, digest, ok := strings.Cut(uri, "@sha256:")
	if !ok || before == "" || len(digest) < 64 {
		return ""
	}
	digest = digest[:64]
	for _, c := range digest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return "sha256:" + digest
}
