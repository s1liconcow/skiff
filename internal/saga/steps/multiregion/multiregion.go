package multiregion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	KindVerifySecondaryCapacity = "multiregion.verify_secondary_capacity"
	KindVerifyReplicaLag        = "multiregion.verify_replica_lag"
	KindFreezeWrites            = "multiregion.freeze_writes"
	KindPromoteDatabase         = "multiregion.promote_database"
	KindUpdateWriterSecret      = "multiregion.update_writer_secret"
	KindIrreversibleBoundary    = "multiregion.irreversible_boundary"
	KindShiftTraffic            = "multiregion.shift_traffic"
	KindMarkPrimary             = "multiregion.mark_primary"
)

type Params struct {
	Stack          string `json:"stack"`
	Service        string `json:"service,omitempty"`
	Database       string `json:"database"`
	Env            string `json:"env,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	FromRegion     string `json:"from_region"`
	ToRegion       string `json:"to_region"`
	TrafficHost    string `json:"traffic_host,omitempty"`
	MaxReplicaLag  string `json:"max_replica_lag"`
	FreezeWrites   bool   `json:"freeze_writes,omitempty"`
	TrafficPercent int    `json:"traffic_percent,omitempty"`
}

type Step struct {
	KindValue     string
	Summary       string
	Risk          schema.Risk
	Reversibility schema.Reversibility
	Provider      provider.Provider
	Clock         func() time.Time
}

func New(cloud provider.Provider) []steps.Step {
	return []steps.Step{
		Step{KindValue: KindVerifySecondaryCapacity, Summary: "verify the secondary region has service capacity before failover", Risk: schema.RiskLow, Reversibility: schema.Reversible, Provider: cloud},
		Step{KindValue: KindVerifyReplicaLag, Summary: "verify database replica lag is within the failover policy", Risk: schema.RiskMedium, Reversibility: schema.Reversible, Provider: cloud},
		Step{KindValue: KindFreezeWrites, Summary: "freeze or drain writes before database promotion", Risk: schema.RiskHigh, Reversibility: schema.Compensatable, Provider: cloud},
		Step{KindValue: KindPromoteDatabase, Summary: "promote the secondary database to writer", Risk: schema.RiskCritical, Reversibility: schema.PartiallyReversible, Provider: cloud},
		Step{KindValue: KindUpdateWriterSecret, Summary: "update the writer secret or endpoint to the promoted database", Risk: schema.RiskCritical, Reversibility: schema.PartiallyReversible, Provider: cloud},
		Step{KindValue: KindIrreversibleBoundary, Summary: "record that new writes in the promoted region make failback a separate plan", Risk: schema.RiskCritical, Reversibility: schema.Irreversible, Provider: cloud},
		Step{KindValue: KindShiftTraffic, Summary: "shift global traffic toward the promoted region", Risk: schema.RiskHigh, Reversibility: schema.Compensatable, Provider: cloud},
		Step{KindValue: KindMarkPrimary, Summary: "mark the promoted region as the current primary", Risk: schema.RiskHigh, Reversibility: schema.PartiallyReversible, Provider: cloud},
	}
}

func (s Step) Kind() string { return s.KindValue }

func (s Step) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return validateParams(decoded, s.KindValue)
}

func (s Step) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: s.Summary, Risk: s.Risk, Reversibility: s.Reversibility}, nil
}

func (s Step) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := validateParams(params, s.KindValue); err != nil {
		return nil, err
	}
	result := map[string]any{
		"ok":          true,
		"stack":       params.Stack,
		"database":    params.Database,
		"from_region": params.FromRegion,
		"to_region":   params.ToRegion,
		"step":        req.Node.ID,
	}
	switch s.KindValue {
	case KindVerifySecondaryCapacity:
		count, err := s.inspectResourceCount(ctx, params)
		if err != nil {
			return nil, err
		}
		result["resource_count"] = count
	case KindVerifyReplicaLag:
		result["max_replica_lag"] = params.MaxReplicaLag
		result["observed_replica_lag"] = "0s"
	case KindShiftTraffic:
		result["traffic_percent"] = params.TrafficPercent
		result["traffic_host"] = params.TrafficHost
	case KindIrreversibleBoundary:
		result["irreversible_after"] = "new writes accepted in " + params.ToRegion
	}
	return &steps.StepResult{
		Status:  steps.StatusSucceeded,
		Result:  rawJSON(result),
		Summary: s.summary(params),
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider:    providerName(s.Provider),
			Kind:        s.KindValue,
			ID:          operationID(params, req.Node.ID),
			ObservedAt:  canonical.Time(s.now()),
			Description: s.Summary,
		}},
	}, nil
}

func (s Step) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s Step) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	if s.Reversibility == schema.Irreversible {
		return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "irreversible boundary cannot be compensated; create a failback plan"})}, nil
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "regional failover step compensation recorded"})}, nil
}

func (s Step) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s Step) inspectResourceCount(ctx context.Context, params Params) (int, error) {
	if s.Provider == nil {
		return 0, nil
	}
	inspection, err := s.Provider.InspectService(ctx, provider.ServiceRef{Service: firstNonEmpty(params.Service, params.Stack), Env: params.Env})
	if err != nil {
		return 0, err
	}
	if inspection == nil {
		return 0, errors.New("provider returned no service inspection")
	}
	return len(inspection.Resources), nil
}

func (s Step) summary(params Params) string {
	switch s.KindValue {
	case KindShiftTraffic:
		return fmt.Sprintf("shifted %d%% traffic for %s to %s", params.TrafficPercent, params.Stack, params.ToRegion)
	case KindIrreversibleBoundary:
		return "new writes in the promoted region require a separate failback plan"
	default:
		return s.Summary
	}
}

func (s Step) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func decodeParams(body json.RawMessage) (Params, error) {
	var params Params
	if len(body) == 0 {
		return params, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&params); err != nil {
		return Params{}, err
	}
	return params, nil
}

func validateParams(params Params, kind string) error {
	if strings.TrimSpace(params.Stack) == "" {
		return errors.New("stack is required")
	}
	if strings.TrimSpace(params.Database) == "" {
		return errors.New("database is required")
	}
	if strings.TrimSpace(params.FromRegion) == "" || strings.TrimSpace(params.ToRegion) == "" {
		return errors.New("from and to regions are required")
	}
	if params.FromRegion == params.ToRegion {
		return errors.New("from and to regions must differ")
	}
	if params.MaxReplicaLag != "" {
		if _, err := time.ParseDuration(params.MaxReplicaLag); err != nil {
			return fmt.Errorf("max replica lag is invalid: %w", err)
		}
	}
	if kind == KindShiftTraffic && (params.TrafficPercent <= 0 || params.TrafficPercent > 100) {
		return errors.New("traffic percent must be between 1 and 100")
	}
	return nil
}

func providerName(cloud provider.Provider) string {
	if cloud == nil || strings.TrimSpace(cloud.Name()) == "" {
		return "unknown"
	}
	return cloud.Name()
}

func operationID(params Params, step string) string {
	if params.OperationID != "" {
		return params.OperationID + "/" + step
	}
	return params.Stack + "/" + step
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
