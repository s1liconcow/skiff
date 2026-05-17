package spec_test

import (
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/spec"
)

const validServiceYAML = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:abc123
runtime:
  port: 8080
  health:
    path: /healthz
scale:
  min: 3
  max: 20
network:
  ingress:
    type: public-http
    host: payments.example.com
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
`

func TestDecodeYAMLAppliesDefaultsAndValidates(t *testing.T) {
	doc, err := spec.Decode([]byte(validServiceYAML), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if doc.Machine.Arch != "x86_64" {
		t.Fatalf("machine arch = %q, want x86_64", doc.Machine.Arch)
	}
	if doc.Runtime.ShutdownGrace != "30s" {
		t.Fatalf("shutdown grace = %q, want 30s", doc.Runtime.ShutdownGrace)
	}
	if doc.Runtime.Health.Interval != "10s" || doc.Runtime.Health.Type != "http" {
		t.Fatalf("unexpected health defaults: %+v", doc.Runtime.Health)
	}

	result := spec.Validate(*doc)
	if !result.OK {
		t.Fatalf("Validate diagnostics = %+v, want OK", result.Diagnostics)
	}
}

func TestStrictUnknownFieldRejectedUnlessAllowed(t *testing.T) {
	body := []byte(validServiceYAML + "\ntypoField: true\n")
	if _, err := spec.Decode(body, spec.DecodeOptions{}); err == nil {
		t.Fatal("Decode succeeded with unknown field, want error")
	}
	if _, err := spec.Decode(body, spec.DecodeOptions{AllowUnknownFields: true}); err != nil {
		t.Fatalf("Decode with AllowUnknownFields returned error: %v", err)
	}
}

func TestValidationDiagnosticsArePathSpecific(t *testing.T) {
	doc, err := spec.Decode([]byte(strings.Replace(validServiceYAML, "@sha256:abc123", ":latest", 1)), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	doc.Network.Ingress.TLS = nil
	result := spec.Validate(*doc)
	if result.OK {
		t.Fatal("Validate returned OK, want diagnostics")
	}
	assertDiagnostic(t, result.Diagnostics, "$.artifact.ref", "MUTABLE_ARTIFACT_REF")
	assertDiagnostic(t, result.Diagnostics, "$.network.ingress.tls", "TLS_REQUIRED")
}

func TestSecretRefsRejectPlaintextValues(t *testing.T) {
	doc, err := spec.Decode([]byte(validServiceYAML+`
secrets:
  - name: db-password
    ref: plaintext-password
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	result := spec.Validate(*doc)
	if result.OK {
		t.Fatal("Validate returned OK, want invalid secret ref")
	}
	assertDiagnostic(t, result.Diagnostics, "$.secrets[0].ref", "INVALID_SECRET_REF")
}

func TestStackValidationRequiresDatabaseBinding(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/orders-api@sha256:abc123
      runtime:
        port: 8080
        health:
          path: /healthz
  databases:
    - name: db
      engine: postgres
      version: "16"
      size: small
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	result := spec.Validate(*doc)
	if result.OK {
		t.Fatal("Validate returned OK, want missing binding diagnostic")
	}
	assertDiagnostic(t, result.Diagnostics, "$.stack.bindings", "REQUIRED")
}

func TestStackValidationRejectsBadBindingTarget(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/orders-api@sha256:abc123
      runtime:
        port: 8080
        health:
          path: /healthz
  databases:
    - name: db
      engine: postgres
      version: "16"
      size: small
  bindings:
    - from: api
      to: missing
      as: database-url
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	result := spec.Validate(*doc)
	if result.OK {
		t.Fatal("Validate returned OK, want binding diagnostics")
	}
	assertDiagnostic(t, result.Diagnostics, "$.stack.bindings[0].to", "UNKNOWN_STACK_DATABASE")
	assertDiagnostic(t, result.Diagnostics, "$.stack.bindings[0].as", "INVALID_ENV_NAME")
}

func TestMarshalYAMLShowsDefaultedFields(t *testing.T) {
	doc, err := spec.Decode([]byte(validServiceYAML), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	body, err := spec.MarshalYAML(*doc)
	if err != nil {
		t.Fatalf("MarshalYAML returned error: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"arch: x86_64",
		"shutdownGrace: 30s",
		"strategy: rolling",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("defaulted YAML missing %q:\n%s", want, text)
		}
	}
}

func assertDiagnostic(t *testing.T, diagnostics []spec.Diagnostic, path, code string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path && diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostics = %+v, want %s %s", diagnostics, path, code)
}
