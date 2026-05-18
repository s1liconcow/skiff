package fake_test

import (
	"context"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestPlanIncludesStatefulResources(t *testing.T) {
	doc, result, err := spec.Parse([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: ledger
  env: dev
stateful:
  replicas: 1
  volume:
    size: 10Gi
  recipe:
    name: sqlite
    config:
      runtime:
        health:
          path: /healthz
          port: 9000
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if !result.OK {
		t.Fatalf("spec invalid: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plan, err := fake.New().Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantKinds := map[string]bool{
		ir.ResourceKindStatefulGroup:  false,
		ir.ResourceKindStatefulMember: false,
		ir.ResourceKindStatefulVolume: false,
		ir.ResourceKindStatefulDNS:    false,
		ir.ResourceKindStatefulRecipe: false,
		ir.ResourceKindSnapshotPolicy: false,
		ir.ResourceKindUpdatePolicy:   false,
	}
	for _, change := range plan.Resources {
		if _, ok := wantKinds[change.Kind]; ok {
			wantKinds[change.Kind] = true
		}
		if change.Kind == ir.ResourceKindStatefulMember {
			if change.Action != provider.ActionCreate || change.ProviderID == "" || change.Tags[ir.TagMemberOrdinal] != "0" {
				t.Fatalf("stateful member fake plan missing details: %+v", change)
			}
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Fatalf("fake plan missing stateful kind %s: %+v", kind, plan.Resources)
		}
	}
}
