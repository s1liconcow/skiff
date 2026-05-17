package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	KindSnapshot              = "database.snapshot"
	KindVerifyRestorePoint    = "database.verify_restore_point"
	KindRestoreToNewInstance  = "database.restore_to_new_instance"
	KindWaitAvailable         = "database.wait_available"
	KindRunSmokeQuery         = "database.run_smoke_query"
	KindShadowServiceTest     = "database.shadow_service_test"
	KindSecretUpdatePointer   = "secret.update_pointer"
	KindServiceRolloutRestart = "service.rollout_restart"
	KindRetireRestored        = "database.retire_restored_instance"
)

type Snapshot struct {
	Provider provider.DatabaseOperations
	Clock    func() time.Time
}

type VerifyRestorePoint struct {
	Provider provider.DatabaseOperations
}

type RestoreToNewInstance struct {
	Provider provider.DatabaseOperations
	Clock    func() time.Time
}

type WaitAvailable struct {
	Provider provider.DatabaseOperations
}

type RunSmokeQuery struct {
	Provider provider.DatabaseOperations
}

type ShadowServiceTest struct {
	Provider provider.DatabaseOperations
}

type SecretUpdatePointer struct {
	Provider provider.DatabaseOperations
}

type ServiceRolloutRestart struct {
	Provider provider.DatabaseOperations
	Clock    func() time.Time
}

type RetireRestored struct {
	Provider provider.DatabaseOperations
}

type Params struct {
	Database         string `json:"database"`
	Env              string `json:"env,omitempty"`
	Service          string `json:"service,omitempty"`
	OperationID      string `json:"operation_id,omitempty"`
	RestorePoint     string `json:"restore_point,omitempty"`
	RestoreTime      string `json:"restore_time,omitempty"`
	RestoredDatabase string `json:"restored_database,omitempty"`
	SecretRef        string `json:"secret_ref,omitempty"`
	SmokeQuery       string `json:"smoke_query,omitempty"`
	ReleaseID        string `json:"release_id,omitempty"`
	Mode             string `json:"mode,omitempty"`
}

func New(provider provider.DatabaseOperations) []steps.Step {
	return []steps.Step{
		Snapshot{Provider: provider},
		VerifyRestorePoint{Provider: provider},
		RestoreToNewInstance{Provider: provider},
		WaitAvailable{Provider: provider},
		RunSmokeQuery{Provider: provider},
		ShadowServiceTest{Provider: provider},
		SecretUpdatePointer{Provider: provider},
		ServiceRolloutRestart{Provider: provider},
		RetireRestored{Provider: provider},
	}
}

func (s Snapshot) Kind() string { return KindSnapshot }

func (s Snapshot) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireDatabase(decoded)
}

func (s Snapshot) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "create a provider database snapshot and store the provider operation before restore work continues", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s Snapshot) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := requireDatabase(params); err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("database snapshot provider is required")
	}
	snapshot, err := s.Provider.SnapshotDatabase(ctx, provider.DatabaseSnapshotRequest{
		Ref:         databaseRef(params),
		OperationID: params.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errors.New("database snapshot provider returned no result")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":          true,
			"database":    params.Database,
			"snapshot_id": firstNonEmpty(snapshot.ID, snapshot.ProviderID),
			"provider":    snapshot.Provider,
			"provider_id": snapshot.ProviderID,
			"status":      snapshot.Status,
			"next_action": "verify_restore_point",
		}),
		ProviderOperations: []schema.ProviderOperationRef{providerOperation(firstNonEmpty(snapshot.ID, snapshot.ProviderID), snapshot.Provider, "database_snapshot", "database snapshot started", s.now())},
		Summary:            fmt.Sprintf("snapshot started for database %s", params.Database),
	}, nil
}

func (s Snapshot) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s Snapshot) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "database snapshots are retained for recovery and are not removed by default"})}, nil
}

func (s Snapshot) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s VerifyRestorePoint) Kind() string { return KindVerifyRestorePoint }

func (s VerifyRestorePoint) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return requireDatabase(decoded)
}

