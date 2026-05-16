package bootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStateBucketPolicyGolden(t *testing.T) {
	got, err := PolicyJSON(StateBucketPolicy("skiff-state-prod"))
	if err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("..", "..", "tests", "golden", "bootstrap", "aws-bucket-policy.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var compactWant bytes.Buffer
	if err := json.Compact(&compactWant, want); err != nil {
		t.Fatalf("compact golden: %v", err)
	}
	if got != compactWant.String() {
		t.Fatalf("bucket policy golden mismatch\nwant:\n%s\n\ngot:\n%s", compactWant.String(), got)
	}
}

func TestRunnerPolicyIsReadOnlyForState(t *testing.T) {
	policy := RunnerPolicy("skiff-state-prod", "alias/skiff-prod-state")
	var hasReadReleases bool
	for _, statement := range policy.Statement {
		switch statement.Sid {
		case "ReadServiceControlAndReleases":
			hasReadReleases = true
		}
		for _, action := range actions(statement.Action) {
			if action == "s3:PutObject" {
				t.Fatalf("runner policy must not write state objects: %+v", statement)
			}
		}
	}
	if !hasReadReleases {
		t.Fatalf("runner policy missing expected statements: %#v", policy.Statement)
	}
}

func actions(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	default:
		return nil
	}
}
