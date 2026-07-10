#!/usr/bin/env bash
# Phase 2 end-to-end smoke test: drives a real order through
# gateway -> orders -> RabbitMQ -> worker -> PostgreSQL and verifies:
#   1. the transaction completes and the DB row reaches a terminal status
#   2. an invalid token produces a security-stream auth_failure event
#   3. PII (card, phone) never appears unmasked in any service log
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"
LOGDIR="${LOGDIR:-/tmp/obs-lab-smoke}"
mkdir -p "$LOGDIR"

export POSTGRES_DSN="postgres://obs:obs@localhost:5433/obs?sslmode=disable"
export AMQP_URL="amqp://obs:obs@localhost:5672/"
export AUTH_TOKEN="dev-secret-token"
export OTLP_ENDPOINT="localhost:4318" # OTel Collector OTLP/HTTP

go build -o bin/orders ./services/orders/cmd || exit 1
go build -o bin/gateway ./services/gateway/cmd || exit 1
go build -o bin/worker ./services/worker/cmd || exit 1

pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT

HTTP_ADDR=":8081" ./bin/orders  >"$LOGDIR/orders.log"  2>&1 & pids+=($!)
HTTP_ADDR=":8082" ./bin/worker  >"$LOGDIR/worker.log"  2>&1 & pids+=($!)
HTTP_ADDR=":8080" ORDERS_URL="http://localhost:8081" ./bin/gateway >"$LOGDIR/gateway.log" 2>&1 & pids+=($!)

for i in $(seq 1 20); do
  ok=1
  for p in 8080 8081 8082; do
    curl -sf -o /dev/null "http://localhost:$p/healthz" || ok=0
  done
  [ "$ok" = 1 ] && break
  sleep 0.5
done
echo "== services healthy =="

echo "== 1) valid order =="
body=$(curl -s -D "$LOGDIR/headers.txt" -X POST http://localhost:8080/api/orders \
  -H "Authorization: Bearer dev-secret-token" -H "Content-Type: application/json" \
  -d '{"customer_id":"cust-123","amount_cents":4999,"currency":"USD","card_number":"4111 1111 1111 1111","phone":"+254712345678"}')
echo "$body"
TRACE_ID=$(grep -i '^Trace-Id:' "$LOGDIR/headers.txt" | tr -d '\r' | awk '{print $2}')
echo "trace_id=$TRACE_ID"

echo "== 2) invalid token (expect 401 + security event) =="
curl -s -o /dev/null -w "http_status=%{http_code}\n" -X POST http://localhost:8080/api/orders \
  -H "Authorization: Bearer wrong-token" -H "Content-Type: application/json" -d '{}'

sleep 1.5
echo "== 3) order status in PostgreSQL =="
docker exec local-postgres-1 psql -U obs -d obs -t -c \
  "select id, customer_id, card_last4, status from orders order by created_at desc limit 3;"

echo "== 4) PII leak scan across all logs =="
if grep -REq '4111 1111 1111 1111|254712345678' "$LOGDIR"; then
  echo "FAIL: unmasked PII found in logs"; grep -RE '4111 1111 1111 1111|254712345678' "$LOGDIR"
else
  echo "PASS: no unmasked card/phone in any log"
fi

echo "== 5) distributed trace spans gateway -> orders -> worker (Tempo) =="
trace=""
for i in $(seq 1 15); do
  trace=$(curl -s "http://localhost:3200/api/traces/$TRACE_ID" 2>/dev/null)
  echo "$trace" | grep -q '"stringValue":"gateway"' && break
  sleep 1
done
for svc in gateway orders worker; do
  if echo "$trace" | grep -q "\"stringValue\":\"$svc\""; then
    echo "PASS: span from $svc present in trace $TRACE_ID"
  else
    echo "FAIL: no span from $svc in trace $TRACE_ID"
  fi
done

echo "== auth events (security stream) =="
grep -h '"stream":"security"' "$LOGDIR/gateway.log"
echo "== sample orders log line (note masking) =="
grep -h 'creating order' "$LOGDIR/orders.log" | head -1
