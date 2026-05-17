package stateful

import (
	"context"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Recipe interface {
	Name() string
	Stop(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	Start(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	Health(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	Backup(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	Restore(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	DetectRole(ctx context.Context, req RecipeRequest) (*RoleResult, error)
}

type RecipeRequest struct {
	Group       string                       `json:"group"`
	Env         string                       `json:"env"`
	Member      int                          `json:"member"`
	Generation  int64                        `json:"generation"`
	InstanceID  string                       `json:"instance_id,omitempty"`
	VolumeID    string                       `json:"volume_id,omitempty"`
	DNSName     string                       `json:"dns_name,omitempty"`
	Control     schema.StatefulMemberControl `json:"control"`
	OperationID string                       `json:"operation_id,omitempty"`
	TraceID     string                       `json:"trace_id,omitempty"`
}

type RecipeResult struct {
	OK      bool              `json:"ok"`
	Summary string            `json:"summary,omitempty"`
	Facts   map[string]string `json:"facts,omitempty"`
}

type RoleResult struct {
	Role    string            `json:"role"`
	Primary bool              `json:"primary,omitempty"`
	Facts   map[string]string `json:"facts,omitempty"`
}
