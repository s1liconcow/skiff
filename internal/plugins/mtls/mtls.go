package mtls

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/spec"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

const (
	Name    = "mtls"
	Version = "0.1.0"

	capabilityEgress          = "service-to-service-egress"
	capabilityIngressClientCA = "ingress-client-certificate"
	capabilityIAMSecret       = "workload-certificate-secret"
	capabilityRuntimeProxy    = "local-proxy-cert-agent"
	capabilityDoctor          = "mtls-health"

	FindingProxyUnhealthy = "MTLS_PROXY_UNHEALTHY"
	FindingCertExpiring   = "MTLS_CERT_EXPIRING"
	FindingCertExpired    = "MTLS_CERT_EXPIRED"
	FindingPolicyMismatch = "MTLS_POLICY_MISMATCH"
)

type Runner struct{}

type Config struct {
	CertificateSecretRef string          `json:"certificateSecretRef,omitempty"`
	Ingress              IngressConfig   `json:"ingress,omitempty"`
	Outbound             []OutboundRule  `json:"outbound,omitempty"`
	Proxy                ProxyConfig     `json:"proxy,omitempty"`
	Extra                json.RawMessage `json:"-"`
}

type IngressConfig struct {
	ClientCertificate ClientCertificateConfig `json:"clientCertificate,omitempty"`
}

type ClientCertificateConfig struct {
	Mode          string `json:"mode,omitempty"`
	TrustStoreRef string `json:"trustStoreRef,omitempty"`
}

type OutboundRule struct {
	Service string `json:"service,omitempty"`
	Port    int    `json:"port"`
	CIDR    string `json:"cidr,omitempty"`
}

type ProxyConfig struct {
	ListenPort int `json:"listenPort,omitempty"`
	AdminPort  int `json:"adminPort,omitempty"`
}

type addonConfig struct {
	Name   string
	Mode   string
	Config Config
}

func Plugin() plugins.Plugin {
	return plugins.Plugin{
		Manifest: Manifest(),
		Source: plugins.Source{
			Kind: plugins.SourcePath,
			Path: "plugins/mtls",
		},
	}
}

func Manifest() pluginapi.Manifest {
	return pluginapi.Manifest{
		APIVersion:  pluginapi.APIVersion,
		Kind:        pluginapi.KindPlugin,
		Name:        Name,
		Version:     Version,
		Description: "Optional mTLS capability for explicit service pairs and ingress client certificate validation.",
		Runtime: pluginapi.RuntimeSpec{
			Kind:    pluginapi.RuntimeCommand,
			Command: []string{"skiff-mtls-plugin"},
		},
		Hooks: []pluginapi.Hook{
			pluginapi.HookValidate,
			pluginapi.HookMutateIR,
			pluginapi.HookRuntimeAddons,
			pluginapi.HookDoctorChecks,
		},
		Permissions: pluginapi.Permissions{
			AllowedPatchKinds: []string{plugins.PatchKindSecurityGroupRule, plugins.PatchKindListenerMTLS, plugins.PatchKindIAMRoleSecretRef},
			RuntimeAddons:     true,
			DoctorChecks:      true,
		},
		Capabilities: []pluginapi.Capability{
			{
				Kind:        pluginapi.CapabilityIRPatch,
				Name:        capabilityEgress,
				Description: "Adds explicit workload egress rules for named mTLS peers.",
				PatchKinds:  []string{plugins.PatchKindSecurityGroupRule},
			},
			{
				Kind:        pluginapi.CapabilityIRPatch,
				Name:        capabilityIngressClientCA,
				Description: "Adds load balancer client-certificate validation when ingress mTLS is configured.",
				PatchKinds:  []string{plugins.PatchKindListenerMTLS},
			},
			{
				Kind:        pluginapi.CapabilityIRPatch,
				Name:        capabilityIAMSecret,
				Description: "Adds least-privilege IAM access to the configured workload certificate secret.",
				PatchKinds:  []string{plugins.PatchKindIAMRoleSecretRef},
			},
			{
				Kind:          pluginapi.CapabilityRuntimeAddon,
				Name:          capabilityRuntimeProxy,
				Description:   "Adds a VM-local proxy and certificate agent runtime addon.",
				RuntimeAddons: []string{"systemd-unit"},
			},
			{
				Kind:         pluginapi.CapabilityDoctorCheck,
				Name:         capabilityDoctor,
				Description:  "Reports mTLS certificate, proxy, and policy findings.",
				DoctorChecks: []string{FindingProxyUnhealthy, FindingCertExpiring, FindingCertExpired, FindingPolicyMismatch},
			},
		},
	}
}

