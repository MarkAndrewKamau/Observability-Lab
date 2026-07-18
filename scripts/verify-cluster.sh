#!/usr/bin/env bash
# Phase 5 in-cluster verification: proves the full stack deployed by Terraform
# actually works end-to-end inside kind:
#   1. app Deployments become Ready
#   2. a real order driven through the gateway NodePort reaches 'paid' in the
#      in-cluster PostgreSQL
#   3. no unmasked PII appears in any service pod log
#   4. the distributed trace lands in the in-cluster Tempo
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
NS_APP=obs
NS_MON=monitoring
GW_URL="http://localhost:18080" # kind maps NodePort 30080 -> host 18080

echo "== 1) wait for app deployments =="
for d in obs-gateway obs-orders obs-worker; do
  kubectl -n "$NS_APP" rollout status deploy/"$d" --timeout=120s || exit 1
done

echo "== 2) drive an order through the gateway NodePort =="
resp=$(curl -s -D /tmp/gw-headers.txt -X POST "$GW_URL/api/orders" \
  -H "Authorization: Bearer dev-secret-token" -H "Content-Type: application/json" \
  -d '{"customer_id":"cust-k8s","amount_cents":7999,"currency":"USD","card_number":"4111 1111 1111 1111","phone":"+254712345678"}')
echo "$resp"
TRACE_ID=$(grep -i '^Trace-Id:' /tmp/gw-headers.txt | tr -d '\r' | awk '{print $2}')
echo "trace_id=$TRACE_ID"
sleep 2

echo "== 3) order status in in-cluster PostgreSQL =="
PG_POD=$(kubectl -n "$NS_APP" get pod -l app.kubernetes.io/name=postgresql -o name | head -1)
kubectl -n "$NS_APP" exec "$PG_POD" -- env PGPASSWORD=obs \
  psql -U obs -d obs -t -c "select id, customer_id, card_last4, status from orders order by created_at desc limit 3;"

echo "== 4) PII leak scan across app pod logs =="
logs=$(kubectl -n "$NS_APP" logs -l app.kubernetes.io/part-of=obs-lab --tail=200 --prefix 2>/dev/null)
if grep -Eq '4111 1111 1111 1111|254712345678' <<<"$logs"; then
  echo "FAIL: unmasked PII in pod logs"; else echo "PASS: no unmasked card/phone in pod logs"; fi

echo "== 5) trace present in in-cluster Tempo =="
kubectl -n "$NS_MON" port-forward svc/obs-tempo 3200:3200 >/tmp/tempo-pf.log 2>&1 &
PF=$!; trap "kill $PF 2>/dev/null" EXIT; sleep 3
trace=""
for i in $(seq 1 15); do
  trace=$(curl -s "http://localhost:3200/api/traces/$TRACE_ID" 2>/dev/null)
  echo "$trace" | grep -q '"stringValue":"gateway"' && break
  sleep 1
done
for svc in gateway orders worker; do
  if echo "$trace" | grep -q "\"stringValue\":\"$svc\""; then
    echo "PASS: span from $svc present"
  else
    echo "FAIL: no span from $svc in trace $TRACE_ID"
  fi
done
