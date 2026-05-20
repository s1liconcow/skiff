package packagestep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

const redactedValue = "[REDACTED]"

type HookRunner func(context.Context, pluginapi.Hook, any, any) error

type Step struct {
	Manifest   pluginapi.Manifest
	Capability sagaapi.PackageStepCapability
	RunHook    HookRunner
}

func New(manifest pluginapi.Manifest, capability sagaapi.PackageStepCapability, run HookRunner) (Step, error) {
	step := Step{Manifest: manifest, Capability: capability, RunHook: run}
	if strings.TrimSpace(step.Capability.Kind) == "" {
		return Step{}, errors.New("package step capability kind is required")
	}
	if step.RunHook == nil {
		return Step{}, errors.New("package step hook runner is required")
	}
	return step, nil
}

func (s Step) Kind() string {
	return s.Capability.Kind
}

func (s Step) ValidateParams(ctx context.Context, params json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return validateParams(params, s.Capability.Params)
}

func (s Step) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	var response sagaapi.PackageStepPlanResponse
	_, err := s.invoke(ctx, sagaapi.StepPhasePlan, req, schema.StepResult{}, &response)
	if err != nil {
		return nil, err
	}
	risk := schema.Risk(response.Risk)
	if risk == "" {
		risk = schema.Risk(s.Capability.Risk)
	}
	if risk == "" {
		risk = req.Node.Risk
	}
	reversibility := schema.Reversibility(response.Reversibility)
	if reversibility == "" {
		reversibility = schema.Reversibility(s.Capability.Reversibility)
	}
	if reversibility == "" {
		reversibility = req.Node.Reversibility
	}
	summary := response.Summary
	if summary == "" {
		summary = s.Capability.Summary
	}
	return &steps.StepPlan{Summary: summary, Risk: risk, Reversibility: reversibility}, nil
}

func (s Step) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	var response sagaapi.PackageStepResultResponse
	secrets, err := s.invoke(ctx, sagaapi.StepPhaseRun, req, schema.StepResult{}, &response)
	if err != nil {
		return nil, err
	}
	return s.stepResult(response, secrets), nil
}

func (s Step) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	var response sagaapi.PackageStepResultResponse
	secrets, err := s.invoke(ctx, sagaapi.StepPhaseResume, req, schema.StepResult{}, &response)
	if err != nil {
		return nil, err
	}
	return s.stepResult(response, secrets), nil
}

func (s Step) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	var response sagaapi.PackageStepResultResponse
	secrets, err := s.invoke(ctx, sagaapi.StepPhaseCompensate, req, result, &response)
	if err != nil {
		return nil, err
	}
	return s.stepResult(response, secrets), nil
}

func (s Step) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	var response sagaapi.PackageStepDoctorResponse
	secrets, err := s.invoke(ctx, sagaapi.StepPhaseDoctor, req, schema.StepResult{}, &response)
	if err != nil {
		return nil, err
	}
	out := make([]steps.Finding, 0, len(response.Findings))
	for _, finding := range response.Findings {
		out = append(out, steps.Finding{
			Code:     finding.Code,
			Severity: finding.Severity,
			Summary:  redactText(finding.Summary, secrets),
		})
	}
	return out, nil
}

func (s Step) invoke(ctx context.Context, phase sagaapi.StepPhase, req steps.StepRequest, compensating schema.StepResult, response any) ([]string, error) {
	params, secrets, err := redactRaw(req.Node.Params, s.Capability.Params, nil)
	if err != nil {
		return nil, err
	}
	previous := make(map[string]json.RawMessage, len(req.PreviousResults))
	for key, result := range req.PreviousResults {
		body, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		redacted, found, err := redactRaw(body, nil, secrets)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, found...)
		previous[key] = redacted
	}
	request := pluginapi.PackageStepRequest{
		Manifest: s.Manifest,
		PackageStepRequest: sagaapi.PackageStepRequest{
			SchemaVersion:   sagaapi.PackageStepSchemaVersion,
			Phase:           phase,
			Kind:            s.Kind(),
			Context:         stepContext(req),
			Params:          params,
			PreviousResults: previous,
		},
	}
	if phase == sagaapi.StepPhaseCompensate {
		request.Compensating = packageStepResult(compensating, secrets)
	}
	if err := s.RunHook(ctx, pluginapi.HookPackageStep, request, response); err != nil {
		return secrets, err
	}
	return secrets, nil
}

