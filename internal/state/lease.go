package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type LeaseOptions struct {
	Owner    string
	Duration time.Duration
	Actor    schema.Actor
	TraceID  string
}

type LeaseHandle struct {
	Service    string    `json:"service"`
	Owner      string    `json:"owner"`
	Token      string    `json:"token"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	ETag       string    `json:"etag"`
}

func AcquireLease(ctx context.Context, store objstore.ObjectStore, service string, opts LeaseOptions) (*LeaseHandle, *ServiceControlDocument, error) {
	return NewClient(store).AcquireLease(ctx, service, opts)
}

func HeartbeatLease(ctx context.Context, store objstore.ObjectStore, handle LeaseHandle, duration time.Duration) (*LeaseHandle, *ServiceControlDocument, error) {
	return NewClient(store).HeartbeatLease(ctx, handle, duration)
}

func ReleaseLease(ctx context.Context, store objstore.ObjectStore, handle LeaseHandle) (*ServiceControlDocument, error) {
	return NewClient(store).ReleaseLease(ctx, handle)
}

func (c *Client) AcquireLease(ctx context.Context, service string, opts LeaseOptions) (*LeaseHandle, *ServiceControlDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, nil, err
	}
	if err := validateLeaseOptions(service, opts.Owner, opts.Duration); err != nil {
		return nil, nil, err
	}
	token := c.tokenGenerator()
	if strings.TrimSpace(token) == "" {
		return nil, nil, stateError(CodeInvalidTransition, service, "", "lease token generator returned an empty token", ErrInvalidTransition)
	}

	var lastErr error
	retry := c.retry.normalized()
	for attempt := 0; attempt < retry.MaxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, retry, attempt); err != nil {
				return nil, nil, err
			}
		}
		current, err := c.GetServiceControl(ctx, service)
		if err != nil {
			return nil, nil, err
		}
		now := c.clock.Now()
		active, err := activeLease(current.Control.Lease, now)
		if err != nil {
			return nil, nil, stateError(CodeInvalidTransition, service, current.Key, err.Error(), err)
		}
		if active {
			return nil, nil, leaseHeldError(service, current.Key, current.Control.Lease)
		}

		next := current.Control
		next.Lease = &schema.Lease{
			Owner:      opts.Owner,
			Token:      token,
			Generation: current.Control.Version + 1,
			ExpiresAt:  canonical.Time(now.Add(opts.Duration)),
		}
		next.UpdatedBy = actorForLease(opts.Actor, opts.Owner)
		next.TraceID = opts.TraceID

		updated, err := c.UpdateServiceControlCAS(ctx, current, next)
		if err == nil {
			handle, err := handleFromDocument(updated)
			if err != nil {
				return nil, nil, err
			}
			return handle, updated, nil
		}
		if !errors.Is(err, ErrPreconditionFailed) {
			return nil, nil, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = stateError(CodePreconditionFailed, service, "", "lease acquisition did not complete", ErrPreconditionFailed)
	}
	return nil, nil, lastErr
}

func (c *Client) HeartbeatLease(ctx context.Context, handle LeaseHandle, duration time.Duration) (*LeaseHandle, *ServiceControlDocument, error) {
	if err := validateHandle(handle); err != nil {
		return nil, nil, err
	}
	if duration <= 0 {
		return nil, nil, stateError(CodeInvalidTransition, handle.Service, "", "lease heartbeat duration must be positive", ErrInvalidTransition)
	}
	current, err := c.GetServiceControl(ctx, handle.Service)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureLeaseUsable(handle, current, c.clock.Now()); err != nil {
		return nil, nil, err
	}

	next := current.Control
	next.Lease.ExpiresAt = canonical.Time(c.clock.Now().Add(duration))
	updated, err := c.UpdateServiceControlCAS(ctx, &ServiceControlDocument{
		Key:     current.Key,
		ETag:    handle.ETag,
		Meta:    current.Meta,
		Control: current.Control,
	}, next)
	if err != nil {
		if errors.Is(err, ErrPreconditionFailed) {
			if lostErr := c.detectLeaseLoss(ctx, handle); lostErr != nil {
				return nil, nil, lostErr
			}
		}
		return nil, nil, err
	}
	newHandle, err := handleFromDocument(updated)
	if err != nil {
		return nil, nil, err
	}
	return newHandle, updated, nil
}

func (c *Client) ReleaseLease(ctx context.Context, handle LeaseHandle) (*ServiceControlDocument, error) {
	if err := validateHandle(handle); err != nil {
		return nil, err
	}
	current, err := c.GetServiceControl(ctx, handle.Service)
	if err != nil {
		return nil, err
	}
	if err := ensureLeaseUsable(handle, current, c.clock.Now()); err != nil {
		return nil, err
	}
	next := current.Control
	next.Lease = nil
	updated, err := c.UpdateServiceControlCAS(ctx, &ServiceControlDocument{
		Key:     current.Key,
		ETag:    handle.ETag,
		Meta:    current.Meta,
		Control: current.Control,
	}, next)
	if err != nil {
		if errors.Is(err, ErrPreconditionFailed) {
			if lostErr := c.detectLeaseLoss(ctx, handle); lostErr != nil {
				return nil, lostErr
			}
		}
		return nil, err
	}
	return updated, nil
}

func (c *Client) UpdateServiceControlWithLeaseCAS(ctx context.Context, handle LeaseHandle, mutate func(*schema.ServiceControl) error) (*LeaseHandle, *ServiceControlDocument, error) {
	if err := validateHandle(handle); err != nil {
		return nil, nil, err
	}
	if mutate == nil {
		return nil, nil, stateError(CodeInvalidTransition, handle.Service, "", "service control mutation callback is required", ErrInvalidTransition)
	}
	current, err := c.GetServiceControl(ctx, handle.Service)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureLeaseUsable(handle, current, c.clock.Now()); err != nil {
		return nil, nil, err
	}
	if current.ETag != handle.ETag {
		return nil, nil, stateError(CodePreconditionFailed, handle.Service, current.Key, "lease holder does not have the latest service control ETag", ErrPreconditionFailed)
	}
	lease := *current.Control.Lease
	next := current.Control
	if err := mutate(&next); err != nil {
		return nil, nil, err
	}
	next.Lease = &lease
	updated, err := c.UpdateServiceControlCAS(ctx, current, next)
	if err != nil {
		return nil, nil, err
	}
	newHandle, err := handleFromDocument(updated)
	if err != nil {
		return nil, nil, err
	}
	return newHandle, updated, nil
}

func validateLeaseOptions(service, owner string, duration time.Duration) error {
	if strings.TrimSpace(owner) == "" {
		return stateError(CodeInvalidTransition, service, "", "lease owner is required", ErrInvalidTransition)
	}
	if duration <= 0 {
		return stateError(CodeInvalidTransition, service, "", "lease duration must be positive", ErrInvalidTransition)
	}
	return nil
}

func validateLease(lease schema.Lease) error {
	if strings.TrimSpace(lease.Owner) == "" {
		return stateError(CodeInvalidTransition, "", "", "lease owner is required", ErrInvalidTransition)
	}
	if strings.TrimSpace(lease.Token) == "" {
		return stateError(CodeInvalidTransition, "", "", "lease token is required", ErrInvalidTransition)
	}
	if lease.Generation <= 0 {
		return stateError(CodeInvalidTransition, "", "", "lease generation must be positive", ErrInvalidTransition)
	}
	if _, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt); err != nil {
		return stateError(CodeInvalidTransition, "", "", fmt.Sprintf("lease expires_at %q is invalid", lease.ExpiresAt), err)
	}
	return nil
}

func activeLease(lease *schema.Lease, now time.Time) (bool, error) {
	if lease == nil {
		return false, nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("lease expires_at %q is invalid: %w", lease.ExpiresAt, err)
	}
	return expiresAt.After(now), nil
}

func handleFromDocument(doc *ServiceControlDocument) (*LeaseHandle, error) {
	if doc == nil || doc.Control.Lease == nil {
		return nil, stateError(CodeLeaseLost, "", "", "service control does not contain a lease", ErrLeaseLost)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, doc.Control.Lease.ExpiresAt)
	if err != nil {
		return nil, stateError(CodeInvalidTransition, doc.Control.Service, doc.Key, fmt.Sprintf("lease expires_at %q is invalid", doc.Control.Lease.ExpiresAt), err)
	}
	return &LeaseHandle{
		Service:    doc.Control.Service,
		Owner:      doc.Control.Lease.Owner,
		Token:      doc.Control.Lease.Token,
		Generation: doc.Control.Lease.Generation,
		ExpiresAt:  expiresAt,
		ETag:       doc.ETag,
	}, nil
}

func ensureLeaseMatches(handle LeaseHandle, doc *ServiceControlDocument) error {
	if doc == nil || doc.Control.Lease == nil {
		return stateError(CodeLeaseLost, handle.Service, "", "service control no longer has a lease", ErrLeaseLost)
	}
	lease := doc.Control.Lease
	if lease.Owner != handle.Owner || lease.Token != handle.Token || lease.Generation != handle.Generation {
		return stateError(CodeLeaseLost, handle.Service, doc.Key, "service control lease is owned by another actor", ErrLeaseLost)
	}
	return nil
}

func ensureLeaseUsable(handle LeaseHandle, doc *ServiceControlDocument, now time.Time) error {
	if err := ensureLeaseMatches(handle, doc); err != nil {
		return err
	}
	active, err := activeLease(doc.Control.Lease, now)
	if err != nil {
		return stateError(CodeInvalidTransition, handle.Service, doc.Key, err.Error(), err)
	}
	if !active {
		return stateError(CodeLeaseLost, handle.Service, doc.Key, "service control lease has expired", ErrLeaseLost)
	}
	return nil
}

func (c *Client) detectLeaseLoss(ctx context.Context, handle LeaseHandle) error {
	current, err := c.GetServiceControl(ctx, handle.Service)
	if err != nil {
		return err
	}
	if err := ensureLeaseUsable(handle, current, c.clock.Now()); err != nil {
		return err
	}
	return nil
}

func validateHandle(handle LeaseHandle) error {
	if strings.TrimSpace(handle.Service) == "" {
		return stateError(CodeInvalidTransition, "", "", "lease handle service is required", ErrInvalidTransition)
	}
	if strings.TrimSpace(handle.Owner) == "" || strings.TrimSpace(handle.Token) == "" || handle.Generation <= 0 || strings.TrimSpace(handle.ETag) == "" {
		return stateError(CodeInvalidTransition, handle.Service, "", "lease handle is incomplete", ErrInvalidTransition)
	}
	return nil
}

func leaseHeldError(service, key string, lease *schema.Lease) error {
	summary := "service control lease is already held"
	if lease != nil {
		summary = fmt.Sprintf("service control lease is held by %q until %s", lease.Owner, lease.ExpiresAt)
	}
	return stateError(CodeLeaseHeld, service, key, summary, ErrLeaseHeld)
}

func actorForLease(actor schema.Actor, owner string) schema.Actor {
	if actor.ID != "" {
		return actor
	}
	return schema.Actor{ID: owner, Type: "agent"}
}
