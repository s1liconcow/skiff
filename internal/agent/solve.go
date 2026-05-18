package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type SolveOptions struct {
	Goal    string
	Service string
	TraceID string
	Binary  string
}

func Solve(result doctor.Result, opts SolveOptions) ActionGraph {
	goal := firstNonEmpty(opts.Goal, GoalRestoreHealth)
	service := firstNonEmpty(opts.Service, result.Service)
	traceID := firstNonEmpty(opts.TraceID, result.TraceID)
	binary := firstNonEmpty(opts.Binary, binaryFromActions(result.RecommendedActions), "skiff")

	graph := ActionGraph{
		TraceID:    traceID,
		Goal:       goal,
		Confidence: confidence(result),
		Service:    service,
		Health:     result.Health,
		Source:     result.Source,
		Facts:      append([]doctor.Evidence(nil), result.Facts...),
		Findings:   append([]doctor.Finding(nil), result.Findings...),
		Hypotheses: append([]doctor.Hypothesis(nil), result.Hypotheses...),
		Steps:      []ActionStep{},
	}

	actions := append([]doctor.RecommendedAction(nil), result.RecommendedActions...)
	sort.SliceStable(actions, func(i, j int) bool {
		return actionLess(actions[i], actions[j])
	})

	readOnlyIDs := make([]string, 0, len(actions))
	mutatingIDs := make([]string, 0, len(actions))
	stepIDs := make(map[string]struct{}, len(actions)+1)
	for _, action := range actions {
		step := stepFromAction(action, service)
		if step.ID == "" || step.Command == "" {
			continue
		}
		step.ID = uniqueStepID(step.ID, stepIDs)
		if step.Mutating {
			step.Requires = append(step.Requires, readOnlyIDs...)
			step.Requires = append(step.Requires, mutatingIDs...)
			step.ExpectedValidation = append(step.ExpectedValidation, validationForService(step.ID+"_health", service, binary))
			mutatingIDs = append(mutatingIDs, step.ID)
		} else {
			readOnlyIDs = append(readOnlyIDs, step.ID)
		}
		graph.Steps = append(graph.Steps, step)
	}

	if len(graph.Steps) == 0 && len(result.Findings) > 0 {
		step := inspectDoctorStep(service, binary)
		step.ID = uniqueStepID(step.ID, stepIDs)
		graph.Steps = append(graph.Steps, step)
	}

	if len(mutatingIDs) > 0 {
		verify := verifyHealthStep(service, binary, mutatingIDs)
		verify.ID = uniqueStepID(verify.ID, stepIDs)
		graph.Steps = append(graph.Steps, verify)
	}

	graph.Status = statusForGraph(graph)
	return graph
}

func stepFromAction(action doctor.RecommendedAction, defaultService string) ActionStep {
	service := firstNonEmpty(action.Service, defaultService)
	kind := firstNonEmpty(action.Kind, "command")
	risk := riskForAction(action)
	reversibility := reversibilityForAction(action)
	requiresApproval := requiresApproval(action, risk, reversibility)
	command := strings.TrimSpace(action.Command)
	if requiresApproval {
		command = stripAssumeYes(command)
	}
	step := ActionStep{
		ID:               firstNonEmpty(action.ID, compactID(service, action.Kind), compactID(service, "action")),
		Kind:             kind,
		Service:          service,
		Summary:          action.Summary,
		Command:          command,
		APIOperation:     apiOperationForAction(action, service),
		Mutating:         action.Mutating,
		Safety:           safetyForAction(action),
		Risk:             risk,
		Reversibility:    reversibility,
		Reversible:       isReversible(reversibility),
		RequiresApproval: requiresApproval,
		Requires:         []string{},
		SourceActionID:   action.ID,
	}
	if step.APIOperation == nil {
		step.APIOperation = &APIOperation{
			Operation: firstNonEmpty(kind, "command.run"),
			Target:    schema.Target{Kind: "service", Name: service},
			Mutating:  action.Mutating,
		}
	}
	return step
}

func inspectDoctorStep(service, binary string) ActionStep {
	return ActionStep{
		ID:               compactID(service, "inspect_doctor"),
		Kind:             "command",
		Service:          service,
		Summary:          "rerun doctor with a fresh read before choosing a mutating action",
		Command:          fmt.Sprintf("%s doctor %s --fresh --format json", binary, service),
		APIOperation:     doctorAPIOperation(service),
		Mutating:         false,
		Safety:           "read-only observation; no durable state change",
		Risk:             schema.RiskLow,
		Reversibility:    schema.Reversible,
		Reversible:       true,
		RequiresApproval: false,
		Requires:         []string{},
	}
}

