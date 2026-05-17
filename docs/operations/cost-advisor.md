# Cost Advisor

Skiff runs one workload replica per VM by default. `skiff cost explain` makes
that tradeoff visible without turning Skiff into a bin-packing scheduler.

The advisor is read-only. It combines the service spec shape with optional
observed signals and emits relative recommendations with confidence and
evidence. It does not claim exact cloud billing impact.

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

- Pricing, discounts, committed use, and region-specific rates are not modeled.
- Recommendations are relative shape and capacity guidance, not billing truth.
- Production capacity changes still need SLO validation and an explicit Skiff
  operation or saga before mutation.