func (s VerifyRestorePoint) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "verify that the requested restore point exists before restore starts", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s VerifyRestorePoint) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := requireDatabase(params); err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("restore point provider is required")
	}
	restoreTime, err := parseRestoreTime(params.RestoreTime)
	if err != nil {
		return nil, err
	}
	point, err := s.Provider.VerifyRestorePoint(ctx, provider.RestorePointRequest{
		Ref:          databaseRef(params),
		RestorePoint: firstNonEmpty(params.RestorePoint, previousString(req.PreviousResults, "snapshot-current", "snapshot_id")),
		RestoreTime:  restoreTime,
		SnapshotID:   previousString(req.PreviousResults, "snapshot-current", "snapshot_id"),
	})
	if err != nil {
		return nil, err
	}
	if point == nil {
		return nil, errors.New("restore point provider returned no result")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":               true,
			"database":         params.Database,
			"restore_point_id": firstNonEmpty(point.ID, point.ProviderID),
			"provider":         point.Provider,
			"provider_id":      point.ProviderID,
			"status":           point.Status,
			"next_action":      "snapshot_current",
		}),
		Summary: fmt.Sprintf("restore point verified for database %s", params.Database),
	}, nil
}

func (s VerifyRestorePoint) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s VerifyRestorePoint) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "restore point verification has no compensation"})}, nil
}

func (s VerifyRestorePoint) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s RestoreToNewInstance) Kind() string { return KindRestoreToNewInstance }

func (s RestoreToNewInstance) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if err := requireDatabase(decoded); err != nil {
		return err
	}
	if strings.TrimSpace(decoded.RestoredDatabase) == "" {
		return errors.New("restored database is required")
	}
	return nil
}

func (s RestoreToNewInstance) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "start restore into a new managed database instance; production remains unchanged", Risk: schema.RiskHigh, Reversibility: schema.Compensatable}, nil
}

func (s RestoreToNewInstance) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := requireDatabase(params); err != nil {
		return nil, err
	}
	if params.RestoredDatabase == "" {
		return nil, errors.New("restored database is required")
	}
	if s.Provider == nil {
		return nil, errors.New("database restore provider is required")
	}
	restoreTime, err := parseRestoreTime(params.RestoreTime)
	if err != nil {
		return nil, err
	}
	restore, err := s.Provider.RestoreDatabase(ctx, provider.DatabaseRestoreRequest{
		Source:         databaseRef(params),
		RestorePointID: firstNonEmpty(params.RestorePoint, previousString(req.PreviousResults, "verify-restore-point", "restore_point_id")),
		RestoreTime:    restoreTime,
		TargetDatabase: params.RestoredDatabase,
		OperationID:    params.OperationID,
		SagaID:         req.SagaID,
		TraceID:        req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	if restore == nil {
		return nil, errors.New("database restore provider returned no result")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":                true,
			"source_database":   params.Database,
			"restored_database": firstNonEmpty(restore.Database, params.RestoredDatabase),
			"restore_id":        firstNonEmpty(restore.ID, restore.ProviderID),
			"provider":          restore.Provider,
			"provider_id":       restore.ProviderID,
			"status":            restore.Status,
			"next_action":       "wait_available",
		}),
		ProviderOperations: []schema.ProviderOperationRef{providerOperation(firstNonEmpty(restore.ID, restore.ProviderID), restore.Provider, "database_restore", "database restore started", s.now())},
		Summary:            fmt.Sprintf("restore started into %s", firstNonEmpty(restore.Database, params.RestoredDatabase)),
	}, nil
}

func (s RestoreToNewInstance) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RestoreToNewInstance) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	retire := RetireRestored{Provider: s.Provider}
	return retire.retire(ctx, req, params, result, "restore failed before cutover")
}

func (s RestoreToNewInstance) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s WaitAvailable) Kind() string { return KindWaitAvailable }

func (s WaitAvailable) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if err := requireDatabase(decoded); err != nil {
		return err
	}
	if decoded.RestoredDatabase == "" {
		return errors.New("restored database is required")
	}
	return nil
}

func (s WaitAvailable) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "inspect restored database availability using the stored provider restore ID", Risk: schema.RiskMedium, Reversibility: schema.Reversible}, nil
}

func (s WaitAvailable) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := requireDatabase(params); err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("database availability provider is required")
	}
	inspection, err := s.Provider.InspectDatabase(ctx, restoredDatabaseRef(params, req.PreviousResults))
	if err != nil {
		return nil, err
	}
	if inspection == nil {
		return nil, errors.New("database availability provider returned no inspection")
	}
	if !available(inspection.Status) {
		return failed("DATABASE_NOT_AVAILABLE", fmt.Sprintf("restored database %s status is %s", params.RestoredDatabase, firstNonEmpty(inspection.Status, "unknown")), map[string]any{
			"database":    params.RestoredDatabase,
			"status":      inspection.Status,
			"provider_id": inspection.ProviderID,
		}), nil
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":                true,
			"restored_database": params.RestoredDatabase,
			"provider":          inspection.Provider,
			"provider_id":       firstNonEmpty(inspection.ProviderID, inspection.Ref.ProviderID),
			"status":            inspection.Status,
			"endpoint":          inspection.Endpoint,
			"next_action":       "run_smoke_query",
		}),
		Summary: fmt.Sprintf("restored database %s is available", params.RestoredDatabase),
	}, nil
}

