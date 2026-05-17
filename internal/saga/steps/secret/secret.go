package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	KindCreateVersion          = "secret.create_version"
	KindValidateVersion        = "secret.validate_version"
	KindUpdatePointer          = "secret.update_pointer"
	KindRestorePreviousVersion = "secret.restore_previous_version"
	KindCanaryWithSecret       = "service.canary_with_secret"
	KindRollConsumers          = "service.roll_consumers"
	KindDisableOldCredential   = "credential.disable_old"
)

type Params struct {
	SecretRef       string   `json:"secret_ref"`
	Env             string   `json:"env,omitempty"`
	Consumers       []string `json:"consumers,omitempty"`
	CanaryConsumer  string   `json:"canary_consumer,omitempty"`
	Database        string   `json:"database,omitempty"`
	OperationID     string   `json:"operation_id,omitempty"`
	NewVersion      string   `json:"new_version,omitempty"`
	PreviousVersion string   `json:"previous_version,omitempty"`
	DisableAfter    string   `json:"disable_after,omitempty"`
	Scope           string   `json:"scope,omitempty"`
}

type base struct {
	Provider provider.SecretOperations
	Clock    func() time.Time
}

type CreateVersion struct{ base }
type ValidateVersion struct{ base }
type UpdatePointer struct{ base }
type RestorePreviousVersion struct{ base }
type CanaryWithSecret struct{ base }
type RollConsumers struct{ base }
type DisableOldCredential struct{ base }

func New(provider provider.SecretOperations) []steps.Step {
	base := base{Provider: provider}
	return []steps.Step{
		CreateVersion{base: base},
		ValidateVersion{base: base},
		UpdatePointer{base: base},
		RestorePreviousVersion{base: base},
		CanaryWithSecret{base: base},
		RollConsumers{base: base},
		DisableOldCredential{base: base},
	}
}

func (s CreateVersion) Kind() string          { return KindCreateVersion }
func (s ValidateVersion) Kind() string        { return KindValidateVersion }
func (s UpdatePointer) Kind() string          { return KindUpdatePointer }
func (s RestorePreviousVersion) Kind() string { return KindRestorePreviousVersion }
func (s CanaryWithSecret) Kind() string       { return KindCanaryWithSecret }
func (s RollConsumers) Kind() string          { return KindRollConsumers }
func (s DisableOldCredential) Kind() string   { return KindDisableOldCredential }

func (s CreateVersion) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireSecret(decoded)
}

func (s CreateVersion) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "create a new secret version without exposing plaintext in object state", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s CreateVersion) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := requireSecret(params); err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	version, err := s.Provider.CreateSecretVersion(ctx, provider.SecretVersionRequest{
		SecretRef:   params.SecretRef,
		Env:         params.Env,
		Consumers:   params.Consumers,
		Database:    params.Database,
		OperationID: params.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, errors.New("secret provider returned no version")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":               true,
			"secret_ref":       params.SecretRef,
			"new_version":      version.VersionID,
			"previous_version": version.PreviousVersion,
			"provider":         version.Provider,
		}),
		ProviderOperations: []schema.ProviderOperationRef{providerOperation(version.VersionID, version.Provider, "secret_version_create", "secret version created", s.now())},
		Summary:            "created new secret version for " + params.SecretRef,
	}, nil
}

func (s CreateVersion) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s CreateVersion) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "created secret versions are retained until explicit delayed disable"})}, nil
}

func (s CreateVersion) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s ValidateVersion) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireSecret(decoded)
}

func (s ValidateVersion) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "validate the new secret version before any consumer uses it", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s ValidateVersion) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	versionID := firstNonEmpty(params.NewVersion, previousString(req.PreviousResults, "create-version", "new_version"))
	result, err := s.Provider.ValidateSecretVersion(ctx, provider.SecretValidationRequest{SecretRef: params.SecretRef, VersionID: versionID, Env: params.Env, Database: params.Database})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.OK {
		return &steps.StepResult{Status: steps.StatusFailed, Failure: &schema.StepFailure{Code: "SECRET_VERSION_INVALID", Summary: "secret version validation failed"}}, nil
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(result), Summary: "validated new secret version for " + params.SecretRef}, nil
}

func (s ValidateVersion) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s ValidateVersion) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "validation has no compensation"})}, nil
}

func (s ValidateVersion) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s UpdatePointer) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireSecret(decoded)
}

func (s UpdatePointer) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "promote the new secret version pointer for the selected consumers", Risk: schema.RiskHigh, Reversibility: schema.Compensatable}, nil
}

