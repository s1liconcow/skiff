package saga

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type LeaseOptions struct {
	Owner    string
	Duration time.Duration
}

type LeaseHandle struct {
	SagaID     string    `json:"saga_id"`
	Owner      string    `json:"owner"`
	Token      string    `json:"token"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	ETag       string    `json:"etag"`
}

func (s *Store) AcquireLease(ctx context.Context, sagaID string, opts LeaseOptions) (*LeaseHandle, *ControlDocument, error) {
	if opts.Owner == "" {
		return nil, nil, errors.New("saga lease owner is required")
	}
	if opts.Duration <= 0 {
		return nil, nil, errors.New("saga lease duration must be positive")
	}
	current, err := s.GetControl(ctx, sagaID)
	if err != nil {
		return nil, nil, err
	}
	now := s.clock().UTC()
	if active, err := sagaLeaseActive(current.Control.Lease, now); err != nil {
		return nil, nil, err
	} else if active {
		return nil, nil, fmt.Errorf("%w: saga lease is held by %s", state.ErrLeaseHeld, current.Control.Lease.Owner)
	}
	next := current.Control
	next.Lease = &schema.Lease{
		Owner:      opts.Owner,
		Token:      randomToken(),
		Generation: now.UnixNano(),
		ExpiresAt:  canonical.Time(now.Add(opts.Duration)),
	}
	updated, err := s.UpdateControlCAS(ctx, current, next)
	if err != nil {
		return nil, nil, err
	}
	handle, err := sagaLeaseHandle(updated)
	if err != nil {
		return nil, nil, err
	}
	return handle, updated, nil
}

func (s *Store) HeartbeatLease(ctx context.Context, handle LeaseHandle, duration time.Duration) (*LeaseHandle, *ControlDocument, error) {
	if duration <= 0 {
		return nil, nil, errors.New("saga lease heartbeat duration must be positive")
	}
	current, err := s.GetControl(ctx, handle.SagaID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureSagaLease(handle, current, s.clock().UTC()); err != nil {
		return nil, nil, err
	}
	next := current.Control
	next.Lease.ExpiresAt = canonical.Time(s.clock().UTC().Add(duration))
	updated, err := s.UpdateControlCAS(ctx, &ControlDocument{Key: current.Key, ETag: handle.ETag, Meta: current.Meta, Control: current.Control}, next)
	if err != nil {
		return nil, nil, err
	}
	nextHandle, err := sagaLeaseHandle(updated)
	if err != nil {
		return nil, nil, err
	}
	return nextHandle, updated, nil
}

func (s *Store) ReleaseLease(ctx context.Context, handle LeaseHandle) (*ControlDocument, error) {
	current, err := s.GetControl(ctx, handle.SagaID)
	if err != nil {
		return nil, err
	}
	if err := ensureSagaLease(handle, current, s.clock().UTC()); err != nil {
		return nil, err
	}
	next := current.Control
	next.Lease = nil
	return s.UpdateControlCAS(ctx, &ControlDocument{Key: current.Key, ETag: handle.ETag, Meta: current.Meta, Control: current.Control}, next)
}

func (s *Store) UpdateControlWithLeaseCAS(ctx context.Context, handle LeaseHandle, mutate func(*schema.SagaControl) error) (*LeaseHandle, *ControlDocument, error) {
	if mutate == nil {
		return nil, nil, errors.New("saga control mutation callback is required")
	}
	current, err := s.GetControl(ctx, handle.SagaID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureSagaLease(handle, current, s.clock().UTC()); err != nil {
		return nil, nil, err
	}
	if current.ETag != handle.ETag {
		return nil, nil, fmt.Errorf("%w: saga lease holder does not have latest control ETag", state.ErrPreconditionFailed)
	}
	lease := *current.Control.Lease
	next := current.Control
	if err := mutate(&next); err != nil {
		return nil, nil, err
	}
	next.Lease = &lease
	updated, err := s.UpdateControlCAS(ctx, current, next)
	if err != nil {
		return nil, nil, err
	}
	nextHandle, err := sagaLeaseHandle(updated)
	if err != nil {
		return nil, nil, err
	}
	return nextHandle, updated, nil
}

func sagaLeaseActive(lease *schema.Lease, now time.Time) (bool, error) {
	if lease == nil {
		return false, nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		return false, err
	}
	return expiresAt.After(now), nil
}

func ensureSagaLease(handle LeaseHandle, doc *ControlDocument, now time.Time) error {
	if doc == nil || doc.Control.Lease == nil {
		return fmt.Errorf("%w: saga control no longer has a lease", state.ErrLeaseLost)
	}
	lease := doc.Control.Lease
	if handle.SagaID != doc.Control.SagaID || handle.Owner != lease.Owner || handle.Token != lease.Token || handle.Generation != lease.Generation {
		return fmt.Errorf("%w: saga control lease belongs to another executor", state.ErrLeaseLost)
	}
	active, err := sagaLeaseActive(lease, now)
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("%w: saga control lease has expired", state.ErrLeaseLost)
	}
	return nil
}

func sagaLeaseHandle(doc *ControlDocument) (*LeaseHandle, error) {
	if doc == nil || doc.Control.Lease == nil {
		return nil, fmt.Errorf("%w: saga control does not contain a lease", state.ErrLeaseLost)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, doc.Control.Lease.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &LeaseHandle{
		SagaID:     doc.Control.SagaID,
		Owner:      doc.Control.Lease.Owner,
		Token:      doc.Control.Lease.Token,
		Generation: doc.Control.Lease.Generation,
		ExpiresAt:  expiresAt,
		ETag:       doc.ETag,
	}, nil
}

func randomToken() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("lease_%d", time.Now().UnixNano())
	}
	return "lease_" + hex.EncodeToString(bytes[:])
}