func (Runner) RunPluginHook(ctx context.Context, plugin plugins.Plugin, hook pluginapi.Hook, request any, response any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if plugin.Manifest.Name != Name {
		return fmt.Errorf("mtls plugin runner received plugin %q", plugin.Manifest.Name)
	}
	body, err := requestBody(request)
	if err != nil {
		return err
	}
	out, err := Handle(ctx, hook, body)
	if err != nil {
		return err
	}
	return assign(response, out)
}

func Handle(ctx context.Context, hook pluginapi.Hook, request json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch hook {
	case pluginapi.HookValidate:
		var req pluginapi.ValidateRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode validate request: %w", err)
		}
		return validate(req), nil
	case pluginapi.HookMutateIR:
		var req pluginapi.MutateIRRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode mutate_ir request: %w", err)
		}
		return mutateIR(req)
	case pluginapi.HookRuntimeAddons:
		var req pluginapi.RuntimeAddonsRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode runtime_addons request: %w", err)
		}
		return runtimeAddons(req)
	case pluginapi.HookDoctorChecks:
		var req pluginapi.DoctorChecksRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode doctor_checks request: %w", err)
		}
		return doctorChecks(req)
	default:
		return map[string]any{}, nil
	}
}

func validate(req pluginapi.ValidateRequest) pluginapi.ValidateResponse {
	addon, enabled, diagnostics := addonFromSpec(req.Spec)
	if !enabled {
		return pluginapi.ValidateResponse{Diagnostics: diagnostics}
	}
	diagnostics = append(diagnostics, validateAddon(addon)...)
	return pluginapi.ValidateResponse{Diagnostics: diagnostics}
}

func mutateIR(req pluginapi.MutateIRRequest) (pluginapi.MutateIRResponse, error) {
	addon, enabled, diagnostics := addonFromSpec(req.Spec)
	if !enabled {
		return pluginapi.MutateIRResponse{Diagnostics: diagnostics}, nil
	}
	diagnostics = append(diagnostics, validateAddon(addon)...)
	if hasError(diagnostics) {
		return pluginapi.MutateIRResponse{Diagnostics: diagnostics}, nil
	}
	var graph ir.Graph
	if err := json.Unmarshal(req.Graph, &graph); err != nil {
		return pluginapi.MutateIRResponse{}, fmt.Errorf("decode graph: %w", err)
	}
	var patches []pluginapi.IRPatch
	for _, outbound := range addon.Config.Outbound {
		patch, err := outboundPatch(graph, outbound)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic("MTLS_OUTBOUND_UNAVAILABLE", "warn", "$.addons[mtls].config.outbound", err.Error()))
			continue
		}
		patches = append(patches, patch)
	}
	if addon.Config.Ingress.ClientCertificate.TrustStoreRef != "" {
		patch, err := ingressPatch(graph, addon.Config.Ingress.ClientCertificate)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic("MTLS_INGRESS_UNAVAILABLE", "warn", "$.addons[mtls].config.ingress", err.Error()))
		} else {
			patches = append(patches, patch)
		}
	}
	if addon.Config.CertificateSecretRef != "" {
		patch, err := certificateSecretPatch(graph, addon.Config.CertificateSecretRef)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic("MTLS_CERT_SECRET_UNAVAILABLE", "warn", "$.addons[mtls].config.certificateSecretRef", err.Error()))
		} else {
			patches = append(patches, patch)
		}
	}
	return pluginapi.MutateIRResponse{Patches: patches, Diagnostics: diagnostics}, nil
}

