package runner

import (
	"context"

	"github.com/s1liconcow/skiff/internal/artifact"
)

type WorkloadArtifactPreparer struct {
	RootDir   string
	Fetcher   artifact.Fetcher
	OCIPuller artifact.OCIPuller
}

func (p WorkloadArtifactPreparer) PrepareArtifact(ctx context.Context, req ArtifactRequest) (*ArtifactResult, error) {
	result, err := (artifact.Preparer{
		RootDir:   p.RootDir,
		Fetcher:   p.Fetcher,
		OCIPuller: p.OCIPuller,
	}).Prepare(ctx, artifact.Request{
		Service:         req.Service,
		Env:             req.Env,
		ReleaseID:       req.ReleaseID,
		Artifact:        req.Artifact,
		RuntimeManifest: req.RuntimeManifest,
	})
	if err != nil {
		return nil, err
	}
	return &ArtifactResult{
		Command:          result.Command,
		EnvVars:          result.EnvVars,
		WorkingDirectory: result.WorkingDirectory,
	}, nil
}
