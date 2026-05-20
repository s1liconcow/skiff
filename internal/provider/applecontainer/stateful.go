package applecontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	stateruntime "github.com/s1liconcow/skiff/internal/stateful"
)

const (
	appleStatefulRuntimeKind          = "apple-stateful-runtime"
	appleStatefulOperationFence       = "apple-container-stop"
	appleStatefulOperationDetach      = "apple-volume-detach"
	appleStatefulOperationLaunch      = "apple-container-run"
	appleStatefulOperationAttach      = "apple-volume-attach"
	appleStatefulOperationDNS         = "apple-dns-alias"
	appleStatefulOperationSnapshot    = "apple-volume-snapshot"
	appleStatefulDefaultMountPath     = "/data"
	appleStatefulDefaultVolumeBytes   = int64(268435456)
	appleStatefulDefaultPortBase      = 20000
	appleStatefulContainerPortSpacing = 100
)

type appleStatefulRuntime struct {
	SchemaVersion string                       `json:"schema_version"`
	Provider      string                       `json:"provider"`
	Group         string                       `json:"group"`
	Env           string                       `json:"env"`
	Recipe        string                       `json:"recipe,omitempty"`
	Image         string                       `json:"image"`
	Command       []string                     `json:"command,omitempty"`
	EnvVars       map[string]string            `json:"env,omitempty"`
	Ports         map[string]int               `json:"ports,omitempty"`
	HealthPath    string                       `json:"health_path,omitempty"`
	HealthPort    int                          `json:"health_port,omitempty"`
	MountPath     string                       `json:"mount_path"`
	VolumeSize    string                       `json:"volume_size,omitempty"`
	PortBase      int                          `json:"port_base"`
	Members       []appleStatefulMemberRuntime `json:"members"`
	UpdatedAt     string                       `json:"updated_at"`
}

type appleStatefulMemberRuntime struct {
	Ordinal       int            `json:"ordinal"`
	ContainerName string         `json:"container_name"`
	VolumeName    string         `json:"volume_name"`
	DNSName       string         `json:"dns_name,omitempty"`
	HostPorts     map[string]int `json:"host_ports,omitempty"`
}

type appleStatefulMemberDesired struct {
	Ordinal       int            `json:"ordinal"`
	MemberOrdinal int            `json:"member_ordinal"`
	ContainerName string         `json:"container_name"`
	VolumeName    string         `json:"volume_name"`
	DNSName       string         `json:"dns_name,omitempty"`
	HostPorts     map[string]int `json:"host_ports,omitempty"`
	Ports         map[string]int `json:"ports,omitempty"`
}

type appleStatefulVolumeDesired struct {
	MemberOrdinal int    `json:"member_ordinal"`
	VolumeName    string `json:"volume_name"`
	Size          string `json:"size,omitempty"`
	MountPath     string `json:"mount_path"`
	Encrypted     bool   `json:"encrypted"`
}

type appleStatefulDNSDesired struct {
	MemberOrdinal int    `json:"member_ordinal"`
	DNSName       string `json:"dns_name"`
	ContainerName string `json:"container_name"`
}

func (p *Provider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	if graph != nil && len(graph.Resources.StatefulGroups) > 0 {
		return p.planStateful(ctx, graph)
	}
	return p.Provider.Plan(ctx, graph)
}

func (p *Provider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	if plan != nil && plan.Provider == Name && planContainsAppleStateful(plan) {
		runtime, err := appleStatefulRuntimeFromPlan(plan, p.clock())
		if err != nil {
			return nil, err
		}
		return p.applyStatefulRuntime(ctx, plan, runtime)
	}
	return p.Provider.Apply(ctx, plan)
}

func (p *Provider) ApplyStatefulGroup(ctx context.Context, graph *ir.Graph, plan *provider.Plan) (*provider.ApplyResult, error) {
	if graph == nil {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "stateful_apply", Summary: "graph is required"}
	}
	runtime, err := p.appleStatefulRuntimeFromGraph(graph)
	if err != nil {
		return nil, err
	}
	return p.applyStatefulRuntime(ctx, plan, runtime)
}

func (p *Provider) FenceInstance(ctx context.Context, req provider.FenceInstanceRequest) (*provider.FenceInstanceResult, error) {
	if strings.TrimSpace(req.InstanceID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "fence_instance", Summary: "instance_id is required"}
	}
	_, stopErr := p.container(ctx, "stop", "--time", "2", req.InstanceID)
	if stopErr != nil && !appleContainerNotFound(stopErr) {
		return nil, stopErr
	}
	_, deleteErr := p.container(ctx, "delete", "--force", req.InstanceID)
	if deleteErr != nil && !appleContainerNotFound(deleteErr) {
		return nil, deleteErr
	}
	now := p.now()
	return &provider.FenceInstanceResult{
		ProviderOperation: p.appleStatefulOperation(appleStatefulOperationFence, req.InstanceID, "stopped and deleted Apple stateful member container", now),
		FencedAt:          now,
	}, nil
}

