package templates_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestKeyRotationTemplateBuildsStagedGraph(t *testing.T) {
	factory, ok := templates.LookupKeyRotation(templates.KeyRotationKind)
	if !ok {
		t.Fatal("key rotation template not registered")
	}
	req, err := factory(templates.KeyRotationRequest{
		SagaID:         "saga_key_rotation",
		OperationID:    "op_key_rotation",
		KeyAlias:       "alias/skiff/prod/state",
		Env:            "prod",
		Consumers:      []string{"payments-api", "orders-api"},
		CanaryConsumer: "payments-api",
		MaterialRefs:   []string{"secret://payments/api-token"},
		DisableAfter:   "240h",
		Actor:          schema.Actor{ID: "operator", Type: "user"},
		TraceID:        "tr_key_rotation",
		CreatedAt:      time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("KeyRotation: %v", err)
	}
	if req.Intent.Kind != templates.KeyRotationKind || req.Intent.Risk != schema.RiskHigh || req.Intent.Reversibility != schema.PartiallyReversible {
		t.Fatalf("intent = %+v", req.Intent)
	}
	var params struct {
		BlastRadius []string `json:"blast_radius"`
	}
	if err := json.Unmarshal(req.Intent.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if !contains(params.BlastRadius, "consumer:payments-api") || !contains(params.BlastRadius, "material_ref:secret://payments/api-token") {
		t.Fatalf("blast radius missing expected entries: %+v", params.BlastRadius)
	}
	var sawCandidate, sawReencrypt, sawApproval, sawScheduleDisable bool
	for _, node := range req.Graph.Nodes {
		switch node.ID {
		case "create-candidate-key":
			sawCandidate = node.Kind == "key.create_candidate"
		case "reencrypt-materials":
			sawReencrypt = node.Kind == "key.reencrypt_materials" && node.Reversibility == schema.PartiallyReversible
		case "approve-promotion":
			sawApproval = node.Kind == "approval.manual" && strings.Contains(string(node.Params), "old_key_deletion_requires_separate_critical_approval")
		case "schedule-disable-old-key":
			sawScheduleDisable = node.Kind == "key.schedule_disable_old"
		}
		if strings.Contains(node.Kind, "delete") {
			t.Fatalf("key rotation graph must not delete old keys automatically: %+v", node)
		}
	}
	if !sawCandidate || !sawReencrypt || !sawApproval || !sawScheduleDisable {
		t.Fatalf("graph missing required key rotation steps: %+v", req.Graph.Nodes)
	}
}

func TestCertificateRotationTemplateBuildsConsumerVerifiedGraph(t *testing.T) {
	factory, ok := templates.LookupCertificateRotation(templates.CertificateRotationKind)
	if !ok {
		t.Fatal("certificate rotation template not registered")
	}
	req, err := factory(templates.CertificateRotationRequest{
		SagaID:         "saga_cert_rotation",
		OperationID:    "op_cert_rotation",
		Name:           "payments-api-mtls",
		CertificateRef: "aws-acm://us-west-2/certificate/payments-api",
		Env:            "prod",
		Consumers:      []string{"payments-api", "orders-api"},
		CanaryConsumer: "payments-api",
		TrustStoreRef:  "aws-acm-pca://us-west-2/private-ca/root",
		RetireAfter:    "240h",
		Actor:          schema.Actor{ID: "operator", Type: "user"},
		TraceID:        "tr_cert_rotation",
		CreatedAt:      time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CertificateRotation: %v", err)
	}
	if req.Intent.Kind != templates.CertificateRotationKind || req.Intent.Risk != schema.RiskHigh || req.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("intent = %+v", req.Intent)
	}
	var sawIssue, sawCanaryVerify, sawPromotion, sawTrustVerify, sawRetire bool
	for _, node := range req.Graph.Nodes {
		switch node.ID {
		case "issue-candidate-certificate":
			sawIssue = node.Kind == "certificate.issue_candidate"
		case "verify-canary-consumer":
			sawCanaryVerify = node.Kind == "service.verify_certificate"
		case "promote-certificate-reference":
			sawPromotion = node.Kind == "certificate.promote_reference" && node.Compensate != nil
		case "verify-consumer-trust":
			sawTrustVerify = node.Kind == "certificate.verify_consumer_trust"
		case "schedule-retire-old-certificate":
			sawRetire = node.Kind == "certificate.schedule_retire_old"
		}
		if strings.Contains(node.Kind, "revoke") || strings.Contains(node.Kind, "delete") {
			t.Fatalf("certificate rotation graph must not revoke or delete automatically: %+v", node)
		}
	}
	if !sawIssue || !sawCanaryVerify || !sawPromotion || !sawTrustVerify || !sawRetire {
		t.Fatalf("graph missing required certificate rotation steps: %+v", req.Graph.Nodes)
	}
}

func TestKeyRotationRejectsInvalidCanaryConsumer(t *testing.T) {
	_, err := templates.KeyRotation(templates.KeyRotationRequest{
		KeyAlias:       "alias/skiff/prod/state",
		Consumers:      []string{"payments-api"},
		CanaryConsumer: "orders-api",
		Actor:          schema.Actor{ID: "operator", Type: "user"},
		TraceID:        "tr_key_rotation",
		CreatedAt:      time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected invalid canary consumer to be rejected")
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
