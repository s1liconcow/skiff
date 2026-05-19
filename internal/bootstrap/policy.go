package bootstrap

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	securitypolicy "github.com/s1liconcow/skiff/internal/security/policy"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

type PolicyDocument = securitypolicy.Document
type PolicyStatement = securitypolicy.Statement

func StateBucketPolicy(bucket string) PolicyDocument {
	return securitypolicy.StateBucketPolicy(bucket)
}

func DeployerPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.DeployerPolicy(bucket, kmsAlias)
}

func RunnerPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.RunnerPolicy(bucket, kmsAlias)
}

func SkiffdPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.SkiffdPolicy(bucket, kmsAlias)
}

func BreakGlassPolicy(bucket, kmsAlias string) PolicyDocument {
	return securitypolicy.BreakGlassPolicy(bucket, kmsAlias)
}

func PolicyJSON(policy PolicyDocument) (string, error) {
	body, err := canonical.Marshal(policy)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func TerraformAWS(plan *AWSPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("aws bootstrap plan is required")
	}
	bucketPolicy, err := PolicyJSON(plan.BucketPolicy)
	if err != nil {
		return "", err
	}
	rootConfig, err := canonical.Marshal(plan.RootConfig)
	if err != nil {
		return "", err
	}
	var policyVars []terraformPolicy
	for _, name := range sortedPolicyNames(plan.IAMPolicies) {
		body, err := PolicyJSON(plan.IAMPolicies[name])
		if err != nil {
			return "", err
		}
		policyVars = append(policyVars, terraformPolicy{Name: name, JSON: body})
	}
	data := struct {
		Plan                           *AWSPlan
		BucketPolicy                   string
		RootConfigJSON                 string
		Policies                       []terraformPolicy
		ManagedNetwork                 bool
		InternalIngress                bool
		PublicIngress                  bool
		PublicDNS                      bool
		PublicHTTPS                    bool
		PublicManagedCertificate       bool
		PublicCertificateARNExpression string
		TerraformManagedReleaseSigner  bool
		ReleaseSigningKMSAlias         string
	}{
		Plan:                     plan,
		BucketPolicy:             bucketPolicy,
		RootConfigJSON:           string(rootConfig),
		Policies:                 policyVars,
		ManagedNetwork:           plan.RootConfig.Network != nil && plan.RootConfig.Network.Mode == NetworkManaged,
		InternalIngress:          plan.RootConfig.Ingress != nil && plan.RootConfig.Ingress.Type == IngressInternalHTTP,
		PublicIngress:            plan.RootConfig.Ingress != nil && plan.RootConfig.Ingress.Type == IngressPublic,
		PublicDNS:                plan.PublicBaseDomain != "",
		PublicHTTPS:              plan.PublicBaseDomain != "" || plan.CertificateARN != "",
		PublicManagedCertificate: plan.PublicBaseDomain != "" && plan.CertificateARN == "",
	}
	data.ReleaseSigningKMSAlias = releaseSigningKMSAlias(plan.ReleaseSigningKeyRef)
	data.TerraformManagedReleaseSigner = data.ReleaseSigningKMSAlias != "" && plan.RootConfig.ReleaseTrust == nil
	switch {
	case data.PublicManagedCertificate:
		data.PublicCertificateARNExpression = "aws_acm_certificate_validation.skiff_public.certificate_arn"
	case plan.CertificateARN != "":
		data.PublicCertificateARNExpression = strconv.Quote(plan.CertificateARN)
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.New("terraform").Parse(terraformTemplate))
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()) + "\n", nil
}

func AWSTeardownScript(plan *AWSPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("aws bootstrap plan is required")
	}
	data := struct {
		Plan           *AWSPlan
		ManagedNetwork bool
	}{
		Plan:           plan,
		ManagedNetwork: plan.RootConfig.Network != nil && plan.RootConfig.Network.Mode == NetworkManaged,
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.New("aws-teardown").Parse(awsTeardownTemplate))
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()) + "\n", nil
}

type terraformPolicy struct {
	Name string
	JSON string
}

//go:embed templates/aws-bootstrap.tf.tmpl
var terraformTemplate string

//go:embed templates/aws-teardown.sh.tmpl
var awsTeardownTemplate string