func runtimeAddons(req pluginapi.RuntimeAddonsRequest) (pluginapi.RuntimeAddonsResponse, error) {
	var graph ir.Graph
	if err := json.Unmarshal(req.Graph, &graph); err != nil {
		return pluginapi.RuntimeAddonsResponse{}, fmt.Errorf("decode graph: %w", err)
	}
	if !graphHasPluginSource(graph) {
		return pluginapi.RuntimeAddonsResponse{}, nil
	}
	config := map[string]any{
		"unit":       "skiff-mtls-proxy.service",
		"admin_port": 15000,
		"mode":       "explicit-service-pairs",
		"service":    graph.Service,
		"env":        graph.Env,
	}
	body, err := json.Marshal(config)
	if err != nil {
		return pluginapi.RuntimeAddonsResponse{}, err
	}
	return pluginapi.RuntimeAddonsResponse{Addons: []pluginapi.RuntimeAddon{{
		Kind:    "systemd-unit",
		Name:    "mtls-proxy",
		Target:  "workload-vm",
		Summary: "run local mTLS proxy and certificate agent for explicitly configured peers",
		Config:  body,
		SecretRefs: []pluginapi.SecretRef{{
			Name: "workload-certificate",
			Ref:  "secret://mtls/" + graph.Env + "/" + graph.Service + "/workload-certificate",
		}},
	}}}, nil
}

func doctorChecks(req pluginapi.DoctorChecksRequest) (pluginapi.DoctorChecksResponse, error) {
	var service servicestatus.Service
	if len(req.Service) > 0 {
		if err := json.Unmarshal(req.Service, &service); err != nil {
			return pluginapi.DoctorChecksResponse{}, fmt.Errorf("decode service status: %w", err)
		}
	}
	var findings []pluginapi.DoctorFinding
	for _, finding := range service.Findings {
		if mapped, ok := doctorFinding(service.Service, finding.Code, finding.Summary); ok {
			findings = append(findings, mapped)
		}
	}
	return pluginapi.DoctorChecksResponse{Findings: findings}, nil
}

func addonFromSpec(body json.RawMessage) (addonConfig, bool, []pluginapi.Diagnostic) {
	if len(body) == 0 || string(body) == "null" {
		return addonConfig{}, false, nil
	}
	var doc spec.Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return addonConfig{}, false, []pluginapi.Diagnostic{diagnostic("MTLS_SPEC_DECODE_FAILED", "error", "$", err.Error())}
	}
	var found *spec.Addon
	for i := range doc.Addons {
		if doc.Addons[i].Name != Name {
			continue
		}
		if found != nil {
			return addonConfig{}, true, []pluginapi.Diagnostic{diagnostic("MTLS_ADDON_DUPLICATE", "error", "$.addons", "mtls addon must be declared at most once")}
		}
		found = &doc.Addons[i]
	}
	if found == nil {
		return addonConfig{}, false, nil
	}
	mode := strings.ToLower(strings.TrimSpace(found.Mode))
	if mode == "" {
		mode = "permissive"
	}
	out := addonConfig{Name: found.Name, Mode: mode}
	if len(found.Config) > 0 && string(found.Config) != "null" {
		if err := json.Unmarshal(found.Config, &out.Config); err != nil {
			return out, true, []pluginapi.Diagnostic{diagnostic("MTLS_CONFIG_DECODE_FAILED", "error", "$.addons[mtls].config", err.Error())}
		}
	}
	return out, true, nil
}

