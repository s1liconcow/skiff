package runner

import "github.com/s1liconcow/skiff/internal/config"

const (
	LogProviderAWSCloudWatch = "aws-cloudwatch"
	DefaultLogStreamTemplate = "{service}/{release}/{instance}"
)

func CloudWatchLogForwarding(service, env, releaseID, region, group, archivePrefix string) config.Logs {
	labels := map[string]string{
		"service": service,
		"env":     env,
	}
	if releaseID != "" {
		labels["release"] = releaseID
	}
	if region != "" {
		labels["region"] = region
	}
	return config.Logs{
		Provider:       LogProviderAWSCloudWatch,
		Group:          group,
		StreamTemplate: DefaultLogStreamTemplate,
		ArchivePrefix:  archivePrefix,
		Labels:         labels,
	}
}