func (p *Provider) DetachVolume(ctx context.Context, req provider.DetachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.VolumeID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "detach_volume", Summary: "volume_id is required"}
	}
	now := p.now()
	return &provider.VolumeAttachmentResult{
		ProviderOperation: p.appleStatefulOperation(appleStatefulOperationDetach, req.VolumeID, "Apple volume detached by container deletion", now),
		VolumeID:          req.VolumeID,
		InstanceID:        req.InstanceID,
		CompletedAt:       now,
	}, nil
}

func (p *Provider) LaunchReplacement(ctx context.Context, req provider.LaunchReplacementRequest) (*provider.ReplacementInstance, error) {
	if req.Generation <= 0 {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "launch_replacement", Summary: "generation must be positive"}
	}
	runtime, err := p.loadStatefulRuntime(ctx, req.Ref.Group)
	if err != nil {
		return nil, err
	}
	member, index, ok := runtime.member(req.Ref.Member)
	if !ok {
		return nil, &provider.Error{Code: provider.CodeNotFound, Provider: Name, Op: "launch_replacement", Resource: req.Ref.Group, Summary: fmt.Sprintf("member %d runtime is not present", req.Ref.Member)}
	}
	member.ContainerName = appleStatefulContainerName(firstNonEmpty(runtime.Env, req.Ref.Env), runtime.Group, req.Ref.Member, req.Generation)
	if req.VolumeID != "" {
		member.VolumeName = req.VolumeID
	}
	runtime.Members[index] = member
	if err := p.persistStatefulRuntime(ctx, runtime); err != nil {
		return nil, err
	}
	if err := p.startStatefulMember(ctx, runtime, member, req.Generation); err != nil {
		return nil, err
	}
	now := p.now()
	return &provider.ReplacementInstance{
		ProviderOperation: p.appleStatefulOperation(appleStatefulOperationLaunch, member.ContainerName, "launched Apple replacement stateful member container", now),
		InstanceID:        member.ContainerName,
		Zone:              req.Zone,
		LaunchedAt:        now,
	}, nil
}

func (p *Provider) AttachVolume(ctx context.Context, req provider.AttachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.VolumeID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "attach_volume", Summary: "instance_id and volume_id are required"}
	}
	now := p.now()
	return &provider.VolumeAttachmentResult{
		ProviderOperation: p.appleStatefulOperation(appleStatefulOperationAttach, req.VolumeID+":"+req.InstanceID, "Apple volume attached at container launch", now),
		VolumeID:          req.VolumeID,
		InstanceID:        req.InstanceID,
		CompletedAt:       now,
	}, nil
}

func (p *Provider) UpdateMemberDNS(ctx context.Context, req provider.UpdateMemberDNSRequest) (*provider.DNSUpdateResult, error) {
	if strings.TrimSpace(req.DNSName) == "" || strings.TrimSpace(req.InstanceID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "update_member_dns", Summary: "dns_name and instance_id are required"}
	}
	now := p.now()
	return &provider.DNSUpdateResult{
		ProviderOperation: p.appleStatefulOperation(appleStatefulOperationDNS, req.DNSName, "recorded Apple stateful DNS identity alias", now),
		DNSName:           req.DNSName,
		UpdatedAt:         now,
	}, nil
}

func (p *Provider) SnapshotVolume(ctx context.Context, req provider.SnapshotVolumeRequest) (*provider.VolumeSnapshot, error) {
	if strings.TrimSpace(req.VolumeID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "snapshot_volume", Summary: "volume_id is required"}
	}
	now := p.now()
	snapshotID := "apple-snapshot-" + applePathSafe(req.VolumeID)
	return &provider.VolumeSnapshot{
		ProviderOperation: p.appleStatefulOperation(appleStatefulOperationSnapshot, snapshotID, "recorded Apple stateful volume snapshot marker", now),
		SnapshotID:        snapshotID,
		VolumeID:          req.VolumeID,
		CreatedAt:         now,
	}, nil
}

func (p *Provider) Stop(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	if strings.TrimSpace(req.InstanceID) == "" {
		return nil, errors.New("stateful recipe stop requires instance ID")
	}
	if _, err := p.FenceInstance(ctx, provider.FenceInstanceRequest{Ref: provider.StatefulMemberRef{Group: req.Group, Env: req.Env, Member: req.Member}, InstanceID: req.InstanceID, Reason: "ordered update"}); err != nil {
		return nil, err
	}
	return &stateruntime.RecipeResult{OK: true, Summary: "stopped Apple stateful member container"}, nil
}