func validateAddon(addon addonConfig) []pluginapi.Diagnostic {
	var diagnostics []pluginapi.Diagnostic
	switch addon.Mode {
	case "permissive", "strict":
	default:
		diagnostics = append(diagnostics, diagnostic("MTLS_MODE_UNSUPPORTED", "error", "$.addons[mtls].mode", "mode must be permissive or strict"))
	}
	client := addon.Config.Ingress.ClientCertificate
	if client.TrustStoreRef != "" {
		mode := strings.ToLower(strings.TrimSpace(client.Mode))
		if mode == "" {
			mode = "verify"
		}
		if mode != "verify" {
			diagnostics = append(diagnostics, diagnostic("MTLS_CLIENT_CERT_MODE_UNSUPPORTED", "error", "$.addons[mtls].config.ingress.clientCertificate.mode", "client certificate mode must be verify"))
		}
		if !validReference(client.TrustStoreRef) {
			diagnostics = append(diagnostics, diagnostic("MTLS_TRUST_STORE_INVALID", "error", "$.addons[mtls].config.ingress.clientCertificate.trustStoreRef", "trustStoreRef must be a provider or secret reference"))
		}
	} else if strings.TrimSpace(client.Mode) != "" {
		diagnostics = append(diagnostics, diagnostic("MTLS_TRUST_STORE_REQUIRED", "error", "$.addons[mtls].config.ingress.clientCertificate.trustStoreRef", "trustStoreRef is required when ingress clientCertificate is configured"))
	}
	for i, outbound := range addon.Config.Outbound {
		base := fmt.Sprintf("$.addons[mtls].config.outbound[%d]", i)
		if outbound.Port <= 0 || outbound.Port > 65535 {
			diagnostics = append(diagnostics, diagnostic("MTLS_OUTBOUND_PORT_INVALID", "error", base+".port", "port must be between 1 and 65535"))
		}
		if strings.TrimSpace(outbound.Service) == "" && strings.TrimSpace(outbound.CIDR) == "" {
			diagnostics = append(diagnostics, diagnostic("MTLS_OUTBOUND_DESTINATION_REQUIRED", "error", base, "service or cidr is required"))
		}
		if outbound.Service != "" && !validName(outbound.Service) {
			diagnostics = append(diagnostics, diagnostic("MTLS_OUTBOUND_SERVICE_INVALID", "error", base+".service", "service must be a DNS-style Skiff name"))
		}
		if outbound.CIDR != "" {
			if _, _, err := net.ParseCIDR(outbound.CIDR); err != nil {
				diagnostics = append(diagnostics, diagnostic("MTLS_OUTBOUND_CIDR_INVALID", "error", base+".cidr", "cidr must be a valid CIDR block"))
			}
		}
	}
	if addon.Config.CertificateSecretRef != "" && !validReference(addon.Config.CertificateSecretRef) {
		diagnostics = append(diagnostics, diagnostic("MTLS_CERT_SECRET_INVALID", "error", "$.addons[mtls].config.certificateSecretRef", "certificateSecretRef must be a secret manager reference"))
	}
	return diagnostics
}

func outboundPatch(graph ir.Graph, outbound OutboundRule) (pluginapi.IRPatch, error) {
	sgRef := serviceSecurityGroupRef(graph)
	if sgRef == "" {
		return pluginapi.IRPatch{}, fmt.Errorf("service security group was not found")
	}
	destination := strings.TrimSpace(outbound.CIDR)
	summaryTarget := destination
	if outbound.Service != "" {
		destination = "security-group:" + outbound.Service
		summaryTarget = outbound.Service
	}
	value, err := json.Marshal(plugins.SecurityGroupRulePatch{
		SecurityGroupRef: sgRef,
		Direction:        "egress",
		Protocol:         "tcp",
		FromPort:         outbound.Port,
		ToPort:           outbound.Port,
		Destination:      destination,
		Description:      "allow mTLS egress to " + summaryTarget,
	})
	if err != nil {
		return pluginapi.IRPatch{}, err
	}
	return pluginapi.IRPatch{
		Op:      pluginapi.PatchAdd,
		Path:    "/resources/security_groups/" + sgRef + "/rules/-",
		Kind:    plugins.PatchKindSecurityGroupRule,
		Value:   value,
		Summary: fmt.Sprintf("allow mTLS egress from %s to %s:%d", graph.Service, summaryTarget, outbound.Port),
		Source:  pluginapi.PatchSource{Plugin: Name, Version: Version, Capability: capabilityEgress},
	}, nil
}

func certificateSecretPatch(graph ir.Graph, ref string) (pluginapi.IRPatch, error) {
	roleRef := serviceIAMRoleRef(graph)
	if roleRef == "" {
		return pluginapi.IRPatch{}, fmt.Errorf("service IAM role was not found")
	}
	value, err := json.Marshal(plugins.IAMRoleSecretRefPatch{
		IAMRoleRef: roleRef,
		Name:       "mtls-workload-certificate",
		Ref:        strings.TrimSpace(ref),
	})
	if err != nil {
		return pluginapi.IRPatch{}, err
	}
	return pluginapi.IRPatch{
		Op:      pluginapi.PatchAdd,
		Path:    "/resources/iam_roles/" + roleRef + "/secret_refs/-",
		Kind:    plugins.PatchKindIAMRoleSecretRef,
		Value:   value,
		Summary: "allow workload role to read the mTLS workload certificate secret",
		Source:  pluginapi.PatchSource{Plugin: Name, Version: Version, Capability: capabilityIAMSecret},
	}, nil
}

