package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

func TestDebugCollectWritesBundleAuditAndEvents(t *testing.T) {
	clearSkiffEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	const (
		service   = "http-hello"
		env       = "prod"
		releaseID = "rel_debug_01"
		traceID   = "tr_debug_collect"
	)
	seedStatelessService(t, store, service, env, releaseID)

	outPath := filepath.Join(t.TempDir(), "bundles", "debug.json")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"debug", "collect", service,
		"--direct", "--state", "file://" + dir, "--env", env, "--provider", "fake", "--region", "local",
		"--instance", "i-debug123",
		"--reason", "investigate incident",
		"--approval-id", "approval_debug_collect",
		"--out", outPath,
		"--format", "json",
		"--trace-id", traceID,
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("debug collect exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("debug collect wrote stderr: %s", stderr.String())
	}
	var got debugCollectOutput
	decodeJSON(t, stdout.Bytes(), &got)
	if !got.OK || got.TraceID != traceID || !got.Bundle.OK || got.Bundle.BundleID == "" {
		t.Fatalf("unexpected debug output: %+v", got)
	}
	if got.Bundle.Provider != "fake" || got.Bundle.DebugSession == nil || got.Bundle.DebugSession.ID == "" || string(got.Bundle.DebugSession.Mode) != "bundle" {
		t.Fatalf("debug session missing provider identity: %+v", got.Bundle.DebugSession)
	}
	if got.Bundle.Service != service || got.Bundle.Env != env || got.Bundle.InstanceID != "i-debug123" {
		t.Fatalf("bundle target mismatch: %+v", got.Bundle)
	}
	if got.Bundle.ReleaseID != releaseID || !strings.HasPrefix(got.Bundle.ReleaseDigest, "sha256:") {
		t.Fatalf("bundle did not include release evidence: %+v", got.Bundle)
	}
	if got.Bundle.ServiceControl == nil || len(got.Bundle.Logs) == 0 || len(got.Bundle.Metrics) == 0 {
		t.Fatalf("bundle missing core diagnostics: %+v", got.Bundle)
	}
	if !containsString(got.Bundle.Redactions, "secret values omitted") || len(got.Bundle.NextCommands) == 0 {
		t.Fatalf("bundle missing redactions or continuation commands: %+v", got.Bundle)
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read bundle out path: %v", err)
	}
	var fromFile map[string]any
	if err := json.Unmarshal(body, &fromFile); err != nil {
		t.Fatalf("bundle file is not JSON: %v\n%s", err, string(body))
	}
	if fromFile["bundle_id"] != got.Bundle.BundleID {
		t.Fatalf("bundle file ID = %v, want %s", fromFile["bundle_id"], got.Bundle.BundleID)
	}

	eventLog, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		t.Fatalf("new event log: %v", err)
	}
	serviceEvents, err := eventLog.List(ctx, events.Scope{Kind: events.ScopeService, Service: service}, events.ListOptions{})
	if err != nil {
		t.Fatalf("list service events: %v", err)
	}
	if !hasEventType(serviceEvents, "debug.bundle_collected") {
		t.Fatalf("debug event missing from service stream: %+v", serviceEvents)
	}

	audits, err := readAuditRecords(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAuditAction(audits, "debug.collect", "approval_debug_collect") {
		t.Fatalf("debug audit record missing: %+v", audits)
	}
}

func TestDebugCollectProdRequiresApproval(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedStatelessService(t, store, "http-hello", "prod", "rel_debug_denied")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"debug", "collect", "http-hello",
		"--direct", "--state", "file://" + dir, "--env", "prod", "--provider", "fake", "--region", "local",
		"--format", "json",
		"--trace-id", "tr_debug_denied",
	}, &stdout, &stderr)
	if code != ExitPolicyDenied {
		t.Fatalf("debug collect exit=%d, want %d stderr=%s stdout=%s", code, ExitPolicyDenied, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("policy denial in json mode wrote stderr: %s", stderr.String())
	}
	var got commandErrorOutput
	decodeJSON(t, stdout.Bytes(), &got)
	if got.OK || got.Code != "POLICY_DENIED" || !strings.Contains(got.Summary, "approval required") {
		t.Fatalf("unexpected denial output: %+v", got)
	}
}

func TestDebugShellRunsBundlePreflightAndAuditsSession(t *testing.T) {
	clearSkiffEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedStatelessService(t, store, "http-hello", "prod", "rel_debug_shell")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"debug", "shell", "http-hello",
		"--direct", "--state", "file://" + dir, "--env", "prod", "--provider", "fake", "--region", "local",
		"--instance", "i-shell123",
		"--approval-id", "approval_debug_shell",
		"--format", "json",
		"--trace-id", "tr_debug_shell",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("debug shell exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got debugSessionOutput
	decodeJSON(t, stdout.Bytes(), &got)
	if !got.OK || !got.PreflightBundle.OK || got.PreflightBundle.BundleID == "" || got.Session.ID == "" {
		t.Fatalf("unexpected debug shell output: %+v", got)
	}
	if got.Session.Mode != "shell" || got.Session.InstanceID != "i-shell123" || got.Session.ConnectionHint == "" {
		t.Fatalf("session metadata missing: %+v", got.Session)
	}

	serviceEvents, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		t.Fatalf("new event log: %v", err)
	}
	entries, err := serviceEvents.List(ctx, events.Scope{Kind: events.ScopeService, Service: "http-hello"}, events.ListOptions{})
	if err != nil {
		t.Fatalf("list service events: %v", err)
	}
	if !hasEventType(entries, "debug.bundle_collected") || !hasEventType(entries, "debug.session_started") {
		t.Fatalf("debug shell did not write both events: %+v", entries)
	}
	audits, err := readAuditRecords(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAuditAction(audits, "debug.collect", "approval_debug_shell") || !hasAuditAction(audits, "debug.shell", "approval_debug_shell") {
		t.Fatalf("debug shell audits missing: %+v", audits)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasEventType(events []events.Event, want string) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

func readAuditRecords(ctx context.Context, store objstore.ObjectStore) ([]events.AuditRecord, error) {
	metas, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]events.AuditRecord, 0, len(metas))
	for _, meta := range metas {
		object, err := store.Get(ctx, meta.Key)
		if err != nil {
			return nil, err
		}
		var record events.AuditRecord
		if err := canonical.UnmarshalStrict(object.Body, &record); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func hasAuditAction(records []events.AuditRecord, action, approvalID string) bool {
	for _, record := range records {
		if record.Action == action && record.ApprovalID == approvalID {
			return true
		}
	}
	return false
}
