# Reproducing the retail-at-scale showcase

Requirements: Go (to build Pulse), `bash`, `jq` and `bc` (the run script uses jq
only to point a request at a different cohort file), about 4.5 GB of free disk
and **at least 6 GB of free RAM**. Archive commands peak at about 3.7–4.9 GB
with the `GOMEMLIMIT` the script sets. A full run takes about 20 minutes on 4
vCPUs.

## 1. Build Pulse and the bench helper

```sh
cd /path/to/pulse                       # fresh checkout
go build -o /tmp/pulse ./cmd/pulse
go build -o /tmp/retail-bench docs/showcase/retail-at-scale/bench/main.go   # optional, step 9
```

`bench/main.go` carries a `//go:build ignore` tag. It is not part of
`go build ./...` and builds only when named explicitly.

## 2. Copy the showcase to a scratch directory and run it

```sh
cp -r docs/showcase/retail-at-scale /tmp/retail && cd /tmp/retail
PULSE=/tmp/pulse BENCH=/tmp/retail-bench ./run-all.sh
```

`run-all.sh` runs every step in README order. It writes each response to `out/`
and appends `name<TAB>seconds<TAB>peak_rss_mb` per command to `out/timings.tsv`.
Leave out `BENCH=` to skip the library-only measurements (step 9).

What the script does, step by step (all commands run in the scratch dir):

| Step | Command(s) |
|---|---|
| 0 | `pulse synth from-schema -s synth/orders-YYYY-MM.synth.json -o orders-YYYY-MM.pulse --seed YYYYMM` × 13 |
| 1 | `pulse cohort inspect orders-2025-12.pulse --json` |
| 2 | `pulse shard create orders.pulse -i orders-2025-01.pulse … -i orders-2025-12.pulse --json`; `pulse shard list orders.pulse`; `pulse shard verify orders.pulse --json`; `pulse cohort inspect orders.pulse --json` |
| 3 | `pulse api predict -r req/01_year_kpis.json --json`; `pulse api process -r req/01_year_kpis.json --json`; the same request on `orders-2025-12.pulse`, with and without `--no-project` |
| 4 | `pulse api process -r req/02_monthly_trend.json --json` and `--stream` |
| 5 | `pulse api predict-compose -r req/04_dashboard_compose.json --json`; `pulse api compose -r req/04_dashboard_compose.json --json` (serial and `--parallel 4`) |
| 6 | `pulse api process-chain -r req/05_retention_chain.json --json --no-components`; same for `req/06_spend_distribution_chain.json` |
| 7 | `pulse index build orders-2025-MM.pulse --key customer_id --json` × 12; `pulse index list orders-2025-12.pulse`; `pulse api lookup -r req/07_lookup_customer.json -i orders-2025-MM.pulse --json` × 12; `pulse api process -r req/03_customer_scan.json --json` on the archive and on December |
| 8 | `pulse cohort inspect orders-2026-01.pulse --json`; `pulse shard add orders.pulse orders-2026-01.pulse --json`; `shard list`, `shard verify`, `shard compact`; KPIs again; `pulse index build orders-2026-01.pulse --key customer_id`; lookup in 2026-01; `pulse index verify orders-2025-12.pulse --key customer_id --json` |
| 9 | `retail-bench -mode month` (CountRecords, Inspect, in-process Lookup, DecodeWorkers 1/2/4, projection off); `-mode stream` (ProcessStreamResult on req/02); `-mode archive -shard-workers 1` and `4` (runs on the 13-month archive) |

Every archive-opening command runs under `GOMEMLIMIT=3500MiB` (override with
`ARCHIVE_MEM=`). Without it, a full-year process peaked at 7.3 GB.

To run a single step by hand, `cd` into the scratch dir and use the command from
the table. Request files use `"data_dir": "."`, so run them from the directory
that holds the cohorts.

## 3. Expected results

Counts and sums are deterministic (spec + seed). Float sums may differ in the
last printed digits between the serial and shard-parallel paths. Timings are
indicative only: they were measured on a shared 4-vCPU Xeon @ 2.10 GHz with 16 GB.

