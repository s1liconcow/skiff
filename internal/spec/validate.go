package spec

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
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
	case KindService, KindWorker, KindJob, KindManagedDatabase, KindStatefulGroup, KindStack, KindMultiRegionStack:
	default:
		add("$.kind", "UNSUPPORTED_KIND", "kind must be Service, Worker, Job, ManagedDatabase, StatefulGroup, Stack, or MultiRegionStack")
	}
	validateName(&diagnostics, "$.metadata.name", doc.Metadata.Name, "metadata.name is required and must be a DNS-style Skiff name")
	validateName(&diagnostics, "$.metadata.env", doc.Metadata.Env, "metadata.env is required and must be a DNS-style Skiff name")

	switch doc.Kind {
	case KindService, KindWorker, KindJob:
		validateArtifactAt(&diagnostics, doc.Artifact, doc.Metadata.Env, "$.artifact")
		validateRuntimeAt(&diagnostics, doc.Kind, doc.Runtime, "$.runtime")
		validateScaleAt(&diagnostics, doc.Kind, doc.Scale, "$.scale")
	case KindManagedDatabase:
		if doc.ManagedDatabase == nil {
			add("$.database", "REQUIRED", "managed database specs must include database settings")
		} else {
			validateManagedDatabase(&diagnostics, *doc.ManagedDatabase, "$.database")
		}
	case KindStatefulGroup:
		if doc.StatefulGroup == nil {
			add("$.stateful", "REQUIRED", "stateful group specs must include stateful settings")
		}
	case KindStack:
		if doc.Stack == nil {
			add("$.stack", "REQUIRED", "stack specs must include stack settings")
		} else {
			validateStack(&diagnostics, doc)
		}
	case KindMultiRegionStack:
		if doc.MultiRegion == nil {
			add("$.multiRegion", "REQUIRED", "multi-region stack specs must include multiRegion settings")
		} else {
			validateMultiRegionStack(&diagnostics, doc)
		}
	}
	validateNetworkAt(&diagnostics, doc.Network, "$.network")
	validateSecretsAt(&diagnostics, doc.Secrets, "$.secrets")

	return Result{
		OK:          len(diagnostics) == 0,
		APIVersion:  doc.APIVersion,
		Kind:        doc.Kind,
		Name:        doc.Metadata.Name,
		Env:         doc.Metadata.Env,
		Diagnostics: diagnostics,
	}
}

