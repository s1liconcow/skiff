package gc

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/audit"
	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
	stateruntime "github.com/s1liconcow/skiff/internal/stateful"
)

const statefulGroupTag = "skiff.dev/stateful-group"

type ActionKind string

const (
	ActionExpireRelease         ActionKind = "expire_release_artifact"
	ActionArchiveOperation      ActionKind = "archive_abandoned_operation"
	ActionRefreshResourceRecord ActionKind = "refresh_resource_record"
	ActionProtectStateful       ActionKind = "protect_stateful_resource"
)

type PlanRequest struct {
	Service   string        `json:"service,omitempty"`
	Env       string        `json:"env,omitempty"`
	Retention time.Duration `json:"retention,omitempty"`
	Now       time.Time     `json:"now,omitempty"`
}

type Plan struct {
	OK        bool     `json:"ok"`
	Service   string   `json:"service,omitempty"`
	Env       string   `json:"env,omitempty"`
	Retention string   `json:"retention"`
	Actions   []Action `json:"actions,omitempty"`
	ReadOnly  bool     `json:"read_only"`
}

type Action struct {
	ID               string     `json:"id"`
	Kind             ActionKind `json:"kind"`
	Key              string     `json:"key,omitempty"`
	Service          string     `json:"service,omitempty"`
	Env              string     `json:"env,omitempty"`
	ResourceKind     string     `json:"resource_kind,omitempty"`
	ProviderID       string     `json:"provider_id,omitempty"`
	Summary          string     `json:"summary"`
	Mutating         bool       `json:"mutating"`
	Protected        bool       `json:"protected,omitempty"`
	RequiresSnapshot bool       `json:"requires_snapshot,omitempty"`
	RequiresApproval bool       `json:"requires_approval,omitempty"`
	Safety           string     `json:"safety"`
}

type ApplyRequest struct {
	Actor      schema.Actor `json:"actor"`
	TraceID    string       `json:"trace_id,omitempty"`
	ApprovalID string       `json:"approval_id,omitempty"`
	Yes        bool         `json:"yes"`
}

type ApplyResult struct {
	OK      bool                 `json:"ok"`
	TraceID string               `json:"trace_id,omitempty"`
	Applied []Action             `json:"applied,omitempty"`
	Skipped []Action             `json:"skipped,omitempty"`
	Audits  []events.AuditRecord `json:"audits,omitempty"`
}

type Planner struct {
	Store      objstore.ObjectStore
	Clock      func() time.Time
	Authorizer authz.Authorizer
}

func (p Planner) Plan(ctx context.Context, req PlanRequest) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Store == nil {
		return nil, errors.New("object store is required")
	}
	now := req.Now
	if now.IsZero() {
		now = p.now()
	}
	retention := req.Retention
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	plan := &Plan{OK: true, Service: req.Service, Env: req.Env, Retention: retention.String(), ReadOnly: true}
	if err := p.addReleaseActions(ctx, plan, req, now, retention); err != nil {
		return nil, err
	}
	if err := p.addOperationActions(ctx, plan, req, now, retention); err != nil {
		return nil, err
	}
	if err := p.addResourceRecordActions(ctx, plan, req, now, retention); err != nil {
		return nil, err
	}
	if err := p.addStatefulBackupActions(ctx, plan, req, now, retention); err != nil {
		return nil, err
	}
	sort.Slice(plan.Actions, func(i, j int) bool {
		return plan.Actions[i].ID < plan.Actions[j].ID
	})
	return plan, nil
}

