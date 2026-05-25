package policy_test

import (
	"testing"

	"github.com/s1liconcow/skiff/internal/security/policy"
)

func TestGeneratedPoliciesLintClean(t *testing.T) {
	for _, tc := range []struct {
		role policy.Role
		doc  policy.Document
	}{
		{role: policy.RoleStateBucket, doc: policy.StateBucketPolicy("skiff-state-prod")},
		{role: policy.RoleDeveloper, doc: policy.DeveloperPolicy("skiff-state-prod", "alias/skiff-prod-state")},
		{role: policy.RoleRunner, doc: policy.RunnerPolicy("skiff-state-prod", "alias/skiff-prod-state")},
		{role: policy.RoleDeployer, doc: policy.DeployerPolicy("skiff-state-prod", "alias/skiff-prod-state")},
		{role: policy.RoleSkiffd, doc: policy.SkiffdPolicy("skiff-state-prod", "alias/skiff-prod-state")},
		{role: policy.RoleBreakGlass, doc: policy.BreakGlassPolicy("skiff-state-prod", "alias/skiff-prod-state")},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			if findings := policy.Lint(tc.doc, policy.LintOptions{Role: tc.role}); len(findings) != 0 {
				t.Fatalf("Lint returned findings: %+v", findings)
			}
		})
	}
}

func TestDeveloperPolicyCannotWriteStateObjects(t *testing.T) {
	doc := policy.DeveloperPolicy("skiff-state-prod", "alias/skiff-prod-state")
	for _, statement := range doc.Statement {
		if hasAction(statement.Action, "s3:PutObject") {
			t.Fatalf("developer policy grants PutObject in %s: %+v", statement.Sid, statement)
		}
		if hasAction(statement.Action, "kms:Encrypt") || hasAction(statement.Action, "kms:GenerateDataKey") {
			t.Fatalf("developer policy grants KMS write action in %s: %+v", statement.Sid, statement)
		}
	}
	if !statementHasResource(doc, "ReadOperationalState", "arn:aws:s3:::skiff-state-prod/audit/*/*") {
		t.Fatalf("developer policy must allow audit inspection: %+v", doc.Statement)
	}
}

func TestRunnerPolicyCannotWriteStateObjects(t *testing.T) {
	doc := policy.RunnerPolicy("skiff-state-prod", "alias/skiff-prod-state")
	for _, statement := range doc.Statement {
		if hasAction(statement.Action, "s3:PutObject") {
			t.Fatalf("runner policy grants PutObject in %s: %+v", statement.Sid, statement)
		}
		if hasAction(statement.Action, "kms:Encrypt") || hasAction(statement.Action, "kms:GenerateDataKey") {
			t.Fatalf("runner policy grants KMS write action in %s: %+v", statement.Sid, statement)
		}
	}
}

func TestEscalatedWriteTrustRequiresAuditableContext(t *testing.T) {
	doc := policy.EscalatedWriteTrustPolicy()
	assume := statementBySid(doc, "AllowTemporaryWriteEscalation")
	if assume == nil {
		t.Fatalf("missing temporary escalation trust statement: %+v", doc.Statement)
	}
	for _, action := range []string{"sts:AssumeRole", "sts:SetSourceIdentity"} {
		if !hasAction(assume.Action, action) {
			t.Fatalf("trust statement missing %s: %+v", action, assume)
		}
	}
	for _, key := range []string{
		"sts:SourceIdentity",
		"aws:RequestTag/skiff.dev/business-justification",
		"aws:RequestTag/skiff.dev/trace-id",
	} {
		if assume.Condition["Null"][key] != "false" || assume.Condition["StringLike"][key] == "" {
			t.Fatalf("trust statement does not require %s: %+v", key, assume.Condition)
		}
	}

	tags := statementBySid(doc, "AllowAuditableEscalationTags")
	if tags == nil || !hasAction(tags.Action, "sts:TagSession") {
		t.Fatalf("missing auditable session-tag trust statement: %+v", doc.Statement)
	}
}

