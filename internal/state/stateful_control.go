package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	StatefulMemberReady     = "ready"
	StatefulMemberReplacing = "replacing"
	StatefulMemberUpdating  = "updating"
	StatefulMemberFailed    = "failed"
)

type StatefulMemberDocument struct {
	Key     string                       `json:"key"`
	ETag    string                       `json:"etag"`
	Meta    objstore.ObjectMeta          `json:"meta"`
	Control schema.StatefulMemberControl `json:"control"`
}

type StatefulGroupDocument struct {
	Key     string                      `json:"key"`
	ETag    string                      `json:"etag"`
	Meta    objstore.ObjectMeta         `json:"meta"`
	Control schema.StatefulGroupControl `json:"control"`
}

type StatefulMemberLeaseOptions struct {
	Owner    string
	Duration time.Duration
	Actor    schema.Actor
	TraceID  string
	Purpose  string
}

type StatefulMemberLeaseHandle struct {
	Group      string    `json:"group"`
	Member     int       `json:"member"`
	Owner      string    `json:"owner"`
	Token      string    `json:"token"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	ETag       string    `json:"etag"`
}

func (c *Client) GetStatefulGroupControl(ctx context.Context, group string) (*StatefulGroupDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.StatefulGroupControl(group)
	if err != nil {
		return nil, err
	}
	obj, err := c.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var control schema.StatefulGroupControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return nil, stateError(CodeInvalidTransition, group, key, "stateful group control document is not valid canonical schema", err)
	}
	if err := validateStatefulGroupControl(control); err != nil {
		return nil, err
	}
	if control.Group != group {
		return nil, stateError(CodeInvalidTransition, group, key, fmt.Sprintf("stateful group control names group %q", control.Group), nil)
	}
	meta := objectMetaFromObject(obj)
	return &StatefulGroupDocument{Key: key, ETag: meta.ETag, Meta: meta, Control: control}, nil
}

func (c *Client) CreateStatefulGroupControl(ctx context.Context, control schema.StatefulGroupControl) (*StatefulGroupDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	if control.UpdatedAt == "" {
		control.UpdatedAt = canonical.Time(c.clock.Now())
	}
	normalizeStatefulGroupControl(&control)
	if err := validateStatefulGroupControl(control); err != nil {
		return nil, err
	}
	key, err := paths.StatefulGroupControl(control.Group)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return nil, err
	}
	meta, err := c.store.Create(ctx, key, body, putOptionsForStatefulGroup(control))
	if err != nil {
		return nil, err
	}
	return &StatefulGroupDocument{Key: key, ETag: meta.ETag, Meta: *meta, Control: control}, nil
}

func (c *Client) UpdateStatefulGroupControlCAS(ctx context.Context, current *StatefulGroupDocument, next schema.StatefulGroupControl) (*StatefulGroupDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, stateError(CodeInvalidTransition, "", "", "current stateful group control document is required", ErrInvalidTransition)
	}
	normalizeStatefulGroupControl(&next)
	if next.Group == "" {
		next.Group = current.Control.Group
	}
	if next.Env == "" {
		next.Env = current.Control.Env
	}
	if next.Group != current.Control.Group || next.Env != current.Control.Env {
		return nil, stateError(CodeInvalidTransition, current.Control.Group, current.Key, "cannot change stateful group or env", ErrInvalidTransition)
	}
	next.Version = current.Control.Version + 1
	next.UpdatedAt = canonical.Time(c.clock.Now())
	if err := validateStatefulGroupControl(next); err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(next)
	if err != nil {
		return nil, err
	}
	meta, err := c.store.CompareAndSwap(ctx, current.Key, current.ETag, body, putOptionsForStatefulGroup(next))
	if err != nil {
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			return nil, stateError(CodePreconditionFailed, current.Control.Group, current.Key, "stateful group control ETag is stale", err)
		}
		return nil, err
	}
	return &StatefulGroupDocument{Key: current.Key, ETag: meta.ETag, Meta: *meta, Control: next}, nil
}

func (c *Client) GetStatefulMemberControl(ctx context.Context, group string, member int) (*StatefulMemberDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.StatefulMemberControl(group, member)
	if err != nil {
		return nil, err
	}
	obj, err := c.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var control schema.StatefulMemberControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return nil, stateError(CodeInvalidTransition, group, key, "stateful member control document is not valid canonical schema", err)
	}
	if err := validateStatefulMemberControl(control); err != nil {
		return nil, err
	}
	if control.Group != group || control.Member != member {
		return nil, stateError(CodeInvalidTransition, group, key, fmt.Sprintf("stateful member control names %s/%d", control.Group, control.Member), nil)
	}
	meta := objectMetaFromObject(obj)
	return &StatefulMemberDocument{Key: key, ETag: meta.ETag, Meta: meta, Control: control}, nil
}

func (c *Client) CreateStatefulMemberControl(ctx context.Context, control schema.StatefulMemberControl) (*StatefulMemberDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	if control.UpdatedAt == "" {
		control.UpdatedAt = canonical.Time(c.clock.Now())
	}
	normalizeStatefulMemberControl(&control)
	if err := validateStatefulMemberControl(control); err != nil {
		return nil, err
	}
	key, err := paths.StatefulMemberControl(control.Group, control.Member)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return nil, err
	}
	meta, err := c.store.Create(ctx, key, body, putOptionsForStatefulMember(control))
	if err != nil {
		return nil, err
	}
	return &StatefulMemberDocument{Key: key, ETag: meta.ETag, Meta: *meta, Control: control}, nil
}

func (c *Client) UpdateStatefulMemberControlCAS(ctx context.Context, current *StatefulMemberDocument, next schema.StatefulMemberControl) (*StatefulMemberDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, stateError(CodeInvalidTransition, "", "", "current stateful member control document is required", ErrInvalidTransition)
	}
	normalizeStatefulMemberControl(&next)
	if next.Group == "" {
		next.Group = current.Control.Group
	}
	if next.Env == "" {
		next.Env = current.Control.Env
	}
	next.Member = current.Control.Member
	if next.Group != current.Control.Group || next.Env != current.Control.Env {
		return nil, stateError(CodeInvalidTransition, current.Control.Group, current.Key, "cannot change stateful member group or env", ErrInvalidTransition)
	}
	next.Version = current.Control.Version + 1
	if next.Version <= 0 {
		next.Version = 1
	}
	next.UpdatedAt = canonical.Time(c.clock.Now())
	if err := validateStatefulMemberControl(next); err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(next)
	if err != nil {
		return nil, err
	}
	meta, err := c.store.CompareAndSwap(ctx, current.Key, current.ETag, body, putOptionsForStatefulMember(next))
	if err != nil {
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			return nil, stateError(CodePreconditionFailed, current.Control.Group, current.Key, "stateful member control ETag is stale", err)
		}
		return nil, err
	}
	return &StatefulMemberDocument{Key: current.Key, ETag: meta.ETag, Meta: *meta, Control: next}, nil
}

func (c *Client) AcquireStatefulMemberLease(ctx context.Context, group string, member int, opts StatefulMemberLeaseOptions) (*StatefulMemberLeaseHandle, *StatefulMemberDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, nil, err
	}
	if err := validateLeaseOptions(group, opts.Owner, opts.Duration); err != nil {
		return nil, nil, err
	}
	token := c.tokenGenerator()
	if token == "" {
		return nil, nil, stateError(CodeInvalidTransition, group, "", "lease token generator returned an empty token", ErrInvalidTransition)
	}
	current, err := c.GetStatefulMemberControl(ctx, group, member)
	if err != nil {
		return nil, nil, err
	}
	now := c.clock.Now()
	active, err := activeLease(current.Control.Lease, now)
	if err != nil {
		return nil, nil, stateError(CodeInvalidTransition, group, current.Key, err.Error(), err)
	}
	if active {
		return nil, nil, leaseHeldError(group, current.Key, current.Control.Lease)
	}
	next := current.Control
	next.Lease = &schema.Lease{Owner: opts.Owner, Token: token, Generation: current.Control.Generation + 1, ExpiresAt: canonical.Time(now.Add(opts.Duration))}
	next.UpdatedBy = actorForLease(opts.Actor, opts.Owner)
	next.TraceID = opts.TraceID
	updated, err := c.UpdateStatefulMemberControlCAS(ctx, current, next)
	if err != nil {
		return nil, nil, err
	}
	handle, err := statefulMemberHandleFromDocument(updated)
	if err != nil {
		return nil, nil, err
	}
	return handle, updated, nil
}

func (c *Client) UpdateStatefulMemberWithLeaseCAS(ctx context.Context, handle StatefulMemberLeaseHandle, mutate func(*schema.StatefulMemberControl) error) (*StatefulMemberLeaseHandle, *StatefulMemberDocument, error) {
	if mutate == nil {
		return nil, nil, stateError(CodeInvalidTransition, handle.Group, "", "stateful member mutation callback is required", ErrInvalidTransition)
	}
	current, err := c.GetStatefulMemberControl(ctx, handle.Group, handle.Member)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureStatefulMemberLeaseUsable(handle, current, c.clock.Now()); err != nil {
		return nil, nil, err
	}
	if current.ETag != handle.ETag {
		return nil, nil, stateError(CodePreconditionFailed, handle.Group, current.Key, "lease holder does not have latest stateful member ETag", ErrPreconditionFailed)
	}
	lease := *current.Control.Lease
	next := current.Control
	if err := mutate(&next); err != nil {
		return nil, nil, err
	}
	next.Lease = &lease
	updated, err := c.UpdateStatefulMemberControlCAS(ctx, current, next)
	if err != nil {
		return nil, nil, err
	}
	newHandle, err := statefulMemberHandleFromDocument(updated)
	if err != nil {
		return nil, nil, err
	}
	return newHandle, updated, nil
}

func (c *Client) ReleaseStatefulMemberLease(ctx context.Context, handle StatefulMemberLeaseHandle) (*StatefulMemberDocument, error) {
	current, err := c.GetStatefulMemberControl(ctx, handle.Group, handle.Member)
	if err != nil {
		return nil, err
	}
	if err := ensureStatefulMemberLeaseUsable(handle, current, c.clock.Now()); err != nil {
		return nil, err
	}
	next := current.Control
	next.Lease = nil
	return c.UpdateStatefulMemberControlCAS(ctx, current, next)
}

func normalizeStatefulGroupControl(control *schema.StatefulGroupControl) {
	if control.SchemaVersion == "" {
		control.SchemaVersion = schema.Version
	}
	if control.Version <= 0 {
		control.Version = 1
	}
}

func validateStatefulGroupControl(control schema.StatefulGroupControl) error {
	if control.SchemaVersion != schema.Version {
		return stateError(CodeInvalidTransition, control.Group, "", fmt.Sprintf("unsupported stateful group control schema version %q", control.SchemaVersion), ErrInvalidTransition)
	}
	if err := paths.ValidateName("stateful_group", control.Group); err != nil {
		return err
	}
	if err := paths.ValidateName("env", control.Env); err != nil {
		return err
	}
	if control.Replicas < 1 {
		return stateError(CodeInvalidTransition, control.Group, "", "replicas must be positive", ErrInvalidTransition)
	}
	if control.Version <= 0 {
		return stateError(CodeInvalidTransition, control.Group, "", "version must be positive", ErrInvalidTransition)
	}
	if control.Lease != nil {
		if err := validateLease(*control.Lease); err != nil {
			return err
		}
	}
	return nil
}

func putOptionsForStatefulGroup(control schema.StatefulGroupControl) objstore.PutOptions {
	return objstore.PutOptions{ContentType: canonical.ContentType, Metadata: map[string]string{"schema_version": control.SchemaVersion, "group": control.Group, "env": control.Env}}
}

func normalizeStatefulMemberControl(control *schema.StatefulMemberControl) {
	if control.SchemaVersion == "" {
		control.SchemaVersion = schema.Version
	}
	if control.Version <= 0 {
		control.Version = 1
	}
	if control.Generation <= 0 {
		control.Generation = 1
	}
	if control.Phase == "" {
		control.Phase = StatefulMemberReady
	}
}

func validateStatefulMemberControl(control schema.StatefulMemberControl) error {
	if control.SchemaVersion != schema.Version {
		return stateError(CodeInvalidTransition, control.Group, "", fmt.Sprintf("unsupported stateful member control schema version %q", control.SchemaVersion), ErrInvalidTransition)
	}
	if err := paths.ValidateName("stateful_group", control.Group); err != nil {
		return err
	}
	if err := paths.ValidateName("env", control.Env); err != nil {
		return err
	}
	if control.Member < 0 {
		return stateError(CodeInvalidTransition, control.Group, "", "member ordinal must be non-negative", ErrInvalidTransition)
	}
	if control.Generation <= 0 || control.Version <= 0 {
		return stateError(CodeInvalidTransition, control.Group, "", "generation and version must be positive", ErrInvalidTransition)
	}
	if control.Lease != nil {
		if err := validateLease(*control.Lease); err != nil {
			return err
		}
	}
	return nil
}

func putOptionsForStatefulMember(control schema.StatefulMemberControl) objstore.PutOptions {
	return objstore.PutOptions{ContentType: canonical.ContentType, Metadata: map[string]string{"schema_version": control.SchemaVersion, "group": control.Group, "env": control.Env, "member": fmt.Sprintf("%d", control.Member)}}
}

func statefulMemberHandleFromDocument(doc *StatefulMemberDocument) (*StatefulMemberLeaseHandle, error) {
	if doc == nil || doc.Control.Lease == nil {
		return nil, stateError(CodeLeaseLost, "", "", "stateful member control does not contain a lease", ErrLeaseLost)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, doc.Control.Lease.ExpiresAt)
	if err != nil {
		return nil, stateError(CodeInvalidTransition, doc.Control.Group, doc.Key, fmt.Sprintf("lease expires_at %q is invalid", doc.Control.Lease.ExpiresAt), err)
	}
	return &StatefulMemberLeaseHandle{Group: doc.Control.Group, Member: doc.Control.Member, Owner: doc.Control.Lease.Owner, Token: doc.Control.Lease.Token, Generation: doc.Control.Lease.Generation, ExpiresAt: expiresAt, ETag: doc.ETag}, nil
}

func ensureStatefulMemberLeaseUsable(handle StatefulMemberLeaseHandle, doc *StatefulMemberDocument, now time.Time) error {
	if doc == nil || doc.Control.Lease == nil {
		return stateError(CodeLeaseLost, handle.Group, "", "stateful member lease is missing", ErrLeaseLost)
	}
	if doc.Control.Lease.Token != handle.Token || doc.Control.Lease.Owner != handle.Owner {
		return stateError(CodeLeaseLost, handle.Group, doc.Key, "stateful member lease token does not match", ErrLeaseLost)
	}
	active, err := activeLease(doc.Control.Lease, now)
	if err != nil {
		return stateError(CodeInvalidTransition, handle.Group, doc.Key, err.Error(), err)
	}
	if !active {
		return stateError(CodeLeaseLost, handle.Group, doc.Key, "stateful member lease expired", ErrLeaseLost)
	}
	return nil
}
