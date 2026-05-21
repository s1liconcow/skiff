package applecontainer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

const (
	appleServiceDefaultGeneration = int64(1)
)

type appleServiceRuntime struct {
	SchemaVersion string                       `json:"schema_version"`
	Provider      string                       `json:"provider"`
	Service       string                       `json:"service"`
	Env           string                       `json:"env"`
	GroupName     string                       `json:"group_name"`
	Image         string                       `json:"image"`
	Command       []string                     `json:"command,omitempty"`
	EnvVars       map[string]string            `json:"env_vars,omitempty"`
	Port          int                          `json:"port,omitempty"`
	HealthPath    string                       `json:"health_path,omitempty"`
	HealthPort    int                          `json:"health_port,omitempty"`
	Replicas      []appleServiceReplicaRuntime `json:"replicas"`
	UpdatedAt     string                       `json:"updated_at"`
}

type appleServiceReplicaRuntime struct {
	Ordinal       int    `json:"ordinal"`
	ContainerName string `json:"container_name"`
	HostPort      int    `json:"host_port,omitempty"`
}

func (p *Provider) planServiceWithStateful(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	servicePlan, err := p.planService(ctx, graph)
	if err != nil {
		return nil, err
	}
	statefulPlan, err := p.planStateful(ctx, graph)
	if err != nil {
		return nil, err
	}
	merged := &provider.Plan{
		Provider:  Name,
		Service:   graph.Service,
		Env:       graph.Env,
		Resources: make([]provider.PlannedChange, 0, len(servicePlan.Resources)+len(statefulPlan.Resources)),
	}
	for _, change := range servicePlan.Resources {
		if appleStatefulPlanKind(change.Kind) {
			continue
		}
		merged.Resources = append(merged.Resources, change)
	}
	merged.Resources = append(merged.Resources, statefulPlan.Resources...)
	sortApplePlan(merged.Resources)
	return merged, nil
}

func (p *Provider) planService(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	plan, err := p.Provider.Plan(ctx, graph)
	if err != nil {
		return nil, err
	}
	runtime, ok, err := p.appleServiceRuntimeFromGraph(graph)
	if err != nil {
		return nil, err
	}
	if !ok {
		return plan, nil
	}
	plan.Provider = Name
	body, fingerprint, err := appleDesiredBody(runtime)
	if err != nil {
		return nil, err
	}
	for i := range plan.Resources {
		if plan.Resources[i].Kind != ir.ResourceKindAutoscalingGroup {
			continue
		}
		plan.Resources[i].ProviderID = runtime.GroupName
		plan.Resources[i].Summary = fmt.Sprintf("Apple service with %d local replica container(s)", len(runtime.Replicas))
		plan.Resources[i].Desired = body
		plan.Resources[i].Fingerprint = fingerprint
		break
	}
	return plan, nil
}

func (p *Provider) appleServiceRuntimeFromGraph(graph *ir.Graph) (appleServiceRuntime, bool, error) {
	if graph == nil || len(graph.Resources.RuntimeManifests) == 0 {
		return appleServiceRuntime{}, false, nil
	}
	compiled := graph.Resources.RuntimeManifests[0]
	if strings.TrimSpace(compiled.Artifact.Type) != "oci" {
		return appleServiceRuntime{}, false, nil
	}
	image := strings.TrimPrefix(strings.TrimSpace(compiled.Artifact.Ref), "oci://")
	if image == "" {
		return appleServiceRuntime{}, false, nil
	}
	replicas := 1
	if len(graph.Resources.AutoscalingGroups) > 0 && graph.Resources.AutoscalingGroups[0].Min > 0 {
		replicas = graph.Resources.AutoscalingGroups[0].Min
	}
	port := firstNonZero(compiled.Metrics.Port, firstTargetGroupPort(graph), compiled.HealthCheck.Port)
	hostBase := envInt("SKIFF_APPLE_SERVICE_PORT_BASE", port)
	groupName := appleServiceGroupName(graph.Env, graph.Service)
	serviceReplicas := make([]appleServiceReplicaRuntime, 0, replicas)
	for ordinal := 0; ordinal < replicas; ordinal++ {
		hostPort := 0
		if port > 0 && hostBase > 0 {
			hostPort = hostBase + ordinal
		}
		serviceReplicas = append(serviceReplicas, appleServiceReplicaRuntime{
			Ordinal:       ordinal,
			ContainerName: appleServiceContainerName(graph.Env, graph.Service, ordinal, appleServiceDefaultGeneration),
			HostPort:      hostPort,
		})
	}
	return appleServiceRuntime{
		SchemaVersion: "skiff.apple-container/v1",
		Provider:      Name,
		Service:       graph.Service,
		Env:           graph.Env,
		GroupName:     groupName,
		Image:         image,
		Command:       append([]string(nil), compiled.Command...),
		EnvVars:       cloneStringMap(compiled.Env),
		Port:          port,
		HealthPath:    compiled.HealthCheck.Path,
		HealthPort:    firstNonZero(compiled.HealthCheck.Port, port),
		Replicas:      serviceReplicas,
		UpdatedAt:     canonical.Time(p.now()),
	}, true, nil
}