| Check | Where in `out/` | Expected |
|---|---|---|
| Rows per month | `synth-orders-*.txt` | 1.70 M, 1.60 M, 1.80 M, 1.85 M, 1.90 M, 1.90 M, 1.95 M, 2.05 M, 1.85 M, 1.95 M, 2.40 M, 2.80 M; 2026-01: 1.80 M; 0 rejected |
| December file size | `ls -l orders-2025-12.pulse` | 148,401,193 bytes |
| Archive records | `02_inspect_archive.json` `.data.record_count` | 23,750,000 (12 shards) |
| `shard verify` | `02_shard_verify.json` | `verified: true`, `error_n: 0`, `warning_n: 0` |
| Predict | `03_predict_kpis.json` | `valid: true`, `streamable: true` |
| Year KPIs | `03_year_kpis.json` `.data.data[0]` | lines 23,750,000; revenue 1,004,299,670.39; units 44,660,178; avg_line_value 42.2863; return_rate 0.044977 |
| December KPIs | `03_dec_kpis.json` | lines 2,800,000; revenue 131,868,581.97; units 5,267,586 |
| Monthly trend | `04_monthly_trend.json` | 12 rows; 202501 revenue 69,551,170.40, online_share 0.25025; 202512 revenue 131,868,581.97, online_share 0.35994 |
| Dashboard | `05_dashboard.json` | Southeast revenue 32,877,788.23 (share 0.2493); Electronics 44,115,363.50 (share 0.335); loyalty `1`: 50,000 customers, 615,909 lines; discount 0 %: 1,958,140 lines. Serial == `--parallel 4` |
| Retention chain | `06_retention_chain.json` | stage 0: 252,938 customers; 202501 cohort 85,032 customers, m02 0.61286, m12 0.27087; 202512 cohort 13,792 |
| Spend chain | `06_spend_chain.json` | stage 0 filtered_rows 2,373,549; band 7: 5,122 customers, spend 22,410,911.48, share 0.224, member_share 0.973 |
| Dec index | `07_index_2025-12.json` | distinct_keys 1,297,702; indexed_records 2,800,000 |
| Lookup 4242 | `07_lookup_2025-MM.json` row counts | 13, 7, 8, 2, 14, 8, 8, 10, 3, 8, 10, 8 (equal to `07_customer_scan_archive.json` `lines`); 2026-01: 11 |
| Customer scan | `07_customer_scan_archive.json` | 202501: 13 lines, 9 orders, spend 539.91; 202512: 8 lines, 6 orders, spend 236.43 |
| After append | `08_kpis_13_months.json` | lines 25,550,000; revenue 1,077,716,051.99; units 48,040,576 |
| Index after append | `08_index_verify_2025-12.json` | `fresh: true` |
| Bench month | `09_bench_month.json` | count_records 2,800,000; lookup_rows 8; kpi_revenue 131,868,581.97 |
| Bench archive | `09_bench_archive_sw*.json` | kpi_lines 25,550,000 |

Indicative timings from the reference run (seconds / peak RSS):

| Command | Time | RSS |
|---|---:|---:|
| synth one month | 9.5–16.5 | 0.44–0.82 GB |
| inspect one month (CLI) | 0.28 | 147 MB |
| shard create, 12 months | 25.9 | 4.6 GB |
| inspect / list / verify archive | 23.3 / 16.7 / 21.5 | 3.6–4.9 GB |
| KPIs on archive (23.75 M) | 56.3 (41.9 on a quiet run) | 3.7 GB |
| KPIs on December file | 0.81 | 192 MB |
| Compose dashboard (December) | 4.84 serial / 4.23 parallel | 307 / 769 MB |
| Retention chain / spend chain | 123.5 / 71.8 | 3.9 / 3.7 GB |
| Index build per month | 7.7–13.2 | 0.39–0.62 GB |
| Lookup per month (CLI) | 0.17–0.22 | 40 MB |
| Customer scan, archive / December | 55.7 / 0.90 | 3.7 GB / 194 MB |
| shard add 2026-01 / compact | 36.4 / 39.5 | 3.8 / 4.0 GB |
| Bench: CountRecords / in-process lookup | 0.089 ms / 0.34 ms | — |

## Cleanup

The scratch directory holds about 4.2 GB: 13 month files, the archive and 13
sidecar indexes. Remove it with `rm -rf /tmp/retail` when done. None of it
belongs in the repository.
