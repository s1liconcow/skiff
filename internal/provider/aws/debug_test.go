package aws

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
)

func TestDebugUsesSSMClientAbstraction(t *testing.T) {
	client := &recordingDebugSessionClient{
		result: &DebugSessionResult{
			ID:             "ssm-session-123",
			InstanceID:     "i-abc123",
			ProviderID:     "i-abc123",
			ConnectionHint: "aws ssm start-session",
			StartedAt:      time.Date(2026, 5, 16, 22, 0, 0, 0, time.UTC),
		},
	}
	p, err := New(Config{Region: "us-west-2"}, WithClients(Clients{DebugSessions: client}))
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.Debug(context.Background(), provider.DebugRequest{
		Service:    "payments-api",
		Env:        "prod",
		InstanceID: "i-abc123",
		Mode:       provider.DebugModePortForward,
		LocalPort:  18080,
		RemotePort: 8080,
		Reason:     "incident response",
	})
	if err != nil {
		t.Fatalf("Debug: %v", err)
	}
	if session.ID != "ssm-session-123" || session.Provider != Name || session.Mode != provider.DebugModePortForward {
		t.Fatalf("unexpected session: %+v", session)
	}
	if session.ProviderID != "i-abc123" || session.LocalPort != 18080 || session.RemotePort != 8080 || session.ConnectionHint == "" {
		t.Fatalf("session missing SSM metadata: %+v", session)
	}
	if client.req.Region != "us-west-2" || client.req.Service != "payments-api" || client.req.Env != "prod" || client.req.InstanceID != "i-abc123" {
		t.Fatalf("request not propagated: %+v", client.req)
	}
	if client.req.Mode != provider.DebugModePortForward || client.req.LocalPort != 18080 || client.req.RemotePort != 8080 || client.req.Reason != "incident response" {
		t.Fatalf("debug options not propagated: %+v", client.req)
	}
}

func TestDebugCommandModePropagatesCommand(t *testing.T) {
	client := &recordingDebugSessionClient{result: &DebugSessionResult{ID: "ssm-command-123", StartedAt: time.Date(2026, 5, 16, 22, 0, 0, 0, time.UTC)}}
	p, err := New(Config{Region: "us-west-2"}, WithClients(Clients{DebugSessions: client}))
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.Debug(context.Background(), provider.DebugRequest{
		Service: "payments-api",
		Env:     "prod",
		Mode:    provider.DebugModeCommand,
		Command: []string{"systemctl", "status", "payments-api"},
	})
	if err != nil {
		t.Fatalf("Debug: %v", err)
	}
	if !reflect.DeepEqual(client.req.Command, []string{"systemctl", "status", "payments-api"}) {
		t.Fatalf("command not propagated: %+v", client.req)
	}
	if !reflect.DeepEqual(session.Command, []string{"systemctl", "status", "payments-api"}) {
		t.Fatalf("session command not preserved: %+v", session)
	}
}

type recordingDebugSessionClient struct {
	req    DebugSessionRequest
	result *DebugSessionResult
}

func (c *recordingDebugSessionClient) StartDebugSession(ctx context.Context, req DebugSessionRequest) (*DebugSessionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.req = req
	return c.result, nil
}
