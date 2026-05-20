package stateful

import (
	"context"
	"encoding/json"

	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
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

type PackageStepRecipe interface {
	PackageStepKinds() []string
	PlanPackageStep(ctx context.Context, req PackageStepRequest) (*PackageStepPlan, error)
	RunPackageStep(ctx context.Context, req PackageStepRequest) (*PackageStepResult, error)
	ResumePackageStep(ctx context.Context, req PackageStepRequest) (*PackageStepResult, error)
	CompensatePackageStep(ctx context.Context, req PackageStepRequest, result PackageStepResult) (*PackageStepResult, error)
	DoctorPackageStep(ctx context.Context, req PackageStepRequest) ([]PackageStepFinding, error)
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

type PackageStepRequest struct {
	Group           string                       `json:"group"`
	Env             string                       `json:"env,omitempty"`
	Kind            string                       `json:"kind"`
	SagaID          string                       `json:"saga_id,omitempty"`
	StepID          string                       `json:"step_id,omitempty"`
	OperationID     string                       `json:"operation_id,omitempty"`
	TraceID         string                       `json:"trace_id,omitempty"`
	Params          json.RawMessage              `json:"params,omitempty"`
	PreviousResults map[string]PackageStepResult `json:"previous_results,omitempty"`
}

type PackageStepPlan struct {
	Summary       string               `json:"summary,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
}

type PackageStepResult struct {
	Status             sagaapi.StepStatus            `json:"status"`
	Result             json.RawMessage               `json:"result,omitempty"`
	Failure            *schema.StepFailure           `json:"failure,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	Summary            string                        `json:"summary,omitempty"`
}

type PackageStepFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
}
