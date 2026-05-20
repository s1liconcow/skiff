package postgresha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/pkg/pluginapi"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

const (
	envAdminURLs     = "SKIFF_POSTGRES_HA_MEMBER_ADMIN_URLS"
	envAdminTemplate = "SKIFF_POSTGRES_HA_ADMIN_URL_TEMPLATE"
	envMaxLagBytes   = "SKIFF_POSTGRES_HA_MAX_REPLICA_LAG_BYTES"
)

type Options struct {
	Client  *http.Client
	Now     func() time.Time
	Environ func(string) string
}

type envelope struct {
	Hook    pluginapi.Hook  `json:"hook"`
	Request json.RawMessage `json:"request"`
}

type stepParams struct {
	ProfileKind        string            `json:"profile_kind,omitempty"`
	ReleaseID          string            `json:"release_id,omitempty"`
	Candidate          string            `json:"candidate,omitempty"`
	ReturnPrimary      bool              `json:"return_primary,omitempty"`
	OriginalPrimary    string            `json:"original_primary,omitempty"`
	ExpectedPrimary    string            `json:"expected_primary,omitempty"`
	MaxReplicaLagBytes *int64            `json:"maxReplicaLagBytes,omitempty"`
	MemberAdminURLs    map[string]string `json:"member_admin_urls,omitempty"`
	AdminURLTemplate   string            `json:"admin_url_template,omitempty"`
	Emergency          bool              `json:"emergency,omitempty"`
}

type adminState struct {
	Mode       string            `json:"mode"`
	Member     int               `json:"member"`
	Members    int               `json:"members"`
	Generation int64             `json:"generation"`
	Role       string            `json:"role"`
	Term       int64             `json:"term"`
	Leader     int               `json:"leader"`
	Lag        int64             `json:"lag"`
	Failures   map[string]string `json:"failures,omitempty"`
	UpdatedAt  string            `json:"updated_at"`
}

type adminMutationResponse struct {
	OK     bool       `json:"ok"`
	Action string     `json:"action,omitempty"`
	State  adminState `json:"state"`
}

type clusterClient struct {
	http        *http.Client
	env         func(string) string
	params      stepParams
	context     sagaapi.PackageStepContext
	urls        map[int]string
	urlTemplate string
}

func Execute(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, opts Options) int {
	var env envelope
	if err := json.NewDecoder(stdin).Decode(&env); err != nil {
		fmt.Fprintf(stderr, "decode plugin request: %v\n", err)
		return 1
	}
	response, err := Handle(ctx, env.Hook, env.Request, opts)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		fmt.Fprintf(stderr, "encode plugin response: %v\n", err)
		return 1
	}
	return 0
}

func Handle(ctx context.Context, hook pluginapi.Hook, request json.RawMessage, opts Options) (any, error) {
	switch hook {
	case pluginapi.HookPackageStep:
		var req pluginapi.PackageStepRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode package_step request: %w", err)
		}
		return handlePackageStep(ctx, req, opts), nil
	case pluginapi.HookDoctorChecks:
		var req pluginapi.DoctorChecksRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode doctor_checks request: %w", err)
		}
		_ = req
		return pluginapi.DoctorChecksResponse{}, nil
	default:
		return nil, fmt.Errorf("unsupported postgres-ha hook %q", hook)
	}
}

func RunPackageStep(ctx context.Context, req pluginapi.PackageStepRequest, opts Options) any {
	return handlePackageStep(ctx, req, opts)
}

func handlePackageStep(ctx context.Context, req pluginapi.PackageStepRequest, opts Options) any {
	switch req.Phase {
	case sagaapi.StepPhasePlan:
		return planPackageStep(req)
	case sagaapi.StepPhaseDoctor:
		return doctorPackageStep(ctx, req, opts)
	case sagaapi.StepPhaseRun, sagaapi.StepPhaseResume, sagaapi.StepPhaseCompensate:
		result, err := runPackageStep(ctx, req, opts)
		if err != nil {
			return failed("POSTGRES_HA_STEP_FAILED", "Postgres HA package step failed", err.Error())
		}
		return result
	default:
		return failed("POSTGRES_HA_PHASE_UNSUPPORTED", "unsupported Postgres HA package step phase", string(req.Phase))
	}
}

