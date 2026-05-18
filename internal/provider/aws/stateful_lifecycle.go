package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	StatefulOperationFenceInstance     = "ec2-terminate-instance"
	StatefulOperationDetachVolume      = "ebs-detach-volume"
	StatefulOperationLaunchReplacement = "ec2-run-instance"
	StatefulOperationAttachVolume      = "ebs-attach-volume"
	StatefulOperationUpdateDNS         = "route53-upsert-record"
	StatefulOperationSnapshotVolume    = "ebs-create-snapshot"
)

type Route53RecordUpdate struct {
	HostedZoneRef    string            `json:"hosted_zone_ref"`
	DNSName          string            `json:"dns_name"`
	RecordType       string            `json:"record_type"`
	TTLSeconds       int               `json:"ttl_seconds"`
	TargetInstanceID string            `json:"target_instance_id,omitempty"`
	Service          string            `json:"service,omitempty"`
	Env              string            `json:"env,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type Route53RecordChange struct {
	ChangeID     string    `json:"change_id"`
	HostedZoneID string    `json:"hosted_zone_id,omitempty"`
	DNSName      string    `json:"dns_name"`
	Status       string    `json:"status,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (p *Provider) FenceInstance(ctx context.Context, req provider.FenceInstanceRequest) (*provider.FenceInstanceResult, error) {
	if strings.TrimSpace(req.InstanceID) == "" {
		return nil, validationError("fence_instance", "instance_id is required")
	}
	var result *provider.FenceInstanceResult
	if err := retryProviderCall(ctx, "fence_stateful_instance", func() error {
		client := p.statefulOperations()
		if client == nil {
			return provider.Unsupported(Name, "stateful fence instance")
		}
		var err error
		result, err = client.FenceInstance(ctx, req)
		return err
	}); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, providerResultError("fence_stateful_instance")
	}
	ensureProviderOperation(&result.ProviderOperation, StatefulOperationFenceInstance, req.InstanceID, "terminated stateful instance")
	return result, nil
}

func (p *Provider) DetachVolume(ctx context.Context, req provider.DetachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	if strings.TrimSpace(req.VolumeID) == "" {
		return nil, validationError("detach_volume", "volume_id is required")
	}
	var result *provider.VolumeAttachmentResult
	if err := retryProviderCall(ctx, "detach_stateful_volume", func() error {
		client := p.statefulOperations()
		if client == nil {
			return provider.Unsupported(Name, "stateful detach volume")
		}
		var err error
		result, err = client.DetachVolume(ctx, req)
		return err
	}); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, providerResultError("detach_stateful_volume")
	}
	ensureProviderOperation(&result.ProviderOperation, StatefulOperationDetachVolume, req.VolumeID, "detached stateful volume")
	return result, nil
}

func (p *Provider) LaunchReplacement(ctx context.Context, req provider.LaunchReplacementRequest) (*provider.ReplacementInstance, error) {
	var result *provider.ReplacementInstance
	if err := retryProviderCall(ctx, "launch_stateful_replacement", func() error {
		client := p.statefulOperations()
		if client == nil {
			return provider.Unsupported(Name, "stateful launch replacement")
		}
		var err error
		result, err = client.LaunchReplacement(ctx, req)
		return err
	}); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, providerResultError("launch_stateful_replacement")
	}
	ensureProviderOperation(&result.ProviderOperation, StatefulOperationLaunchReplacement, result.InstanceID, "launched replacement stateful instance")
	return result, nil
}

func (p *Provider) AttachVolume(ctx context.Context, req provider.AttachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	if strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.VolumeID) == "" {
		return nil, validationError("attach_volume", "instance_id and volume_id are required")
	}
	var result *provider.VolumeAttachmentResult
	if err := retryProviderCall(ctx, "attach_stateful_volume", func() error {
		client := p.statefulOperations()
		if client == nil {
			return provider.Unsupported(Name, "stateful attach volume")
		}
		var err error
		result, err = client.AttachVolume(ctx, req)
		return err
	}); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, providerResultError("attach_stateful_volume")
	}
	ensureProviderOperation(&result.ProviderOperation, StatefulOperationAttachVolume, req.VolumeID+":"+req.InstanceID, "attached stateful volume")
	return result, nil
}

