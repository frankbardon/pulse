# Pulse Examples

Runnable JSON request files organized by topic. Every example resolves
its cohort from the shared fixture set under `fixtures/` so adding a new
example is just authoring JSON — no new CSV, schema, or build step.

## Layout

```
internal/examples/
├── README.md                  # this file
├── fixtures/                  # shared CSVs + schemas + generator + build script
│   ├── gen.go                 # deterministic CSV generator (run once)
│   ├── build.sh               # CSV -> .pulse importer (writes ./.data)
│   ├── *.csv                  # checked-in source data
│   └── schemas/*.json         # hand-tuned schemas with descriptions
├── aggregations/              # AGG_* aggregations, incl. vector aggregators (21)
├── attributes/                # ATTR_* per-record derivations (23)
├── compose/                   # Compose batches: sweeps, fits, populations, the MMM decay sweep (8)
├── crosstab/                  # Crosstab tables, margins and inference (17)
├── facet/                     # Facet requests (3)
├── features/                  # FEAT_* feature engineering (12)
├── filterers/                 # FILTER_* row-level predicates (9)
├── groupers/                  # GROUP_* partitioning (11)
├── matrices/                  # MAT_* matrix operators over virtual vectors (14)
├── overlays/                  # OVERLAY_* layers on Request / Compose / Facet hosts (48)
├── regression/                # REG_* regressions (18)
├── synth/                     # synth specs (6)
├── tests/                     # TEST_* tier-1 and tier-2 statistical tests (37)
├── windows/                   # WIN_* per-row analytics (12)
└── <new-category>/            # each: README.md + run-all.sh + *.json
```

## One-time setup

Build the binary, generate fixtures, import to `.pulse`:

```
make build
./internal/examples/fixtures/build.sh
```

That writes `transactions.pulse`, `customers.pulse`, `orders.pulse`,
`training_data.pulse`, `all_types.pulse`, `experiment.pulse`,
`repeated_measures.pulse`, `card_issuers.pulse`, `tier_migrations.pulse`,
`survey.pulse`, `subscribers.pulse`, `warehouse_orders.pulse`, `plan_history.pulse`, `plan_history_wide.pulse`, `battery.pulse`, `intake.pulse` and `mmm_weekly.pulse` into `.data/` at the repo root, plus the point-lookup
index on `experiment.pulse` (`id`). Every example request has
`cohort.data_dir = ".data"` so the requests run unmodified.

## Available cohorts

