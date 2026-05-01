# Feature Pack Examples

Runnable JSON requests demonstrating each `FEAT_*` operator and idiomatic
combinations. Each example points at a small fixture cohort built from a
checked-in CSV.

## One-time setup

Build the binary and the four `.pulse` fixture cohorts:

```
make build
./examples/features/fixtures/build.sh
```

That writes `transactions.pulse`, `customers.pulse`, `orders.pulse`, and
`training_data.pulse` into `.data/` under the repo root. Each example's
`cohort.data_dir` already points at `.data`, so the requests run
unmodified.

## Running

Validate (no execution):

```
bin/pulse api predict --request examples/features/01_log_transform.json --json
```

Execute:

```
bin/pulse api process --request examples/features/01_log_transform.json --json
```

Run every example end to end:

```
./examples/features/run-all.sh
```

`--strict` upgrades warnings (including the target leakage gate) to
errors.

## Catalog

| File | Operator(s) | Demonstrates |
|---|---|---|
| `01_log_transform.json` | `FEAT_LOG` | Per-row log1p on a skewed numeric |
| `02_bucketize_quantile.json` | `FEAT_BUCKETIZE` | Equal-frequency deciles |
| `03_bucketize_explicit.json` | `FEAT_BUCKETIZE` | Explicit boundary edges |
| `04_one_hot_filter.json` | `FEAT_ONE_HOT` | Multi-output + downstream filter on derived column |
| `05_date_features_seasonality.json` | `FEAT_DATE_FEATURES` | Calendar decomposition + group-by-quarter |
| `06_frequency_encode.json` | `FEAT_FREQUENCY_ENCODE` | Replace high-cardinality categorical with frequency |
| `07_train_test_split.json` | `FEAT_TRAIN_TEST_SPLIT` | Stratified 70/15/15 partition by category |
| `08_target_encode_safe.json` | `FEAT_TARGET_ENCODE` (safe) | Split-then-encode order, smoothing=5 |
| `09_target_encode_leaky.json` | `FEAT_TARGET_ENCODE` (unsafe) | Triggers `PULSE_FEAT_TARGET_LEAKAGE_RISK` |
| `10_full_ml_pipeline.json` | All eight | Compose the full preprocessing graph |

## Fixture cohorts

CSV sources and matching schemas live under `fixtures/`. The generator
(`fixtures/gen.go`) is deterministic — re-running it produces identical
output. Re-generate from scratch:

```
go run examples/features/fixtures/gen.go
./examples/features/fixtures/build.sh
```

Schemas are hand-tuned for description quality and explicit type
choices (`u32` ids, `categorical_u8` for low-cardinality strings,
`date` for calendar fields).

## Required fields per example

| File | Cohort fields used |
|---|---|
| 01 | numeric `amount` |
| 02 | numeric `income`, identifier `id` |
| 03 | numeric `age`, numeric `income`, identifier `id` |
| 04 | categorical `region`, identifier `id` |
| 05 | date `order_date`, numeric `revenue`, identifier `id` |
| 06 | categorical `city` |
| 07 | categorical `category`, identifier `id` |
| 08 | categorical `category`, numeric `price`, identifier `id` |
| 09 | categorical `category`, numeric `price` |
| 10 | categorical `region`/`occupation`/`category`, numeric `income`/`label`, date `signup_date`, identifier `id` |

If you adapt to a different cohort, run `pulse cohort inspect <file>.pulse --json`
to see the actual schema and edit the request `field` values to match.

## Leakage gate demo

```
bin/pulse api predict --request examples/features/09_target_encode_leaky.json --json
# warning: PULSE_FEAT_TARGET_LEAKAGE_RISK

bin/pulse api predict --request examples/features/09_target_encode_leaky.json --json --strict
# error: PULSE_FEAT_TARGET_LEAKAGE_RISK
```

Reorder so `FEAT_TRAIN_TEST_SPLIT` precedes `FEAT_TARGET_ENCODE`
(see `08_target_encode_safe.json`) and the gate goes silent.

## Default output column names

| Operator | Default label |
|---|---|
| `FEAT_LOG` | `LOG_<field>` |
| `FEAT_SQRT` | `SQRT_<field>` |
| `FEAT_BUCKETIZE` | `BUCKET_<field>` |
| `FEAT_FREQUENCY_ENCODE` | `FREQ_<field>` |
| `FEAT_TARGET_ENCODE` | `TARGET_<field>` |
| `FEAT_TRAIN_TEST_SPLIT` | `split` |
| `FEAT_ONE_HOT` | `<field>_<category>` (per category) |
| `FEAT_DATE_FEATURES` | `<field>_year`, `<field>_month`, `<field>_day`, `<field>_dow`, `<field>_quarter` |

Set `label` to override single-output names or change the prefix on
multi-output operators.

## CI coverage

`TestFeatureExamples_RunEndToEnd` (in `examples_test.go`) builds the
fixtures into a temp directory and runs every example through
`pulse.Process`, asserting no errors. Added to catch example bitrot
(schema drift) and operator regressions.

```
go test -run TestFeatureExamples -v .
```

See `pulse skills show feature-engineering` for the full operator
contract and the leakage-trap rationale.
