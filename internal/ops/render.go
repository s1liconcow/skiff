package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

type ProfileRenderRequest struct {
	Profile   sagaapi.OperationProfile
	SagaID    string
	Target    schema.Target
	Actor     schema.Actor
	TraceID   string
	Params    map[string]json.RawMessage
	Package   schema.PackageProvenance
	CreatedAt time.Time
}

type ProfileRenderResult struct {
	Intent      schema.SagaIntent  `json:"intent"`
	Graph       schema.SagaGraph   `json:"graph"`
	Control     schema.SagaControl `json:"control"`
	Params      json.RawMessage    `json:"params,omitempty"`
	Explanation ProfileExplanation `json:"explanation"`
}

type resolvedProfileParams struct {
	Raw     json.RawMessage
	Values  map[string]json.RawMessage
	Decoded map[string]any
}

var (
	templateParamReference = regexp.MustCompile(`^\$\{params\.([a-z0-9][a-z0-9._-]*)\}$`)
	sha256DigestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func RenderProfile(req ProfileRenderRequest) (*ProfileRenderResult, error) {
	if err := ValidateProfile(req.Profile); err != nil {
		return nil, err
	}
	req = normalizeProfileRenderRequest(req)
	if err := validateProfileRenderRequest(req); err != nil {
		return nil, err
	}
	resolved, err := resolveProfileParams(req.Profile, req.Params)
	if err != nil {
		return nil, err
	}
	nodes, edges, err := renderProfileGraphTemplate(req.Profile, resolved)
	if err != nil {
		return nil, err
	}
	explanation, err := ExplainProfile(req.Profile)
	if err != nil {
		return nil, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	intent := schema.SagaIntent{
		SchemaVersion:     schema.Version,
		SagaID:            req.SagaID,
		Kind:              string(req.Profile.Kind),
		Target:            req.Target,
		Actor:             req.Actor,
		TraceID:           req.TraceID,
		Risk:              schema.Risk(req.Profile.Risk),
		Reversibility:     schema.Reversibility(req.Profile.Reversibility),
		PackageLockDigest: req.Package.LockfileDigest,
		Summary:           req.Profile.Summary,
		CreatedAt:         now,
		Params:            resolved.Raw,
		Package:           clonePackageProvenance(req.Package),
	}
	graph := schema.SagaGraph{
		SchemaVersion: schema.Version,
		SagaID:        req.SagaID,
		Nodes:         nodes,
		Edges:         edges,
		CreatedAt:     now,
		Package:       clonePackageProvenance(req.Package),
	}
	control := schema.SagaControl{
		SchemaVersion: schema.Version,
		SagaID:        req.SagaID,
		Status:        schema.SagaPending,
		UpdatedAt:     now,
		TraceID:       req.TraceID,
	}
	return &ProfileRenderResult{Intent: intent, Graph: graph, Control: control, Params: resolved.Raw, Explanation: explanation}, nil
}

func CreateProfileSaga(ctx context.Context, store objstore.ObjectStore, req ProfileRenderRequest) (*sagastate.Documents, *ProfileRenderResult, error) {
	if store == nil {
		return nil, nil, errors.New("object store is required")
	}
	rendered, err := RenderProfile(req)
	if err != nil {
		return nil, nil, err
	}
	docs, err := sagastate.NewStore(store).Create(ctx, sagastate.CreateRequest{
		Intent:  rendered.Intent,
		Graph:   rendered.Graph,
		Control: rendered.Control,
	})
	if err != nil {
		return nil, rendered, err
	}
	return docs, rendered, nil
}

func normalizeProfileRenderRequest(req ProfileRenderRequest) ProfileRenderRequest {
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.Target.Kind = strings.TrimSpace(req.Target.Kind)
	req.Target.Name = strings.TrimSpace(req.Target.Name)
	req.Actor.ID = strings.TrimSpace(req.Actor.ID)
	req.Actor.Type = strings.TrimSpace(req.Actor.Type)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	} else {
		req.CreatedAt = req.CreatedAt.UTC()
	}
	if req.Actor.ID == "" {
		req.Actor.ID = "skiff"
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Profile.Name+"profile")
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.TraceID+req.Profile.Name)
	}
	return req
}