func (p *Provider) MatchesRecipe(name string) bool {
	return strings.TrimSpace(name) != ""
}

func (p *Provider) Start(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	runtime, err := p.loadStatefulRuntime(ctx, req.Group)
	if err != nil {
		return nil, err
	}
	member, index, ok := runtime.member(req.Member)
	if !ok {
		return nil, fmt.Errorf("member %d runtime is not present", req.Member)
	}
	if req.InstanceID != "" {
		member.ContainerName = req.InstanceID
	}
	if req.VolumeID != "" {
		member.VolumeName = req.VolumeID
	}
	runtime.Members[index] = member
	if err := p.persistStatefulRuntime(ctx, runtime); err != nil {
		return nil, err
	}
	if err := p.startStatefulMember(ctx, runtime, member, firstPositive(req.Generation, 1)); err != nil {
		return nil, err
	}
	return &stateruntime.RecipeResult{OK: true, Summary: "started Apple stateful member container"}, nil
}

func (p *Provider) Health(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	runtime, err := p.loadStatefulRuntime(ctx, req.Group)
	if err != nil {
		return nil, err
	}
	member, _, ok := runtime.member(req.Member)
	if !ok {
		return nil, fmt.Errorf("member %d runtime is not present", req.Member)
	}
	port := runtime.HealthPort
	if port == 0 {
		port = firstPort(runtime.Ports)
	}
	hostPort := member.HostPorts[portNameForContainerPort(runtime.Ports, port)]
	if hostPort == 0 {
		hostPort = firstHostPort(member.HostPorts)
	}
	if hostPort == 0 {
		return &stateruntime.RecipeResult{OK: true, Summary: "Apple stateful member has no health port"}, nil
	}
	path := firstNonEmpty(runtime.HealthPath, "/")
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, path), nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(reqHTTP)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return &stateruntime.RecipeResult{OK: true, Summary: "Apple stateful member health check passed"}, nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("Apple stateful member health check failed: %w", lastErr)
}

func (p *Provider) Backup(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	if _, err := p.SnapshotVolume(ctx, provider.SnapshotVolumeRequest{Ref: provider.StatefulMemberRef{Group: req.Group, Env: req.Env, Member: req.Member}, VolumeID: req.VolumeID, Reason: "stateful recipe backup"}); err != nil {
		return nil, err
	}
	return &stateruntime.RecipeResult{OK: true, Summary: "recorded Apple stateful volume snapshot marker"}, nil
}

func (p *Provider) Restore(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &stateruntime.RecipeResult{OK: true, Summary: "Apple stateful member uses the existing durable volume"}, nil
}

func (p *Provider) DetectRole(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RoleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &stateruntime.RoleResult{Role: "member", Primary: req.Member == 0, Facts: map[string]string{"provider": Name}}, nil
}