func planPackageStep(req pluginapi.PackageStepRequest) sagaapi.PackageStepPlanResponse {
	summary := summaryForKind(req.Kind)
	risk := sagaapi.RiskLow
	reversibility := sagaapi.Reversible
	switch req.Kind {
	case "package.primary_switchover.move_primary", "postgres.switchover":
		risk = sagaapi.RiskHigh
		reversibility = sagaapi.PartiallyReversible
	case "package.primary_switchover.update_old_primary", "package.primary_switchover.update_candidate":
		risk = sagaapi.RiskMedium
		reversibility = sagaapi.Compensatable
	case "package.primary_switchover.optional_failback":
		risk = sagaapi.RiskMedium
		reversibility = sagaapi.PartiallyReversible
	}
	return sagaapi.PackageStepPlanResponse{
		Summary:       summary,
		Risk:          risk,
		Reversibility: reversibility,
	}
}

func doctorPackageStep(ctx context.Context, req pluginapi.PackageStepRequest, opts Options) sagaapi.PackageStepDoctorResponse {
	params, err := parseParams(req.Params, opts)
	if err != nil {
		return sagaapi.PackageStepDoctorResponse{Findings: []sagaapi.PackageStepFinding{{
			Code:       "POSTGRES_HA_PARAMS_INVALID",
			Severity:   "warning",
			Summary:    err.Error(),
			Confidence: 0.95,
		}}}
	}
	client := newClusterClient(params, req.Context, opts)
	states, err := client.states(ctx)
	if err != nil {
		return sagaapi.PackageStepDoctorResponse{Findings: []sagaapi.PackageStepFinding{{
			Code:       "POSTGRES_HA_ADMIN_UNREACHABLE",
			Severity:   "warning",
			Summary:    err.Error(),
			Confidence: 0.9,
		}}}
	}
	if err := verifyClusterHealthy(states); err != nil {
		return sagaapi.PackageStepDoctorResponse{Findings: []sagaapi.PackageStepFinding{{
			Code:       "POSTGRES_HA_TOPOLOGY_UNHEALTHY",
			Severity:   "warning",
			Summary:    err.Error(),
			Confidence: 0.95,
		}}}
	}
	return sagaapi.PackageStepDoctorResponse{}
}

