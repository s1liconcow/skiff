package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	DefaultUnitDir       = "/etc/systemd/system"
	DefaultRestartSec    = 5 * time.Second
	DefaultStopTimeout   = 30 * time.Second
	DefaultSystemctlPath = "systemctl"
)

type WorkloadUnitSpec struct {
	Service          string
	Env              string
	ReleaseID        string
	Command          []string
	EnvVars          map[string]string
	WorkingDirectory string
	User             string
	Group            string
	RestartSec       time.Duration
	StopTimeout      time.Duration
}

type SystemdManager interface {
	WriteUnit(ctx context.Context, name string, contents []byte) error
	DaemonReload(ctx context.Context) error
	StartUnit(ctx context.Context, name string) error
	RestartUnit(ctx context.Context, name string) error
	StopUnit(ctx context.Context, name string) error
}

type FileSystemdManager struct {
	UnitDir       string
	SystemctlPath string
}

func WorkloadUnitName(service, env string) string {
	return "skiff-" + unitNamePart(service) + "-" + unitNamePart(env) + ".service"
}

func RenderWorkloadUnit(spec WorkloadUnitSpec) (string, error) {
	if spec.Service == "" {
		return "", errors.New("service is required")
	}
	if spec.Env == "" {
		return "", errors.New("env is required")
	}
	if len(spec.Command) == 0 {
		return "", errors.New("command is required")
	}
	restartSec := spec.RestartSec
	if restartSec <= 0 {
		restartSec = DefaultRestartSec
	}
	stopTimeout := spec.StopTimeout
	if stopTimeout <= 0 {
		stopTimeout = DefaultStopTimeout
	}

	var buf bytes.Buffer
	fmt.Fprintln(&buf, "[Unit]")
	fmt.Fprintf(&buf, "Description=Skiff workload %s/%s release %s\n", spec.Env, spec.Service, spec.ReleaseID)
	fmt.Fprintln(&buf, "After=network-online.target")
	fmt.Fprintln(&buf, "Wants=network-online.target")
	fmt.Fprintln(&buf)
	fmt.Fprintln(&buf, "[Service]")
	fmt.Fprintln(&buf, "Type=simple")
	if spec.WorkingDirectory != "" {
		fmt.Fprintf(&buf, "WorkingDirectory=%s\n", systemdQuote(spec.WorkingDirectory))
	}
	if spec.User != "" {
		fmt.Fprintf(&buf, "User=%s\n", spec.User)
	}
	if spec.Group != "" {
		fmt.Fprintf(&buf, "Group=%s\n", spec.Group)
	}
	writeEnvironment(&buf, spec.EnvVars)
	fmt.Fprintf(&buf, "ExecStart=%s\n", quoteCommand(spec.Command))
	fmt.Fprintln(&buf, "Restart=always")
	fmt.Fprintf(&buf, "RestartSec=%s\n", formatSystemdDuration(restartSec))
	fmt.Fprintln(&buf, "KillSignal=SIGTERM")
	fmt.Fprintf(&buf, "TimeoutStopSec=%s\n", formatSystemdDuration(stopTimeout))
	fmt.Fprintln(&buf, "SyslogIdentifier="+WorkloadUnitName(spec.Service, spec.Env))
	fmt.Fprintln(&buf, "NoNewPrivileges=yes")
	fmt.Fprintln(&buf, "PrivateTmp=yes")
	fmt.Fprintln(&buf, "ProtectSystem=strict")
	fmt.Fprintln(&buf, "ProtectHome=yes")
	fmt.Fprintln(&buf, "CapabilityBoundingSet=")
	fmt.Fprintln(&buf, "RestrictSUIDSGID=yes")
	fmt.Fprintln(&buf, "LockPersonality=yes")
	fmt.Fprintln(&buf)
	fmt.Fprintln(&buf, "[Install]")
	fmt.Fprintln(&buf, "WantedBy=multi-user.target")
	return buf.String(), nil
}

func (m FileSystemdManager) WriteUnit(ctx context.Context, name string, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" {
		return errors.New("unit name is required")
	}
	dir := m.UnitDir
	if dir == "" {
		dir = DefaultUnitDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), contents, 0o644)
}

func (m FileSystemdManager) DaemonReload(ctx context.Context) error {
	return m.systemctl(ctx, "daemon-reload")
}

func (m FileSystemdManager) StartUnit(ctx context.Context, name string) error {
	return m.systemctl(ctx, "start", name)
}

func (m FileSystemdManager) RestartUnit(ctx context.Context, name string) error {
	return m.systemctl(ctx, "restart", name)
}

func (m FileSystemdManager) StopUnit(ctx context.Context, name string) error {
	return m.systemctl(ctx, "stop", name)
}

func (m FileSystemdManager) systemctl(ctx context.Context, args ...string) error {
	path := m.SystemctlPath
	if path == "" {
		path = DefaultSystemctlPath
	}
	cmd := exec.CommandContext(ctx, path, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func writeEnvironment(buf *bytes.Buffer, env map[string]string) {
	if len(env) == 0 {
		return
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprint(buf, "Environment=")
	for i, key := range keys {
		if i > 0 {
			fmt.Fprint(buf, " ")
		}
		fmt.Fprint(buf, systemdQuote(key+"="+env[key]))
	}
	fmt.Fprintln(buf)
}

func quoteCommand(command []string) string {
	parts := make([]string, 0, len(command))
	for _, arg := range command {
		parts = append(parts, systemdQuote(arg))
	}
	return strings.Join(parts, " ")
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return `"` + value + `"`
}

func formatSystemdDuration(duration time.Duration) string {
	if duration%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(duration/time.Second))
	}
	return duration.String()
}

func unitNamePart(value string) string {
	var buf strings.Builder
	for _, r := range value {
		if r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(r)
			continue
		}
		buf.WriteByte('-')
	}
	out := strings.Trim(buf.String(), "-._")
	if out == "" {
		return "workload"
	}
	return out
}
