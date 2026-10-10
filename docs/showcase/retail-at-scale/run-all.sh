#!/usr/bin/env bash
# Runs every step of the retail-at-scale showcase end to end.
# Usage: PULSE=/path/to/pulse ./run-all.sh   (run from a scratch copy of this directory)
# Every number is computed by Pulse; this script only invokes Pulse and records
# wall-clock time and peak resident memory (VmHWM from /proc) per command.
set -euo pipefail
PULSE=${PULSE:-pulse}
# Opening a shard archive reads the whole archive into memory; capping the Go
# heap keeps peak RSS near 3.7 GB instead of ~7.3 GB for the 1.26 GB archive.
ARCHIVE_MEM=${ARCHIVE_MEM:-3500MiB}
mkdir -p out
: > out/timings.tsv

# step NAME OUTFILE CMD... : runs CMD, stdout -> out/OUTFILE, logs seconds + peak RSS (MB)
step() {
  local name=$1 outfile=$2; shift 2
  local start end hwm=0 v pid
  start=$(date +%s.%N)
  "$@" > "out/$outfile" &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    v=$(awk '/VmHWM/{print $2}' "/proc/$pid/status" 2>/dev/null || true)
    [ -n "$v" ] && hwm=$v
    sleep 0.05
  done
  wait "$pid"
  end=$(date +%s.%N)
  printf '%s\t%.2f\t%d\n' "$name" "$(echo "$end - $start" | bc)" $((hwm / 1024)) | tee -a out/timings.tsv
}

# 0. Generate thirteen monthly single-file cohorts (deterministic: spec + seed).
for spec in synth/orders-*.synth.json; do
  m=$(basename "$spec" .synth.json)            # orders-2025-01
  seed=$(echo "$m" | tr -d 'a-z-')             # 202501
  step "synth $m" "synth-$m.txt" "$PULSE" synth from-schema -s "$spec" -o "$m.pulse" --seed "$seed"
done

# 1. Header-only inspect of one month (reads no records).
step "inspect month (single file)" 01_inspect_month.json "$PULSE" cohort inspect orders-2025-12.pulse --json

# 2. Bundle 2025 into a shard archive, then list and verify it.
includes=(); for m in 01 02 03 04 05 06 07 08 09 10 11 12; do includes+=(-i "orders-2025-$m.pulse"); done
step "shard create (12 months)" 02_shard_create.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard create orders.pulse "${includes[@]}" --json
step "shard list" 02_shard_list.txt env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard list orders.pulse
step "shard verify" 02_shard_verify.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard verify orders.pulse --json
step "inspect archive" 02_inspect_archive.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" cohort inspect orders.pulse --json

# 3. Predict, then run, the full-year KPI request.
step "predict 01 (archive)" 03_predict_kpis.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api predict -r req/01_year_kpis.json --json
step "process 01 (archive, 23.75M rows)" 03_year_kpis.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process -r req/01_year_kpis.json --json
jq '.cohort.filename = "orders-2025-12.pulse"' req/01_year_kpis.json > out/01_dec_kpis.req.json
step "process 01 (December single file)" 03_dec_kpis.json "$PULSE" api process -r out/01_dec_kpis.req.json --json
step "process 01 (December, --no-project)" 03_dec_kpis_noproject.json "$PULSE" api process -r out/01_dec_kpis.req.json --json --no-project

# 4. Monthly trend, buffered and streamed.
step "process 02 (archive)" 04_monthly_trend.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process -r req/02_monthly_trend.json --json
step "process 02 --stream (archive)" 04_monthly_trend.ndjson env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process -r req/02_monthly_trend.json --stream

# 5. December dashboard: five questions in one Compose call.
step "predict-compose 04" 05_predict_dashboard.json "$PULSE" api predict-compose -r req/04_dashboard_compose.json --json
step "compose 04 (serial)" 05_dashboard.json "$PULSE" api compose -r req/04_dashboard_compose.json --json
step "compose 04 (--parallel 4)" 05_dashboard_parallel.json "$PULSE" api compose -r req/04_dashboard_compose.json --json --parallel 4

# 6. Customer-level rollups with process-chain (1-in-10 customer panel).
step "process-chain 05 retention" 06_retention_chain.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process-chain -r req/05_retention_chain.json --json --no-components
step "process-chain 06 spend bands" 06_spend_chain.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process-chain -r req/06_spend_distribution_chain.json --json --no-components

# 7. Customer 360: sidecar index per month + point lookups vs filtered scans.
for m in 01 02 03 04 05 06 07 08 09 10 11 12; do
  step "index build 2025-$m" "07_index_2025-$m.json" "$PULSE" index build "orders-2025-$m.pulse" --key customer_id --json
done
step "index list 2025-12" 07_index_list.txt "$PULSE" index list orders-2025-12.pulse
for m in 01 02 03 04 05 06 07 08 09 10 11 12; do
  step "lookup 4242 in 2025-$m" "07_lookup_2025-$m.json" "$PULSE" api lookup -r req/07_lookup_customer.json -i "orders-2025-$m.pulse" --json
done
step "scan 03 for customer 4242 (archive)" 07_customer_scan_archive.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process -r req/03_customer_scan.json --json --no-components
jq '.cohort.filename = "orders-2025-12.pulse"' req/03_customer_scan.json > out/03_dec_scan.req.json
step "scan 03 for customer 4242 (December file)" 07_customer_scan_dec.json "$PULSE" api process -r out/03_dec_scan.req.json --json --no-components

# 8. A new month arrives: append it, verify, and re-run the KPIs.
step "inspect month 2026-01" 08_inspect_2026-01.json "$PULSE" cohort inspect orders-2026-01.pulse --json
step "shard add 2026-01" 08_shard_add.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard add orders.pulse orders-2026-01.pulse --json
step "shard list (13)" 08_shard_list.txt env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard list orders.pulse
step "shard verify (13)" 08_shard_verify.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard verify orders.pulse --json
step "shard compact" 08_shard_compact.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" shard compact orders.pulse --json
step "process 01 (archive, 13 months)" 08_kpis_13_months.json env GOMEMLIMIT=$ARCHIVE_MEM "$PULSE" api process -r req/01_year_kpis.json --json
step "index build 2026-01" 08_index_2026-01.json "$PULSE" index build orders-2026-01.pulse --key customer_id --json
step "lookup 4242 in 2026-01" 08_lookup_2026-01.json "$PULSE" api lookup -r req/07_lookup_customer.json -i orders-2026-01.pulse --json
step "index verify 2025-12 after add" 08_index_verify_2025-12.json "$PULSE" index verify orders-2025-12.pulse --key customer_id --json

echo "steps 0-8 done; see out/timings.tsv"

# 9. Library-only knobs (optional): BENCH=/path/to/bench built from bench/main.go.
if [ -n "${BENCH:-}" ]; then
  step "bench month" 09_bench_month.json "$BENCH" -mode month -month orders-2025-12.pulse
  step "bench stream (archive)" 09_bench_stream.json env GOMEMLIMIT=$ARCHIVE_MEM "$BENCH" -mode stream
  step "bench archive ShardWorkers=1" 09_bench_archive_sw1.json env GOMEMLIMIT=$ARCHIVE_MEM "$BENCH" -mode archive -shard-workers 1
  step "bench archive ShardWorkers=4" 09_bench_archive_sw4.json env GOMEMLIMIT=$ARCHIVE_MEM "$BENCH" -mode archive -shard-workers 4
fi
echo "bench done"
