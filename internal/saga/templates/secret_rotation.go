package templates

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	SecretRotationKind        = "secret.rotation"
	DefaultSecretDisableAfter = "24h"
)

type SecretRotationRequest struct {
	SagaID         string       `json:"saga_id,omitempty"`
	OperationID    string       `json:"operation_id,omitempty"`
	SecretRef      string       `json:"secret_ref"`
	Env            string       `json:"env,omitempty"`
	Consumers      []string     `json:"consumers"`
	CanaryConsumer string       `json:"canary_consumer,omitempty"`
	Database       string       `json:"database,omitempty"`
	DisableAfter   string       `json:"disable_after,omitempty"`
	TraceID        string       `json:"trace_id,omitempty"`
	Actor          schema.Actor `json:"actor"`
	CreatedAt      time.Time    `json:"created_at,omitempty"`
}

type SecretRotationFactory func(SecretRotationRequest) (saga.CreateRequest, error)

var secretRotationTemplates = map[string]SecretRotationFactory{
	SecretRotationKind: SecretRotation,
}

func LookupSecretRotation(kind string) (SecretRotationFactory, bool) {
	factory, ok := secretRotationTemplates[kind]
	return factory, ok
}

func RegisteredSecretRotationKinds() []string {
	kinds := make([]string, 0, len(secretRotationTemplates))
	for kind := range secretRotationTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func SecretRotation(req SecretRotationRequest) (saga.CreateRequest, error) {
	req = NormalizeSecretRotationRequest(req)
	if err := validateSecretRotationRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := secretRotationParams{
		SecretRef:      req.SecretRef,
		Env:            req.Env,
		Consumers:      append([]string(nil), req.Consumers...),
		CanaryConsumer: req.CanaryConsumer,
		Database:       req.Database,
		OperationID:    req.OperationID,
		DisableAfter:   req.DisableAfter,
	}
	nodes, edges := secretRotationGraph(params)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          SecretRotationKind,
			Target:        schema.Target{Kind: "secret", Name: req.SecretRef},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("rotate secret %s for %d consumers", req.SecretRef, len(req.Consumers)),
			CreatedAt:     now,
			Params:        mustJSON(params),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Nodes:         nodes,
			Edges:         edges,
			CreatedAt:     now,
		},
		Control: schema.SagaControl{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Status:        schema.SagaPending,
			UpdatedAt:     now,
			TraceID:       req.TraceID,
		},
	}, nil
}

func NormalizeSecretRotationRequest(req SecretRotationRequest) SecretRotationRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.SecretRef = strings.TrimSpace(req.SecretRef)
	req.Env = strings.TrimSpace(req.Env)
	req.Consumers = normalizeStringList(req.Consumers)
	req.CanaryConsumer = strings.TrimSpace(req.CanaryConsumer)
	req.Database = strings.TrimSpace(req.Database)
	req.DisableAfter = strings.TrimSpace(req.DisableAfter)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.DisableAfter == "" {
		req.DisableAfter = DefaultSecretDisableAfter
	}
	if req.CanaryConsumer == "" && len(req.Consumers) > 0 {
		req.CanaryConsumer = req.Consumers[0]
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.SecretRef+"rotation")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateSecretRotationRequest(req SecretRotationRequest) error {
	switch {
	case req.SecretRef == "":
		return errors.New("secret ref is required")
	case len(req.Consumers) == 0:
		return errors.New("at least one consumer is required")
	case req.CanaryConsumer == "":
		return errors.New("canary consumer is required")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	if !containsString(req.Consumers, req.CanaryConsumer) {
		return errors.New("canary consumer must be one of the consumers")
	}
	if _, err := time.ParseDuration(req.DisableAfter); err != nil {
		return fmt.Errorf("disable after is invalid: %w", err)
	}
	return nil
}

type secretRotationParams struct {
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

func secretRotationGraph(params secretRotationParams) ([]schema.SagaNode, []schema.SagaEdge) {
	base := mustJSON(params)
	canary := mustJSON(secretRotationParamsWithScope(params, "canary"))
	all := mustJSON(secretRotationParamsWithScope(params, "all"))
	nodes := []schema.SagaNode{
		{
			ID:   "preflight",
			Kind: "check.preflight",
			Params: mustJSON(map[string]any{
				"env":                     params.Env,
				"require_service_control": false,
				"require_provider":        true,
				"required_facts":          secretRotationFacts(params),
			}),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "create-version",
			Kind:          "secret.create_version",
			Requires:      []string{"preflight"},
			Params:        base,
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "validate-version",
			Kind:          "secret.validate_version",
			Requires:      []string{"create-version"},
			Params:        base,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "update-canary-pointer",
			Kind:          "secret.update_pointer",
			Requires:      []string{"validate-version"},
			Params:        canary,
			Compensate:    &schema.CompensationSpec{Kind: "secret.restore_previous_version", Params: canary},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "canary-consumer",
			Kind:          "service.canary_with_secret",
			Requires:      []string{"update-canary-pointer"},
			Params:        canary,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:       "approve-promotion",
			Kind:     "approval.manual",
			Requires: []string{"canary-consumer"},
			Params: mustJSON(map[string]any{
				"summary": "approve secret promotion after the canary consumer accepted the new version",
				"risk":    schema.RiskHigh,
				"facts": []string{
					"secret_ref:" + params.SecretRef,
					"canary_consumer:" + params.CanaryConsumer,
					"consumers:" + strings.Join(params.Consumers, ","),
					"disable_old_after:" + params.DisableAfter,
				},
			}),
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "promote-secret-pointer",
			Kind:          "secret.update_pointer",
			Requires:      []string{"approve-promotion"},
			Params:        all,
			Compensate:    &schema.CompensationSpec{Kind: "secret.restore_previous_version", Params: all},
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "roll-consumers",
			Kind:          "service.roll_consumers",
			Requires:      []string{"promote-secret-pointer"},
			Params:        all,
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "schedule-disable-old",
			Kind:          "credential.disable_old",
			Requires:      []string{"roll-consumers"},
			Params:        all,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
	}
	return nodes, sequentialEdges(nodes)
}

func secretRotationParamsWithScope(params secretRotationParams, scope string) secretRotationParams {
	params.Scope = scope
	return params
}

func secretRotationFacts(params secretRotationParams) []string {
	facts := []string{
		"secret_ref:" + params.SecretRef,
		"consumers:" + strings.Join(params.Consumers, ","),
		"canary_consumer:" + params.CanaryConsumer,
		"disable_old_after:" + params.DisableAfter,
	}
	if params.Database != "" {
		facts = append(facts, "database:"+params.Database)
	}
	return facts
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