func (s WaitAvailable) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s WaitAvailable) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "availability wait has no compensation"})}, nil
}

func (s WaitAvailable) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s RunSmokeQuery) Kind() string { return KindRunSmokeQuery }

func (s RunSmokeQuery) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if err := requireDatabase(decoded); err != nil {
		return err
	}
	if decoded.RestoredDatabase == "" {
		return errors.New("restored database is required")
	}
	return nil
}

func (s RunSmokeQuery) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "run a non-destructive smoke query against the restored database before cutover", Risk: schema.RiskMedium, Reversibility: schema.Reversible}, nil
}

func (s RunSmokeQuery) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("database smoke query provider is required")
	}
	query := firstNonEmpty(params.SmokeQuery, "select 1")
	result, err := s.Provider.RunDatabaseSmokeQuery(ctx, provider.DatabaseSmokeQueryRequest{Ref: restoredDatabaseRef(params, req.PreviousResults), Query: query})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("database smoke query provider returned no result")
	}
	payload := map[string]any{
		"ok":                result.OK,
		"restored_database": params.RestoredDatabase,
		"query":             query,
		"summary":           result.Summary,
		"rows":              result.Rows,
		"latency_ms":        result.LatencyMS,
	}
	if !result.OK {
		return failed("DATABASE_SMOKE_QUERY_FAILED", firstNonEmpty(result.Summary, "database smoke query failed"), payload), nil
	}
	payload["next_action"] = "shadow_service_or_approval"
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(payload), Summary: firstNonEmpty(result.Summary, "database smoke query passed")}, nil
}

func (s RunSmokeQuery) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RunSmokeQuery) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "smoke query has no compensation"})}, nil
}

func (s RunSmokeQuery) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s ShadowServiceTest) Kind() string { return KindShadowServiceTest }

func (s ShadowServiceTest) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if decoded.Service == "" {
		return errors.New("service is required")
	}
	return requireDatabase(decoded)
}

func (s ShadowServiceTest) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "test the attached service against the restored database without changing production traffic", Risk: schema.RiskMedium, Reversibility: schema.Reversible}, nil
}

func (s ShadowServiceTest) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if params.Service == "" {
		return nil, errors.New("service is required for shadow service test")
	}
	if s.Provider == nil {
		return nil, errors.New("shadow service test provider is required")
	}
	result, err := s.Provider.RunShadowServiceTest(ctx, provider.ShadowServiceTestRequest{
		Service:  params.Service,
		Env:      params.Env,
		Database: restoredDatabaseRef(params, req.PreviousResults),
		Query:    firstNonEmpty(params.SmokeQuery, "select 1"),
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("shadow service test provider returned no result")
	}
	payload := map[string]any{
		"ok":                result.OK,
		"service":           params.Service,
		"restored_database": params.RestoredDatabase,
		"summary":           result.Summary,
	}
	if !result.OK {
		return failed("DATABASE_SHADOW_SERVICE_FAILED", firstNonEmpty(result.Summary, "shadow service test failed"), payload), nil
	}
	payload["next_action"] = "approve_cutover"
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(payload), Summary: firstNonEmpty(result.Summary, "shadow service test passed")}, nil
}

func (s ShadowServiceTest) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s ShadowServiceTest) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "shadow service test has no compensation"})}, nil
}

func (s ShadowServiceTest) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s SecretUpdatePointer) Kind() string { return KindSecretUpdatePointer }

func (s SecretUpdatePointer) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if decoded.SecretRef == "" {
		return errors.New("secret ref is required")
	}
	return requireDatabase(decoded)
}

func (s SecretUpdatePointer) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "update the secret pointer to the restored database after approval", Risk: schema.RiskHigh, Reversibility: schema.Compensatable}, nil
}