func validateMultiRegionStack(diagnostics *[]Diagnostic, doc Document) {
	stack := doc.MultiRegion
	validateName(diagnostics, "$.multiRegion.primaryRegion", stack.PrimaryRegion, "primaryRegion is required and must be a DNS-style region name")
	if len(stack.SecondaryRegions) == 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.secondaryRegions", Code: "REQUIRED", Severity: SeverityError, Message: "at least one secondary region is required"})
	}
	regions := map[string]struct{}{}
	if stack.PrimaryRegion != "" {
		regions[stack.PrimaryRegion] = struct{}{}
	}
	for i, region := range stack.SecondaryRegions {
		base := fmt.Sprintf("$.multiRegion.secondaryRegions[%d]", i)
		validateName(diagnostics, base, region, "secondary region must be a DNS-style region name")
		if _, ok := regions[region]; ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base, Code: "DUPLICATE_REGION", Severity: SeverityError, Message: "regions must be unique"})
		}
		regions[region] = struct{}{}
	}
	validateName(diagnostics, "$.multiRegion.service.name", stack.Service.Name, "multi-region service name must be a DNS-style Skiff name")
	validateArtifactAt(diagnostics, stack.Service.Artifact, doc.Metadata.Env, "$.multiRegion.service.artifact")
	validateRuntimeAt(diagnostics, KindService, stack.Service.Runtime, "$.multiRegion.service.runtime")
	validateScaleAt(diagnostics, KindService, stack.Service.Scale, "$.multiRegion.service.scale")
	validateNetworkAt(diagnostics, stack.Service.Network, "$.multiRegion.service.network")
	validateSecretsAt(diagnostics, stack.Service.Secrets, "$.multiRegion.service.secrets")
	validateName(diagnostics, "$.multiRegion.database.name", stack.Database.Name, "multi-region database name must be a DNS-style Skiff name")
	validateManagedDatabase(diagnostics, stack.Database.ManagedDatabase, "$.multiRegion.database")
	if stack.Binding.From != stack.Service.Name {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.binding.from", Code: "UNKNOWN_STACK_SERVICE", Severity: SeverityError, Message: "binding.from must name the multi-region service"})
	}
	if stack.Binding.To != stack.Database.Name {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.binding.to", Code: "UNKNOWN_STACK_DATABASE", Severity: SeverityError, Message: "binding.to must name the multi-region database"})
	}
	if !validEnvName(stack.Binding.As) {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.binding.as", Code: "INVALID_ENV_NAME", Severity: SeverityError, Message: "binding.as must be an environment variable name like DATABASE_URL"})
	}
	switch strings.ToLower(strings.TrimSpace(stack.TrafficPolicy.Mode)) {
	case "weighted-dns", "global-load-balancer":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.trafficPolicy.mode", Code: "UNSUPPORTED_TRAFFIC_POLICY", Severity: SeverityError, Message: "traffic policy mode must be weighted-dns or global-load-balancer"})
	}
	if stack.TrafficPolicy.Host != "" && (net.ParseIP(stack.TrafficPolicy.Host) != nil || !strings.Contains(stack.TrafficPolicy.Host, ".")) {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.trafficPolicy.host", Code: "INVALID_HOST", Severity: SeverityError, Message: "traffic policy host must be a DNS hostname"})
	}
	for i, weight := range stack.TrafficPolicy.Weights {
		base := fmt.Sprintf("$.multiRegion.trafficPolicy.weights[%d]", i)
		if _, ok := regions[weight.Region]; !ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".region", Code: "UNKNOWN_REGION", Severity: SeverityError, Message: "traffic weight region must name a primary or secondary region"})
		}
		if weight.Weight < 0 || weight.Weight > 100 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".weight", Code: "INVALID_TRAFFIC_WEIGHT", Severity: SeverityError, Message: "traffic weight must be between 0 and 100"})
		}
	}
	switch strings.ToLower(strings.TrimSpace(stack.DatabaseReplication.Mode)) {
	case "async", "sync", "snapshot-seed":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.databaseReplication.mode", Code: "UNSUPPORTED_REPLICATION_MODE", Severity: SeverityError, Message: "database replication mode must be async, sync, or snapshot-seed"})
	}
	if stack.DatabaseReplication.MaxReplicaLag != "" {
		if _, err := time.ParseDuration(stack.DatabaseReplication.MaxReplicaLag); err != nil {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.databaseReplication.maxReplicaLag", Code: "INVALID_DURATION", Severity: SeverityError, Message: "maxReplicaLag must be a Go duration like 30s"})
		}
	}
	if stack.FailoverPolicy.MaxReplicaLag != "" {
		if _, err := time.ParseDuration(stack.FailoverPolicy.MaxReplicaLag); err != nil {
			*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.failoverPolicy.maxReplicaLag", Code: "INVALID_DURATION", Severity: SeverityError, Message: "failoverPolicy.maxReplicaLag must be a Go duration like 30s"})
		}
	}
	switch strings.ToLower(strings.TrimSpace(stack.FailoverPolicy.Failback)) {
	case "", "plan-required", "manual-only":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.multiRegion.failoverPolicy.failback", Code: "UNSUPPORTED_FAILBACK_POLICY", Severity: SeverityError, Message: "failback must be plan-required or manual-only"})
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

func validateArtifactAt(diagnostics *[]Diagnostic, artifact *Artifact, env, path string) {
	if artifact == nil {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "REQUIRED", Severity: SeverityError, Message: "workload specs must include an artifact"})
		return
	}
	switch artifact.Type {
	case "oci", "tarball", "binary":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".type", Code: "UNSUPPORTED_ARTIFACT_TYPE", Severity: SeverityError, Message: "artifact.type must be oci, tarball, or binary"})
	}
	if strings.TrimSpace(artifact.Ref) == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ref", Code: "REQUIRED", Severity: SeverityError, Message: "artifact.ref is required"})
	}
	if isProductionEnv(env) {
		switch artifact.Type {
		case "oci":
			if !strings.Contains(artifact.Ref, "@sha256:") {
				*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ref", Code: "MUTABLE_ARTIFACT_REF", Severity: SeverityError, Message: "production OCI artifacts must be digest-pinned with @sha256:"})
			}
		case "tarball", "binary":
			if !strings.HasPrefix(artifact.Digest, "sha256:") {
				*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".digest", Code: "ARTIFACT_DIGEST_REQUIRED", Severity: SeverityError, Message: "production tarball and binary artifacts require a sha256 digest"})
			}
		}
	}
}