func (p *Provider) planStateful(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime, err := p.appleStatefulRuntimeFromGraph(graph)
	if err != nil {
		return nil, err
	}
	changes := make([]provider.PlannedChange, 0)
	add := func(action, kind, logicalID, name, providerID, summary string, tags map[string]string, desired any) error {
		body, fingerprint, err := appleDesiredBody(desired)
		if err != nil {
			return err
		}
		changes = append(changes, provider.PlannedChange{
			Action:      action,
			Kind:        kind,
			LogicalID:   logicalID,
			Name:        name,
			ProviderID:  providerID,
			Tags:        tags,
			Summary:     summary,
			Fingerprint: fingerprint,
			Desired:     body,
		})
		return nil
	}
	baseTags := ir.RequiredTags(graph.Service, graph.Env)
	for _, group := range graph.Resources.StatefulGroups {
		tags := appleStatefulTags(baseTags, graph.Service, -1, runtime.Recipe)
		if err := add(provider.ActionCreate, ir.ResourceKindStatefulGroup, group.Meta.LogicalID, firstNonEmpty(group.Meta.Name, graph.Service), runtime.Group, fmt.Sprintf("Apple StatefulGroup with %d local member containers", group.Replicas), tags, runtime); err != nil {
			return nil, err
		}
	}
	for _, member := range runtime.Members {
		tags := appleStatefulTags(baseTags, graph.Service, member.Ordinal, runtime.Recipe)
		desired := appleStatefulMemberDesired{
			Ordinal:       member.Ordinal,
			MemberOrdinal: member.Ordinal,
			ContainerName: member.ContainerName,
			VolumeName:    member.VolumeName,
			DNSName:       member.DNSName,
			HostPorts:     cloneIntMap(member.HostPorts),
			Ports:         cloneIntMap(runtime.Ports),
		}
		logicalID := statefulMemberLogicalID(graph, member.Ordinal)
		if err := add(provider.ActionCreate, ir.ResourceKindStatefulMember, logicalID, member.ContainerName, member.ContainerName, fmt.Sprintf("Apple container for StatefulGroup member %d", member.Ordinal), tags, desired); err != nil {
			return nil, err
		}
		desiredVolume := appleStatefulVolumeDesired{
			MemberOrdinal: member.Ordinal,
			VolumeName:    member.VolumeName,
			Size:          runtime.VolumeSize,
			MountPath:     runtime.MountPath,
			Encrypted:     true,
		}
		if volume := statefulVolumeForOrdinal(graph, member.Ordinal); volume != nil {
			desiredVolume.Size = volume.Size
			desiredVolume.Encrypted = volume.Encrypted
		}
		if err := add(provider.ActionCreate, ir.ResourceKindStatefulVolume, statefulVolumeLogicalID(graph, member.Ordinal), member.VolumeName, member.VolumeName, fmt.Sprintf("Apple persistent volume for StatefulGroup member %d mounted at %s", member.Ordinal, runtime.MountPath), tags, desiredVolume); err != nil {
			return nil, err
		}
		if member.DNSName != "" {
			desiredDNS := appleStatefulDNSDesired{MemberOrdinal: member.Ordinal, DNSName: member.DNSName, ContainerName: member.ContainerName}
			if err := add(provider.ActionCreate, ir.ResourceKindStatefulDNS, statefulDNSLogicalID(graph, member.Ordinal), member.DNSName, member.DNSName, fmt.Sprintf("Apple local DNS identity marker for member %d", member.Ordinal), tags, desiredDNS); err != nil {
				return nil, err
			}
		}
	}
	for _, recipe := range graph.Resources.StatefulRecipes {
		tags := appleStatefulTags(baseTags, graph.Service, -1, runtime.Recipe)
		if err := add(provider.ActionCreate, ir.ResourceKindStatefulRecipe, recipe.Meta.LogicalID, firstNonEmpty(recipe.Meta.Name, recipe.Name, runtime.Recipe), runtime.Recipe, "Apple StatefulGroup recipe runtime", tags, recipe); err != nil {
			return nil, err
		}
	}
	for _, policy := range graph.Resources.UpdatePolicies {
		tags := appleStatefulTags(baseTags, graph.Service, -1, runtime.Recipe)
		if err := add(provider.ActionCreate, ir.ResourceKindUpdatePolicy, policy.Meta.LogicalID, firstNonEmpty(policy.Meta.Name, "ordered"), policy.Meta.LogicalID, "Apple StatefulGroup ordered update policy", tags, policy); err != nil {
			return nil, err
		}
	}
	for _, policy := range graph.Resources.SnapshotPolicies {
		tags := appleStatefulTags(baseTags, graph.Service, -1, runtime.Recipe)
		if err := add(provider.ActionCreate, ir.ResourceKindSnapshotPolicy, policy.Meta.LogicalID, firstNonEmpty(policy.Meta.Name, "snapshot"), policy.Meta.LogicalID, "Apple StatefulGroup snapshot marker policy", tags, policy); err != nil {
			return nil, err
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind == changes[j].Kind {
			return changes[i].LogicalID < changes[j].LogicalID
		}
		return changes[i].Kind < changes[j].Kind
	})
	return &provider.Plan{Provider: Name, Service: graph.Service, Env: graph.Env, Resources: changes}, nil
}