| File | Fields |
|---|---|
| `transactions.pulse` | `id` (u32), `amount` (f64) |
| `customers.pulse` | `id` (u32), `age` (u8), `income` (f64), `region` (categorical), `city` (categorical) |
| `orders.pulse` | `id` (u32), `order_date` (date), `revenue` (f64), `ordered_at` (datetime) |
| `training_data.pulse` | `id` (u32), `region`/`occupation`/`category` (categorical), `label` (u8 binary), `price`/`income` (f64), `signup_date` (date) |
| `all_types.pulse` | One column for every one of the 17 supported types (u8/u16/u32/u64, f32/f64, nullable_bool/u4/u8/u16, date, packed_bool, categorical_u8/u16/u32, decimal128, nullable_decimal128). Hand-curated 5-row CSV — exists so a single fixture exercises every type-aware code path. |
| `experiment.pulse` | A/B test cohort: `id` (u32), `treatment`/`region`/`segment`/`converted` (categorical), `revenue`/`session_minutes` (f64), `period` (date). Planted effects across treatment, region, segment, and time so every `TEST_*` example produces a meaningful, non-trivial result. |
| `survey.pulse` | A six-item rating battery: `id` (u32), `panel` (categorical, `a` / `b`), `q1`-`q6` (f64). Two correlated factors (q1-q3 service, q4-q6 value; q3 / q6 cross-load) for factor-analysis, rotation, vector-fit and population examples. |
| `subscribers.pulse` | 48 subscriber accounts with binary outcomes stored as flags: `id` (u32), `arm` / `plan` (categorical), `converted` / `churned` / `defaulted` (packed_bool, read as levels `"0"` / `"1"`), `late_payment` (u8 0/1), `signup_date` / `first_repeat_order` (date), `revenue` / `cost` (decimal128, cents). Every value is a closed-form function of the row index (`fixtures/gen.go`), so the flag tests, the logistic fit, calendar retention and exact money examples have hand-derivable answers (`showcase_flags_money_test.go`). |
| `warehouse_orders.pulse` | 8 orders across four warehouses: `id` (u32), `warehouse_tz` (categorical, IANA zone names), `ordered_at` (datetime, UTC instants straddling local midnights), `revenue` (f64). The per-row-zone example (`groupers/13_date_per_row_zone.json`) reads `warehouse_tz` through `time_zone_field`. |
| `plan_history.pulse` | A long panel of 15 customers at three month-end snapshots (45 rows, not sorted by customer): `id` (u32), `customer_id` (u32), `seen_at` (date), `plan` (categorical, `basic` / `plus` / `pro`). Hand-curated; `attributes/24_previous_state_transitions.json` reads each row's previous plan (`ATTR_PREVIOUS_STATE`) for a from-plan × to-plan crosstab. |
| `plan_history_wide.pulse` | The wide twin of `plan_history`: one row per move between two consecutive snapshots (30 rows): `id` (u32), `customer_id` (u32), `moved_at` (date, the later snapshot), `plan_before` / `plan_after` (categorical). `overlays/55_crosstab_previous_state_flows.json` runs Markov, symmetry and retention on the long panel; `TestExamples_PreviousStateFlowsMatchWideTwin` holds its answer to the same request over this file. |
| `battery.pulse` | A one-trait engagement battery (300 respondents): `id` (u32), `bat_energy` / `bat_proud` / `bat_absorbed` / `bat_recommend` / `bat_time_flies` (u8, 1-5, positively keyed) and `bat_dread` (u8, 1-5, reverse-keyed). Every item loads on one latent trait (`fixtures/gen.go`), so the reliability example's omega is defined and the PCA example has one dominant component (`TestExamples_BatteryMeasuresOneTrait`). |
| `intake.pulse` | 300 sign-up intake forms, 100 per channel, with planted nulls: `id` (u32), `channel` (categorical, `web` / `phone` / `paper`), `age` (u8, **nullable**, blank on a few forms in every channel) and `income` (f64, **nullable**, blank far more often on paper than on web). The one fixture with real nulls: a blank CSV cell in a nullable numeric column imports as a null in the per-record bitmap, never as zero, so the null-count and null-filter examples report non-zero, channel-dependent gaps (`TestExamples_IntakeNullsArePlanted`). To plant nulls in a new fixture, declare the field `"nullable": true` in its schema and write an empty cell (`fixtures/gen.go` `writeIntake`, which draws from its own seeded source so every other CSV stays byte-identical). |
| `mmm_weekly.pulse` | A weekly media-mix series, two markets x 104 weeks (208 rows): `market` (categorical, `east` / `west`), `week` (u16), `tv` / `radio` / `search` / `social` spend, `price` / `promo` controls and `sales` (f64). Sales carry a TRUE geometric adstock decay of 0.5 on every channel plus a Hill saturation, so `compose/08_mmm_decay_sweep.json` ranks decay 0.5 first (`TestExamples_MMMDecaySweepRanksTrueDecayFirst`). Rows come from `internal/mmmfixture` (`fixtures/gen.go` `writeMMMWeekly`), the one source the acceptance suites share. |

The generator (`fixtures/gen.go`) is deterministic — re-running with the
same seed produces byte-identical CSVs. Re-generate from scratch:

```
go run internal/examples/fixtures/gen.go
./internal/examples/fixtures/build.sh
```

## Running

A category-specific runner exercises everything in its folder:

