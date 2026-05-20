package compiler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	internalpackages "github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
)

type compiledPackageDependency struct {
	Dependency   spec.StackDependency
	Index        int
	Entry        internalpackages.LockEntry
	Manifest     internalpackages.Manifest
	Provenance   ir.PackageProvenance
	ResourceName string
	Mode         string
	Config       packageDependencyConfig
}

type packageDependencyConfig struct {
	Mode     string                `json:"mode,omitempty"`
	Endpoint string                `json:"endpoint,omitempty"`
	Engine   string                `json:"engine,omitempty"`
	Version  string                `json:"version,omitempty"`
	Size     string                `json:"size,omitempty"`
	Region   string                `json:"region,omitempty"`
	Storage  spec.DatabaseStorage  `json:"storage,omitempty"`
	Backups  spec.DatabaseBackups  `json:"backups,omitempty"`
	Network  spec.DatabaseNetwork  `json:"network,omitempty"`
	Managed  *spec.ManagedDatabase `json:"managed,omitempty"`

	Stateful *spec.StatefulGroup    `json:"stateful,omitempty"`
	Replicas int                    `json:"replicas,omitempty"`
	Volume   spec.Volume            `json:"volume,omitempty"`
	Identity spec.StatefulIdentity  `json:"identity,omitempty"`
	Recipe   spec.StatefulRecipe    `json:"recipe,omitempty"`
	Update   spec.StatefulUpdate    `json:"update,omitempty"`
	Artifact *spec.Artifact         `json:"artifact,omitempty"`
	Runtime  map[string]interface{} `json:"runtime,omitempty"`
}

func compilePackageDependencies(doc spec.Document, opts Options, stackName, serviceName string) ([]compiledPackageDependency, error) {
	if doc.Stack == nil || len(doc.Stack.Dependencies) == 0 {
		return nil, nil
	}
	if opts.PackageLock == nil {
		return nil, fmt.Errorf("stack dependencies require skiff.lock.json for compile, plan, explain, and deploy")
	}
	out := make([]compiledPackageDependency, 0, len(doc.Stack.Dependencies))
	for i, dependency := range doc.Stack.Dependencies {
		entry, ok := internalpackages.FindLockEntryForDependency(*opts.PackageLock, dependency)
		if !ok {
			return nil, fmt.Errorf("stack dependency %q has no matching skiff.lock.json entry", dependency.Name)
		}
		manifest, ok := packageManifestForDependency(opts.PackageManifests, dependency, entry)
		if !ok {
			return nil, fmt.Errorf("stack dependency %q package manifest is not present in the local package cache", dependency.Name)
		}
		cfg, err := decodePackageDependencyConfig(dependency.Config)
		if err != nil {
			return nil, fmt.Errorf("stack dependency %q config: %w", dependency.Name, err)
		}
		provenance := packageProvenance(entry, opts.PackageLockDigest)
		out = append(out, compiledPackageDependency{
			Dependency:   dependency,
			Index:        i,
			Entry:        entry,
			Manifest:     manifest,
			Provenance:   provenance,
			ResourceName: stackComponentName(stackName, dependency.Name),
			Mode:         packageDependencyMode(dependency, manifest, cfg),
			Config:       cfg,
		})
		_ = serviceName
	}
	return out, nil
}

func packageManifestForDependency(manifests map[string]internalpackages.Manifest, dependency spec.StackDependency, entry internalpackages.LockEntry) (internalpackages.Manifest, bool) {
	if len(manifests) == 0 {
		return internalpackages.Manifest{}, false
	}
	for _, key := range []string{dependency.Name, entry.Name, entry.Ref, entry.Source, entry.Digest} {
		if key == "" {
			continue
		}
		if manifest, ok := manifests[key]; ok {
			return manifest, true
		}
	}
	return internalpackages.Manifest{}, false
}

func decodePackageDependencyConfig(raw json.RawMessage) (packageDependencyConfig, error) {
	if len(raw) == 0 {
		return packageDependencyConfig{}, nil
	}
	var cfg packageDependencyConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return packageDependencyConfig{}, err
	}
	return cfg, nil
}

