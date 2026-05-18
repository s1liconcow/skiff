package recipes_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestAPISlateDBRecipeCompilesAndPlansObjectStore(t *testing.T) {
	doc, err := spec.LoadFile(filepath.Join("..", "..", "examples", "stacks", "api-slatedb", "skiff.yaml"), spec.DecodeOptions{})
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
	if len(graph.Resources.ManagedDatabases) != 0 {
		t.Fatalf("SlateDB recipe should not compile managed databases: %+v", graph.Resources.ManagedDatabases)
	}
	if len(graph.Resources.RuntimeManifests[0].Command) != 2 || graph.Resources.RuntimeManifests[0].Command[1] != "/app/app.py" {
		t.Fatalf("SlateDB recipe should run the generated Python app: %+v", graph.Resources.RuntimeManifests[0].Command)
	}
	if len(graph.Resources.ObjectStores) != 1 || len(graph.Resources.ObjectStoreBindings) != 1 {
		t.Fatalf("object store/binding missing: stores=%+v bindings=%+v", graph.Resources.ObjectStores, graph.Resources.ObjectStoreBindings)
	}
	store := graph.Resources.ObjectStores[0]
	if store.Bucket != "orders-slatedb-prod" || store.Prefix != "slatedb/orders" || store.Purpose != "slatedb" || store.Access != "read-write" || !store.Versioned || !store.Encrypted {
		t.Fatalf("object store not compiled with safe SlateDB defaults: %+v", store)
	}
	runtime := graph.Resources.RuntimeManifests[0]
	if runtime.Env["SLATEDB_URI"] != store.URI || runtime.Env["SLATEDB_TABLE"] != "orders" {
		t.Fatalf("runtime SlateDB env not wired: %+v", runtime.Env)
	}

	lowered, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("lower aws resources: %v", err)
	}
	if len(lowered.ObjectStores) != 1 || lowered.ObjectStores[0].Name != "orders-slatedb-prod" || !lowered.ObjectStores[0].VersioningEnabled || !lowered.ObjectStores[0].EncryptionEnabled {
		t.Fatalf("lowered object store missing: %+v", lowered.ObjectStores)
	}
	if !policyMentions(lowered.IAMRoles[0].InlinePolicy, "s3:PutObject") || !policyMentions(lowered.IAMRoles[0].InlinePolicy, "arn:aws:s3:::orders-slatedb-prod/slatedb/orders/*") {
		t.Fatalf("workload IAM policy does not include scoped SlateDB object-store access: %+v", lowered.IAMRoles[0].InlinePolicy)
	}

	awsProvider, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}
	plan, err := awsProvider.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan recipe: %v", err)
	}
	if !planHasKind(plan.Resources, aws.ResourceKindS3Bucket) {
		t.Fatalf("plan missing %s: %+v", aws.ResourceKindS3Bucket, plan.Resources)
	}
	if planHasKind(plan.Resources, aws.ResourceKindRDSInstance) {
		t.Fatalf("SlateDB recipe should not plan RDS: %+v", plan.Resources)
	}
}

func TestAPISlateDBExampleAppInstallsAndUsesSlateDB(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "examples", "stacks", "api-slatedb", "app.py"))
	if err != nil {
		t.Fatalf("read app: %v", err)
	}
	appBody := string(app)
	for _, want := range []string{
		"from slatedb.uniffi import DbBuilder, ObjectStore",
		"ObjectStore.resolve(slatedb_uri)",
		"await DbBuilder(slatedb_table, store).build()",
		"await db.put",
		"await db.get",
		"await db.shutdown()",
	} {
		if !strings.Contains(appBody, want) {
			t.Fatalf("SlateDB app missing %q:\n%s", want, appBody)
		}
	}

	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "examples", "stacks", "api-slatedb", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "pip install --no-cache-dir slatedb") {
		t.Fatalf("Dockerfile does not install SlateDB:\n%s", string(dockerfile))
	}
}
