package ir

const (
	ResourceKindWorkloadIdentity   = "WorkloadIdentity"
	ResourceKindIAMRole            = "IAMRole"
	ResourceKindSecurityGroup      = "SecurityGroup"
	ResourceKindLogConfig          = "LogConfig"
	ResourceKindMetricConfig       = "MetricConfig"
	ResourceKindTargetGroup        = "TargetGroup"
	ResourceKindListener           = "Listener"
	ResourceKindManagedDatabase    = "ManagedDatabase"
	ResourceKindDatabaseSecret     = "DatabaseSecret"
	ResourceKindDatabaseBinding    = "DatabaseBinding"
	ResourceKindObjectStore        = "ObjectStore"
	ResourceKindObjectStoreBinding = "ObjectStoreBinding"
	ResourceKindInstanceTemplate   = "InstanceTemplate"
	ResourceKindAutoscalingGroup   = "AutoscalingGroup"
	ResourceKindRuntimeManifest    = "RuntimeManifest"
	ResourceKindGlobalTraffic      = "GlobalTrafficPolicy"
	ResourceKindStatefulGroup      = "StatefulGroup"
	ResourceKindStatefulMember     = "StatefulMember"
	ResourceKindStatefulVolume     = "StatefulVolume"
	ResourceKindStatefulDNS        = "StatefulDNSIdentity"
	ResourceKindStatefulRecipe     = "StatefulRecipeRuntime"
	ResourceKindSnapshotPolicy     = "StatefulSnapshotPolicy"
	ResourceKindUpdatePolicy       = "StatefulUpdatePolicy"
	ResourceKindPackageOperation   = "PackageOperation"

	TagService            = "skiff.dev/service"
	TagEnv                = "skiff.dev/env"
	TagManaged            = "skiff.dev/managed"
	TagGraph              = "skiff.dev/graph"
	TagDatabase           = "skiff.dev/database"
	TagObjectStore        = "skiff.dev/object-store"
	TagObjectStorePurpose = "skiff.dev/object-store-purpose"
	TagRegion             = "skiff.dev/region"
	TagStatefulGroup      = "skiff.dev/stateful-group"
	TagMemberOrdinal      = "skiff.dev/member-ordinal"
	TagStatefulRecipe     = "skiff.dev/recipe"
	TagPackage            = "skiff.dev/package"
	TagDependency         = "skiff.dev/dependency"
)

func RequiredTags(service, env string) map[string]string {
	return map[string]string{
		TagService: service,
		TagEnv:     env,
		TagManaged: "true",
		TagGraph:   "service/" + env + "/" + service,
	}
}
