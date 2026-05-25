package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/state/canonical"
)

func TestPlanAWSComplete(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:              "prod",
		EnvironmentClass: "production",
		Region:           "us-west-2",
		StateBucket:      "skiff-state-prod",
		Now:              time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Provider != ProviderAWS || plan.Env != "prod" || plan.Region != "us-west-2" {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if plan.StateBucketURI != "s3://skiff-state-prod" {
		t.Fatalf("state bucket URI = %q", plan.StateBucketURI)
	}
	if plan.KMSAlias != "alias/skiff-prod-state" {
		t.Fatalf("kms alias = %q", plan.KMSAlias)
	}
	if plan.RootObjectKey != "envs/prod/root.json" {
		t.Fatalf("root object key = %q", plan.RootObjectKey)
	}
	if plan.RootConfig.EnvironmentClass != "production" {
		t.Fatalf("environment class = %q", plan.RootConfig.EnvironmentClass)
	}
	if plan.RootConfig.ReleasePolicy == nil || !plan.RootConfig.ReleasePolicy.RequireSignedReleases || plan.RootConfig.ReleasePolicy.AllowUnsignedCode {
		t.Fatalf("production release policy = %+v", plan.RootConfig.ReleasePolicy)
	}
	if len(plan.Resources) != 8 {
		t.Fatalf("resource count = %d, want 8", len(plan.Resources))
	}
	wantPolicies := []string{"deployer", "developer", "runner", "skiffd"}
	if got := sortedPolicyNames(plan.IAMPolicies); !reflect.DeepEqual(got, wantPolicies) {
		t.Fatalf("policy names = %#v, want %#v", got, wantPolicies)
	}
	if plan.RootConfig.Roles["developer"] != "skiff-prod-developer" {
		t.Fatalf("developer role = %q", plan.RootConfig.Roles["developer"])
	}
	deployerSpec := roleSpec("deployer", plan.RootConfig.Roles["deployer"], plan.IAMPolicies["deployer"], plan.Env)
	if deployerSpec.MaxSessionDurationSeconds != 3600 || len(deployerSpec.TrustPolicy.Statement) != 2 {
		t.Fatalf("deployer role must require temporary auditable escalation: %+v", deployerSpec)
	}
	if len(plan.BucketPolicy.Statement) == 0 {
		t.Fatal("missing bucket policy statements")
	}
	if plan.RootConfig.Runner == nil || plan.RootConfig.Runner.AMISSMParameter != DefaultRunnerAMISSMParameter {
		t.Fatalf("runner AMI SSM parameter = %+v", plan.RootConfig.Runner)
	}
	if plan.RootConfig.Runner.InstallVersion != "" {
		t.Fatalf("official runner AMI should not need first-boot install: %+v", plan.RootConfig.Runner)
	}
}

func TestPlanAWSDefaultsToDevelopmentClass(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "prod",
		Region:      "us-west-2",
		StateBucket: "skiff-state-prod",
		Now:         time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.EnvironmentClass != "development" {
		t.Fatalf("environment class = %q", plan.RootConfig.EnvironmentClass)
	}
	if plan.RootConfig.ReleasePolicy == nil || plan.RootConfig.ReleasePolicy.RequireSignedReleases || !plan.RootConfig.ReleasePolicy.AllowUnsignedCode {
		t.Fatalf("default release policy = %+v", plan.RootConfig.ReleasePolicy)
	}
}

func TestPlanAWSDevelopmentClassAllowsUnsignedCode(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:              "david-dev",
		EnvironmentClass: "development",
		Region:           "us-west-2",
		Now:              time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.EnvironmentClass != "development" {
		t.Fatalf("environment class = %q", plan.RootConfig.EnvironmentClass)
	}
	if plan.RootConfig.ReleasePolicy == nil || plan.RootConfig.ReleasePolicy.RequireSignedReleases || !plan.RootConfig.ReleasePolicy.AllowUnsignedCode {
		t.Fatalf("development release policy = %+v", plan.RootConfig.ReleasePolicy)
	}
}

func TestPlanAWSManagedPrivateNetwork(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:     "dev",
		Region:  "us-west-2",
		Network: NetworkManaged,
		Ingress: IngressPrivate,
		Now:     time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plan.StateBucket, "skiff-") || !strings.HasSuffix(plan.StateBucket, "-dev-us-west-2-state") {
		t.Fatalf("default state bucket = %q", plan.StateBucket)
	}
	if plan.RootConfig.Network == nil || plan.RootConfig.Network.Mode != NetworkManaged {
		t.Fatalf("missing managed network root config: %+v", plan.RootConfig.Network)
	}
	if plan.RootConfig.Ingress == nil || plan.RootConfig.Ingress.Type != IngressPrivate {
		t.Fatalf("missing private ingress root config: %+v", plan.RootConfig.Ingress)
	}
	if plan.RootConfig.Runner == nil || plan.RootConfig.Runner.AMISSMParameter != DefaultRunnerAMISSMParameter {
		t.Fatalf("missing runner SSM default: %+v", plan.RootConfig.Runner)
	}
	if len(plan.Resources) != 14 {
		t.Fatalf("resource count = %d, want 14", len(plan.Resources))
	}
	if !hasBootstrapResourceKind(plan.Resources, "vpc") || !hasBootstrapResourceKind(plan.Resources, "nat-gateway") {
		t.Fatalf("managed network resources missing: %+v", plan.Resources)
	}
}

