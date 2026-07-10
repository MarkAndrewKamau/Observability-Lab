# Observability Lab

An end-to-end Kubernetes observability laboratory: a handful of Go APIs backed
by RabbitMQ and PostgreSQL, fully instrumented with metrics, traces and logs,
deployed via Terraform + Helm, with SLO burn-rate alerting, security-event
routing to Wazuh, and provable PII masking.

> Built **phase by phase** — see [docs/PLAN.md](docs/PLAN.md). We pause after
> each phase for review before continuing.

## Status

| Phase | State |
|---|---|
| 1 — Repo & masking core | ✅ done |
| 2 — Go services | ⏳ next |
| 3–11 | ⬜ planned |

## Layout

```
services/          Go microservices (gateway, orders, worker, logparser)
pkg/               Shared libraries (masking, logging, telemetry, httpmw, amqp, config)
deploy/terraform/  Terraform root + modules, environments/{dev,prod}
deploy/helm/       App umbrella chart + values/{dev,prod}
k8s/               kind cluster config
docs/              Plan and architecture
```

## Prerequisites

- Docker (present) · Go 1.26 (present)
- `kind`, `kubectl`, `helm`, `terraform` — installed in Phase 0/4 (`make tools`)

## Quick start (current)

```bash
make test        # runs unit tests, including the PII-masking proof suite
```

## Masking proof

`pkg/masking` is dependency-free and unit-tested to guarantee tokens, phone
numbers, national IDs, account numbers, cards, IBANs, emails and SSNs cannot
appear unmasked:

```bash
go test ./pkg/masking/ -v
```
