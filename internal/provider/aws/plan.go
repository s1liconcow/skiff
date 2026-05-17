package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type ServiceResourceManager interface {
	PlanResource(ctx context.Context, desired DesiredServiceResource) (*ResourcePlan, error)
	ApplyResource(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error)
}

type DesiredServiceResource struct {
	Kind        string            `json:"kind"`
	LogicalID   string            `json:"logical_id"`
	Name        string            `json:"name"`
	Tags        map[string]string `json:"tags,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Fingerprint string            `json:"fingerprint"`
	Desired     json.RawMessage   `json:"desired"`
}

type ResourcePlan struct {
	Action      string `json:"action"`
	ProviderID  string `json:"provider_id,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type AppliedResource struct {
	Kind        string            `json:"kind"`
	LogicalID   string            `json:"logical_id,omitempty"`
	Name        string            `json:"name"`
	ProviderID  string            `json:"provider_id"`
	ARN         string            `json:"arn,omitempty"`
	Status      string            `json:"status,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
}

func desiredServiceResources(resources *ServiceResources) ([]DesiredServiceResource, error) {
	if resources == nil {
		return nil, nil
	}
	var out []DesiredServiceResource
	appendDesired := func(kind, logicalID, name string, tags map[string]string, summary string, details any) error {
		resource, err := desiredServiceResource(kind, logicalID, name, tags, summary, details)
		if err != nil {
			return err
		}
		out = append(out, resource)
		return nil
	}
	for _, item := range resources.IAMRoles {
		if err := appendDesired(ResourceKindIAMRole, item.LogicalID, item.Name, item.Tags, "IAM role for workload and runner state access", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.InstanceProfiles {
		if err := appendDesired(ResourceKindIAMInstanceProfile, item.LogicalID, item.Name, item.Tags, "EC2 instance profile attached to workload VMs", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.SecurityGroups {
		if err := appendDesired(ResourceKindSecurityGroup, item.LogicalID, item.Name, item.Tags, "Security group for workload VM ingress and egress", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.LogGroups {
		if err := appendDesired(ResourceKindLogGroup, item.LogicalID, item.Name, item.Tags, "CloudWatch log group for workload logs", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.MetricConfigs {
		if err := appendDesired(ResourceKindMetricConfig, item.LogicalID, item.Namespace, item.Tags, "CloudWatch metric namespace and scraping config", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.TargetGroups {
		if err := appendDesired(ResourceKindTargetGroup, item.LogicalID, item.Name, item.Tags, "Load balancer target group for service instances", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.ListenerRules {
		if err := appendDesired(ResourceKindListenerRule, item.LogicalID, item.Name, item.Tags, "Load balancer listener rule for service ingress", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.Databases {
		if err := appendDesired(ResourceKindRDSInstance, item.LogicalID, item.Name, item.Tags, "RDS managed database with private network access, encrypted storage, backups, and deletion protection", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.Secrets {
		if err := appendDesired(ResourceKindSecret, item.LogicalID, item.Name, item.Tags, "Secrets Manager secret reference for managed database credentials", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.LaunchTemplates {
		if err := appendDesired(ResourceKindLaunchTemplate, item.LogicalID, item.Name, item.Tags, "EC2 launch template with skiff-runner user-data", item); err != nil {
			return nil, err
		}
	}
	for _, item := range resources.AutoScalingGroups {
		if err := appendDesired(ResourceKindAutoScalingGroup, item.LogicalID, item.Name, item.Tags, "Auto Scaling Group representing the service replica pool", item); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func desiredServiceResource(kind, logicalID, name string, tags map[string]string, summary string, details any) (DesiredServiceResource, error) {
	body, err := canonical.Marshal(details)
	if err != nil {
		return DesiredServiceResource{}, err
	}
	sum := sha256.Sum256(body)
	return DesiredServiceResource{
		Kind:        kind,
		LogicalID:   logicalID,
		Name:        name,
		Tags:        cloneTags(tags),
		Summary:     summary,
		Fingerprint: hex.EncodeToString(sum[:]),
		Desired:     append(json.RawMessage(nil), body...),
	}, nil
}

func plannedChangeFromDesired(resource DesiredServiceResource, plan *ResourcePlan) provider.PlannedChange {
	change := provider.PlannedChange{
		Action:      provider.ActionCreate,
		Kind:        resource.Kind,
		LogicalID:   resource.LogicalID,
		Name:        resource.Name,
		Tags:        cloneTags(resource.Tags),
		Summary:     "create " + resource.Summary,
		Fingerprint: resource.Fingerprint,
		Desired:     append(json.RawMessage(nil), resource.Desired...),
	}
	if plan == nil {
		return change
	}
	if plan.Action != "" {
		change.Action = plan.Action
	}
	if plan.ProviderID != "" {
		change.ProviderID = plan.ProviderID
	}
	if plan.Summary != "" {
		change.Summary = plan.Summary
	} else if plan.Action != "" {
		change.Summary = plan.Action + " " + resource.Summary
	}
	return change
}

func desiredFromPlannedChange(change provider.PlannedChange) DesiredServiceResource {
	return DesiredServiceResource{
		Kind:        change.Kind,
		LogicalID:   change.LogicalID,
		Name:        change.Name,
		Tags:        cloneTags(change.Tags),
		Summary:     change.Summary,
		Fingerprint: change.Fingerprint,
		Desired:     append(json.RawMessage(nil), change.Desired...),
	}
}

func appliedInspection(change provider.PlannedChange, applied AppliedResource) provider.ResourceInspection {
	if applied.Kind == "" {
		applied.Kind = change.Kind
	}
	if applied.LogicalID == "" {
		applied.LogicalID = change.LogicalID
	}
	if applied.Name == "" {
		applied.Name = change.Name
	}
	if applied.ProviderID == "" {
		applied.ProviderID = change.ProviderID
	}
	if len(applied.Tags) == 0 {
		applied.Tags = change.Tags
	}
	return provider.ResourceInspection{
		Kind:       applied.Kind,
		LogicalID:  applied.LogicalID,
		Name:       applied.Name,
		ProviderID: applied.ProviderID,
		ARN:        applied.ARN,
		Status:     applied.Status,
		Tags:       cloneTags(applied.Tags),
	}
}

func (p *Provider) recordAppliedResource(ctx context.Context, plan *provider.Plan, change provider.PlannedChange, applied AppliedResource) error {
	if p.stateStore == nil {
		return nil
	}
	if applied.ProviderID == "" {
		applied.ProviderID = firstNonEmpty(applied.Name, change.Name)
	}
	if applied.Kind == "" {
		applied.Kind = change.Kind
	}
	if applied.Name == "" {
		applied.Name = change.Name
	}
	if applied.LogicalID == "" {
		applied.LogicalID = change.LogicalID
	}
	if len(applied.Tags) == 0 {
		applied.Tags = change.Tags
	}
	record := schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical: schema.ResourceLogicalRef{
			Kind: change.Kind,
			Name: firstNonEmpty(applied.LogicalID, change.LogicalID, applied.Name, change.Name),
		},
		Provider: schema.ResourceProviderRef{
			Provider: Name,
			Kind:     applied.Kind,
			ID:       applied.ProviderID,
		},
		Service:    plan.Service,
		Env:        plan.Env,
		Tags:       cloneTags(applied.Tags),
		ObservedAt: canonical.Time(time.Now().UTC()),
	}
	body, err := canonical.Marshal(record)
	if err != nil {
		return err
	}
	logicalKey, err := paths.LogicalResource(change.Kind, pathSafeResourceName(record.Logical.Name))
	if err != nil {
		return err
	}
	providerKey, err := paths.ProviderResource(Name, applied.Kind, applied.ProviderID)
	if err != nil {
		return err
	}
	if err := upsertResourceRecord(ctx, p.stateStore, logicalKey, body, record); err != nil {
		return err
	}
	return upsertResourceRecord(ctx, p.stateStore, providerKey, body, record)
}

func upsertResourceRecord(ctx context.Context, store objstore.ObjectStore, key string, body []byte, record schema.ResourceRecord) error {
	opts := objstore.PutOptions{
		ContentType: canonical.ContentType,
		Metadata: map[string]string{
			"schema_version": record.SchemaVersion,
			"provider":       record.Provider.Provider,
			"provider_kind":  record.Provider.Kind,
			"provider_id":    record.Provider.ID,
			"logical_kind":   record.Logical.Kind,
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

func retryProviderCall(ctx context.Context, op string, fn func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			return nil
		}
		classified := ClassifyError(op, err)
		if !isProviderCode(classified, provider.CodeThrottled) || attempt == 2 {
			return classified
		}
		if sleepErr := sleepProviderBackoff(ctx, attempt); sleepErr != nil {
			return sleepErr
		}
	}
	return ClassifyError(op, err)
}

func isProviderCode(err error, code provider.ErrorCode) bool {
	var providerErr *provider.Error
	return errors.As(err, &providerErr) && providerErr.Code == code
}

func sleepProviderBackoff(ctx context.Context, attempt int) error {
	delay := time.Duration(10*(1<<attempt)) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validatePlanForApply(plan *provider.Plan) error {
	if plan == nil {
		return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: "plan is required"}
	}
	if plan.Provider != Name {
		return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: fmt.Sprintf("plan provider must be %q", Name)}
	}
	if strings.TrimSpace(plan.Service) == "" || strings.TrimSpace(plan.Env) == "" {
		return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: "plan service and env are required"}
	}
	return nil
}
