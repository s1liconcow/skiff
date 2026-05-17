package terraform_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	terraformrender "github.com/s1liconcow/skiff/internal/terraform"
)

func TestRenderAWSServiceTerraformMinimalAndIngress(t *testing.T) {
	minimal := renderFixture(t, filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml"))
	minimalMain := minimal.Files["main.tf"]
	for _, want := range []string{
		`resource "aws_launch_template"`,
		`resource "aws_autoscaling_group"`,
		`output "skiff_resources"`,
		`ownership_mode = "terraform-infra-skiff-release"`,
	} {
		if !strings.Contains(minimalMain+minimal.Files["outputs.tf"], want) {
			t.Fatalf("minimal Terraform missing %q\nmain.tf:\n%s\noutputs.tf:\n%s", want, minimalMain, minimal.Files["outputs.tf"])
		}
	}
	if strings.Contains(minimalMain, `resource "aws_lb_listener_rule"`) {
		t.Fatalf("private minimal service should not render listener rule:\n%s", minimalMain)
	}
	if !balancedBraces(minimalMain) || !balancedBraces(minimal.Files["outputs.tf"]) {
		t.Fatalf("generated minimal Terraform has unbalanced braces")
	}

	ingress := renderFixture(t, filepath.Join("..", "..", "examples", "service", "skiff.yaml"))
	ingressMain := ingress.Files["main.tf"]
	for _, want := range []string{
		`variable "alb_listener_arn"`,
		`resource "aws_lb_listener_rule"`,
		`host_header`,
		`payments.example.com`,
	} {
		if !strings.Contains(ingressMain, want) {
			t.Fatalf("ingress Terraform missing %q\n%s", want, ingressMain)
		}
	}
	if len(ingress.Mapping.Resources) == 0 {
		t.Fatalf("renderer did not expose adoption mapping")
	}
}

func renderFixture(t *testing.T, path string) *terraformrender.Module {
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
	module, err := terraformrender.RenderAWSService(resources, terraformrender.Options{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return module
}

func balancedBraces(value string) bool {
	depth := 0
	for _, r := range value {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}