func packageDependencyMode(dependency spec.StackDependency, manifest internalpackages.Manifest, cfg packageDependencyConfig) string {
	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case "managed", "managed-database", "database", "rds":
		return "managed"
	case "stateful", "stateful-group", "self-managed", "self_managed":
		return "stateful"
	}
	if cfg.Managed != nil || strings.TrimSpace(cfg.Engine) != "" {
		return "managed"
	}
	if cfg.Stateful != nil || cfg.Replicas > 0 || cfg.Recipe.Name != "" || cfg.Recipe.Ref != "" || cfg.Artifact != nil || len(cfg.Runtime) > 0 {
		return "stateful"
	}
	haystack := strings.ToLower(strings.Join(append([]string{dependency.Name, dependency.Uses, manifest.Name}, manifest.Exports.Dependencies...), " "))
	switch {
	case strings.Contains(haystack, "postgres"), strings.Contains(haystack, "mysql"), strings.Contains(haystack, "mariadb"):
		return "managed"
	default:
		return "stateful"
	}
}

func packageProvenance(entry internalpackages.LockEntry, lockDigest string) ir.PackageProvenance {
	return ir.PackageProvenance{
		Name:           entry.Name,
		Ref:            entry.Ref,
		Version:        entry.Version,
		Digest:         entry.Digest,
		ManifestDigest: entry.ManifestDigest,
		LockfileDigest: lockDigest,
	}
}

func packageSourceRef(provenance ir.PackageProvenance) ir.SourceRef {
	return ir.SourceRef{
		Package:        firstNonEmpty(provenance.Ref, provenance.Name),
		Version:        provenance.Version,
		Digest:         provenance.Digest,
		ManifestDigest: provenance.ManifestDigest,
		LockfileDigest: provenance.LockfileDigest,
	}
}

func packageTags(service, env string, dependency compiledPackageDependency) map[string]string {
	tags := ir.RequiredTags(service, env)
	tags[ir.TagPackage] = firstNonEmpty(dependency.Provenance.Ref, dependency.Provenance.Name)
	tags[ir.TagDependency] = dependency.Dependency.Name
	return tags
}

func annotatePackageMeta(meta *ir.ResourceMeta, sourcePath string, provenance ir.PackageProvenance, dependencyName string) {
	if meta.Tags == nil {
		meta.Tags = map[string]string{}
	}
	meta.Tags[ir.TagPackage] = firstNonEmpty(provenance.Ref, provenance.Name)
	if dependencyName != "" {
		meta.Tags[ir.TagDependency] = dependencyName
	}
	if sourcePath != "" {
		meta.Source = append(meta.Source, ir.SourceRef{Path: sourcePath})
	}
	meta.Source = append(meta.Source, packageSourceRef(provenance))
}

func addPackageGraphMetadata(graph *ir.Graph, dependencies []compiledPackageDependency, opts Options) {
	if len(dependencies) == 0 {
		return
	}
	graph.PackageLockDigest = opts.PackageLockDigest
	seen := map[string]struct{}{}
	for _, current := range graph.Packages {
		key := current.Ref + "\x00" + current.Digest
		seen[key] = struct{}{}
	}
	for _, dependency := range dependencies {
		key := dependency.Provenance.Ref + "\x00" + dependency.Provenance.Digest
		if _, ok := seen[key]; ok {
			continue
		}
		graph.Packages = append(graph.Packages, dependency.Provenance)
		seen[key] = struct{}{}
	}
}

func packageBindingEnv(dependency compiledPackageDependency, binding spec.StackBinding) (envName, value string, secret spec.SecretRef, hasSecret bool) {
	envName = binding.As
	switch dependency.Mode {
	case "managed":
		secretRef := databaseConnectionSecretRef(dependency.ResourceName)
		return envName, secretRef, spec.SecretRef{Name: envSecretName(binding.As), Ref: secretRef}, true
	default:
		endpoint := strings.TrimSpace(dependency.Config.Endpoint)
		if endpoint == "" {
			endpoint = "skiff://stateful/" + dependency.ResourceName
		}
		return envName, endpoint, spec.SecretRef{}, false
	}
}

