package applecontainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
)

func TestLogsReadAppleContainerOutput(t *testing.T) {
	dir := t.TempDir()
	container := filepath.Join(dir, "container")
	if err := os.WriteFile(container, []byte(`#!/usr/bin/env bash
if [[ "$1" != "logs" || "$2" != "skiff-demo-caddy" ]]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
printf '%s\n' '2026-05-18T01:25:40.044109Z caddy serving request'
printf '%s\n' 'plain caddy line'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 18, 1, 26, 0, 0, time.UTC)
	logs, err := New(
		WithContainerCLI(container),
		WithCaddyContainer("skiff-demo-caddy"),
		WithClock(func() time.Time { return now }),
	).Logs(context.Background(), provider.LogsRequest{
		Service: "caddy-web",
		Env:     "prod",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(logs.Entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(logs.Entries), logs.Entries)
	}
	if logs.Entries[0].Source != "skiff-demo-caddy" || logs.Entries[0].Timestamp.Format(time.RFC3339Nano) != "2026-05-18T01:25:40.044109Z" || logs.Entries[0].Message != "caddy serving request" {
		t.Fatalf("unexpected parsed entry: %+v", logs.Entries[0])
	}
	if logs.Entries[1].Timestamp != now || logs.Entries[1].Message != "plain caddy line" {
		t.Fatalf("unexpected fallback entry: %+v", logs.Entries[1])
	}
}

func TestLogsRequireGeneratedDemoContainer(t *testing.T) {
	_, err := New(WithCaddyContainer("")).Logs(context.Background(), provider.LogsRequest{Service: "caddy-web", Env: "prod"})
	if err == nil || !strings.Contains(err.Error(), "SKIFF_APPLE_CADDY_CONTAINER") {
		t.Fatalf("logs err = %v, want generated env hint", err)
	}
}
