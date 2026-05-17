package plugins

import (
	"context"
	"encoding/json"

	"github.com/s1liconcow/skiff/internal/doctor"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type DoctorHook struct {
	Host *Host
}

func (h DoctorHook) Check(ctx context.Context, req doctor.PluginRequest) ([]doctor.Finding, error) {
	if h.Host == nil || h.Host.Registry == nil {
		return nil, nil
	}
	statusBody, err := json.Marshal(req.Status)
	if err != nil {
		return nil, err
	}
	serviceBody, err := json.Marshal(req.Service)
	if err != nil {
		return nil, err
	}
	var out []doctor.Finding
	for _, plugin := range h.Host.Registry.Hooks(pluginapi.HookDoctorChecks) {
		if !plugin.Manifest.Permissions.DoctorChecks {
			return nil, PermissionError{Plugin: plugin.Manifest.Name, Kind: "doctor_checks", Summary: "doctor check permission is not declared"}
		}
		if plugin.Manifest.Runtime.Kind != pluginapi.RuntimeCommand {
			continue
		}
		var response pluginapi.DoctorChecksResponse
		err := h.Host.Runner.RunPluginHook(ctx, plugin, pluginapi.HookDoctorChecks, pluginapi.DoctorChecksRequest{
			Manifest: plugin.Manifest,
			Status:   statusBody,
			Service:  serviceBody,
			TraceID:  req.TraceID,
		}, &response)
		if err != nil {
			return nil, err
		}
		out = append(out, doctorFindings(response.Findings, req.Service)...)
	}
	return out, nil
}

func doctorFindings(findings []pluginapi.DoctorFinding, service servicestatus.Service) []doctor.Finding {
	out := make([]doctor.Finding, 0, len(findings))
	for _, finding := range findings {
		severity := doctor.Severity(finding.Severity)
		if severity == "" {
			severity = doctor.SeverityInfo
		}
		out = append(out, doctor.Finding{
			Code:       finding.Code,
			Severity:   severity,
			Service:    firstNonEmpty(finding.Service, service.Service),
			Summary:    finding.Summary,
			Confidence: finding.Confidence,
		})
	}
	return out
}