func TestPlanAWSManagedPublicIngress(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:     "quickstart",
		Region:  "us-west-2",
		Network: NetworkManaged,
		Ingress: IngressPublic,
		Now:     time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.Ingress == nil || plan.RootConfig.Ingress.Type != IngressPublic {
		t.Fatalf("missing public ingress root config: %+v", plan.RootConfig.Ingress)
	}
	if plan.RootConfig.Ingress.LoadBalancer == nil {
		t.Fatalf("missing public load balancer defaults: %+v", plan.RootConfig.Ingress)
	}
	lb := plan.RootConfig.Ingress.LoadBalancer
	if lb.ARN != "${aws_lb.skiff_public.arn}" || lb.SecurityGroupID != "${aws_security_group.skiff_public_lb.id}" || lb.HTTPListenerARN != "${aws_lb_listener.skiff_public_http.arn}" {
		t.Fatalf("unexpected public load balancer defaults: %+v", lb)
	}
	if len(plan.Resources) != 17 {
		t.Fatalf("resource count = %d, want 17", len(plan.Resources))
	}
	for _, kind := range []string{"vpc", "load-balancer", "listener"} {
		if !hasBootstrapResourceKind(plan.Resources, kind) {
			t.Fatalf("managed public ingress resources missing %s: %+v", kind, plan.Resources)
		}
	}
}

func TestPlanAWSManagedPublicIngressWithDomain(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "quickstart",
		Region:      "us-west-2",
		Network:     NetworkManaged,
		Ingress:     IngressPublic,
		CompanyName: "Acme Corp",
		DomainName:  "example.com",
		Now:         time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CompanySlug != "acme-corp" {
		t.Fatalf("company slug = %q", plan.CompanySlug)
	}
	if plan.PublicBaseDomain != "quickstart.example.com" {
		t.Fatalf("public base domain = %q", plan.PublicBaseDomain)
	}
	if plan.DefaultHostTemplate != "{service}.quickstart.example.com" {
		t.Fatalf("default host template = %q", plan.DefaultHostTemplate)
	}
	if plan.StateBucket != "skiff-acme-corp-quickstart-us-west-2-state" {
		t.Fatalf("state bucket = %q, want company-derived bucket without generated suffix", plan.StateBucket)
	}
	ingress := plan.RootConfig.Ingress
	if ingress == nil || ingress.BaseDomain != "quickstart.example.com" || ingress.DefaultHostTemplate != "{service}.quickstart.example.com" || ingress.DomainName != "example.com" {
		t.Fatalf("missing public ingress hostname defaults: %+v", ingress)
	}
	lb := ingress.LoadBalancer
	if lb == nil || lb.DNSName != "quickstart.example.com" || lb.ProviderDNSName != "${aws_lb.skiff_public.dns_name}" {
		t.Fatalf("unexpected DNS defaults: %+v", lb)
	}
	if lb.HTTPSListenerARN != "${aws_lb_listener.skiff_public_https.arn}" || lb.CertificateARN != "${aws_acm_certificate_validation.skiff_public.certificate_arn}" {
		t.Fatalf("unexpected HTTPS/certificate defaults: %+v", lb)
	}
	for _, kind := range []string{"dns-record", "certificate"} {
		if !hasBootstrapResourceKind(plan.Resources, kind) {
			t.Fatalf("managed public ingress resources missing %s: %+v", kind, plan.Resources)
		}
	}
}

