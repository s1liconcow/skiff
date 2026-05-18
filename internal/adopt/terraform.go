package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	OwnershipDirect                     = "direct"
	OwnershipTerraformInfraSkiffRelease = "terraform-infra-skiff-release"
	OwnershipExternal                   = "external"
)

type TerraformMapping struct {
	Service       string                       `json:"service"`
	Env           string                       `json:"env"`
	Provider      string                       `json:"provider"`
	OwnershipMode string                       `json:"ownership_mode"`
	Resources     map[string]TerraformResource `json:"resources"`
}

type TerraformResource struct {
	Kind       string            `json:"kind"`
	LogicalID  string            `json:"logical_id"`
	Name       string            `json:"name,omitempty"`
	ProviderID string            `json:"provider_id"`
	ARN        string            `json:"arn,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type LoadOptions struct {
	Service       string
	Env           string
	OwnershipMode string
}

type RecordOptions struct {
	Clock func() time.Time
}

type RecordResult struct {
	Service   string             `json:"service"`
	Env       string             `json:"env"`
	Provider  string             `json:"provider"`
	Mode      string             `json:"mode"`
	Resources []RecordedResource `json:"resources"`
}

type RecordedResource struct {
	Kind        string                `json:"kind"`
	LogicalID   string                `json:"logical_id"`
	ProviderID  string                `json:"provider_id"`
	LogicalKey  string                `json:"logical_key"`
	ProviderKey string                `json:"provider_key"`
	Record      schema.ResourceRecord `json:"record"`
}

func MappingFromAWSResources(resources *aws.ServiceResources, mode string) TerraformMapping {
	if mode == "" {
		mode = OwnershipTerraformInfraSkiffRelease
	}
	mapping := TerraformMapping{
		Provider:      aws.Name,
		OwnershipMode: mode,
		Resources:     map[string]TerraformResource{},
	}
	if resources == nil {
		return mapping
	}
	mapping.Service = resources.Service
	mapping.Env = resources.Env
	add := func(key, kind, logicalID, name, providerID string, tags map[string]string) {
		if providerID == "" {
			providerID = name
		}
		mapping.Resources[key] = TerraformResource{
			Kind:       kind,
			LogicalID:  logicalID,
			Name:       name,
			ProviderID: providerID,
			Tags:       cloneTags(tags),
		}
	}
	for _, item := range resources.IAMRoles {
		add(resourceKey(item.LogicalID), aws.ResourceKindIAMRole, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.InstanceProfiles {
		add(resourceKey(item.LogicalID), aws.ResourceKindIAMInstanceProfile, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.SecurityGroups {
		add(resourceKey(item.LogicalID), aws.ResourceKindSecurityGroup, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.LogGroups {
		add(resourceKey(item.LogicalID), aws.ResourceKindLogGroup, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.MetricConfigs {
		add(resourceKey(item.LogicalID), aws.ResourceKindMetricConfig, item.LogicalID, item.Namespace, item.Namespace, item.Tags)
	}
	for _, item := range resources.TargetGroups {
		add(resourceKey(item.LogicalID), aws.ResourceKindTargetGroup, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.ListenerRules {
		add(resourceKey(item.LogicalID), aws.ResourceKindListenerRule, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.Databases {
		add(resourceKey(item.LogicalID), aws.ResourceKindRDSInstance, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.Secrets {
		add(resourceKey(item.LogicalID), aws.ResourceKindSecret, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.LaunchTemplates {
		add(resourceKey(item.LogicalID), aws.ResourceKindLaunchTemplate, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.AutoScalingGroups {
		add(resourceKey(item.LogicalID), aws.ResourceKindAutoScalingGroup, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.StatefulMembers {
		add(resourceKey(item.LogicalID), aws.ResourceKindEC2Instance, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.EBSVolumes {
		add(resourceKey(item.LogicalID), aws.ResourceKindEBSVolume, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.VolumeAttachments {
		add(resourceKey(item.LogicalID), aws.ResourceKindEBSAttachment, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.Route53Records {
		add(resourceKey(item.LogicalID), aws.ResourceKindRoute53Record, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.SnapshotPolicies {
		add(resourceKey(item.LogicalID), aws.ResourceKindSnapshotPolicy, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	for _, item := range resources.FencingPolicies {
		add(resourceKey(item.LogicalID), aws.ResourceKindFencingPolicy, item.LogicalID, item.Name, item.Name, item.Tags)
	}
	return mapping
}

func LoadTerraformMapping(path string, opts LoadOptions) (TerraformMapping, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return TerraformMapping{}, errors.New("terraform mapping path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return TerraformMapping{}, err
	}
	if info.IsDir() {
		for _, name := range []string{"skiff_resources.json", "terraform-output.json", "outputs.json"} {
			candidate := filepath.Join(path, name)
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return TerraformMapping{}, err
	}
	mapping, err := DecodeTerraformMapping(body)
	if err != nil {
		return TerraformMapping{}, err
	}
	if mapping.Service == "" {
		mapping.Service = opts.Service
	}
	if mapping.Env == "" {
		mapping.Env = opts.Env
	}
	if mapping.OwnershipMode == "" {
		mapping.OwnershipMode = opts.OwnershipMode
	}
	return normalizeMapping(mapping)
}

func DecodeTerraformMapping(body []byte) (TerraformMapping, error) {
	var direct TerraformMapping
	if err := json.Unmarshal(body, &direct); err == nil && (direct.Service != "" || len(direct.Resources) > 0) {
		return direct, nil
	}
	var allOutputs map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &allOutputs); err == nil {
		if output, ok := allOutputs["skiff_resources"]; ok && len(output.Value) > 0 {
			if err := json.Unmarshal(output.Value, &direct); err != nil {
				return TerraformMapping{}, err
			}
			return direct, nil
		}
	}
	var singleOutput struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &singleOutput); err == nil && len(singleOutput.Value) > 0 {
		if err := json.Unmarshal(singleOutput.Value, &direct); err != nil {
			return TerraformMapping{}, err
		}
		return direct, nil
	}
	return TerraformMapping{}, errors.New("terraform output does not contain skiff_resources")
}

func RecordTerraform(ctx context.Context, store objstore.ObjectStore, mapping TerraformMapping, opts RecordOptions) (*RecordResult, error) {
	if store == nil {
		return nil, errors.New("object store is required")
	}
	mapping, err := normalizeMapping(mapping)
	if err != nil {
		return nil, err
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	result := &RecordResult{
		Service:  mapping.Service,
		Env:      mapping.Env,
		Provider: mapping.Provider,
		Mode:     mapping.OwnershipMode,
	}
	keys := make([]string, 0, len(mapping.Resources))
	for key := range mapping.Resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		resource := mapping.Resources[key]
		record := schema.ResourceRecord{
			SchemaVersion: schema.Version,
			Logical: schema.ResourceLogicalRef{
				Kind: resource.Kind,
				Name: resource.LogicalID,
			},
			Provider: schema.ResourceProviderRef{
				Provider: mapping.Provider,
				Kind:     resource.Kind,
				ID:       resource.ProviderID,
			},
			Service: mapping.Service,
			Env:     mapping.Env,
			Ownership: &schema.ResourceOwnership{
				Mode:      mapping.OwnershipMode,
				Source:    "terraform",
				ManagedBy: "terraform",
				AdoptedAt: canonical.Time(clock().UTC()),
			},
			Tags:       cloneTags(resource.Tags),
			ObservedAt: canonical.Time(clock().UTC()),
		}
		body, err := canonical.Marshal(record)
		if err != nil {
			return nil, err
		}
		logicalKey, err := paths.LogicalResource(resource.Kind, pathSafeResourceName(resource.LogicalID))
		if err != nil {
			return nil, err
		}
		providerKey, err := paths.ProviderResource(mapping.Provider, resource.Kind, resource.ProviderID)
		if err != nil {
			return nil, err
		}
		if err := upsert(ctx, store, logicalKey, body, record); err != nil {
			return nil, err
		}
		if err := upsert(ctx, store, providerKey, body, record); err != nil {
			return nil, err
		}
		result.Resources = append(result.Resources, RecordedResource{
			Kind:        resource.Kind,
			LogicalID:   resource.LogicalID,
			ProviderID:  resource.ProviderID,
			LogicalKey:  logicalKey,
			ProviderKey: providerKey,
			Record:      record,
		})
	}
	return result, nil
}

func normalizeMapping(mapping TerraformMapping) (TerraformMapping, error) {
	mapping.Service = strings.TrimSpace(mapping.Service)
	mapping.Env = strings.TrimSpace(mapping.Env)
	mapping.Provider = strings.TrimSpace(mapping.Provider)
	mapping.OwnershipMode = strings.TrimSpace(mapping.OwnershipMode)
	if mapping.Provider == "" {
		mapping.Provider = aws.Name
	}
	if mapping.OwnershipMode == "" {
		mapping.OwnershipMode = OwnershipTerraformInfraSkiffRelease
	}
	switch mapping.OwnershipMode {
	case OwnershipDirect, OwnershipTerraformInfraSkiffRelease, OwnershipExternal:
	default:
		return mapping, fmt.Errorf("unsupported ownership mode %q", mapping.OwnershipMode)
	}
	if mapping.Service == "" {
		return mapping, errors.New("service is required")
	}
	if mapping.Env == "" {
		return mapping, errors.New("env is required")
	}
	if len(mapping.Resources) == 0 {
		return mapping, errors.New("at least one Terraform resource is required")
	}
	for key, resource := range mapping.Resources {
		resource.Kind = strings.TrimSpace(resource.Kind)
		resource.LogicalID = strings.TrimSpace(resource.LogicalID)
		resource.Name = strings.TrimSpace(resource.Name)
		resource.ProviderID = strings.TrimSpace(resource.ProviderID)
		if resource.Kind == "" {
			return mapping, fmt.Errorf("resource %s kind is required", key)
		}
		if resource.LogicalID == "" {
			resource.LogicalID = key
		}
		if resource.ProviderID == "" {
			resource.ProviderID = resource.Name
		}
		if resource.ProviderID == "" {
			return mapping, fmt.Errorf("resource %s provider_id is required", key)
		}
		resource.Tags = cloneTags(resource.Tags)
		mapping.Resources[key] = resource
	}
	return mapping, nil
}

func upsert(ctx context.Context, store objstore.ObjectStore, key string, body []byte, record schema.ResourceRecord) error {
	opts := objstore.PutOptions{
		ContentType: canonical.ContentType,
		Metadata: map[string]string{
			"schema_version": record.SchemaVersion,
			"provider":       record.Provider.Provider,
			"provider_kind":  record.Provider.Kind,
			"provider_id":    record.Provider.ID,
			"logical_kind":   record.Logical.Kind,
			"ownership":      record.Ownership.Mode,
		},
	}
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

func resourceKey(logicalID string) string {
	return strings.ReplaceAll(pathSafeResourceName(logicalID), "-", "_")
}

func pathSafeResourceName(value string) string {
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

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}
