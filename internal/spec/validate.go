package spec

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
)

type Diagnostic struct {
	Path     string   `json:"path"`
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

type Result struct {
	OK          bool         `json:"ok"`
	APIVersion  string       `json:"apiVersion,omitempty"`
	Kind        Kind         `json:"kind,omitempty"`
	Name        string       `json:"name,omitempty"`
	Env         string       `json:"env,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type ValidationError struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (e ValidationError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "spec validation failed"
	}
	parts := make([]string, 0, len(e.Diagnostics))
	for _, diag := range e.Diagnostics {
		parts = append(parts, fmt.Sprintf("%s %s: %s", diag.Path, diag.Code, diag.Message))
	}
	return "spec validation failed: " + strings.Join(parts, "; ")
}

func Validate(doc Document) Result {
	var diagnostics []Diagnostic
	add := func(path, code, message string) {
		diagnostics = append(diagnostics, Diagnostic{
			Path:     path,
			Code:     code,
			Severity: SeverityError,
			Message:  message,
		})
	}

	if doc.APIVersion != APIVersion {
		add("$.apiVersion", "UNSUPPORTED_API_VERSION", "apiVersion must be skiff.dev/v1alpha1")
	}
	switch doc.Kind {
	case KindService, KindWorker, KindJob, KindManagedDatabase, KindStatefulGroup, KindStack:
	default:
		add("$.kind", "UNSUPPORTED_KIND", "kind must be Service, Worker, Job, ManagedDatabase, StatefulGroup, or Stack")
	}
	validateName(&diagnostics, "$.metadata.name", doc.Metadata.Name, "metadata.name is required and must be a DNS-style Skiff name")
	validateName(&diagnostics, "$.metadata.env", doc.Metadata.Env, "metadata.env is required and must be a DNS-style Skiff name")

	switch doc.Kind {
	case KindService, KindWorker, KindJob:
		validateArtifact(&diagnostics, doc)
		validateRuntime(&diagnostics, doc)
		validateScale(&diagnostics, doc)
	case KindManagedDatabase:
		if doc.ManagedDatabase == nil {
			add("$.database", "REQUIRED", "managed database specs must include database settings")
		}
	case KindStatefulGroup:
		if doc.StatefulGroup == nil {
			add("$.stateful", "REQUIRED", "stateful group specs must include stateful settings")
		}
	case KindStack:
		if doc.Stack == nil {
			add("$.stack", "REQUIRED", "stack specs must include stack settings")
		}
	}
	validateNetwork(&diagnostics, doc)
	validateSecrets(&diagnostics, doc)

	return Result{
		OK:          len(diagnostics) == 0,
		APIVersion:  doc.APIVersion,
		Kind:        doc.Kind,
		Name:        doc.Metadata.Name,
		Env:         doc.Metadata.Env,
		Diagnostics: diagnostics,
	}
}

var skiffNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func validateName(diagnostics *[]Diagnostic, path, value, message string) {
	value = strings.TrimSpace(value)
	if value == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "REQUIRED", Severity: SeverityError, Message: message})
		return
	}
	if len(value) > 63 || !skiffNamePattern.MatchString(value) {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_NAME", Severity: SeverityError, Message: message})
	}
}

func validateArtifact(diagnostics *[]Diagnostic, doc Document) {
	if doc.Artifact == nil {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.artifact", Code: "REQUIRED", Severity: SeverityError, Message: "workload specs must include an artifact"})
		return
	}
	switch doc.Artifact.Type {
	case "oci", "tarball", "binary":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.artifact.type", Code: "UNSUPPORTED_ARTIFACT_TYPE", Severity: SeverityError, Message: "artifact.type must be oci, tarball, or binary"})
	}
	if strings.TrimSpace(doc.Artifact.Ref) == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.artifact.ref", Code: "REQUIRED", Severity: SeverityError, Message: "artifact.ref is required"})
	}
	if isProductionEnv(doc.Metadata.Env) {
		switch doc.Artifact.Type {
		case "oci":
			if !strings.Contains(doc.Artifact.Ref, "@sha256:") {
				*diagnostics = append(*diagnostics, Diagnostic{Path: "$.artifact.ref", Code: "MUTABLE_ARTIFACT_REF", Severity: SeverityError, Message: "production OCI artifacts must be digest-pinned with @sha256:"})
			}
		case "tarball", "binary":
			if !strings.HasPrefix(doc.Artifact.Digest, "sha256:") {
				*diagnostics = append(*diagnostics, Diagnostic{Path: "$.artifact.digest", Code: "ARTIFACT_DIGEST_REQUIRED", Severity: SeverityError, Message: "production tarball and binary artifacts require a sha256 digest"})
			}
		}
	}
}

func validateRuntime(diagnostics *[]Diagnostic, doc Document) {
	if doc.Kind == KindService {
		if doc.Runtime.Port <= 0 || doc.Runtime.Port > 65535 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.runtime.port", Code: "INVALID_PORT", Severity: SeverityError, Message: "services must set runtime.port between 1 and 65535"})
		}
		if doc.Runtime.Health.Path == "" && len(doc.Runtime.Health.Command) == 0 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.runtime.health", Code: "HEALTH_CHECK_REQUIRED", Severity: SeverityError, Message: "services must define an HTTP or exec health check"})
		}
	}
	if doc.Runtime.Health.Path != "" && !strings.HasPrefix(doc.Runtime.Health.Path, "/") {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.runtime.health.path", Code: "INVALID_PATH", Severity: SeverityError, Message: "health check paths must start with /"})
	}
	if doc.Runtime.Health.Port < 0 || doc.Runtime.Health.Port > 65535 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.runtime.health.port", Code: "INVALID_PORT", Severity: SeverityError, Message: "health check port must be between 1 and 65535"})
	}
}

func validateScale(diagnostics *[]Diagnostic, doc Document) {
	if doc.Kind != KindService && doc.Kind != KindWorker {
		return
	}
	if doc.Scale.Min < 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.scale.min", Code: "INVALID_SCALE", Severity: SeverityError, Message: "scale.min cannot be negative"})
	}
	if doc.Scale.Max < 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.scale.max", Code: "INVALID_SCALE", Severity: SeverityError, Message: "scale.max must be at least 1"})
	}
	if doc.Scale.Min > doc.Scale.Max {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.scale", Code: "INVALID_SCALE", Severity: SeverityError, Message: "scale.min cannot exceed scale.max"})
	}
}

func validateNetwork(diagnostics *[]Diagnostic, doc Document) {
	if doc.Network.Ingress == nil {
		return
	}
	ingress := doc.Network.Ingress
	switch ingress.Type {
	case "", "private", "public-http", "internal-http":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.network.ingress.type", Code: "UNSUPPORTED_INGRESS_TYPE", Severity: SeverityError, Message: "ingress type must be private, public-http, or internal-http"})
	}
	if ingress.Type == "public-http" {
		if strings.TrimSpace(ingress.Host) == "" {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.network.ingress.host", Code: "REQUIRED", Severity: SeverityError, Message: "public ingress requires a host"})
		} else if net.ParseIP(ingress.Host) != nil || !strings.Contains(ingress.Host, ".") {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.network.ingress.host", Code: "INVALID_HOST", Severity: SeverityError, Message: "public ingress host must be a DNS hostname"})
		}
		tlsEnabled := ingress.TLS != nil && ingress.TLS.Enabled
		certRef := ingress.CertRef
		if ingress.TLS != nil && ingress.TLS.CertRef != "" {
			certRef = ingress.TLS.CertRef
		}
		if !tlsEnabled || strings.TrimSpace(certRef) == "" {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.network.ingress.tls", Code: "TLS_REQUIRED", Severity: SeverityError, Message: "public ingress requires TLS and a certificate reference"})
		}
	}
}

func validateSecrets(diagnostics *[]Diagnostic, doc Document) {
	seen := make(map[string]struct{}, len(doc.Secrets))
	for i, secret := range doc.Secrets {
		base := fmt.Sprintf("$.secrets[%d]", i)
		validateName(diagnostics, base+".name", secret.Name, "secret name must be a DNS-style Skiff name")
		if _, ok := seen[secret.Name]; secret.Name != "" && ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".name", Code: "DUPLICATE_SECRET", Severity: SeverityError, Message: "secret names must be unique"})
		}
		seen[secret.Name] = struct{}{}
		if !validSecretRef(secret.Ref) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".ref", Code: "INVALID_SECRET_REF", Severity: SeverityError, Message: "secrets must use secret manager references, not plaintext values"})
		}
	}
}

func validSecretRef(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "aws-secretsmanager://") ||
		strings.HasPrefix(value, "aws-ssm://") ||
		strings.HasPrefix(value, "secret://")
}