func TestPlanAWSIncludesReleaseSigningTrust(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:                     "quickstart",
		Region:                  "us-west-2",
		StateBucket:             "skiff-state-quickstart",
		ReleaseSigningKeyID:     "skiff-quickstart-release-test",
		ReleaseSigningKeyRef:    "keychain://dev.skiff.release-signing/quickstart/release",
		ReleaseSigningPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.ReleaseTrust == nil || len(plan.RootConfig.ReleaseTrust.Keys) != 1 {
		t.Fatalf("release trust missing from root config: %+v", plan.RootConfig.ReleaseTrust)
	}
	key := plan.RootConfig.ReleaseTrust.Keys[0]
	if key.KeyID != "skiff-quickstart-release-test" || key.Backend != "keychain" || key.Algorithm != "ed25519" || key.Encoding != "raw" || key.Status != "active" {
		t.Fatalf("unexpected release trust key: %+v", key)
	}
	if plan.ReleaseSigningKeyID != "skiff-quickstart-release-test" || plan.ReleaseSigningKeyRef == "" {
		t.Fatalf("plan signing summary missing: %+v", plan)
	}
}

func TestPlanAWSAllowsTerraformManagedKMSReleaseSigner(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:                  "quickstart",
		Region:               "us-west-2",
		StateBucket:          "skiff-state-quickstart",
		ReleaseSigningKeyRef: "aws-kms://alias/skiff-quickstart-release-signing?region=us-west-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.ReleaseTrust != nil {
		t.Fatalf("terraform-managed signer should materialize release trust in Terraform, not static plan: %+v", plan.RootConfig.ReleaseTrust)
	}
	if !hasBootstrapResource(plan.Resources, "kms-key", "alias/skiff-quickstart-release-signing") {
		t.Fatalf("plan missing KMS signing key resource: %+v", plan.Resources)
	}
	terraform, err := TerraformAWS(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`resource "aws_kms_key" "skiff_release_signing"`, `data "aws_kms_public_key" "skiff_release_signing"`, `release_trust = {`} {
		if !strings.Contains(terraform, want) {
			t.Fatalf("terraform output missing %q:\n%s", want, terraform)
		}
	}
}

func TestPlanAWSExplicitRunnerAMIIDOverridesSSMDefault(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "prod",
		Region:      "us-west-2",
		StateBucket: "skiff-state-prod",
		RunnerAMIID: "ami-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.Runner == nil {
		t.Fatal("missing runner defaults")
	}
	if plan.RootConfig.Runner.AMIID != "ami-123" {
		t.Fatalf("runner AMI ID = %q", plan.RootConfig.Runner.AMIID)
	}
	if plan.RootConfig.Runner.AMISSMParameter != "" {
		t.Fatalf("runner SSM parameter should be empty when AMI ID is explicit: %+v", plan.RootConfig.Runner)
	}
	if plan.RootConfig.Runner.InstallVersion != "" {
		t.Fatalf("runner install version should be empty for custom AMI: %+v", plan.RootConfig.Runner)
	}
}

func TestPlanAWSFallbackAL2023RunnerAMIInstallsPinnedRunner(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:                   "prod",
		Region:                "us-west-2",
		StateBucket:           "skiff-state-prod",
		RunnerAMISSMParameter: FallbackRunnerAMISSMParameter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootConfig.Runner == nil || plan.RootConfig.Runner.AMISSMParameter != FallbackRunnerAMISSMParameter {
		t.Fatalf("runner fallback SSM parameter = %+v", plan.RootConfig.Runner)
	}
	if plan.RootConfig.Runner.InstallVersion != DefaultRunnerInstallVersion {
		t.Fatalf("fallback AL2023 AMI should install pinned runner release: %+v", plan.RootConfig.Runner)
	}
}

func TestApplyAWSIdempotentWithFakeClient(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "prod",
		Region:      "us-west-2",
		StateBucket: "skiff-state-prod",
		Now:         time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeAWSBootstrapClient()

	first, err := ApplyAWS(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("first ApplyAWS() error = %v", err)
	}
	if len(first.Actions) != 8 {
		t.Fatalf("first action count = %d, want 8", len(first.Actions))
	}
	for _, action := range first.Actions {
		if action.Action != "created" && action.Action != "put" {
			t.Fatalf("first apply action = %#v, want created/put", action)
		}
	}

	second, err := ApplyAWS(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("second ApplyAWS() error = %v", err)
	}
	for _, action := range second.Actions {
		if action.Action != "unchanged" {
			t.Fatalf("second apply action = %#v, want unchanged", action)
		}
	}
}