func (s Step) stepResult(response sagaapi.PackageStepResultResponse, secrets []string) *steps.StepResult {
	status := steps.Status(response.Status)
	if status == "" {
		status = steps.StatusSucceeded
	}
	result, found, err := redactRaw(response.Result, s.Capability.Result, secrets)
	if err == nil {
		secrets = append(secrets, found...)
	} else {
		result = nil
	}
	out := &steps.StepResult{
		Status:  status,
		Result:  result,
		Summary: redactText(response.Summary, secrets),
	}
	if response.Failure != nil {
		out.Failure = &schema.StepFailure{
			Code:       response.Failure.Code,
			Summary:    redactText(response.Failure.Summary, secrets),
			Cause:      redactText(response.Failure.Cause, secrets),
			Retriable:  response.Failure.Retriable,
			RetryAfter: response.Failure.RetryAfter,
		}
	}
	for _, op := range response.ProviderOperations {
		out.ProviderOperations = append(out.ProviderOperations, schema.ProviderOperationRef{
			Provider:    op.Provider,
			Kind:        op.Kind,
			ID:          op.ID,
			ObservedAt:  op.ObservedAt,
			Description: redactText(op.Description, secrets),
		})
	}
	return out
}

func stepContext(req steps.StepRequest) sagaapi.PackageStepContext {
	ctx := sagaapi.PackageStepContext{
		Target:     req.Intent.Target.Name,
		TargetKind: req.Intent.Target.Kind,
		Service:    req.Intent.Target.Name,
		SagaID:     req.SagaID,
		StepID:     req.Node.ID,
		TraceID:    req.TraceID,
	}
	if req.Intent.Package != nil {
		ctx.PackageRef = req.Intent.Package.Ref
		ctx.PackageDigest = req.Intent.Package.Digest
	}
	var params struct {
		Env         string `json:"env"`
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(req.Node.Params, &params)
	ctx.Env = params.Env
	ctx.OperationID = params.OperationID
	return ctx
}

func packageStepResult(result schema.StepResult, secrets []string) *sagaapi.PackageStepResult {
	if result.StepID == "" && result.Status == "" && len(result.Result) == 0 && result.Failure == nil {
		return nil
	}
	body, _, err := redactRaw(result.Result, nil, secrets)
	if err != nil {
		body = nil
	}
	out := &sagaapi.PackageStepResult{
		Status:             sagaapi.StepStatus(result.Status),
		Result:             body,
		ProviderOperations: make([]sagaapi.ProviderOperationRef, 0, len(result.ProviderOperations)),
	}
	if out.Status == "" {
		out.Status = sagaapi.StepStatusSucceeded
	}
	if result.Failure != nil {
		out.Failure = &sagaapi.StepFailure{
			Code:       result.Failure.Code,
			Summary:    redactText(result.Failure.Summary, secrets),
			Cause:      redactText(result.Failure.Cause, secrets),
			Retriable:  result.Failure.Retriable,
			RetryAfter: result.Failure.RetryAfter,
		}
	}
	for _, op := range result.ProviderOperations {
		out.ProviderOperations = append(out.ProviderOperations, sagaapi.ProviderOperationRef{
			Provider:    op.Provider,
			Kind:        op.Kind,
			ID:          op.ID,
			ObservedAt:  op.ObservedAt,
			Description: redactText(op.Description, secrets),
		})
	}
	return out
}

func validateParams(raw json.RawMessage, fields map[string]sagaapi.ParamSchema) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		for name, field := range fields {
			if field.Required {
				return fmt.Errorf("package step param %q is required", name)
			}
		}
		return nil
	}
	value, err := decodeJSON(raw)
	if err != nil {
		return fmt.Errorf("package step params must be valid JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("package step params must be a JSON object")
	}
	return validateObject(object, fields, "$.params")
}