func (p *Provider) applyStatefulRuntime(ctx context.Context, plan *provider.Plan, runtime appleStatefulRuntime) (*provider.ApplyResult, error) {
	if strings.TrimSpace(runtime.Image) == "" {
		return nil, &provider.Error{Code: provider.CodeInvalidConfig, Provider: Name, Op: "stateful_apply", Summary: "StatefulGroup Apple apply requires an OCI image in the recipe artifact"}
	}
	if err := p.persistStatefulRuntime(ctx, runtime); err != nil {
		return nil, err
	}
	now := p.now()
	result := &provider.ApplyResult{Provider: Name, Service: runtime.Group, Env: runtime.Env, AppliedAt: now}
	if plan != nil {
		result.Service = plan.Service
		result.Env = plan.Env
	}
	for _, member := range runtime.Members {
		sizeBytes := appleVolumeSizeBytes(runtime.VolumeSize)
		if _, err := p.container(ctx, "volume", "create", "--opt", fmt.Sprintf("size=%d", sizeBytes), member.VolumeName); err != nil && !appleAlreadyExists(err) {
			return nil, err
		}
		volumeInspection := provider.ResourceInspection{
			Kind:       ir.ResourceKindStatefulVolume,
			LogicalID:  statefulVolumeLogicalIDFromRuntime(runtime, member.Ordinal),
			Name:       member.VolumeName,
			ProviderID: member.VolumeName,
			Status:     "configured",
			Tags:       appleStatefulTags(ir.RequiredTags(runtime.Group, runtime.Env), runtime.Group, member.Ordinal, runtime.Recipe),
		}
		if err := p.recordAppleResource(ctx, result.Service, result.Env, volumeInspection, now); err != nil {
			return nil, err
		}
		if err := p.startStatefulMember(ctx, runtime, member, 1); err != nil {
			return nil, err
		}
		memberInspection := provider.ResourceInspection{
			Kind:       ir.ResourceKindStatefulMember,
			LogicalID:  statefulMemberLogicalIDFromRuntime(runtime, member.Ordinal),
			Name:       member.ContainerName,
			ProviderID: member.ContainerName,
			Status:     "running",
			Tags:       appleStatefulTags(ir.RequiredTags(runtime.Group, runtime.Env), runtime.Group, member.Ordinal, runtime.Recipe),
		}
		if err := p.recordAppleResource(ctx, result.Service, result.Env, memberInspection, now); err != nil {
			return nil, err
		}
		result.ResourceIDs = append(result.ResourceIDs, member.VolumeName, member.ContainerName)
		result.Resources = append(result.Resources, volumeInspection, memberInspection)
	}
	sort.Strings(result.ResourceIDs)
	sort.Slice(result.Resources, func(i, j int) bool {
		if result.Resources[i].Kind == result.Resources[j].Kind {
			return result.Resources[i].LogicalID < result.Resources[j].LogicalID
		}
		return result.Resources[i].Kind < result.Resources[j].Kind
	})
	return result, nil
}

func (p *Provider) startStatefulMember(ctx context.Context, runtime appleStatefulRuntime, member appleStatefulMemberRuntime, generation int64) error {
	_, stopErr := p.container(ctx, "stop", "--time", "2", member.ContainerName)
	if stopErr != nil && !appleContainerNotFound(stopErr) {
		return stopErr
	}
	_, deleteErr := p.container(ctx, "delete", "--force", member.ContainerName)
	if deleteErr != nil && !appleContainerNotFound(deleteErr) {
		return deleteErr
	}
	args := []string{"run", "--name", member.ContainerName, "--detach", "--user", "0"}
	command := append([]string(nil), runtime.Command...)
	if len(command) > 0 {
		args = append(args, "--entrypoint", command[0])
	}
	for _, name := range sortedIntKeys(member.HostPorts) {
		containerPort := runtime.Ports[name]
		hostPort := member.HostPorts[name]
		if containerPort > 0 && hostPort > 0 {
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, containerPort))
		}
	}
	for _, item := range appleStatefulEnv(runtime, member, generation) {
		args = append(args, "-e", item)
	}
	args = append(args, "-v", member.VolumeName+":"+firstNonEmpty(runtime.MountPath, appleStatefulDefaultMountPath))
	args = append(args, runtime.Image)
	if len(command) > 1 {
		args = append(args, command[1:]...)
	}
	_, err := p.container(ctx, args...)
	return err
}