func (s SecretUpdatePointer) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if s.Provider == nil {
		return nil, errors.New("secret pointer provider is required")
	}
	ref := restoredDatabaseRef(params, req.PreviousResults)
	update, err := s.Provider.UpdateSecretPointer(ctx, provider.SecretPointerRequest{
		Service:          params.Service,
		Env:              params.Env,
		Database:         params.Database,
		SecretRef:        params.SecretRef,
		TargetDatabase:   ref.Database,
		TargetProviderID: ref.ProviderID,
		OperationID:      params.OperationID,
		SagaID:           req.SagaID,
		TraceID:          req.TraceID,
		Reason:           "database restore cutover",
	})
	if err != nil {
		return nil, err
	}
	if update == nil {
		return nil, errors.New("secret pointer provider returned no result")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":                true,
			"secret_ref":        update.SecretRef,
			"previous_version":  update.PreviousVersion,
			"new_version":       update.NewVersion,
			"previous_database": update.PreviousDatabase,
			"new_database":      update.NewDatabase,
			"next_action":       "roll_service",
		}),
		Summary: fmt.Sprintf("secret pointer %s updated to restored database", params.SecretRef),
	}, nil
}

func (s SecretUpdatePointer) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s SecretUpdatePointer) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	payload := payloadMap(result.Result)
	previousDB := stringFromPayload(payload, "previous_database")
	previousVersion := stringFromPayload(payload, "previous_version")
	if previousDB == "" && previousVersion == "" {
		return failed("SECRET_COMPENSATION_MISSING_PREVIOUS", "secret pointer result does not include previous database or version", payload), nil
	}
	if s.Provider == nil {
		return nil, errors.New("secret pointer provider is required")
	}
	update, err := s.Provider.UpdateSecretPointer(ctx, provider.SecretPointerRequest{
		Service:         params.Service,
		Env:             params.Env,
		Database:        params.Database,
		SecretRef:       params.SecretRef,
		TargetDatabase:  previousDB,
		PreviousVersion: previousVersion,
		OperationID:     params.OperationID,
		SagaID:          req.SagaID,
		TraceID:         req.TraceID,
		Reason:          "database restore compensation",
	})
	if err != nil {
		return nil, err
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":                true,
			"secret_ref":        params.SecretRef,
			"restored_version":  previousVersion,
			"previous_database": previousDB,
			"new_version":       updateString(update, "new_version"),
			"summary":           "secret pointer restored to previous database reference",
		}),
		Summary: "secret pointer compensation completed",
	}, nil
}

func (s SecretUpdatePointer) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s ServiceRolloutRestart) Kind() string { return KindServiceRolloutRestart }

func (s ServiceRolloutRestart) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if decoded.Service == "" {
		return errors.New("service is required")
	}
	return nil
}

func (s ServiceRolloutRestart) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "restart the attached service so it reads the approved database secret pointer", Risk: schema.RiskHigh, Reversibility: schema.PartiallyReversible}, nil
}

func (s ServiceRolloutRestart) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if params.Service == "" {
		return nil, errors.New("service is required")
	}
	if s.Provider == nil {
		return nil, errors.New("service restart provider is required")
	}
	rollout, err := s.Provider.RestartService(ctx, provider.ServiceRestartRequest{
		Service:     params.Service,
		Env:         params.Env,
		ReleaseID:   params.ReleaseID,
		OperationID: params.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
		Reason:      "database restore cutover",
	})
	if err != nil {
		return nil, err
	}
	if rollout == nil {
		return nil, errors.New("service restart provider returned no rollout")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":          true,
			"service":     params.Service,
			"rollout_id":  rollout.ID,
			"provider":    rollout.Provider,
			"provider_id": rollout.ProviderID,
			"next_action": "verify_service",
		}),
		ProviderOperations: []schema.ProviderOperationRef{providerOperation(firstNonEmpty(rollout.ID, rollout.ProviderID), rollout.Provider, "service_restart", "service restart started", s.now())},
		Summary:            fmt.Sprintf("service %s restart started", params.Service),
	}, nil
}

func (s ServiceRolloutRestart) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s ServiceRolloutRestart) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "service restart is partially reversible; use rollback if the new database pointer is bad"})}, nil
}

func (s ServiceRolloutRestart) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s RetireRestored) Kind() string { return KindRetireRestored }

func (s RetireRestored) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	if decoded.RestoredDatabase == "" {
		return errors.New("restored database is required")
	}
	return nil
}

func (s RetireRestored) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "delete or mark the restored database for cleanup before cutover", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s RetireRestored) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	return s.retire(ctx, req, params, schema.StepResult{}, "manual cleanup of restored database")
}

func (s RetireRestored) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RetireRestored) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "retire restored database has no compensation"})}, nil
}

