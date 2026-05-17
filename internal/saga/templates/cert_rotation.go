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
	CertificateRotationKind        = "certificate.rotation"
	DefaultCertificateRetireAfter  = "168h"
	DefaultCertificateValidateFrom = "consumer"
)

type CertificateRotationRequest struct {
	SagaID         string       `json:"saga_id,omitempty"`
	OperationID    string       `json:"operation_id,omitempty"`
	Name           string       `json:"name"`
	CertificateRef string       `json:"certificate_ref,omitempty"`
	Env            string       `json:"env,omitempty"`
	Consumers      []string     `json:"consumers,omitempty"`
	CanaryConsumer string       `json:"canary_consumer,omitempty"`
	TrustStoreRef  string       `json:"trust_store_ref,omitempty"`
	RetireAfter    string       `json:"retire_after,omitempty"`
	ValidateFrom   string       `json:"validate_from,omitempty"`
	TraceID        string       `json:"trace_id,omitempty"`
	Actor          schema.Actor `json:"actor"`
	CreatedAt      time.Time    `json:"created_at,omitempty"`
}

type CertificateRotationFactory func(CertificateRotationRequest) (saga.CreateRequest, error)

var certificateRotationTemplates = map[string]CertificateRotationFactory{
	CertificateRotationKind: CertificateRotation,
}

func LookupCertificateRotation(kind string) (CertificateRotationFactory, bool) {
	factory, ok := certificateRotationTemplates[kind]
	return factory, ok
}