func (p *Provider) appleStatefulRuntimeFromGraph(graph *ir.Graph) (appleStatefulRuntime, error) {
	if graph == nil || len(graph.Resources.StatefulGroups) == 0 {
		return appleStatefulRuntime{}, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "stateful_plan", Summary: "StatefulGroup graph is required"}
	}
	group := graph.Resources.StatefulGroups[0]
	recipe := firstStatefulRecipe(graph)
	volume := firstStatefulVolume(graph)
	image := strings.TrimPrefix(strings.TrimSpace(recipe.Artifact.Ref), "oci://")
	image = firstNonEmpty(os.Getenv("SKIFF_APPLE_STATEFUL_IMAGE"), image)
	mountPath := firstNonEmpty(os.Getenv("SKIFF_APPLE_STATEFUL_MOUNT_PATH"), volume.MountPath, appleStatefulDefaultMountPath)
	ports := cloneIntMap(recipe.Ports)
	if len(ports) == 0 && recipe.HealthCheck.Port > 0 {
		ports = map[string]int{"health": recipe.HealthCheck.Port}
	}
	portBase := envInt("SKIFF_APPLE_STATEFUL_PORT_BASE", appleStatefulDefaultPortBase)
	members := append([]ir.StatefulMember(nil), graph.Resources.StatefulMembers...)
	sort.Slice(members, func(i, j int) bool { return members[i].Ordinal < members[j].Ordinal })
	runtimeMembers := make([]appleStatefulMemberRuntime, 0, len(members))
	for _, member := range members {
		runtimeMembers = append(runtimeMembers, appleStatefulMemberRuntime{
			Ordinal:       member.Ordinal,
			ContainerName: appleStatefulContainerName(graph.Env, graph.Service, member.Ordinal, 1),
			VolumeName:    appleStatefulVolumeName(graph.Env, graph.Service, member.Ordinal),
			DNSName:       member.DNSName,
			HostPorts:     appleStatefulHostPorts(portBase, member.Ordinal, ports),
		})
	}
	if len(runtimeMembers) == 0 {
		for i := 0; i < group.Replicas; i++ {
			runtimeMembers = append(runtimeMembers, appleStatefulMemberRuntime{
				Ordinal:       i,
				ContainerName: appleStatefulContainerName(graph.Env, graph.Service, i, 1),
				VolumeName:    appleStatefulVolumeName(graph.Env, graph.Service, i),
				HostPorts:     appleStatefulHostPorts(portBase, i, ports),
			})
		}
	}
	return appleStatefulRuntime{
		SchemaVersion: schema.Version,
		Provider:      Name,
		Group:         graph.Service,
		Env:           graph.Env,
		Recipe:        firstNonEmpty(recipe.Name, recipe.Meta.Name, recipe.Ref),
		Image:         image,
		Command:       append([]string(nil), recipe.Command...),
		EnvVars:       cloneStringMap(recipe.Env),
		Ports:         ports,
		HealthPath:    recipe.HealthCheck.Path,
		HealthPort:    recipe.HealthCheck.Port,
		MountPath:     mountPath,
		VolumeSize:    volume.Size,
		PortBase:      portBase,
		Members:       runtimeMembers,
		UpdatedAt:     canonical.Time(p.now()),
	}, nil
}

func appleStatefulRuntimeFromPlan(plan *provider.Plan, now time.Time) (appleStatefulRuntime, error) {
	var runtime appleStatefulRuntime
	for _, change := range plan.Resources {
		if change.Kind == ir.ResourceKindStatefulGroup && len(change.Desired) > 0 {
			if err := canonical.UnmarshalStrict(change.Desired, &runtime); err != nil {
				return appleStatefulRuntime{}, err
			}
			if runtime.UpdatedAt == "" {
				runtime.UpdatedAt = canonical.Time(now)
			}
			return runtime, nil
		}
	}
	return appleStatefulRuntime{}, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: "Apple StatefulGroup plan is missing runtime desired state"}
}

func (p *Provider) loadStatefulRuntime(ctx context.Context, group string) (appleStatefulRuntime, error) {
	if p.store == nil {
		return appleStatefulRuntime{}, &provider.Error{Code: provider.CodeInvalidConfig, Provider: Name, Op: "stateful_runtime", Summary: "object store is required for Apple StatefulGroup runtime"}
	}
	key, err := paths.StatefulProviderRuntime(group, Name)
	if err != nil {
		return appleStatefulRuntime{}, err
	}
	obj, err := p.store.Get(ctx, key)
	if err != nil {
		return appleStatefulRuntime{}, err
	}
	var runtime appleStatefulRuntime
	if err := canonical.UnmarshalStrict(obj.Body, &runtime); err != nil {
		return appleStatefulRuntime{}, err
	}
	return runtime, nil
}

func (p *Provider) persistStatefulRuntime(ctx context.Context, runtime appleStatefulRuntime) error {
	if p.store == nil {
		return nil
	}
	runtime.SchemaVersion = firstNonEmpty(runtime.SchemaVersion, schema.Version)
	runtime.Provider = Name
	runtime.UpdatedAt = canonical.Time(p.now())
	key, err := paths.StatefulProviderRuntime(runtime.Group, Name)
	if err != nil {
		return err
	}
	body, err := canonical.Marshal(runtime)
	if err != nil {
		return err
	}
	opts := objstore.PutOptions{ContentType: canonical.ContentType, Metadata: map[string]string{"schema_version": runtime.SchemaVersion, "provider": Name, "stateful_group": runtime.Group}}
	return upsertAppleObject(ctx, p.store, key, body, opts)
}

