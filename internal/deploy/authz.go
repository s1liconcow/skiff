package deploy

import (
	"context"

	"github.com/s1liconcow/skiff/internal/authz"
)

func (d Deployer) authorize(ctx context.Context, req authz.Request) (authz.Decision, error) {
	return authz.MustAuthorize(ctx, d.Authorizer, req)
}