func runPackageStep(ctx context.Context, req pluginapi.PackageStepRequest, opts Options) (sagaapi.PackageStepResultResponse, error) {
	params, err := parseParams(req.Params, opts)
	if err != nil {
		return failed("POSTGRES_HA_PARAMS_INVALID", "invalid Postgres HA package step parameters", err.Error()), nil
	}
	client := newClusterClient(params, req.Context, opts)
	switch req.Kind {
	case "package.primary_switchover.verify_cluster_healthy":
		states, err := client.states(ctx)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := verifyClusterHealthy(states); err != nil {
			return failed("POSTGRES_HA_CLUSTER_UNHEALTHY", "Postgres HA cluster is not healthy", err.Error()), nil
		}
		return succeeded(req, "Postgres HA cluster is healthy", topologyResult(states, "")), nil
	case "package.primary_switchover.verify_candidate_caught_up", "postgres.verify_replica_lag":
		candidate, err := candidateOrdinal(params)
		if err != nil {
			return failed("POSTGRES_HA_CANDIDATE_INVALID", "candidate member is invalid", err.Error()), nil
		}
		state, err := client.state(ctx, candidate)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := verifyCaughtUpReplica(state, maxLag(params)); err != nil {
			return failed("POSTGRES_HA_REPLICA_LAG_UNSAFE", "planned switchover candidate is not safely caught up", err.Error()), nil
		}
		return succeeded(req, "candidate replica is caught up", stateResult(state, maxLag(params))), nil
	case "package.primary_switchover.move_primary", "postgres.switchover":
		return runSwitchover(ctx, req, client, params)
	case "package.primary_switchover.update_old_primary":
		return runCatchUpMembers(ctx, req, client, params, []int{originalPrimaryOrdinal(req, params)})
	case "package.primary_switchover.verify_old_primary_caught_up":
		primary := originalPrimaryOrdinal(req, params)
		state, err := client.state(ctx, primary)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := verifyLag(state, maxLag(params)); err != nil {
			return failed("POSTGRES_HA_OLD_PRIMARY_NOT_CAUGHT_UP", "old primary has not caught up after update", err.Error()), nil
		}
		return succeeded(req, "old primary is caught up", stateResult(state, maxLag(params))), nil
	case "package.primary_switchover.optional_failback":
		return runOptionalFailback(ctx, req, client, params)
	case "package.primary_switchover.update_candidate":
		candidate, err := candidateOrdinal(params)
		if err != nil {
			return failed("POSTGRES_HA_CANDIDATE_INVALID", "candidate member is invalid", err.Error()), nil
		}
		return runCatchUpMembers(ctx, req, client, params, []int{candidate})
	case "package.primary_switchover.verify_final_topology", "postgres.verify_timeline":
		return verifyFinalTopology(ctx, req, client, params)
	default:
		return failed("POSTGRES_HA_STEP_UNSUPPORTED", "unsupported Postgres HA package step", req.Kind), nil
	}
}