func appendPackageDependencyResources(graph *ir.Graph, dependencies []compiledPackageDependency, bindings []spec.StackBinding, serviceName, env string) {
	for _, dependency := range dependencies {
		sourcePath := fmt.Sprintf("$.stack.dependencies[%d]", dependency.Index)
		switch dependency.Mode {
		case "managed":
			appendPackageManagedDatabase(graph, dependency, bindings, serviceName, env, sourcePath)
		default:
			appendPackageStatefulGroup(graph, dependency, serviceName, env, sourcePath)
		}
		appendPackageOperation(graph, dependency, serviceName, env, sourcePath)
	}
}

func appendPackageManagedDatabase(graph *ir.Graph, dependency compiledPackageDependency, bindings []spec.StackBinding, serviceName, env, sourcePath string) {
	dbSpec := packageManagedDatabaseSpec(dependency)
	databaseName := dependency.ResourceName
	dbPort := databasePort(dbSpec.Engine)
	graph.Resources.SecurityGroups = append(graph.Resources.SecurityGroups, packageDatabaseSecurityGroup(serviceName, env, databaseName, dbPort, dependency, sourcePath))
	addDatabaseEgress(graph, databaseName, dbPort)

	dbBindings := packageBindingsForDatabase(bindings, dependency, databaseName)
	secretRef := ""
	if len(dbBindings) > 0 {
		secretRef = dbBindings[0].SecretRef
	}
	dbMeta := meta("managed-database:"+databaseName, ir.ResourceKindManagedDatabase, resourceName(env, databaseName, "db"), packageTags(serviceName, env, dependency), sourcePath)
	annotatePackageMeta(&dbMeta, "", dependency.Provenance, dependency.Dependency.Name)
	graph.Resources.ManagedDatabases = append(graph.Resources.ManagedDatabases, ir.ManagedDatabase{
		Meta:                dbMeta,
		Engine:              normalizeDatabaseEngine(dbSpec.Engine),
		Version:             dbSpec.Version,
		Size:                dbSpec.Size,
		Port:                dbPort,
		Region:              dbSpec.Region,
		Storage:             compileDatabaseStorage(dbSpec.Storage),
		Backups:             compileDatabaseBackups(dbSpec.Backups),
		Network:             compileDatabaseNetwork(dbSpec.Network),
		SecurityGroupRefs:   []string{"security-group:" + databaseName},
		ConnectionSecretRef: secretRef,
	})
	for _, binding := range dbBindings {
		secretMeta := meta("database-secret:"+databaseName+":"+envSecretName(binding.EnvName), ir.ResourceKindDatabaseSecret, resourceName(env, databaseName, envSecretName(binding.EnvName)+"-secret"), packageTags(serviceName, env, dependency), "$.stack.bindings")
		annotatePackageMeta(&secretMeta, "", dependency.Provenance, dependency.Dependency.Name)
		graph.Resources.DatabaseSecrets = append(graph.Resources.DatabaseSecrets, ir.DatabaseSecret{
			Meta:        secretMeta,
			DatabaseRef: "managed-database:" + databaseName,
			Name:        envSecretName(binding.EnvName),
			Ref:         binding.SecretRef,
			EnvName:     binding.EnvName,
		})
		bindingMeta := meta("database-binding:"+serviceName+":"+databaseName+":"+binding.EnvName, ir.ResourceKindDatabaseBinding, resourceName(env, serviceName, strings.ToLower(binding.EnvName)+"-binding"), packageTags(serviceName, env, dependency), "$.stack.bindings")
		annotatePackageMeta(&bindingMeta, "", dependency.Provenance, dependency.Dependency.Name)
		graph.Resources.DatabaseBindings = append(graph.Resources.DatabaseBindings, ir.DatabaseBinding{
			Meta:        bindingMeta,
			FromService: serviceName,
			DatabaseRef: "managed-database:" + databaseName,
			EnvName:     binding.EnvName,
			SecretRef:   binding.SecretRef,
		})
	}
}

