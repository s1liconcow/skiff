package events

import (
	"context"
	"sync"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

const DefaultSubscriberBuffer = 16

type Filter struct {
	Kind      ScopeKind
	Service   string
	Operation string
	Saga      string
}

type SubscribeOptions struct {
	Buffer int
}

type Delivery struct {
	Event          schema.Event `json:"event,omitempty"`
	ResyncRequired bool         `json:"resync_required,omitempty"`
	LastEventID    string       `json:"last_event_id,omitempty"`
}

type Subscription struct {
	C      <-chan Delivery
	cancel func()
}

func (s *Subscription) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

type Bus struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]*subscriber
}

type subscriber struct {
	filter Filter
	ch     chan Delivery
}

func NewBus() *Bus {
	return &Bus{subscribers: make(map[uint64]*subscriber)}
}

func (b *Bus) Publish(ctx context.Context, event schema.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subscribers {
		if !matchesFilter(event, sub.filter) {
			continue
		}
		deliverNonBlocking(sub.ch, Delivery{Event: event})
	}
	return nil
}

func (b *Bus) Subscribe(ctx context.Context, filter Filter, opts SubscribeOptions) (*Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = DefaultSubscriberBuffer
	}
	ch := make(chan Delivery, buffer)
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	if b.subscribers == nil {
		b.subscribers = make(map[uint64]*subscriber)
	}
	b.subscribers[id] = &subscriber{filter: filter, ch: ch}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(ch)
		}
		b.mu.Unlock()
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return &Subscription{C: ch, cancel: cancel}, nil
}

func deliverNonBlocking(ch chan Delivery, delivery Delivery) {
	select {
	case ch <- delivery:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	resync := Delivery{ResyncRequired: true, LastEventID: delivery.Event.ID}
	select {
	case ch <- resync:
	default:
	}
}

func matchesFilter(event schema.Event, filter Filter) bool {
	switch filter.Kind {
	case "", "recent", "all":
		return true
	case ScopeService:
		return filter.Service == "" || (event.Subject.Kind == "service" && event.Subject.Name == filter.Service)
	case ScopeOperation:
		if filter.Operation != "" && event.Subject.Kind == "operation" && event.Subject.Name == filter.Operation {
			return true
		}
		return filter.Service != "" && event.Subject.Kind == "service" && event.Subject.Name == filter.Service
	case ScopeSaga:
		return filter.Saga == "" || (event.Subject.Kind == "saga" && event.Subject.Name == filter.Saga)
	default:
		return false
	}
}
