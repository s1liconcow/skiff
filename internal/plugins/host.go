package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type Runner interface {
	RunPluginHook(ctx context.Context, plugin Plugin, hook pluginapi.Hook, request any, response any) error
}

type CommandRunner struct {
	Timeout time.Duration
}

type Host struct {
	Registry *Registry
	Runner   Runner
}

func NewHost(registry *Registry, runner Runner) *Host {
	if runner == nil {
		runner = CommandRunner{Timeout: 30 * time.Second}
	}
	return &Host{Registry: registry, Runner: runner}
}

func (h *Host) Validate(ctx context.Context, spec json.RawMessage, traceID string) ([]PatchSet, error) {
	var out []PatchSet
	for _, plugin := range h.Registry.Hooks(pluginapi.HookValidate) {
		if plugin.Manifest.Runtime.Kind != pluginapi.RuntimeCommand {
			continue
		}
		var response pluginapi.ValidateResponse
		err := h.Runner.RunPluginHook(ctx, plugin, pluginapi.HookValidate, pluginapi.ValidateRequest{
			Manifest: plugin.Manifest,
			Spec:     spec,
			TraceID:  traceID,
		}, &response)
		if err != nil {
			return nil, err
		}
		out = append(out, PatchSet{
			Plugin:      plugin.Manifest.Name,
			Version:     plugin.Manifest.Version,
			Source:      plugin.Source,
			Diagnostics: response.Diagnostics,
		})
	}
	return out, nil
}

func (h *Host) MutateIR(ctx context.Context, graph *ir.Graph, spec json.RawMessage, traceID string) ([]PatchSet, error) {
	if graph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	graphBody, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	var out []PatchSet
	for _, plugin := range h.Registry.Hooks(pluginapi.HookMutateIR) {
		if plugin.Manifest.Runtime.Kind != pluginapi.RuntimeCommand {
			continue
		}
		var response pluginapi.MutateIRResponse
		err := h.Runner.RunPluginHook(ctx, plugin, pluginapi.HookMutateIR, pluginapi.MutateIRRequest{
			Manifest: plugin.Manifest,
			Graph:    graphBody,
			Spec:     spec,
			TraceID:  traceID,
		}, &response)
		if err != nil {
			return nil, err
		}
		patches, err := EnforcePatches(plugin, response.Patches)
		if err != nil {
			return nil, err
		}
		out = append(out, PatchSet{
			Plugin:      plugin.Manifest.Name,
			Version:     plugin.Manifest.Version,
			Source:      plugin.Source,
			Patches:     patches,
			Diagnostics: response.Diagnostics,
		})
	}
	return out, nil
}

func (h *Host) RuntimeAddons(ctx context.Context, graph *ir.Graph, traceID string) ([]pluginapi.RuntimeAddon, []pluginapi.Diagnostic, error) {
	if graph == nil {
		return nil, nil, fmt.Errorf("graph is required")
	}
	graphBody, err := json.Marshal(graph)
	if err != nil {
		return nil, nil, err
	}
	var addons []pluginapi.RuntimeAddon
	var diagnostics []pluginapi.Diagnostic
	for _, plugin := range h.Registry.Hooks(pluginapi.HookRuntimeAddons) {
		if !plugin.Manifest.Permissions.RuntimeAddons {
			return nil, nil, PermissionError{Plugin: plugin.Manifest.Name, Kind: "runtime_addons", Summary: "runtime addon permission is not declared"}
		}
		if plugin.Manifest.Runtime.Kind != pluginapi.RuntimeCommand {
			continue
		}
		var response pluginapi.RuntimeAddonsResponse
		err := h.Runner.RunPluginHook(ctx, plugin, pluginapi.HookRuntimeAddons, pluginapi.RuntimeAddonsRequest{
			Manifest: plugin.Manifest,
			Graph:    graphBody,
			TraceID:  traceID,
		}, &response)
		if err != nil {
			return nil, nil, err
		}
		addons = append(addons, response.Addons...)
		diagnostics = append(diagnostics, response.Diagnostics...)
	}
	return addons, diagnostics, nil
}

func (r CommandRunner) RunPluginHook(ctx context.Context, plugin Plugin, hook pluginapi.Hook, request any, response any) error {
	if plugin.Manifest.Runtime.Kind != pluginapi.RuntimeCommand {
		return fmt.Errorf("plugin %s does not use command runtime", plugin.Manifest.Name)
	}
	if len(plugin.Manifest.Runtime.Command) == 0 {
		return fmt.Errorf("plugin %s command runtime is missing command", plugin.Manifest.Name)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(struct {
		Hook    pluginapi.Hook `json:"hook"`
		Request any            `json:"request"`
	}{
		Hook:    hook,
		Request: request,
	})
	if err != nil {
		return err
	}
	command := plugin.Manifest.Runtime.Command
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("plugin %s hook %s timed out: %w", plugin.Manifest.Name, hook, ctx.Err())
		}
		return fmt.Errorf("plugin %s hook %s failed: %w: %s", plugin.Manifest.Name, hook, err, strings.TrimSpace(stderr.String()))
	}
	if response == nil {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), response); err != nil {
		return fmt.Errorf("plugin %s hook %s returned invalid JSON: %w", plugin.Manifest.Name, hook, err)
	}
	return nil
}