func (s UpdatePointer) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	pointer, err := s.Provider.UpdateSecretVersionPointer(ctx, provider.SecretUpdateRequest{
		SecretRef:       params.SecretRef,
		VersionID:       firstNonEmpty(params.NewVersion, previousString(req.PreviousResults, "create-version", "new_version")),
		PreviousVersion: firstNonEmpty(params.PreviousVersion, previousString(req.PreviousResults, "create-version", "previous_version")),
		Env:             params.Env,
		Database:        params.Database,
		Consumers:       scopedConsumers(params),
		Scope:           params.Scope,
		OperationID:     params.OperationID,
		SagaID:          req.SagaID,
		TraceID:         req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	if pointer == nil {
		return nil, errors.New("secret provider returned no pointer")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":               true,
			"secret_ref":       pointer.SecretRef,
			"new_version":      pointer.VersionID,
			"previous_version": pointer.PreviousVersion,
			"scope":            params.Scope,
		}),
		ProviderOperations: []schema.ProviderOperationRef{providerOperation(pointer.VersionID, pointer.Provider, "secret_pointer_update", "secret pointer updated", s.now())},
		Summary:            "updated secret pointer for " + params.SecretRef,
	}, nil
}

func (s UpdatePointer) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s UpdatePointer) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	previous := firstNonEmpty(params.PreviousVersion, resultString(result, "previous_version"), previousString(req.PreviousResults, "create-version", "previous_version"))
	pointer, err := s.Provider.RestoreSecretVersion(ctx, provider.SecretRestoreRequest{
		SecretRef:       params.SecretRef,
		PreviousVersion: previous,
		Env:             params.Env,
		Database:        params.Database,
		Consumers:       scopedConsumers(params),
		Scope:           params.Scope,
		OperationID:     params.OperationID,
		SagaID:          req.SagaID,
		TraceID:         req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	if pointer == nil {
		return nil, errors.New("secret provider returned no restored pointer")
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(pointer), Summary: "restored previous secret pointer for " + params.SecretRef}, nil
}

func (s UpdatePointer) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s RestorePreviousVersion) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireSecret(decoded)
}

func (s RestorePreviousVersion) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "restore the previous secret pointer", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s RestorePreviousVersion) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	previous := firstNonEmpty(params.PreviousVersion, previousString(req.PreviousResults, "create-version", "previous_version"))
	pointer, err := s.Provider.RestoreSecretVersion(ctx, provider.SecretRestoreRequest{SecretRef: params.SecretRef, PreviousVersion: previous, Env: params.Env, Database: params.Database, Consumers: scopedConsumers(params), Scope: params.Scope, OperationID: params.OperationID, SagaID: req.SagaID, TraceID: req.TraceID})
	if err != nil {
		return nil, err
	}
	if pointer == nil {
		return nil, errors.New("secret provider returned no restored pointer")
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(pointer), Summary: "restored previous secret pointer for " + params.SecretRef}, nil
}

func (s RestorePreviousVersion) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RestorePreviousVersion) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "restore previous version has no additional compensation"})}, nil
}

func (s RestorePreviousVersion) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s CanaryWithSecret) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if err := requireSecret(decoded); err != nil {
		return err
	}
	if firstNonEmpty(decoded.CanaryConsumer, firstConsumer(decoded.Consumers)) == "" {
		return errors.New("canary consumer is required")
	}
	return nil
}

func (s CanaryWithSecret) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "canary one consumer against the new secret version", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s CanaryWithSecret) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	consumer := firstNonEmpty(params.CanaryConsumer, firstConsumer(params.Consumers))
	result, err := s.Provider.CanaryServiceWithSecret(ctx, provider.SecretCanaryRequest{SecretRef: params.SecretRef, VersionID: firstNonEmpty(params.NewVersion, previousString(req.PreviousResults, "create-version", "new_version")), Env: params.Env, Database: params.Database, Consumer: consumer, OperationID: params.OperationID, SagaID: req.SagaID, TraceID: req.TraceID})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.OK {
		return &steps.StepResult{Status: steps.StatusFailed, Failure: &schema.StepFailure{Code: "SECRET_CANARY_FAILED", Summary: "secret canary consumer failed"}}, nil
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(result), Summary: "secret canary passed for " + consumer}, nil
}

func (s CanaryWithSecret) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s CanaryWithSecret) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "canary compensation is handled by restoring the canary pointer"})}, nil
}

func (s CanaryWithSecret) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s RollConsumers) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if err := requireSecret(decoded); err != nil {
		return err
	}
	if len(decoded.Consumers) == 0 {
		return errors.New("consumers are required")
	}
	return nil
}

func (s RollConsumers) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "roll all consumers to pick up the promoted secret version", Risk: schema.RiskHigh, Reversibility: schema.Compensatable}, nil
}

