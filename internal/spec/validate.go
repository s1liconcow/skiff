package spec

import (
	"fmt"
	"net"
	"net/url"
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

type ValidationOptions struct {
	RequireDigestPinnedArtifacts bool
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
	return ValidateWithOptions(doc, ValidationOptions{})
}

func ValidateWithOptions(doc Document, opts ValidationOptions) Result {
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
		validateArtifactAt(&diagnostics, doc.Artifact, opts, "$.artifact")
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
		} else {
			validateStatefulGroup(&diagnostics, *doc.StatefulGroup, "$.stateful")
		}
	case KindStack:
		if doc.Stack == nil {
			add("$.stack", "REQUIRED", "stack specs must include stack settings")
		} else {
			validateStack(&diagnostics, doc, opts)
		}
	case KindMultiRegionStack:
		if doc.MultiRegion == nil {
			add("$.multiRegion", "REQUIRED", "multi-region stack specs must include multiRegion settings")
		} else {
			validateMultiRegionStack(&diagnostics, doc, opts)
		}
	}
	validateNetworkAt(&diagnostics, doc.Network, "$.network")
	validateSecretsAt(&diagnostics, doc.Secrets, "$.secrets")
	validateAddonsAt(&diagnostics, doc.Addons, "$.addons")

	return Result{
		OK:          len(diagnostics) == 0,
		APIVersion:  doc.APIVersion,
		Kind:        doc.Kind,
		Name:        doc.Metadata.Name,
		Env:         doc.Metadata.Env,
		Diagnostics: diagnostics,
	}
}

func validateMultiRegionStack(diagnostics *[]Diagnostic, doc Document, opts ValidationOptions) {
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
	validateArtifactAt(diagnostics, stack.Service.Artifact, opts, "$.multiRegion.service.artifact")
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
var exactSemverPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
var wildcardSemverPattern = regexp.MustCompile(`^v?[0-9]+(?:\.[0-9]+)?\.x$`)
var caretTildeSemverPattern = regexp.MustCompile(`^[~^]v?[0-9]+(?:\.[0-9]+){0,2}$`)
var comparatorSemverPattern = regexp.MustCompile(`^(?:>=|<=|>|<|=)v?[0-9]+(?:\.[0-9]+){0,2}$`)

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

func validateArtifactAt(diagnostics *[]Diagnostic, artifact *Artifact, opts ValidationOptions, path string) {
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
	if opts.RequireDigestPinnedArtifacts {
		switch artifact.Type {
		case "oci":
			if !strings.Contains(artifact.Ref, "@sha256:") {
				*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ref", Code: "MUTABLE_ARTIFACT_REF", Severity: SeverityError, Message: "production-like OCI artifacts must be digest-pinned with @sha256:"})
			}
		case "tarball", "binary":
			if !strings.HasPrefix(artifact.Digest, "sha256:") {
				*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".digest", Code: "ARTIFACT_DIGEST_REQUIRED", Severity: SeverityError, Message: "production-like tarball and binary artifacts require a sha256 digest"})
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
		if strings.TrimSpace(ingress.Host) != "" && (net.ParseIP(ingress.Host) != nil || !strings.Contains(ingress.Host, ".")) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".ingress.host", Code: "INVALID_HOST", Severity: SeverityError, Message: "public ingress host must be a DNS hostname"})
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

func validateAddonsAt(diagnostics *[]Diagnostic, addons []Addon, path string) {
	seen := make(map[string]struct{}, len(addons))
	for i, addon := range addons {
		base := fmt.Sprintf("%s[%d]", path, i)
		validateName(diagnostics, base+".name", addon.Name, "addon name must be a DNS-style Skiff name")
		if addon.Name != "" {
			if _, ok := seen[addon.Name]; ok {
				*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".name", Code: "DUPLICATE_ADDON", Severity: SeverityError, Message: "addon names must be unique"})
			}
			seen[addon.Name] = struct{}{}
		}
		if strings.TrimSpace(addon.Mode) != "" && len(addon.Mode) > 64 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".mode", Code: "INVALID_ADDON_MODE", Severity: SeverityError, Message: "addon mode must be 64 characters or fewer"})
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

