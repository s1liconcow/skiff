package applecontainer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/artifact"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const Name = "apple-container"

type Option func(*options)

type options struct {
	fakeOpts       []fakeprovider.Option
	store          objstore.ObjectStore
	containerCLI   string
	caddyContainer string
	caddyURL       string
	caddyImage     string
	workloadRoot   string
	clock          func() time.Time
}

type Provider struct {
	*fakeprovider.Provider
	store          objstore.ObjectStore
	containerCLI   string
	caddyContainer string
	caddyURL       string
	caddyImage     string
	workloadRoot   string
	clock          func() time.Time
}

func New(opts ...Option) *Provider {
	cfg := options{
		containerCLI:   "container",
		caddyContainer: strings.TrimSpace(os.Getenv("SKIFF_APPLE_CADDY_CONTAINER")),
		caddyURL:       strings.TrimSpace(os.Getenv("SKIFF_APPLE_CADDY_URL")),
		caddyImage:     strings.TrimSpace(os.Getenv("SKIFF_APPLE_CADDY_IMAGE")),
		workloadRoot:   strings.TrimSpace(os.Getenv("SKIFF_APPLE_WORKLOAD_ROOT")),
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
		store:          cfg.store,
		containerCLI:   cfg.containerCLI,
		caddyContainer: cfg.caddyContainer,
		caddyURL:       cfg.caddyURL,
		caddyImage:     cfg.caddyImage,
		workloadRoot:   cfg.workloadRoot,
		clock:          cfg.clock,
	}
}

func WithStateStore(store objstore.ObjectStore) Option {
	return func(o *options) {
		o.store = store
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

func WithCaddyURL(value string) Option {
	return func(o *options) {
		o.caddyURL = strings.TrimSpace(value)
	}
}

func WithCaddyImage(value string) Option {
	return func(o *options) {
		o.caddyImage = strings.TrimSpace(value)
	}
}

func WithWorkloadRoot(value string) Option {
	return func(o *options) {
		o.workloadRoot = strings.TrimSpace(value)
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

func (p *Provider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	rollout, err := p.Provider.StartRollout(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := p.restartCaddyForRelease(ctx, req); err != nil {
		return nil, err
	}
	return rollout, nil
}

func (p *Provider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	if p.shouldInspectCaddyTarget(ctx, ref) {
		inspection := &provider.ResourceInspection{
			Kind:       firstNonEmpty(ref.Kind, "target-group"),
			LogicalID:  ref.LogicalID,
			Name:       ref.Name,
			ProviderID: firstNonEmpty(ref.ProviderID, p.caddyContainer),
		}
		if stored, err := p.Provider.InspectResource(ctx, ref); err == nil && stored != nil {
			copy := *stored
			inspection = &copy
		}
		inspection.Status = p.caddyHealthStatus(ctx)
		return inspection, nil
	}
	return p.Provider.InspectResource(ctx, ref)
}

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

func (p *Provider) restartCaddyForRelease(ctx context.Context, req provider.RolloutRequest) error {
	if p.store == nil || p.caddyContainer == "" || p.caddyImage == "" || p.caddyURL == "" || p.workloadRoot == "" {
		return nil
	}
	releaseManifest, runtimeManifest, err := p.readRelease(ctx, req.Service, req.ReleaseID)
	if err != nil {
		return err
	}
	if releaseManifest.Artifact.Type != artifact.TypeTarball {
		return nil
	}
	prepared, err := (artifact.Preparer{RootDir: p.workloadRoot}).Prepare(ctx, artifact.Request{
		Service:         req.Service,
		Env:             req.Env,
		ReleaseID:       req.ReleaseID,
		Artifact:        releaseManifest.Artifact,
		RuntimeManifest: runtimeManifest,
	})
	if err != nil {
		return err
	}
	hostPort, err := p.caddyHostPort()
	if err != nil {
		return err
	}
	_, _ = p.container(ctx, "stop", "--time", "2", p.caddyContainer)
	_, _ = p.container(ctx, "delete", "--force", p.caddyContainer)
	_, err = p.container(ctx,
		"run",
		"--name", p.caddyContainer,
		"--detach",
		"-p", hostPort+":80",
		"-v", filepath.Clean(prepared.WorkingDirectory)+":/srv",
		p.caddyImage,
		"caddy", "file-server", "--root", "/srv", "--listen", ":80",
	)
	return err
}

func (p *Provider) readRelease(ctx context.Context, service, releaseID string) (schema.ReleaseManifest, schema.RuntimeManifest, error) {
	releaseKey, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	releaseObj, err := p.store.Get(ctx, releaseKey)
	if err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	var releaseManifest schema.ReleaseManifest
	if err := canonical.UnmarshalStrict(releaseObj.Body, &releaseManifest); err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	runtimeKey := releaseManifest.RuntimeManifestKey
	if runtimeKey == "" {
		runtimeKey, err = paths.RuntimeManifest(service, releaseID)
		if err != nil {
			return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
		}
	}
	runtimeObj, err := p.store.Get(ctx, runtimeKey)
	if err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	var runtimeManifest schema.RuntimeManifest
	if err := canonical.UnmarshalStrict(runtimeObj.Body, &runtimeManifest); err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	return releaseManifest, runtimeManifest, nil
}

func (p *Provider) caddyHostPort() (string, error) {
	parsed, err := url.Parse(p.caddyURL)
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("Apple Container Caddy URL %q has no host", p.caddyURL)
	}
	return parsed.Host, nil
}

func (p *Provider) shouldInspectCaddyTarget(ctx context.Context, ref provider.ResourceRef) bool {
	if p.caddyURL == "" {
		return false
	}
	kind := strings.TrimSpace(ref.Kind)
	if kind != "" && !strings.EqualFold(kind, "target-group") {
		return false
	}
	if p.store == nil || strings.TrimSpace(ref.Service) == "" {
		return true
	}
	control, err := p.readServiceControl(ctx, ref.Service)
	if err != nil || control.DesiredRelease == "" {
		return true
	}
	releaseManifest, _, err := p.readRelease(ctx, ref.Service, control.DesiredRelease)
	if err != nil {
		return true
	}
	return releaseManifest.Artifact.Type == artifact.TypeTarball
}

func (p *Provider) readServiceControl(ctx context.Context, service string) (schema.ServiceControl, error) {
	key, err := paths.ServiceControl(service)
	if err != nil {
		return schema.ServiceControl{}, err
	}
	obj, err := p.store.Get(ctx, key)
	if err != nil {
		return schema.ServiceControl{}, err
	}
	var control schema.ServiceControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return schema.ServiceControl{}, err
	}
	return control, nil
}

func (p *Provider) caddyHealthStatus(ctx context.Context) string {
	endpoint, err := url.Parse(p.caddyURL)
	if err != nil {
		return "unhealthy"
	}
	endpoint.Path = "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return "unhealthy"
		}
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return "healthy"
			}
			return "unhealthy"
		}
		if time.Now().After(deadline) {
			return "unhealthy"
		}
		select {
		case <-ctx.Done():
			return "unhealthy"
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (p *Provider) container(ctx context.Context, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, p.containerCLI, args...).CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return output, err
		}
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
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
