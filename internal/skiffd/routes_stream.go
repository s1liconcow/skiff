package skiffd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	eventstream "github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const streamSubscriberBuffer = 32

func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "STREAM_UNSUPPORTED", "response writer does not support streaming", nil)
		return
	}
	filter, err := streamFilterFromRequest(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "EVENT_SCOPE_INVALID", err.Error(), nil)
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIMIT", err.Error(), nil)
		return
	}
	afterID := strings.TrimSpace(r.URL.Query().Get("after"))
	if afterID == "" {
		afterID = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = io.WriteString(w, ": skiff event stream\n\n")
	flusher.Flush()

	replay, err := s.streamReplayEvents(r.Context(), filter, limit, afterID)
	if err != nil {
		_ = writeSSE(w, "error", "", map[string]any{
			"ok":       false,
			"code":     "EVENT_REPLAY_FAILED",
			"summary":  err.Error(),
			"trace_id": traceIDFromContext(r.Context()),
		})
		flusher.Flush()
		return
	}
	for _, event := range replay {
		if err := writeSSE(w, "skiff.event", event.ID, event); err != nil {
			return
		}
		flusher.Flush()
	}
	if streamOnce(r) {
		return
	}

	sub, err := s.eventStream.Subscribe(r.Context(), filter, eventstream.SubscribeOptions{Buffer: streamSubscriberBuffer})
	if err != nil {
		_ = writeSSE(w, "error", "", map[string]any{
			"ok":       false,
			"code":     "EVENT_STREAM_FAILED",
			"summary":  err.Error(),
			"trace_id": traceIDFromContext(r.Context()),
		})
		flusher.Flush()
		return
	}
	defer sub.Close()

	for {
		select {
		case <-r.Context().Done():
			return
		case delivery, ok := <-sub.C:
			if !ok {
				return
			}
			if delivery.ResyncRequired {
				if err := writeSSE(w, "resync_required", delivery.LastEventID, map[string]any{
					"ok":            false,
					"code":          "RESYNC_REQUIRED",
					"summary":       "event stream subscriber fell behind; reconnect with the returned event id",
					"after":         delivery.LastEventID,
					"last_event_id": delivery.LastEventID,
					"trace_id":      traceIDFromContext(r.Context()),
				}); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			if delivery.Event.ID == "" || !eventMatchesStreamFilter(delivery.Event, filter) {
				continue
			}
			if err := writeSSE(w, "skiff.event", delivery.Event.ID, delivery.Event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) streamReplayEvents(ctx context.Context, filter eventstream.Filter, limit int, afterID string) ([]schema.Event, error) {
	prefixes, err := streamPrefixes(filter)
	if err != nil {
		return nil, err
	}
	var metas []objstore.ObjectMeta
	for _, prefix := range prefixes {
		listed, err := s.store.List(ctx, prefix, objstore.ListOptions{})
		if err != nil {
			return nil, err
		}
		metas = append(metas, listed...)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Key < metas[j].Key })
	seen := make(map[string]struct{}, len(metas))
	events := make([]schema.Event, 0, len(metas))
	for _, meta := range metas {
		if _, ok := seen[meta.Key]; ok {
			continue
		}
		seen[meta.Key] = struct{}{}
		if !streamEventKey(meta.Key) {
			continue
		}
		object, err := s.store.Get(ctx, meta.Key)
		if err != nil {
			return nil, err
		}
		var event schema.Event
		if err := canonical.UnmarshalStrict(object.Body, &event); err != nil {
			continue
		}
		if afterID != "" && event.ID <= afterID {
			continue
		}
		if !eventMatchesStreamFilter(event, filter) {
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time == events[j].Time {
			return events[i].ID < events[j].ID
		}
		return events[i].Time < events[j].Time
	})
	if limit > 0 && afterID == "" && limit < len(events) {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func streamFilterFromRequest(r *http.Request) (eventstream.Filter, error) {
	query := r.URL.Query()
	filter := eventstream.Filter{
		Kind:      eventstream.ScopeKind(strings.TrimSpace(query.Get("scope"))),
		Service:   strings.TrimSpace(query.Get("service")),
		Operation: strings.TrimSpace(query.Get("operation")),
		Saga:      strings.TrimSpace(query.Get("saga")),
	}
	if filter.Kind == "" {
		filter.Kind = "recent"
	}
	switch filter.Kind {
	case "recent", "all":
		return filter, nil
	case eventstream.ScopeService:
		if filter.Service == "" {
			return filter, fmt.Errorf("service event stream requires service")
		}
	case eventstream.ScopeOperation:
		if filter.Service == "" || filter.Operation == "" {
			return filter, fmt.Errorf("operation event stream requires service and operation")
		}
	case eventstream.ScopeSaga:
		if filter.Saga == "" {
			return filter, fmt.Errorf("saga event stream requires saga")
		}
	default:
		return filter, fmt.Errorf("unknown event scope %q", filter.Kind)
	}
	return filter, nil
}

func streamPrefixes(filter eventstream.Filter) ([]string, error) {
	switch filter.Kind {
	case "", "recent", "all":
		return []string{"services/", "sagas/"}, nil
	case eventstream.ScopeService:
		servicePrefix, err := paths.ServiceEventsPrefix(filter.Service)
		if err != nil {
			return nil, err
		}
		return []string{servicePrefix, "services/" + filter.Service + "/operations/"}, nil
	case eventstream.ScopeOperation:
		prefix, err := paths.OperationEventsPrefix(filter.Service, filter.Operation)
		if err != nil {
			return nil, err
		}
		return []string{prefix}, nil
	case eventstream.ScopeSaga:
		prefix, err := paths.SagaEventsPrefix(filter.Saga)
		if err != nil {
			return nil, err
		}
		return []string{prefix}, nil
	default:
		return nil, fmt.Errorf("unknown event scope %q", filter.Kind)
	}
}

func eventMatchesStreamFilter(event schema.Event, filter eventstream.Filter) bool {
	switch filter.Kind {
	case "", "recent", "all":
		return true
	case eventstream.ScopeService:
		return event.Subject.Kind == "service" && event.Subject.Name == filter.Service
	case eventstream.ScopeOperation:
		return (event.Subject.Kind == "operation" && event.Subject.Name == filter.Operation) ||
			(event.Subject.Kind == "service" && event.Subject.Name == filter.Service)
	case eventstream.ScopeSaga:
		return event.Subject.Kind == "saga" && event.Subject.Name == filter.Saga
	default:
		return false
	}
}

func streamEventKey(key string) bool {
	return strings.Contains(key, "/events/") && strings.HasSuffix(key, ".json")
}

func streamOnce(r *http.Request) bool {
	value := strings.TrimSpace(r.URL.Query().Get("once"))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func writeSSE(w io.Writer, eventType, id string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if eventType != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", eventType); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", body)
	return err
}