func validateProfileRenderRequest(req ProfileRenderRequest) error {
	var diagnostics []ProfileDiagnostic
	add := func(path, code, message string) {
		diagnostics = append(diagnostics, ProfileDiagnostic{Path: path, Code: code, Message: message})
	}
	if err := paths.ValidateID("saga", req.SagaID); err != nil {
		add("$.saga_id", "INVALID_SAGA_ID", err.Error())
	}
	if strings.TrimSpace(req.Target.Kind) == "" || strings.TrimSpace(req.Target.Name) == "" {
		add("$.target", "REQUIRED", "target kind and name are required")
	} else if !profileTargetsKind(req.Profile.TargetKinds, req.Target.Kind) {
		add("$.target.kind", "UNSUPPORTED_TARGET_KIND", "target kind is not supported by the operation profile")
	}
	if strings.TrimSpace(req.Actor.ID) == "" || strings.TrimSpace(req.Actor.Type) == "" {
		add("$.actor", "REQUIRED", "actor id and type are required")
	}
	if strings.TrimSpace(req.TraceID) == "" {
		add("$.trace_id", "REQUIRED", "trace id is required")
	}
	validatePackageProvenance(add, req.Package)
	if len(diagnostics) > 0 {
		return ProfileValidationError{Diagnostics: diagnostics}
	}
	return nil
}

func profileTargetsKind(targetKinds []string, kind string) bool {
	for _, target := range targetKinds {
		if target == kind {
			return true
		}
	}
	return false
}

func validatePackageProvenance(add func(path, code, message string), provenance schema.PackageProvenance) {
	if strings.TrimSpace(provenance.Digest) == "" {
		add("$.package.digest", "REQUIRED", "package digest is required when rendering a package operation profile")
	} else if !sha256DigestPattern.MatchString(provenance.Digest) {
		add("$.package.digest", "INVALID_DIGEST", "package digest must be sha256:<64 lowercase hex chars>")
	}
	if strings.TrimSpace(provenance.LockfileDigest) == "" {
		add("$.package.lockfile_digest", "REQUIRED", "lockfile digest is required when rendering a package operation profile")
	} else if !sha256DigestPattern.MatchString(provenance.LockfileDigest) {
		add("$.package.lockfile_digest", "INVALID_DIGEST", "lockfile digest must be sha256:<64 lowercase hex chars>")
	}
	if provenance.ManifestDigest != "" && !sha256DigestPattern.MatchString(provenance.ManifestDigest) {
		add("$.package.manifest_digest", "INVALID_DIGEST", "manifest digest must be sha256:<64 lowercase hex chars>")
	}
}

func resolveProfileParams(profile sagaapi.OperationProfile, supplied map[string]json.RawMessage) (resolvedProfileParams, error) {
	var diagnostics []ProfileDiagnostic
	add := func(path, code, message string) {
		diagnostics = append(diagnostics, ProfileDiagnostic{Path: path, Code: code, Message: message})
	}
	for name := range supplied {
		if _, ok := profile.Params[name]; !ok {
			add("$.params."+name, "UNKNOWN_PARAM", "parameter is not declared by the operation profile")
		}
	}
	values := make(map[string]json.RawMessage, len(profile.Params))
	decoded := make(map[string]any, len(profile.Params))
	names := make([]string, 0, len(profile.Params))
	for name := range profile.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		param := profile.Params[name]
		raw, ok := supplied[name]
		if !ok || len(strings.TrimSpace(string(raw))) == 0 {
			raw = paramDefault(profile, name, param)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			if param.Required {
				add("$.params."+name, "REQUIRED", "required parameter is missing")
			}
			continue
		}
		value, err := decodeJSONValue(raw)
		if err != nil {
			add("$.params."+name, "INVALID_JSON", "parameter value must be valid JSON: "+err.Error())
			continue
		}
		if err := validateDecodedValueType(value, param.Type, param.Enum); err != nil {
			add("$.params."+name, "INVALID_PARAM_VALUE", err.Error())
			continue
		}
		canonicalValue, err := canonical.Marshal(value)
		if err != nil {
			add("$.params."+name, "INVALID_PARAM_VALUE", err.Error())
			continue
		}
		values[name] = canonicalValue
		decoded[name] = value
	}
	if len(diagnostics) > 0 {
		return resolvedProfileParams{}, ProfileValidationError{Diagnostics: diagnostics}
	}
	raw, err := canonical.Marshal(values)
	if err != nil {
		return resolvedProfileParams{}, err
	}
	if len(values) == 0 {
		raw = nil
	}
	return resolvedProfileParams{Raw: raw, Values: values, Decoded: decoded}, nil
}