func runSwitchover(ctx context.Context, req pluginapi.PackageStepRequest, client clusterClient, params stepParams) (sagaapi.PackageStepResultResponse, error) {
	candidate, err := candidateOrdinal(params)
	if err != nil {
		return failed("POSTGRES_HA_CANDIDATE_INVALID", "candidate member is invalid", err.Error()), nil
	}
	if !params.Emergency {
		state, err := client.state(ctx, candidate)
		if err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
		if err := verifyCaughtUpReplica(state, maxLag(params)); err != nil {
			return failed("POSTGRES_HA_REPLICA_LAG_UNSAFE", "planned switchover candidate is not safely caught up", err.Error()), nil
		}
	}
	states, err := client.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	oldPrimary, err := currentPrimary(states)
	if err != nil {
		return failed("POSTGRES_HA_PRIMARY_UNKNOWN", "current primary could not be determined", err.Error()), nil
	}
	if _, err := client.post(ctx, candidate, "/admin/promote", nil); err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	if oldPrimary.Member != candidate {
		if _, err := client.post(ctx, oldPrimary.Member, "/admin/stepdown", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
	}
	next, err := client.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	result := topologyResult(next, strconv.Itoa(candidate))
	result["old_primary"] = oldPrimary.Member
	return sagaapi.PackageStepResultResponse{
		Status:             sagaapi.StepStatusSucceeded,
		Summary:            "primary moved to caught-up Postgres candidate",
		Result:             mustJSON(result),
		ProviderOperations: []sagaapi.ProviderOperationRef{providerOperation(req, "postgres.switchover", "member-level planned switchover")},
	}, nil
}

func runCatchUpMembers(ctx context.Context, req pluginapi.PackageStepRequest, client clusterClient, params stepParams, members []int) (sagaapi.PackageStepResultResponse, error) {
	for _, member := range members {
		if _, err := client.post(ctx, member, "/admin/catch-up", nil); err != nil {
			return sagaapi.PackageStepResultResponse{}, err
		}
	}
	states, err := client.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	for _, member := range members {
		state, ok := states[member]
		if !ok {
			return failed("POSTGRES_HA_MEMBER_MISSING", "Postgres HA member state is missing", fmt.Sprintf("member %d was not returned by admin endpoints", member)), nil
		}
		if err := verifyLag(state, maxLag(params)); err != nil {
			return failed("POSTGRES_HA_MEMBER_NOT_CAUGHT_UP", "Postgres HA member has not caught up", err.Error()), nil
		}
	}
	return sagaapi.PackageStepResultResponse{
		Status:             sagaapi.StepStatusSucceeded,
		Summary:            "Postgres HA member update/catch-up completed",
		Result:             mustJSON(topologyResult(states, "")),
		ProviderOperations: []sagaapi.ProviderOperationRef{providerOperation(req, "postgres.member.catch_up", "member-level catch-up after update")},
	}, nil
}

func runOptionalFailback(ctx context.Context, req pluginapi.PackageStepRequest, client clusterClient, params stepParams) (sagaapi.PackageStepResultResponse, error) {
	if !params.ReturnPrimary {
		return succeeded(req, "failback not requested", map[string]any{"return_primary": false}), nil
	}
	candidate, err := candidateOrdinal(params)
	if err != nil {
		return failed("POSTGRES_HA_CANDIDATE_INVALID", "candidate member is invalid", err.Error()), nil
	}
	original := originalPrimaryOrdinal(req, params)
	originalState, err := client.state(ctx, original)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	if err := verifyLag(originalState, maxLag(params)); err != nil {
		return failed("POSTGRES_HA_FAILBACK_UNSAFE", "original primary is not caught up enough for failback", err.Error()), nil
	}
	if _, err := client.post(ctx, original, "/admin/promote", nil); err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	if _, err := client.post(ctx, candidate, "/admin/stepdown", nil); err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	states, err := client.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	return sagaapi.PackageStepResultResponse{
		Status:             sagaapi.StepStatusSucceeded,
		Summary:            "primary role returned to original Postgres member",
		Result:             mustJSON(topologyResult(states, strconv.Itoa(original))),
		ProviderOperations: []sagaapi.ProviderOperationRef{providerOperation(req, "postgres.failback", "member-level planned failback")},
	}, nil
}

func verifyFinalTopology(ctx context.Context, req pluginapi.PackageStepRequest, client clusterClient, params stepParams) (sagaapi.PackageStepResultResponse, error) {
	states, err := client.states(ctx)
	if err != nil {
		return sagaapi.PackageStepResultResponse{}, err
	}
	if err := verifyClusterHealthy(states); err != nil {
		return failed("POSTGRES_HA_FINAL_TOPOLOGY_UNHEALTHY", "final Postgres HA topology is unhealthy", err.Error()), nil
	}
	expected := strings.TrimSpace(params.ExpectedPrimary)
	if expected == "" {
		if params.ReturnPrimary {
			expected = strconv.Itoa(originalPrimaryOrdinal(req, params))
		} else {
			expected = strings.TrimSpace(params.Candidate)
		}
	}
	expectedOrdinal, err := parseOrdinal(expected)
	if err != nil {
		return failed("POSTGRES_HA_EXPECTED_PRIMARY_INVALID", "expected primary is invalid", err.Error()), nil
	}
	primary, err := currentPrimary(states)
	if err != nil {
		return failed("POSTGRES_HA_PRIMARY_UNKNOWN", "current primary could not be determined", err.Error()), nil
	}
	if primary.Member != expectedOrdinal {
		return failed("POSTGRES_HA_FINAL_PRIMARY_MISMATCH", "final Postgres HA primary does not match expected topology", fmt.Sprintf("primary member %d, want %d", primary.Member, expectedOrdinal)), nil
	}
	for _, state := range states {
		if state.Member == primary.Member {
			continue
		}
		if err := verifyLag(state, maxLag(params)); err != nil {
			return failed("POSTGRES_HA_FINAL_REPLICA_LAG_UNSAFE", "final Postgres HA replica lag exceeds budget", err.Error()), nil
		}
	}
	return succeeded(req, "final Postgres HA topology is healthy", topologyResult(states, strconv.Itoa(expectedOrdinal))), nil
}

func parseParams(raw json.RawMessage, opts Options) (stepParams, error) {
	params := stepParams{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return stepParams{}, err
		}
	}
	env := environ(opts)
	if params.MaxReplicaLagBytes == nil {
		if value := strings.TrimSpace(env(envMaxLagBytes)); value != "" {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return stepParams{}, fmt.Errorf("%s must be an integer: %w", envMaxLagBytes, err)
			}
			params.MaxReplicaLagBytes = &parsed
		}
	}
	if params.MemberAdminURLs == nil {
		parsed, err := parseAdminURLs(env(envAdminURLs))
		if err != nil {
			return stepParams{}, err
		}
		params.MemberAdminURLs = parsed
	}
	if strings.TrimSpace(params.AdminURLTemplate) == "" {
		params.AdminURLTemplate = env(envAdminTemplate)
	}
	return params, nil
}

