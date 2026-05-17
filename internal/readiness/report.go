package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SchemaVersion = "skiff.readiness/v1"

type Report struct {
	SchemaVersion      string              `json:"schema_version"`
	OK                 bool                `json:"ok"`
	TraceID            string              `json:"trace_id,omitempty"`
	GeneratedAt        time.Time           `json:"generated_at"`
	Mode               string              `json:"mode,omitempty"`
	Provider           string              `json:"provider,omitempty"`
	Region             string              `json:"region,omitempty"`
	Summary            Summary             `json:"summary"`
	Scenarios          []Scenario          `json:"scenarios"`
	RecommendedActions []RecommendedAction `json:"recommended_actions,omitempty"`
}

type Options struct {
	TraceID     string
	GeneratedAt time.Time
	Mode        string
	Provider    string
	Region      string
}

type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type Scenario struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Category     string              `json:"category"`
	OK           bool                `json:"ok"`
	Facts        []Fact              `json:"facts,omitempty"`
	ObjectPaths  []string            `json:"object_paths,omitempty"`
	ProviderIDs  []string            `json:"provider_ids,omitempty"`
	OperationIDs []string            `json:"operation_ids,omitempty"`
	SagaIDs      []string            `json:"saga_ids,omitempty"`
	Commands     []RecommendedAction `json:"commands,omitempty"`
	Failure      string              `json:"failure,omitempty"`
}

type Fact struct {
	Type    string `json:"type"`
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

type RecommendedAction struct {
	ID            string `json:"id"`
	Command       string `json:"command"`
	Mutating      bool   `json:"mutating"`
	Safety        string `json:"safety,omitempty"`
	Reversibility string `json:"reversibility,omitempty"`
	Risk          string `json:"risk,omitempty"`
}

func New(opts Options) *Report {
	generatedAt := opts.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	return &Report{
		SchemaVersion: SchemaVersion,
		OK:            true,
		TraceID:       opts.TraceID,
		GeneratedAt:   generatedAt.UTC(),
		Mode:          opts.Mode,
		Provider:      opts.Provider,
		Region:        opts.Region,
	}
}

func (r *Report) AddScenario(s Scenario) {
	if s.ID == "" {
		s.ID = fmt.Sprintf("scenario_%d", len(r.Scenarios)+1)
	}
	r.Scenarios = append(r.Scenarios, s)
	if !s.OK {
		r.OK = false
	}
}

func (r *Report) AddAction(action RecommendedAction) {
	if action.ID == "" || action.Command == "" {
		return
	}
	r.RecommendedActions = append(r.RecommendedActions, action)
}

func (r *Report) Finalize() {
	r.OK = true
	r.Summary = Summary{Total: len(r.Scenarios)}
	for _, scenario := range r.Scenarios {
		if scenario.OK {
			r.Summary.Passed++
			continue
		}
		r.Summary.Failed++
		r.OK = false
	}
}

func (r *Report) WriteJSON(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	r.Finalize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

func (r Report) Markdown() string {
	r.Finalize()
	var b strings.Builder
	fmt.Fprintf(&b, "# Production Readiness Report\n\n")
	fmt.Fprintf(&b, "- ok: %t\n", r.OK)
	fmt.Fprintf(&b, "- scenarios: %d passed, %d failed, %d total\n", r.Summary.Passed, r.Summary.Failed, r.Summary.Total)
	if r.TraceID != "" {
		fmt.Fprintf(&b, "- trace_id: %s\n", r.TraceID)
	}
	for _, scenario := range r.Scenarios {
		status := "pass"
		if !scenario.OK {
			status = "fail"
		}
		fmt.Fprintf(&b, "\n## %s\n\n- status: %s\n- category: %s\n", scenario.Title, status, scenario.Category)
		if scenario.Failure != "" {
			fmt.Fprintf(&b, "- failure: %s\n", scenario.Failure)
		}
		for _, fact := range scenario.Facts {
			fmt.Fprintf(&b, "- %s: %s\n", firstNonEmpty(fact.Type, "fact"), fact.Message)
		}
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