func (p *Provider) applyServiceRuntime(ctx context.Context, plan *provider.Plan, runtime appleServiceRuntime, statefulByGroup map[string]appleStatefulRuntime) (*provider.ApplyResult, error) {
	if strings.TrimSpace(runtime.Image) == "" {
		return nil, &provider.Error{Code: provider.CodeInvalidConfig, Provider: Name, Op: "service_apply", Summary: "Apple service apply requires an OCI image"}
	}
	now := p.now()
	result := &provider.ApplyResult{Provider: Name, Service: runtime.Service, Env: runtime.Env, AppliedAt: now}
	for _, replica := range runtime.Replicas {
		if err := p.startServiceReplica(ctx, runtime, replica, statefulByGroup); err != nil {
			return nil, err
		}
		result.ResourceIDs = append(result.ResourceIDs, replica.ContainerName)
	}
	inspection := provider.ResourceInspection{
		Kind:       ir.ResourceKindAutoscalingGroup,
		LogicalID:  firstAutoscalingGroupID(plan),
		Name:       runtime.GroupName,
		ProviderID: runtime.GroupName,
		Status:     "running",
		Tags:       ir.RequiredTags(runtime.Service, runtime.Env),
	}
	if err := p.recordAppleResource(ctx, runtime.Service, runtime.Env, inspection, now); err != nil {
		return nil, err
	}
	result.ResourceIDs = append(result.ResourceIDs, runtime.GroupName)
	result.Resources = append(result.Resources, inspection)
	sort.Strings(result.ResourceIDs)
	return result, nil
}

func (p *Provider) startServiceReplica(ctx context.Context, runtime appleServiceRuntime, replica appleServiceReplicaRuntime, statefulByGroup map[string]appleStatefulRuntime) error {
	_, stopErr := p.container(ctx, "stop", "--time", "2", replica.ContainerName)
	if stopErr != nil && !appleContainerNotFound(stopErr) {
		return stopErr
	}
	_, deleteErr := p.container(ctx, "delete", "--force", replica.ContainerName)
	if deleteErr != nil && !appleContainerNotFound(deleteErr) {
		return deleteErr
	}
	args := []string{"run", "--name", replica.ContainerName, "--detach"}
	command := append([]string(nil), runtime.Command...)
	if len(command) > 0 {
		args = append(args, "--entrypoint", command[0])
	}
	if runtime.Port > 0 && replica.HostPort > 0 {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", replica.HostPort, runtime.Port))
	}
	env, err := appleServiceEnv(runtime, replica, statefulByGroup)
	if err != nil {
		return err
	}
	for _, item := range env {
		args = append(args, "-e", item)
	}
	args = append(args, runtime.Image)
	if len(command) > 1 {
		args = append(args, command[1:]...)
	}
	if _, err := p.container(ctx, args...); err != nil {
		return err
	}
	return p.waitServiceHealth(ctx, runtime, replica)
}

func (p *Provider) waitServiceHealth(ctx context.Context, runtime appleServiceRuntime, replica appleServiceReplicaRuntime) error {
	if runtime.HealthPath == "" || replica.HostPort <= 0 {
		return nil
	}
	client := http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(45 * time.Second)
	url := fmt.Sprintf("http://127.0.0.1:%d%s", replica.HostPort, runtime.HealthPath)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
			lastErr = fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: "service_health", Resource: replica.ContainerName, Summary: fmt.Sprintf("Apple service health check failed at %s: %v", url, lastErr)}
}

func appleServiceEnv(runtime appleServiceRuntime, replica appleServiceReplicaRuntime, statefulByGroup map[string]appleStatefulRuntime) ([]string, error) {
	env := cloneStringMap(runtime.EnvVars)
	if env == nil {
		env = map[string]string{}
	}
	env["SKIFF_SERVICE"] = runtime.Service
	env["SKIFF_ENV"] = runtime.Env
	env["SKIFF_REPLICA"] = fmt.Sprintf("%d", replica.Ordinal)
	env["SKIFF_GENERATION"] = fmt.Sprintf("%d", appleServiceDefaultGeneration)
	if runtime.Port > 0 {
		env["PORT"] = fmt.Sprintf("%d", runtime.Port)
	}
	for key, value := range env {
		resolved, err := resolveAppleServiceValue(value, statefulByGroup)
		if err != nil {
			return nil, err
		}
		env[key] = resolved
	}
	keys := sortedStringKeys(env)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out, nil
}

