package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

type ProfileDiagnostic struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ProfileValidationError struct {
	Diagnostics []ProfileDiagnostic `json:"diagnostics"`
}

func (e ProfileValidationError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "operation profile validation failed"
	}
	parts := make([]string, 0, len(e.Diagnostics))
	for _, diagnostic := range e.Diagnostics {
		parts = append(parts, fmt.Sprintf("%s %s: %s", diagnostic.Path, diagnostic.Code, diagnostic.Message))
	}
	return "operation profile validation failed: " + strings.Join(parts, "; ")
}

type ProfileExplanation struct {
	Name                 string                `json:"name"`
	Kind                 sagaapi.ProfileKind   `json:"kind"`
	TargetKinds          []string              `json:"target_kinds"`
	Summary              string                `json:"summary"`
	Risk                 sagaapi.Risk          `json:"risk"`
	Reversibility        sagaapi.Reversibility `json:"reversibility"`
	RequiredCapabilities []string              `json:"required_capabilities,omitempty"`
	Params               []ParamExplanation    `json:"params,omitempty"`
	Steps                []ProfileStepSummary  `json:"steps"`
}

type ParamExplanation struct {
	Name     string            `json:"name"`
	Type     sagaapi.ParamType `json:"type"`
	Required bool              `json:"required,omitempty"`
	Default  json.RawMessage   `json:"default,omitempty"`
	Enum     []string          `json:"enum,omitempty"`
	Summary  string            `json:"summary,omitempty"`
}

type ProfileStepSummary struct {
	ID            string                `json:"id"`
	Kind          string                `json:"kind"`
	Requires      []string              `json:"requires,omitempty"`
	Risk          sagaapi.Risk          `json:"risk,omitempty"`
	Reversibility sagaapi.Reversibility `json:"reversibility,omitempty"`
}

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func ValidateProfile(profile sagaapi.OperationProfile) error {
	var diagnostics []ProfileDiagnostic
	add := func(path, code, message string) {
		diagnostics = append(diagnostics, ProfileDiagnostic{Path: path, Code: code, Message: message})
	}
	if profile.SchemaVersion != sagaapi.ProfileSchemaVersion {
		add("$.schema_version", "UNSUPPORTED_PROFILE_SCHEMA", "operation profile schema_version must be skiff.operation-profile/v1alpha1")
	}
	if !validProfileName(profile.Name) {
		add("$.name", "INVALID_PROFILE_NAME", "operation profile name is required and must use lowercase letters, numbers, dots, underscores, or hyphens")
	}
	if strings.TrimSpace(string(profile.Kind)) == "" {
		add("$.kind", "REQUIRED", "operation profile kind is required")
	}
	if len(profile.TargetKinds) == 0 {
		add("$.target_kinds", "REQUIRED", "operation profile target_kinds must not be empty")
	}
	for i, target := range profile.TargetKinds {
		if strings.TrimSpace(target) == "" {
			add(fmt.Sprintf("$.target_kinds[%d]", i), "REQUIRED", "target kind must not be empty")
		}
	}
	if strings.TrimSpace(profile.Summary) == "" {
		add("$.summary", "REQUIRED", "operation profile summary is required")
	}
	if !validRisk(profile.Risk) {
		add("$.risk", "INVALID_RISK", "risk must be low, medium, high, or critical")
	}
	if !validReversibility(profile.Reversibility) {
		add("$.reversibility", "INVALID_REVERSIBILITY", "reversibility must be reversible, compensatable, partially_reversible, or irreversible")
	}
	for i, capability := range profile.RequiredCapabilities {
		if !validProfileName(capability) {
			add(fmt.Sprintf("$.required_capabilities[%d]", i), "INVALID_CAPABILITY", "required capabilities must use lowercase letters, numbers, dots, underscores, or hyphens")
		}
	}
	validateParamSchemas(add, profile)
	validateGraphTemplate(add, profile)
	if len(diagnostics) > 0 {
		return ProfileValidationError{Diagnostics: diagnostics}
	}
	return nil
}