func validateStatefulGroup(diagnostics *[]Diagnostic, group StatefulGroup, path string) {
	if group.Replicas < 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".replicas", Code: "INVALID_REPLICAS", Severity: SeverityError, Message: "stateful group replicas must be at least 1"})
	}
	if group.Replicas > 0 && len(group.Members) > 0 && len(group.Members) != group.Replicas {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".members", Code: "MEMBER_COUNT_MISMATCH", Severity: SeverityError, Message: "stateful members must match replicas when listed explicitly"})
	}
	seenMembers := map[int]struct{}{}
	for i, member := range group.Members {
		base := fmt.Sprintf("%s.members[%d]", path, i)
		if member.Ordinal < 0 {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".ordinal", Code: "INVALID_MEMBER", Severity: SeverityError, Message: "member ordinal must be non-negative"})
		}
		if _, ok := seenMembers[member.Ordinal]; ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".ordinal", Code: "DUPLICATE_MEMBER", Severity: SeverityError, Message: "member ordinals must be unique"})
		}
		seenMembers[member.Ordinal] = struct{}{}
		if member.Zone != "" && !skiffNamePattern.MatchString(member.Zone) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".zone", Code: "INVALID_ZONE", Severity: SeverityError, Message: "member zone must be a DNS-style zone name"})
		}
		if member.DNSName != "" && (net.ParseIP(member.DNSName) != nil || !strings.Contains(member.DNSName, ".")) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".dnsName", Code: "INVALID_DNS_NAME", Severity: SeverityError, Message: "member dnsName must be a DNS hostname"})
		}
	}
	if strings.TrimSpace(group.Volume.Size) == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".volume.size", Code: "REQUIRED", Severity: SeverityError, Message: "stateful volume size is required"})
	}
	switch strings.ToLower(strings.TrimSpace(group.Volume.Type)) {
	case "", "gp3", "io1", "standard":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".volume.type", Code: "UNSUPPORTED_VOLUME_TYPE", Severity: SeverityError, Message: "stateful volume type must be gp3, io1, or standard"})
	}
	if strings.TrimSpace(group.Volume.MountPath) != "" && !strings.HasPrefix(group.Volume.MountPath, "/") {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".volume.mountPath", Code: "INVALID_PATH", Severity: SeverityError, Message: "stateful volume mountPath must be absolute"})
	}
	switch strings.ToLower(strings.TrimSpace(group.Update.Strategy)) {
	case "", "ordered":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".update.strategy", Code: "UNSUPPORTED_UPDATE_STRATEGY", Severity: SeverityError, Message: "stateful update strategy must be ordered"})
	}
	if group.Recipe.Name == "" && group.Recipe.Ref == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".recipe", Code: "RECIPE_REQUIRED", Severity: SeverityError, Message: "stateful groups require a recipe name or plugin ref"})
	}
}

func validateStack(diagnostics *[]Diagnostic, doc Document, opts ValidationOptions) {
	stack := doc.Stack
	if len(stack.Services) == 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.services", Code: "REQUIRED", Severity: SeverityError, Message: "stack specs must include at least one service"})
	}
	if len(stack.Databases) == 0 && len(stack.ObjectStores) == 0 && len(stack.Dependencies) == 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack", Code: "REQUIRED", Severity: SeverityError, Message: "stack specs must include at least one managed database, object store, or package dependency"})
	}
	if len(stack.Services) > 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.services", Code: "UNSUPPORTED_STACK_SHAPE", Severity: SeverityError, Message: "this Skiff version supports one service per stack recipe"})
	}
	if len(stack.Databases) > 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.databases", Code: "UNSUPPORTED_STACK_SHAPE", Severity: SeverityError, Message: "this Skiff version supports one managed database per API/database stack"})
	}
	if len(stack.ObjectStores) > 1 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.objectStores", Code: "UNSUPPORTED_STACK_SHAPE", Severity: SeverityError, Message: "this Skiff version supports one object store per API/object-store stack"})
	}
	serviceNames := map[string]struct{}{}
	for i, service := range stack.Services {
		base := fmt.Sprintf("$.stack.services[%d]", i)
		validateName(diagnostics, base+".name", service.Name, "stack service name must be a DNS-style Skiff name")
		serviceNames[service.Name] = struct{}{}
		validateArtifactAt(diagnostics, service.Artifact, opts, base+".artifact")
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
	objectStoreNames := map[string]struct{}{}
	for i, store := range stack.ObjectStores {
		base := fmt.Sprintf("$.stack.objectStores[%d]", i)
		validateName(diagnostics, base+".name", store.Name, "stack object store name must be a DNS-style Skiff name")
		if _, ok := databaseNames[store.Name]; ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".name", Code: "DUPLICATE_STACK_RESOURCE", Severity: SeverityError, Message: "object store names must not duplicate database names in the same stack"})
		}
		objectStoreNames[store.Name] = struct{}{}
		validateStackObjectStore(diagnostics, store, base)
	}
	dependencyNames := map[string]struct{}{}
	for i, dependency := range stack.Dependencies {
		base := fmt.Sprintf("$.stack.dependencies[%d]", i)
		validateName(diagnostics, base+".name", dependency.Name, "stack dependency name must be a DNS-style Skiff name")
		if dependency.Name != "" {
			if _, ok := databaseNames[dependency.Name]; ok {
				*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".name", Code: "DUPLICATE_STACK_RESOURCE", Severity: SeverityError, Message: "dependency names must not duplicate database names in the same stack"})
			}
			if _, ok := objectStoreNames[dependency.Name]; ok {
				*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".name", Code: "DUPLICATE_STACK_RESOURCE", Severity: SeverityError, Message: "dependency names must not duplicate object store names in the same stack"})
			}
			if _, ok := dependencyNames[dependency.Name]; ok {
				*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".name", Code: "DUPLICATE_STACK_RESOURCE", Severity: SeverityError, Message: "dependency names must be unique"})
			}
			dependencyNames[dependency.Name] = struct{}{}
		}
		validatePackageRef(diagnostics, base+".uses", dependency.Uses)
		validatePackageVersionRange(diagnostics, base+".version", dependency.Version)
	}
	for i, binding := range stack.Bindings {
		base := fmt.Sprintf("$.stack.bindings[%d]", i)
		if _, ok := serviceNames[binding.From]; !ok {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".from", Code: "UNKNOWN_STACK_SERVICE", Severity: SeverityError, Message: "binding.from must name a service in this stack"})
		}
		_, bindsDatabase := databaseNames[binding.To]
		_, bindsObjectStore := objectStoreNames[binding.To]
		_, bindsDependency := dependencyNames[binding.To]
		if !bindsDatabase && !bindsObjectStore && !bindsDependency {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".to", Code: "UNKNOWN_STACK_RESOURCE", Severity: SeverityError, Message: "binding.to must name a database, object store, or package dependency in this stack"})
		}
		if !validEnvName(binding.As) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: base + ".as", Code: "INVALID_ENV_NAME", Severity: SeverityError, Message: "binding.as must be an environment variable name like DATABASE_URL"})
		}
	}
	if len(stack.Bindings) == 0 && len(stack.Services) > 0 && len(stack.Databases)+len(stack.ObjectStores)+len(stack.Dependencies) > 0 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: "$.stack.bindings", Code: "REQUIRED", Severity: SeverityError, Message: "stack specs must bind the service to its database, object store, or package dependency"})
	}
}

