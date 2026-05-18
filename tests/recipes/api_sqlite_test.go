package recipes_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestAPISQLiteRecipeCompilesToSingleMemberStatefulGroup(t *testing.T) {
	doc, err := spec.LoadFile(filepath.Join("..", "..", "examples", "stacks", "api-sqlite", "skiff.yaml"), spec.DecodeOptions{})
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
	if len(graph.Resources.StatefulGroups) != 1 || len(graph.Resources.StatefulMembers) != 1 || len(graph.Resources.StatefulVolumes) != 1 {
		t.Fatalf("stateful resources missing: %+v", graph.Resources)
	}
	volume := graph.Resources.StatefulVolumes[0]
	if volume.MountPath != "/var/lib/skiff/sqlite" || !volume.Encrypted {
		t.Fatalf("sqlite volume not durable/encrypted: %+v", volume)
	}
	recipe := graph.Resources.StatefulRecipes[0]
	if recipe.Name != "sqlite-api-single" || recipe.Artifact.Ref == "" || recipe.HealthCheck.Port != 8080 || !recipe.Metrics.Enabled {
		t.Fatalf("sqlite recipe runtime not compiled: %+v", recipe)
	}
	if recipe.Env["SQLITE_PATH"] != "/var/lib/skiff/sqlite/app.db" {
		t.Fatalf("sqlite path not carried into recipe env: %+v", recipe.Env)
	}
	if !graph.Resources.SnapshotPolicies[0].Enabled || graph.Resources.SnapshotPolicies[0].Retention != "7d" {
		t.Fatalf("snapshot policy missing: %+v", graph.Resources.SnapshotPolicies[0])
	}

	lowered, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("lower aws resources: %v", err)
	}
	if len(lowered.Databases) != 0 || len(lowered.Secrets) != 0 {
		t.Fatalf("local sqlite recipe should not lower managed database resources: db=%+v secrets=%+v", lowered.Databases, lowered.Secrets)
	}
	if len(lowered.StatefulMembers) != 1 || len(lowered.EBSVolumes) != 1 || len(lowered.VolumeAttachments) != 1 {
		t.Fatalf("stateful AWS resources missing: %+v", lowered)
	}
	if !lowered.EBSVolumes[0].Encrypted || lowered.EBSVolumes[0].MountPath != "/var/lib/skiff/sqlite" || lowered.EBSVolumes[0].DeleteOnDestroy {
		t.Fatalf("sqlite EBS volume is not safe by default: %+v", lowered.EBSVolumes[0])
	}

	awsProvider, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}
	plan, err := awsProvider.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan recipe: %v", err)
	}
	for _, want := range []string{aws.ResourceKindEC2Instance, aws.ResourceKindEBSVolume, aws.ResourceKindEBSAttachment, aws.ResourceKindSnapshotPolicy, aws.ResourceKindFencingPolicy} {
		if !planHasKind(plan.Resources, want) {
			t.Fatalf("plan missing %s: %+v", want, plan.Resources)
		}
	}
	if planHasKind(plan.Resources, aws.ResourceKindRDSInstance) {
		t.Fatalf("local sqlite plan should not include RDS: %+v", plan.Resources)
	}
}
