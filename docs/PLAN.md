# Observability Lab — Phased Plan

An end-to-end Kubernetes observability laboratory. Built **phase by phase**;
after each phase we pause, review, and answer open questions before continuing.

## Target architecture

```
                       ┌────────────────────────────────────────────┐
   client ── HTTP ───▶ │ gateway (Go)  auth, edge, PII in requests   │
                       └───────┬────────────────────────────────────┘
                               │ HTTP (traceparent propagated)
                       ┌───────▼────────────┐   AMQP (traceparent in headers)
                       │ orders (Go)        │──────────────┐
                       │ writes PostgreSQL  │              │
                       └───────┬────────────┘        ┌─────▼──────────────┐
                               │                     │ worker (Go)        │
                          PostgreSQL                 │ consumes queue,    │
                               ▲                     │ processes payment  │
                               └─────────────────────┴────────────────────┘

  Telemetry planes:
   - Metrics  : services → Prometheus → Grafana / Alertmanager
   - Traces   : services → OTel Collector → Tempo → Grafana
   - Op logs  : services → Fluent Bit → Loki → Grafana
   - Sec logs : auth/security events → Fluent Bit → Wazuh
   - Custom   : logparser (Go) parses logs, masks PII, exports Prometheus metrics
```

## Key decisions

| Concern | Choice | Rationale |
|---|---|---|
| Local cluster | **kind** | Docker already present; reproducible, CI-friendly |
| Queue | **RabbitMQ** | Clean AMQP header propagation for cross-queue traces |
| Trace backend | **Grafana Tempo** | Native Grafana integration alongside Loki |
| Languages | **Go** | Single toolchain across all services |
| IaC | **Terraform → Helm provider** | Declarative, dev/prod parity via values files |
| Config split | `environments/dev` vs `environments/prod` | Separate replica counts, resources, retention, alert thresholds |

## Phases

- [x] **Phase 1 — Repo & masking core.** Monorepo scaffold, plan, Makefile, kind
      config, and the dependency-free `pkg/masking` library proven by `go test`
      (tokens, phones, IDs, cards, IBANs, emails, SSNs cannot appear unmasked).
- [x] **Phase 2 — Go services.** gateway → orders → worker, PostgreSQL,
      RabbitMQ; shared `logging`, `config`, `httpmw`, `metrics`, `amqp`,
      `postgres` packages. Verified end-to-end via `scripts/smoke.sh`: a real
      order flows gateway→orders→queue→worker→Postgres (pending→paid), invalid
      tokens raise security-stream auth events, and no unmasked PII reaches logs.
- [x] **Phase 3 — OpenTelemetry.** `pkg/telemetry` (OTLP/HTTP exporter, W3C
      propagators); gateway/orders/worker instrumented; traceparent propagated
      over HTTP (otelhttp) and injected into/extracted from RabbitMQ headers.
      OTel Collector + Tempo + Grafana in compose. Verified: `scripts/smoke.sh`
      asserts one trace spans gateway → orders → worker.
- [x] **Phase 4 — Containerize & kind.** One multi-stage `Dockerfile`
      (distroless, non-root, static, ~7 MB images) parameterized by `SERVICE`;
      `make images` + `make kind-load`. 3-node kind cluster up; images loaded and
      verified runnable in-cluster (gateway pod reaches Running/"listening").
- [x] **Phase 5 — Terraform + Helm.** Terraform (`environments/{dev,prod}` +
      shared modules) drives Helm: kube-prometheus-stack, Loki, Tempo, an
      in-repo datastores chart (Postgres + RabbitMQ), and the app umbrella
      chart. Verified via `scripts/verify-cluster.sh`: an order driven through
      the gateway NodePort reaches `paid` in in-cluster Postgres, PII stays
      masked, and one trace spans gateway→orders→worker in in-cluster Tempo.
      Note: Bitnami's free chart repo was sunset, so Postgres/RabbitMQ use a
      small self-contained chart on stock images instead.
- [ ] **Phase 6 — Metrics, Grafana, Alertmanager.** RED metrics, dashboards.
- [ ] **Phase 7 — Logging pipeline.** Fluent Bit routing: operational → Loki,
      auth/security → Wazuh.
- [ ] **Phase 8 — Wazuh.** Manager/indexer/dashboard; security event ingestion.
- [ ] **Phase 9 — Custom Go exporter.** `logparser` masks PII and exports
      `masked_pii_events_total` and log-derived RED metrics.
- [ ] **Phase 10 — SLOs & burn-rate alerts.** Order success-rate and latency
      objectives with multi-window multi-burn-rate alerts + SLO dashboards.
- [ ] **Phase 11 — RBAC & masking proof.** K8s RBAC, Grafana RBAC, end-to-end
      demonstration that PII never appears unmasked in Loki or traces.

## Requirement traceability

| Advert requirement | Phase(s) |
|---|---|
| K8s cluster w/ APIs, queue, PostgreSQL, Prometheus, Grafana, Loki, Alertmanager | 2, 4, 5, 6 |
| Terraform + Helm, dev/prod values, everything in Git | 5 |
| OpenTelemetry tracing, ≥2 services, HTTP + queue propagation | 3 |
| SLOs & burn-rate alerts (transaction success + latency) | 10 |
| Fluent Bit + Wazuh; op logs → Loki, security → Wazuh | 7, 8 |
| Data masking + RBAC; no unmasked tokens/phones/IDs/accounts | 1, 11 |
| One custom Go log parser/exporter | 9 |