func RegisteredCertificateRotationKinds() []string {
	kinds := make([]string, 0, len(certificateRotationTemplates))
	for kind := range certificateRotationTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func CertificateRotation(req CertificateRotationRequest) (saga.CreateRequest, error) {
	req = NormalizeCertificateRotationRequest(req)
	if err := validateCertificateRotationRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := certificateRotationParams{
		Name:           req.Name,
		CertificateRef: req.CertificateRef,
		Env:            req.Env,
		Consumers:      append([]string(nil), req.Consumers...),
		CanaryConsumer: req.CanaryConsumer,
		TrustStoreRef:  req.TrustStoreRef,
		RetireAfter:    req.RetireAfter,
		ValidateFrom:   req.ValidateFrom,
		OperationID:    req.OperationID,
		BlastRadius:    certificateRotationBlastRadius(req),
	}
	nodes, edges := certificateRotationGraph(params)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          CertificateRotationKind,
			Target:        schema.Target{Kind: "certificate", Name: req.Name},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("rotate certificate %s across %d consumers", req.Name, len(req.Consumers)),
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

func NormalizeCertificateRotationRequest(req CertificateRotationRequest) CertificateRotationRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Name = strings.TrimSpace(req.Name)
	req.CertificateRef = strings.TrimSpace(req.CertificateRef)
	req.Env = strings.TrimSpace(req.Env)
	req.Consumers = normalizeStringList(req.Consumers)
	req.CanaryConsumer = strings.TrimSpace(req.CanaryConsumer)
	req.TrustStoreRef = strings.TrimSpace(req.TrustStoreRef)
	req.RetireAfter = strings.TrimSpace(req.RetireAfter)
	req.ValidateFrom = strings.TrimSpace(req.ValidateFrom)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.RetireAfter == "" {
		req.RetireAfter = DefaultCertificateRetireAfter
	}
	if req.ValidateFrom == "" {
		req.ValidateFrom = DefaultCertificateValidateFrom
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
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Name+"certificate-rotation")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateCertificateRotationRequest(req CertificateRotationRequest) error {
	switch {
	case req.Name == "":
		return errors.New("certificate name is required")
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
	if _, err := time.ParseDuration(req.RetireAfter); err != nil {
		return fmt.Errorf("retire after is invalid: %w", err)
	}
	return nil
}

type certificateRotationParams struct {
	Name             string   `json:"name"`
	CertificateRef   string   `json:"certificate_ref,omitempty"`
	Env              string   `json:"env,omitempty"`
	Consumers        []string `json:"consumers,omitempty"`
	CanaryConsumer   string   `json:"canary_consumer,omitempty"`
	TrustStoreRef    string   `json:"trust_store_ref,omitempty"`
	CandidateCertRef string   `json:"candidate_cert_ref,omitempty"`
	PreviousCertRef  string   `json:"previous_cert_ref,omitempty"`
	RetireAfter      string   `json:"retire_after,omitempty"`
	ValidateFrom     string   `json:"validate_from,omitempty"`
	OperationID      string   `json:"operation_id,omitempty"`
	Scope            string   `json:"scope,omitempty"`
	BlastRadius      []string `json:"blast_radius,omitempty"`
}

func certificateRotationGraph(params certificateRotationParams) ([]schema.SagaNode, []schema.SagaEdge) {
	base := mustJSON(params)
	canary := mustJSON(certificateRotationParamsWithScope(params, "canary"))
	all := mustJSON(certificateRotationParamsWithScope(params, "all"))
	nodes := []schema.SagaNode{
		{
			ID:   "preflight",
			Kind: "check.preflight",
			Params: mustJSON(map[string]any{
				"env":              params.Env,
				"require_provider": true,
				"required_facts":   certificateRotationFacts(params),
			}),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "issue-candidate-certificate",
			Kind:          "certificate.issue_candidate",
			Requires:      []string{"preflight"},
			Params:        base,
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "validate-candidate-certificate",
			Kind:          "certificate.validate_candidate",
			Requires:      []string{"issue-candidate-certificate"},
			Params:        base,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "update-canary-reference",
			Kind:          "certificate.update_reference",
			Requires:      []string{"validate-candidate-certificate"},
			Params:        canary,
			Compensate:    &schema.CompensationSpec{Kind: "certificate.restore_previous_reference", Params: canary},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "verify-canary-consumer",
			Kind:          "service.verify_certificate",
			Requires:      []string{"update-canary-reference"},
			Params:        canary,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		},
		{
			ID:       "approve-promotion",
			Kind:     "approval.manual",
			Requires: []string{"verify-canary-consumer"},
			Params: mustJSON(map[string]any{
				"summary": "approve certificate promotion after consumer-side verification",
				"risk":    schema.RiskHigh,
				"facts": []string{
					"certificate:" + params.Name,
					"canary_consumer:" + params.CanaryConsumer,
					"consumers:" + strings.Join(params.Consumers, ","),
					"validate_from:" + params.ValidateFrom,
					"retire_old_after:" + params.RetireAfter,
					"old_certificate_revocation_requires_separate_approval",
				},
			}),
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "promote-certificate-reference",
			Kind:          "certificate.promote_reference",
			Requires:      []string{"approve-promotion"},
			Params:        all,
			Compensate:    &schema.CompensationSpec{Kind: "certificate.restore_previous_reference", Params: all},
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "roll-consumers",
			Kind:          "service.roll_consumers",
			Requires:      []string{"promote-certificate-reference"},
			Params:        all,
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "verify-consumer-trust",
			Kind:          "certificate.verify_consumer_trust",
			Requires:      []string{"roll-consumers"},
			Params:        all,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "schedule-retire-old-certificate",
			Kind:          "certificate.schedule_retire_old",
			Requires:      []string{"verify-consumer-trust"},
			Params:        all,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
	}
	return nodes, sequentialEdges(nodes)
}

func certificateRotationParamsWithScope(params certificateRotationParams, scope string) certificateRotationParams {
	params.Scope = scope
	return params
}

func certificateRotationFacts(params certificateRotationParams) []string {
	facts := []string{
		"certificate:" + params.Name,
		"consumers:" + strings.Join(params.Consumers, ","),
		"canary_consumer:" + params.CanaryConsumer,
		"validate_from:" + params.ValidateFrom,
		"retire_old_after:" + params.RetireAfter,
		"old_certificate_revocation_requires_separate_approval",
	}
	if params.CertificateRef != "" {
		facts = append(facts, "certificate_ref:"+params.CertificateRef)
	}
	if params.TrustStoreRef != "" {
		facts = append(facts, "trust_store_ref:"+params.TrustStoreRef)
	}
	return facts
}

func certificateRotationBlastRadius(req CertificateRotationRequest) []string {
	out := []string{"certificate:" + req.Name}
	if req.CertificateRef != "" {
		out = append(out, "certificate_ref:"+req.CertificateRef)
	}
	for _, consumer := range req.Consumers {
		out = append(out, "consumer:"+consumer)
	}
	if req.TrustStoreRef != "" {
		out = append(out, "trust_store_ref:"+req.TrustStoreRef)
	}
	return out
}