func validateRuntimeAt(diagnostics *[]Diagnostic, kind Kind, runtime Runtime, path string) {
	if kind == KindService {
		if runtime.Port <= 0 || runtime.Port > 65535 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".port", Code: "INVALID_PORT", Severity: SeverityError, Message: "services must set runtime.port between 1 and 65535"})
		}
		if runtime.Health.Path == "" && len(runtime.Health.Command) == 0 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".health", Code: "HEALTH_CHECK_REQUIRED", Severity: SeverityError, Message: "services must define an HTTP or exec health check"})
		}
	}
	if runtime.Health.Path != "" && !strings.HasPrefix(runtime.Health.Path, "/") {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".health.path", Code: "INVALID_PATH", Severity: SeverityError, Message: "health check paths must start with /"})
	}
	if runtime.Health.Port < 0 || runtime.Health.Port > 65535 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".health.port", Code: "INVALID_PORT", Severity: SeverityError, Message: "health check port must be between 1 and 65535"})
	}
}

func validateScaleAt(diagnostics *[]Diagnostic, kind Kind, scale Scale, path string) {
	if kind != KindService && kind != KindWorker {
		return
	}
	if scale.Min < 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".min", Code: "INVALID_SCALE", Severity: SeverityError, Message: "scale.min cannot be negative"})
	}
	if scale.Max < 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".max", Code: "INVALID_SCALE", Severity: SeverityError, Message: "scale.max must be at least 1"})
	}
	if scale.Min > scale.Max {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_SCALE", Severity: SeverityError, Message: "scale.min cannot exceed scale.max"})
	}
}

func validateNetworkAt(diagnostics *[]Diagnostic, network Network, path string) {
	if network.Ingress == nil {
		return
	}
	ingress := network.Ingress
	switch ingress.Type {
	case "", "private", "public-http", "internal-http":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ingress.type", Code: "UNSUPPORTED_INGRESS_TYPE", Severity: SeverityError, Message: "ingress type must be private, public-http, or internal-http"})
	}
	if ingress.Type == "public-http" {
		if strings.TrimSpace(ingress.Host) == "" {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ingress.host", Code: "REQUIRED", Severity: SeverityError, Message: "public ingress requires a host"})
		} else if net.ParseIP(ingress.Host) != nil || !strings.Contains(ingress.Host, ".") {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ingress.host", Code: "INVALID_HOST", Severity: SeverityError, Message: "public ingress host must be a DNS hostname"})
		}
		tlsEnabled := ingress.TLS != nil && ingress.TLS.Enabled
		certRef := ingress.CertRef
		if ingress.TLS != nil && ingress.TLS.CertRef != "" {
			certRef = ingress.TLS.CertRef
		}
		if !tlsEnabled || strings.TrimSpace(certRef) == "" {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ingress.tls", Code: "TLS_REQUIRED", Severity: SeverityError, Message: "public ingress requires TLS and a certificate reference"})
		}
	}
}

