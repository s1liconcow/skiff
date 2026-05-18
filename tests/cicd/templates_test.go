package cicd_test

import (
	"os"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/cicd"
	"gopkg.in/yaml.v3"
)

func TestGeneratedTemplatesContainRequiredFlow(t *testing.T) {
	for _, target := range cicd.Targets() {
		target := target
		t.Run(target, func(t *testing.T) {
			generated, err := cicd.Generate(target, cicd.Options{Service: "payments-api"})
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			var parsed any
			if err := yaml.Unmarshal([]byte(generated.Content), &parsed); err != nil {
				t.Fatalf("generated YAML is invalid: %v\n%s", err, generated.Content)
			}
			for _, want := range []string{
				"skiff validate",
				"skiff contract test",
				"skiff plan",
				"skiff release candidate create",
				"skiff deploy",
				"skiff release promote",
				"--artifact-digest",
				"$IMAGE_REPO@$IMAGE_DIGEST",
				"--direct",
				"--api",
				"AWS_ROLE_TO_ASSUME",
			} {
				if !strings.Contains(generated.Content, want) {
					t.Fatalf("generated template missing %q:\n%s", want, generated.Content)
				}
			}
			if strings.Contains(generated.Content, ":latest") {
				t.Fatalf("generated template uses a mutable latest tag:\n%s", generated.Content)
			}
		})
	}
}

func TestDocsMentionGeneratedCommands(t *testing.T) {
	body, err := os.ReadFile("../../docs/adoption/cicd.md")
	if err != nil {
		t.Fatalf("read docs: %v", err)
	}
	doc := string(body)
	for _, want := range []string{
		"skiff ci generate github-actions",
		"skiff ci generate gitlab",
		"skiff ci generate buildkite",
		"skiff validate",
		"skiff contract test",
		"skiff plan",
		"skiff release candidate create",
		"skiff deploy",
		"skiff release promote",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("docs missing %q", want)
		}
	}
}
