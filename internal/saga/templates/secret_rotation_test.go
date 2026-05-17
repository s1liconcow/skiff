package templates_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestSecretRotationTemplateBuildsSafePromotionGraph(t *testing.T) {
	factory, ok := templates.LookupSecretRotation(templates.SecretRotationKind)
	if !ok {
		t.Fatal("secret rotation template not registered")
	}
	req, err := factory(templates.SecretRotationRequest{
		SagaID:         "saga_secret_rotation",
		OperationID:    "op_secret_rotation",
		SecretRef:      "secret://managed-database/orders-db/connection-url",
		Env:            "prod",
		Consumers:      []string{"orders-api", "orders-worker"},
		CanaryConsumer: "orders-api",
		Database:       "orders-db",
		DisableAfter:   "48h",
		Actor:          schema.Actor{ID: "operator", Type: "user"},
		TraceID:        "tr_secret_rotation",
		CreatedAt:      time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("SecretRotation: %v", err)
	}
	if req.Intent.Kind != templates.SecretRotationKind || req.Intent.Risk != schema.RiskHigh || req.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("intent = %+v", req.Intent)
	}
	var sawCanaryPointer, sawApproval, sawScheduleDisable bool
	for _, node := range req.Graph.Nodes {
		switch node.ID {
		case "update-canary-pointer":
			sawCanaryPointer = node.Kind == "secret.update_pointer" && node.Compensate != nil && node.Compensate.Kind == "secret.restore_previous_version"
			var params struct {
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(node.Params, &params); err != nil {
				t.Fatalf("decode canary params: %v", err)
			}
			if params.Scope != "canary" {
				t.Fatalf("canary pointer scope = %q", params.Scope)
			}
		case "approve-promotion":
			sawApproval = node.Kind == "approval.manual" && node.Risk == schema.RiskHigh
		case "schedule-disable-old":
			sawScheduleDisable = node.Kind == "credential.disable_old" && node.Reversibility == schema.Compensatable
		}
	}
	if !sawCanaryPointer || !sawApproval || !sawScheduleDisable {
		t.Fatalf("graph missing canary pointer, approval, or delayed disable: %+v", req.Graph.Nodes)
	}
}

func TestSecretRotationRejectsInvalidCanaryConsumer(t *testing.T) {
	_, err := templates.SecretRotation(templates.SecretRotationRequest{
		SecretRef:      "secret://app/api-key",
		Consumers:      []string{"orders-api"},
		CanaryConsumer: "billing-api",
		Actor:          schema.Actor{ID: "operator", Type: "user"},
		TraceID:        "tr_secret_rotation",
		CreatedAt:      time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected invalid canary consumer to be rejected")
	}
}
