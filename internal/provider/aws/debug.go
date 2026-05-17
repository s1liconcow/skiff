package aws

import (
	"context"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
)

type DebugSessionClient interface {
	StartDebugSession(ctx context.Context, req DebugSessionRequest) (*DebugSessionResult, error)
}

type DebugSessionRequest struct {
	Service    string             `json:"service"`
	Env        string             `json:"env"`
	InstanceID string             `json:"instance_id,omitempty"`
	Mode       provider.DebugMode `json:"mode"`
	Command    []string           `json:"command,omitempty"`
	LocalPort  int                `json:"local_port,omitempty"`
	RemotePort int                `json:"remote_port,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	Region     string             `json:"region,omitempty"`
}

type DebugSessionResult struct {
	ID             string    `json:"id"`
	InstanceID     string    `json:"instance_id,omitempty"`
	ProviderID     string    `json:"provider_id,omitempty"`
	ConnectionHint string    `json:"connection_hint,omitempty"`
	StartedAt      time.Time `json:"started_at"`
}

func (p *Provider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "debug", Summary: "service and env are required"}
	}
	mode := req.Mode
	if mode == "" {
		mode = provider.DebugModeBundle
	}
	if p.clients.DebugSessions == nil {
		return nil, provider.Unsupported(Name, "debug "+string(mode))
	}
	result, err := p.clients.DebugSessions.StartDebugSession(ctx, DebugSessionRequest{
		Service:    req.Service,
		Env:        req.Env,
		InstanceID: req.InstanceID,
		Mode:       mode,
		Command:    append([]string(nil), req.Command...),
		LocalPort:  req.LocalPort,
		RemotePort: req.RemotePort,
		Reason:     req.Reason,
		Region:     p.cfg.Region,
	})
	if err != nil {
		return nil, ClassifyError("debug", err)
	}
	started := result.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	return &provider.DebugSession{
		ID:             result.ID,
		Provider:       Name,
		Mode:           mode,
		InstanceID:     firstNonEmpty(result.InstanceID, req.InstanceID),
		ProviderID:     result.ProviderID,
		Command:        append([]string(nil), req.Command...),
		LocalPort:      req.LocalPort,
		RemotePort:     req.RemotePort,
		ConnectionHint: firstNonEmpty(result.ConnectionHint, "aws ssm session manager"),
		StartedAt:      started.UTC(),
	}, nil
}
