package cost

import "testing"

func TestAnalyzeOverprovisionedService(t *testing.T) {
	cpu := 18.0
	mem := 41.0
	warm := 8
	result := Analyze(Input{
		Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "medium", MinReplicas: 12, MaxReplicas: 24, LogsEnabled: true, MetricsEnabled: true},
		Signals: ObservedSignals{
			CPUP95Percent:    &cpu,
			MemoryP95Percent: &mem,
			WarmCapacity:     &warm,
		},
	})
	assertRecommendation(t, result.Recommendations, "cost.shape.downsize", ConfidenceHigh)
	assertRecommendation(t, result.Recommendations, "cost.replicas.reduce_min", ConfidenceMedium)
}

func TestAnalyzeUnderprovisionedService(t *testing.T) {
	cpu := 86.0
	mem := 68.0
	unhealthy := 2
	result := Analyze(Input{
		Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "small", MinReplicas: 3, MaxReplicas: 3},
		Signals: ObservedSignals{
			CPUP95Percent:    &cpu,
			MemoryP95Percent: &mem,
			UnhealthyTargets: &unhealthy,
		},
	})
	assertRecommendation(t, result.Recommendations, "cost.shape.upsize", ConfidenceHigh)
	assertRecommendation(t, result.Recommendations, "cost.warm_capacity.add_headroom", ConfidenceMedium)
}

func TestAnalyzeNoisyLogs(t *testing.T) {
	logs := 1400.0
	result := Analyze(Input{
		Shape:   ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "small", MinReplicas: 2, MaxReplicas: 4},
		Signals: ObservedSignals{LogMegabytesPerHour: &logs},
	})
	rec := findRecommendation(result.Recommendations, "cost.logs.noisy")
	if rec == nil {
		t.Fatalf("missing noisy log recommendation: %+v", result.Recommendations)
	}
	if rec.Severity != SeverityMedium || rec.Confidence != ConfidenceHigh {
		t.Fatalf("unexpected log recommendation: %+v", *rec)
	}
}

func TestPlanWarningsFlagExpensiveDefaults(t *testing.T) {
	warnings := PlanWarnings(Input{Shape: ServiceShape{Service: "payments-api", Env: "prod", MachineSize: "large", MinReplicas: 8, MaxReplicas: 8}})
	assertRecommendation(t, warnings, "cost.plan.high_min_replicas", ConfidenceLow)
	assertRecommendation(t, warnings, "cost.plan.large_shape", ConfidenceLow)
	assertRecommendation(t, warnings, "cost.plan.fixed_warm_capacity", ConfidenceLow)
}

func assertRecommendation(t *testing.T, recs []Recommendation, id, confidence string) {
	t.Helper()
	rec := findRecommendation(recs, id)
	if rec == nil {
		t.Fatalf("missing recommendation %s in %+v", id, recs)
	}
	if rec.Confidence != confidence {
		t.Fatalf("%s confidence = %q, want %q", id, rec.Confidence, confidence)
	}
	if rec.Summary == "" || len(rec.Evidence) == 0 {
		t.Fatalf("%s lacks summary or evidence: %+v", id, *rec)
	}
}

func findRecommendation(recs []Recommendation, id string) *Recommendation {
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}
