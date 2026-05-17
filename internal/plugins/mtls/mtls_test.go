package mtls_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/plugins/mtls"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

func TestManifestMatchesFixture(t *testing.T) {
	if diagnostics := plugins.ValidateManifest(mtls.Manifest()); len(diagnostics) > 0 {
		t.Fatalf("manifest diagnostics = %+v", diagnostics)
	}
	registry, err := plugins.LoadRegistry(context.Background(), plugins.RegistryOptions{
		Paths: []string{filepath.Join("..", "..", "..", "plugins", "mtls")},
	})
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(registry.Plugins) != 1 || registry.Plugins[0].Manifest.Name != mtls.Name {
		t.Fatalf("registry = %+v", registry)
	}
}

func TestMutateIRRequiresExplicitAddon(t *testing.T) {
	ctx := context.Background()
	host := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{mtls.Plugin()}}, mtls.Runner{})
	graph := testGraph(t, serviceSpecWithoutAddon)

	sets, err := host.MutateIR(ctx, graph, nil, "tr_mtls_disabled")
	if err != nil {
		t.Fatalf("MutateIR: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("patch sets = %+v", sets)
	}
	if len(sets[0].Patches) != 0 {
		t.Fatalf("mTLS patched graph without explicit addon: %+v", sets[0].Patches)
	}
}

func TestMutateIRAddsVisibleEgressAndIngressClientCertificate(t *testing.T) {
	ctx := context.Background()
	host := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{mtls.Plugin()}}, mtls.Runner{})
	doc, graph := testDocAndGraph(t, strictMTLSSpec)
	specBody, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}

	sets, err := host.MutateIR(ctx, graph, specBody, "tr_mtls_strict")
	if err != nil {
		t.Fatalf("MutateIR: %v", err)
	}
	if len(sets) != 1 || len(sets[0].Patches) != 3 {
		t.Fatalf("patch sets = %+v", sets)
	}
	if err := plugins.ApplyIRPatches(graph, sets); err != nil {
		t.Fatalf("ApplyIRPatches: %v", err)
	}

	explanations := plugins.ExplainPatchSets(sets)
	assertPatchKind(t, explanations, plugins.PatchKindSecurityGroupRule)
	assertPatchKind(t, explanations, plugins.PatchKindListenerMTLS)
	assertPatchKind(t, explanations, plugins.PatchKindIAMRoleSecretRef)

	lowered, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-test"})
	if err != nil {
		t.Fatalf("LowerService: %v", err)
	}
	if len(lowered.ListenerRules) != 1 || lowered.ListenerRules[0].ClientCertificateMode != "verify" || lowered.ListenerRules[0].TrustStoreRef == "" {
		t.Fatalf("listener mTLS was not visible in AWS lowering: %+v", lowered.ListenerRules)
	}
	if !hasEgressToService(lowered.SecurityGroups, "security-group:orders-api", 8443) {
		t.Fatalf("mTLS service egress was not visible in AWS lowering: %+v", lowered.SecurityGroups)
	}
	if !hasIAMSecretResource(lowered.IAMRoles, "arn:aws:secretsmanager:us-west-2:111122223333:secret:skiff/prod/payments-api/mtls-workload-cert") {
		t.Fatalf("mTLS workload certificate secret was not visible in IAM policy: %+v", lowered.IAMRoles)
	}
	if !metaHasSource(graph.Resources.Listeners[0].Meta, "plugin:"+mtls.Name) {
		t.Fatalf("listener missing plugin source: %+v", graph.Resources.Listeners[0].Meta.Source)
	}
}

func TestRuntimeAddonRequiresPatchedGraph(t *testing.T) {
	ctx := context.Background()
	host := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{mtls.Plugin()}}, mtls.Runner{})
	doc, graph := testDocAndGraph(t, strictMTLSSpec)

	addons, diagnostics, err := host.RuntimeAddons(ctx, graph, "tr_mtls_addons")
	if err != nil {
		t.Fatalf("RuntimeAddons before patch: %v", err)
	}
	if len(addons) != 0 || len(diagnostics) != 0 {
		t.Fatalf("runtime addons emitted before mTLS patches: addons=%+v diagnostics=%+v", addons, diagnostics)
	}

	specBody, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	sets, err := host.MutateIR(ctx, graph, specBody, "tr_mtls_addons")
	if err != nil {
		t.Fatalf("MutateIR: %v", err)
	}
	if err := plugins.ApplyIRPatches(graph, sets); err != nil {
		t.Fatalf("ApplyIRPatches: %v", err)
	}
	addons, diagnostics, err = host.RuntimeAddons(ctx, graph, "tr_mtls_addons")
	if err != nil {
		t.Fatalf("RuntimeAddons after patch: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("runtime addon diagnostics = %+v", diagnostics)
	}
	if len(addons) != 1 || addons[0].Kind != "systemd-unit" || addons[0].Name != "mtls-proxy" || len(addons[0].SecretRefs) == 0 {
		t.Fatalf("runtime addons = %+v", addons)
	}
}

