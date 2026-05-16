package runner

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Identity struct {
	Provider   string `json:"provider,omitempty"`
	Region     string `json:"region,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	Zone       string `json:"zone,omitempty"`
	PrivateIP  string `json:"private_ip,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

type MetadataProvider interface {
	Identity(ctx context.Context) (Identity, error)
}

type StaticMetadataProvider struct {
	Value Identity
	Err   error
}

func (p StaticMetadataProvider) Identity(ctx context.Context) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if p.Err != nil {
		return Identity{}, p.Err
	}
	return p.Value, nil
}

type IdentityOptions struct {
	Attempts int
	Backoff  time.Duration
	Sleep    func(context.Context, time.Duration) error
}

func DiscoverIdentity(ctx context.Context, provider MetadataProvider, opts IdentityOptions) (Identity, error) {
	if provider == nil {
		return Identity{}, errors.New("metadata provider is required")
	}
	attempts := opts.Attempts
	if attempts < 1 {
		attempts = 1
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		identity, err := provider.Identity(ctx)
		if err == nil {
			return identity, nil
		}
		lastErr = err
		if attempt == attempts || opts.Backoff <= 0 {
			continue
		}
		if err := sleep(ctx, opts.Backoff); err != nil {
			return Identity{}, err
		}
	}
	return Identity{}, fmt.Errorf("discover instance identity after %d attempt(s): %w", attempts, lastErr)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