func ExplainProfile(profile sagaapi.OperationProfile) (ProfileExplanation, error) {
	if err := ValidateProfile(profile); err != nil {
		return ProfileExplanation{}, err
	}
	out := ProfileExplanation{
		Name:                 profile.Name,
		Kind:                 profile.Kind,
		TargetKinds:          append([]string(nil), profile.TargetKinds...),
		Summary:              profile.Summary,
		Risk:                 profile.Risk,
		Reversibility:        profile.Reversibility,
		RequiredCapabilities: append([]string(nil), profile.RequiredCapabilities...),
	}
	paramNames := make([]string, 0, len(profile.Params))
	for name := range profile.Params {
		paramNames = append(paramNames, name)
	}
	sort.Strings(paramNames)
	for _, name := range paramNames {
		param := profile.Params[name]
		explained := ParamExplanation{
			Name:     name,
			Type:     param.Type,
			Required: param.Required,
			Default:  cloneRaw(paramDefault(profile, name, param)),
			Enum:     append([]string(nil), param.Enum...),
			Summary:  param.Summary,
		}
		out.Params = append(out.Params, explained)
	}
	for _, node := range profile.GraphTemplate.Nodes {
		risk := node.Risk
		if risk == "" {
			risk = profile.Risk
		}
		reversibility := node.Reversibility
		if reversibility == "" {
			reversibility = profile.Reversibility
		}
		out.Steps = append(out.Steps, ProfileStepSummary{
			ID:            node.ID,
			Kind:          node.Kind,
			Requires:      append([]string(nil), node.Requires...),
			Risk:          risk,
			Reversibility: reversibility,
		})
	}
	return out, nil
}

func validateParamSchemas(add func(path, code, message string), profile sagaapi.OperationProfile) {
	names := make([]string, 0, len(profile.Params))
	for name := range profile.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		base := "$.params." + name
		if !validProfileName(name) {
			add(base, "INVALID_PARAM_NAME", "parameter names must use lowercase letters, numbers, dots, underscores, or hyphens")
		}
		param := profile.Params[name]
		if !validParamType(param.Type) {
			add(base+".type", "INVALID_PARAM_TYPE", "parameter type must be string, boolean, integer, number, object, or array")
		}
		if len(param.Enum) > 0 && param.Type != sagaapi.ParamString {
			add(base+".enum", "INVALID_PARAM_ENUM", "enum is only supported for string parameters")
		}
		if len(bytes.TrimSpace(param.Default)) > 0 {
			if err := validateJSONValueType(param.Default, param.Type, param.Enum); err != nil {
				add(base+".default", "INVALID_PARAM_DEFAULT", err.Error())
			}
		}
	}
	defaultNames := make([]string, 0, len(profile.Defaults))
	for name := range profile.Defaults {
		defaultNames = append(defaultNames, name)
	}
	sort.Strings(defaultNames)
	for _, name := range defaultNames {
		param, ok := profile.Params[name]
		if !ok {
			add("$.defaults."+name, "UNKNOWN_PARAM_DEFAULT", "default provided for an unknown parameter")
			continue
		}
		if err := validateJSONValueType(profile.Defaults[name], param.Type, param.Enum); err != nil {
			add("$.defaults."+name, "INVALID_PARAM_DEFAULT", err.Error())
		}
	}
}

func validateGraphTemplate(add func(path, code, message string), profile sagaapi.OperationProfile) {
	if len(profile.GraphTemplate.Nodes) == 0 {
		add("$.graph_template.nodes", "REQUIRED", "graph template must include at least one node")
		return
	}
	seen := map[string]struct{}{}
	for i, node := range profile.GraphTemplate.Nodes {
		base := fmt.Sprintf("$.graph_template.nodes[%d]", i)
		if !validProfileName(node.ID) {
			add(base+".id", "INVALID_NODE_ID", "node id is required and must use lowercase letters, numbers, dots, underscores, or hyphens")
		}
		if strings.TrimSpace(node.Kind) == "" {
			add(base+".kind", "REQUIRED", "node kind is required")
		}
		if _, ok := seen[node.ID]; node.ID != "" && ok {
			add(base+".id", "DUPLICATE_NODE", "node ids must be unique")
		}
		seen[node.ID] = struct{}{}
		if node.Risk != "" && !validRisk(node.Risk) {
			add(base+".risk", "INVALID_RISK", "node risk must be low, medium, high, or critical")
		}
		if node.Reversibility != "" && !validReversibility(node.Reversibility) {
			add(base+".reversibility", "INVALID_REVERSIBILITY", "node reversibility must be reversible, compensatable, partially_reversible, or irreversible")
		}
		if node.Compensate != nil && strings.TrimSpace(node.Compensate.Kind) == "" {
			add(base+".compensate.kind", "REQUIRED", "compensation kind is required when compensate is set")
		}
		if len(bytes.TrimSpace(node.Params)) > 0 && !json.Valid(node.Params) {
			add(base+".params", "INVALID_JSON", "node params must be valid JSON")
		}
		if node.Retry != nil && node.Retry.MaxAttempts < 0 {
			add(base+".retry.max_attempts", "INVALID_RETRY", "retry max_attempts must not be negative")
		}
		if node.Compensate != nil && len(bytes.TrimSpace(node.Compensate.Params)) > 0 && !json.Valid(node.Compensate.Params) {
			add(base+".compensate.params", "INVALID_JSON", "compensation params must be valid JSON")
		}
	}
	for i, node := range profile.GraphTemplate.Nodes {
		for j, required := range node.Requires {
			if _, ok := seen[required]; !ok {
				add(fmt.Sprintf("$.graph_template.nodes[%d].requires[%d]", i, j), "UNKNOWN_NODE", "required node is not present in graph template")
			}
		}
	}
	for i, edge := range profile.GraphTemplate.Edges {
		base := fmt.Sprintf("$.graph_template.edges[%d]", i)
		if _, ok := seen[edge.From]; !ok {
			add(base+".from", "UNKNOWN_NODE", "edge.from node is not present in graph template")
		}
		if _, ok := seen[edge.To]; !ok {
			add(base+".to", "UNKNOWN_NODE", "edge.to node is not present in graph template")
		}
	}
	if cycle := firstGraphTemplateCycle(profile.GraphTemplate); len(cycle) > 0 {
		add("$.graph_template", "GRAPH_CYCLE", "graph template dependency cycle includes "+strings.Join(cycle, " -> "))
	}
}

