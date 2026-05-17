package security_test

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/audit"
	"github.com/s1liconcow/skiff/internal/auth"
	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestAuthorizationMatrix(t *testing.T) {
	policy := authz.DefaultPolicy{}
	cases := []struct {
		name             string
		req              authz.Request
		allowed          bool
		requiresApproval bool
		breakGlass       bool
	}{
		{
			name: "agent may plan high risk restore",
			req: authz.Request{
				Actor:  schema.Actor{ID: "agent-one", Type: auth.ActorAgent},
				Action: authz.ActionPlan,
				Env:    "prod",
				Risk:   schema.RiskHigh,
			},
			allowed: true,
		},
		{
			name: "agent restore denied without approval",
			req: authz.Request{
				Actor:  schema.Actor{ID: "agent-one", Type: auth.ActorAgent},
				Action: authz.ActionRestore,
				Env:    "prod",
				Risk:   schema.RiskHigh,
			},
			allowed:          false,
			requiresApproval: true,
		},
		{
			name: "agent restore allowed with approval context",
			req: authz.Request{
				Actor:      schema.Actor{ID: "agent-one", Type: auth.ActorAgent},
				Action:     authz.ActionRestore,
				Env:        "prod",
				Risk:       schema.RiskHigh,
				ApprovalID: "approval_01JRESTORE",
			},
			allowed:          true,
			requiresApproval: true,
		},
		{
			name: "ci cannot approve",
			req: authz.Request{
				Actor:  schema.Actor{ID: "github-actions", Type: auth.ActorCI},
				Action: authz.ActionApprove,
				Env:    "prod",
			},
			allowed: false,
		},
		{
			name: "break glass restore allowed and marked",
			req: authz.Request{
				Actor:  schema.Actor{ID: "break-glass:incident-123", Type: auth.ActorBreakGlass},
				Action: authz.ActionRestore,
				Env:    "prod",
				Risk:   schema.RiskCritical,
			},
			allowed:    true,
			breakGlass: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := policy.Authorize(context.Background(), tc.req)
			if tc.allowed && err != nil {
				t.Fatalf("Authorize: %v", err)
			}
			if !tc.allowed && err == nil {
				t.Fatalf("Authorize allowed unexpectedly: %+v", decision)
			}
			if decision.Allowed != tc.allowed || decision.RequiresApproval != tc.requiresApproval || decision.BreakGlass != tc.breakGlass {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}

func TestAuditRecordIncludesApprovalAndSummaries(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	now := time.Date(2026, 5, 17, 3, 30, 0, 0, time.UTC)
	record, err := audit.Append(ctx, store, audit.RecordRequest{
		Actor:         schema.Actor{ID: "alice", Type: auth.ActorUser},
		Action:        "restore",
		Target:        schema.Target{Kind: "database", Name: "payments-db"},
		TraceID:       "tr_audit",
		Risk:          schema.RiskHigh,
		ApprovalID:    "approval_01JRESTORE",
		Summary:       "restored payments-db",
		BeforeSummary: "database endpoint old",
		AfterSummary:  "database endpoint restored",
		Data:          map[string]string{"operation_id": "op_01JRESTORE"},
	}, now, "audit-test")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if record.ApprovalID != "approval_01JRESTORE" || record.BeforeSummary == "" || record.AfterSummary == "" {
		t.Fatalf("record missing audit hardening fields: %+v", record)
	}
	objects, err := store.List(ctx, "audit/2026-05-17/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 {
		t.Fatalf("audit objects = %d, want 1", len(objects))
	}
}
