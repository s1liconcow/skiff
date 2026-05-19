package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"gopkg.in/yaml.v3"
)

func TestLowerServicePublicHTTPGoldenPrimitives(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:      "us-west-2",
		StateBucket: "s3://skiff-state-prod",
		ReleaseID:   "rel_01JABC",
	})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}

	if lowered.Provider != "aws" || lowered.Service != "payments-api" || lowered.Env != "prod" {
		t.Fatalf("unexpected identity: %+v", lowered)
	}
	assertLen(t, "iam roles", lowered.IAMRoles, 1)
	assertLen(t, "instance profiles", lowered.InstanceProfiles, 1)
	assertLen(t, "security groups", lowered.SecurityGroups, 1)
	assertLen(t, "log groups", lowered.LogGroups, 1)
	assertLen(t, "metric configs", lowered.MetricConfigs, 1)
	assertLen(t, "target groups", lowered.TargetGroups, 1)
	assertLen(t, "listener rules", lowered.ListenerRules, 1)
	assertLen(t, "launch templates", lowered.LaunchTemplates, 1)
	assertLen(t, "auto scaling groups", lowered.AutoScalingGroups, 1)

	if lowered.TargetGroups[0].TargetType != "instance" || lowered.TargetGroups[0].Port != 8080 {
		t.Fatalf("target group should point at workload VM instances: %+v", lowered.TargetGroups[0])
	}
	if lowered.ListenerRules[0].Visibility != "public" || lowered.ListenerRules[0].Protocol != "HTTPS" || lowered.ListenerRules[0].CertificateRef == "" {
		t.Fatalf("public listener did not lower to HTTPS with certificate: %+v", lowered.ListenerRules[0])
	}
	sg := lowered.SecurityGroups[0]
	if len(sg.Ingress) != 1 || sg.Ingress[0].SourceSecurityGroupRef != "load-balancer" || sg.Ingress[0].CIDR != "" {
		t.Fatalf("instance ingress must come from the load balancer security group only: %+v", sg.Ingress)
	}
	if len(sg.Egress) != 1 || sg.Egress[0].CIDR != "0.0.0.0/0" {
		t.Fatalf("unexpected egress: %+v", sg.Egress)
	}
	userData := lowered.LaunchTemplates[0].UserData
	for _, want := range []string{
		`"skiff":`,
		`"state_bucket":"s3://skiff-state-prod"`,
		`"service":"payments-api"`,
		`"control_key":"services/payments-api/control.json"`,
		`"region":"us-west-2"`,
		`"release_id":"rel_01JABC"`,
		`"logs":`,
		`"group":"/skiff/prod/payments-api"`,
		`"stream_template":"{service}/{release}/{instance}"`,
		`"archive_prefix":"services/payments-api/log-archives/prod/"`,
		"/etc/skiff/runner.json",
		"skiff-runner",
	} {
		if !strings.Contains(userData, want) {
			t.Fatalf("launch template user-data missing %q:\n%s", want, userData)
		}
	}
	assertPolicyIsLeastPrivilege(t, lowered.IAMRoles[0])
}

func TestLowerServiceMinimalHasNoListenerRule(t *testing.T) {
	graph := compileSpec(t, minimalServiceSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{Region: "us-west-2", StateBucket: "s3://skiff-state-dev"})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}
	if len(lowered.ListenerRules) != 0 {
		t.Fatalf("private service should not create listener rules: %+v", lowered.ListenerRules)
	}
	if len(lowered.TargetGroups) != 1 || len(lowered.LaunchTemplates) != 1 || len(lowered.AutoScalingGroups) != 1 {
		t.Fatalf("minimal service missing core deployable resources: %+v", lowered)
	}
}

func TestLowerServiceInternalHTTP(t *testing.T) {
	graph := compileSpec(t, internalHTTPSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:                       "us-east-1",
		StateBucket:                  "s3://skiff-state-prod",
		LoadBalancerSecurityGroupRef: "sg-internal-alb",
	})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}
	assertLen(t, "listener rules", lowered.ListenerRules, 1)
	listener := lowered.ListenerRules[0]
	if listener.Visibility != "internal" || listener.Protocol != "HTTP" || listener.Port != 80 || listener.CertificateRef != "" {
		t.Fatalf("unexpected internal listener: %+v", listener)
	}
	if got := lowered.SecurityGroups[0].Ingress[0].SourceSecurityGroupRef; got != "sg-internal-alb" {
		t.Fatalf("ingress source SG = %q, want sg-internal-alb", got)
	}
}

