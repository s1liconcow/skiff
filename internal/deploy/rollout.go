package deploy

import (
	"context"
	"fmt"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type StartRolloutRequest struct {
	Service              string       `json:"service"`
	Env                  string       `json:"env"`
	OperationID          string       `json:"operation_id"`
	ReleaseID            string       `json:"release_id,omitempty"`
	TraceID              string       `json:"trace_id,omitempty"`
	Actor                schema.Actor `json:"actor"`
	MinHealthyPercentage int          `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int          `json:"instance_warmup,omitempty"`
}

type WatchRolloutRequest struct {
	Service     string       `json:"service"`
	Env         string       `json:"env"`
	OperationID string       `json:"operation_id"`
	RolloutID   string       `json:"rollout_id,omitempty"`
	ProviderID  string       `json:"provider_id,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
}

func (d Deployer) StartRollout(ctx context.Context, req StartRolloutRequest) (*provider.Rollout, error) {
	if err := d.requireRolloutDeps(); err != nil {
		return nil, err
	}
	rollout, err := d.Provider.StartRollout(ctx, provider.RolloutRequest{
		Service:              req.Service,
		Env:                  req.Env,
		ReleaseID:            req.ReleaseID,
		OperationID:          req.OperationID,
		MinHealthyPercentage: req.MinHealthyPercentage,
		InstanceWarmup:       req.InstanceWarmup,
	})
	if err != nil {
		return nil, err
	}
	if err := d.appendProviderOperation(ctx, req.Service, req.OperationID, schema.ProviderOperationRef{
		Provider:    rollout.Provider,
		Kind:        aws.RolloutKindASGInstanceRefresh,
		ID:          rollout.ProviderID,
		ObservedAt:  canonical.Time(d.now()),
		Description: "ASG instance refresh rollout",
	}); err != nil {
		return nil, err
	}
	_ = d.appendRolloutEvent(ctx, req.Service, req.OperationID, req.TraceID, req.Actor, "rollout.started", "ASG instance refresh started", schema.Fact{Type: "provider_id", Message: rollout.ProviderID})
	return rollout, nil
}

func (d Deployer) WatchRollout(ctx context.Context, req WatchRolloutRequest) (*provider.RolloutStatus, error) {
	if err := d.requireRolloutDeps(); err != nil {
		return nil, err
	}
	providerID := req.ProviderID
	if providerID == "" {
		stored, err := d.StoredRolloutProviderID(ctx, req.Service, req.OperationID)
		if err != nil {
			return nil, err
		}
		providerID = stored
	}
	status, err := d.Provider.WatchRollout(ctx, provider.WatchRolloutRequest{
		Service:    req.Service,
		Env:        req.Env,
		RolloutID:  firstNonEmpty(req.RolloutID, req.OperationID),
		ProviderID: providerID,
	})
	if err != nil {
		return nil, err
	}
	_ = d.appendRolloutEvent(ctx, req.Service, req.OperationID, req.TraceID, req.Actor, "rollout."+status.Status, "rollout status "+status.Status, schema.Fact{Type: "provider_id", Message: status.ProviderID})
	if terminal := operationStatusForRollout(status.Status); terminal != "" && terminal != schema.OperationRunning {
		_ = d.setOperationStatus(ctx, req.Service, req.OperationID, terminal)
	}
	return status, nil
}

func (d Deployer) StoredRolloutProviderID(ctx context.Context, service, operationID string) (string, error) {
	control, _, err := d.getOperationControl(ctx, service, operationID)
	if err != nil {
		return "", err
	}
	for _, ref := range control.ProviderOperations {
		if ref.Provider == aws.Name && ref.Kind == aws.RolloutKindASGInstanceRefresh {
			return ref.ID, nil
		}
	}
	return "", fmt.Errorf("operation %s has no stored ASG instance refresh provider ID", operationID)
}

func (d Deployer) appendProviderOperation(ctx context.Context, service, operationID string, ref schema.ProviderOperationRef) error {
	for attempt := 0; attempt < 5; attempt++ {
		control, etag, err := d.getOperationControl(ctx, service, operationID)
		if err != nil {
			return err
		}
		replaced := false
		for i := range control.ProviderOperations {
			if control.ProviderOperations[i].Provider == ref.Provider && control.ProviderOperations[i].Kind == ref.Kind {
				control.ProviderOperations[i] = ref
				replaced = true
				break
			}
		}
		if !replaced {
			control.ProviderOperations = append(control.ProviderOperations, ref)
		}
		control.UpdatedAt = canonical.Time(d.now())
		if err := d.putOperationControlCAS(ctx, service, operationID, etag, control); err != nil {
			if attempt < 4 {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("operation control CAS did not complete")
}

func (d Deployer) setOperationStatus(ctx context.Context, service, operationID string, status schema.OperationStatus) error {
	control, etag, err := d.getOperationControl(ctx, service, operationID)
	if err != nil {
		return err
	}
	control.Status = status
	control.UpdatedAt = canonical.Time(d.now())
	return d.putOperationControlCAS(ctx, service, operationID, etag, control)
}

func (d Deployer) getOperationControl(ctx context.Context, service, operationID string) (schema.OperationControl, string, error) {
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		return schema.OperationControl{}, "", err
	}
	obj, err := d.Store.Get(ctx, key)
	if err != nil {
		return schema.OperationControl{}, "", err
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return schema.OperationControl{}, "", err
	}
	return control, obj.ETag, nil
}

func (d Deployer) putOperationControlCAS(ctx context.Context, service, operationID, etag string, control schema.OperationControl) error {
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		return err
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	_, err = d.Store.CompareAndSwap(ctx, key, etag, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (d Deployer) appendRolloutEvent(ctx context.Context, service, operationID, traceID string, actor schema.Actor, eventType, summary string, facts ...schema.Fact) error {
	log, err := events.NewLog(events.Options{Store: d.Store, Clock: d.now})
	if err != nil {
		return err
	}
	event := events.NewOperationEvent(service, operationID, eventType, summary, d.now(), traceID+eventType)
	event.TraceID = traceID
	event.Actor = &actor
	event.Facts = facts
	_, err = log.Append(ctx, event)
	return err
}

func (d Deployer) requireRolloutDeps() error {
	if d.Store == nil {
		return fmt.Errorf("object store is required")
	}
	if d.Provider == nil {
		return fmt.Errorf("provider is required")
	}
	return nil
}

func operationStatusForRollout(status string) schema.OperationStatus {
	switch status {
	case "succeeded":
		return schema.OperationSucceeded
	case "failed":
		return schema.OperationFailed
	case "cancelled":
		return schema.OperationCanceled
	case "rolling_out", "starting", "rolling_back":
		return schema.OperationRunning
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
