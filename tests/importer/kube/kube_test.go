package kube_test

import (
	"os"
	"strings"
	"testing"

	kubeimport "github.com/s1liconcow/skiff/internal/importer/kube"
)

func TestCleanImportProducesSkiffServiceSpec(t *testing.T) {
	result := convertFixture(t, "simple.yaml", kubeimport.Options{Env: "staging"})
	if !result.OK {
		t.Fatalf("import should be clean: %+v", result.Findings)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("unexpected findings: %+v", result.Findings)
	}
	doc := result.Service
	if doc.Metadata.Name != "payments-api" || doc.Metadata.Env != "staging" {
		t.Fatalf("unexpected service metadata: %+v", doc.Metadata)
	}
	if doc.Artifact == nil || doc.Artifact.Type != "oci" || !strings.Contains(doc.Artifact.Ref, "@sha256:") {
		t.Fatalf("artifact was not imported safely: %+v", doc.Artifact)
	}
	if doc.Runtime.Port != 8080 || doc.Runtime.Health.Path != "/ready" {
		t.Fatalf("runtime was not imported: %+v", doc.Runtime)
	}
	if doc.Runtime.Env["FEATURE_FLAG"] != "enabled" || doc.Runtime.Env["LOG_LEVEL"] != "info" {
		t.Fatalf("environment was not imported: %+v", doc.Runtime.Env)
	}
	if doc.Scale.Min != 2 || doc.Scale.Max != 5 {
		t.Fatalf("HPA scale was not imported: %+v", doc.Scale)
	}
	if doc.Network.Ingress == nil || doc.Network.Ingress.Host != "payments.staging.example.com" || doc.Network.Ingress.TLS == nil {
		t.Fatalf("ingress was not imported: %+v", doc.Network.Ingress)
	}
	for _, want := range []string{"kind: Service", "payments-api", "payments.staging.example.com"} {
		if !strings.Contains(result.SkiffYAML, want) {
			t.Fatalf("generated YAML missing %q:\n%s", want, result.SkiffYAML)
		}
	}
	if !strings.Contains(result.MarkdownReport, "Kubernetes Migration Report") || !strings.Contains(result.MarkdownReport, "Generated Skiff Spec") {
		t.Fatalf("migration report missing expected sections:\n%s", result.MarkdownReport)
	}
}

func TestImportReportsWarningsWithoutImportingPlaintextSecretData(t *testing.T) {
	result := convertFixture(t, "warnings.yaml", kubeimport.Options{Env: "staging"})
	if !result.OK {
		t.Fatalf("warnings should not block import: %+v", result.Findings)
	}
	for _, code := range []string{
		"KUBE_SIDECAR_IGNORED",
		"KUBE_INIT_CONTAINERS_IGNORED",
		"KUBE_SERVICE_MESH_ANNOTATION",
		"KUBE_SECRET_REFERENCE_IMPORTED",
		"KUBE_ENVFROM_REVIEW_REQUIRED",
		"KUBE_PDB_REVIEW_REQUIRED",
		"KUBE_LOADBALANCER_REVIEW_REQUIRED",
	} {
		if !hasFinding(result.Findings, code, "warn") {
			t.Fatalf("missing warning %s in %+v", code, result.Findings)
		}
	}
	if len(result.Service.Secrets) != 1 || !strings.HasPrefix(result.Service.Secrets[0].Ref, "secret://kubernetes/staging/checkout-secrets/") {
		t.Fatalf("secret reference was not converted: %+v", result.Service.Secrets)
	}
	if strings.Contains(result.SkiffYAML, "c2VjcmV0") || strings.Contains(result.MarkdownReport, "c2VjcmV0") {
		t.Fatalf("plaintext Kubernetes Secret data leaked into output")
	}
}

func TestImportReportsUnsupportedFeaturesAsErrors(t *testing.T) {
	result := convertFixture(t, "unsupported.yaml", kubeimport.Options{Env: "staging"})
	if result.OK {
		t.Fatalf("unsupported import should fail")
	}
	for _, code := range []string{
		"KUBE_HOSTPATH_UNSUPPORTED",
		"KUBE_PRIVILEGED_UNSUPPORTED",
		"KUBE_CRD_UNSUPPORTED",
	} {
		if !hasFinding(result.Findings, code, "error") {
			t.Fatalf("missing error %s in %+v", code, result.Findings)
		}
	}
	if !hasFinding(result.Findings, "KUBE_STATEFULSET_PROPOSAL", "warn") {
		t.Fatalf("StatefulSet should be reported as a proposal warning: %+v", result.Findings)
	}
	if !strings.Contains(result.MarkdownReport, "KUBE_HOSTPATH_UNSUPPORTED") {
		t.Fatalf("migration report did not include unsupported feature:\n%s", result.MarkdownReport)
	}
}

func TestStatefulSetImportProducesStatefulGroupProposal(t *testing.T) {
	result := convertFixture(t, "statefulset.yaml", kubeimport.Options{Env: "prod"})
	if !result.OK {
		t.Fatalf("StatefulSet proposal should be importable with warnings: %+v", result.Findings)
	}
	if result.Service.Kind != "StatefulGroup" || result.Service.StatefulGroup == nil {
		t.Fatalf("expected StatefulGroup proposal: %+v", result.Service)
	}
	group := result.Service.StatefulGroup
	if group.Replicas != 3 || group.Volume.Size != "20Gi" || group.Volume.MountPath != "/var/lib/postgresql/data" {
		t.Fatalf("unexpected StatefulGroup proposal: %+v", group)
	}
	if !hasFinding(result.Findings, "KUBE_STATEFULSET_PROPOSAL", "warn") || !hasFinding(result.Findings, "KUBE_STATEFULSET_VOLUME_REVIEW_REQUIRED", "warn") {
		t.Fatalf("missing StatefulSet proposal warnings: %+v", result.Findings)
	}
	if !strings.Contains(result.SkiffYAML, "kind: StatefulGroup") || !strings.Contains(result.MarkdownReport, "StatefulGroup proposal") {
		t.Fatalf("stateful report missing proposal output:\n%s", result.MarkdownReport)
	}
}

func convertFixture(t *testing.T, path string, opts kubeimport.Options) *kubeimport.Result {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	objects, err := kubeimport.Parse(body)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	result, err := kubeimport.Convert(objects, opts)
	if err != nil {
		t.Fatalf("convert fixture: %v", err)
	}
	return result
}

func hasFinding(findings []kubeimport.Finding, code, severity string) bool {
	for _, finding := range findings {
		if finding.Code == code && finding.Severity == severity {
			return true
		}
	}
	return false
}