func TestLowerServiceCarriesAWSLiveShapeInputs(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:                       "us-west-2",
		StateBucket:                  "s3://skiff-state-prod",
		VPCID:                        "vpc-123",
		SubnetIDs:                    []string{"subnet-a", " subnet-b "},
		AMIID:                        "ami-123",
		ALBListenerARN:               "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff/abc/def",
		LoadBalancerSecurityGroupRef: "sg-alb",
	})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}
	if lowered.SecurityGroups[0].VPCID != "vpc-123" {
		t.Fatalf("security group VPC ID = %q", lowered.SecurityGroups[0].VPCID)
	}
	if lowered.TargetGroups[0].VPCID != "vpc-123" {
		t.Fatalf("target group VPC ID = %q", lowered.TargetGroups[0].VPCID)
	}
	if lowered.LaunchTemplates[0].AMIID != "ami-123" {
		t.Fatalf("launch template AMI ID = %q", lowered.LaunchTemplates[0].AMIID)
	}
	if got := lowered.AutoScalingGroups[0].SubnetIDs; len(got) != 2 || got[0] != "subnet-a" || got[1] != "subnet-b" {
		t.Fatalf("ASG subnet IDs = %+v", got)
	}
	if lowered.ListenerRules[0].ListenerARN == "" {
		t.Fatalf("listener ARN was not carried: %+v", lowered.ListenerRules[0])
	}
	if got := lowered.SecurityGroups[0].Ingress[0].SourceSecurityGroupRef; got != "sg-alb" {
		t.Fatalf("load balancer SG ref = %q, want sg-alb", got)
	}
	if err := aws.ValidateLiveApplyInputs(lowered); err != nil {
		t.Fatalf("live apply inputs should validate: %v", err)
	}
}

func TestProviderPlanFillsLiveInputsFromEnvironmentRoot(t *testing.T) {
	store := memory.New()
	key, err := paths.EnvironmentRoot("dev")
	if err != nil {
		t.Fatal(err)
	}
	root := schema.EnvironmentRoot{
		SchemaVersion: schema.EnvironmentRootSchemaVersion,
		Env:           "dev",
		Provider:      aws.Name,
		Region:        "us-west-2",
		StateBucket:   "memory://aws-env-root",
		KMSAlias:      "alias/skiff-dev-state",
		Network: &schema.EnvironmentNetwork{
			Mode:             "managed",
			VPCID:            "vpc-env",
			PrivateSubnetIDs: []string{"subnet-private-a", "subnet-private-b"},
		},
		Runner: &schema.EnvironmentRunner{
			AMISSMParameter:  "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64",
			InstallVersion:   "v0.1.0",
			InstallScriptURL: "https://raw.githubusercontent.com/s1liconcow/skiff/v0.1.0/scripts/install.sh",
		},
		CreatedAt: "2026-05-16T21:00:00Z",
		UpdatedAt: "2026-05-16T21:00:00Z",
	}
	body, err := canonical.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatal(err)
	}
	p, err := aws.New(aws.Config{
		Region:      "us-west-2",
		StateBucket: "memory://aws-env-root",
		LiveApply:   true,
	}, aws.WithStateStore(store))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), compileSpec(t, minimalServiceSpec))
	if err != nil {
		t.Fatalf("plan with environment root defaults: %v", err)
	}
	targetGroup := desiredResource[aws.TargetGroupAWS](t, plan, aws.ResourceKindTargetGroup)
	if targetGroup.VPCID != "vpc-env" {
		t.Fatalf("target group VPC ID = %q", targetGroup.VPCID)
	}
	template := desiredResource[aws.LaunchTemplate](t, plan, aws.ResourceKindLaunchTemplate)
	wantAMI := "resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
	if template.AMIID != wantAMI {
		t.Fatalf("launch template AMI ID = %q", template.AMIID)
	}
	for _, want := range []string{
		"dnf install -y bash curl tar gzip",
		"SKIFF_INSTALL_VERSION=",
		"v0.1.0",
		"https://raw.githubusercontent.com/s1liconcow/skiff/v0.1.0/scripts/install.sh",
		"/etc/skiff/runner.json",
	} {
		if !strings.Contains(template.UserData, want) {
			t.Fatalf("launch template user-data missing %q:\n%s", want, template.UserData)
		}
	}
	var cloudInit any
	if err := yaml.Unmarshal([]byte(template.UserData), &cloudInit); err != nil {
		t.Fatalf("launch template user-data is not valid cloud-init YAML: %v\n%s", err, template.UserData)
	}
	asg := desiredResource[aws.AutoScalingGroup](t, plan, aws.ResourceKindAutoScalingGroup)
	if strings.Join(asg.SubnetIDs, ",") != "subnet-private-a,subnet-private-b" {
		t.Fatalf("ASG subnet IDs = %#v", asg.SubnetIDs)
	}
}

