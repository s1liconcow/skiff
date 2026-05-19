package aws

import (
	"context"
	"errors"
	"strings"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func (p *Provider) lowerOptionsWithEnvironmentRoot(ctx context.Context, env string, opts LowerOptions) (LowerOptions, error) {
	if p.stateStore == nil || strings.TrimSpace(env) == "" {
		return opts, nil
	}
	key, err := paths.EnvironmentRoot(env)
	if err != nil {
		return opts, err
	}
	obj, err := p.stateStore.Get(ctx, key)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return opts, nil
		}
		return opts, err
	}
	var root schema.EnvironmentRoot
	if err := canonical.UnmarshalStrict(obj.Body, &root); err != nil {
		return opts, err
	}
	if root.Provider != "" && root.Provider != Name {
		return opts, nil
	}
	if strings.TrimSpace(opts.Region) == "" {
		opts.Region = strings.TrimSpace(root.Region)
	}
	if strings.TrimSpace(opts.StateBucket) == "" {
		opts.StateBucket = strings.TrimSpace(root.StateBucket)
	}
	if root.Network != nil {
		if strings.TrimSpace(opts.VPCID) == "" {
			opts.VPCID = strings.TrimSpace(root.Network.VPCID)
		}
		if len(cleanStringSlice(opts.SubnetIDs)) == 0 {
			opts.SubnetIDs = firstNonEmptyStringSlice(root.Network.PrivateSubnetIDs, root.Network.PublicSubnetIDs)
		}
	}
	if root.Ingress != nil && root.Ingress.LoadBalancer != nil {
		lb := root.Ingress.LoadBalancer
		if strings.TrimSpace(opts.IngressBaseDomain) == "" {
			opts.IngressBaseDomain = firstNonEmpty(root.Ingress.BaseDomain, root.Ingress.Host)
		}
		if strings.TrimSpace(opts.IngressDefaultHostTemplate) == "" {
			opts.IngressDefaultHostTemplate = strings.TrimSpace(root.Ingress.DefaultHostTemplate)
		}
		if strings.TrimSpace(opts.IngressCertificateRef) == "" {
			opts.IngressCertificateRef = strings.TrimSpace(lb.CertificateARN)
		}
		if strings.TrimSpace(opts.LoadBalancerSecurityGroupRef) == "" {
			opts.LoadBalancerSecurityGroupRef = strings.TrimSpace(lb.SecurityGroupID)
		}
		if strings.TrimSpace(opts.ALBListenerARN) == "" {
			opts.ALBListenerARN = firstNonEmpty(lb.HTTPSListenerARN, lb.HTTPListenerARN)
		}
	}
	if root.Runner != nil {
		if strings.TrimSpace(opts.AMIID) == "" {
			opts.AMIID = runnerAMIReference(*root.Runner)
		}
		if strings.TrimSpace(opts.RunnerInstallVersion) == "" {
			opts.RunnerInstallVersion = strings.TrimSpace(root.Runner.InstallVersion)
		}
		if strings.TrimSpace(opts.RunnerInstallBaseURL) == "" {
			opts.RunnerInstallBaseURL = strings.TrimSpace(root.Runner.InstallBaseURL)
		}
		if strings.TrimSpace(opts.RunnerInstallScriptURL) == "" {
			opts.RunnerInstallScriptURL = strings.TrimSpace(root.Runner.InstallScriptURL)
		}
		if strings.TrimSpace(opts.RunnerInstallPublicKeyRef) == "" {
			opts.RunnerInstallPublicKeyRef = strings.TrimSpace(root.Runner.InstallPublicKeyRef)
		}
	}
	return opts, nil
}

func runnerAMIReference(runner schema.EnvironmentRunner) string {
	if amiID := strings.TrimSpace(runner.AMIID); amiID != "" {
		return amiID
	}
	if parameter := strings.TrimSpace(runner.AMISSMParameter); parameter != "" {
		return "resolve:ssm:" + strings.TrimPrefix(parameter, "resolve:ssm:")
	}
	return ""
}

func firstNonEmptyStringSlice(values ...[]string) []string {
	for _, value := range values {
		cleaned := cleanStringSlice(value)
		if len(cleaned) > 0 {
			return cleaned
		}
	}
	return nil
}