func (p *Provider) recordAppleResource(ctx context.Context, service, env string, resource provider.ResourceInspection, observedAt time.Time) error {
	if p.store == nil {
		return nil
	}
	record := schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: resource.Kind, Name: firstNonEmpty(resource.LogicalID, resource.Name)},
		Provider:      schema.ResourceProviderRef{Provider: Name, Kind: resource.Kind, ID: resource.ProviderID},
		Service:       service,
		Env:           env,
		Tags:          cloneStringMap(resource.Tags),
		ObservedAt:    canonical.Time(observedAt),
	}
	body, err := canonical.Marshal(record)
	if err != nil {
		return err
	}
	opts := objstore.PutOptions{ContentType: canonical.ContentType, Metadata: map[string]string{"schema_version": record.SchemaVersion, "provider": Name, "provider_kind": resource.Kind, "provider_id": resource.ProviderID}}
	pathKind := applePathSafe(resource.Kind)
	logicalKey, err := paths.LogicalResource(pathKind, applePathSafe(record.Logical.Name))
	if err != nil {
		return err
	}
	if err := upsertAppleObject(ctx, p.store, logicalKey, body, opts); err != nil {
		return err
	}
	providerKey, err := paths.ProviderResource(Name, pathKind, resource.ProviderID)
	if err != nil {
		return err
	}
	return upsertAppleObject(ctx, p.store, providerKey, body, opts)
}

func upsertAppleObject(ctx context.Context, store objstore.ObjectStore, key string, body []byte, opts objstore.PutOptions) error {
	for attempt := 0; attempt < 5; attempt++ {
		_, err := store.Create(ctx, key, body, opts)
		if err == nil {
			return nil
		}
		if !errors.Is(err, objstore.ErrAlreadyExists) {
			return err
		}
		current, err := store.Get(ctx, key)
		if err != nil {
			return err
		}
		_, err = store.CompareAndSwap(ctx, key, current.ETag, body, opts)
		if err == nil {
			return nil
		}
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			continue
		}
		return err
	}
	return objstore.WrapError("compare-and-swap", key, objstore.ErrPreconditionFailed)
}

func (runtime appleStatefulRuntime) member(ordinal int) (appleStatefulMemberRuntime, int, bool) {
	for i, member := range runtime.Members {
		if member.Ordinal == ordinal {
			return member, i, true
		}
	}
	return appleStatefulMemberRuntime{}, -1, false
}

func firstStatefulRecipe(graph *ir.Graph) ir.StatefulRecipe {
	if graph != nil && len(graph.Resources.StatefulRecipes) > 0 {
		return graph.Resources.StatefulRecipes[0]
	}
	return ir.StatefulRecipe{}
}

func firstStatefulVolume(graph *ir.Graph) ir.StatefulVolume {
	if graph != nil && len(graph.Resources.StatefulVolumes) > 0 {
		return graph.Resources.StatefulVolumes[0]
	}
	return ir.StatefulVolume{MountPath: appleStatefulDefaultMountPath}
}

func statefulVolumeForOrdinal(graph *ir.Graph, ordinal int) *ir.StatefulVolume {
	for i := range graph.Resources.StatefulVolumes {
		if graph.Resources.StatefulVolumes[i].MemberOrdinal == ordinal {
			return &graph.Resources.StatefulVolumes[i]
		}
	}
	return nil
}

func appleStatefulContainerName(env, group string, ordinal int, generation int64) string {
	return fmt.Sprintf("skiff-%s-%s-m%d-g%d", applePathSafe(env), applePathSafe(group), ordinal, generation)
}

func appleStatefulVolumeName(env, group string, ordinal int) string {
	return fmt.Sprintf("skiff-%s-%s-m%d-data", applePathSafe(env), applePathSafe(group), ordinal)
}

func statefulMemberLogicalID(graph *ir.Graph, ordinal int) string {
	for _, member := range graph.Resources.StatefulMembers {
		if member.Ordinal == ordinal {
			return firstNonEmpty(member.Meta.LogicalID, fmt.Sprintf("stateful-member:%s:%d", graph.Service, ordinal))
		}
	}
	return fmt.Sprintf("stateful-member:%s:%d", graph.Service, ordinal)
}

func statefulVolumeLogicalID(graph *ir.Graph, ordinal int) string {
	for _, volume := range graph.Resources.StatefulVolumes {
		if volume.MemberOrdinal == ordinal {
			return firstNonEmpty(volume.Meta.LogicalID, fmt.Sprintf("stateful-volume:%s:%d", graph.Service, ordinal))
		}
	}
	return fmt.Sprintf("stateful-volume:%s:%d", graph.Service, ordinal)
}

func statefulDNSLogicalID(graph *ir.Graph, ordinal int) string {
	for _, dns := range graph.Resources.StatefulDNS {
		if dns.MemberOrdinal == ordinal {
			return firstNonEmpty(dns.Meta.LogicalID, fmt.Sprintf("stateful-dns:%s:%d", graph.Service, ordinal))
		}
	}
	return fmt.Sprintf("stateful-dns:%s:%d", graph.Service, ordinal)
}

