# API plus managed database

This recipe creates one API service and one private managed relational database. Object storage remains Skiff's durable control plane; database credentials are represented as secret references and are not written to object state as plaintext.

Generate the starter:

```bash
skiff init stack api-database orders --dir examples/stacks/api-database
```

The stack binding:

```yaml
bindings:
  - from: api
    to: db
    as: DATABASE_URL
```

compiles into:

- an API service running one workload replica per VM by default
- an RDS managed database with encrypted storage and backup retention
- a Secrets Manager connection reference surfaced to the service as `DATABASE_URL`
- security-group rules that allow the service to reach the database privately
- logs, metrics, target group, launch template, IAM role, and ASG resources for the API

Credential handling is reference-based. The compiled runtime manifest carries `DATABASE_URL` as a `secret://managed-database/<database>/connection-url` reference. Provider lowering creates the Secrets Manager secret resource, and the service IAM role is scoped to referenced secrets. The recipe never stores a database password in a spec, event, release, or control document.

Backup defaults are conservative: backups are enabled, storage is encrypted, retention defaults to seven days, deletion protection is enabled in the AWS plan, and destructive restore is intentionally not part of this recipe. Restore and cutover are handled by explicit database restore sagas.

Preview cloud primitives before deploying:

```bash
skiff explain examples/stacks/api-database/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod
skiff plan examples/stacks/api-database/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod --format json
```

The plan should show visible cloud resources, including RDS, Secrets Manager, EC2 security groups, a launch template, an Auto Scaling Group, a target group, and CloudWatch logging/metrics.
