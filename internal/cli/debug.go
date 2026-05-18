package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	debugbundle "github.com/s1liconcow/skiff/internal/debug"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type debugCollectOutput struct {
	OK      bool               `json:"ok"`
	TraceID string             `json:"trace_id,omitempty"`
	Bundle  debugbundle.Bundle `json:"bundle"`
}

type debugSessionOutput struct {
	OK              bool                  `json:"ok"`
	TraceID         string                `json:"trace_id,omitempty"`
	PreflightBundle debugbundle.Bundle    `json:"preflight_bundle"`
	Session         provider.DebugSession `json:"session"`
}

var newDebugProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
	return newCLIProvider(cfg, store)
}

func runDebug(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printDebugUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "collect":
		return runDebugCollect(binary, args[1:], root, stdout, stderr)
	case "command":
		return runDebugSession(binary, "command", args[1:], root, provider.DebugModeCommand, stdout, stderr)
	case "port-forward":
		return runDebugSession(binary, "port-forward", args[1:], root, provider.DebugModePortForward, stdout, stderr)
	case "shell":
		return runDebugSession(binary, "shell", args[1:], root, provider.DebugModeShell, stdout, stderr)
	case "help", "-h", "--help":
		printDebugUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s debug: unknown command %q\n", binary, args[0])
		printDebugUsage(stderr, binary)
		return ExitUserError
	}
}