func TestApplyAWSManagedPublicIngressMaterializesEnvironmentRoot(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "quickstart",
		Region:      "us-west-2",
		Network:     NetworkManaged,
		Ingress:     IngressPublic,
		CompanyName: "Acme Corp",
		DomainName:  "example.com",
		Now:         time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeAWSBootstrapClient()

	result, err := ApplyAWS(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("ApplyAWS() error = %v", err)
	}
	root := result.RootConfig
	if root.Network == nil || root.Network.VPCID != "vpc-skiff-acme-corp-quickstart" {
		t.Fatalf("environment root did not materialize network IDs: %+v", root.Network)
	}
	if root.Ingress == nil || root.Ingress.BaseDomain != "quickstart.example.com" || root.Ingress.Route53ZoneID != "ZEXAMPLE" {
		t.Fatalf("environment root did not materialize public ingress DNS defaults: %+v", root.Ingress)
	}
	lb := root.Ingress.LoadBalancer
	if lb == nil || lb.ARN == "" || lb.ProviderDNSName != plan.PublicLBName+".elb.amazonaws.com" || lb.DNSName != "quickstart.example.com" {
		t.Fatalf("environment root did not materialize public load balancer defaults: %+v", lb)
	}
	if lb.HTTPListenerARN == "" || lb.HTTPSListenerARN == "" || lb.CertificateARN != "arn:aws:acm:us-west-2:123456789012:certificate/quickstart.example.com" {
		t.Fatalf("environment root did not materialize HTTPS defaults: %+v", lb)
	}
	if strings.Contains(mustCanonicalString(t, root), "${") {
		t.Fatalf("direct apply root should not contain Terraform expressions: %+v", root)
	}
	if !client.seen["dns-record/quickstart.example.com"] || !client.seen["dns-record/*.quickstart.example.com"] {
		t.Fatalf("direct apply did not create base and wildcard DNS aliases: %+v", client.seen)
	}
}

func TestApplyAWSManagedInternalIngressMaterializesEnvironmentRoot(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:     "dev",
		Region:  "us-west-2",
		Network: NetworkManaged,
		Ingress: IngressInternalHTTP,
		Now:     time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyAWS(context.Background(), newFakeAWSBootstrapClient(), plan)
	if err != nil {
		t.Fatalf("ApplyAWS() error = %v", err)
	}
	root := result.RootConfig
	if root.Network == nil || root.Network.VPCID != "vpc-skiff-dev" {
		t.Fatalf("environment root did not materialize network IDs: %+v", root.Network)
	}
	if root.Ingress == nil || root.Ingress.Type != IngressInternalHTTP {
		t.Fatalf("environment root did not keep internal ingress type: %+v", root.Ingress)
	}
	lb := root.Ingress.LoadBalancer
	if lb == nil || lb.DNSName != "skiff-dev-internal.elb.amazonaws.com" || lb.HTTPListenerARN == "" || lb.HTTPSListenerARN != "" {
		t.Fatalf("environment root did not materialize internal ALB defaults: %+v", lb)
	}
	if strings.Contains(mustCanonicalString(t, root), "${") {
		t.Fatalf("direct apply root should not contain Terraform expressions: %+v", root)
	}
}

type fakeAWSBootstrapClient struct {
	seen     map[string]bool
	lastRoot EnvironmentRoot
}

func newFakeAWSBootstrapClient() *fakeAWSBootstrapClient {
	return &fakeAWSBootstrapClient{seen: make(map[string]bool)}
}

func (f *fakeAWSBootstrapClient) EnsureStateBucket(ctx context.Context, spec StateBucketSpec) (ApplyAction, error) {
	return f.ensure("s3-bucket", spec.Name), nil
}

func (f *fakeAWSBootstrapClient) EnsureKMSKey(ctx context.Context, spec KMSKeySpec) (ApplyAction, error) {
	return f.ensure("kms-key", spec.Alias), nil
}

func (f *fakeAWSBootstrapClient) EnsureIAMRole(ctx context.Context, spec IAMRoleSpec) (ApplyAction, error) {
	action := f.ensure("iam-role", spec.Name)
	action.ProviderID = "arn:aws:iam::123456789012:role/" + spec.Name
	return action, nil
}

func (f *fakeAWSBootstrapClient) PutBucketPolicy(ctx context.Context, spec BucketPolicySpec) (ApplyAction, error) {
	return f.put("s3-bucket-policy", spec.Bucket), nil
}

