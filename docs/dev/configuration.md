# Configuration

Skiff loads runtime configuration from four sources, with later sources taking
precedence:

1. compiled defaults
2. config file or selected config context
3. environment variables
4. command-line flags

Use `skiff config show` to inspect the effective values and their sources:

```bash
skiff config show --mode direct --config ./skiff.yaml --format json
```

Supported file formats are strict flat JSON/YAML and kubeconfig-style
`.skiffconfig` files. Unknown fields are rejected so operators do not
accidentally believe unsupported configuration is active.

Flat config:

```yaml
env: prod
provider: aws
region: us-west-2
stateBucket: s3://skiff-state-prod
kmsKey: alias/skiff-prod
mode: direct
authMode: none
logLevel: info
apiURL: https://skiff.example.com
```

Context config:

```yaml
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: prod
contexts:
  - name: prod
    context:
      mode: direct
      env: prod
      environmentClass: production
      provider: aws
      region: us-west-2
      state: s3://skiff-state-prod
      writeRoleARN: arn:aws:iam::123456789012:role/skiff-prod-deployer
      releasePolicy:
        requireSignedReleases: true
        allowUnsignedCode: false
  - name: local
    context:
      mode: direct
      env: local
      environmentClass: development
      provider: fake
      region: local
      state: file:///tmp/skiff-state
      releasePolicy:
        requireSignedReleases: false
        allowUnsignedCode: true
```

Context commands:

```bash
skiff config get-contexts --config .skiffconfig
skiff config current-context --config .skiffconfig
skiff config use-context local --config .skiffconfig
```

Selection works like kubeconfig: `SKIFF_CONFIG` chooses the config file,
`SKIFF_CONTEXT` chooses a context without modifying the file, and `--context`
overrides both for one command. For single-variable environments, `SKIFF_CONFIG`
may include a context fragment such as `.skiffconfig#prod`; `SKIFF_CONTEXT` and
`--context` still override that fragment. If no config path is provided and
`.skiffconfig` exists in the current working directory, Skiff uses it.

Environment variable equivalents:

```text
SKIFF_CONFIG
SKIFF_CONTEXT
SKIFF_ENV
SKIFF_ENVIRONMENT_CLASS
SKIFF_PROVIDER
SKIFF_REGION
SKIFF_STATE_BUCKET
SKIFF_KMS_KEY
SKIFF_AUTH_MODE
SKIFF_LOG_LEVEL
SKIFF_MODE
SKIFF_API_URL
SKIFF_SERVICE
SKIFF_CONTROL_KEY
SKIFF_REQUIRE_SIGNED_RELEASES
SKIFF_ALLOW_UNSIGNED_CODE
SKIFF_WRITE_ROLE_ARN
SKIFF_AWS_LIVE_APPLY
SKIFF_AWS_VPC_ID
SKIFF_AWS_SUBNET_IDS
SKIFF_AWS_AMI_ID
SKIFF_AWS_ALB_LISTENER_ARN
SKIFF_AWS_LOAD_BALANCER_SECURITY_GROUP_REF
```

Mode-specific required fields:

| Mode | Required fields |
| --- | --- |
| `api` | `apiURL` |
| `direct` | `env`, `provider`, `region`, `stateBucket` |
| `skiffd` | `env`, `provider`, `region`, `stateBucket` |
| `runner` | `env`, `provider`, `region`, `stateBucket`, `service`, `controlKey` |

AWS live-apply inputs are provider configuration, not service-spec fields. Set
`awsLiveApply: true` only when requesting SDK-backed live apply. Without an
environment root object, `awsVpcId`, `awsSubnetIds`, and `awsAmiId` are required
for live target groups, Auto Scaling Groups, and launch templates. A managed
`skiff bootstrap aws` environment root can supply those defaults from object
state, including a runner AMI SSM parameter. The default official x86_64
parameter is `/skiff/runner/ami/al2023/x86_64/stable`; the AWS public AL2023
fallback remains available through `--runner-ami-ssm-parameter` when an
official AMI has not been published in a target region. When bootstrapped with
`--ingress public` or `--ingress internal-http`, the environment root can also
supply the ALB listener ARN and load-balancer security group. `awsAlbListenerArn`
is required only when the service has an ingress listener rule and no
bootstrapped ALB default exists. `awsLoadBalancerSecurityGroupRef` is required
when service VM ingress uses the `load-balancer` source and no bootstrapped ALB
security group default exists. Public bootstrap can also derive DNS and TLS:
`--domain-name example.com` creates the environment base domain
`<env>.example.com`, a wildcard service host alias for `*.<env>.example.com`,
an ACM certificate validated through Route53, and an HTTPS listener;
`--certificate-arn` reuses an existing ACM certificate. The environment root
stores `base_domain`, `default_host_template`, the shared listener ARN, the
certificate ARN, and the raw ALB `provider_dns_name`.

Bootstrap-created human contexts also include release signing metadata:

```yaml
signing:
  release:
    keyID: skiff-prod-release-abc123def456
    keyRef: aws-kms://alias/skiff-prod-release-signing?region=us-west-2
```

AWS bootstrap defaults to AWS KMS for release signing. The key reference points
to KMS-held private signing material; object state stores only public release
trust metadata in the environment root so runners can verify release manifests
without access to the signing key. Use `skiff bootstrap aws --signing-backend
keychain` for a local OS-keychain fallback.

Environment class is explicit policy, not an environment-name convention.
Bootstrap defaults to `development`; production-like controls require
`--class production` or `--class staging`. `production` and `staging` default to
signed releases and no unsigned code. `development` and `sandbox` default to
allowing unsigned local/dev code and do not require release signatures.
Bootstrap writes the resolved `releasePolicy` into both the environment root
and the local context so later commands enforce the saved policy instead of
guessing from names like `prod`. To catch accidental bootstrap defaults, an
environment named `prod` or `production` requires `--class production` or
`--yes` confirmation that the non-production class is intentional.

AWS bootstrap also creates a read-only `developer` role for day-to-day Skiff
state inspection. The write role remains `deployer`, but its trust policy is
issued for short STS sessions and requires source identity plus session tags
for `skiff.dev/trace-id` and `skiff.dev/business-justification` so write
escalations are visible in AWS audit logs.

Use `skiff sudo <business-justification>` to enter a temporary write session:

```bash
eval "$(skiff sudo JIRA-1234)"
skiff deploy payments-api
```

Runner user-data is strict JSON under a top-level `skiff` object:

```json
{
  "skiff": {
    "env": "prod",
    "service": "payments-api",
    "provider": "aws",
    "region": "us-west-2",
    "state_bucket": "s3://skiff-state-prod",
    "control_key": "services/payments-api/control.json"
  }
}
```