func (s RollConsumers) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	result, err := s.Provider.RollConsumersWithSecret(ctx, provider.SecretRollConsumersRequest{SecretRef: params.SecretRef, VersionID: firstNonEmpty(params.NewVersion, previousString(req.PreviousResults, "create-version", "new_version")), Env: params.Env, Database: params.Database, Consumers: params.Consumers, OperationID: params.OperationID, SagaID: req.SagaID, TraceID: req.TraceID})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.OK {
		return &steps.StepResult{Status: steps.StatusFailed, Failure: &schema.StepFailure{Code: "SECRET_CONSUMER_ROLL_FAILED", Summary: "consumer roll failed"}}, nil
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(result), Summary: "rolled consumers to new secret version"}, nil
}

func (s RollConsumers) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RollConsumers) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "consumer roll compensation requires restoring the secret pointer and rolling consumers again"})}, nil
}

func (s RollConsumers) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s DisableOldCredential) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireSecret(decoded)
}

func (s DisableOldCredential) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "schedule delayed disable of the old credential version", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s DisableOldCredential) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := paramsWithPrevious(req)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret provider is required")
	}
	result, err := s.Provider.DisableOldCredential(ctx, provider.CredentialDisableRequest{SecretRef: params.SecretRef, PreviousVersion: firstNonEmpty(params.PreviousVersion, previousString(req.PreviousResults, "create-version", "previous_version")), Env: params.Env, Database: params.Database, Consumers: params.Consumers, DisableAfter: params.DisableAfter, OperationID: params.OperationID, SagaID: req.SagaID, TraceID: req.TraceID})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("secret provider returned no disable result")
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(result), Summary: "scheduled delayed disable of old credential"}, nil
}

func (s DisableOldCredential) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s DisableOldCredential) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "scheduled disable can be canceled by credential provider policy"})}, nil
}

func (s DisableOldCredential) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func decodeParams(body json.RawMessage) (Params, error) {
	var params Params
	if len(bytes.TrimSpace(body)) == 0 {
		return params, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&params); err != nil {
		return Params{}, err
	}
	params.SecretRef = strings.TrimSpace(params.SecretRef)
	params.Env = strings.TrimSpace(params.Env)
	params.CanaryConsumer = strings.TrimSpace(params.CanaryConsumer)
	params.Database = strings.TrimSpace(params.Database)
	params.OperationID = strings.TrimSpace(params.OperationID)
	params.NewVersion = strings.TrimSpace(params.NewVersion)
	params.PreviousVersion = strings.TrimSpace(params.PreviousVersion)
	params.DisableAfter = strings.TrimSpace(params.DisableAfter)
	params.Scope = strings.TrimSpace(params.Scope)
	params.Consumers = normalizeConsumers(params.Consumers)
	return params, nil
}

func paramsWithPrevious(req steps.StepRequest) (Params, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return Params{}, err
	}
	if err := requireSecret(params); err != nil {
		return Params{}, err
	}
	if params.NewVersion == "" {
		params.NewVersion = previousString(req.PreviousResults, "create-version", "new_version")
	}
	if params.PreviousVersion == "" {
		params.PreviousVersion = previousString(req.PreviousResults, "create-version", "previous_version")
	}
	return params, nil
}

func requireSecret(params Params) error {
	if strings.TrimSpace(params.SecretRef) == "" {
		return errors.New("secret ref is required")
	}
	return nil
}

func scopedConsumers(params Params) []string {
	if strings.EqualFold(params.Scope, "canary") {
		if consumer := firstNonEmpty(params.CanaryConsumer, firstConsumer(params.Consumers)); consumer != "" {
			return []string{consumer}
		}
	}
	return append([]string(nil), params.Consumers...)
}

func previousString(results map[string]schema.StepResult, stepID, field string) string {
	if results == nil {
		return ""
	}
	result, ok := results[stepID]
	if !ok || len(result.Result) == 0 {
		return ""
	}
	return resultString(result, field)
}

func resultString(result schema.StepResult, field string) string {
	var payload map[string]any
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		return ""
	}
	if value, ok := payload[field].(string); ok {
		return value
	}
	return ""
}

func providerOperation(id, providerName, kind, description string, observedAt time.Time) schema.ProviderOperationRef {
	if strings.TrimSpace(providerName) == "" {
		providerName = "unknown"
	}
	return schema.ProviderOperationRef{Provider: providerName, Kind: kind, ID: id, ObservedAt: canonical.Time(observedAt), Description: description}
}

func (b base) now() time.Time {
	if b.Clock != nil {
		return b.Clock().UTC()
	}
	return time.Now().UTC()
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func firstConsumer(consumers []string) string {
	if len(consumers) == 0 {
		return ""
	}
	return consumers[0]
}

func normalizeConsumers(consumers []string) []string {
	out := make([]string, 0, len(consumers))
	seen := map[string]struct{}{}
	for _, consumer := range consumers {
		consumer = strings.TrimSpace(consumer)
		if consumer == "" {
			continue
		}
		key := strings.ToLower(consumer)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, consumer)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
