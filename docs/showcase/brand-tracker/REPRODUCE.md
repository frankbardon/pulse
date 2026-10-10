# Reproducing the brand-tracker example

Everything runs through the `pulse` CLI. `jq` is used only to read figures out of the JSON responses (`run.sh` prints error counts and `verify.sh` compares figures).

## From a fresh checkout

```sh
# 1. Build the CLI
go build -o /tmp/pulse ./cmd/pulse

# 2. Copy the example somewhere disposable (it writes brand_tracker.pulse, out/ and deliver/)
rm -rf /tmp/brand-tracker && cp -r docs/showcase/brand-tracker /tmp/brand-tracker
cd /tmp/brand-tracker

# 3. Regenerate the cohort and run every step (predict first, then execute)
PULSE=/tmp/pulse ./run.sh

# 4. Check the key figures
./verify.sh
```

`run.sh` does the following, in order:

| Step | Command | Output |
|---|---|---|
| 0 | `pulse synth from-schema -s spec/brand_tracker.synth.json -o brand_tracker.pulse --seed 2025 --json`, then `pulse cohort inspect brand_tracker.pulse --json` | `out/00_synth.json`, `out/00_inspect.json` |
| 1–9 | For each `req/01…10_*.json`: `pulse api predict` (single request) or `pulse api predict-compose` (a file with `"requests"`), then `pulse api process` or `pulse api compose` with `--json` | `out/<name>.predict.json`, `out/<name>.json` |
| 10 | `pulse export predict --format spss --sanitize-names`, `pulse export spss --sanitize-names`, `pulse export excel --labels wave=wave_names:augment --labels region=region_names:augment`, `pulse import spss`, `pulse cohort inspect` | `out/11_*.json`, `deliver/brand_tracker_2025.sav`, `deliver/brand_tracker_2025.xlsx`, `deliver/roundtrip.pulse` |
| 10 (check) | `pulse api process -r req/12_roundtrip_check.json --json` (the Step 2 request pointed at the re-imported `.sav`) | `out/12_roundtrip_check.json` |

`run.sh` exports `PULSE_LABEL_TABLES_DIR=./labels`. Steps 2 and 10 need it, because they bind the `wave_names` and `region_names` tables.

To run one step by hand from the example directory:

```sh
export PULSE_LABEL_TABLES_DIR=./labels
/tmp/pulse api process -r req/02_funnel_by_wave.json --json
/tmp/pulse api compose -r req/06_q4_banner.json --json
```

The expected end state:

- Every response has `"errors": []` and `"warnings": []`.
- Every predict says `"valid": true`.
- The SPSS export reports two expected warnings: `PULSE_SPSS_SIDECAR_ABSENT` and `PULSE_SPSS_NAME_SANITIZED`.
- The round-trip predict lists `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warnings for the eight re-imported indicator variables, which come back without a variable label.

## Expected key figures

`verify.sh` reads each row of `expected.tsv`, evaluates the jq path against the named output and compares the result after rounding to 3 decimals. These were the values on the reference run:

| Step | Figure | Expected |
|---|---|---|
| 0 | Rows synthesised | 6000 |
| 1 | Q4 share aged 18–34, unweighted → weighted | 0.593 → 0.301 |
| 1 | Weighted share aged 55+ (all waves; census 0.38) | 0.380 |
| 1 | Q4 Kish effective sample size (of 1,481 interviews) | 925.788 |
| 2 | Q4 unaided awareness, unweighted / weighted | 0.383 / 0.288 |
| 3 | Q1 vs Q4 unaided p, unweighted / weighted | 0.001 (0.0006) / 0.867 |
| 4 | NPS unweighted / weighted | +5.604 / −3.176 |
| 5 | Consideration Q3 vs Q4, Holm p (m = 30) | 0.012 |
| 5 | Aided awareness Q3 vs Q4, raw / Holm p | 0.046 / 1 |
| 6 | Q4 top-2-box Midwest vs West, raw / Holm p (m = 50) | 0.046 / 1 |
| 7 | Q4 Trustworthy Midwest vs South, raw / Holm / BH p (m = 63) | 0.003 / 0.172 / 0.031 |
| 8 | Eco-friendly association, Q4 (weighted, aware base) | 0.236 |
| 9 | Eco-friendly Q3 vs Q4, Holm p | 0.034 |
| 10 | Cases written to the `.sav` | 6000 |
| 10 | Q4 weighted unaided awareness read back from the `.sav` | 0.288 |

All of the figures quoted in `README.md` come from the `out/*.json` files of this run.

## Determinism

The synth spec has no `--fit-models` or `--fit-shape` capture, so with `--seed 2025` the cohort is byte-identical between runs. On the reference run the MD5 of `brand_tracker.pulse` was `a7633e74756237042e0663a4ac6d139e` (linux/amd64). If a different architecture produces a different hash, `verify.sh` is the real check.
