package observability

import (
	"sort"
	"time"
)

type LogIdentity struct {
	Service  string `json:"service"`
	Env      string `json:"env"`
	Release  string `json:"release,omitempty"`
	Instance string `json:"instance,omitempty"`
	Region   string `json:"region,omitempty"`
	Zone     string `json:"zone,omitempty"`
}

type LogRecord struct {
	Timestamp time.Time         `json:"timestamp"`
	Message   string            `json:"message"`
	Source    string            `json:"source,omitempty"`
	Identity  LogIdentity       `json:"identity"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func SortLogs(records []LogRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Timestamp.Equal(records[j].Timestamp) {
			return records[i].Source < records[j].Source
		}
		return records[i].Timestamp.Before(records[j].Timestamp)
	})
}