func ingressPatch(graph ir.Graph, client ClientCertificateConfig) (pluginapi.IRPatch, error) {
	if len(graph.Resources.Listeners) == 0 {
		return pluginapi.IRPatch{}, fmt.Errorf("service has no load balancer listener to attach client certificate validation")
	}
	listenerRef := graph.Resources.Listeners[0].Meta.LogicalID
	mode := strings.ToLower(strings.TrimSpace(client.Mode))
	if mode == "" {
		mode = "verify"
	}
	value, err := json.Marshal(plugins.ListenerMTLSPatch{
		ListenerRef:   listenerRef,
		Mode:          mode,
		TrustStoreRef: strings.TrimSpace(client.TrustStoreRef),
	})
	if err != nil {
		return pluginapi.IRPatch{}, err
	}
	return pluginapi.IRPatch{
		Op:      pluginapi.PatchAdd,
		Path:    "/resources/listeners/" + listenerRef + "/tls/client_certificate",
		Kind:    plugins.PatchKindListenerMTLS,
		Value:   value,
		Summary: "verify ingress client certificates with configured mTLS trust store",
		Source:  pluginapi.PatchSource{Plugin: Name, Version: Version, Capability: capabilityIngressClientCA},
	}, nil
}

func serviceSecurityGroupRef(graph ir.Graph) string {
	preferred := "security-group:" + graph.Service
	for _, group := range graph.Resources.SecurityGroups {
		if group.Meta.LogicalID == preferred {
			return group.Meta.LogicalID
		}
	}
	if len(graph.Resources.SecurityGroups) > 0 {
		return graph.Resources.SecurityGroups[0].Meta.LogicalID
	}
	return ""
}

func serviceIAMRoleRef(graph ir.Graph) string {
	preferred := "iam-role:" + graph.Service
	for _, role := range graph.Resources.IAMRoles {
		if role.Meta.LogicalID == preferred {
			return role.Meta.LogicalID
		}
	}
	if len(graph.Resources.IAMRoles) > 0 {
		return graph.Resources.IAMRoles[0].Meta.LogicalID
	}
	return ""
}

func graphHasPluginSource(graph ir.Graph) bool {
	for _, role := range graph.Resources.IAMRoles {
		if metaHasPluginSource(role.Meta) {
			return true
		}
	}
	for _, group := range graph.Resources.SecurityGroups {
		if metaHasPluginSource(group.Meta) {
			return true
		}
	}
	for _, listener := range graph.Resources.Listeners {
		if metaHasPluginSource(listener.Meta) {
			return true
		}
	}
	return false
}

func metaHasPluginSource(meta ir.ResourceMeta) bool {
	for _, source := range meta.Source {
		if source.Path == "plugin:"+Name {
			return true
		}
	}
	return false
}

func doctorFinding(service, code, summary string) (pluginapi.DoctorFinding, bool) {
	switch code {
	case FindingProxyUnhealthy:
		return pluginapi.DoctorFinding{Code: code, Severity: "high", Service: service, Summary: firstNonEmpty(summary, "mTLS local proxy is unhealthy"), Confidence: 0.9}, true
	case FindingCertExpiring:
		return pluginapi.DoctorFinding{Code: code, Severity: "medium", Service: service, Summary: firstNonEmpty(summary, "mTLS workload certificate is nearing expiry"), Confidence: 0.85}, true
	case FindingCertExpired:
		return pluginapi.DoctorFinding{Code: code, Severity: "critical", Service: service, Summary: firstNonEmpty(summary, "mTLS workload certificate is expired"), Confidence: 0.95}, true
	case FindingPolicyMismatch:
		return pluginapi.DoctorFinding{Code: code, Severity: "high", Service: service, Summary: firstNonEmpty(summary, "mTLS policy does not match deployed listener or egress rules"), Confidence: 0.85}, true
	default:
		return pluginapi.DoctorFinding{}, false
	}
}

func requestBody(request any) (json.RawMessage, error) {
	if raw, ok := request.(json.RawMessage); ok {
		return raw, nil
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func assign(dst any, src any) error {
	if dst == nil {
		return nil
	}
	body, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}

func diagnostic(code, severity, field, summary string) pluginapi.Diagnostic {
	return pluginapi.Diagnostic{Code: code, Severity: severity, Field: field, Summary: summary}
}

func hasError(diagnostics []pluginapi.Diagnostic) bool {
	for _, item := range diagnostics {
		if item.Severity == "error" {
			return true
		}
	}
	return false
}

func validReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, "://")
}

func validName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || len(value) > 63 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
