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
	KeyRotationKind        = "key.rotation"
	DefaultKeyDisableAfter = "168h"
)

type KeyRotationRequest struct {
	SagaID         string       `json:"saga_id,omitempty"`
	OperationID    string       `json:"operation_id,omitempty"`
	KeyAlias       string       `json:"key_alias"`
	Env            string       `json:"env,omitempty"`
	Consumers      []string     `json:"consumers,omitempty"`
	CanaryConsumer string       `json:"canary_consumer,omitempty"`
	MaterialRefs   []string     `json:"material_refs,omitempty"`
	DisableAfter   string       `json:"disable_after,omitempty"`
	TraceID        string       `json:"trace_id,omitempty"`
	Actor          schema.Actor `json:"actor"`
	CreatedAt      time.Time    `json:"created_at,omitempty"`
}

type KeyRotationFactory func(KeyRotationRequest) (saga.CreateRequest, error)

var keyRotationTemplates = map[string]KeyRotationFactory{
	KeyRotationKind: KeyRotation,
}

func LookupKeyRotation(kind string) (KeyRotationFactory, bool) {
	factory, ok := keyRotationTemplates[kind]
	return factory, ok
}

func RegisteredKeyRotationKinds() []string {
	kinds := make([]string, 0, len(keyRotationTemplates))
	for kind := range keyRotationTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func KeyRotation(req KeyRotationRequest) (saga.CreateRequest, error) {
	req = NormalizeKeyRotationRequest(req)
	if err := validateKeyRotationRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := keyRotationParams{
		KeyAlias:       req.KeyAlias,
		Env:            req.Env,
		Consumers:      append([]string(nil), req.Consumers...),
		CanaryConsumer: req.CanaryConsumer,
		MaterialRefs:   append([]string(nil), req.MaterialRefs...),
		OperationID:    req.OperationID,
		DisableAfter:   req.DisableAfter,
		BlastRadius:    keyRotationBlastRadius(req),
	}
	nodes, edges := keyRotationGraph(params)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          KeyRotationKind,
			Target:        schema.Target{Kind: "kms-key-alias", Name: req.KeyAlias},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskHigh,
			Reversibility: schema.PartiallyReversible,
			Summary:       fmt.Sprintf("rotate key alias %s across %d consumers", req.KeyAlias, len(req.Consumers)),
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

func NormalizeKeyRotationRequest(req KeyRotationRequest) KeyRotationRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.KeyAlias = strings.TrimSpace(req.KeyAlias)
	req.Env = strings.TrimSpace(req.Env)
	req.Consumers = normalizeStringList(req.Consumers)
	req.CanaryConsumer = strings.TrimSpace(req.CanaryConsumer)
	req.MaterialRefs = normalizeStringList(req.MaterialRefs)
	req.DisableAfter = strings.TrimSpace(req.DisableAfter)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.DisableAfter == "" {
		req.DisableAfter = DefaultKeyDisableAfter
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
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.KeyAlias+"key-rotation")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateKeyRotationRequest(req KeyRotationRequest) error {
	switch {
	case req.KeyAlias == "":
		return errors.New("key alias is required")
	case len(req.Consumers) == 0 && len(req.MaterialRefs) == 0:
		return errors.New("at least one consumer or material ref is required")
	case len(req.Consumers) > 0 && req.CanaryConsumer == "":
		return errors.New("canary consumer is required when consumers are provided")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	if req.CanaryConsumer != "" && !containsString(req.Consumers, req.CanaryConsumer) {
		return errors.New("canary consumer must be one of the consumers")
	}
	if _, err := time.ParseDuration(req.DisableAfter); err != nil {
		return fmt.Errorf("disable after is invalid: %w", err)
	}
	return nil
}

type keyRotationParams struct {
	KeyAlias       string   `json:"key_alias"`
	Env            string   `json:"env,omitempty"`
	Consumers      []string `json:"consumers,omitempty"`
	CanaryConsumer string   `json:"canary_consumer,omitempty"`
	MaterialRefs   []string `json:"material_refs,omitempty"`
	OperationID    string   `json:"operation_id,omitempty"`
	CandidateKeyID string   `json:"candidate_key_id,omitempty"`
	PreviousKeyID  string   `json:"previous_key_id,omitempty"`
	DisableAfter   string   `json:"disable_after,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	BlastRadius    []string `json:"blast_radius,omitempty"`
}

func keyRotationGraph(params keyRotationParams) ([]schema.SagaNode, []schema.SagaEdge) {
	base := mustJSON(params)
	canary := mustJSON(keyRotationParamsWithScope(params, "canary"))
	all := mustJSON(keyRotationParamsWithScope(params, "all"))
	nodes := []schema.SagaNode{
		{
			ID:   "preflight",
			Kind: "check.preflight",
			Params: mustJSON(map[string]any{
				"env":              params.Env,
				"require_provider": true,
				"required_facts":   keyRotationFacts(params),
			}),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "create-candidate-key",
			Kind:          "key.create_candidate",
			Requires:      []string{"preflight"},
			Params:        base,
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "reencrypt-materials",
			Kind:          "key.reencrypt_materials",
			Requires:      []string{"create-candidate-key"},
			Params:        base,
			Risk:          schema.RiskHigh,
			Reversibility: schema.PartiallyReversible,
		},
		{
			ID:            "validate-candidate-key",
			Kind:          "key.validate_candidate",
			Requires:      []string{"reencrypt-materials"},
			Params:        base,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "canary-consumer",
			Kind:          "service.canary_with_key",
			Requires:      []string{"validate-candidate-key"},
			Params:        canary,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:       "approve-promotion",
			Kind:     "approval.manual",
			Requires: []string{"canary-consumer"},
			Params: mustJSON(map[string]any{
				"summary": "approve key alias promotion after canary verification",
				"risk":    schema.RiskHigh,
				"facts": []string{
					"key_alias:" + params.KeyAlias,
					"canary_consumer:" + params.CanaryConsumer,
					"consumers:" + strings.Join(params.Consumers, ","),
					"material_refs:" + strings.Join(params.MaterialRefs, ","),
					"disable_old_after:" + params.DisableAfter,
					"old_key_deletion_requires_separate_critical_approval",
				},
			}),
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "promote-key-alias",
			Kind:          "key.promote_alias",
			Requires:      []string{"approve-promotion"},
			Params:        all,
			Compensate:    &schema.CompensationSpec{Kind: "key.restore_previous_alias", Params: all},
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "verify-key-policy",
			Kind:          "key.verify_policy",
			Requires:      []string{"promote-key-alias"},
			Params:        all,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "roll-consumers",
			Kind:          "service.roll_consumers",
			Requires:      []string{"verify-key-policy"},
			Params:        all,
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "schedule-disable-old-key",
			Kind:          "key.schedule_disable_old",
			Requires:      []string{"roll-consumers"},
			Params:        all,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
	}
	return nodes, sequentialEdges(nodes)
}

func keyRotationParamsWithScope(params keyRotationParams, scope string) keyRotationParams {
	params.Scope = scope
	return params
}

func keyRotationFacts(params keyRotationParams) []string {
	facts := []string{
		"key_alias:" + params.KeyAlias,
		"disable_old_after:" + params.DisableAfter,
		"old_key_deletion_requires_separate_critical_approval",
	}
	if len(params.Consumers) > 0 {
		facts = append(facts, "consumers:"+strings.Join(params.Consumers, ","))
	}
	if params.CanaryConsumer != "" {
		facts = append(facts, "canary_consumer:"+params.CanaryConsumer)
	}
	if len(params.MaterialRefs) > 0 {
		facts = append(facts, "material_refs:"+strings.Join(params.MaterialRefs, ","))
	}
	return facts
}

func keyRotationBlastRadius(req KeyRotationRequest) []string {
	out := make([]string, 0, len(req.Consumers)+len(req.MaterialRefs)+1)
	out = append(out, "key_alias:"+req.KeyAlias)
	for _, consumer := range req.Consumers {
		out = append(out, "consumer:"+consumer)
	}
	for _, ref := range req.MaterialRefs {
		out = append(out, "material_ref:"+ref)
	}
	return out
}
