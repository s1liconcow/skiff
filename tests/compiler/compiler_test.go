package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

func TestCompileServiceGoldenIR(t *testing.T) {
	graph := compileExample(t)
	body, err := canonical.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal IR: %v", err)
	}
	goldenPath := filepath.Join("..", "golden", "compiler", "service-ir.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(want)) {
		t.Fatalf("IR golden mismatch\nwant:\n%s\n\ngot:\n%s", string(want), string(body))
	}
	if strings.Contains(string(body), "arn:") {
		t.Fatalf("provider-specific ARN leaked into common IR:\n%s", string(body))
	}
}

func TestCompileServiceResourceMetadata(t *testing.T) {
	graph := compileExample(t)
	if len(graph.Resources.RuntimeManifests) != 1 || !graph.Resources.RuntimeManifests[0].Metrics.Enabled || graph.Resources.RuntimeManifests[0].Metrics.Path != "/metrics" {
		t.Fatalf("runtime manifest missing app metrics endpoint: %+v", graph.Resources.RuntimeManifests)
	}
	for _, meta := range resourceMetas(graph) {
		if meta.LogicalID == "" || meta.Kind == "" || meta.Name == "" {
			t.Fatalf("resource metadata missing identity: %+v", meta)
		}
		for _, tag := range []string{ir.TagService, ir.TagEnv, ir.TagManaged, ir.TagGraph} {
			if meta.Tags[tag] == "" {
				t.Fatalf("%s missing required tag %s: %+v", meta.LogicalID, tag, meta.Tags)
			}
		}
		if len(meta.Source) == 0 {
			t.Fatalf("%s missing source refs", meta.LogicalID)
		}
	}
}

func TestSemanticDiffIgnoresResourceOrdering(t *testing.T) {
	graph := *compileExample(t)
	extra := graph.Resources.LogConfigs[0]
	extra.Meta.LogicalID = "logs:payments-api-extra"
	extra.Meta.Name = "skiff-prod-payments-api-extra-logs"

	left := graph
	left.Resources.LogConfigs = []ir.LogConfig{graph.Resources.LogConfigs[0], extra}
	right := graph
	right.Resources.LogConfigs = []ir.LogConfig{extra, graph.Resources.LogConfigs[0]}

	diff, err := ir.SemanticDiff(left, right)
	if err != nil {
		t.Fatalf("semantic diff: %v", err)
	}
	if diff.Changed {
		t.Fatalf("reorder-only graph diff reported changes: %+v", diff)
	}
}

func TestCompileMultiRegionStack(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: MultiRegionStack
metadata:
  name: orders
  env: prod
multiRegion:
  primaryRegion: us-west-2
  secondaryRegions:
    - us-east-1
  service:
    name: api
    artifact:
      type: oci
      ref: registry.example.com/orders-api@sha256:abc123
    runtime:
      port: 8080
      health:
        path: /healthz
  database:
    name: db
    engine: postgres
    version: "16"
    size: small
  trafficPolicy:
    mode: weighted-dns
    host: orders.example.com
    weights:
      - region: us-west-2
        weight: 100
      - region: us-east-1
        weight: 0
  databaseReplication:
    mode: async
    maxReplicaLag: 30s
  failoverPolicy:
    freezeWrites: true
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if graph.Service != "orders" || len(graph.Resources.GlobalTraffic) != 1 {
		t.Fatalf("graph target/global traffic mismatch: %+v", graph)
	}
	if len(graph.Resources.ManagedDatabases) != 2 {
		t.Fatalf("managed databases = %+v, want primary and replica", graph.Resources.ManagedDatabases)
	}
	roles := map[string]string{}
	for _, db := range graph.Resources.ManagedDatabases {
		roles[db.Region] = db.Role
		if db.Meta.Tags[ir.TagRegion] != db.Region {
			t.Fatalf("database %s missing region tag: %+v", db.Meta.LogicalID, db.Meta.Tags)
		}
	}
	if roles["us-west-2"] != "primary" || roles["us-east-1"] != "replica" {
		t.Fatalf("database roles = %+v", roles)
	}
	traffic := graph.Resources.GlobalTraffic[0]
	if traffic.PrimaryRegion != "us-west-2" || len(traffic.Regions) != 2 || traffic.Regions[1].Weight != 0 {
		t.Fatalf("traffic policy = %+v", traffic)
	}
}

func compileExample(t *testing.T) *ir.Graph {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	doc, err := spec.LoadFile(path, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load example spec: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile example spec: %v", err)
	}
	return graph
}

func resourceMetas(graph *ir.Graph) []ir.ResourceMeta {
	var out []ir.ResourceMeta
	for _, resource := range graph.Resources.WorkloadIdentities {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.IAMRoles {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.SecurityGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.LogConfigs {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.MetricConfigs {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.TargetGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.Listeners {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.InstanceTemplates {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.AutoscalingGroups {
		out = append(out, resource.Meta)
	}
	for _, resource := range graph.Resources.RuntimeManifests {
		out = append(out, resource.Meta)
	}
	return out
}
