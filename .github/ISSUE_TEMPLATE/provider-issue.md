---
name: Provider issue
about: Report cloud provider behavior, drift, or primitive mapping problems
title: "provider: "
labels: provider
---

## Summary

## Provider Context

- Provider:
- Region:
- Environment:
- Service:
- Cloud primitive type:
- Provider resource ID:

## Skiff Evidence

```bash
skiff doctor <service> --direct --state <state-uri> --env <env> --provider <provider> --region <region> --fresh --format json --trace-id <trace-id>
skiff drift <service> --direct --state <state-uri> --env <env> --provider <provider> --region <region> --format json --trace-id <trace-id>
```

## Object State

- service control path:
- operation control path:
- resource record path:
- trace ID:
- operation or saga ID:

## Cloud Evidence

- ASG:
- target group:
- launch template:
- IAM role:
- log group:
- metrics:

## Cleanup Status

- resources still present:
- safe cleanup command:
