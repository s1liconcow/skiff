package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const SchemaVersion = "skiff.event/v1"

type ScopeKind string

const (
	ScopeService   ScopeKind = "service"
	ScopeOperation ScopeKind = "operation"
	ScopeSaga      ScopeKind = "saga"
	ScopeAudit     ScopeKind = "audit"
)

type Event struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Time          string          `json:"time"`
	TraceID       string          `json:"trace_id,omitempty"`
	Scope         Scope           `json:"scope"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity,omitempty"`
	Actor         *schema.Actor   `json:"actor,omitempty"`
	Summary       string          `json:"summary"`
	Facts         []schema.Fact   `json:"facts,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
	HashChain     *HashChain      `json:"hash_chain,omitempty"`
}

type Scope struct {
	Kind      ScopeKind `json:"kind"`
	Service   string    `json:"service,omitempty"`
	Operation string    `json:"operation,omitempty"`
	Saga      string    `json:"saga,omitempty"`
}

type HashChain struct {
	PreviousEventID string `json:"previous_event_id,omitempty"`
	PreviousHash    string `json:"previous_hash,omitempty"`
	Hash            string `json:"hash"`
}

type AuditRecord struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Time          string          `json:"time"`
	TraceID       string          `json:"trace_id"`
	Actor         schema.Actor    `json:"actor"`
	Action        string          `json:"action"`
	Target        schema.Target   `json:"target"`
	Risk          schema.Risk     `json:"risk,omitempty"`
	ApprovalID    string          `json:"approval_id,omitempty"`
	Summary       string          `json:"summary"`
	BeforeSummary string          `json:"before_summary,omitempty"`
	AfterSummary  string          `json:"after_summary,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

func NewID(t time.Time, seed string) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	millis := uint64(t.UTC().UnixNano() / int64(time.Millisecond))
	prefix := encodeBase32(millis, 10)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", t.UTC().UnixNano(), seed)))
	return prefix + encodeBytesBase32(sum[:], 16)
}

func NewServiceEvent(service, eventType, summary string, t time.Time, seed string) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		ID:            NewID(t, seed),
		Time:          canonical.Time(t),
		Scope:         Scope{Kind: ScopeService, Service: service},
		Type:          eventType,
		Summary:       summary,
	}
}

func NewOperationEvent(service, operation, eventType, summary string, t time.Time, seed string) Event {
	event := NewServiceEvent(service, eventType, summary, t, seed)
	event.Scope = Scope{Kind: ScopeOperation, Service: service, Operation: operation}
	return event
}

func NewSagaEvent(saga, eventType, summary string, t time.Time, seed string) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		ID:            NewID(t, seed),
		Time:          canonical.Time(t),
		Scope:         Scope{Kind: ScopeSaga, Saga: saga},
		Type:          eventType,
		Summary:       summary,
	}
}

func NewAuditRecord(actor schema.Actor, target schema.Target, action, summary, traceID string, t time.Time, seed string) AuditRecord {
	return AuditRecord{
		SchemaVersion: SchemaVersion,
		ID:            NewID(t, seed),
		Time:          canonical.Time(t),
		TraceID:       traceID,
		Actor:         actor,
		Action:        action,
		Target:        target,
		Summary:       summary,
	}
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func encodeBase32(value uint64, width int) string {
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = crockford[value&31]
		value >>= 5
	}
	return string(out)
}

func encodeBytesBase32(body []byte, width int) string {
	out := make([]byte, 0, width)
	var buffer uint32
	var bits uint
	for _, b := range body {
		buffer = (buffer << 8) | uint32(b)
		bits += 8
		for bits >= 5 && len(out) < width {
			bits -= 5
			out = append(out, crockford[(buffer>>bits)&31])
		}
		if len(out) == width {
			return string(out)
		}
	}
	if len(out) < width && bits > 0 {
		out = append(out, crockford[(buffer<<(5-bits))&31])
	}
	for len(out) < width {
		out = append(out, '0')
	}
	return string(out)
}

func hexSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
