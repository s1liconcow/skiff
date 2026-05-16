package aws_test

import (
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestResourceNameUsesDeterministicLimits(t *testing.T) {
	input := aws.NameInput{
		Service:   strings.Repeat("payments-", 12) + "api",
		Env:       "prod",
		Kind:      aws.ResourceKindTargetGroup,
		LogicalID: "target-group:payments-api",
	}
	name, err := aws.ResourceName(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(name) > aws.ResourceNameLimit(aws.ResourceKindTargetGroup) {
		t.Fatalf("target group name length = %d, limit = %d, name = %q", len(name), aws.ResourceNameLimit(aws.ResourceKindTargetGroup), name)
	}

	again, err := aws.ResourceName(input)
	if err != nil {
		t.Fatal(err)
	}
	if name != again {
		t.Fatalf("name is not deterministic: %q != %q", name, again)
	}
	if !hasHashSuffix(name) {
		t.Fatalf("long name %q is missing collision suffix", name)
	}
}

func TestResourceNameSpecialCharacterCollisionsGetStableSuffixes(t *testing.T) {
	first, err := aws.ResourceName(aws.NameInput{
		Service:   "pay!ments",
		Env:       "prod",
		Kind:      aws.ResourceKindIAMRole,
		LogicalID: "iam-role:pay!ments",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := aws.ResourceName(aws.NameInput{
		Service:   "pay?ments",
		Env:       "prod",
		Kind:      aws.ResourceKindIAMRole,
		LogicalID: "iam-role:pay?ments",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("colliding sanitized names were not disambiguated: %q", first)
	}
	if !strings.HasPrefix(first, "skiff-prod-pay-ments-role-") || !strings.HasPrefix(second, "skiff-prod-pay-ments-role-") {
		t.Fatalf("unexpected sanitized names: %q %q", first, second)
	}
	if !hasHashSuffix(first) || !hasHashSuffix(second) {
		t.Fatalf("special character names must include hash suffixes: %q %q", first, second)
	}
}

func TestResourceNameKeepsSimpleNamesReadable(t *testing.T) {
	name, err := aws.ResourceName(aws.NameInput{
		Service:   "payments-api",
		Env:       "prod",
		Kind:      aws.ResourceKindAutoScalingGroup,
		LogicalID: "autoscaling-group:payments-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "skiff-prod-payments-api-asg" {
		t.Fatalf("name = %q, want readable name without suffix", name)
	}
}

func hasHashSuffix(name string) bool {
	parts := strings.Split(name, "-")
	if len(parts) == 0 {
		return false
	}
	suffix := parts[len(parts)-1]
	if len(suffix) != 10 {
		return false
	}
	for _, r := range suffix {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