func validProfileName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 128 && profileNamePattern.MatchString(value)
}

func validRisk(value sagaapi.Risk) bool {
	switch value {
	case sagaapi.RiskLow, sagaapi.RiskMedium, sagaapi.RiskHigh, sagaapi.RiskCritical:
		return true
	default:
		return false
	}
}

func validReversibility(value sagaapi.Reversibility) bool {
	switch value {
	case sagaapi.Reversible, sagaapi.Compensatable, sagaapi.PartiallyReversible, sagaapi.Irreversible:
		return true
	default:
		return false
	}
}

func validParamType(value sagaapi.ParamType) bool {
	switch value {
	case sagaapi.ParamString, sagaapi.ParamBoolean, sagaapi.ParamInteger, sagaapi.ParamNumber, sagaapi.ParamObject, sagaapi.ParamArray:
		return true
	default:
		return false
	}
}

func paramDefault(profile sagaapi.OperationProfile, name string, param sagaapi.ParamSchema) json.RawMessage {
	if len(bytes.TrimSpace(param.Default)) > 0 {
		return param.Default
	}
	if profile.Defaults != nil {
		return profile.Defaults[name]
	}
	return nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func validateJSONValueType(raw json.RawMessage, typ sagaapi.ParamType, enum []string) error {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return fmt.Errorf("value must be valid JSON: %w", err)
	}
	return validateDecodedValueType(value, typ, enum)
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not supported")
		}
		return nil, err
	}
	return value, nil
}

func validateDecodedValueType(value any, typ sagaapi.ParamType, enum []string) error {
	switch typ {
	case sagaapi.ParamString:
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("value must be a string")
		}
		if len(enum) > 0 {
			for _, allowed := range enum {
				if str == allowed {
					return nil
				}
			}
			return fmt.Errorf("value %q is not in enum %s", str, strings.Join(enum, ", "))
		}
	case sagaapi.ParamBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("value must be a boolean")
		}
	case sagaapi.ParamInteger:
		num, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("value must be an integer")
		}
		if _, err := strconv.ParseInt(num.String(), 10, 64); err != nil {
			return fmt.Errorf("value must be an integer")
		}
	case sagaapi.ParamNumber:
		num, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("value must be a number")
		}
		if _, err := num.Float64(); err != nil {
			return fmt.Errorf("value must be a number")
		}
	case sagaapi.ParamObject:
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("value must be an object")
		}
	case sagaapi.ParamArray:
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("value must be an array")
		}
	default:
		return fmt.Errorf("unknown parameter type %q", typ)
	}
	return nil
}

func firstGraphTemplateCycle(template sagaapi.GraphTemplate) []string {
	ids := make(map[string]struct{}, len(template.Nodes))
	outgoing := make(map[string][]string, len(template.Nodes))
	for _, node := range template.Nodes {
		if node.ID == "" {
			continue
		}
		ids[node.ID] = struct{}{}
	}
	for _, node := range template.Nodes {
		for _, required := range node.Requires {
			if _, ok := ids[node.ID]; ok {
				outgoing[required] = append(outgoing[required], node.ID)
			}
		}
	}
	for _, edge := range template.Edges {
		if _, ok := ids[edge.From]; ok {
			outgoing[edge.From] = append(outgoing[edge.From], edge.To)
		}
	}
	for id := range outgoing {
		sort.Strings(outgoing[id])
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var stack []string
	var visit func(string) []string
	visit = func(id string) []string {
		if visiting[id] {
			for i, item := range stack {
				if item == id {
					return append(append([]string(nil), stack[i:]...), id)
				}
			}
			return []string{id, id}
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		stack = append(stack, id)
		for _, next := range outgoing[id] {
			if _, ok := ids[next]; !ok {
				continue
			}
			if cycle := visit(next); len(cycle) > 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		visiting[id] = false
		visited[id] = true
		return nil
	}
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	for _, id := range names {
		if cycle := visit(id); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}