func packageBindingsForDatabase(bindings []spec.StackBinding, dependency compiledPackageDependency, databaseName string) []compiledBinding {
	var out []compiledBinding
	for _, binding := range bindings {
		if binding.To != dependency.Dependency.Name {
			continue
		}
		out = append(out, compiledBinding{
			EnvName:   binding.As,
			SecretRef: databaseConnectionSecretRef(databaseName),
		})
	}
	return out
}

func packageManagedDatabaseSpec(dependency compiledPackageDependency) spec.ManagedDatabase {
	cfg := dependency.Config
	db := spec.ManagedDatabase{}
	if cfg.Managed != nil {
		db = *cfg.Managed
	}
	if db.Engine == "" {
		db.Engine = cfg.Engine
	}
	if db.Engine == "" {
		db.Engine = inferManagedDatabaseEngine(dependency)
	}
	if db.Version == "" {
		db.Version = firstNonEmpty(cfg.Version, defaultDatabaseVersion(db.Engine))
	}
	if db.Size == "" {
		db.Size = cfg.Size
	}
	if db.Region == "" {
		db.Region = cfg.Region
	}
	if db.Storage.SizeGB == 0 {
		db.Storage = cfg.Storage
	}
	if db.Backups.RetentionDays == 0 && !db.Backups.Enabled {
		db.Backups = cfg.Backups
	}
	if !db.Network.Private && db.Network.SubnetGroupRef == "" && len(db.Network.SecurityGroupRefs) == 0 {
		db.Network = cfg.Network
	}
	doc := spec.Document{Kind: spec.KindManagedDatabase, ManagedDatabase: &db}
	spec.ApplyDefaults(&doc)
	return db
}

func inferManagedDatabaseEngine(dependency compiledPackageDependency) string {
	haystack := strings.ToLower(strings.Join([]string{dependency.Dependency.Name, dependency.Dependency.Uses, dependency.Manifest.Name, strings.Join(dependency.Manifest.Exports.Dependencies, " ")}, " "))
	switch {
	case strings.Contains(haystack, "mysql"), strings.Contains(haystack, "mariadb"):
		return "mysql"
	default:
		return "postgres"
	}
}

func defaultDatabaseVersion(engine string) string {
	switch normalizeDatabaseEngine(engine) {
	case "mysql", "aurora-mysql":
		return "8.0"
	default:
		return "16"
	}
}

func packageDatabaseSecurityGroup(serviceName, env, databaseName string, port int, dependency compiledPackageDependency, sourcePath string) ir.SecurityGroup {
	sg := ir.SecurityGroup{
		Meta: meta("security-group:"+databaseName, ir.ResourceKindSecurityGroup, resourceName(env, databaseName, "db-sg"), packageTags(serviceName, env, dependency), sourcePath, "$.stack.bindings"),
		Rules: []ir.SecurityRule{
			{
				Direction:   "ingress",
				Protocol:    "tcp",
				FromPort:    port,
				ToPort:      port,
				Source:      "security-group:" + serviceName,
				Description: "allow bound service traffic to package managed database",
			},
			{
				Direction:   "egress",
				Protocol:    "all",
				Destination: "0.0.0.0/0",
				Description: "allow package managed database control-plane egress",
			},
		},
	}
	annotatePackageMeta(&sg.Meta, "", dependency.Provenance, dependency.Dependency.Name)
	return sg
}

