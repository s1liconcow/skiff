package ops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func (s *Store) AcquireLease(ctx context.Context, service, operationID string, opts LeaseOptions) (*LeaseAcquireResult, error) {
	if strings.TrimSpace(opts.Owner) == "" {
		return nil, errors.New("operation lease owner is required")
	}
	if opts.Duration <= 0 {
		return nil, errors.New("operation lease duration must be positive")
	}
	current, err := s.GetControl(ctx, service, operationID)
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	active, err := activeLease(current.Control.Lease, now)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, fmt.Errorf("%w: operation lease is held by %s", state.ErrLeaseHeld, current.Control.Lease.Owner)
	}
	previousLease := cloneLease(current.Control.Lease)
	next := current.Control
	next.Lease = &schema.Lease{
		Owner:      opts.Owner,
		Token:      randomToken(),
		Generation: now.UnixNano(),
		ExpiresAt:  canonical.Time(now.Add(opts.Duration)),
	}
	updated, err := s.UpdateControlCAS(ctx, current, next)
	if err != nil {
		return nil, err
	}
	handle, err := leaseHandleFromControl(updated)
	if err != nil {
		return nil, err
	}
	return &LeaseAcquireResult{
		Handle:        handle,
		Control:       updated,
		PreviousLease: previousLease,
		TookOver:      previousLease != nil,
	}, nil
}

func (s *Store) ReleaseLease(ctx context.Context, handle LeaseHandle) (*ControlDocument, error) {
	current, err := s.GetControl(ctx, handle.Service, handle.OperationID)
	if err != nil {
		return nil, err
	}
	if err := ensureLease(handle, current, s.clock().UTC()); err != nil {
		return nil, err
	}
	next := current.Control
	next.Lease = nil
	return s.UpdateControlCAS(ctx, current, next)
}

func (s *Store) UpdateControlWithLeaseCAS(ctx context.Context, handle LeaseHandle, mutate func(*schema.OperationControl) error) (*LeaseHandle, *ControlDocument, error) {
	if mutate == nil {
		return nil, nil, errors.New("operation control mutation callback is required")
	}
	current, err := s.GetControl(ctx, handle.Service, handle.OperationID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureLease(handle, current, s.clock().UTC()); err != nil {
		return nil, nil, err
	}
	if current.ETag != handle.ETag {
		return nil, nil, fmt.Errorf("%w: operation lease holder does not have latest control ETag", state.ErrPreconditionFailed)
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
	nextHandle, err := leaseHandleFromControl(updated)
	if err != nil {
		return nil, nil, err
	}
	return nextHandle, updated, nil
}

func activeLease(lease *schema.Lease, now time.Time) (bool, error) {
	if lease == nil {
		return false, nil
	}
	expiresAt, err := leaseExpires(*lease)
	if err != nil {
		return false, err
	}
	return expiresAt.After(now), nil
}

func leaseExpires(lease schema.Lease) (time.Time, error) {
	if strings.TrimSpace(lease.Owner) == "" {
		return time.Time{}, errors.New("operation lease owner is required")
	}
	if strings.TrimSpace(lease.Token) == "" {
		return time.Time{}, errors.New("operation lease token is required")
	}
	if lease.Generation <= 0 {
		return time.Time{}, errors.New("operation lease generation must be positive")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("operation lease expires_at %q is invalid: %w", lease.ExpiresAt, err)
	}
	return expiresAt, nil
}

func ensureLease(handle LeaseHandle, doc *ControlDocument, now time.Time) error {
	if doc == nil || doc.Control.Lease == nil {
		return fmt.Errorf("%w: operation control no longer has a lease", state.ErrLeaseLost)
	}
	lease := doc.Control.Lease
	if handle.Service != doc.Control.Service || handle.OperationID != doc.Control.OperationID || handle.Owner != lease.Owner || handle.Token != lease.Token || handle.Generation != lease.Generation {
		return fmt.Errorf("%w: operation control lease belongs to another worker", state.ErrLeaseLost)
	}
	active, err := activeLease(lease, now)
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("%w: operation control lease has expired", state.ErrLeaseLost)
	}
	return nil
}

func leaseHandleFromControl(doc *ControlDocument) (*LeaseHandle, error) {
	if doc == nil || doc.Control.Lease == nil {
		return nil, fmt.Errorf("%w: operation control does not contain a lease", state.ErrLeaseLost)
	}
	expiresAt, err := leaseExpires(*doc.Control.Lease)
	if err != nil {
		return nil, err
	}
	return &LeaseHandle{
		Service:     doc.Control.Service,
		OperationID: doc.Control.OperationID,
		Owner:       doc.Control.Lease.Owner,
		Token:       doc.Control.Lease.Token,
		Generation:  doc.Control.Lease.Generation,
		ExpiresAt:   expiresAt,
		ETag:        doc.ETag,
	}, nil
}

func randomToken() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("lease_%d", time.Now().UnixNano())
	}
	return "lease_" + hex.EncodeToString(bytes[:])
}