func (s RetireRestored) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s RetireRestored) retire(ctx context.Context, req steps.StepRequest, params Params, result schema.StepResult, reason string) (*steps.StepResult, error) {
	if s.Provider == nil {
		return nil, errors.New("database retire provider is required")
	}
	ref := provider.DatabaseRef{
		Service:    params.Service,
		Env:        params.Env,
		Database:   firstNonEmpty(stringFromPayload(payloadMap(result.Result), "restored_database"), params.RestoredDatabase),
		ProviderID: stringFromPayload(payloadMap(result.Result), "provider_id"),
	}
	retired, err := s.Provider.RetireDatabase(ctx, provider.DatabaseRetireRequest{
		Ref:         ref,
		OperationID: params.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
		Reason:      reason,
	})
	if err != nil {
		return nil, err
	}
	if retired == nil {
		return nil, errors.New("database retire provider returned no result")
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":          true,
			"database":    retired.Database,
			"provider":    retired.Provider,
			"provider_id": retired.ProviderID,
			"status":      retired.Status,
			"summary":     reason,
		}),
		Summary: fmt.Sprintf("restored database %s retired", retired.Database),
	}, nil
}

func decodeParams(params json.RawMessage) (Params, error) {
	var decoded Params
	if len(bytes.TrimSpace(params)) == 0 {
		return decoded, nil
	}
	if err := json.Unmarshal(params, &decoded); err != nil {
		return decoded, err
	}
	decoded.Database = strings.TrimSpace(decoded.Database)
	decoded.Env = strings.TrimSpace(decoded.Env)
	decoded.Service = strings.TrimSpace(decoded.Service)
	decoded.OperationID = strings.TrimSpace(decoded.OperationID)
	decoded.RestorePoint = strings.TrimSpace(decoded.RestorePoint)
	decoded.RestoreTime = strings.TrimSpace(decoded.RestoreTime)
	decoded.RestoredDatabase = strings.TrimSpace(decoded.RestoredDatabase)
	decoded.SecretRef = strings.TrimSpace(decoded.SecretRef)
	decoded.SmokeQuery = strings.TrimSpace(decoded.SmokeQuery)
	decoded.ReleaseID = strings.TrimSpace(decoded.ReleaseID)
	decoded.Mode = strings.TrimSpace(decoded.Mode)
	return decoded, nil
}

func requireDatabase(params Params) error {
	if params.Database == "" {
		return errors.New("database is required")
	}
	return nil
}

func databaseRef(params Params) provider.DatabaseRef {
	return provider.DatabaseRef{Service: params.Service, Env: params.Env, Database: params.Database}
}

func restoredDatabaseRef(params Params, previous map[string]schema.StepResult) provider.DatabaseRef {
	return provider.DatabaseRef{
		Service:    params.Service,
		Env:        params.Env,
		Database:   firstNonEmpty(previousString(previous, "restore-new-db", "restored_database"), params.RestoredDatabase),
		ProviderID: firstNonEmpty(previousString(previous, "wait-restored-db", "provider_id"), previousString(previous, "restore-new-db", "provider_id")),
	}
}

func parseRestoreTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func payloadMap(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func previousString(results map[string]schema.StepResult, stepID, key string) string {
	if results == nil {
		return ""
	}
	result, ok := results[stepID]
	if !ok {
		return ""
	}
	return stringFromPayload(payloadMap(result.Result), key)
}

func stringFromPayload(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func providerOperation(id, providerName, kind, summary string, now time.Time) schema.ProviderOperationRef {
	if strings.TrimSpace(providerName) == "" {
		providerName = "unknown"
	}
	return schema.ProviderOperationRef{
		Provider:    providerName,
		Kind:        kind,
		ID:          id,
		ObservedAt:  canonical.Time(now.UTC()),
		Description: summary,
	}
}

func failed(code, summary string, payload map[string]any) *steps.StepResult {
	return &steps.StepResult{
		Status: steps.StatusFailed,
		Result: rawJSON(payload),
		Failure: &schema.StepFailure{
			Code:    code,
			Summary: summary,
		},
		Summary: summary,
	}
}

func available(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "available", "active", "healthy", "ok", "ready", "running", "succeeded", "configured", "applied", "unchanged":
		return true
	default:
		return false
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

func updateString(update *provider.SecretPointerUpdate, field string) string {
	if update == nil {
		return ""
	}
	switch field {
	case "new_version":
		return update.NewVersion
	default:
		return ""
	}
}

func (s Snapshot) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s RestoreToNewInstance) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s ServiceRolloutRestart) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}
