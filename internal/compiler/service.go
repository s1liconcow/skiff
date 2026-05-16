package compiler

import (
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/spec"
)

func compileService(doc spec.Document, _ Options) *ir.Graph {
	service := doc.Metadata.Name
	env := doc.Metadata.Env
	ids := resourceIDs(service)
	tags := ir.RequiredTags(service, env)
	secretRefs := compileSecretRefs(doc.Secrets)
	health := compileHealth(doc.Runtime.Health)

	graph := &ir.Graph{
		SchemaVersion: ir.SchemaVersion,
		Service:       service,
		Env:           env,
	}

	graph.Resources.WorkloadIdentities = []ir.WorkloadIdentity{
		{
			Meta: meta(ids.identity, ir.ResourceKindWorkloadIdentity, resourceName(env, service, "identity"), tags, "$.metadata"),
		},
	}
	graph.Resources.IAMRoles = []ir.IAMRole{
		{
			Meta:                meta(ids.role, ir.ResourceKindIAMRole, resourceName(env, service, "role"), tags, "$.metadata", "$.secrets"),
			WorkloadIdentityRef: ids.identity,
			SecretRefs:          secretRefs,
		},
	}
	graph.Resources.SecurityGroups = []ir.SecurityGroup{
		{
			Meta:  meta(ids.securityGroup, ir.ResourceKindSecurityGroup, resourceName(env, service, "sg"), tags, "$.network", "$.runtime.port"),
			Rules: securityRules(doc),
		},
	}
	graph.Resources.LogConfigs = []ir.LogConfig{
		{
			Meta:    meta(ids.logs, ir.ResourceKindLogConfig, resourceName(env, service, "logs"), tags, "$.runtime.logs"),
			Enabled: doc.Runtime.Logs.Enabled,
			Format:  doc.Runtime.Logs.Format,
		},
	}
	graph.Resources.MetricConfigs = []ir.MetricConfig{
		{
			Meta:    meta(ids.metrics, ir.ResourceKindMetricConfig, resourceName(env, service, "metrics"), tags, "$.runtime.metrics"),
			Enabled: doc.Runtime.Metrics.Enabled,
			Path:    doc.Runtime.Metrics.Path,
		},
	}
	graph.Resources.TargetGroups = []ir.TargetGroup{
		{
			Meta:        meta(ids.targetGroup, ir.ResourceKindTargetGroup, resourceName(env, service, "tg"), tags, "$.runtime.port", "$.runtime.health"),
			Protocol:    "HTTP",
			Port:        doc.Runtime.Port,
			HealthCheck: health,
		},
	}
	if listener := compileListener(doc, ids.targetGroup, env, service, tags); listener != nil {
		graph.Resources.Listeners = []ir.Listener{*listener}
	}
	graph.Resources.InstanceTemplates = []ir.InstanceTemplate{
		{
			Meta:                meta(ids.instanceTemplate, ir.ResourceKindInstanceTemplate, resourceName(env, service, "instance-template"), tags, "$.artifact", "$.runtime", "$.machine", "$.secrets"),
			Machine:             ir.Machine{Size: doc.Machine.Size, Arch: doc.Machine.Arch},
			Artifact:            compileArtifact(*doc.Artifact),
			Runtime:             compileRuntime(doc.Runtime),
			WorkloadIdentityRef: ids.identity,
			IAMRoleRef:          ids.role,
			SecurityGroupRefs:   []string{ids.securityGroup},
			LogConfigRef:        ids.logs,
			MetricConfigRef:     ids.metrics,
		},
	}
	graph.Resources.AutoscalingGroups = []ir.AutoscalingGroup{
		{
			Meta:                meta(ids.autoscalingGroup, ir.ResourceKindAutoscalingGroup, resourceName(env, service, "asg"), tags, "$.scale", "$.rollout"),
			Min:                 doc.Scale.Min,
			Max:                 doc.Scale.Max,
			InstanceTemplateRef: ids.instanceTemplate,
			TargetGroupRefs:     []string{ids.targetGroup},
			Rollout: ir.Rollout{
				Strategy:          doc.Rollout.Strategy,
				BatchSize:         doc.Rollout.BatchSize,
				HealthGracePeriod: doc.Rollout.HealthGracePeriod,
			},
		},
	}
	graph.Resources.RuntimeManifests = []ir.RuntimeManifest{
		{
			Meta:        meta(ids.runtimeManifest, ir.ResourceKindRuntimeManifest, resourceName(env, service, "runtime"), tags, "$.artifact", "$.runtime", "$.secrets"),
			Artifact:    compileArtifact(*doc.Artifact),
			Command:     cloneStrings(doc.Runtime.Command),
			Env:         cloneMap(doc.Runtime.Env),
			SecretRefs:  secretRefs,
			HealthCheck: health,
			Metrics: ir.AppMetrics{
				Enabled: doc.Runtime.Metrics.Enabled,
				Path:    doc.Runtime.Metrics.Path,
				Port:    doc.Runtime.Port,
			},
		},
	}

	return graph
}