func resolveAppleServiceValue(value string, statefulByGroup map[string]appleStatefulRuntime) (string, error) {
	group, ok := statefulConnectionGroup(value)
	if !ok {
		return value, nil
	}
	runtime, ok := statefulByGroup[group]
	if !ok || len(runtime.Members) == 0 {
		return "", &provider.Error{Code: provider.CodeInvalidConfig, Provider: Name, Op: "service_env", Resource: group, Summary: fmt.Sprintf("stateful package binding %q is not available in the Apple plan", value)}
	}
	member := runtime.Members[0]
	port := runtime.Ports["postgres"]
	if port == 0 {
		port = firstPort(runtime.Ports)
	}
	if port == 0 {
		return "", &provider.Error{Code: provider.CodeInvalidConfig, Provider: Name, Op: "service_env", Resource: group, Summary: fmt.Sprintf("stateful package binding %q has no container port to connect to", value)}
	}
	address := strings.TrimSpace(member.ContainerAddress)
	if address == "" {
		return "", &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: "service_env", Resource: member.ContainerName, Summary: fmt.Sprintf("stateful package binding %q has no Apple container address; inspect must complete before launching the service", value)}
	}
	return fmt.Sprintf("postgres://postgres@%s/postgres?sslmode=disable", net.JoinHostPort(address, strconv.Itoa(port))), nil
}

func statefulConnectionGroup(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "secret://stateful/") {
		rest := strings.TrimPrefix(value, "secret://stateful/")
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] == "connection-url" {
			return parts[0], true
		}
	}
	if strings.HasPrefix(value, "skiff://stateful/") {
		rest := strings.TrimPrefix(value, "skiff://stateful/")
		if rest != "" {
			return strings.Trim(rest, "/"), true
		}
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme == "skiff" && parsed.Host == "stateful" {
		group := strings.Trim(parsed.Path, "/")
		return group, group != ""
	}
	return "", false
}

func appleServiceRuntimeFromPlan(plan *provider.Plan, now time.Time) (appleServiceRuntime, bool, error) {
	var runtime appleServiceRuntime
	for _, change := range plan.Resources {
		if change.Kind != ir.ResourceKindAutoscalingGroup || len(change.Desired) == 0 {
			continue
		}
		if err := canonical.UnmarshalStrict(change.Desired, &runtime); err != nil {
			continue
		}
		if runtime.Provider != Name || runtime.Service == "" || runtime.Image == "" {
			continue
		}
		if runtime.UpdatedAt == "" {
			runtime.UpdatedAt = canonical.Time(now)
		}
		return runtime, true, nil
	}
	return appleServiceRuntime{}, false, nil
}

func planContainsAppleService(plan *provider.Plan) bool {
	_, ok, _ := appleServiceRuntimeFromPlan(plan, time.Time{})
	return ok
}

func appleStatefulPlanKind(kind string) bool {
	switch kind {
	case ir.ResourceKindStatefulGroup, ir.ResourceKindStatefulMember, ir.ResourceKindStatefulVolume, ir.ResourceKindStatefulDNS, ir.ResourceKindStatefulRecipe, ir.ResourceKindSnapshotPolicy, ir.ResourceKindUpdatePolicy:
		return true
	default:
		return false
	}
}

func sortApplePlan(changes []provider.PlannedChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind == changes[j].Kind {
			return changes[i].LogicalID < changes[j].LogicalID
		}
		return changes[i].Kind < changes[j].Kind
	})
}

func appleServiceContainerName(env, service string, ordinal int, generation int64) string {
	return fmt.Sprintf("skiff-%s-%s-r%d-g%d", applePathSafe(env), applePathSafe(service), ordinal, generation)
}

func appleServiceGroupName(env, service string) string {
	return fmt.Sprintf("skiff-%s-%s-service", applePathSafe(env), applePathSafe(service))
}

func firstTargetGroupPort(graph *ir.Graph) int {
	if graph == nil || len(graph.Resources.TargetGroups) == 0 {
		return 0
	}
	return graph.Resources.TargetGroups[0].Port
}

func firstAutoscalingGroupID(plan *provider.Plan) string {
	if plan != nil {
		for _, change := range plan.Resources {
			if change.Kind == ir.ResourceKindAutoscalingGroup {
				return change.LogicalID
			}
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
