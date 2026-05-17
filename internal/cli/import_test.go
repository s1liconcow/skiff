package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportKubeOutputsJSONAndYAML(t *testing.T) {
	clearSkiffEnv(t)
	fixture := filepath.Join("..", "..", "tests", "importer", "kube", "simple.yaml")

	var jsonOut, jsonErr bytes.Buffer
	code := Run("skiff", []string{"import", "kube", fixture, "--env", "staging", "--format", "json", "--trace-id", "tr_import_kube"}, &jsonOut, &jsonErr)
	if code != ExitSuccess {
		t.Fatalf("json exit=%d stderr=%s stdout=%s", code, jsonErr.String(), jsonOut.String())
	}
	var got importKubeOutput
	if err := json.Unmarshal(jsonOut.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\n%s", err, jsonOut.String())
	}
	if !got.OK || got.TraceID != "tr_import_kube" || got.Result.Service.Metadata.Name != "payments-api" {
		t.Fatalf("unexpected import output: %+v", got)
	}

	var yamlOut, yamlErr bytes.Buffer
	code = Run("skiff", []string{"import", "kube", "--file", fixture, "--env", "staging", "--format", "yaml"}, &yamlOut, &yamlErr)
	if code != ExitSuccess {
		t.Fatalf("yaml exit=%d stderr=%s stdout=%s", code, yamlErr.String(), yamlOut.String())
	}
	for _, want := range []string{"apiVersion: skiff.dev/v1alpha1", "kind: Service", "payments.staging.example.com"} {
		if !strings.Contains(yamlOut.String(), want) {
			t.Fatalf("YAML output missing %q:\n%s", want, yamlOut.String())
		}
	}
}
