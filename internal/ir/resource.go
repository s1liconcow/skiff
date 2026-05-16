package ir

const (
	ResourceKindWorkloadIdentity = "WorkloadIdentity"
	ResourceKindIAMRole          = "IAMRole"
	ResourceKindSecurityGroup    = "SecurityGroup"
	ResourceKindLogConfig        = "LogConfig"
	ResourceKindMetricConfig     = "MetricConfig"
	ResourceKindTargetGroup      = "TargetGroup"
	ResourceKindListener         = "Listener"
	ResourceKindInstanceTemplate = "InstanceTemplate"
	ResourceKindAutoscalingGroup = "AutoscalingGroup"
	ResourceKindRuntimeManifest  = "RuntimeManifest"

	TagService = "skiff.dev/service"
	TagEnv     = "skiff.dev/env"
	TagManaged = "skiff.dev/managed"
	TagGraph   = "skiff.dev/graph"
)

func RequiredTags(service, env string) map[string]string {
	return map[string]string{
		TagService: service,
		TagEnv:     env,
		TagManaged: "true",
		TagGraph:   "service/" + env + "/" + service,
	}
}