func parseAdminURLs(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	out := map[string]string{}
	if strings.HasPrefix(value, "{") {
		if err := json.Unmarshal([]byte(value), &out); err != nil {
			return nil, fmt.Errorf("%s must be a JSON object mapping member ordinals to admin URLs: %w", envAdminURLs, err)
		}
		return out, nil
	}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		member, url, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("%s entries must be member=url", envAdminURLs)
		}
		out[strings.TrimSpace(member)] = strings.TrimSpace(url)
	}
	return out, nil
}

func newClusterClient(params stepParams, context sagaapi.PackageStepContext, opts Options) clusterClient {
	httpClient := opts.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return clusterClient{
		http:        httpClient,
		env:         environ(opts),
		params:      params,
		context:     context,
		urls:        parseMemberURLMap(params.MemberAdminURLs),
		urlTemplate: firstNonEmpty(strings.TrimSpace(params.AdminURLTemplate), "http://{target}-{member}:8008"),
	}
}

func (c clusterClient) states(ctx context.Context) (map[int]adminState, error) {
	members := sortedMemberKeys(c.urls)
	if len(members) == 0 {
		first, err := c.state(ctx, 0)
		if err != nil {
			return nil, err
		}
		total := first.Members
		if total <= 0 {
			total = 1
		}
		members = make([]int, 0, total)
		for i := 0; i < total; i++ {
			members = append(members, i)
		}
	}
	out := make(map[int]adminState, len(members))
	for _, member := range members {
		state, err := c.state(ctx, member)
		if err != nil {
			return nil, err
		}
		out[member] = state
	}
	return out, nil
}

func (c clusterClient) state(ctx context.Context, member int) (adminState, error) {
	var state adminState
	if err := c.get(ctx, member, "/admin/state", &state); err != nil {
		return adminState{}, err
	}
	if state.Member == 0 && member != 0 && state.Members == 0 && state.Role == "" {
		state.Member = member
	}
	return state, nil
}

func (c clusterClient) get(ctx context.Context, member int, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.memberURL(member, path), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GET member %d %s returned %s: %s", member, path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func (c clusterClient) post(ctx context.Context, member int, path string, body any) (adminState, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return adminState{}, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.memberURL(member, path), reader)
	if err != nil {
		return adminState{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return adminState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return adminState{}, fmt.Errorf("POST member %d %s returned %s: %s", member, path, resp.Status, strings.TrimSpace(string(payload)))
	}
	var wrapper adminMutationResponse
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return adminState{}, err
	}
	return wrapper.State, nil
}

func (c clusterClient) memberURL(member int, path string) string {
	if url := strings.TrimSpace(c.urls[member]); url != "" {
		return strings.TrimRight(url, "/") + path
	}
	value := c.urlTemplate
	value = strings.ReplaceAll(value, "{member}", strconv.Itoa(member))
	value = strings.ReplaceAll(value, "{target}", firstNonEmpty(c.context.Target, c.context.Service))
	value = strings.ReplaceAll(value, "{service}", c.context.Service)
	value = strings.ReplaceAll(value, "{env}", c.context.Env)
	return strings.TrimRight(value, "/") + path
}

