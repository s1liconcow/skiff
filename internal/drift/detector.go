package drift

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const statefulGroupTag = "skiff.dev/stateful-group"

type Class string

const (
	ClassMissing       Class = "missing"
	ClassChanged       Class = "changed"
	ClassOrphaned      Class = "orphaned"
	ClassUnsafe        Class = "unsafe"
	ClassInformational Class = "informational"
)

type Detector struct {
	Store    objstore.ObjectStore
	Provider provider.Provider
	Clock    func() time.Time
}

type Request struct {
	Service string `json:"service"`
	Env     string `json:"env,omitempty"`
}

type Result struct {
	OK       bool      `json:"ok"`
	Service  string    `json:"service"`
	Env      string    `json:"env,omitempty"`
	Provider string    `json:"provider,omitempty"`
	FreshAt  string    `json:"fresh_at,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
}

type Finding struct {
	Class      Class    `json:"class"`
	Code       string   `json:"code"`
	Kind       string   `json:"kind,omitempty"`
	LogicalID  string   `json:"logical_id,omitempty"`
	Name       string   `json:"name,omitempty"`
	ProviderID string   `json:"provider_id,omitempty"`
	Summary    string   `json:"summary"`
	Safety     string   `json:"safety,omitempty"`
	Actions    []Action `json:"recommended_actions,omitempty"`
}

type Action struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Mutating bool   `json:"mutating"`
	Safety   string `json:"safety,omitempty"`
}

func (d Detector) Detect(ctx context.Context, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Store == nil {
		return nil, errors.New("object store is required")
	}
	if d.Provider == nil {
		return nil, errors.New("provider is required")
	}
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	if req.Service == "" {
		return nil, errors.New("service is required")
	}
	providerName := d.Provider.Name()
	records, err := d.resourceRecords(ctx, providerName, req)
	if err != nil {
		return nil, err
	}
	inspection, err := d.Provider.InspectService(ctx, provider.ServiceRef{Service: req.Service, Env: req.Env})
	if err != nil {
		return nil, err
	}
	result := &Result{
		OK:       true,
		Service:  req.Service,
		Env:      firstNonEmpty(req.Env, inspection.Ref.Env),
		Provider: providerName,
		FreshAt:  canonical.Time(inspection.FreshAt),
	}
	observedByProviderID := map[string]provider.ResourceInspection{}
	observedByLogical := map[string]provider.ResourceInspection{}
	for _, observed := range inspection.Resources {
		if observed.ProviderID != "" {
			observedByProviderID[observed.ProviderID] = observed
		}
		observedByLogical[logicalKey(observed.Kind, firstNonEmpty(observed.LogicalID, observed.Name))] = observed
	}
	desiredIDs := map[string]bool{}
	for _, record := range records {
		desiredIDs[record.Provider.ID] = true
		observed, ok := observedByProviderID[record.Provider.ID]
		if !ok {
			observed, ok = observedByLogical[logicalKey(record.Provider.Kind, record.Logical.Name)]
		}
		if !ok {
			result.Findings = append(result.Findings, missingFinding(req.Service, record))
			continue
		}
		if changed := changedFinding(req.Service, record, observed); changed != nil {
			result.Findings = append(result.Findings, *changed)
		}
	}
	for _, observed := range inspection.Resources {
		if observed.ProviderID != "" && desiredIDs[observed.ProviderID] {
			continue
		}
		key := logicalKey(observed.Kind, firstNonEmpty(observed.LogicalID, observed.Name))
		found := false
		for _, record := range records {
			if key == logicalKey(record.Provider.Kind, record.Logical.Name) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		result.Findings = append(result.Findings, orphanFinding(req.Service, observed))
	}
	if len(result.Findings) == 0 {
		result.Findings = append(result.Findings, Finding{
			Class:   ClassInformational,
			Code:    "NO_DRIFT_DETECTED",
			Summary: "provider inspection matches Skiff resource records",
		})
	}
	sort.Slice(result.Findings, func(i, j int) bool {
		if result.Findings[i].Class == result.Findings[j].Class {
			return result.Findings[i].Code < result.Findings[j].Code
		}
		return result.Findings[i].Class < result.Findings[j].Class
	})
	return result, nil
}

func (d Detector) resourceRecords(ctx context.Context, providerName string, req Request) ([]schema.ResourceRecord, error) {
	prefix, err := paths.ProviderResourcesPrefix(providerName)
	if err != nil {
		return nil, err
	}
	metas, err := d.Store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []schema.ResourceRecord
	for _, meta := range metas {
		object, err := d.Store.Get(ctx, meta.Key)
		if err != nil {
			return nil, err
		}
		var record schema.ResourceRecord
		if err := canonical.UnmarshalStrict(object.Body, &record); err != nil {
			return nil, fmt.Errorf("decode resource record %q: %w", meta.Key, err)
		}
		if record.Service != req.Service {
			continue
		}
		if req.Env != "" && record.Env != req.Env {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func missingFinding(service string, record schema.ResourceRecord) Finding {
	return Finding{
		Class:      ClassMissing,
		Code:       "RESOURCE_MISSING",
		Kind:       record.Provider.Kind,
		LogicalID:  record.Logical.Name,
		ProviderID: record.Provider.ID,
		Summary:    fmt.Sprintf("%s %s recorded by Skiff is missing from provider inspection", record.Provider.Kind, record.Provider.ID),
		Safety:     "inspect_before_mutating",
		Actions: []Action{{
			ID:       "inspect_resource",
			Command:  fmt.Sprintf("skiff drift %s --format json", service),
			Mutating: false,
		}},
	}
}

func changedFinding(service string, record schema.ResourceRecord, observed provider.ResourceInspection) *Finding {
	stateful := isStatefulRecord(record) || isStatefulObserved(observed)
	for key, desired := range record.Tags {
		if observed.Tags[key] == desired {
			continue
		}
		code := "RESOURCE_TAG_DRIFT"
		safety := "provider_update_or_manual_investigation"
		if stateful {
			code = "STATEFUL_RESOURCE_DRIFT"
			safety = "snapshot_and_explicit_approval_required"
		}
		return &Finding{
			Class:      ClassChanged,
			Code:       code,
			Kind:       record.Provider.Kind,
			LogicalID:  record.Logical.Name,
			ProviderID: firstNonEmpty(observed.ProviderID, record.Provider.ID),
			Summary:    fmt.Sprintf("%s tag %s drifted: desired %q observed %q", record.Provider.Kind, key, desired, observed.Tags[key]),
			Safety:     safety,
			Actions: []Action{{
				ID:       "plan_service",
				Command:  fmt.Sprintf("skiff plan <spec> --service %s --format json", service),
				Mutating: false,
			}},
		}
	}
	if strings.Contains(strings.ToLower(observed.Status), "drift") || strings.Contains(strings.ToLower(observed.Status), "changed") {
		code := "RESOURCE_STATUS_DRIFT"
		safety := "provider_update_or_manual_investigation"
		if stateful {
			code = "STATEFUL_RESOURCE_DRIFT"
			safety = "snapshot_and_explicit_approval_required"
		}
		return &Finding{
			Class:      ClassChanged,
			Code:       code,
			Kind:       record.Provider.Kind,
			LogicalID:  record.Logical.Name,
			ProviderID: firstNonEmpty(observed.ProviderID, record.Provider.ID),
			Summary:    fmt.Sprintf("%s status indicates drift: %s", record.Provider.Kind, observed.Status),
			Safety:     safety,
		}
	}
	return nil
}

func orphanFinding(service string, observed provider.ResourceInspection) Finding {
	class := ClassOrphaned
	code := "RESOURCE_ORPHANED"
	safety := "gc_plan_required"
	if isStatefulObserved(observed) {
		class = ClassUnsafe
		code = "STATEFUL_ORPHAN_PROTECTED"
		safety = "snapshot_and_explicit_approval_required"
	}
	return Finding{
		Class:      class,
		Code:       code,
		Kind:       observed.Kind,
		LogicalID:  observed.LogicalID,
		Name:       observed.Name,
		ProviderID: observed.ProviderID,
		Summary:    fmt.Sprintf("%s %s is visible in provider inspection but not in Skiff resource records", observed.Kind, firstNonEmpty(observed.ProviderID, observed.Name, observed.LogicalID)),
		Safety:     safety,
		Actions: []Action{{
			ID:       "gc_plan",
			Command:  fmt.Sprintf("skiff gc plan --service %s --format json", service),
			Mutating: false,
		}},
	}
}

func isStatefulKind(kind string) bool {
	kind = strings.ToLower(kind)
	return strings.Contains(kind, "database") ||
		strings.Contains(kind, "rds") ||
		strings.Contains(kind, "volume") ||
		strings.Contains(kind, "snapshot") ||
		strings.Contains(kind, "fencing") ||
		strings.Contains(kind, "stateful")
}

func isStatefulRecord(record schema.ResourceRecord) bool {
	return isStatefulKind(record.Provider.Kind) || isStatefulTags(record.Tags)
}

func isStatefulObserved(observed provider.ResourceInspection) bool {
	return isStatefulKind(observed.Kind) || isStatefulTags(observed.Tags)
}

func isStatefulTags(tags map[string]string) bool {
	if len(tags) == 0 {
		return false
	}
	return strings.TrimSpace(tags[statefulGroupTag]) != ""
}

func logicalKey(kind, logical string) string {
	return strings.ToLower(strings.TrimSpace(kind)) + "/" + strings.TrimSpace(logical)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