func (p Planner) Apply(ctx context.Context, plan Plan, req ApplyRequest) (*ApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Store == nil {
		return nil, errors.New("object store is required")
	}
	if !req.Yes {
		return nil, errors.New("gc apply requires --yes")
	}
	if _, err := authz.MustAuthorize(ctx, p.Authorizer, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionGC,
		Target:     schema.Target{Kind: "gc", Name: firstNonEmpty(plan.Service, "state")},
		Env:        plan.Env,
		Service:    plan.Service,
		Risk:       schema.RiskHigh,
		ApprovalID: req.ApprovalID,
		TraceID:    req.TraceID,
	}); err != nil {
		return nil, err
	}
	result := &ApplyResult{OK: true, TraceID: req.TraceID}
	for _, action := range plan.Actions {
		if action.Protected || action.RequiresSnapshot {
			result.Skipped = append(result.Skipped, action)
			continue
		}
		record, err := audit.Append(ctx, p.Store, audit.RecordRequest{
			Actor:      req.Actor,
			Action:     "gc.apply",
			Target:     schema.Target{Kind: string(action.Kind), Name: firstNonEmpty(action.Key, action.ProviderID, action.ID)},
			TraceID:    req.TraceID,
			Risk:       schema.RiskHigh,
			ApprovalID: req.ApprovalID,
			Summary:    action.Summary,
			Data: map[string]string{
				"gc_action": string(action.Kind),
				"key":       action.Key,
				"service":   action.Service,
			},
		}, p.now(), action.ID+req.TraceID)
		if err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, action)
		result.Audits = append(result.Audits, *record)
	}
	return result, nil
}

func (p Planner) addReleaseActions(ctx context.Context, plan *Plan, req PlanRequest, now time.Time, retention time.Duration) error {
	services := []string{}
	if req.Service != "" {
		services = []string{req.Service}
	} else {
		metas, err := p.Store.List(ctx, "services/", objstore.ListOptions{})
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, meta := range metas {
			parts := strings.Split(meta.Key, "/")
			if len(parts) >= 2 && parts[0] == "services" && !seen[parts[1]] {
				seen[parts[1]] = true
				services = append(services, parts[1])
			}
		}
	}
	for _, service := range services {
		control := p.readServiceControl(ctx, service)
		prefix := "services/" + service + "/releases/"
		metas, err := p.Store.List(ctx, prefix, objstore.ListOptions{})
		if err != nil {
			return err
		}
		for _, meta := range metas {
			if !strings.HasSuffix(meta.Key, "/release.json") {
				continue
			}
			var release schema.ReleaseManifest
			if !p.readJSON(ctx, meta.Key, &release) {
				continue
			}
			if release.ReleaseID == "" || release.ReleaseID == control.DesiredRelease || release.ReleaseID == control.StableRelease {
				continue
			}
			createdAt, err := time.Parse(time.RFC3339Nano, release.CreatedAt)
			if err != nil || now.Sub(createdAt.UTC()) < retention {
				continue
			}
			plan.Actions = append(plan.Actions, Action{
				ID:       "release:" + service + ":" + release.ReleaseID,
				Kind:     ActionExpireRelease,
				Key:      meta.Key,
				Service:  service,
				Env:      release.Env,
				Summary:  "release is outside retention and not desired or stable",
				Mutating: true,
				Safety:   "lifecycle_expiration_only",
			})
		}
	}
	return nil
}

func (p Planner) addOperationActions(ctx context.Context, plan *Plan, req PlanRequest, now time.Time, retention time.Duration) error {
	prefix := "services/"
	if req.Service != "" {
		prefix = "services/" + req.Service + "/operations/"
	}
	metas, err := p.Store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return err
	}
	for _, meta := range metas {
		if !strings.HasSuffix(meta.Key, "/control.json") || !strings.Contains(meta.Key, "/operations/") {
			continue
		}
		var control schema.OperationControl
		if !p.readJSON(ctx, meta.Key, &control) {
			continue
		}
		if req.Env != "" && control.Env != req.Env {
			continue
		}
		if control.Status != schema.OperationSucceeded && control.Status != schema.OperationFailed && control.Status != schema.OperationCanceled {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, control.UpdatedAt)
		if err != nil || now.Sub(updatedAt.UTC()) < retention {
			continue
		}
		plan.Actions = append(plan.Actions, Action{
			ID:       "operation:" + control.Service + ":" + control.OperationID,
			Kind:     ActionArchiveOperation,
			Key:      meta.Key,
			Service:  control.Service,
			Env:      control.Env,
			Summary:  "completed operation is outside retention and can be archived by lifecycle policy",
			Mutating: true,
			Safety:   "archive_only",
		})
	}
	return nil
}

