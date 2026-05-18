package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/spec"
)

func compileStatefulGroup(doc spec.Document, _ Options) *ir.Graph {
	group := doc.StatefulGroup
	service := doc.Metadata.Name
	env := doc.Metadata.Env
	recipeID := firstNonEmpty(group.Recipe.Name, group.Recipe.Ref)
	tags := statefulTags(service, env, recipeID)
	recipe := compileStatefulRecipe(service, env, tags, group.Recipe)
	snapshot := compileSnapshotPolicy(service, env, tags, group.Recipe.Config)
	update := compileStatefulUpdatePolicy(service, env, tags, group.Update)
	members := compileStatefulMembers(*group)

	graph := &ir.Graph{
		SchemaVersion: ir.SchemaVersion,
		Service:       service,
		Env:           env,
	}
	graph.Resources.StatefulRecipes = []ir.StatefulRecipe{recipe}
	graph.Resources.SnapshotPolicies = []ir.SnapshotPolicy{snapshot}
	graph.Resources.UpdatePolicies = []ir.UpdatePolicy{update}

	memberRefs := make([]string, 0, len(members))
	volumeRefs := make([]string, 0, len(members))
	dnsRefs := make([]string, 0, len(members))
	for _, member := range members {
		memberID := statefulMemberID(service, member.Ordinal)
		volumeID := statefulVolumeID(service, member.Ordinal)
		dnsID := statefulDNSID(service, member.Ordinal)
		memberTags := statefulMemberTags(tags, member.Ordinal)
		dnsName := firstNonEmpty(member.DNSName, generatedStatefulDNSName(group.Identity.HostnamePrefix, member.Ordinal))

		memberRefs = append(memberRefs, memberID)
		volumeRefs = append(volumeRefs, volumeID)
		dnsRefs = append(dnsRefs, dnsID)
		graph.Resources.StatefulMembers = append(graph.Resources.StatefulMembers, ir.StatefulMember{
			Meta:             meta(memberID, ir.ResourceKindStatefulMember, resourceName(env, service, fmt.Sprintf("member-%d", member.Ordinal)), memberTags, member.SourcePath, "$.stateful.recipe", "$.stateful.update"),
			Ordinal:          member.Ordinal,
			Zone:             member.Zone,
			DNSName:          dnsName,
			VolumeRef:        volumeID,
			DNSIdentityRef:   dnsID,
			RecipeRuntimeRef: recipe.Meta.LogicalID,
			UpdatePolicyRef:  update.Meta.LogicalID,
		})
		graph.Resources.StatefulVolumes = append(graph.Resources.StatefulVolumes, ir.StatefulVolume{
			Meta:          meta(volumeID, ir.ResourceKindStatefulVolume, resourceName(env, service, fmt.Sprintf("volume-%d", member.Ordinal)), memberTags, "$.stateful.volume", member.SourcePath),
			MemberOrdinal: member.Ordinal,
			Size:          group.Volume.Size,
			Type:          group.Volume.Type,
			MountPath:     group.Volume.MountPath,
			Encrypted:     group.Volume.Encrypted,
		})
		graph.Resources.StatefulDNS = append(graph.Resources.StatefulDNS, ir.StatefulDNS{
			Meta:           meta(dnsID, ir.ResourceKindStatefulDNS, resourceName(env, service, fmt.Sprintf("dns-%d", member.Ordinal)), memberTags, "$.stateful.identity", member.SourcePath),
			MemberOrdinal:  member.Ordinal,
			DNSZoneRef:     group.Identity.DNSZoneRef,
			HostnamePrefix: group.Identity.HostnamePrefix,
			DNSName:        dnsName,
		})
	}
	graph.Resources.StatefulGroups = []ir.StatefulGroup{
		{
			Meta:              meta("stateful-group:"+service, ir.ResourceKindStatefulGroup, resourceName(env, service, "stateful-group"), tags, "$.metadata", "$.stateful"),
			Replicas:          group.Replicas,
			MemberRefs:        memberRefs,
			VolumeRefs:        volumeRefs,
			DNSIdentityRefs:   dnsRefs,
			RecipeRuntimeRef:  recipe.Meta.LogicalID,
			SnapshotPolicyRef: snapshot.Meta.LogicalID,
			UpdatePolicyRef:   update.Meta.LogicalID,
		},
	}
	return graph
}

type compiledStatefulMember struct {
	Ordinal    int
	Zone       string
	DNSName    string
	SourcePath string
}

func compileStatefulMembers(group spec.StatefulGroup) []compiledStatefulMember {
	if len(group.Members) == 0 {
		members := make([]compiledStatefulMember, 0, group.Replicas)
		for ordinal := 0; ordinal < group.Replicas; ordinal++ {
			members = append(members, compiledStatefulMember{
				Ordinal:    ordinal,
				SourcePath: "$.stateful.replicas",
			})
		}
		return members
	}
	members := make([]compiledStatefulMember, 0, len(group.Members))
	for i, member := range group.Members {
		members = append(members, compiledStatefulMember{
			Ordinal:    member.Ordinal,
			Zone:       member.Zone,
			DNSName:    member.DNSName,
			SourcePath: fmt.Sprintf("$.stateful.members[%d]", i),
		})
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].Ordinal < members[j].Ordinal
	})
	return members
}

