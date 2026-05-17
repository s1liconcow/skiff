package aws

import (
	"context"

	"github.com/s1liconcow/skiff/internal/provider"
)

func (p *Provider) CreateSecretVersion(ctx context.Context, req provider.SecretVersionRequest) (*provider.SecretVersion, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "secret.create_version")
	}
	return p.clients.Secrets.CreateSecretVersion(ctx, req)
}

func (p *Provider) ValidateSecretVersion(ctx context.Context, req provider.SecretValidationRequest) (*provider.SecretValidationResult, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "secret.validate_version")
	}
	return p.clients.Secrets.ValidateSecretVersion(ctx, req)
}

func (p *Provider) UpdateSecretVersionPointer(ctx context.Context, req provider.SecretUpdateRequest) (*provider.SecretPointer, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "secret.update_pointer")
	}
	return p.clients.Secrets.UpdateSecretVersionPointer(ctx, req)
}

func (p *Provider) RestoreSecretVersion(ctx context.Context, req provider.SecretRestoreRequest) (*provider.SecretPointer, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "secret.restore_previous_version")
	}
	return p.clients.Secrets.RestoreSecretVersion(ctx, req)
}

func (p *Provider) CanaryServiceWithSecret(ctx context.Context, req provider.SecretCanaryRequest) (*provider.SecretCanaryResult, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "service.canary_with_secret")
	}
	return p.clients.Secrets.CanaryServiceWithSecret(ctx, req)
}

func (p *Provider) RollConsumersWithSecret(ctx context.Context, req provider.SecretRollConsumersRequest) (*provider.SecretRollConsumersResult, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "service.roll_consumers")
	}
	return p.clients.Secrets.RollConsumersWithSecret(ctx, req)
}

func (p *Provider) DisableOldCredential(ctx context.Context, req provider.CredentialDisableRequest) (*provider.CredentialDisableResult, error) {
	if p.clients.Secrets == nil {
		return nil, provider.Unsupported(Name, "credential.disable_old")
	}
	return p.clients.Secrets.DisableOldCredential(ctx, req)
}
