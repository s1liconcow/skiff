package provider

import (
	"context"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

type StatefulOperations interface {
	FenceInstance(ctx context.Context, req FenceInstanceRequest) (*FenceInstanceResult, error)
	DetachVolume(ctx context.Context, req DetachVolumeRequest) (*VolumeAttachmentResult, error)
	LaunchReplacement(ctx context.Context, req LaunchReplacementRequest) (*ReplacementInstance, error)
	AttachVolume(ctx context.Context, req AttachVolumeRequest) (*VolumeAttachmentResult, error)
	UpdateMemberDNS(ctx context.Context, req UpdateMemberDNSRequest) (*DNSUpdateResult, error)
	SnapshotVolume(ctx context.Context, req SnapshotVolumeRequest) (*VolumeSnapshot, error)
}

type StatefulMemberRef struct {
	Group  string `json:"group"`
	Env    string `json:"env"`
	Member int    `json:"member"`
}

type FenceInstanceRequest struct {
	Ref        StatefulMemberRef `json:"ref"`
	InstanceID string            `json:"instance_id"`
	Reason     string            `json:"reason,omitempty"`
}

type FenceInstanceResult struct {
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
	FencedAt          time.Time                   `json:"fenced_at"`
}

type DetachVolumeRequest struct {
	Ref        StatefulMemberRef `json:"ref"`
	InstanceID string            `json:"instance_id,omitempty"`
	VolumeID   string            `json:"volume_id"`
}

type AttachVolumeRequest struct {
	Ref        StatefulMemberRef `json:"ref"`
	InstanceID string            `json:"instance_id"`
	VolumeID   string            `json:"volume_id"`
	Device     string            `json:"device,omitempty"`
}

type VolumeAttachmentResult struct {
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
	VolumeID          string                      `json:"volume_id"`
	InstanceID        string                      `json:"instance_id,omitempty"`
	CompletedAt       time.Time                   `json:"completed_at"`
}

type LaunchReplacementRequest struct {
	Ref          StatefulMemberRef `json:"ref"`
	Generation   int64             `json:"generation"`
	Zone         string            `json:"zone,omitempty"`
	PreviousID   string            `json:"previous_id,omitempty"`
	VolumeID     string            `json:"volume_id,omitempty"`
	IdentityHint string            `json:"identity_hint,omitempty"`
}

type ReplacementInstance struct {
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
	InstanceID        string                      `json:"instance_id"`
	Zone              string                      `json:"zone,omitempty"`
	LaunchedAt        time.Time                   `json:"launched_at"`
}

type UpdateMemberDNSRequest struct {
	Ref        StatefulMemberRef `json:"ref"`
	DNSName    string            `json:"dns_name"`
	InstanceID string            `json:"instance_id"`
}

type DNSUpdateResult struct {
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
	DNSName           string                      `json:"dns_name"`
	UpdatedAt         time.Time                   `json:"updated_at"`
}

type SnapshotVolumeRequest struct {
	Ref      StatefulMemberRef `json:"ref"`
	VolumeID string            `json:"volume_id"`
	Reason   string            `json:"reason,omitempty"`
}

type VolumeSnapshot struct {
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
	SnapshotID        string                      `json:"snapshot_id"`
	VolumeID          string                      `json:"volume_id"`
	CreatedAt         time.Time                   `json:"created_at"`
}
