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
├── features/                  # FEAT_* feature engineering examples
│   ├── README.md
│   ├── run-all.sh             # smoke-runs every JSON in this folder
│   └── *.json                 # request files
└── <new-category>/            # follow the same pattern
```

## One-time setup

Build the binary, generate fixtures, import to `.pulse`:

```
make build
./examples/fixtures/build.sh
```

That writes `transactions.pulse`, `customers.pulse`, `orders.pulse`,
and `training_data.pulse` into `.data/` at the repo root. Every example
request has `cohort.data_dir = ".data"` so the requests run unmodified.

## Available cohorts

| File | Fields |
|---|---|
| `transactions.pulse` | `id` (u32), `amount` (f64) |
| `customers.pulse` | `id` (u32), `age` (u8), `income` (f64), `region` (categorical), `city` (categorical) |
| `orders.pulse` | `id` (u32), `order_date` (date), `revenue` (f64) |
| `training_data.pulse` | `id` (u32), `region`/`occupation`/`category` (categorical), `label` (u8 binary), `price`/`income` (f64), `signup_date` (date) |

The generator (`fixtures/gen.go`) is deterministic — re-running with the
same seed produces byte-identical CSVs. Re-generate from scratch:

```
go run examples/fixtures/gen.go
./examples/fixtures/build.sh
```

## Running

A category-specific runner exercises everything in its folder:

```
./examples/features/run-all.sh
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

`TestFeatureExamples_RunEndToEnd` (in `examples_test.go`) builds the
shared fixtures into a temp directory and runs every feature-pack
example through `pulse.Process`, asserting no errors. Add similar tests
for new categories as they land so example bitrot fails CI rather than
the user's first run.

```
go test -run TestFeatureExamples -v .
```
