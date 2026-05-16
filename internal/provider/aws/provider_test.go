package aws_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

func TestProviderConstructsFromConfig(t *testing.T) {
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"})
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	if p.Name() != "aws" {
		t.Fatalf("Name() = %q, want aws", p.Name())
	}
	if p.Config().Region != "us-west-2" {
		t.Fatalf("region = %q, want us-west-2", p.Config().Region)
	}

	var _ provider.Provider = p
}

func TestProviderRequiresRegion(t *testing.T) {
	_, err := aws.NewFromConfig(config.Config{})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("NewFromConfig() error = %T, want provider.Error", err)
	}
	if providerErr.Code != provider.CodeInvalidConfig {
		t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeInvalidConfig)
	}
}

func TestUnsupportedOperationsAreClassified(t *testing.T) {
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Apply(context.Background(), &provider.Plan{})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("Apply() error = %T, want provider.Error", err)
	}
	if providerErr.Code != provider.CodeUnsupported {
		t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeUnsupported)
	}
}

func TestPlanCompiledGraph(t *testing.T) {
	doc, err := spec.LoadFile(filepath.Join("..", "..", "..", "examples", "service", "skiff.yaml"), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile spec: %v", err)
	}
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Provider != "aws" || plan.Service != graph.Service || plan.Env != graph.Env {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if len(plan.Resources) == 0 {
		t.Fatal("expected planned resources")
	}
	for _, resource := range plan.Resources {
		if resource.Name == "" || resource.Action != "ensure" {
			t.Fatalf("invalid planned resource: %#v", resource)
		}
	}
}
