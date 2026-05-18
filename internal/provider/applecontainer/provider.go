package applecontainer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
)

const Name = "apple-container"

type Option func(*options)

type options struct {
	fakeOpts       []fakeprovider.Option
	containerCLI   string
	caddyContainer string
	clock          func() time.Time
}

type Provider struct {
	*fakeprovider.Provider
	containerCLI   string
	caddyContainer string
	clock          func() time.Time
}

func New(opts ...Option) *Provider {
	cfg := options{
		containerCLI:   "container",
		caddyContainer: strings.TrimSpace(os.Getenv("SKIFF_APPLE_CADDY_CONTAINER")),
		clock:          func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.clock != nil {
		cfg.fakeOpts = append(cfg.fakeOpts, fakeprovider.WithClock(cfg.clock))
	}
	return &Provider{
		Provider:       fakeprovider.New(cfg.fakeOpts...),
		containerCLI:   cfg.containerCLI,
		caddyContainer: cfg.caddyContainer,
		clock:          cfg.clock,
	}
}

func WithStateStore(store objstore.ObjectStore) Option {
	return func(o *options) {
		o.fakeOpts = append(o.fakeOpts, fakeprovider.WithStateStore(store))
	}
}

func WithContainerCLI(path string) Option {
	return func(o *options) {
		if strings.TrimSpace(path) != "" {
			o.containerCLI = strings.TrimSpace(path)
		}
	}
}

func WithCaddyContainer(name string) Option {
	return func(o *options) {
		o.caddyContainer = strings.TrimSpace(name)
	}
}

func WithClock(clock func() time.Time) Option {
	return func(o *options) {
		if clock != nil {
			o.clock = clock
		}
	}
}

func (p *Provider) Name() string { return Name }

func (p *Provider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "logs", Summary: "service and env are required"}
	}
	containerName := firstNonEmpty(req.InstanceID, req.ResourceID, p.caddyContainer)
	if containerName == "" {
		return nil, &provider.Error{
			Code:     provider.CodeInvalidConfig,
			Provider: Name,
			Op:       "logs",
			Summary:  "Apple Container logs require SKIFF_APPLE_CADDY_CONTAINER from the generated demo env file, or --instance <container-name>",
		}
	}
	output, err := exec.CommandContext(ctx, p.containerCLI, "logs", containerName).CombinedOutput()
	if err != nil {
		summary := fmt.Sprintf("container logs %s failed", containerName)
		if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
			summary += ": " + trimmed
		} else {
			summary += ": " + err.Error()
		}
		return nil, &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: "logs", Resource: containerName, Summary: summary, Cause: err}
	}
	return &provider.LogsResult{Entries: p.parseLogEntries(string(output), containerName, req)}, nil
}

func (p *Provider) parseLogEntries(output, source string, req provider.LogsRequest) []provider.LogEntry {
	var entries []provider.LogEntry
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		timestamp := p.clock().UTC()
		if parsed, rest, ok := parseTimestampPrefix(line); ok {
			timestamp = parsed
			line = rest
		}
		if !req.Since.IsZero() && timestamp.Before(req.Since) {
			continue
		}
		entries = append(entries, provider.LogEntry{
			Timestamp: timestamp,
			Message:   line,
			Source:    source,
			Fields: map[string]string{
				"service":   req.Service,
				"env":       req.Env,
				"provider":  Name,
				"container": source,
			},
		})
	}
	if req.Limit > 0 && len(entries) > req.Limit {
		entries = entries[len(entries)-req.Limit:]
	}
	return entries
}

func parseTimestampPrefix(line string) (time.Time, string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return time.Time{}, line, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, fields[0])
		if err == nil {
			return parsed.UTC(), strings.TrimSpace(strings.TrimPrefix(line, fields[0])), true
		}
	}
	return time.Time{}, line, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