```
./internal/examples/attributes/run-all.sh
./internal/examples/features/run-all.sh
./internal/examples/filterers/run-all.sh
./internal/examples/groupers/run-all.sh
./internal/examples/windows/run-all.sh
./internal/examples/tests/run-all.sh
```

Or invoke individual requests:

```
bin/pulse api predict --request internal/examples/features/01_log_transform.json --json
bin/pulse api process --request internal/examples/features/01_log_transform.json --json
```

## The `_meta` block

Every example JSON carries a top-level `_meta` object; it is stripped
before the body is served or run (`examples.Get`), and request decoding
ignores it.

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | unique example ID (`pulse_examples_get name=...`) |
| `category` | yes | must equal the parent directory |
| `description` | yes | one-sentence summary served by search |
| `tags` | yes | drawn from `CanonicalTags` |
| `operators` | yes (may be `[]`) | exactly the `"type": "AGG_/ATTR_/FILTER_/GROUP_/WIN_/FEAT_/TEST_/REG_..."` values in the body (`TestExamples_OperatorsMatchBody`); overlay kinds ride `overlays[].kind` instead |
| `intents` | expected | a LIST of intent-taxonomy IDs (`TestExamples_IntentsFromTaxonomy`), even for one intent: `["describe"]` |
| `capabilities` | optional | non-operator feature names in feature-profile spelling (`capability:stream`), for capabilities the body carries no structural signal for |
| `question` | story only | the plain-language question the example answers; declared together with `interpretation` |
| `interpretation` | story only | a qualitative reading of the result — no digit `0`–`9`; the figures live in the story's golden |

`capabilities` exists so an example is joined to every capability it
needs by an explicit edge in the discovery graph rather than by a scan of
its prose. The graph builder (`internal/descriptor/ontology_examples.go`)
already DETECTS `capability:crosstab` (a `crosstab` key), `capability:compose`
(a root `requests`), `capability:process_chain` (a root `stages`),
`capability:joins` (a `joins` key) and `capability:facet` (category or tag
`facet`) from the body, plus every `overlays[].kind`; declare the rest —
`capability:stream`, `watch`, `shard`, `index`, `sample`, `labels`,
`range_tables`, `filter_to_file` — in `capabilities`. An example with an
empty `operators` list must declare at least one capability. Gates
(`internal/descriptor`): `TestExamples_CapabilitiesFromFeatures` (every
value is a known non-operator feature), `TestExamples_EdgeCoverage` (every
operator a body or description names is an edge; a declared capability
never contradicts its detector), and
`TestExamples_EmptyOperatorExamplesDeclareCapabilities`.

## Story examples

An example is a **story** iff its `_meta` declares both `question` and
`interpretation`; one without the other is refused. A story's request
sets `"return": {"precision": 4}` (`examples.StoryPrecision`, four
significant digits), so a float's last few ulps — which differ between
amd64 and arm64 — never reach its output. Rules: `examples.StoryProblems`,
gated by `TestStories_LibraryHoldsTheRules`. They bind the shipped
library only: an embedder example (`Extensions.Examples`) may declare
`question` / `interpretation`, and `pulse.New` serves them as declared
without applying these rules.

Each story's response is pinned by a harness-produced golden,
`testdata/stories/<name>.json` — the response exactly as
`json.MarshalIndent` writes it. The root package's
`TestExamples_StoryGoldens` builds the fixture cohorts, runs every story
and fails on a missing, extra or differing golden. Never hand-edit one;
regenerate after changing a story or a fixture:

```sh
go test -count=1 . -run TestExamples_StoryGoldens -update
```

The goldens are embedded: `examples.StoryGolden(name)` returns one, so
the generated docs quote real figures without executing a request.

## Adding a new example category

1. Pick a category name (e.g. `windows`, `aggregation`, `attributes`).
2. Create `internal/examples/<category>/` with a `README.md` and request `*.json` files.
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

One sub-test runs per example file, grouped by category.
