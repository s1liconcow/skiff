package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/explain"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestInitStackAPIDatabaseGeneratesGoldenTemplate(t *testing.T) {
	clearSkiffEnv(t)
	dir := filepath.Join(t.TempDir(), "orders")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"init", "stack", "api-database", "orders",
		"--dir", dir,
		"--format", "json",
		"--trace-id", "tr_init_stack",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got initStackOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("init output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_init_stack" || got.Recipe != "api-database" || got.Name != "orders" || len(got.Files) != 3 {
		t.Fatalf("unexpected init output: %+v", got)
	}

	specPath := filepath.Join(dir, "skiff.yaml")
	body, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read generated spec: %v", err)
	}
	golden, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "init", "api-database-skiff.yaml"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(golden)) {
		t.Fatalf("generated spec mismatch\nwant:\n%s\n\ngot:\n%s", string(golden), string(body))
	}

	doc, err := spec.LoadFile(specPath, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("generated spec did not decode: %v", err)
	}
	if result := spec.Validate(*doc); !result.OK {
		t.Fatalf("generated spec did not validate: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("generated spec did not compile: %v", err)
	}
	if graph.Service != "orders-api" || len(graph.Resources.ManagedDatabases) != 1 {
		t.Fatalf("generated stack compiled to unexpected graph: %+v", graph)
	}
}

func TestInitStackUsesSkiffEnvDefault(t *testing.T) {
	clearSkiffEnv(t)
	t.Setenv("SKIFF_ENV", "quickstart")
	dir := filepath.Join(t.TempDir(), "orders")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"init", "stack", "api-database", "orders",
		"--dir", dir,
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	doc, err := spec.LoadFile(filepath.Join(dir, "skiff.yaml"), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("generated spec did not decode: %v", err)
	}
	if doc.Metadata.Env != "quickstart" {
		t.Fatalf("generated env = %q, want quickstart", doc.Metadata.Env)
	}
}

func TestInitStackAPISQLiteGeneratesStatefulTemplate(t *testing.T) {
	clearSkiffEnv(t)
	dir := filepath.Join(t.TempDir(), "orders")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"init", "stack", "api-sqlite", "orders",
		"--dir", dir,
		"--format", "json",
		"--trace-id", "tr_init_sqlite_stack",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got initStackOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("init output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_init_sqlite_stack" || got.Recipe != "api-sqlite" || got.Name != "orders" || len(got.Files) != 4 {
		t.Fatalf("unexpected init output: %+v", got)
	}

	specPath := filepath.Join(dir, "skiff.yaml")
	body, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read generated spec: %v", err)
	}
	golden, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "init", "api-sqlite-skiff.yaml"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(golden)) {
		t.Fatalf("generated spec mismatch\nwant:\n%s\n\ngot:\n%s", string(golden), string(body))
	}

	doc, err := spec.LoadFile(specPath, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("generated spec did not decode: %v", err)
	}
	if result := spec.Validate(*doc); !result.OK {
		t.Fatalf("generated spec did not validate: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("generated spec did not compile: %v", err)
	}
	if graph.Service != "orders-api" || len(graph.Resources.StatefulGroups) != 1 || len(graph.Resources.StatefulVolumes) != 1 {
		t.Fatalf("generated stack compiled to unexpected graph: %+v", graph)
	}
	if graph.Resources.StatefulRecipes[0].Env["SQLITE_PATH"] != "/var/lib/skiff/sqlite/app.db" {
		t.Fatalf("generated sqlite path missing: %+v", graph.Resources.StatefulRecipes[0])
	}
}

func TestInitStackAPISlateDBGeneratesObjectStoreTemplate(t *testing.T) {
	clearSkiffEnv(t)
	dir := filepath.Join(t.TempDir(), "orders")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"init", "stack", "api-slatedb", "orders",
		"--dir", dir,
		"--format", "json",
		"--trace-id", "tr_init_slatedb_stack",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got initStackOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("init output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_init_slatedb_stack" || got.Recipe != "api-slatedb" || got.Name != "orders" || len(got.Files) != 4 {
		t.Fatalf("unexpected init output: %+v", got)
	}

	specPath := filepath.Join(dir, "skiff.yaml")
	body, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read generated spec: %v", err)
	}
	golden, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "init", "api-slatedb-skiff.yaml"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(golden)) {
		t.Fatalf("generated spec mismatch\nwant:\n%s\n\ngot:\n%s", string(golden), string(body))
	}

	doc, err := spec.LoadFile(specPath, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("generated spec did not decode: %v", err)
	}
	if result := spec.Validate(*doc); !result.OK {
		t.Fatalf("generated spec did not validate: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("generated spec did not compile: %v", err)
	}
	if graph.Service != "orders-api" || len(graph.Resources.ObjectStores) != 1 || len(graph.Resources.ObjectStoreBindings) != 1 {
		t.Fatalf("generated stack compiled to unexpected graph: %+v", graph)
	}
	if graph.Resources.RuntimeManifests[0].Env["SLATEDB_URI"] != "s3://orders-slatedb-prod/slatedb/orders" {
		t.Fatalf("generated SlateDB URI missing: %+v", graph.Resources.RuntimeManifests[0].Env)
	}
	appBody, err := os.ReadFile(filepath.Join(dir, "app.py"))
	if err != nil {
		t.Fatalf("read generated app: %v", err)
	}
	if !strings.Contains(string(appBody), "from slatedb.uniffi import DbBuilder, ObjectStore") || !strings.Contains(string(appBody), "await db.put") {
		t.Fatalf("generated app does not use SlateDB: %s", string(appBody))
	}
	dockerfile, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read generated Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "pip install --no-cache-dir slatedb") {
		t.Fatalf("generated Dockerfile does not install SlateDB: %s", string(dockerfile))
	}
}