type statefulRecipeConfig struct {
	Artifact  *spec.Artifact          `json:"artifact,omitempty"`
	Runtime   statefulRuntimeConfig   `json:"runtime,omitempty"`
	Snapshots statefulSnapshotsConfig `json:"snapshots,omitempty"`
}

type statefulRuntimeConfig struct {
	Command []string            `json:"command,omitempty"`
	Env     map[string]string   `json:"env,omitempty"`
	Ports   map[string]int      `json:"ports,omitempty"`
	Health  spec.Health         `json:"health,omitempty"`
	Metrics statefulMetricsSpec `json:"metrics,omitempty"`
}

type statefulMetricsSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Path    string `json:"path,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type statefulSnapshotsConfig struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Interval  string `json:"interval,omitempty"`
	Retention string `json:"retention,omitempty"`
}

func compileStatefulRecipe(service, env string, tags map[string]string, recipe spec.StatefulRecipe) ir.StatefulRecipe {
	cfg := decodeStatefulRecipeConfig(recipe.Config)
	compiled := ir.StatefulRecipe{
		Meta:   meta("stateful-recipe:"+service, ir.ResourceKindStatefulRecipe, resourceName(env, service, "recipe-runtime"), tags, "$.stateful.recipe"),
		Name:   recipe.Name,
		Ref:    recipe.Ref,
		Config: cloneRaw(recipe.Config),
	}
	if cfg.Artifact != nil {
		compiled.Artifact = compileArtifact(*cfg.Artifact)
	}
	compiled.Command = cloneStrings(cfg.Runtime.Command)
	compiled.Ports = cloneIntMap(cfg.Runtime.Ports)
	compiled.Env = cloneMap(cfg.Runtime.Env)
	compiled.HealthCheck = compileStatefulHealth(cfg.Runtime.Health, cfg.Runtime.Ports)
	metricsEnabled := cfg.Runtime.Metrics.Enabled || cfg.Runtime.Metrics.Path != "" || cfg.Runtime.Metrics.Port != 0
	if metricsEnabled {
		compiled.Metrics = ir.AppMetrics{
			Enabled: true,
			Path:    cfg.Runtime.Metrics.Path,
			Port:    firstNonZero(cfg.Runtime.Metrics.Port, cfg.Runtime.Health.Port, cfg.Runtime.Ports["monitoring"]),
		}
	}
	return compiled
}

func compileSnapshotPolicy(service, env string, tags map[string]string, raw json.RawMessage) ir.SnapshotPolicy {
	cfg := decodeStatefulRecipeConfig(raw)
	return ir.SnapshotPolicy{
		Meta:      meta("stateful-snapshot-policy:"+service, ir.ResourceKindSnapshotPolicy, resourceName(env, service, "snapshot-policy"), tags, "$.stateful.recipe.config.snapshots"),
		Enabled:   cfg.Snapshots.Enabled,
		Interval:  cfg.Snapshots.Interval,
		Retention: cfg.Snapshots.Retention,
	}
}

func compileStatefulUpdatePolicy(service, env string, tags map[string]string, update spec.StatefulUpdate) ir.UpdatePolicy {
	return ir.UpdatePolicy{
		Meta:     meta("stateful-update-policy:"+service, ir.ResourceKindUpdatePolicy, resourceName(env, service, "update-policy"), tags, "$.stateful.update"),
		Strategy: update.Strategy,
	}
}

func decodeStatefulRecipeConfig(raw json.RawMessage) statefulRecipeConfig {
	if len(raw) == 0 {
		return statefulRecipeConfig{}
	}
	var cfg statefulRecipeConfig
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

func compileStatefulHealth(health spec.Health, ports map[string]int) ir.HealthCheck {
	if health.Type == "" && health.Path != "" {
		health.Type = "http"
	}
	if health.Port == 0 {
		health.Port = firstNonZero(ports["monitoring"], ports["client"])
	}
	if health.Interval == "" {
		health.Interval = "10s"
	}
	if health.Timeout == "" {
		health.Timeout = "2s"
	}
	return compileHealth(health)
}

func statefulTags(service, env, recipe string) map[string]string {
	tags := ir.RequiredTags(service, env)
	tags[ir.TagStatefulGroup] = service
	if recipe != "" {
		tags[ir.TagStatefulRecipe] = recipe
	}
	return tags
}

func statefulMemberTags(tags map[string]string, ordinal int) map[string]string {
	out := cloneMap(tags)
	out[ir.TagMemberOrdinal] = strconv.Itoa(ordinal)
	return out
}

func statefulMemberID(service string, ordinal int) string {
	return fmt.Sprintf("stateful-member:%s:%d", service, ordinal)
}

func statefulVolumeID(service string, ordinal int) string {
	return fmt.Sprintf("stateful-volume:%s:%d", service, ordinal)
}

func statefulDNSID(service string, ordinal int) string {
	return fmt.Sprintf("stateful-dns:%s:%d", service, ordinal)
}

func generatedStatefulDNSName(prefix string, ordinal int) string {
	if prefix == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d", prefix, ordinal)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]int, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
