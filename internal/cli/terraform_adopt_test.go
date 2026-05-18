package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/adopt"
	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestTerraformGenerateAndAdoptJSON(t *testing.T) {
	clearSkiffEnv(t)
	outDir := filepath.Join(t.TempDir(), "tf")
	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"terraform", "generate", specPath,
		"--out", outDir,
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
		"--format", "json",
		"--trace-id", "tr_tf_generate",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("generate exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var generated terraformGenerateOutput
	if err := json.Unmarshal(stdout.Bytes(), &generated); err != nil {
		t.Fatalf("decode generate output: %v\n%s", err, stdout.String())
	}
	if !generated.OK || generated.TraceID != "tr_tf_generate" || generated.Result.ResourceCount == 0 {
		t.Fatalf("unexpected generate output: %+v", generated)
	}
	for _, name := range []string{"main.tf", "variables.tf", "outputs.tf", "README.md"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("missing generated %s: %v", name, err)
		}
	}

	mapping := terraformMappingForSpec(t, specPath)
	mappingPath := filepath.Join(t.TempDir(), "skiff_resources.json")
	body, err := json.Marshal(mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mappingPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	code = Run("skiff", []string{
		"adopt", "terraform", mappingPath,
		"--direct",
		"--state", "file://" + stateRoot,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_tf_adopt",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("adopt exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var adopted adoptTerraformOutput
	if err := json.Unmarshal(stdout.Bytes(), &adopted); err != nil {
		t.Fatalf("decode adopt output: %v\n%s", err, stdout.String())
	}
	if !adopted.OK || adopted.TraceID != "tr_tf_adopt" || len(adopted.Result.Resources) != len(mapping.Resources) {
		t.Fatalf("unexpected adopt output: %+v", adopted)
	}
	if adopted.Result.Resources[0].Record.Ownership == nil || adopted.Result.Resources[0].Record.Ownership.Mode != adopt.OwnershipTerraformInfraSkiffRelease {
		t.Fatalf("ownership not recorded: %+v", adopted.Result.Resources[0].Record)
	}
}

func TestDeployUsesTerraformOwnedInfrastructure(t *testing.T) {
	clearSkiffEnv(t)
	stateRoot := t.TempDir()
	store, err := file.New(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	mapping := terraformMappingForSpec(t, specPath)
	if _, err := adopt.RecordTerraform(context.Background(), store, mapping, adopt.RecordOptions{}); err != nil {
		t.Fatalf("record terraform resources: %v", err)
	}
	signingSeed := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("T", ed25519.SeedSize)))
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + stateRoot,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--release-id", "rel_tf_owned",
		"--operation-id", "op_tf_owned",
		"--signing-seed-base64", signingSeed,
		"--format", "json",
		"--trace-id", "tr_tf_owned",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("deploy exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var deployed deployOutput
	if err := json.Unmarshal(stdout.Bytes(), &deployed); err != nil {
		t.Fatalf("decode deploy output: %v\n%s", err, stdout.String())
	}
	if !deployed.OK || !deployed.Result.OK || deployed.Result.ReleaseID != "rel_tf_owned" {
		t.Fatalf("unexpected deploy output: %+v", deployed)
	}
	if len(deployed.Result.Plan.Resources) == 0 {
		t.Fatalf("deploy plan was empty")
	}
	for _, change := range deployed.Result.Plan.Resources {
		if change.Action != "no-op" || change.ProviderID == "" {
			t.Fatalf("terraform-owned deploy should not reapply infra: %+v", change)
		}
	}
}

func TestTerraformAdoptRecordsStatefulResources(t *testing.T) {
	stateRoot := t.TempDir()
	store, err := file.New(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	mapping := terraformMappingForSpec(t, filepath.Join("..", "..", "examples", "stateful", "jetstream", "skiff.yaml"))
	result, err := adopt.RecordTerraform(context.Background(), store, mapping, adopt.RecordOptions{})
	if err != nil {
		t.Fatalf("record stateful terraform resources: %v", err)
	}
	for _, kind := range []string{"ec2-instance", "ebs-volume", "ebs-volume-attachment", "route53-record", "snapshot-policy", "fencing-policy"} {
		if !recordedKind(result.Resources, kind) {
			t.Fatalf("missing recorded stateful kind %s: %+v", kind, result.Resources)
		}
	}
	for _, resource := range result.Resources {
		if resource.Record.Ownership == nil || resource.Record.Ownership.ManagedBy != "terraform" {
			t.Fatalf("stateful resource missing terraform ownership: %+v", resource.Record)
		}
		if resource.Record.Provider.Kind == "ebs-volume" && resource.Record.Tags["skiff.dev/stateful-group"] == "" {
			t.Fatalf("stateful resource missing stateful ownership tag: %+v", resource.Record)
		}
	}
}

func terraformMappingForSpec(t *testing.T, path string) adopt.TerraformMapping {
	t.Helper()
	doc, err := spec.LoadFile(path, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	resources, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return adopt.MappingFromAWSResources(resources, adopt.OwnershipTerraformInfraSkiffRelease)
}

func recordedKind(resources []adopt.RecordedResource, kind string) bool {
	for _, resource := range resources {
		if resource.Record.Provider.Kind == kind {
			return true
		}
	}
	return false
}