type resourceIDSet struct {
	identity         string
	role             string
	securityGroup    string
	logs             string
	metrics          string
	targetGroup      string
	listener         string
	instanceTemplate string
	autoscalingGroup string
	runtimeManifest  string
}

func resourceIDs(service string) resourceIDSet {
	return resourceIDSet{
		identity:         "workload-identity:" + service,
		role:             "iam-role:" + service,
		securityGroup:    "security-group:" + service,
		logs:             "logs:" + service,
		metrics:          "metrics:" + service,
		targetGroup:      "target-group:" + service,
		listener:         "listener:" + service,
		instanceTemplate: "instance-template:" + service,
		autoscalingGroup: "autoscaling-group:" + service,
		runtimeManifest:  "runtime-manifest:" + service,
	}
}

func meta(logicalID, kind, name string, tags map[string]string, sources ...string) ir.ResourceMeta {
	sourceRefs := make([]ir.SourceRef, 0, len(sources))
	for _, source := range sources {
		if source == "" {
			continue
		}
		sourceRefs = append(sourceRefs, ir.SourceRef{Path: source})
	}
	return ir.ResourceMeta{
		LogicalID: logicalID,
		Kind:      kind,
		Name:      name,
		Tags:      cloneMap(tags),
		Source:    sourceRefs,
	}
}

func resourceName(env, service, suffix string) string {
	parts := []string{"skiff", env, service, suffix}
	return strings.Join(parts, "-")
}

func compileArtifact(artifact spec.Artifact) ir.Artifact {
	return ir.Artifact{
		Type:   artifact.Type,
		Ref:    artifact.Ref,
		Digest: artifact.Digest,
	}
}

func compileRuntime(runtime spec.Runtime) ir.Runtime {
	return ir.Runtime{
		Port:          runtime.Port,
		Command:       cloneStrings(runtime.Command),
		Env:           cloneMap(runtime.Env),
		HealthCheck:   compileHealth(runtime.Health),
		ShutdownGrace: runtime.ShutdownGrace,
	}
}

func compileHealth(health spec.Health) ir.HealthCheck {
	return ir.HealthCheck{
		Type:     health.Type,
		Path:     health.Path,
		Port:     health.Port,
		Command:  cloneStrings(health.Command),
		Interval: health.Interval,
		Timeout:  health.Timeout,
	}
}

func compileSecretRefs(secrets []spec.SecretRef) []ir.SecretRef {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]ir.SecretRef, 0, len(secrets))
	for _, secret := range secrets {
		out = append(out, ir.SecretRef{Name: secret.Name, Ref: secret.Ref})
	}
	return out
}

func compileListener(doc spec.Document, targetGroupRef, env, service string, tags map[string]string) *ir.Listener {
	if doc.Network.Ingress == nil {
		return nil
	}
	if doc.Network.Ingress.Type != "public-http" && doc.Network.Ingress.Type != "internal-http" {
		return nil
	}
	visibility := "internal"
	protocol := "HTTP"
	port := 80
	tls := ir.TLS{}
	certRef := doc.Network.Ingress.CertRef
	if doc.Network.Ingress.TLS != nil && doc.Network.Ingress.TLS.CertRef != "" {
		certRef = doc.Network.Ingress.TLS.CertRef
	}
	if doc.Network.Ingress.Type == "public-http" {
		visibility = "public"
		protocol = "HTTPS"
		port = 443
		tls = ir.TLS{Enabled: true, CertRef: certRef}
	}
	ids := resourceIDs(service)
	return &ir.Listener{
		Meta:           meta(ids.listener, ir.ResourceKindListener, resourceName(env, service, "listener"), tags, "$.network.ingress"),
		Visibility:     visibility,
		Protocol:       protocol,
		Port:           port,
		Host:           doc.Network.Ingress.Host,
		TLS:            tls,
		TargetGroupRef: targetGroupRef,
	}
}

func securityRules(doc spec.Document) []ir.SecurityRule {
	rules := []ir.SecurityRule{
		{
			Direction:   "egress",
			Protocol:    "all",
			Destination: "0.0.0.0/0",
			Description: "allow workload egress",
		},
	}
	if doc.Network.Ingress != nil && (doc.Network.Ingress.Type == "public-http" || doc.Network.Ingress.Type == "internal-http") {
		rules = append(rules, ir.SecurityRule{
			Direction:   "ingress",
			Protocol:    "tcp",
			FromPort:    doc.Runtime.Port,
			ToPort:      doc.Runtime.Port,
			Source:      "load-balancer",
			Description: "allow load balancer traffic to workload",
		})
	}
	return rules
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
