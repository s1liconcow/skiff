package worker

import (
	"context"
	"errors"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	defaultOwner         = "skiff-worker"
	defaultLeaseDuration = 30 * time.Second
	defaultPollInterval  = 10 * time.Second
)

type Worker struct {
	Store         objstore.ObjectStore
	Provider      provider.Provider
	SagaSteps     map[string]steps.Step
	Owner         string
	Actor         schema.Actor
	LeaseDuration time.Duration
	PollInterval  time.Duration
	Clock         func() time.Time
	Sleep         func(context.Context, time.Duration) error
}

type RunResult struct {
	OperationResumed int      `json:"operation_resumed"`
	OperationSkipped int      `json:"operation_skipped"`
	SagaResumed      int      `json:"saga_resumed"`
	SagaSkipped      int      `json:"saga_skipped"`
	Errors           []string `json:"errors,omitempty"`
}

func (w Worker) Run(ctx context.Context) error {
	if err := w.validate(); err != nil {
		return err
	}
	sleep := w.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	for {
		if _, err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
}

func (w Worker) RunOnce(ctx context.Context) (*RunResult, error) {
	if err := w.validate(); err != nil {
		return nil, err
	}
	result := &RunResult{}
	operations := ops.NewStore(w.Store, ops.WithClock(w.now))
	items, err := operations.List(ctx, ops.ListOptions{IncludeTerminal: false})
	if err != nil {
		return result, err
	}
	for _, item := range items {
		if !item.Resumable {
			result.OperationSkipped++
			continue
		}
		_, err := (ops.Resumer{Store: w.Store, Provider: w.Provider, Clock: w.now}).Resume(ctx, ops.ResumeRequest{
			Service:       item.Service,
			OperationID:   item.OperationID,
			Actor:         w.actor(),
			TraceID:       item.TraceID,
			Owner:         w.owner(),
			LeaseDuration: w.leaseDuration(),
			Takeover:      true,
		})
		if err != nil {
			if errors.Is(err, state.ErrLeaseHeld) || errors.Is(err, state.ErrPreconditionFailed) {
				result.OperationSkipped++
				continue
			}
			result.Errors = append(result.Errors, err.Error())
			continue
		}
		result.OperationResumed++
	}
	sagaResult, err := w.resumeSagas(ctx)
	if err != nil {
		return result, err
	}
	result.SagaResumed += sagaResult.SagaResumed
	result.SagaSkipped += sagaResult.SagaSkipped
	result.Errors = append(result.Errors, sagaResult.Errors...)
	return result, nil
}

func (w Worker) resumeSagas(ctx context.Context) (*RunResult, error) {
	result := &RunResult{}
	metas, err := w.Store.List(ctx, "sagas/", objstore.ListOptions{})
	if err != nil {
		return result, err
	}
	sagas := sagastate.NewStore(w.Store, sagastate.WithClock(w.now))
	for _, meta := range metas {
		sagaID, ok := parseSagaControlKey(meta.Key)
		if !ok {
			continue
		}
		control, err := sagas.GetControl(ctx, sagaID)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			continue
		}
		if terminalSaga(control.Control.Status) {
			continue
		}
		_, err = (&sagastate.Executor{
			Store:         sagas,
			Steps:         w.SagaSteps,
			Owner:         w.owner(),
			LeaseDuration: w.leaseDuration(),
		}).Execute(ctx, sagaID)
		if err != nil {
			if errors.Is(err, state.ErrLeaseHeld) || errors.Is(err, state.ErrPreconditionFailed) {
				result.SagaSkipped++
				continue
			}
			result.Errors = append(result.Errors, err.Error())
			continue
		}
		result.SagaResumed++
	}
	return result, nil
}

func (w Worker) validate() error {
	if w.Store == nil {
		return errors.New("object store is required")
	}
	if w.Provider == nil {
		return errors.New("provider is required")
	}
	return nil
}

func (w Worker) owner() string {
	if w.Owner != "" {
		return w.Owner
	}
	return defaultOwner
}

func (w Worker) actor() schema.Actor {
	if w.Actor.ID != "" {
		actor := w.Actor
		if actor.Type == "" {
			actor.Type = "agent"
		}
		return actor
	}
	return schema.Actor{ID: w.owner(), Type: "agent"}
}

func (w Worker) leaseDuration() time.Duration {
	if w.LeaseDuration > 0 {
		return w.LeaseDuration
	}
	return defaultLeaseDuration
}

func (w Worker) now() time.Time {
	if w.Clock != nil {
		return w.Clock().UTC()
	}
	return time.Now().UTC()
}

func parseSagaControlKey(key string) (string, bool) {
	const prefix = "sagas/"
	const suffix = "/control.json"
	if len(key) <= len(prefix)+len(suffix) || key[:len(prefix)] != prefix || key[len(key)-len(suffix):] != suffix {
		return "", false
	}
	sagaID := key[len(prefix) : len(key)-len(suffix)]
	if sagaID == "" || containsSlash(sagaID) {
		return "", false
	}
	return sagaID, true
}

func containsSlash(value string) bool {
	for _, r := range value {
		if r == '/' {
			return true
		}
	}
	return false
}

func terminalSaga(status schema.SagaStatus) bool {
	switch status {
	case schema.SagaSucceeded, schema.SagaFailed, schema.SagaCanceled:
		return true
	default:
		return false
	}
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