func (f *fakeAWSBootstrapClient) EnsureManagedNetwork(ctx context.Context, spec ManagedNetworkSpec) (*ManagedNetworkResult, error) {
	actions := []ApplyAction{
		f.ensure("vpc", spec.NamePrefix),
		f.ensure("subnet", spec.NamePrefix+"-public"),
		f.ensure("subnet", spec.NamePrefix+"-private"),
		f.ensure("internet-gateway", spec.NamePrefix),
		f.ensure("nat-gateway", spec.NamePrefix),
		f.ensure("route-table", spec.NamePrefix),
	}
	return &ManagedNetworkResult{
		Actions:          actions,
		VPCID:            "vpc-" + spec.NamePrefix,
		PublicSubnetIDs:  []string{"subnet-" + spec.NamePrefix + "-public-1", "subnet-" + spec.NamePrefix + "-public-2"},
		PrivateSubnetIDs: []string{"subnet-" + spec.NamePrefix + "-private-1", "subnet-" + spec.NamePrefix + "-private-2"},
	}, nil
}

func (f *fakeAWSBootstrapClient) ResolveHostedZone(ctx context.Context, spec HostedZoneSpec) (*HostedZoneResult, error) {
	action := f.ensure("hosted-zone", firstNonEmptyTest(spec.HostedZoneID, spec.DomainName))
	return &HostedZoneResult{Action: action, HostedZoneID: firstNonEmptyTest(spec.HostedZoneID, "ZEXAMPLE"), Name: spec.DomainName}, nil
}

func (f *fakeAWSBootstrapClient) EnsureCertificate(ctx context.Context, spec CertificateSpec) (*CertificateResult, error) {
	action := f.ensure("certificate", spec.DomainName)
	return &CertificateResult{Action: action, CertificateARN: "arn:aws:acm:us-west-2:123456789012:certificate/" + spec.DomainName}, nil
}

func (f *fakeAWSBootstrapClient) EnsureLoadBalancerSecurityGroup(ctx context.Context, spec LoadBalancerSecurityGroupSpec) (*SecurityGroupResult, error) {
	action := f.ensure("security-group", spec.Name)
	return &SecurityGroupResult{Action: action, GroupID: "sg-" + spec.Name}, nil
}

func (f *fakeAWSBootstrapClient) EnsureLoadBalancer(ctx context.Context, spec LoadBalancerSpec) (*LoadBalancerResult, error) {
	action := f.ensure("load-balancer", spec.Name)
	return &LoadBalancerResult{
		Action:       action,
		ARN:          "arn:aws:elasticloadbalancing:us-west-2:123456789012:loadbalancer/app/" + spec.Name + "/abc",
		DNSName:      spec.Name + ".elb.amazonaws.com",
		HostedZoneID: "ZALB",
	}, nil
}

func (f *fakeAWSBootstrapClient) EnsureListener(ctx context.Context, spec ListenerSpec) (*ListenerResult, error) {
	action := f.ensure("listener", spec.Name)
	return &ListenerResult{Action: action, ARN: "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff/abc/" + spec.Name}, nil
}

func (f *fakeAWSBootstrapClient) EnsureDNSAlias(ctx context.Context, spec DNSAliasSpec) (ApplyAction, error) {
	return f.ensure("dns-record", spec.Name), nil
}

func (f *fakeAWSBootstrapClient) PutEnvironmentRoot(ctx context.Context, spec EnvironmentRootSpec) (ApplyAction, error) {
	f.lastRoot = spec.Config
	return f.ensure("environment-root", spec.Key), nil
}

func (f *fakeAWSBootstrapClient) ensure(kind, name string) ApplyAction {
	key := kind + "/" + name
	if f.seen[key] {
		return ApplyAction{Kind: kind, Name: name, Action: "unchanged"}
	}
	f.seen[key] = true
	return ApplyAction{Kind: kind, Name: name, Action: "created", ProviderID: key}
}

func (f *fakeAWSBootstrapClient) put(kind, name string) ApplyAction {
	key := kind + "/" + name
	if f.seen[key] {
		return ApplyAction{Kind: kind, Name: name, Action: "unchanged"}
	}
	f.seen[key] = true
	return ApplyAction{Kind: kind, Name: name, Action: "put", ProviderID: key}
}

func mustCanonicalString(t *testing.T, value any) string {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func hasBootstrapResourceKind(resources []AWSResourcePlan, kind string) bool {
	for _, resource := range resources {
		if resource.Kind == kind {
			return true
		}
	}
	return false
}

func hasBootstrapResource(resources []AWSResourcePlan, kind, name string) bool {
	for _, resource := range resources {
		if resource.Kind == kind && resource.Name == name {
			return true
		}
	}
	return false
}
