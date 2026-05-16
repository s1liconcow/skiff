package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

type TokenGenerator func() string

type Client struct {
	store          objstore.ObjectStore
	clock          Clock
	tokenGenerator TokenGenerator
	retry          RetryOptions
}

type Option func(*Client)

func NewClient(store objstore.ObjectStore, opts ...Option) *Client {
	client := &Client{
		store:          store,
		clock:          realClock{},
		tokenGenerator: randomLeaseToken,
		retry:          defaultRetryOptions(),
	}
	for _, opt := range opts {
		opt(client)
	}
	client.retry = client.retry.normalized()
	return client
}

func WithClock(clock Clock) Option {
	return func(client *Client) {
		if clock != nil {
			client.clock = clock
		}
	}
}

func WithTokenGenerator(generator TokenGenerator) Option {
	return func(client *Client) {
		if generator != nil {
			client.tokenGenerator = generator
		}
	}
}

func WithRetryOptions(opts RetryOptions) Option {
	return func(client *Client) {
		client.retry = opts
	}
}

type ServiceControlDocument struct {
	Key     string                `json:"key"`
	ETag    string                `json:"etag"`
	Meta    objstore.ObjectMeta   `json:"meta"`
	Control schema.ServiceControl `json:"control"`
}

func GetServiceControl(ctx context.Context, store objstore.ObjectStore, service string) (*ServiceControlDocument, error) {
	return NewClient(store).GetServiceControl(ctx, service)
}

func CreateServiceControl(ctx context.Context, store objstore.ObjectStore, control schema.ServiceControl) (*ServiceControlDocument, error) {
	return NewClient(store).CreateServiceControl(ctx, control)
}

func UpdateServiceControlCAS(ctx context.Context, store objstore.ObjectStore, current *ServiceControlDocument, next schema.ServiceControl) (*ServiceControlDocument, error) {
	return NewClient(store).UpdateServiceControlCAS(ctx, current, next)
}

func (c *Client) GetServiceControl(ctx context.Context, service string) (*ServiceControlDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	key, err := paths.ServiceControl(service)
	if err != nil {
		return nil, err
	}
	obj, err := c.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var control schema.ServiceControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return nil, stateError(CodeInvalidTransition, service, key, "service control document is not valid canonical schema", err)
	}
	if err := validateServiceControl(control); err != nil {
		return nil, err
	}
	if control.Service != service {
		return nil, stateError(CodeInvalidTransition, service, key, fmt.Sprintf("service control names service %q", control.Service), nil)
	}
	meta := objectMetaFromObject(obj)
	return &ServiceControlDocument{
		Key:     key,
		ETag:    meta.ETag,
		Meta:    meta,
		Control: control,
	}, nil
}

func (c *Client) CreateServiceControl(ctx context.Context, control schema.ServiceControl) (*ServiceControlDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	if control.UpdatedAt == "" {
		control.UpdatedAt = canonical.Time(c.clock.Now())
	}
	normalizeServiceControl(&control)
	if err := validateServiceControl(control); err != nil {
		return nil, err
	}
	key, err := paths.ServiceControl(control.Service)
	if err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return nil, err
	}
	meta, err := c.store.Create(ctx, key, body, putOptionsForServiceControl(control))
	if err != nil {
		return nil, err
	}
	return &ServiceControlDocument{
		Key:     key,
		ETag:    meta.ETag,
		Meta:    *meta,
		Control: control,
	}, nil
}

func (c *Client) UpdateServiceControlCAS(ctx context.Context, current *ServiceControlDocument, next schema.ServiceControl) (*ServiceControlDocument, error) {
	if err := c.requireStore(); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, stateError(CodeInvalidTransition, "", "", "current service control document is required", ErrInvalidTransition)
	}
	normalizeServiceControl(&next)
	if next.Service == "" {
		next.Service = current.Control.Service
	}
	if next.Env == "" {
		next.Env = current.Control.Env
	}
	if next.Service != current.Control.Service {
		return nil, stateError(CodeInvalidTransition, current.Control.Service, current.Key, fmt.Sprintf("cannot change service from %q to %q", current.Control.Service, next.Service), ErrInvalidTransition)
	}
	if next.Env != current.Control.Env {
		return nil, stateError(CodeInvalidTransition, current.Control.Service, current.Key, fmt.Sprintf("cannot change env from %q to %q", current.Control.Env, next.Env), ErrInvalidTransition)
	}
	next.Version = current.Control.Version + 1
	if next.Version <= 0 {
		next.Version = 1
	}
	next.UpdatedAt = canonical.Time(c.clock.Now())
	if err := validateServiceControl(next); err != nil {
		return nil, err
	}
	body, err := canonical.Marshal(next)
	if err != nil {
		return nil, err
	}
	meta, err := c.store.CompareAndSwap(ctx, current.Key, current.ETag, body, putOptionsForServiceControl(next))
	if err != nil {
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			return nil, stateError(CodePreconditionFailed, current.Control.Service, current.Key, "service control ETag is stale", err)
		}
		return nil, err
	}
	return &ServiceControlDocument{
		Key:     current.Key,
		ETag:    meta.ETag,
		Meta:    *meta,
		Control: next,
	}, nil
}

func (c *Client) requireStore() error {
	if c == nil || c.store == nil {
		return stateError(CodeInvalidTransition, "", "", "object store is required", ErrInvalidTransition)
	}
	return nil
}

func normalizeServiceControl(control *schema.ServiceControl) {
	if control.SchemaVersion == "" {
		control.SchemaVersion = schema.Version
	}
	if control.Version <= 0 {
		control.Version = 1
	}
}

func validateServiceControl(control schema.ServiceControl) error {
	if control.SchemaVersion != schema.Version {
		return stateError(CodeInvalidTransition, control.Service, "", fmt.Sprintf("unsupported service control schema version %q", control.SchemaVersion), ErrInvalidTransition)
	}
	if err := paths.ValidateName("service", control.Service); err != nil {
		return err
	}
	if err := paths.ValidateName("env", control.Env); err != nil {
		return err
	}
	if control.Version <= 0 {
		return stateError(CodeInvalidTransition, control.Service, "", "service control version must be positive", ErrInvalidTransition)
	}
	if control.Lease != nil {
		if err := validateLease(*control.Lease); err != nil {
			return err
		}
	}
	return nil
}

func putOptionsForServiceControl(control schema.ServiceControl) objstore.PutOptions {
	return objstore.PutOptions{
		ContentType: canonical.ContentType,
		Metadata: map[string]string{
			"schema_version": control.SchemaVersion,
			"service":        control.Service,
			"env":            control.Env,
		},
	}
}

func objectMetaFromObject(obj *objstore.Object) objstore.ObjectMeta {
	return objstore.ObjectMeta{
		Key:         obj.Key,
		ETag:        obj.ETag,
		VersionID:   obj.VersionID,
		Size:        obj.Size,
		ContentType: obj.ContentType,
		Metadata:    obj.Metadata,
		CreatedAt:   obj.CreatedAt,
		UpdatedAt:   obj.UpdatedAt,
	}
}

func randomLeaseToken() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("lease_%d", time.Now().UnixNano())
	}
	return "lease_" + hex.EncodeToString(bytes[:])
}
