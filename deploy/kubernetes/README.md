# Kubernetes deployment

Working manifests for the standalone service, split into an **API tier** and a
**worker tier** — the split `FLEXITYPE_RUN_*` exists for. Every adopter was
re-deriving these from prose.

```bash
kubectl create namespace flexitype
kubectl -n flexitype create secret generic flexitype-db \
  --from-literal=url='postgres://flexitype:...@db:5432/flexitype?sslmode=verify-full'
kubectl -n flexitype create secret generic flexitype-accounts \
  --from-file=accounts.json=./accounts.json
kubectl -n flexitype apply -f .
```

## Why two tiers

The API tier serves requests and runs **no** background loops, so it scales on
request rate and a rollout never interrupts delivery. The worker tier runs the
relay, the delivery worker, the pruner and the change-set scheduler, and stays
at one replica by default.

The relay and the delivery worker are safe on many replicas — the relay leases
batches and the worker claims with `FOR UPDATE SKIP LOCKED`. The pruner and the
scheduler are safe too but gain nothing from more than one.

## What the probes mean

- `/healthz` — the process is up. Liveness.
- `/readyz` — the process can serve, database included. Readiness.

The readiness probe is what should gate traffic; a pod whose database is
unreachable is up and cannot serve. `terminationGracePeriodSeconds` is set
above `FLEXITYPE_SHUTDOWN_TIMEOUT` so in-flight work drains before SIGKILL.

## Metrics

Both tiers expose `/metrics` and carry Prometheus scrape annotations. A
`ServiceMonitor` is included for the Prometheus Operator; delete it if you
scrape by annotation.

Alert on **`flexitype_outbox_oldest_pending_seconds`**, not on
`flexitype_outbox_pending`: 500 pending is healthy under load and alarming if
the oldest of them is an hour old, and a count cannot tell those apart.