func TestInitStackRefusesOverwriteWithoutYes(t *testing.T) {
	clearSkiffEnv(t)
	dir := filepath.Join(t.TempDir(), "orders")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"init", "stack", "api-database", "orders", "--dir", dir}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("initial init failed: code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run("skiff", []string{"init", "stack", "api-database", "orders", "--dir", dir, "--format", "json"}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("second init code=%d, want user error; stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "already exists") {
		t.Fatalf("overwrite error missing from JSON output: %s", stdout.String())
	}
}

func TestAPIDatabaseStackPlanAndExplainExposeDatabaseResources(t *testing.T) {
	clearSkiffEnv(t)
	specPath := filepath.Join("..", "..", "examples", "stacks", "api-database", "skiff.yaml")
	var planOut, explainOut, stderr bytes.Buffer
	code := Run("skiff", []string{
		"plan", specPath,
		"--format", "json",
		"--trace-id", "tr_stack_plan",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
	}, &planOut, &stderr)
	if code != ExitSuccess {
		t.Fatalf("plan exit=%d stderr=%s stdout=%s", code, stderr.String(), planOut.String())
	}
	var plan planOutput
	if err := json.Unmarshal(planOut.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, planOut.String())
	}
	if !planHasResourceKind(plan.Plan.Resources, "rds-db-instance") || !planHasResourceKind(plan.Plan.Resources, "secretsmanager-secret") {
		t.Fatalf("stack plan missing database resources: %+v", plan.Plan.Resources)
	}

	stderr.Reset()
	code = Run("skiff", []string{
		"explain", specPath,
		"--format", "json",
		"--trace-id", "tr_stack_explain",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
	}, &explainOut, &stderr)
	if code != ExitSuccess {
		t.Fatalf("explain exit=%d stderr=%s stdout=%s", code, stderr.String(), explainOut.String())
	}
	var explained explainOutput
	if err := json.Unmarshal(explainOut.Bytes(), &explained); err != nil {
		t.Fatalf("decode explain: %v\n%s", err, explainOut.String())
	}
	if !explainHasPrimitive(explained.Result.Resources, "RDS managed database") || !explainHasPrimitive(explained.Result.Resources, "Secrets Manager secret") {
		t.Fatalf("stack explain missing database primitives: %+v", explained.Result.Resources)
	}
}

func TestAPISlateDBStackPlanAndExplainExposeObjectStoreResources(t *testing.T) {
	clearSkiffEnv(t)
	specPath := filepath.Join("..", "..", "examples", "stacks", "api-slatedb", "skiff.yaml")
	var planOut, explainOut, stderr bytes.Buffer
	code := Run("skiff", []string{
		"plan", specPath,
		"--format", "json",
		"--trace-id", "tr_slatedb_stack_plan",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
	}, &planOut, &stderr)
	if code != ExitSuccess {
		t.Fatalf("plan exit=%d stderr=%s stdout=%s", code, stderr.String(), planOut.String())
	}
	var plan planOutput
	if err := json.Unmarshal(planOut.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, planOut.String())
	}
	if !planHasResourceKind(plan.Plan.Resources, "s3-bucket") || planHasResourceKind(plan.Plan.Resources, "rds-db-instance") {
		t.Fatalf("SlateDB stack plan missing object store or included RDS: %+v", plan.Plan.Resources)
	}

	stderr.Reset()
	code = Run("skiff", []string{
		"explain", specPath,
		"--format", "json",
		"--trace-id", "tr_slatedb_stack_explain",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
	}, &explainOut, &stderr)
	if code != ExitSuccess {
		t.Fatalf("explain exit=%d stderr=%s stdout=%s", code, stderr.String(), explainOut.String())
	}
	var explained explainOutput
	if err := json.Unmarshal(explainOut.Bytes(), &explained); err != nil {
		t.Fatalf("decode explain: %v\n%s", err, explainOut.String())
	}
	if !explainHasPrimitive(explained.Result.Resources, "S3 bucket") || explainHasPrimitive(explained.Result.Resources, "RDS managed database") {
		t.Fatalf("SlateDB stack explain missing object store or included RDS: %+v", explained.Result.Resources)
	}
}

func planHasResourceKind(resources []provider.PlannedChange, kind string) bool {
	for _, resource := range resources {
		if resource.Kind == kind {
			return true
		}
	}
	return false
}

func explainHasPrimitive(resources []explain.ResourceExplanation, primitive string) bool {
	for _, resource := range resources {
		if resource.CloudPrimitive == primitive {
			return true
		}
	}
	return false
}