func validateObject(object map[string]any, fields map[string]sagaapi.ParamSchema, path string) error {
	for key := range object {
		if _, ok := fields[key]; !ok && len(fields) > 0 {
			return fmt.Errorf("%s.%s is not declared by package step schema", path, key)
		}
	}
	for name, field := range fields {
		value, ok := object[name]
		if !ok {
			if field.Required {
				return fmt.Errorf("%s.%s is required", path, name)
			}
			continue
		}
		if err := validateValue(value, field, path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(value any, field sagaapi.ParamSchema, path string) error {
	switch field.Type {
	case sagaapi.ParamString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case sagaapi.ParamBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case sagaapi.ParamInteger:
		number, ok := value.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			return fmt.Errorf("%s must be an integer", path)
		}
	case sagaapi.ParamNumber:
		if _, ok := value.(json.Number); !ok {
			return fmt.Errorf("%s must be a number", path)
		}
	case sagaapi.ParamObject:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		if len(field.Properties) > 0 {
			if err := validateObject(object, field.Properties, path); err != nil {
				return err
			}
		}
	case sagaapi.ParamArray:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		if field.Items != nil {
			for i, item := range items {
				if err := validateValue(item, *field.Items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	default:
		if field.Type != "" {
			return fmt.Errorf("%s has unsupported type %q", path, field.Type)
		}
	}
	if len(field.Enum) > 0 {
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s enum requires a string value", path)
		}
		for _, allowed := range field.Enum {
			if str == allowed {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", path, strings.Join(field.Enum, ", "))
	}
	return nil
}

func redactRaw(raw json.RawMessage, fields map[string]sagaapi.ParamSchema, known []string) (json.RawMessage, []string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, nil
	}
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	secrets := append([]string(nil), known...)
	redacted := redactValue(value, fields, &secrets, "")
	body, err := json.Marshal(redacted)
	if err != nil {
		return nil, nil, err
	}
	return body, uniqueSecrets(secrets[len(known):]), nil
}

func redactValue(value any, fields map[string]sagaapi.ParamSchema, secrets *[]string, key string) any {
	if value == nil {
		return nil
	}
	field, hasField := fields[key]
	if key != "" && (isSensitiveKey(key) || hasField && field.Secret) {
		collectSecret(value, secrets)
		return redactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			childFields := map[string]sagaapi.ParamSchema(nil)
			if hasField && field.Type == sagaapi.ParamObject {
				childFields = field.Properties
			} else if fields != nil {
				childFields = fields
			}
			out[childKey] = redactValue(child, childFields, secrets, childKey)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		var itemFields map[string]sagaapi.ParamSchema
		if hasField && field.Items != nil && field.Items.Type == sagaapi.ParamObject {
			itemFields = field.Items.Properties
		}
		for i, child := range typed {
			out[i] = redactValue(child, itemFields, secrets, "")
		}
		return out
	case string:
		return redactText(typed, *secrets)
	default:
		return value
	}
}

func collectSecret(value any, secrets *[]string) {
	switch typed := value.(type) {
	case string:
		if len(typed) >= 4 {
			*secrets = append(*secrets, typed)
		}
	case map[string]any:
		for _, child := range typed {
			collectSecret(child, secrets)
		}
	case []any:
		for _, child := range typed {
			collectSecret(child, secrets)
		}
	}
}

func redactText(value string, secrets []string) string {
	out := value
	for _, secret := range uniqueSecrets(secrets) {
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, redactedValue)
	}
	return out
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"secret", "password", "passwd", "token", "private_key", "credential"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func uniqueSecrets(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func decodeJSON(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values are not supported")
		}
		return nil, err
	}
	return value, nil
}