func statefulMemberLogicalIDFromRuntime(runtime appleStatefulRuntime, ordinal int) string {
	return fmt.Sprintf("stateful-member:%s:%d", runtime.Group, ordinal)
}

func statefulVolumeLogicalIDFromRuntime(runtime appleStatefulRuntime, ordinal int) string {
	return fmt.Sprintf("stateful-volume:%s:%d", runtime.Group, ordinal)
}

func appleStatefulHostPorts(base, ordinal int, ports map[string]int) map[string]int {
	out := make(map[string]int, len(ports))
	names := sortedIntKeys(ports)
	for i, name := range names {
		out[name] = base + ordinal*appleStatefulContainerPortSpacing + i
	}
	return out
}

func appleStatefulTags(base map[string]string, group string, member int, recipe string) map[string]string {
	tags := cloneStringMap(base)
	tags[ir.TagStatefulGroup] = group
	if member >= 0 {
		tags[ir.TagMemberOrdinal] = strconv.Itoa(member)
	}
	if recipe != "" {
		tags[ir.TagStatefulRecipe] = recipe
	}
	return tags
}

func appleStatefulEnv(runtime appleStatefulRuntime, member appleStatefulMemberRuntime, generation int64) []string {
	env := cloneStringMap(runtime.EnvVars)
	if env == nil {
		env = map[string]string{}
	}
	env["SKIFF_STATEFUL_GROUP"] = runtime.Group
	env["SKIFF_STATEFUL_ENV"] = runtime.Env
	env["SKIFF_STATEFUL_MEMBER"] = strconv.Itoa(member.Ordinal)
	env["SKIFF_STATEFUL_ORDINAL"] = strconv.Itoa(member.Ordinal)
	env["SKIFF_STATEFUL_GENERATION"] = strconv.FormatInt(generation, 10)
	env["SKIFF_STATEFUL_DNS_NAME"] = member.DNSName
	env["SKIFF_STATEFUL_VOLUME"] = member.VolumeName
	env["SKIFF_STATEFUL_MOUNT_PATH"] = firstNonEmpty(runtime.MountPath, appleStatefulDefaultMountPath)
	keys := sortedStringKeys(env)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func appleDesiredBody(desired any) (json.RawMessage, string, error) {
	body, err := canonical.Marshal(desired)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return append(json.RawMessage(nil), body...), hex.EncodeToString(sum[:]), nil
}

func planContainsAppleStateful(plan *provider.Plan) bool {
	for _, change := range plan.Resources {
		switch change.Kind {
		case ir.ResourceKindStatefulGroup, ir.ResourceKindStatefulMember, ir.ResourceKindStatefulVolume, ir.ResourceKindStatefulDNS, ir.ResourceKindStatefulRecipe:
			return true
		}
	}
	return false
}

func appleVolumeSizeBytes(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return appleStatefulDefaultVolumeBytes
	}
	multipliers := []struct {
		suffix string
		value  int64
	}{
		{"Gi", 1024 * 1024 * 1024},
		{"Mi", 1024 * 1024},
		{"Ki", 1024},
		{"G", 1000 * 1000 * 1000},
		{"M", 1000 * 1000},
		{"K", 1000},
	}
	for _, multiplier := range multipliers {
		if strings.HasSuffix(value, multiplier.suffix) {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, multiplier.suffix)), 10, 64)
			if err == nil && n > 0 {
				return n * multiplier.value
			}
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err == nil && n > 0 {
		return n
	}
	return appleStatefulDefaultVolumeBytes
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedIntKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstPort(ports map[string]int) int {
	for _, key := range sortedIntKeys(ports) {
		if ports[key] > 0 {
			return ports[key]
		}
	}
	return 0
}

func firstHostPort(ports map[string]int) int {
	for _, key := range sortedIntKeys(ports) {
		if ports[key] > 0 {
			return ports[key]
		}
	}
	return 0
}

func portNameForContainerPort(ports map[string]int, port int) string {
	for _, key := range sortedIntKeys(ports) {
		if ports[key] == port {
			return key
		}
	}
	return ""
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func appleContainerNotFound(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") || strings.Contains(text, "does not exist") || strings.Contains(text, "no such")
}

func appleAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "already exists") || strings.Contains(text, "exists")
}

func (p *Provider) appleStatefulOperation(kind, id, description string, observedAt time.Time) schema.ProviderOperationRef {
	return schema.ProviderOperationRef{
		Provider:    Name,
		Kind:        kind,
		ID:          id,
		ObservedAt:  canonical.Time(observedAt.UTC()),
		Description: description,
	}
}

func applePathSafe(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "resource"
	}
	return out
}