func (p Planner) addResourceRecordActions(ctx context.Context, plan *Plan, req PlanRequest, now time.Time, retention time.Duration) error {
	metas, err := p.Store.List(ctx, "resources/by-provider/", objstore.ListOptions{})
	if err != nil {
		return err
	}
	for _, meta := range metas {
		var record schema.ResourceRecord
		if !p.readJSON(ctx, meta.Key, &record) {
			continue
		}
		if req.Service != "" && record.Service != req.Service {
			continue
		}
		if req.Env != "" && record.Env != req.Env {
			continue
		}
		observedAt, err := time.Parse(time.RFC3339Nano, record.ObservedAt)
		if err != nil || now.Sub(observedAt.UTC()) < retention {
			continue
		}
		action := Action{
			ID:           "resource:" + record.Provider.Provider + ":" + record.Provider.Kind + ":" + record.Provider.ID,
			Kind:         ActionRefreshResourceRecord,
			Key:          meta.Key,
			Service:      record.Service,
			Env:          record.Env,
			ResourceKind: record.Provider.Kind,
			ProviderID:   record.Provider.ID,
			Summary:      "resource record observation is stale and should be refreshed before cleanup",
			Mutating:     true,
			Safety:       "refresh_before_cleanup",
		}
		if isStatefulRecord(record) {
			action.Kind = ActionProtectStateful
			action.Protected = true
			action.RequiresSnapshot = true
			action.RequiresApproval = true
			action.Summary = "stateful resource is protected from automatic cleanup"
			action.Safety = "snapshot_and_explicit_approval_required"
		}
		plan.Actions = append(plan.Actions, action)
	}
	return nil
}

func (p Planner) addStatefulBackupActions(ctx context.Context, plan *Plan, req PlanRequest, now time.Time, retention time.Duration) error {
	prefix := "stateful/"
	if req.Service != "" {
		prefix = "stateful/" + req.Service + "/backups/"
	}
	metas, err := p.Store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return err
	}
	for _, meta := range metas {
		if !strings.HasSuffix(meta.Key, "/record.json") || !strings.Contains(meta.Key, "/backups/") {
			continue
		}
		var record stateruntime.BackupRecord
		if !p.readJSON(ctx, meta.Key, &record) {
			continue
		}
		if req.Service != "" && record.Group != req.Service {
			continue
		}
		if req.Env != "" && record.Env != req.Env {
			continue
		}
		if !backupOutsideRetention(record, now, retention) {
			continue
		}
		plan.Actions = append(plan.Actions, Action{
			ID:               "stateful-backup:" + record.Group + ":" + firstNonEmpty(record.BackupID, record.SnapshotID),
			Kind:             ActionProtectStateful,
			Key:              meta.Key,
			Service:          record.Group,
			Env:              record.Env,
			ResourceKind:     "stateful-backup",
			ProviderID:       firstNonEmpty(record.ProviderID, record.SnapshotID),
			Summary:          "stateful backup is outside retention but protected from automatic cleanup",
			Mutating:         true,
			Protected:        true,
			RequiresApproval: true,
			Safety:           "retention_check_and_explicit_approval_required",
		})
	}
	return nil
}

func (p Planner) readServiceControl(ctx context.Context, service string) schema.ServiceControl {
	var control schema.ServiceControl
	_ = p.readJSON(ctx, "services/"+service+"/control.json", &control)
	return control
}

func (p Planner) readJSON(ctx context.Context, key string, out any) bool {
	object, err := p.Store.Get(ctx, key)
	if err != nil {
		return false
	}
	return canonical.UnmarshalStrict(object.Body, out) == nil
}

func (p Planner) now() time.Time {
	if p.Clock != nil {
		return p.Clock().UTC()
	}
	return time.Now().UTC()
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
	return isStatefulKind(record.Provider.Kind) || strings.TrimSpace(record.Tags[statefulGroupTag]) != ""
}

func backupOutsideRetention(record stateruntime.BackupRecord, now time.Time, retention time.Duration) bool {
	if record.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
		return err == nil && !now.Before(expiresAt.UTC())
	}
	createdAt, err := time.Parse(time.RFC3339Nano, record.CreatedAt)
	return err == nil && now.Sub(createdAt.UTC()) >= retention
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
