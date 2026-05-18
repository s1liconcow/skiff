# Cost Advisor

Skiff runs one workload replica per VM by default. `skiff cost explain` makes
that tradeoff visible without turning Skiff into a bin-packing scheduler.

The advisor is read-only. It combines the service spec shape with optional
observed signals and emits relative recommendations with confidence and
evidence. When a local pricing config is available, it estimates compute and
baseline storage spend for On-Demand and Reserved Instance schemes.

```bash
skiff cost explain payments-api \
  --file examples/service/http-hello/skiff.yaml \
  --cpu-p95 18 \
  --memory-p95 41 \
  --request-count 10300000 \
  --request-rps 120 \
  --warm-capacity 4 \
  --log-mb-per-hour 320 \
  --window 24h \
  --format json
```

AWS pricing estimates use a local pricing config file. Refresh it explicitly
from public AWS EC2 and RDS Price List data:

```bash
skiff cost pricing update \
  --region us-east-1 \
  --out .skiff-pricing.json
```

Then run `cost explain` without making a network request. When the config is
written to the default `.skiff-pricing.json` path, `cost explain` detects it on
the next run without needing `--pricing-config`:

```bash
skiff cost explain payments-api \
  --file examples/service/http-hello/skiff.yaml \
  --region us-east-1 \
  --pricing-scheme on-demand \
  --pricing-scheme ri-1yr-standard-no-upfront \
  --pricing-scheme ri-3yr-standard-all-upfront \
  --format json
```

`skiff cost pricing update` is the slow public-data refresh step. It fetches the
regional Amazon EC2 and Amazon RDS bulk price lists and writes a small Skiff
pricing catalog. Use `--aws-pricing-file <path>` and
`--aws-rds-pricing-file <path>` for offline tests or pinned comparisons. The
estimate uses the AWS instance type produced by Skiff's provider lowering for
the service machine size, for example `small` -> `t3.small`, and the RDS
instance class produced by database lowering, for example database `small` ->
`db.t4g.micro`.
Teams with negotiated rates can edit the pricing config or pass another
`--pricing-config` file; `cost explain` consumes the local file as-is. The
default `.skiff-pricing.json` path is local operator state and is ignored by
git.

`cost explain` does not fetch AWS pricing by default. For one-off diagnostics,
`--aws-pricing` still forces a live public AWS pricing fetch, but normal
operator use should prefer a local pricing config. If pricing data is missing,
human and JSON output include the exact `skiff cost pricing update` command to
generate the local config.

The infrastructure section includes low, medium, and high utilization
scenarios. Fixed resources such as provisioned EBS volume storage stay fixed.
Autoscaled services use min replicas for low, the midpoint for medium, and max
replicas for high. Stateful groups keep their fixed replica count and vary
snapshot-storage assumptions across 25%, 50%, and 100% of provisioned volume
size.

Useful signals:

- `--cpu-p95` and `--memory-p95`: utilization evidence for downsize or upsize
  recommendations.
- `--request-count` and `--request-rps`: traffic context for conservative
  replica guidance.
- `--warm-capacity`: the number of replicas the operator wants always warm.
- `--unhealthy-targets`: target health evidence that max replicas may need
  headroom.
- `--log-mb-per-hour`: log-volume evidence for noisy-log recommendations.

`skiff plan` also includes `advisor_warnings` in JSON output for obviously
expensive desired state, such as high minimum replicas or large VM shapes.
Those warnings are static and low-confidence until runtime metrics are supplied.

Limitations:

- AWS pricing estimates include EC2 instance compute, RDS instance compute, and
  baseline EBS/RDS storage when present. They exclude load balancer hourly/LCU
  charges, NAT, data transfer, CloudWatch usage, taxes, credits, Savings Plans,
  and private discounts. RDS backup line items are listed, but charged backup
  storage above the free allocation needs observed retained-backup volume.
- RI estimates are effective hourly equivalents for matching Standard RI terms.
  Actual billing can differ with existing reservations, size flexibility, scope,
  and account discounts.
- Production capacity changes still need SLO validation and an explicit Skiff
  operation or saga before mutation.
