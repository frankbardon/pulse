# Reproducing the multi-time-zone operations showcase

Every figure in [README.md](README.md) comes from the Pulse CLI. These steps rebuild them from a fresh checkout.

Requirements: Go (the version in the repo's `go.mod`), `jq` and `bash`.

## 1. Build Pulse

```bash
cd <repo root>
go build -o /tmp/pulse ./cmd/pulse
```

## 2. Copy the showcase to a scratch location

The run writes `.pulse` cohorts and an `out/` directory next to the requests, so work on a copy:

```bash
rm -rf /tmp/tz-ops && cp -r docs/showcase/multi-timezone-ops /tmp/tz-ops
cd /tmp/tz-ops
```

## 3. (Optional) Check the committed data

The CSVs under `data/` are committed. Confirm they are unchanged:

```bash
sha256sum data/deliveries.csv data/feeds/*.csv
```

Expected digests:

```
e1abf03d0e40aa06253581695a2e95f60a43ff1d45addf6799e023ad3462c0c0  data/deliveries.csv
4d4ceaba622ae4d3501090c7552c0fda22562fc828a6f469e73037084cc36098  data/feeds/nyc_night_sort.csv
d75aee8218ba41ba7c56c9e35e3d1176487dee9222f924f56adf85f26e3956b3  data/feeds/syd_night_sort.csv
```

## 4. Run every step

```bash
PULSE=/tmp/pulse ./run_all.sh
```

[`run_all.sh`](run_all.sh) runs the following, in order:

| Step | Command | Output in `out/` |
|---|---|---|
| 0 | `pulse import csv -i data/deliveries.csv -o deliveries.pulse --schema schemas/deliveries.schema.json --json` | `00_import_deliveries.json` |
| 6a | `pulse import csv` of each night-sort feed with `--source-tz` and the default DST policy (refused by design) | `13a_import_syd_default.json`, `14a_import_nyc_default.json` |
| 6b | the same imports with `--dst-policy earlier` and `--dst-policy later` → `{syd,nyc}_night_sort_{earlier,later}.pulse` | `13b_*`, `14b_*` |
| 6c | `pulse export csv -i syd_night_sort_{earlier,later}.pulse --tz Australia/Sydney` | `syd_night_sort_*_local.csv` |
| – | `pulse api predict -r req/06_syd_daily_windows.json --json` (shows the resolved `time_zones[]`) | `06_predict.json` |
| 1–6 | each `req/NN_*.json`: `pulse api compose -r … --json --no-components` when the file has a `requests` array, otherwise `pulse api process -r … --json --no-components` | `NN_*.json` |

The script stops on the first request whose envelope has a non-empty `errors` array. A successful run ends with `ok  14_nyc_dst_night`.

To run a single step by hand, from the copy:

```bash
/tmp/pulse api process -r req/01_weekday_volume_utc.json --json
/tmp/pulse api compose -r req/05_daily_anomaly_screen.json --json
```

## 5. Check the key figures

```bash
./key_figures.sh
```

[`key_figures.sh`](key_figures.sh) only quotes values from `out/`. Expected output, verified on 2026-10-10 against this branch:

| Figure | Source | Expected |
|---|---|---|
| Deliveries imported | `00_import_deliveries.json` | 17193 |
| Deliveries on UTC Sundays, all sites | `01` column margin "Sunday" | 286 |
| … of which SYD | `01` cell SYD × Sunday | 221 |
| Sunday columns in the local-zone view | `02` | 0 |
| SYD on-time, Mon 2026-05-11, UTC / Sydney day | `03` | 0.6667 / 1 |
| SYD incident-period on-time, UTC / Sydney days | `04` row `2_incident` | 0.4909 / 0.4167 |
| SYD 2026-05-12 rolling z (on-time, W=28, Sydney days) | `05` slot 6 | −7.6522 |
| SYD 2026-05-12 rolling index (on-time) | `05` slot 6 | 44.2765 |
| SYD 2026-05-12 rolling z (duration) | `06` | 9.3417 |
| SYD 2026-05-14 rolling z on UTC days (missed) | `07` slot 1 | −1.2264 |
| BLR weekly Mann-Kendall Z / Holm-adjusted p | `08` slot 1 | 4.9814 / 3.79e−6 |
| LON weekly Mann-Kendall Z / p | `08` slot 3 | −0.9039 / 0.3660 |
| BLR daily trend Z, raw / 6-day moving average | `09` | 5.3413 / 11.7569 |
| SYD 2026-04-05 night batches (Sydney day) | `13` slot 4 | 28 |
| SYD import with `--dst-policy earlier` | `13b_import_syd_earlier.json` warning details | `ambiguous_n: 8, nonexistent_n: 0` |
| NYC import with the default policy | `14a_import_nyc_default.json` | `PULSE_IMPORT_DST_NONEXISTENT` |

The full tables in the README come from the same `out/*.json` files: crosstab cells under `data.crosstab.matrix` (or `data.responses[i].crosstab.matrix` for Compose), grouped rows under `data.data` / `data.responses[i].data`, overlay values under `…overlays[k].payload.series.entries[j].summary.statistic` (aligned with the data rows), and test results under `…post_tests[0]`.

## 6. Clean up

```bash
rm -rf /tmp/tz-ops
```

In the repository, `.pulse` files and `out/` are git-ignored. Never commit them.
