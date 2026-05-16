package state

import (
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
)

func NewEventLog(store objstore.ObjectStore, clock func() time.Time) (*events.Log, error) {
	return events.NewLog(events.Options{Store: store, Clock: clock})
}
