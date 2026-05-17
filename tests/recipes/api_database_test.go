package recipes_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

func TestAPIDatabaseRecipeCompilesAndPlansManagedResources(t *testing.T) {
	doc, err := spec.LoadFile(filepath.Join("..", "..", "examples", "stacks", "api-database", "skiff.yaml"), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load recipe spec: %v", err)
	}
	if result := spec.Validate(*doc); !result.OK {
		t.Fatalf("recipe spec did not validate: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile recipe: %v", err)
	}
	if graph.Service != "orders-api" {
		t.Fatalf("graph service = %q, want orders-api", graph.Service)
	}
	if len(graph.Resources.ManagedDatabases) != 1 || graph.Resources.ManagedDatabases[0].ConnectionSecretRef == "" {
		t.Fatalf("managed database not compiled with connection secret: %+v", graph.Resources.ManagedDatabases)
	}
	if len(graph.Resources.DatabaseSecrets) != 1 || len(graph.Resources.DatabaseBindings) != 1 {
		t.Fatalf("database secret/binding missing: secrets=%+v bindings=%+v", graph.Resources.DatabaseSecrets, graph.Resources.DatabaseBindings)
	}
	runtime := graph.Resources.RuntimeManifests[0]
	if runtime.Env["DATABASE_URL"] == "" || runtime.Env["DATABASE_URL"] != graph.Resources.DatabaseSecrets[0].Ref {
		t.Fatalf("runtime DATABASE_URL not wired to secret reference: %+v", runtime.Env)
	}
	if !serviceHasDatabaseEgress(graph.Resources.SecurityGroups) {
		t.Fatalf("service security group missing database egress: %+v", graph.Resources.SecurityGroups)
	}

	lowered, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("lower aws resources: %v", err)
	}
	if len(lowered.Databases) != 1 || !lowered.Databases[0].StorageEncrypted || lowered.Databases[0].BackupRetentionDays != 7 || !lowered.Databases[0].DeletionProtection {
		t.Fatalf("lowered database missing safe defaults: %+v", lowered.Databases)
	}
	if len(lowered.Secrets) != 1 || lowered.Secrets[0].Ref == "" {
		t.Fatalf("lowered secret missing: %+v", lowered.Secrets)
	}
	if !policyMentions(lowered.IAMRoles[0].InlinePolicy, "secretsmanager:GetSecretValue") || !policyMentions(lowered.IAMRoles[0].InlinePolicy, "skiff-prod-orders-db-database-url-secret") {
		t.Fatalf("workload IAM policy does not include managed database secret: %+v", lowered.IAMRoles[0].InlinePolicy)
	}
	if !loweredHasDatabaseSecurityRule(lowered.SecurityGroups) {
		t.Fatalf("lowered security groups missing database rules: %+v", lowered.SecurityGroups)
	}

	awsProvider, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}
	plan, err := awsProvider.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan recipe: %v", err)
	}
	for _, want := range []string{aws.ResourceKindRDSInstance, aws.ResourceKindSecret, aws.ResourceKindSecurityGroup, aws.ResourceKindAutoScalingGroup} {
		if !planHasKind(plan.Resources, want) {
			t.Fatalf("plan missing %s: %+v", want, plan.Resources)
		}
	}
}

func TestDoctorDetectsUnavailableManagedDatabase(t *testing.T) {
	result, err := doctor.Diagnose(context.Background(), servicestatus.Result{
		Env:      "prod",
		Provider: "aws",
		Region:   "us-west-2",
		Source:   "direct",
		Services: []servicestatus.Service{{
			Service:        "orders-api",
			Env:            "prod",
			DesiredRelease: "rel_02",
			StableRelease:  "rel_02",
			Health:         "degraded",
			Capacity:       servicestatus.DependencyStatus{Status: "configured", Source: "capacity", ProviderID: "asg-123"},
			TargetHealth:   servicestatus.DependencyStatus{Status: "configured", Source: "target_health", ProviderID: "tg-123"},
			Database:       servicestatus.DependencyStatus{Status: "unknown", Source: "database", Summary: "RDS managed database has not been observed"},
			Logs:           servicestatus.DependencyStatus{Status: "configured", Source: "logs", ProviderID: "/skiff/prod/orders-api"},
			Metrics:        servicestatus.DependencyStatus{Status: "configured", Source: "metrics", ProviderID: "Skiff/prod/orders-api"},
			Resources: []servicestatus.ResourceSummary{{
				Kind:        aws.ResourceKindSecret,
				LogicalKind: aws.ResourceKindSecret,
				ProviderID:  "arn:aws:secretsmanager:us-west-2:123456789012:secret:orders",
			}},
		}},
	}, doctor.Options{Service: "orders-api", TraceID: "tr_db_doctor"})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if !hasFinding(result.Findings, "DATABASE_AVAILABILITY_UNKNOWN") {
		t.Fatalf("database finding missing: %+v", result.Findings)
	}
	if !hasAction(result.RecommendedActions, "orders-api_inspect_status") {
		t.Fatalf("database finding missing inspect status action: %+v", result.RecommendedActions)
	}
}

func serviceHasDatabaseEgress(groups []ir.SecurityGroup) bool {
	for _, group := range groups {
		if group.Meta.LogicalID != "security-group:orders-api" {
			continue
		}
		for _, rule := range group.Rules {
			if rule.Direction == "egress" && rule.Destination == "security-group:orders-db" && rule.FromPort == 5432 {
				return true
			}
		}
	}
	return false
}

func loweredHasDatabaseSecurityRule(groups []aws.SecurityGroupAWS) bool {
	for _, group := range groups {
		for _, rule := range group.Ingress {
			if rule.SourceSecurityGroupRef == "security-group:orders-api" && rule.FromPort == 5432 {
				return true
			}
		}
		for _, rule := range group.Egress {
			if rule.DestinationSecurityGroupRef == "security-group:orders-db" && rule.FromPort == 5432 {
				return true
			}
		}
	}
	return false
}

func planHasKind(resources []provider.PlannedChange, kind string) bool {
	for _, resource := range resources {
		if resource.Kind == kind {
			return true
		}
	}
	return false
}

func policyMentions(policy aws.PolicyDocument, want string) bool {
	for _, statement := range policy.Statement {
		for _, action := range statement.Action {
			if action == want {
				return true
			}
		}
		for _, resource := range statement.Resource {
			if strings.Contains(resource, want) {
				return true
			}
		}
	}
	return false
}

func hasFinding(findings []doctor.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasAction(actions []doctor.RecommendedAction, id string) bool {
	for _, action := range actions {
		if action.ID == id {
			return true
		}
	}
	return false
}
