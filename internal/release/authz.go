package release

import (
	"context"

	"github.com/s1liconcow/skiff/internal/authz"
)

func (m Manager) authorize(ctx context.Context, req authz.Request) (authz.Decision, error) {
	return authz.MustAuthorize(ctx, m.Authorizer, req)
}