func TestValidateReportsInvalidMTLSConfig(t *testing.T) {
	ctx := context.Background()
	host := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{mtls.Plugin()}}, mtls.Runner{})
	doc, err := spec.Decode([]byte(invalidMTLSSpec), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	specBody, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	sets, err := host.Validate(ctx, specBody, "tr_mtls_invalid")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("validate sets = %+v", sets)
	}
	assertDiagnostic(t, sets[0].Diagnostics, "MTLS_OUTBOUND_PORT_INVALID")
	assertDiagnostic(t, sets[0].Diagnostics, "MTLS_TRUST_STORE_REQUIRED")
}

func TestDoctorDetectsCoreMTLSFailures(t *testing.T) {
	host := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{mtls.Plugin()}}, mtls.Runner{})
	findings, err := (plugins.DoctorHook{Host: host}).Check(context.Background(), doctor.PluginRequest{
		Service: servicestatus.Service{
			Service: "payments-api",
			Env:     "prod",
			Findings: []servicestatus.Finding{
				{Code: mtls.FindingProxyUnhealthy, Summary: "proxy health endpoint failed"},
				{Code: mtls.FindingCertExpiring, Summary: "certificate expires in 48h"},
			},
		},
		TraceID: "tr_mtls_doctor",
	})
	if err != nil {
		t.Fatalf("DoctorHook.Check: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v", findings)
	}
	if findings[0].Code != mtls.FindingProxyUnhealthy || findings[0].Severity != doctor.SeverityHigh {
		t.Fatalf("proxy finding = %+v", findings[0])
	}
}

func testGraph(t *testing.T, body string) *ir.Graph {
	t.Helper()
	_, graph := testDocAndGraph(t, body)
	return graph
}

func testDocAndGraph(t *testing.T, body string) (*spec.Document, *ir.Graph) {
	t.Helper()
	doc, err := spec.Decode([]byte(body), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if result := spec.Validate(*doc); !result.OK {
		t.Fatalf("Validate diagnostics = %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return doc, graph
}

func assertPatchKind(t *testing.T, explanations []plugins.PatchExplanation, kind string) {
	t.Helper()
	for _, explanation := range explanations {
		if explanation.Kind == kind && explanation.Plugin == mtls.Name {
			return
		}
	}
	t.Fatalf("patch kind %s not found in %+v", kind, explanations)
}

func assertDiagnostic(t *testing.T, diagnostics []pluginapi.Diagnostic, code string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %s not found in %+v", code, diagnostics)
}

func hasEgressToService(groups []aws.SecurityGroupAWS, destination string, port int) bool {
	for _, group := range groups {
		for _, rule := range group.Egress {
			if rule.DestinationSecurityGroupRef == destination && rule.ToPort == port {
				return true
			}
		}
	}
	return false
}

func hasIAMSecretResource(roles []aws.IAMRoleResource, resource string) bool {
	for _, role := range roles {
		for _, statement := range role.InlinePolicy.Statement {
			for _, got := range statement.Resource {
				if got == resource {
					return true
				}
			}
		}
	}
	return false
}

func metaHasSource(meta ir.ResourceMeta, path string) bool {
	for _, source := range meta.Source {
		if source.Path == path {
			return true
		}
	}
	return false
}

const serviceSpecWithoutAddon = `
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
network:
  ingress:
    type: public-http
    host: payments.example.com
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
`

const strictMTLSSpec = serviceSpecWithoutAddon + `
addons:
  - name: mtls
    mode: strict
    config:
      certificateSecretRef: aws-secretsmanager://arn:aws:secretsmanager:us-west-2:111122223333:secret:skiff/prod/payments-api/mtls-workload-cert
      ingress:
        clientCertificate:
          mode: verify
          trustStoreRef: aws-elbv2-trust-store://us-west-2/payments-clients
      outbound:
        - service: orders-api
          port: 8443
`

const invalidMTLSSpec = serviceSpecWithoutAddon + `
addons:
  - name: mtls
    mode: strict
    config:
      ingress:
        clientCertificate:
          mode: verify
      outbound:
        - service: Orders_API
          port: 70000
`