func TestProviderPlanFillsPublicALBFromEnvironmentRoot(t *testing.T) {
	store := memory.New()
	key, err := paths.EnvironmentRoot("prod")
	if err != nil {
		t.Fatal(err)
	}
	root := schema.EnvironmentRoot{
		SchemaVersion: schema.EnvironmentRootSchemaVersion,
		Env:           "prod",
		Provider:      aws.Name,
		Region:        "us-west-2",
		StateBucket:   "memory://aws-env-root",
		KMSAlias:      "alias/skiff-prod-state",
		Network: &schema.EnvironmentNetwork{
			Mode:             "managed",
			VPCID:            "vpc-public",
			PrivateSubnetIDs: []string{"subnet-private-a", "subnet-private-b"},
			PublicSubnetIDs:  []string{"subnet-public-a", "subnet-public-b"},
		},
		Ingress: &schema.EnvironmentIngress{
			Type:                "public",
			BaseDomain:          "quickstart.example.com",
			DefaultHostTemplate: "{service}.quickstart.example.com",
			LoadBalancer: &schema.EnvironmentLoadBalancerDefaults{
				ARN:              "arn:aws:elasticloadbalancing:us-west-2:123456789012:loadbalancer/app/skiff-public/abc",
				DNSName:          "quickstart.example.com",
				ProviderDNSName:  "skiff-public.us-west-2.elb.amazonaws.com",
				SecurityGroupID:  "sg-public-alb",
				HTTPListenerARN:  "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff-public/abc/http",
				HTTPSListenerARN: "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff-public/abc/https",
				CertificateARN:   "arn:aws:acm:us-west-2:123456789012:certificate/env-shared",
			},
		},
		Runner: &schema.EnvironmentRunner{
			AMISSMParameter: "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64",
		},
		CreatedAt: "2026-05-16T21:00:00Z",
		UpdatedAt: "2026-05-16T21:00:00Z",
	}
	body, err := canonical.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatal(err)
	}
	p, err := aws.New(aws.Config{
		Region:      "us-west-2",
		StateBucket: "memory://aws-env-root",
		LiveApply:   true,
	}, aws.WithStateStore(store))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), compileSpec(t, publicHTTPSpec))
	if err != nil {
		t.Fatalf("plan with public environment root defaults: %v", err)
	}
	listener := desiredResource[aws.ListenerRule](t, plan, aws.ResourceKindListenerRule)
	if listener.ListenerARN != root.Ingress.LoadBalancer.HTTPSListenerARN {
		t.Fatalf("listener ARN = %q, want %q", listener.ListenerARN, root.Ingress.LoadBalancer.HTTPSListenerARN)
	}
	group := desiredResource[aws.SecurityGroupAWS](t, plan, aws.ResourceKindSecurityGroup)
	if len(group.Ingress) != 1 || group.Ingress[0].SourceSecurityGroupRef != "sg-public-alb" {
		t.Fatalf("security group ingress did not use public ALB SG: %+v", group.Ingress)
	}
}