func verifyClusterHealthy(states map[int]adminState) error {
	if len(states) == 0 {
		return errors.New("no Postgres HA member states were returned")
	}
	primaryCount := 0
	for member, state := range states {
		if len(state.Failures) > 0 {
			return fmt.Errorf("member %d has failures: %v", member, state.Failures)
		}
		switch state.Role {
		case "primary", "leader":
			primaryCount++
		case "replica":
		default:
			return fmt.Errorf("member %d role %q is not valid for primary/replica Postgres", member, state.Role)
		}
	}
	if primaryCount != 1 {
		return fmt.Errorf("cluster has %d primary members, want exactly one", primaryCount)
	}
	return nil
}

func verifyCaughtUpReplica(state adminState, maxLagBytes int64) error {
	if state.Role != "replica" {
		return fmt.Errorf("member %d role is %q, want replica", state.Member, state.Role)
	}
	return verifyLag(state, maxLagBytes)
}

func verifyLag(state adminState, maxLagBytes int64) error {
	if len(state.Failures) > 0 {
		return fmt.Errorf("member %d has failures: %v", state.Member, state.Failures)
	}
	if state.Lag > maxLagBytes {
		return fmt.Errorf("member %d lag is %d bytes, exceeds budget %d bytes", state.Member, state.Lag, maxLagBytes)
	}
	return nil
}

func currentPrimary(states map[int]adminState) (adminState, error) {
	var primary adminState
	count := 0
	for _, state := range states {
		if state.Role == "primary" || state.Role == "leader" {
			primary = state
			count++
		}
	}
	if count != 1 {
		return adminState{}, fmt.Errorf("found %d primary members, want exactly one", count)
	}
	return primary, nil
}

func candidateOrdinal(params stepParams) (int, error) {
	return parseOrdinal(params.Candidate)
}

func originalPrimaryOrdinal(req pluginapi.PackageStepRequest, params stepParams) int {
	value := strings.TrimSpace(params.OriginalPrimary)
	if value == "" {
		if previous, ok := previousOldPrimary(req.PreviousResults); ok {
			return previous
		}
		return 0
	}
	parsed, err := parseOrdinal(value)
	if err != nil {
		return 0
	}
	return parsed
}

func previousOldPrimary(previous map[string]json.RawMessage) (int, bool) {
	for _, raw := range previous {
		var wrapped struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil || len(bytes.TrimSpace(wrapped.Result)) == 0 {
			continue
		}
		var result struct {
			OldPrimary any `json:"old_primary"`
		}
		if err := json.Unmarshal(wrapped.Result, &result); err != nil || result.OldPrimary == nil {
			continue
		}
		switch value := result.OldPrimary.(type) {
		case float64:
			return int(value), true
		case string:
			parsed, err := parseOrdinal(value)
			return parsed, err == nil
		}
	}
	return 0, false
}

func parseOrdinal(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("member ordinal is required")
	}
	if strings.HasPrefix(value, "member-") {
		value = strings.TrimPrefix(value, "member-")
	}
	if idx := strings.LastIndex(value, "-"); idx >= 0 && idx+1 < len(value) {
		suffix := value[idx+1:]
		if _, err := strconv.Atoi(suffix); err == nil {
			value = suffix
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("member ordinal %d is negative", parsed)
	}
	return parsed, nil
}

func maxLag(params stepParams) int64 {
	if params.MaxReplicaLagBytes == nil {
		return 0
	}
	if *params.MaxReplicaLagBytes < 0 {
		return 0
	}
	return *params.MaxReplicaLagBytes
}