func (p *Provider) UpdateMemberDNS(ctx context.Context, req provider.UpdateMemberDNSRequest) (*provider.DNSUpdateResult, error) {
	if strings.TrimSpace(req.DNSName) == "" || strings.TrimSpace(req.InstanceID) == "" {
		return nil, validationError("update_member_dns", "dns_name and instance_id are required")
	}
	client := p.statefulOperations()
	if client != nil {
		var result *provider.DNSUpdateResult
		if err := retryProviderCall(ctx, "update_stateful_dns", func() error {
			var err error
			result, err = client.UpdateMemberDNS(ctx, req)
			return err
		}); err != nil {
			return nil, err
		}
		if result == nil {
			return nil, providerResultError("update_stateful_dns")
		}
		ensureProviderOperation(&result.ProviderOperation, StatefulOperationUpdateDNS, result.DNSName, "updated stateful DNS record")
		return result, nil
	}
	if p.clients.Route53 == nil {
		return nil, provider.Unsupported(Name, "stateful update dns")
	}
	var change *Route53RecordChange
	if err := retryProviderCall(ctx, "route53_upsert_stateful_dns", func() error {
		var err error
		change, err = p.clients.Route53.UpsertARecord(ctx, Route53RecordUpdate{
			DNSName:          req.DNSName,
			RecordType:       "A",
			TTLSeconds:       30,
			TargetInstanceID: req.InstanceID,
			Service:          req.Ref.Group,
			Env:              req.Ref.Env,
		})
		return err
	}); err != nil {
		return nil, err
	}
	if change == nil {
		return nil, providerResultError("route53_upsert_stateful_dns")
	}
	now := firstTime(change.UpdatedAt)
	return &provider.DNSUpdateResult{
		ProviderOperation: providerOperation(StatefulOperationUpdateDNS, firstNonEmpty(change.ChangeID, req.DNSName), "updated stateful DNS record", now),
		DNSName:           req.DNSName,
		UpdatedAt:         now,
	}, nil
}

func (p *Provider) SnapshotVolume(ctx context.Context, req provider.SnapshotVolumeRequest) (*provider.VolumeSnapshot, error) {
	if strings.TrimSpace(req.VolumeID) == "" {
		return nil, validationError("snapshot_volume", "volume_id is required")
	}
	var result *provider.VolumeSnapshot
	if err := retryProviderCall(ctx, "snapshot_stateful_volume", func() error {
		client := p.statefulOperations()
		if client == nil {
			return provider.Unsupported(Name, "stateful snapshot volume")
		}
		var err error
		result, err = client.SnapshotVolume(ctx, req)
		return err
	}); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, providerResultError("snapshot_stateful_volume")
	}
	ensureProviderOperation(&result.ProviderOperation, StatefulOperationSnapshotVolume, result.SnapshotID, "created stateful volume snapshot")
	return result, nil
}

func (p *Provider) statefulOperations() provider.StatefulOperations {
	if p.clients.StatefulOperations != nil {
		return p.clients.StatefulOperations
	}
	if p.clients.StatefulResources != nil {
		if ops, ok := p.clients.StatefulResources.(provider.StatefulOperations); ok {
			return ops
		}
	}
	return nil
}

func (p *Provider) applyStatefulResource(ctx context.Context, plan *provider.Plan, change provider.PlannedChange) (*AppliedResource, error) {
	if change.Kind == ResourceKindRoute53Record && p.clients.Route53 != nil {
		var record Route53Record
		if err := json.Unmarshal(change.Desired, &record); err != nil {
			return nil, err
		}
		routeChange, err := p.clients.Route53.UpsertARecord(ctx, Route53RecordUpdate{
			HostedZoneRef: record.HostedZoneRef,
			DNSName:       record.DNSName,
			RecordType:    firstNonEmpty(record.RecordType, "A"),
			TTLSeconds:    record.TTLSeconds,
			Service:       plan.Service,
			Env:           plan.Env,
			Tags:          cloneTags(change.Tags),
		})
		if err != nil {
			return nil, err
		}
		return &AppliedResource{
			Kind:        ResourceKindRoute53Record,
			LogicalID:   change.LogicalID,
			Name:        change.Name,
			ProviderID:  firstNonEmpty(routeChange.ChangeID, routeChange.DNSName, record.DNSName),
			Status:      firstNonEmpty(routeChange.Status, "upserted"),
			Tags:        cloneTags(change.Tags),
			Fingerprint: change.Fingerprint,
		}, nil
	}
	manager := p.clients.StatefulResources
	if manager == nil {
		if typed, ok := p.clients.ServiceResources.(StatefulResourceManager); ok {
			manager = typed
		}
	}
	if manager == nil {
		return nil, provider.Unsupported(Name, "apply "+change.Kind)
	}
	return manager.ApplyStatefulResource(ctx, desiredFromPlannedChange(change))
}

func providerOperation(kind, id, description string, observedAt time.Time) schema.ProviderOperationRef {
	return schema.ProviderOperationRef{
		Provider:    Name,
		Kind:        kind,
		ID:          id,
		ObservedAt:  canonical.Time(firstTime(observedAt)),
		Description: description,
	}
}

func ensureProviderOperation(ref *schema.ProviderOperationRef, kind, id, description string) {
	if ref == nil {
		return
	}
	if ref.Provider == "" {
		ref.Provider = Name
	}
	if ref.Kind == "" {
		ref.Kind = kind
	}
	if ref.ID == "" {
		ref.ID = id
	}
	if ref.Description == "" {
		ref.Description = description
	}
	if ref.ObservedAt == "" {
		ref.ObservedAt = canonical.Time(time.Now().UTC())
	}
}

func validationError(op, summary string) error {
	return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: op, Summary: summary}
}

func providerResultError(op string) error {
	return &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: op, Summary: "aws stateful client returned no result"}
}

func firstTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func statefulOperationID(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ":")
}

func statefulOperationDescription(action string, ref provider.StatefulMemberRef) string {
	if ref.Group == "" {
		return action
	}
	return fmt.Sprintf("%s for %s member %d", action, ref.Group, ref.Member)
}