func TestProviderPlanDefaultsPublicHostAndCertificateFromEnvironmentRoot(t *testing.T) {
	store := memory.New()
	key, err := paths.EnvironmentRoot("quickstart")
	if err != nil {
		t.Fatal(err)
	}
	root := schema.EnvironmentRoot{
		SchemaVersion: schema.EnvironmentRootSchemaVersion,
		Env:           "quickstart",
		Provider:      aws.Name,
		Region:        "us-west-2",
		StateBucket:   "memory://aws-env-root",
		KMSAlias:      "alias/skiff-quickstart-state",
		Network: &schema.EnvironmentNetwork{
			Mode:             "managed",
			VPCID:            "vpc-public",
			PrivateSubnetIDs: []string{"subnet-private-a", "subnet-private-b"},
		},
		Ingress: &schema.EnvironmentIngress{
			Type:                "public",
			BaseDomain:          "quickstart.example.com",
			DefaultHostTemplate: "{service}.quickstart.example.com",
			LoadBalancer: &schema.EnvironmentLoadBalancerDefaults{
				SecurityGroupID:  "sg-public-alb",
				HTTPSListenerARN: "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff-public/abc/https",
				CertificateARN:   "arn:aws:acm:us-west-2:123456789012:certificate/env-shared",
			},
		},
		Runner:    &schema.EnvironmentRunner{AMIID: "ami-123"},
		CreatedAt: "2026-05-16T21:00:00Z",
		UpdatedAt: "2026-05-16T21:00:00Z",
	}
	body, err := canonical.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatal(err)
	}
	p, err := aws.New(aws.Config{
		Region:      "us-west-2",
		StateBucket: "memory://aws-env-root",
		LiveApply:   true,
	}, aws.WithStateStore(store))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), compileSpec(t, publicHTTPWithoutHostSpec))
	if err != nil {
		t.Fatalf("plan with public environment root defaults: %v", err)
	}
	listener := desiredResource[aws.ListenerRule](t, plan, aws.ResourceKindListenerRule)
	if listener.Host != "orders.quickstart.example.com" {
		t.Fatalf("listener host = %q", listener.Host)
	}
	if listener.CertificateRef != root.Ingress.LoadBalancer.CertificateARN {
		t.Fatalf("listener certificate = %q, want %q", listener.CertificateRef, root.Ingress.LoadBalancer.CertificateARN)
	}
	if listener.ListenerARN != root.Ingress.LoadBalancer.HTTPSListenerARN {
		t.Fatalf("listener ARN = %q, want %q", listener.ListenerARN, root.Ingress.LoadBalancer.HTTPSListenerARN)
	}
}

func TestValidateLiveApplyInputsReportsMissingFields(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:      "us-west-2",
		StateBucket: "s3://skiff-state-prod",
	})
	if err != nil {
		t.Fatalf("lower service: %v", err)
	}
	err = aws.ValidateLiveApplyInputs(lowered)
	var validation aws.LiveApplyValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ValidateLiveApplyInputs() error = %T %[1]v, want LiveApplyValidationError", err)
	}
	want := map[string]bool{
		"aws_vpc_id":                           false,
		"aws_subnet_ids":                       false,
		"aws_ami_id":                           false,
		"aws_alb_listener_arn":                 false,
		"aws_load_balancer_security_group_ref": false,
	}
	for _, missing := range validation.Missing {
		if _, ok := want[missing.Field]; ok {
			want[missing.Field] = true
		}
		if missing.EnvVar == "" || missing.Kind == "" || missing.LogicalID == "" || missing.Reason == "" {
			t.Fatalf("missing input should be actionable: %+v", missing)
		}
	}
	for field, found := range want {
		if !found {
			t.Fatalf("missing field %s not reported: %+v", field, validation.Missing)
		}
	}
}