func validateSecretsAt(diagnostics *[]Diagnostic, secrets []SecretRef, path string) {
	seen := make(map[string]struct{}, len(secrets))
	for i, secret := range secrets {
		base := fmt.Sprintf("%s[%d]", path, i)
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

func validateManagedDatabase(diagnostics *[]Diagnostic, db ManagedDatabase, path string) {
	switch strings.ToLower(strings.TrimSpace(db.Engine)) {
	case "postgres", "postgresql", "mysql", "aurora-postgresql", "aurora-mysql":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".engine", Code: "UNSUPPORTED_DATABASE_ENGINE", Severity: SeverityError, Message: "database engine must be postgres, postgresql, mysql, aurora-postgresql, or aurora-mysql"})
	}
	if strings.TrimSpace(db.Version) == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".version", Code: "REQUIRED", Severity: SeverityError, Message: "database version is required"})
	}
	switch strings.ToLower(strings.TrimSpace(db.Size)) {
	case "small", "medium", "large":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".size", Code: "UNSUPPORTED_DATABASE_SIZE", Severity: SeverityError, Message: "database size must be small, medium, or large"})
	}
	if db.Storage.SizeGB < 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".storage.sizeGB", Code: "INVALID_STORAGE", Severity: SeverityError, Message: "database storage.sizeGB must be at least 1"})
	}
	switch db.Storage.Type {
	case "", "gp3", "io1", "standard":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".storage.type", Code: "UNSUPPORTED_STORAGE_TYPE", Severity: SeverityError, Message: "database storage.type must be gp3, io1, or standard"})
	}
	if db.Backups.RetentionDays < 1 || db.Backups.RetentionDays > 35 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".backups.retentionDays", Code: "INVALID_BACKUP_RETENTION", Severity: SeverityError, Message: "database backups.retentionDays must be between 1 and 35"})
	}
}

func validateStack(diagnostics *[]Diagnostic, doc Document) {
	stack := doc.Stack
	if len(stack.Services) == 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.services", Code: "REQUIRED", Severity: SeverityError, Message: "stack specs must include at least one service"})
	}
	if len(stack.Databases) == 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.databases", Code: "REQUIRED", Severity: SeverityError, Message: "api-database stack specs must include at least one managed database"})
	}
	if len(stack.Services) > 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.services", Code: "UNSUPPORTED_STACK_SHAPE", Severity: SeverityError, Message: "this Skiff version supports one service per API/database stack"})
	}
	if len(stack.Databases) > 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.databases", Code: "UNSUPPORTED_STACK_SHAPE", Severity: SeverityError, Message: "this Skiff version supports one managed database per API/database stack"})
	}
	serviceNames := map[string]struct{}{}
	for i, service := range stack.Services {
		base := fmt.Sprintf("$.stack.services[%d]", i)
		validateName(diagnostics, base+".name", service.Name, "stack service name must be a DNS-style Skiff name")
		serviceNames[service.Name] = struct{}{}
		validateArtifactAt(diagnostics, service.Artifact, doc.Metadata.Env, base+".artifact")
		validateRuntimeAt(diagnostics, KindService, service.Runtime, base+".runtime")
		validateScaleAt(diagnostics, KindService, service.Scale, base+".scale")
		validateNetworkAt(diagnostics, service.Network, base+".network")
		validateSecretsAt(diagnostics, service.Secrets, base+".secrets")
	}
	databaseNames := map[string]struct{}{}
	for i, database := range stack.Databases {
		base := fmt.Sprintf("$.stack.databases[%d]", i)
		validateName(diagnostics, base+".name", database.Name, "stack database name must be a DNS-style Skiff name")
		databaseNames[database.Name] = struct{}{}
		validateManagedDatabase(diagnostics, database.ManagedDatabase, base)
	}
	for i, binding := range stack.Bindings {
		base := fmt.Sprintf("$.stack.bindings[%d]", i)
		if _, ok := serviceNames[binding.From]; !ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".from", Code: "UNKNOWN_STACK_SERVICE", Severity: SeverityError, Message: "binding.from must name a service in this stack"})
		}
		if _, ok := databaseNames[binding.To]; !ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".to", Code: "UNKNOWN_STACK_DATABASE", Severity: SeverityError, Message: "binding.to must name a database in this stack"})
		}
		if !validEnvName(binding.As) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".as", Code: "INVALID_ENV_NAME", Severity: SeverityError, Message: "binding.as must be an environment variable name like DATABASE_URL"})
		}
	}
	if len(stack.Bindings) == 0 && len(stack.Services) > 0 && len(stack.Databases) > 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.bindings", Code: "REQUIRED", Severity: SeverityError, Message: "api-database stack specs must bind the service to the database"})
	}
}

func validSecretRef(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "aws-secretsmanager://") ||
		strings.HasPrefix(value, "aws-ssm://") ||
		strings.HasPrefix(value, "secret://")
}

func validEnvName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for i, r := range value {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			if i == 0 && r >= '0' && r <= '9' {
				return false
			}
			continue
		}
		return false
	}
	return true
}
