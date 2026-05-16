package state

import (
	"context"
	"time"
)

type RetryOptions struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func defaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
	}
}

func (o RetryOptions) normalized() RetryOptions {
	defaults := defaultRetryOptions()
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = defaults.MaxAttempts
	}
	if o.BaseDelay < 0 {
		o.BaseDelay = 0
	}
	if o.BaseDelay == 0 && o.MaxAttempts > 1 {
		o.BaseDelay = defaults.BaseDelay
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = defaults.MaxDelay
	}
	if o.MaxDelay < o.BaseDelay {
		o.MaxDelay = o.BaseDelay
	}
	return o
}

func sleepBackoff(ctx context.Context, opts RetryOptions, attempt int) error {
	if attempt <= 0 || opts.BaseDelay == 0 {
		return ctx.Err()
	}
	delay := opts.BaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= opts.MaxDelay {
			delay = opts.MaxDelay
			break
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