func verifyHealthStep(service, binary string, requires []string) ActionStep {
	return ActionStep{
		ID:                 "verify_health",
		Kind:               "command",
		Service:            service,
		Summary:            "verify service health after mutating remediation",
		Command:            fmt.Sprintf("%s doctor %s --fresh --format json", binary, service),
		APIOperation:       doctorAPIOperation(service),
		Mutating:           false,
		Safety:             "read-only validation; no durable state change",
		Risk:               schema.RiskLow,
		Reversibility:      schema.Reversible,
		Reversible:         true,
		RequiresApproval:   false,
		Requires:           append([]string(nil), requires...),
		ExpectedValidation: []ExpectedValidation{validationForService("service_health_restored", service, binary)},
	}
}

func validationForService(id, service, binary string) ExpectedValidation {
	return ExpectedValidation{
		ID:               id,
		Command:          fmt.Sprintf("%s doctor %s --fresh --format json", binary, service),
		Summary:          "confirm doctor no longer reports high or critical findings for the service",
		SuccessCondition: "doctor.health is nominal or warning and doctor.findings contains no high or critical severity findings",
	}
}

func apiOperationForAction(action doctor.RecommendedAction, service string) *APIOperation {
	command := " " + strings.ToLower(action.Command) + " "
	id := strings.ToLower(action.ID)
	params := map[string]string{}
	operation := ""
	targetKind := "service"

	switch {
	case strings.Contains(command, " stateful status "):
		operation = "stateful.status"
		targetKind = "stateful-group"
		params["fresh"] = "true"
	case strings.Contains(command, " stateful logs "):
		operation = "stateful.logs"
		targetKind = "stateful-group"
		params["since"] = firstNonEmpty(flagValue(action.Command, "since"), "20m")
		if member := flagValue(action.Command, "member"); member != "" {
			params["member"] = member
		}
	case strings.Contains(command, " stateful metrics "):
		operation = "stateful.metrics"
		targetKind = "stateful-group"
		params["since"] = firstNonEmpty(flagValue(action.Command, "since"), "20m")
		if member := flagValue(action.Command, "member"); member != "" {
			params["member"] = member
		}
	case strings.Contains(command, " stateful snapshot "):
		operation = "stateful.snapshot"
		targetKind = "stateful-group"
		params["member"] = flagValue(action.Command, "member")
	case strings.Contains(command, " stateful replace-member "):
		operation = "stateful.replace_member"
		targetKind = "stateful-member"
		params["member"] = flagValue(action.Command, "member")
	case strings.Contains(command, " stateful resume "):
		operation = "stateful.resume"
		targetKind = "stateful-group"
	case strings.Contains(id, "inspect_status") || strings.Contains(command, " status "):
		operation = "status.get"
		params["fresh"] = "true"
	case strings.Contains(id, "inspect_events") || strings.Contains(command, " events "):
		operation = "events.list"
		params["scope"] = firstNonEmpty(flagValue(action.Command, "scope"), "service")
		params["limit"] = firstNonEmpty(flagValue(action.Command, "limit"), "20")
		params["fresh"] = "true"
	case strings.Contains(id, "inspect_logs") || strings.Contains(command, " logs "):
		operation = "logs.query"
		params["since"] = firstNonEmpty(flagValue(action.Command, "since"), "20m")
	case strings.Contains(id, "inspect_metrics") || strings.Contains(command, " metrics "):
		operation = "metrics.query"
		params["since"] = firstNonEmpty(flagValue(action.Command, "since"), "20m")
	case strings.Contains(id, "watch_rollout") || strings.Contains(command, " rollout watch "):
		operation = "rollout.watch"
		params["operation_id"] = flagValue(action.Command, "operation")
		if providerID := flagValue(action.Command, "provider-id"); providerID != "" {
			params["provider_id"] = providerID
		}
	case strings.Contains(id, "rollback") || strings.Contains(command, " rollback "):
		operation = "rollback.start"
		params["target_release"] = flagValue(action.Command, "to")
	case strings.Contains(id, "doctor") || strings.Contains(command, " doctor "):
		operation = "doctor.diagnose"
		params["fresh"] = "true"
	default:
		operation = firstNonEmpty(action.Kind, "command.run")
	}

	params = compactParams(params)
	return &APIOperation{
		Operation: operation,
		Target:    schema.Target{Kind: targetKind, Name: service},
		Params:    params,
		Mutating:  action.Mutating,
	}
}

