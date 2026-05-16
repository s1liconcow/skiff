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
