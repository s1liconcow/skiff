package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type RecordRequest struct {
	Actor         schema.Actor      `json:"actor"`
	Action        string            `json:"action"`
	Target        schema.Target     `json:"target"`
	TraceID       string            `json:"trace_id"`
	Risk          schema.Risk       `json:"risk,omitempty"`
	ApprovalID    string            `json:"approval_id,omitempty"`
	Summary       string            `json:"summary"`
	BeforeSummary string            `json:"before_summary,omitempty"`
	AfterSummary  string            `json:"after_summary,omitempty"`
	Data          map[string]string `json:"data,omitempty"`
}

func NewRecord(req RecordRequest, now time.Time, seed string) events.AuditRecord {
	record := events.NewAuditRecord(req.Actor, req.Target, req.Action, req.Summary, req.TraceID, now, seed)
	record.Risk = req.Risk
	record.ApprovalID = req.ApprovalID
	record.BeforeSummary = req.BeforeSummary
	record.AfterSummary = req.AfterSummary
	if len(req.Data) > 0 {
		record.Data = rawJSON(req.Data)
	}
	return record
}

func Append(ctx context.Context, store objstore.ObjectStore, req RecordRequest, now time.Time, seed string) (*events.AuditRecord, error) {
	log, err := events.NewLog(events.Options{Store: store, Clock: func() time.Time { return now }})
	if err != nil {
		return nil, err
	}
	record := NewRecord(req, now, seed)
	if _, err := log.AppendAudit(ctx, record); err != nil {
		return &record, err
	}
	return &record, nil
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return body
}
