# Pulse Examples

Runnable JSON request files organized by topic. Every example resolves
its cohort from the shared fixture set under `fixtures/` so adding a new
example is just authoring JSON — no new CSV, schema, or build step.

## Layout

```
examples/
├── README.md                  # this file
├── fixtures/                  # shared CSVs + schemas + generator + build script
│   ├── gen.go                 # deterministic CSV generator (run once)
│   ├── build.sh               # CSV -> .pulse importer (writes ./.data)
│   ├── *.csv                  # checked-in source data
│   └── schemas/*.json         # hand-tuned schemas with descriptions
├── attributes/                # ATTR_* per-record derivations (6)
├── features/                  # FEAT_* feature engineering (10)
├── filterers/                 # FILTER_* row-level predicates (6)
├── groupers/                  # GROUP_* partitioning (7)
├── windows/                   # WIN_* per-row analytics (10)
├── aggregations/              # AGG_* requests over the all_types cohort (5)
├── tests/                     # TEST_* tier-1 and tier-2 statistical tests (10)
└── <new-category>/            # each: README.md + run-all.sh + *.json
```

## One-time setup

Build the binary, generate fixtures, import to `.pulse`:

```
make build
./examples/fixtures/build.sh
```

That writes `transactions.pulse`, `customers.pulse`, `orders.pulse`,
`training_data.pulse`, `all_types.pulse`, and `experiment.pulse` into
`.data/` at the repo root. Every example request has
`cohort.data_dir = ".data"` so the requests run unmodified.

## Available cohorts

| File | Fields |
|---|---|
| `transactions.pulse` | `id` (u32), `amount` (f64) |
| `customers.pulse` | `id` (u32), `age` (u8), `income` (f64), `region` (categorical), `city` (categorical) |
| `orders.pulse` | `id` (u32), `order_date` (date), `revenue` (f64) |
| `training_data.pulse` | `id` (u32), `region`/`occupation`/`category` (categorical), `label` (u8 binary), `price`/`income` (f64), `signup_date` (date) |
| `all_types.pulse` | One column for every one of the 19 supported types (u8/u16/u32/u64, f32/f64, nullable_bool/u4/u8/u16, date, packed_bool, categorical_u8/u16/u32, decimal128, nullable_decimal128, point_f64, h3_cell). Hand-curated 5-row CSV — exists so a single fixture exercises every type-aware code path. |
| `experiment.pulse` | A/B test cohort: `id` (u32), `treatment`/`region`/`segment`/`converted` (categorical), `revenue`/`session_minutes` (f64), `period` (date). Planted effects across treatment, region, segment, and time so every `TEST_*` example produces a meaningful, non-trivial result. |

The generator (`fixtures/gen.go`) is deterministic — re-running with the
same seed produces byte-identical CSVs. Re-generate from scratch:

```
go run examples/fixtures/gen.go
./examples/fixtures/build.sh
```

## Running

A category-specific runner exercises everything in its folder:

```
./examples/attributes/run-all.sh
./examples/features/run-all.sh
./examples/filterers/run-all.sh
./examples/groupers/run-all.sh
./examples/windows/run-all.sh
./examples/tests/run-all.sh
```

Or invoke individual requests:

```
bin/pulse api predict --request examples/features/01_log_transform.json --json
bin/pulse api process --request examples/features/01_log_transform.json --json
```

## Adding a new example category

1. Pick a category name (e.g. `windows`, `aggregation`, `attributes`).
2. Create `examples/<category>/` with a `README.md` and request `*.json` files.
3. Each JSON sets `cohort.data_dir = ".data"` and references one of the
   shared cohort filenames above.
4. If your category needs a field that's not in any shared cohort, add
   it to `fixtures/gen.go` and the matching schema in
   `fixtures/schemas/`. Re-run the generator and check in the new CSV.
5. Drop in a `run-all.sh` for the category (copy from `features/` and
   adjust the glob).
6. Optionally add an end-to-end test in `examples_test.go` mirroring
   `TestFeatureExamples_RunEndToEnd`.

## CI coverage

`TestExamples_RunEndToEnd` (in `examples_test.go`) builds the shared
fixtures into a temp directory and runs every example across every
category through `pulse.Process`, asserting no errors. Adding a new
category is one line in the test's `categories` slice.

```
go test -run TestExamples -v .
```

Currently 54 sub-tests across seven categories.