func providerOperation(req pluginapi.PackageStepRequest, kind, description string) sagaapi.ProviderOperationRef {
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	idParts := []string{req.Context.OperationID, req.Context.SagaID, req.Context.StepID, req.Kind}
	var clean []string
	for _, part := range idParts {
		part = sanitizeID(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	id := "postgres-ha"
	if len(clean) > 0 {
		id = strings.Join(clean, "-")
	}
	return sagaapi.ProviderOperationRef{
		Provider:    "postgres-ha",
		Kind:        kind,
		ID:          id,
		ObservedAt:  observedAt,
		Description: description,
	}
}

func succeeded(req pluginapi.PackageStepRequest, summary string, result map[string]any) sagaapi.PackageStepResultResponse {
	return sagaapi.PackageStepResultResponse{
		Status:  sagaapi.StepStatusSucceeded,
		Summary: summary,
		Result:  mustJSON(result),
	}
}

func failed(code, summary, cause string) sagaapi.PackageStepResultResponse {
	return sagaapi.PackageStepResultResponse{
		Status:  sagaapi.StepStatusFailed,
		Summary: summary,
		Failure: &sagaapi.StepFailure{
			Code:      code,
			Summary:   summary,
			Cause:     cause,
			Retriable: false,
		},
	}
}

func stateResult(state adminState, maxLagBytes int64) map[string]any {
	return map[string]any{
		"member":                state.Member,
		"role":                  state.Role,
		"lag_bytes":             state.Lag,
		"max_replica_lag_bytes": maxLagBytes,
		"timeline":              timeline(state),
		"term":                  state.Term,
	}
}

func topologyResult(states map[int]adminState, expectedPrimary string) map[string]any {
	members := make([]int, 0, len(states))
	for member := range states {
		members = append(members, member)
	}
	sort.Ints(members)
	var primary any
	outStates := make([]map[string]any, 0, len(members))
	for _, member := range members {
		state := states[member]
		if state.Role == "primary" || state.Role == "leader" {
			primary = state.Member
		}
		outStates = append(outStates, stateResult(state, 0))
	}
	out := map[string]any{
		"primary": primary,
		"members": outStates,
	}
	if expectedPrimary != "" {
		out["expected_primary"] = expectedPrimary
	}
	return out
}

func timeline(state adminState) string {
	if state.Term > 0 {
		return "term-" + strconv.FormatInt(state.Term, 10)
	}
	if state.Generation > 0 {
		return "generation-" + strconv.FormatInt(state.Generation, 10)
	}
	return ""
}

func parseMemberURLMap(values map[string]string) map[int]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[int]string, len(values))
	for key, value := range values {
		member, err := parseOrdinal(key)
		if err != nil || strings.TrimSpace(value) == "" {
			continue
		}
		out[member] = strings.TrimSpace(value)
	}
	return out
}

func sortedMemberKeys(values map[int]string) []int {
	out := make([]int, 0, len(values))
	for member := range values {
		out = append(out, member)
	}
	sort.Ints(out)
	return out
}

func environ(opts Options) func(string) string {
	if opts.Environ != nil {
		return opts.Environ
	}
	return os.Getenv
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sanitizeID(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func summaryForKind(kind string) string {
	switch kind {
	case "package.primary_switchover.verify_cluster_healthy":
		return "verify every Postgres HA member is healthy before planned switchover"
	case "package.primary_switchover.verify_candidate_caught_up", "postgres.verify_replica_lag":
		return "gate planned switchover on candidate health and replica lag"
	case "package.primary_switchover.move_primary", "postgres.switchover":
		return "perform a planned Postgres HA switchover to the caught-up candidate"
	case "package.primary_switchover.update_old_primary":
		return "update and catch up the old Postgres primary after switchover"
	case "package.primary_switchover.verify_old_primary_caught_up":
		return "verify the old Postgres primary has rejoined and caught up"
	case "package.primary_switchover.optional_failback":
		return "optionally move the Postgres primary role back after updating the old primary"
	case "package.primary_switchover.update_candidate":
		return "update and catch up the temporary Postgres primary"
	case "package.primary_switchover.verify_final_topology", "postgres.verify_timeline":
		return "verify final Postgres HA primary, replica lag, and timeline"
	default:
		return "run Postgres HA package step"
	}
}

func mustJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return body
}
