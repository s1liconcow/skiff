package aws

import (
	"context"

	"github.com/s1liconcow/skiff/internal/provider"
)

func (p *Provider) SnapshotDatabase(ctx context.Context, req provider.DatabaseSnapshotRequest) (*provider.DatabaseSnapshot, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.snapshot")
	}
	return p.clients.Databases.SnapshotDatabase(ctx, req)
}

func (p *Provider) VerifyRestorePoint(ctx context.Context, req provider.RestorePointRequest) (*provider.RestorePoint, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.verify_restore_point")
	}
	return p.clients.Databases.VerifyRestorePoint(ctx, req)
}

func (p *Provider) RestoreDatabase(ctx context.Context, req provider.DatabaseRestoreRequest) (*provider.DatabaseRestore, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.restore")
	}
	return p.clients.Databases.RestoreDatabase(ctx, req)
}

func (p *Provider) InspectDatabase(ctx context.Context, ref provider.DatabaseRef) (*provider.DatabaseInspection, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.inspect")
	}
	return p.clients.Databases.InspectDatabase(ctx, ref)
}

func (p *Provider) RunDatabaseSmokeQuery(ctx context.Context, req provider.DatabaseSmokeQueryRequest) (*provider.DatabaseSmokeQueryResult, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.smoke_query")
	}
	return p.clients.Databases.RunDatabaseSmokeQuery(ctx, req)
}

func (p *Provider) RunShadowServiceTest(ctx context.Context, req provider.ShadowServiceTestRequest) (*provider.ShadowServiceTestResult, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.shadow_service_test")
	}
	return p.clients.Databases.RunShadowServiceTest(ctx, req)
}

func (p *Provider) UpdateSecretPointer(ctx context.Context, req provider.SecretPointerRequest) (*provider.SecretPointerUpdate, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.secret_update_pointer")
	}
	return p.clients.Databases.UpdateSecretPointer(ctx, req)
}

func (p *Provider) RestartService(ctx context.Context, req provider.ServiceRestartRequest) (*provider.Rollout, error) {
	if p.clients.Databases == nil {
		return p.StartRollout(ctx, provider.RolloutRequest{
			Service:     req.Service,
			Env:         req.Env,
			ReleaseID:   req.ReleaseID,
			OperationID: req.OperationID,
		})
	}
	return p.clients.Databases.RestartService(ctx, req)
}

func (p *Provider) RetireDatabase(ctx context.Context, req provider.DatabaseRetireRequest) (*provider.DatabaseRetireResult, error) {
	if p.clients.Databases == nil {
		return nil, provider.Unsupported(Name, "database.retire")
	}
	return p.clients.Databases.RetireDatabase(ctx, req)
}