func renderProfileGraphTemplate(profile sagaapi.OperationProfile, params resolvedProfileParams) ([]schema.SagaNode, []schema.SagaEdge, error) {
	nodes := make([]schema.SagaNode, 0, len(profile.GraphTemplate.Nodes))
	for i, template := range profile.GraphTemplate.Nodes {
		base := fmt.Sprintf("$.graph_template.nodes[%d]", i)
		nodeParams, err := renderTemplateJSON(base+".params", template.Params, params.Decoded)
		if err != nil {
			return nil, nil, err
		}
		compensate, err := renderCompensationTemplate(base+".compensate", template.Compensate, params.Decoded)
		if err != nil {
			return nil, nil, err
		}
		risk := template.Risk
		if risk == "" {
			risk = profile.Risk
		}
		reversibility := template.Reversibility
		if reversibility == "" {
			reversibility = profile.Reversibility
		}
		nodes = append(nodes, schema.SagaNode{
			ID:            template.ID,
			Kind:          template.Kind,
			Requires:      append([]string(nil), template.Requires...),
			Params:        nodeParams,
			Retry:         cloneRetryPolicy(template.Retry),
			Compensate:    compensate,
			Risk:          schema.Risk(risk),
			Reversibility: schema.Reversibility(reversibility),
		})
	}
	edges := make([]schema.SagaEdge, 0, len(profile.GraphTemplate.Edges))
	for _, edge := range profile.GraphTemplate.Edges {
		edges = append(edges, schema.SagaEdge{From: edge.From, To: edge.To})
	}
	if len(edges) == 0 {
		for _, node := range nodes {
			for _, required := range node.Requires {
				edges = append(edges, schema.SagaEdge{From: required, To: node.ID})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	return nodes, edges, nil
}

func renderCompensationTemplate(path string, template *sagaapi.CompensationTemplate, params map[string]any) (*schema.CompensationSpec, error) {
	if template == nil {
		return nil, nil
	}
	rendered, err := renderTemplateJSON(path+".params", template.Params, params)
	if err != nil {
		return nil, err
	}
	return &schema.CompensationSpec{Kind: template.Kind, Params: rendered}, nil
}

func renderTemplateJSON(path string, raw json.RawMessage, params map[string]any) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return nil, ProfileValidationError{Diagnostics: []ProfileDiagnostic{{
			Path:    path,
			Code:    "INVALID_JSON",
			Message: "template JSON is invalid: " + err.Error(),
		}}}
	}
	rendered, err := renderTemplateValue(value, params)
	if err != nil {
		return nil, ProfileValidationError{Diagnostics: []ProfileDiagnostic{{
			Path:    path,
			Code:    "UNKNOWN_PARAM_REFERENCE",
			Message: err.Error(),
		}}}
	}
	return canonical.Marshal(rendered)
}

func renderTemplateValue(value any, params map[string]any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			rendered, err := renderTemplateValue(item, params)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			rendered, err := renderTemplateValue(item, params)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case string:
		match := templateParamReference.FindStringSubmatch(typed)
		if len(match) == 0 {
			return typed, nil
		}
		value, ok := params[match[1]]
		if !ok {
			return nil, fmt.Errorf("template references unset parameter %q", match[1])
		}
		return value, nil
	default:
		return value, nil
	}
}

func cloneRetryPolicy(policy *sagaapi.RetryPolicy) *schema.RetryPolicy {
	if policy == nil {
		return nil
	}
	return &schema.RetryPolicy{MaxAttempts: policy.MaxAttempts, Backoff: policy.Backoff}
}

func clonePackageProvenance(provenance schema.PackageProvenance) *schema.PackageProvenance {
	return &schema.PackageProvenance{
		Name:           provenance.Name,
		Ref:            provenance.Ref,
		Version:        provenance.Version,
		Digest:         provenance.Digest,
		ManifestDigest: provenance.ManifestDigest,
		LockfileDigest: provenance.LockfileDigest,
	}
}