func TestProviderPlanPreflightsAWSLiveApplyInputs(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	p, err := aws.New(aws.Config{
		Region:      "us-west-2",
		StateBucket: "s3://skiff-state-prod",
		LiveApply:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Plan(context.Background(), graph)
	var validation aws.LiveApplyValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("plan error = %T %[1]v, want LiveApplyValidationError", err)
	}
}

func TestProviderPlanUsesConcreteAWSLowering(t *testing.T) {
	graph := compileSpec(t, publicHTTPSpec)
	p, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantKinds := map[string]bool{
		aws.ResourceKindIAMRole:            false,
		aws.ResourceKindIAMInstanceProfile: false,
		aws.ResourceKindSecurityGroup:      false,
		aws.ResourceKindLogGroup:           false,
		aws.ResourceKindMetricConfig:       false,
		aws.ResourceKindTargetGroup:        false,
		aws.ResourceKindListenerRule:       false,
		aws.ResourceKindLaunchTemplate:     false,
		aws.ResourceKindAutoScalingGroup:   false,
	}
	for _, resource := range plan.Resources {
		if _, ok := wantKinds[resource.Kind]; ok {
			wantKinds[resource.Kind] = true
		}
		if resource.Tags[ir.TagService] != "payments-api" || resource.Tags[ir.TagEnv] != "prod" {
			t.Fatalf("planned resource missing Skiff tags: %+v", resource)
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Fatalf("plan missing kind %s: %+v", kind, plan.Resources)
		}
	}
}

func TestLowerStatefulGroupAWSResources(t *testing.T) {
	graph := compileSpec(t, statefulGroupSpec)
	lowered, err := aws.LowerService(graph, aws.LowerOptions{
		Region:      "us-west-2",
		StateBucket: "s3://skiff-state-prod",
		KMSKey:      "alias/skiff-stateful",
		VPCID:       "vpc-123",
		SubnetIDs:   []string{"subnet-a", "subnet-b"},
		AMIID:       "ami-123",
	})
	if err != nil {
		t.Fatalf("lower stateful group: %v", err)
	}
	assertLen(t, "iam roles", lowered.IAMRoles, 1)
	assertLen(t, "instance profiles", lowered.InstanceProfiles, 1)
	assertLen(t, "security groups", lowered.SecurityGroups, 1)
	assertLen(t, "log groups", lowered.LogGroups, 1)
	assertLen(t, "metric configs", lowered.MetricConfigs, 1)
	assertLen(t, "target groups", lowered.TargetGroups, 1)
	assertLen(t, "launch templates", lowered.LaunchTemplates, 1)
	assertLen(t, "stateful members", lowered.StatefulMembers, 3)
	assertLen(t, "ebs volumes", lowered.EBSVolumes, 3)
	assertLen(t, "volume attachments", lowered.VolumeAttachments, 3)
	assertLen(t, "route53 records", lowered.Route53Records, 3)
	assertLen(t, "snapshot policies", lowered.SnapshotPolicies, 1)
	assertLen(t, "fencing policies", lowered.FencingPolicies, 3)
	if lowered.EBSVolumes[0].DeleteOnDestroy || !lowered.EBSVolumes[0].Encrypted || lowered.EBSVolumes[0].KMSKeyID != "alias/skiff-stateful" {
		t.Fatalf("stateful EBS volume missing safe retention/encryption defaults: %+v", lowered.EBSVolumes[0])
	}
	if lowered.Route53Records[0].DNSName != "orders-stream-0.state.prod.internal.example.com" || lowered.Route53Records[0].HostedZoneRef == "" {
		t.Fatalf("stateful DNS record missing stable identity: %+v", lowered.Route53Records[0])
	}
	if lowered.FencingPolicies[0].VolumeRef == "" || !lowered.FencingPolicies[0].RequiresInstanceTermination || !lowered.FencingPolicies[0].RequiresVolumeDetach {
		t.Fatalf("fencing policy missing provider fence requirements: %+v", lowered.FencingPolicies[0])
	}
	for _, resource := range lowered.PlannedResources() {
		if limit := aws.ResourceNameLimit(resource.Kind); limit > 0 && len(resource.Name) > limit {
			t.Fatalf("%s name exceeds limit %d: %q", resource.Kind, limit, resource.Name)
		}
	}
	if err := aws.ValidateLiveApplyInputs(lowered); err != nil {
		t.Fatalf("stateful live apply inputs should validate with explicit inputs: %v", err)
	}
}

func TestProviderPlanStatefulGroupIncludesAWSPrimitives(t *testing.T) {
	graph := compileSpec(t, statefulGroupSpec)
	p, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), graph)
	if err != nil {
		t.Fatalf("plan stateful group: %v", err)
	}
	wantKinds := map[string]bool{
		aws.ResourceKindIAMRole:            false,
		aws.ResourceKindIAMInstanceProfile: false,
		aws.ResourceKindSecurityGroup:      false,
		aws.ResourceKindLogGroup:           false,
		aws.ResourceKindMetricConfig:       false,
		aws.ResourceKindTargetGroup:        false,
		aws.ResourceKindLaunchTemplate:     false,
		aws.ResourceKindEC2Instance:        false,
		aws.ResourceKindEBSVolume:          false,
		aws.ResourceKindEBSAttachment:      false,
		aws.ResourceKindRoute53Record:      false,
		aws.ResourceKindSnapshotPolicy:     false,
		aws.ResourceKindFencingPolicy:      false,
	}
	for _, resource := range plan.Resources {
		if _, ok := wantKinds[resource.Kind]; ok {
			wantKinds[resource.Kind] = true
		}
		if resource.Action != provider.ActionCreate || resource.Fingerprint == "" || len(resource.Desired) == 0 {
			t.Fatalf("stateful planned resource missing deterministic desired body: %+v", resource)
		}
		if resource.Tags[ir.TagStatefulGroup] != "orders-stream" {
			t.Fatalf("stateful planned resource missing group tag: %+v", resource)
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Fatalf("stateful plan missing kind %s: %+v", kind, plan.Resources)
		}
	}
}

