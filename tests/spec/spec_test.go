package spec_test

import (
	"path/filepath"
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

func TestAddonsAreExplicitAndValidated(t *testing.T) {
	doc, err := spec.Decode([]byte(validServiceYAML+`
addons:
  - name: mtls
    mode: strict
    config:
      outbound:
        - service: orders-api
          port: 8443
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(doc.Addons) != 1 || doc.Addons[0].Name != "mtls" || len(doc.Addons[0].Config) == 0 {
		t.Fatalf("addons not decoded: %+v", doc.Addons)
	}
	result := spec.Validate(*doc)
	if !result.OK {
		t.Fatalf("Validate diagnostics = %+v, want OK", result.Diagnostics)
	}

	doc.Addons = append(doc.Addons, doc.Addons[0])
	result = spec.Validate(*doc)
	if result.OK {
		t.Fatal("Validate returned OK, want duplicate addon diagnostic")
	}
	assertDiagnostic(t, result.Diagnostics, "$.addons[1].name", "DUPLICATE_ADDON")
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
	assertDiagnostic(t, result.Diagnostics, "$.stack.bindings[0].to", "UNKNOWN_STACK_RESOURCE")
	assertDiagnostic(t, result.Diagnostics, "$.stack.bindings[0].as", "INVALID_ENV_NAME")
}

func TestStackValidationAcceptsObjectStoreBinding(t *testing.T) {
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
  objectStores:
    - name: data
      uri: s3://orders-slatedb-prod/slatedb/orders
      purpose: slatedb
  bindings:
    - from: api
      to: data
      as: SLATEDB_URI
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	result := spec.Validate(*doc)
	if !result.OK {
		t.Fatalf("Validate diagnostics = %+v, want OK", result.Diagnostics)
	}
	if !doc.Stack.ObjectStores[0].Encrypted || !doc.Stack.ObjectStores[0].Versioned || doc.Stack.ObjectStores[0].Access != "read-write" {
		t.Fatalf("object store defaults missing: %+v", doc.Stack.ObjectStores[0])
	}
}

func TestMultiRegionStackValidation(t *testing.T) {
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
  databaseReplication:
    mode: async
    maxReplicaLag: 30s
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if doc.MultiRegion.Binding.As != "DATABASE_URL" || !doc.MultiRegion.FailoverPolicy.RequireApproval {
		t.Fatalf("multi-region defaults not applied: %+v", doc.MultiRegion)
	}
	result := spec.Validate(*doc)
	if !result.OK {
		t.Fatalf("Validate diagnostics = %+v, want OK", result.Diagnostics)
	}
}

func TestStatefulGroupValidation(t *testing.T) {
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: postgres
  env: prod
stateful:
  replicas: 1
  members:
    - ordinal: 0
      zone: us-west-2a
      dnsName: postgres-0.internal.example.com
  volume:
    size: 100Gi
    type: gp3
    mountPath: /var/lib/postgresql
    encrypted: true
  recipe:
    name: postgres-single
  update:
    strategy: ordered
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	result := spec.Validate(*doc)
	if !result.OK {
		t.Fatalf("Validate diagnostics = %+v, want OK", result.Diagnostics)
	}

	doc.StatefulGroup.Volume.MountPath = "relative"
	doc.StatefulGroup.Recipe = spec.StatefulRecipe{}
	result = spec.Validate(*doc)
	if result.OK {
		t.Fatal("Validate returned OK, want stateful diagnostics")
	}
	assertDiagnostic(t, result.Diagnostics, "$.stateful.volume.mountPath", "INVALID_PATH")
	assertDiagnostic(t, result.Diagnostics, "$.stateful.recipe", "RECIPE_REQUIRED")
}

func TestStatefulJetStreamExampleValidates(t *testing.T) {
	doc, err := spec.LoadFile(filepath.Join("..", "..", "examples", "stateful", "jetstream", "skiff.yaml"), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if doc.Kind != spec.KindStatefulGroup || doc.StatefulGroup == nil {
		t.Fatalf("example decoded as %s with stateful=%v, want StatefulGroup", doc.Kind, doc.StatefulGroup != nil)
	}
	if doc.StatefulGroup.Replicas != 3 || len(doc.StatefulGroup.Members) != 3 {
		t.Fatalf("unexpected stateful membership: %+v", doc.StatefulGroup)
	}
	if len(doc.StatefulGroup.Recipe.Config) == 0 {
		t.Fatal("stateful recipe config was not decoded")
	}
	result := spec.Validate(*doc)
	if !result.OK {
		t.Fatalf("Validate diagnostics = %+v, want OK", result.Diagnostics)
	}
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
