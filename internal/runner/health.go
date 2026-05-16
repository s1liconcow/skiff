package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
)

type HealthResult struct {
	Status  HealthStatus `json:"status"`
	Summary string       `json:"summary,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type HealthChecker interface {
	Check(ctx context.Context, check *schema.HealthCheck) HealthResult
}

type CommandRunner interface {
	Run(ctx context.Context, command []string) error
}

type ProbeHealthChecker struct {
	HTTPClient    *http.Client
	CommandRunner CommandRunner
}

func (c ProbeHealthChecker) Check(ctx context.Context, check *schema.HealthCheck) HealthResult {
	if check == nil {
		return HealthResult{Status: HealthHealthy, Summary: "no health check configured"}
	}
	timeout := durationOrDefault(check.Timeout, 2*time.Second)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	switch strings.ToLower(check.Type) {
	case "http":
		return c.checkHTTP(ctx, check)
	case "command":
		return c.checkCommand(ctx, check)
	default:
		return HealthResult{
			Status:  HealthUnhealthy,
			Summary: fmt.Sprintf("unsupported health check type %q", check.Type),
		}
	}
}

func (c ProbeHealthChecker) checkHTTP(ctx context.Context, check *schema.HealthCheck) HealthResult {
	if check.Port <= 0 {
		return HealthResult{Status: HealthUnhealthy, Summary: "http health check port is required"}
	}
	path := check.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", check.Port, path), nil)
	if err != nil {
		return HealthResult{Status: HealthUnhealthy, Summary: "build http health check request", Error: err.Error()}
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return HealthResult{Status: HealthUnhealthy, Summary: "http health check failed", Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return HealthResult{Status: HealthHealthy, Summary: fmt.Sprintf("http health check returned %d", resp.StatusCode)}
	}
	return HealthResult{Status: HealthUnhealthy, Summary: fmt.Sprintf("http health check returned %d", resp.StatusCode)}
}

func (c ProbeHealthChecker) checkCommand(ctx context.Context, check *schema.HealthCheck) HealthResult {
	if len(check.Command) == 0 {
		return HealthResult{Status: HealthUnhealthy, Summary: "command health check command is required"}
	}
	runner := c.CommandRunner
	if runner == nil {
		runner = ExecCommandRunner{}
	}
	if err := runner.Run(ctx, check.Command); err != nil {
		return HealthResult{Status: HealthUnhealthy, Summary: "command health check failed", Error: err.Error()}
	}
	return HealthResult{Status: HealthHealthy, Summary: "command health check succeeded"}
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(ctx context.Context, command []string) error {
	if len(command) == 0 {
		return errors.New("command is required")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func durationOrDefault(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