func appendPackageStatefulGroup(graph *ir.Graph, dependency compiledPackageDependency, serviceName, env, sourcePath string) {
	groupSpec := packageStatefulGroupSpec(dependency)
	doc := spec.Document{
		APIVersion: spec.APIVersion,
		Kind:       spec.KindStatefulGroup,
		Metadata: spec.Metadata{
			Name:   dependency.ResourceName,
			Env:    env,
			Labels: map[string]string{"stack_service": serviceName},
		},
		StatefulGroup: &groupSpec,
	}
	spec.ApplyDefaults(&doc)
	dependencyGraph := compileStatefulGroup(doc, Options{})
	forEachMeta(&dependencyGraph.Resources, func(meta *ir.ResourceMeta) {
		if meta.Tags == nil {
			meta.Tags = map[string]string{}
		}
		meta.Tags[ir.TagService] = serviceName
		meta.Tags[ir.TagGraph] = "service/" + env + "/" + serviceName
		annotatePackageMeta(meta, sourcePath, dependency.Provenance, dependency.Dependency.Name)
	})
	graph.Resources.StatefulGroups = append(graph.Resources.StatefulGroups, dependencyGraph.Resources.StatefulGroups...)
	graph.Resources.StatefulMembers = append(graph.Resources.StatefulMembers, dependencyGraph.Resources.StatefulMembers...)
	graph.Resources.StatefulVolumes = append(graph.Resources.StatefulVolumes, dependencyGraph.Resources.StatefulVolumes...)
	graph.Resources.StatefulDNS = append(graph.Resources.StatefulDNS, dependencyGraph.Resources.StatefulDNS...)
	graph.Resources.StatefulRecipes = append(graph.Resources.StatefulRecipes, dependencyGraph.Resources.StatefulRecipes...)
	graph.Resources.SnapshotPolicies = append(graph.Resources.SnapshotPolicies, dependencyGraph.Resources.SnapshotPolicies...)
	graph.Resources.UpdatePolicies = append(graph.Resources.UpdatePolicies, dependencyGraph.Resources.UpdatePolicies...)
}

func packageStatefulGroupSpec(dependency compiledPackageDependency) spec.StatefulGroup {
	cfg := dependency.Config
	group := spec.StatefulGroup{}
	if cfg.Stateful != nil {
		group = *cfg.Stateful
	}
	if group.Replicas == 0 {
		group.Replicas = firstNonZero(cfg.Replicas, 1)
	}
	if group.Volume.Size == "" {
		group.Volume = cfg.Volume
	}
	if group.Volume.Size == "" {
		group.Volume.Size = "20Gi"
	}
	if group.Identity.HostnamePrefix == "" && cfg.Identity.HostnamePrefix != "" {
		group.Identity = cfg.Identity
	}
	if group.Recipe.Name == "" && group.Recipe.Ref == "" {
		group.Recipe = cfg.Recipe
	}
	if group.Recipe.Name == "" && group.Recipe.Ref == "" {
		group.Recipe.Name = dependency.Manifest.Name
	}
	if len(group.Recipe.Config) == 0 {
		group.Recipe.Config = cloneRaw(dependency.Dependency.Config)
	}
	if group.Update.Strategy == "" {
		group.Update = cfg.Update
	}
	if group.Update.Strategy == "" {
		group.Update.Strategy = "ordered"
	}
	return group
}

func appendPackageOperation(graph *ir.Graph, dependency compiledPackageDependency, serviceName, env, sourcePath string) {
	if len(dependency.Manifest.Exports.OperationProfiles) == 0 && len(dependency.Manifest.Exports.PackageSteps) == 0 {
		return
	}
	meta := meta("package-operation:"+dependency.ResourceName, ir.ResourceKindPackageOperation, resourceName(env, dependency.ResourceName, "package-ops"), packageTags(serviceName, env, dependency), sourcePath)
	annotatePackageMeta(&meta, "", dependency.Provenance, dependency.Dependency.Name)
	graph.Resources.PackageOperations = append(graph.Resources.PackageOperations, ir.PackageOperation{
		Meta:              meta,
		Dependency:        dependency.Dependency.Name,
		Package:           dependency.Provenance,
		OperationProfiles: cloneStrings(dependency.Manifest.Exports.OperationProfiles),
		PackageSteps:      cloneStrings(dependency.Manifest.Exports.PackageSteps),
		Config:            cloneRaw(dependency.Dependency.Config),
	})
}