func TestStatefulLiveApplyValidationReportsMissingInputs(t *testing.T) {
	graph := compileSpec(t, statefulGroupSpecNoDNSZone)
	p, err := aws.New(aws.Config{
		Region:      "us-west-2",
		StateBucket: "s3://skiff-state-prod",
		LiveApply:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Plan(context.Background(), graph)
	var validation aws.LiveApplyValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("plan error = %T %[1]v, want LiveApplyValidationError", err)
	}
	want := map[string]bool{
		"aws_vpc_id":                   false,
		"aws_subnet_ids":               false,
		"aws_ami_id":                   false,
		"kms_key":                      false,
		"stateful.identity.dnsZoneRef": false,
	}
	for _, missing := range validation.Missing {
		if _, ok := want[missing.Field]; ok {
			want[missing.Field] = true
		}
		if missing.Kind == "" || missing.LogicalID == "" || missing.Reason == "" {
			t.Fatalf("missing stateful input should be actionable: %+v", missing)
		}
	}
	for field, found := range want {
		if !found {
			t.Fatalf("missing field %s not reported: %+v", field, validation.Missing)
		}
	}
}

func assertPolicyIsLeastPrivilege(t *testing.T, role aws.IAMRoleResource) {
	t.Helper()
	for _, stmt := range role.InlinePolicy.Statement {
		for _, resource := range stmt.Resource {
			if resource == "*" {
				t.Fatalf("policy statement %s uses wildcard resource: %+v", stmt.Sid, stmt)
			}
		}
	}
	var foundState, foundSecret, foundParameter bool
	for _, stmt := range role.InlinePolicy.Statement {
		if stmt.Sid == "ReadServiceState" {
			foundState = true
			if len(stmt.Resource) != 4 ||
				stmt.Resource[0] != "arn:aws:s3:::skiff-state-prod/services/payments-api/control.json" ||
				stmt.Resource[1] != "arn:aws:s3:::skiff-state-prod/services/payments-api/releases/*" ||
				stmt.Resource[2] != "arn:aws:s3:::skiff-state-prod/stateful/payments-api/control.json" ||
				stmt.Resource[3] != "arn:aws:s3:::skiff-state-prod/stateful/payments-api/members/*/control.json" {
				t.Fatalf("state policy is not scoped to service state: %+v", stmt)
			}
		}
		if stmt.Sid == "ReadReferencedSecretsManagerSecrets" {
			foundSecret = true
			if len(stmt.Action) != 1 || stmt.Action[0] != "secretsmanager:GetSecretValue" ||
				len(stmt.Resource) != 1 ||
				stmt.Resource[0] != "arn:aws:secretsmanager:us-west-2:123456789012:secret:payments/db-password" {
				t.Fatalf("secret policy should include exact Secrets Manager ARN: %+v", stmt)
			}
		}
		if stmt.Sid == "ReadReferencedSSMParameters" {
			foundParameter = true
			if len(stmt.Action) != 1 || stmt.Action[0] != "ssm:GetParameter" ||
				len(stmt.Resource) != 1 ||
				stmt.Resource[0] != "arn:aws:ssm:us-west-2:123456789012:parameter/payments/api-token" {
				t.Fatalf("parameter policy should include exact SSM parameter ARN: %+v", stmt)
			}
		}
	}
	if !foundState || !foundSecret || !foundParameter {
		t.Fatalf("missing least-privilege statements in role policy: %+v", role.InlinePolicy.Statement)
	}
}

func assertLen[T any](t *testing.T, name string, values []T, want int) {
	t.Helper()
	if len(values) != want {
		t.Fatalf("%s count = %d, want %d: %+v", name, len(values), want, values)
	}
}

func desiredResource[T any](t *testing.T, plan *provider.Plan, kind string) T {
	t.Helper()
	for _, resource := range plan.Resources {
		if resource.Kind != kind {
			continue
		}
		var out T
		if err := canonical.UnmarshalStrict(resource.Desired, &out); err != nil {
			t.Fatalf("unmarshal desired %s: %v", kind, err)
		}
		return out
	}
	t.Fatalf("resource kind %s not found in plan: %+v", kind, plan.Resources)
	var zero T
	return zero
}

func compileSpec(t *testing.T, body string) *ir.Graph {
	t.Helper()
	doc, result, err := spec.Parse([]byte(body), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if !result.OK {
		t.Fatalf("spec invalid: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile spec: %v", err)
	}
	return graph
}

const publicHTTPSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:abc123
runtime:
  port: 8080
  health:
    path: /healthz
machine:
  size: small
scale:
  min: 3
  max: 20
network:
  ingress:
    type: public-http
    host: payments.example.com
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
secrets:
  - name: db-password
    ref: aws-secretsmanager://arn:aws:secretsmanager:us-west-2:123456789012:secret:payments/db-password
  - name: api-token
    ref: aws-ssm://arn:aws:ssm:us-west-2:123456789012:parameter/payments/api-token
`

const publicHTTPWithoutHostSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: orders
  env: quickstart
artifact:
  type: oci
  ref: registry.example.com/orders@sha256:def456
runtime:
  port: 8080
  health:
    path: /healthz
network:
  ingress:
    type: public-http
`

const statefulGroupSpec = `
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: orders-stream
  env: prod
stateful:
  replicas: 3
  members:
    - ordinal: 0
      zone: us-west-2a
      dnsName: orders-stream-0.state.prod.internal.example.com
    - ordinal: 1
      zone: us-west-2b
      dnsName: orders-stream-1.state.prod.internal.example.com
    - ordinal: 2
      zone: us-west-2c
      dnsName: orders-stream-2.state.prod.internal.example.com
  volume:
    size: 250Gi
    type: gp3
    mountPath: /var/lib/nats
    encrypted: true
  identity:
    dnsZoneRef: route53://Z0123456789EXAMPLE/prod.internal.example.com
    hostnamePrefix: orders-stream
  recipe:
    name: nats-jetstream
    config:
      artifact:
        type: oci
        ref: docker.io/library/nats:2.14.0@sha256:ddb480f4b97d90f183123e96bbc7c96ab2a126883f7a380531cc208fc8ba9ca7
      runtime:
        command:
          - /usr/local/bin/nats-server
          - --config
          - /etc/nats/server.conf
        ports:
          client: 4222
          cluster: 6222
          monitoring: 8222
        health:
          path: /healthz
          port: 8222
        metrics:
          path: /metrics
          port: 8222
      snapshots:
        enabled: true
        interval: 15m
        retention: 7d
  update:
    strategy: ordered
`

const statefulGroupSpecNoDNSZone = `
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: ledger
  env: prod
stateful:
  replicas: 1
  volume:
    size: 10Gi
    encrypted: true
  recipe:
    name: sqlite
    config:
      runtime:
        health:
          path: /healthz
          port: 9000
  update:
    strategy: ordered
`

const minimalServiceSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: worker-api
  env: dev
artifact:
  type: binary
  ref: file:///tmp/worker-api
runtime:
  port: 9000
  health:
    path: /healthz
`

const internalHTTPSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: orders-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/orders-api@sha256:def456
runtime:
  port: 8081
  health:
    path: /healthz
network:
  ingress:
    type: internal-http
    host: orders.internal.example.com
`
