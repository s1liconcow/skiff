package observability

import (
	"sort"
	"time"
)

type MetricIdentity struct {
	Service  string `json:"service"`
	Env      string `json:"env"`
	Release  string `json:"release,omitempty"`
	Instance string `json:"instance,omitempty"`
	Region   string `json:"region,omitempty"`
	Zone     string `json:"zone,omitempty"`
}

type MetricSeries struct {
	Name     string            `json:"name"`
	Category string            `json:"category,omitempty"`
	Source   string            `json:"source,omitempty"`
	Unit     string            `json:"unit,omitempty"`
	Identity MetricIdentity    `json:"identity"`
	Labels   map[string]string `json:"labels,omitempty"`
	Points   []MetricPoint     `json:"points,omitempty"`
}

type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

func SortMetricSeries(series []MetricSeries) {
	for i := range series {
		sort.SliceStable(series[i].Points, func(a, b int) bool {
			return series[i].Points[a].Timestamp.Before(series[i].Points[b].Timestamp)
		})
	}
	sort.SliceStable(series, func(i, j int) bool {
		if series[i].Name == series[j].Name {
			return series[i].Source < series[j].Source
		}
		return series[i].Name < series[j].Name
	})
}
