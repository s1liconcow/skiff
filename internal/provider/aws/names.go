package aws

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
)

const collisionSuffixLength = 10

type NameInput struct {
	Service   string
	Env       string
	Kind      string
	LogicalID string
	Base      string
}

var resourceNameLimits = map[string]int{
	ResourceKindAutoScalingGroup:   255,
	ResourceKindLaunchTemplate:     128,
	ResourceKindTargetGroup:        32,
	ResourceKindIAMRole:            64,
	ResourceKindLogGroup:           512,
	ResourceKindRDSInstance:        63,
	ResourceKindSecret:             512,
	ResourceKindS3Bucket:           63,
	ResourceKindEC2Instance:        255,
	ResourceKindEBSVolume:          255,
	ResourceKindEBSAttachment:      255,
	ResourceKindRoute53Record:      255,
	ResourceKindSnapshotPolicy:     255,
	ResourceKindFencingPolicy:      255,
	ResourceKindSecurityGroup:      255,
	ir.ResourceKindSecurityGroup:   255,
	ir.ResourceKindListener:        128,
	ir.ResourceKindMetricConfig:    255,
	ir.ResourceKindRuntimeManifest: 255,
}

var resourceKindSuffixes = map[string]string{
	ir.ResourceKindAutoscalingGroup: "asg",
	ir.ResourceKindInstanceTemplate: "lt",
	ir.ResourceKindTargetGroup:      "tg",
	ir.ResourceKindIAMRole:          "role",
	ir.ResourceKindWorkloadIdentity: "identity",
	ir.ResourceKindSecurityGroup:    "sg",
	ir.ResourceKindLogConfig:        "logs",
	ir.ResourceKindMetricConfig:     "metrics",
	ir.ResourceKindListener:         "listener",
	ir.ResourceKindManagedDatabase:  "db",
	ir.ResourceKindDatabaseSecret:   "secret",
	ir.ResourceKindObjectStore:      "bucket",
	ir.ResourceKindRuntimeManifest:  "runtime",
	ResourceKindAutoScalingGroup:    "asg",
	ResourceKindLaunchTemplate:      "lt",
	ResourceKindTargetGroup:         "tg",
	ResourceKindIAMRole:             "role",
	ResourceKindLogGroup:            "logs",
	ResourceKindRDSInstance:         "db",
	ResourceKindSecret:              "secret",
	ResourceKindS3Bucket:            "bucket",
	ResourceKindEC2Instance:         "member",
	ResourceKindEBSVolume:           "volume",
	ResourceKindEBSAttachment:       "volume-attachment",
	ResourceKindRoute53Record:       "dns",
	ResourceKindSnapshotPolicy:      "snapshot-policy",
	ResourceKindFencingPolicy:       "fencing",
	ResourceKindSecurityGroup:       "sg",
}

func NameForResource(service, env string, meta ir.ResourceMeta) (string, error) {
	return ResourceName(NameInput{
		Service:   service,
		Env:       env,
		Kind:      meta.Kind,
		LogicalID: meta.LogicalID,
		Base:      meta.Name,
	})
}

func ResourceName(input NameInput) (string, error) {
	kind := normalizeKind(input.Kind)
	limit := resourceNameLimits[kind]
	if limit == 0 {
		return "", &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "name",
			Resource: input.Kind,
			Summary:  "unsupported aws resource kind",
		}
	}

	raw := strings.TrimSpace(input.Base)
	if raw == "" {
		suffix := resourceKindSuffixes[input.Kind]
		if suffix == "" {
			suffix = resourceKindSuffixes[kind]
		}
		raw = strings.Join(nonEmpty("skiff", input.Env, input.Service, suffix), "-")
	}
	name := sanitizeAWSName(raw)
	if name == "" {
		name = "skiff"
	}

	normalizedRaw := strings.ToLower(strings.TrimSpace(raw))
	needsSuffix := name != normalizedRaw || len(name) > limit
	if !needsSuffix {
		return name, nil
	}

	suffix := "-" + collisionSuffix(input)
	budget := limit - len(suffix)
	if budget < 1 {
		return "", &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "name",
			Resource: input.Kind,
			Summary:  fmt.Sprintf("aws resource name limit %d is too small for collision suffix", limit),
		}
	}
	if len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	if name == "" {
		name = "skiff"
		if len(name) > budget {
			name = name[:budget]
		}
	}
	return name + suffix, nil
}

func ResourceNameLimit(kind string) int {
	return resourceNameLimits[normalizeKind(kind)]
}

func sanitizeAWSName(value string) string {
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
	return strings.Trim(b.String(), "-")
}

func collisionSuffix(input NameInput) string {
	seed := strings.Join([]string{
		input.Service,
		input.Env,
		input.Kind,
		input.LogicalID,
		input.Base,
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])[:collisionSuffixLength]
}

func normalizeKind(kind string) string {
	switch kind {
	case ir.ResourceKindAutoscalingGroup, ResourceKindAutoScalingGroup:
		return ResourceKindAutoScalingGroup
	case ir.ResourceKindInstanceTemplate, ResourceKindLaunchTemplate:
		return ResourceKindLaunchTemplate
	case ir.ResourceKindTargetGroup, ResourceKindTargetGroup:
		return ResourceKindTargetGroup
	case ir.ResourceKindIAMRole, ir.ResourceKindWorkloadIdentity, ResourceKindIAMRole:
		return ResourceKindIAMRole
	case ir.ResourceKindLogConfig, ResourceKindLogGroup:
		return ResourceKindLogGroup
	case ir.ResourceKindManagedDatabase, ResourceKindRDSInstance:
		return ResourceKindRDSInstance
	case ir.ResourceKindDatabaseSecret, ResourceKindSecret:
		return ResourceKindSecret
	case ir.ResourceKindObjectStore, ResourceKindS3Bucket:
		return ResourceKindS3Bucket
	default:
		return kind
	}
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
