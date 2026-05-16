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
