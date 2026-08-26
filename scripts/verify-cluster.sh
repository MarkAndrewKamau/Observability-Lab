#!/usr/bin/env bash
# Phase 5-7 in-cluster verification: proves the full stack deployed by
# Terraform actually works end-to-end inside kind:
#   1. app Deployments become Ready
#   2. a real order driven through the gateway NodePort reaches 'paid' in the
#      in-cluster PostgreSQL
#   3. no unmasked PII appears in any service pod log
#   4. the distributed trace lands in the in-cluster Tempo
#   5. Prometheus scrapes gateway/orders/worker and transaction metrics flow
#   6. Fluent Bit tails every node and ships logs to Loki, routed by the app's
#      stream field (operational vs security)
#   7. Wazuh manager/indexer/dashboard are up and a security-stream auth
#      failure lands as an alert in the Wazuh indexer
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

echo "== 6) Prometheus is scraping our services (targets UP) =="
kubectl -n "$NS_MON" port-forward svc/kube-prometheus-stack-prometheus 9090:9090 >/tmp/prom-pf.log 2>&1 &
PP=$!; trap "kill $PF $PP 2>/dev/null" EXIT
for i in $(seq 1 15); do curl -sf http://localhost:9090/-/ready >/dev/null 2>&1 && break; sleep 1; done
curl -s "http://localhost:9090/api/v1/targets?state=active" | python3 -c "
import sys,json
d=json.load(sys.stdin)['data']['activeTargets']
for svc in ('gateway','orders','worker'):
    h=[t['health'] for t in d if svc in t['labels'].get('job','')]
    print(f'{\"PASS\" if h and all(x==\"up\" for x in h) else \"FAIL\"}: obs-{svc} target {h or \"MISSING\"}')
"
echo "== 7) transaction metrics are non-zero =="
for q in 'sum(transactions_total)' 'sum(queue_consumed_total)'; do
  v=$(curl -s "http://localhost:9090/api/v1/query?query=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$q")" \
      | python3 -c "import sys,json;r=json.load(sys.stdin)['data']['result'];print(r[0]['value'][1] if r else '0')")
  awk -v v="$v" -v q="$q" 'BEGIN{printf "%s: %s = %s\n", (v+0>0?"PASS":"FAIL"), q, v}'
done

echo "== 8) Fluent Bit DaemonSet ready on every node =="
kubectl -n "$NS_MON" rollout status ds/fluent-bit --timeout=60s >/dev/null || { echo "FAIL: fluent-bit rollout not complete"; exit 1; }
fb_ready=$(kubectl -n "$NS_MON" get ds fluent-bit -o jsonpath='{.status.numberAvailable}')
fb_desired=$(kubectl -n "$NS_MON" get ds fluent-bit -o jsonpath='{.status.desiredNumberScheduled}')
[ "${fb_ready:-0}" = "${fb_desired:-0}" ] && echo "PASS: fluent-bit $fb_ready/$fb_desired pods ready" \
  || echo "FAIL: fluent-bit $fb_ready/$fb_desired pods ready"

echo "== 9) logs arrive in Loki, routed by the stream field =="
kubectl -n "$NS_MON" port-forward svc/loki-gateway 3100:80 >/tmp/loki-pf.log 2>&1 &
LP=$!; trap "kill $PF $PP $LP 2>/dev/null" EXIT
for i in $(seq 1 15); do curl -sf http://localhost:3100/ready >/dev/null 2>&1 && break; sleep 1; done
loki_count() { # $1 = LogQL query; prints matching log lines
  curl -s --get "http://localhost:3100/loki/api/v1/query_range" \
    --data-urlencode "query=$1" \
    --data-urlencode "start=$(date -d '10 min ago' +%s%N)" \
    --data-urlencode "end=$(date +%s%N)" --data-urlencode "limit=3" \
    | python3 -c "
import sys, json
d = json.load(sys.stdin)['data']['result']
print(sum(len(s['values']) for s in d))
"
}
op=$(loki_count '{stream="operational"} |= "creating order"')
[ "${op:-0}" -gt 0 ] && echo "PASS: $op operational log lines in Loki" \
  || echo "FAIL: no operational logs in Loki"
# Security-stream lines are routed to Wazuh (UDP/514) since Phase 8; they are
# verified end-to-end as alerts in step 12 below, not in Loki.

echo "== 10) drive an auth failure to feed the Wazuh security stream =="
curl -s -o /dev/null -w "status=%{http_code}\n" -X POST "$GW_URL/api/orders" \
  -H "Authorization: Bearer wrong-token" -H "Content-Type: application/json" \
  -d '{"customer_id":"cust-k8s","amount_cents":100,"currency":"USD","card_number":"4111 1111 1111 1111","phone":"+254712345678"}'

echo "== 11) Wazuh manager / indexer / dashboard ready =="
kubectl -n "$NS_MON" rollout status sts/wazuh-manager-master --timeout=600s >/dev/null || { echo "FAIL: wazuh manager not ready"; exit 1; }
kubectl -n "$NS_MON" rollout status sts/wazuh-indexer --timeout=600s >/dev/null || { echo "FAIL: wazuh indexer not ready"; exit 1; }
kubectl -n "$NS_MON" rollout status deploy/wazuh-dashboard --timeout=600s >/dev/null || { echo "FAIL: wazuh dashboard not ready"; exit 1; }
echo "PASS: wazuh manager, indexer and dashboard are Running"

echo "== 12) security alert ingested into the Wazuh indexer =="
kubectl -n "$NS_MON" port-forward svc/wazuh-indexer 9200:9200 >/tmp/wazuh-pf.log 2>&1 &
WP=$!; trap "kill $PF $PP $LP $WP 2>/dev/null" EXIT
for i in $(seq 1 30); do
  curl -sk https://localhost:9200/_cluster/health >/dev/null 2>&1 && break
  sleep 5
done
hits=0
for i in $(seq 1 24); do
  hits=$(curl -sk -u admin:WazuhSecretPassword \
    'https://localhost:9200/wazuh-alerts-*/_search?size=1' \
    -H 'Content-Type: application/json' \
    -d '{"query":{"query_string":{"query":"authentication failed"}}}' \
    2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)["hits"]["total"]["value"])' 2>/dev/null)
  [ "${hits:-0}" -gt 0 ] && break
  sleep 10
done
if [ "${hits:-0}" -gt 0 ]; then
  echo "PASS: $hits security alert(s) from the gateway in Wazuh"
else
  echo "FAIL: no security alert reached the Wazuh indexer"
fi