func doctorAPIOperation(service string) *APIOperation {
	return &APIOperation{
		Operation: "doctor.diagnose",
		Target:    schema.Target{Kind: "service", Name: service},
		Params:    map[string]string{"fresh": "true"},
		Mutating:  false,
	}
}

func actionLess(left, right doctor.RecommendedAction) bool {
	if left.Mutating != right.Mutating {
		return !left.Mutating
	}
	leftRisk, rightRisk := riskForAction(left), riskForAction(right)
	if riskRank(leftRisk) != riskRank(rightRisk) {
		return riskRank(leftRisk) < riskRank(rightRisk)
	}
	if left.Service != right.Service {
		return left.Service < right.Service
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.Command < right.Command
}

func riskForAction(action doctor.RecommendedAction) schema.Risk {
	if action.Risk != "" {
		return action.Risk
	}
	if action.Mutating {
		return schema.RiskMedium
	}
	return schema.RiskLow
}

func reversibilityForAction(action doctor.RecommendedAction) schema.Reversibility {
	if action.Reversibility != "" {
		return action.Reversibility
	}
	if action.Mutating {
		return schema.PartiallyReversible
	}
	return schema.Reversible
}

func safetyForAction(action doctor.RecommendedAction) string {
	if action.Safety != "" {
		return action.Safety
	}
	if action.Mutating {
		return "mutating action; review risk, reversibility, and approval metadata before execution"
	}
	return "read-only observation; no durable state change"
}

func requiresApproval(action doctor.RecommendedAction, risk schema.Risk, reversibility schema.Reversibility) bool {
	if action.RequiresApproval {
		return true
	}
	return risk == schema.RiskHigh || risk == schema.RiskCritical || reversibility == schema.Irreversible
}

func isReversible(value schema.Reversibility) bool {
	return value == schema.Reversible
}

func statusForGraph(graph ActionGraph) GraphStatus {
	if len(graph.Findings) == 0 && len(graph.Steps) == 0 {
		return StatusNoAction
	}
	for _, step := range graph.Steps {
		if step.RequiresApproval {
			return StatusApprovalRequired
		}
	}
	if len(graph.Steps) == 0 {
		return StatusNoAction
	}
	return StatusPlanReady
}

func confidence(result doctor.Result) float64 {
	if len(result.Findings) == 0 {
		return 1
	}
	var max float64
	for _, finding := range result.Findings {
		if finding.Confidence > max {
			max = finding.Confidence
		}
	}
	if max == 0 {
		return 0.5
	}
	return max
}

func binaryFromActions(actions []doctor.RecommendedAction) string {
	for _, action := range actions {
		fields := strings.Fields(action.Command)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func stripAssumeYes(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return command
	}
	out := fields[:0]
	for _, field := range fields {
		if field == "--yes" {
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}

func flagValue(command, name string) string {
	fields := strings.Fields(command)
	long := "--" + name
	prefix := long + "="
	for i, field := range fields {
		if field == long && i+1 < len(fields) {
			return fields[i+1]
		}
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix)
		}
	}
	return ""
}

func uniqueStepID(id string, seen map[string]struct{}) string {
	if id == "" {
		id = "step"
	}
	base := id
	for i := 2; ; i++ {
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			return id
		}
		id = fmt.Sprintf("%s_%d", base, i)
	}
}

func compactID(service, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "_", "-", "_", ":", "_", "/", "_", ".", "_", "__", "_")
	value = replacer.Replace(value)
	if service == "" {
		return strings.Trim(value, "_")
	}
	return strings.Trim(strings.ToLower(service)+"_"+value, "_")
}

func compactParams(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		if value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func riskRank(risk schema.Risk) int {
	switch risk {
	case schema.RiskLow:
		return 1
	case schema.RiskMedium:
		return 2
	case schema.RiskHigh:
		return 3
	case schema.RiskCritical:
		return 4
	default:
		return 2
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