func runDebugSession(binary, command string, args []string, root rootOptions, mode provider.DebugMode, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" debug "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	instanceID := fs.String("instance", "", "provider instance ID")
	reason := fs.String("reason", "debug "+command+" session", "reason recorded in audit")
	approvalID := fs.String("approval-id", "", "approval context ID for production debug access")
	actorID := fs.String("actor", "skiff-cli", "actor ID")
	actorType := fs.String("actor-type", "user", "actor type")
	localPort := fs.Int("local", 0, "local port for port-forward")
	remotePort := fs.Int("remote", 0, "remote port for port-forward")
	execCommand := fs.String("exec", "", "command to run for debug command mode")

	flagArgs, positionals, err := splitDebugSessionArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "debug "+command, root.Format, root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if strings.TrimSpace(*service) == "" {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	debugCommand := []string(nil)
	if mode == provider.DebugModePortForward && (*localPort <= 0 || *remotePort <= 0) {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, errors.New("--local and --remote are required for port-forward"), stdout, stderr)
	}
	if mode == provider.DebugModeCommand {
		if strings.TrimSpace(*execCommand) == "" {
			return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, errors.New("--exec is required for command mode"), stdout, stderr)
		}
		debugCommand = []string{*execCommand}
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, errors.New("debug sessions currently require --direct mode so audit records are durable"), stdout, stderr)
	}
	actor := schema.Actor{ID: *actorID, Type: *actorType}
	decision, err := authz.MustAuthorize(nilContext(), authz.DefaultPolicy{}, authz.Request{
		Actor:      actor,
		Action:     authz.ActionDebug,
		Target:     schema.Target{Kind: "service", Name: *service},
		Env:        loaded.Config.Env,
		Service:    *service,
		Risk:       schema.RiskHigh,
		ApprovalID: *approvalID,
		TraceID:    *flags.traceID,
	})
	if err != nil {
		return writeDebugDenied(binary, *flags.format, *flags.traceID, decision, stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	cloud, err := newDebugProvider(loaded.Config, store)
	if err != nil {
		return writeClientError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	preflight, err := (debugbundle.Collector{Store: store, Provider: cloud}).Collect(nilContext(), debugbundle.Request{
		Service:    *service,
		Env:        loaded.Config.Env,
		InstanceID: *instanceID,
		Reason:     "preflight bundle before " + command,
		TraceID:    *flags.traceID,
		Actor:      actor,
	})
	if err != nil {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := writeDebugAudit(nilContext(), store, *preflight, actor, "preflight bundle before "+command, *approvalID); err != nil {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	session, err := cloud.Debug(nilContext(), provider.DebugRequest{
		Service:    *service,
		Env:        loaded.Config.Env,
		InstanceID: *instanceID,
		Mode:       mode,
		Command:    debugCommand,
		LocalPort:  *localPort,
		RemotePort: *remotePort,
		Reason:     *reason,
	})
	if err != nil {
		return writeClientError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := writeDebugSessionAudit(nilContext(), store, *session, *service, loaded.Config.Env, *flags.traceID, actor, *reason, *approvalID); err != nil {
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "debug %s session %s started for %s/%s\n", command, session.ID, *service, loaded.Config.Env)
		fmt.Fprintf(stdout, "preflight_bundle: %s\n", preflight.BundleID)
		if session.ConnectionHint != "" {
			fmt.Fprintf(stdout, "connection: %s\n", session.ConnectionHint)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(debugSessionOutput{OK: true, TraceID: *flags.traceID, PreflightBundle: *preflight, Session: *session}); err != nil {
			fmt.Fprintf(stderr, "%s debug %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "debug "+command, *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runDebugCollect(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" debug collect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	instanceID := fs.String("instance", "", "provider instance ID")
	reason := fs.String("reason", "diagnostic bundle collection", "reason recorded in audit")
	outPath := fs.String("out", "", "write bundle JSON to this path")
	approvalID := fs.String("approval-id", "", "approval context ID for production debug access")
	actorID := fs.String("actor", "skiff-cli", "actor ID")
	actorType := fs.String("actor-type", "user", "actor type")

	flagArgs, positionals, err := splitDebugCollectArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if strings.TrimSpace(*service) == "" {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, errors.New("debug collect currently requires --direct mode so audit records are durable"), stdout, stderr)
	}
	actor := schema.Actor{ID: *actorID, Type: *actorType}
	decision, err := authz.MustAuthorize(nilContext(), authz.DefaultPolicy{}, authz.Request{
		Actor:      actor,
		Action:     authz.ActionDebug,
		Target:     schema.Target{Kind: "service", Name: *service},
		Env:        loaded.Config.Env,
		Service:    *service,
		Risk:       schema.RiskHigh,
		ApprovalID: *approvalID,
		TraceID:    *flags.traceID,
	})
	if err != nil {
		return writeDebugDenied(binary, *flags.format, *flags.traceID, decision, stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	cloud, err := newDebugProvider(loaded.Config, store)
	if err != nil {
		return writeClientError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	bundle, err := (debugbundle.Collector{Store: store, Provider: cloud}).Collect(nilContext(), debugbundle.Request{
		Service:    *service,
		Env:        loaded.Config.Env,
		InstanceID: *instanceID,
		Reason:     *reason,
		TraceID:    *flags.traceID,
		Actor:      actor,
	})
	if err != nil {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := writeDebugAudit(nilContext(), store, *bundle, actor, *reason, *approvalID); err != nil {
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if *outPath != "" {
		if err := writeDebugBundleFile(*outPath, *bundle); err != nil {
			return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, err, stdout, stderr)
		}
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "debug bundle %s collected for %s/%s\n", bundle.BundleID, bundle.Service, bundle.Env)
		if *outPath != "" {
			fmt.Fprintf(stdout, "path: %s\n", *outPath)
		}
		if len(bundle.Findings) > 0 {
			fmt.Fprintf(stdout, "findings: %d\n", len(bundle.Findings))
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(debugCollectOutput{OK: true, TraceID: *flags.traceID, Bundle: *bundle}); err != nil {
			fmt.Fprintf(stderr, "%s debug collect: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "debug collect", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeDebugAudit(ctx context.Context, store objstore.ObjectStore, bundle debugbundle.Bundle, actor schema.Actor, reason, approvalID string) error {
	log, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		return err
	}
	event := events.NewServiceEvent(bundle.Service, "debug.bundle_collected", "debug bundle "+bundle.BundleID+" collected", time.Now().UTC(), bundle.TraceID+bundle.BundleID+"debug-event")
	event.TraceID = bundle.TraceID
	event.Actor = &actor
	event.Facts = []schema.Fact{
		{Type: "bundle_id", Message: bundle.BundleID},
		{Type: "provider", Message: bundle.Provider},
	}
	if _, err := log.Append(ctx, event); err != nil {
		return err
	}
	audit := events.NewAuditRecord(actor, schema.Target{Kind: "service", Name: bundle.Service}, "debug.collect", "collected debug bundle "+bundle.BundleID, bundle.TraceID, time.Now().UTC(), bundle.BundleID+"debug-audit")
	audit.Risk = schema.RiskHigh
	audit.ApprovalID = approvalID
	audit.Data = rawJSON(map[string]string{"bundle_id": bundle.BundleID, "instance_id": bundle.InstanceID, "reason": reason})
	_, err = log.AppendAudit(ctx, audit)
	return err
}

func writeDebugSessionAudit(ctx context.Context, store objstore.ObjectStore, session provider.DebugSession, service, env, traceID string, actor schema.Actor, reason, approvalID string) error {
	log, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		return err
	}
	mode := string(session.Mode)
	if mode == "" {
		mode = "session"
	}
	event := events.NewServiceEvent(service, "debug.session_started", "debug "+mode+" session "+session.ID+" started", time.Now().UTC(), traceID+session.ID+"debug-session-event")
	event.TraceID = traceID
	event.Actor = &actor
	event.Facts = []schema.Fact{
		{Type: "session_id", Message: session.ID},
		{Type: "provider", Message: session.Provider},
		{Type: "mode", Message: mode},
	}
	if session.ProviderID != "" {
		event.Facts = append(event.Facts, schema.Fact{Type: "provider_id", Message: session.ProviderID})
	}
	if _, err := log.Append(ctx, event); err != nil {
		return err
	}
	data := map[string]string{
		"session_id":  session.ID,
		"provider":    session.Provider,
		"mode":        mode,
		"env":         env,
		"instance_id": session.InstanceID,
		"provider_id": session.ProviderID,
		"reason":      reason,
	}
	if session.LocalPort > 0 {
		data["local_port"] = strconv.Itoa(session.LocalPort)
	}
	if session.RemotePort > 0 {
		data["remote_port"] = strconv.Itoa(session.RemotePort)
	}
	record := events.NewAuditRecord(actor, schema.Target{Kind: "service", Name: service}, "debug."+mode, "started debug "+mode+" session "+session.ID, traceID, time.Now().UTC(), session.ID+"debug-session-audit")
	record.Risk = schema.RiskHigh
	record.ApprovalID = approvalID
	record.Data = rawJSON(data)
	_, err = log.AppendAudit(ctx, record)
	return err
}

func writeDebugBundleFile(path string, bundle debugbundle.Bundle) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("--out path is required")
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func writeDebugDenied(binary, format, traceID string, decision authz.Decision, stdout, stderr io.Writer) int {
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(commandErrorOutput{OK: false, Code: "POLICY_DENIED", Summary: strings.Join(decision.Denials, "; "), TraceID: traceID})
		return ExitPolicyDenied
	}
	fmt.Fprintf(stderr, "%s debug collect: %s\n", binary, strings.Join(decision.Denials, "; "))
	return ExitPolicyDenied
}

func splitDebugCollectArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"actor":        true,
		"actor-type":   true,
		"api":          false,
		"api-url":      true,
		"approval-id":  true,
		"config":       true,
		"direct":       false,
		"env":          true,
		"format":       true,
		"instance":     true,
		"mode":         true,
		"no-color":     false,
		"out":          true,
		"provider":     true,
		"reason":       true,
		"region":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
		"yes":          false,
	}
	return splitArgs(args, valueFlags)
}

func splitDebugSessionArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"actor":        true,
		"actor-type":   true,
		"api":          false,
		"api-url":      true,
		"approval-id":  true,
		"config":       true,
		"direct":       false,
		"env":          true,
		"exec":         true,
		"format":       true,
		"instance":     true,
		"local":        true,
		"mode":         true,
		"no-color":     false,
		"provider":     true,
		"reason":       true,
		"region":       true,
		"remote":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
		"yes":          false,
	}
	return splitArgs(args, valueFlags)
}

func printDebugUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s debug collect <service> [flags]\n", binary)
	fmt.Fprintf(w, "       %s debug shell <service> --instance <id> [flags]\n", binary)
	fmt.Fprintf(w, "       %s debug port-forward <service> --instance <id> --remote <port> --local <port> [flags]\n", binary)
	fmt.Fprintf(w, "       %s debug command <service> --instance <id> --exec <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  collect   Collect a redacted diagnostic bundle")
	fmt.Fprintln(w, "  shell     Start a scoped provider debug shell after bundle preflight")
	fmt.Fprintln(w, "  port-forward")
	fmt.Fprintln(w, "            Start a scoped provider port forward after bundle preflight")
	fmt.Fprintln(w, "  command   Run a scoped provider debug command after bundle preflight")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Collect flags:")
	fmt.Fprintln(w, "  --instance <provider-instance-id>")
	fmt.Fprintln(w, "  --reason <summary>")
	fmt.Fprintln(w, "  --approval-id <id>")
	fmt.Fprintln(w, "  --out <path>")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Session flags:")
	fmt.Fprintln(w, "  --instance <provider-instance-id>")
	fmt.Fprintln(w, "  --remote <port> --local <port>")
	fmt.Fprintln(w, "  --exec <command>")
	fmt.Fprintln(w, "  --reason <summary>")
	fmt.Fprintln(w, "  --approval-id <id>")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
}