func validatePackageRef(diagnostics *[]Diagnostic, path, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "REQUIRED", Severity: SeverityError, Message: "dependency uses is required"})
		return
	}
	switch {
	case strings.HasPrefix(value, "skiff.dev/"):
		name := strings.TrimPrefix(value, "skiff.dev/")
		if !validPackagePath(name) {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_PACKAGE_REF", Severity: SeverityError, Message: "skiff.dev package refs must look like skiff.dev/name"})
		}
	case strings.HasPrefix(value, "oci://"):
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_PACKAGE_REF", Severity: SeverityError, Message: "OCI package refs must look like oci://registry/repo/name:version"})
		}
	case strings.HasPrefix(value, "file://"):
		if strings.TrimSpace(strings.TrimPrefix(value, "file://")) == "" {
			*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_PACKAGE_REF", Severity: SeverityError, Message: "file package refs must include a local package path"})
		}
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_PACKAGE_REF", Severity: SeverityError, Message: "package refs must use skiff.dev/name, oci://registry/repo/name:version, or file://../local-package"})
	}
}

func validatePackageVersionRange(diagnostics *[]Diagnostic, path, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "REQUIRED", Severity: SeverityError, Message: "dependency version is required"})
		return
	}
	if !validPackageVersionRange(value) {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path, Code: "INVALID_PACKAGE_VERSION", Severity: SeverityError, Message: "dependency version must be an exact semver or a semver range such as 1.x, ^1.2.0, or >=1.2.0 <2.0.0"})
	}
}

func validPackagePath(value string) bool {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) == 0 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 63 || !skiffNamePattern.MatchString(part) {
			return false
		}
	}
	return true
}

func validPackageVersionRange(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\n\r\t") {
		return false
	}
	if exactSemverPattern.MatchString(value) || wildcardSemverPattern.MatchString(value) || caretTildeSemverPattern.MatchString(value) {
		return true
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !comparatorSemverPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func validateStackObjectStore(diagnostics *[]Diagnostic, store StackObjectStore, path string) {
	if strings.TrimSpace(store.URI) == "" {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".uri", Code: "REQUIRED", Severity: SeverityError, Message: "object store uri is required"})
	} else if !validObjectStoreURI(store.URI) {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".uri", Code: "INVALID_OBJECT_STORE_URI", Severity: SeverityError, Message: "object store uri must be an object-store URI such as s3://bucket/prefix"})
	}
	switch strings.ToLower(strings.TrimSpace(store.Access)) {
	case "", "read-only", "read-write":
	default:
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".access", Code: "UNSUPPORTED_OBJECT_STORE_ACCESS", Severity: SeverityError, Message: "object store access must be read-only or read-write"})
	}
	if store.Purpose != "" && len(store.Purpose) > 64 {
		*diagnostics = append(*diagnostics, Diagnostic{Path: path + ".purpose", Code: "INVALID_OBJECT_STORE_PURPOSE", Severity: SeverityError, Message: "object store purpose must be 64 characters or fewer"})
	}
}

func validObjectStoreURI(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return false
	}
	switch parsed.Scheme {
	case "s3", "gs", "azblob":
		return strings.TrimSpace(parsed.Host) != ""
	case "file":
		return strings.HasPrefix(parsed.Path, "/")
	default:
		return false
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