func TestStatefulPoliciesAreLeastPrivilege(t *testing.T) {
	runner := policy.RunnerPolicy("skiff-state-prod", "alias/skiff-prod-state")
	if !statementHasResource(runner, "ReadServiceControlAndReleases", "arn:aws:s3:::skiff-state-prod/stateful/*/members/*/control.json") {
		t.Fatalf("runner policy must read stateful member controls: %+v", runner.Statement)
	}

	deployer := policy.DeployerPolicy("skiff-state-prod", "alias/skiff-prod-state")
	for _, want := range []struct {
		sid      string
		resource string
	}{
		{sid: "CreateImmutableState", resource: "arn:aws:s3:::skiff-state-prod/stateful/*/members/*/control.json"},
		{sid: "CASControlState", resource: "arn:aws:s3:::skiff-state-prod/stateful/*/members/*/control.json"},
	} {
		if !statementHasResource(deployer, want.sid, want.resource) {
			t.Fatalf("deployer policy %s missing %s: %+v", want.sid, want.resource, deployer.Statement)
		}
	}
}

func TestLinterBlocksDangerousPolicyRegressions(t *testing.T) {
	doc := policy.Document{
		Version: "2012-10-17",
		Statement: []policy.Statement{
			{
				Sid:      "AllowAllS3",
				Effect:   "Allow",
				Action:   "s3:*",
				Resource: "arn:aws:s3:::skiff-state-prod/*",
			},
			{
				Sid:      "WildcardResource",
				Effect:   "Allow",
				Action:   "s3:GetObject",
				Resource: "*",
			},
			{
				Sid:      "DeleteState",
				Effect:   "Allow",
				Action:   []string{"s3:DeleteObject"},
				Resource: "arn:aws:s3:::skiff-state-prod/services/*/control.json",
			},
			{
				Sid:      "UnconditionedWrite",
				Effect:   "Allow",
				Action:   []string{"s3:PutObject"},
				Resource: "arn:aws:s3:::skiff-state-prod/services/*/control.json",
			},
		},
	}

	findings := policy.Lint(doc, policy.LintOptions{Role: policy.RoleStateBucket})
	for _, code := range []string{
		"DANGEROUS_WILDCARD_ACTION",
		"DANGEROUS_WILDCARD_RESOURCE",
		"DELETE_STATE_PRIVILEGE",
		"UNCONDITIONED_STATE_WRITE",
		"MISSING_TLS_DENY",
		"MISSING_KMS_ENCRYPTION_DENY",
	} {
		if !hasFinding(findings, code) {
			t.Fatalf("findings missing %s: %+v", code, findings)
		}
	}
}

func TestExplainCoversEveryStatement(t *testing.T) {
	doc := policy.DeployerPolicy("skiff-state-prod", "alias/skiff-prod-state")
	explanations := policy.Explain(policy.RoleDeployer, doc)
	if len(explanations) != len(doc.Statement) {
		t.Fatalf("explanations = %d, statements = %d", len(explanations), len(doc.Statement))
	}
	for _, explanation := range explanations {
		if explanation.Reason == "" || explanation.Safety == "" || len(explanation.Actions) == 0 {
			t.Fatalf("incomplete explanation: %+v", explanation)
		}
	}
}

func hasFinding(findings []policy.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasAction(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []string:
		for _, item := range typed {
			if item == want {
				return true
			}
		}
	}
	return false
}

func statementHasResource(doc policy.Document, sid, want string) bool {
	for _, statement := range doc.Statement {
		if statement.Sid != sid {
			continue
		}
		switch resources := statement.Resource.(type) {
		case string:
			return resources == want
		case []string:
			for _, resource := range resources {
				if resource == want {
					return true
				}
			}
		}
	}
	return false
}

func statementBySid(doc policy.Document, sid string) *policy.Statement {
	for i := range doc.Statement {
		if doc.Statement[i].Sid == sid {
			return &doc.Statement[i]
		}
	}
	return nil
}
