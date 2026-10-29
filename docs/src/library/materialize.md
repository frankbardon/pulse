# Materializing Derived Columns

A derived column — a window, a feature output, an attribute — exists
only inside the request that computes it. Regressions, groupers,
filters, joins and tests read cohort FIELDS, so none of them can name
one. `Pulse.Materialize` writes the derived columns as ordinary fields
of a NEW cohort; the next request reads them like any other field.

## Library

`Pulse.Materialize(ctx, req, out, opts)` turns a request's derived
columns into ordinary fields of a NEW cohort (a single file, or a shard
archive under `output.shards`), so later
requests can filter, group or join on them:

```go
res, err := p.Materialize(ctx, &pulse.Request{
    Cohort:   &types.Cohort{Filename: "sales.pulse"},
    Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}},
    Windows: []*types.Window{{Type: types.WIN_LAG, Field: "spend",
        PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "week"}}}},
}, "sales_derived.pulse", pulse.MaterializeOptions{})
// res.Records, res.DerivedColumns ("LOG_spend", "WIN_LAG_spend")
```

- **Accepted slots.** Features, filterers, attributes, windows, `sort`,
  weight, `time_zone` and `output` (below). Any other slot is refused with
  `PULSE_MATERIALIZE_SLOT_UNSUPPORTED` (`details.slot`); a request with
  no feature, attribute or window — and no `output` — with
  `PULSE_MATERIALIZE_NO_DERIVED`;
  an embedder-registered feature (its outputs are unknown before it
  runs) with `PULSE_MATERIALIZE_OUTPUT_UNDECLARED`. Process's own
  pre-flight runs first, so a request `Process` refuses is refused with
  the same error. Every refusal happens before anything is created.
- **Rows.** One per filter-passing source record, in source order (an
  archive's shards concatenated in archive order). Features see every
  record, attributes and windows the survivors — exactly as `Process`
  computes them; `ATTR_PREVIOUS_STATE` also sees every record (it runs
  before the filters). With `sort`, the rows follow the order `Request.Sort`
  gives `Process`'s row set — the engine's own comparator, ties in
  source order, nulls last in both directions — on keys naming source
  fields or derived columns (feature outputs, attribute and window
  labels). Sorting moves rows, never values: every window has already
  run under its own `partition_by` / `order_by`. Only the permutation
  and each survivor's source index are held beside the buffered
  records, and the memory pre-flight is `Process`'s
  (`PULSE_LIMIT_EXCEEDED` before anything is created). Source cells are the exact stored values
  (`CohortReader.RecordAt`).
- **Schema.** The source's logical schema verbatim (order, types,
  nullability, dictionaries, descriptions), then one nullable `f64` per
  derived output in pipeline order — features (every output of a
  multi-output feature), attributes, windows — except an
  `ATTR_PREVIOUS_STATE` output, which is written as its state field's
  own type (`categorical_*` or `packed_bool`) and dictionary, nullable
  (a null rides the bitmap), so it reads back as the same labels and a
  crosstab on the derived cohort equals the in-request one; each with a generated
  description such as `WIN_LAG of spend (partition region; order week)
  — derived from sales.pulse`. A window's frame is stated only when the
  operator reads it: `WIN_EWMA` is cumulative whatever its required
  frame says (manifest `ignores_frame`), so its description has no
  frame clause. An operator whose input is the slot's `expression`
  (manifest `reads_expression`) is described by that expression,
  verbatim — `ATTR_FORMULA = tv_spend_k * 0.80866 — derived from
  panel.pulse` — and a regression attribute names its `penalty` /
  `alpha` / `l1_ratio`. Every description is capped at 1,000 bytes,
  cut on a rune boundary with an ellipsis. Integer window outputs (row number,
  rank) are stored as `f64`; a non-numeric value (a lag copying a
  categorical label) fails the run with
  `PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC`.
- **Writing.** An ungrouped single file — `0x01`, or `0x02` when
  it keeps a `vec_*` field, whose dim, kind, scale and labels follow it
  to its output position — unless the `output` slot's container keys
  ask for a shard archive, constant elision or parent groups (below) — written
  atomically through `NewCohortBuilder`. The source's SPSS metadata
  sidecar is carried — rewritten with a plain numeric variable per
  derived column (a previous-state column copies its state variable's
  codes and value labels, each category's `observed` flag recomputed
  from the rows written) and re-fingerprinted, so value labels survive to
  `pulse export spss`; a stale or unreadable one is dropped with a warning
  carrying its own code. No other sidecar is carried. An existing target needs `Overwrite`; replacing one reports
  its stale sidecars in `InvalidatedSidecars` plus a
  `PULSE_MATERIALIZE_SIDECARS_INVALIDATED` warning. `Strict` escalates
  a weak description on the output schema (the generated ones always
  pass; an undescribed source field does not). A limit breach, a
  refusal or a write failure leaves nothing at the target.

## Shaping the output: the `output` slot

`Request.Output` (`types.MaterializeOutput`, JSON `output`) lays out the
cohort a materialize run writes. It is materialize-only: `Process`,
`Compose`, `ProcessChain`, `Predict` and their stream, CLI and MCP
surfaces refuse a request carrying it with
`PULSE_MATERIALIZE_OUTPUT_ONLY` (`details.slot`; a Compose slot, fit or
chain stage is located by `details.request` / `fit` / `stage`).

```go
desc, required := "Customer key", false
res, err := p.Materialize(ctx, &pulse.Request{
    Cohort:    &types.Cohort{Filename: "sales.pulse"},
    Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}},
    Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
        {From: "customer_id", As: "key", Description: &desc, Nullable: &required},
        {From: "spend"},
    }},
}, "north_customers.pulse", pulse.MaterializeOptions{})
```

- **`columns` XOR `exclude`.** `columns` is the closed list — exactly
  these columns, in list order; `exclude` drops the named columns and
  keeps every other in source-then-derived order. Neither keeps every
  column. Both, an empty `columns` list, an `exclude` that drops every
  column or an entry without `from` is `PULSE_MATERIALIZE_OUTPUT_INVALID`
  (`details.reason`: `columns_and_exclude`, `no_columns`,
  `from_missing`; `details.path`).
- **`from` / `as`.** `from` names a source field or a derived column's
  label; an unknown name is `PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN`
  (`details.column`, `path`, and `suggestion` when a name is close).
  `as` renames; two columns written under one name is
  `PULSE_MATERIALIZE_OUTPUT_DUPLICATE` (`details.column`, `path`,
  `first`). The same `from` under two `as` names duplicates the column.
- **Inheritance.** A key left out inherits: a source field keeps its
  type, nullability, dictionary, decimal shape and description; a
  derived column stays a nullable `f64` (a previous-state column its
  state field's type) with its generated description. `description`
  replaces the description (`""` clears it); `nullable` overrides the
  nullability. `nullable: false` on a column that holds a null at run
  time fails the run with `PULSE_MATERIALIZE_OUTPUT_NULL`
  (`details.column`, `details.row` — the 0-based output row), nothing
  written.
- **Retyping: `type`.** A column's `type` (a field type name) casts it;
  empty or its own type keeps it. Every cast is exact or fails — a
  source integer (a `u64` past 2^53 included) never goes through
  `float64` on its way to an integer or `decimal128`:

  | From → to | Rule |
  |---|---|
  | numeric (`u4`..`u64`, `f32`, `f64`, `decimal128`) → numeric | a fraction to an integer and a value past the range fail; to `f32` / `f64` rounds to nearest (only an overflow to ±Inf fails) |
  | numeric → `packed_bool` | `0` / `1` only; `packed_bool` → numeric is `0` / `1` |
  | numeric → `date` / `datetime` | the epoch count (days / seconds), integral and in range; `date` / `datetime` → numeric is the epoch count |
  | `date` → `datetime` | local midnight in `time_zone` (else `Options.DefaultTimeZone`, else UTC; a day whose midnight DST skips starts at its first instant) |
  | `datetime` → `date` | the local day in that zone, truncating toward the past |
  | `set_*` → a wider `set_*` | the labels and dictionary unchanged |
  | `categorical_*` → `categorical_*` | the labels and the whole dictionary (order, codes, unused entries) unchanged; a narrowing whose dictionary holds more labels than the target (257 into `categorical_u8`) is refused |
  | `categorical_*` → numeric | each label parsed as a number (decimal syntax; exact to an integer or `decimal128`); a label that is not one fails `unparseable` |
  | numeric → `categorical_*` | each value stringified — integers in digits, a float as JSON prints it (`3.0` → `"3"`, `-0` → `"0"`), a `decimal128` at its scale (`"3.50"`) — into a dictionary built in first-seen output order; one distinct value past the rung fails `overflow` |
  | `vec_*` | keeps its type |

  `decimal128` takes `precision` / `scale` (a decimal column's own
  shape by default, else `38` / `0`; a float keeps its shortest printed
  digits, and more fractional digits than `scale` fail). Any other pair
  is `PULSE_MATERIALIZE_CAST_UNSUPPORTED` (`details.column`, `path`,
  `from`, `from_type`, `to_type`, `reason`); an unknown name, a bad
  decimal shape and every `on_cast_error` misuse are
  `PULSE_MATERIALIZE_OUTPUT_INVALID` (`unknown_type`, `decimal_shape`,
  `on_cast_error_invalid`, `on_cast_error_without_type` (no `type`,
  `recode` or `bucket`),
  `on_cast_error_not_nullable`) — all before a record is read, and
  predicted identically.
- **A value that does not fit.** By default it fails the run with
  `PULSE_MATERIALIZE_CAST_FAILED` (`details.column`, `row`, `value`
  rendered exactly — a label verbatim, `reason`: `fractional` /
  `overflow` / `not_finite` / `not_boolean` / `unparseable`), nothing
  written. `on_cast_error: "null"` (on a
  nullable output column only) writes a null instead and warns once
  per column with `PULSE_MATERIALIZE_CAST_NULLED` (`details.count`,
  `first_row`). A cast column's SPSS variable is rebuilt from its new
  type (a categorical: an unlabelled string variable of its labels) —
  no old value label lands on the cast values, a cast previous-state
  column's included.
- **Recoding: `recode`.** On a `categorical_*` column,
  `recode: {map, order}` rewrites the dictionary — never a per-row
  expression. `map` takes a source label to a new label (relabel), many
  labels to one (merge: one entry, at the first one's position) or to
  `null` (the cells written null; the output column must be nullable).
  Unmapped labels keep themselves; the written dictionary is the
  source's in code order with each label replaced, unused entries
  included, unless `order` lists every recoded label once in the order
  wanted (codes follow). Order of operations on one column: `recode`,
  then `type` (the cast reads the recoded labels — a merge can bring a
  257-label column under `categorical_u8`), then the nullability check;
  `as` only names the result. A `map` key the source dictionary does not
  hold is `PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL` (`details.column`,
  `path`, `from`, `label`, `suggestion`) — the drift guard when a stored
  request meets a new source cohort; a recode of a non-categorical, a
  `null` on a non-nullable column, or an `order` naming a label the
  recode does not write, omitting one it does or naming one twice is
  `PULSE_MATERIALIZE_OUTPUT_INVALID` (`recode_not_categorical`,
  `recode_null_not_nullable`, `recode_order_unknown`,
  `recode_order_missing`, `recode_order_duplicate`; `details.label`).
  All are refused before a record is read, and predicted identically.
  `on_cast_error` is accepted on a recoded column (a recode alone never
  fails a value). The column's SPSS variable is rebuilt from the
  recoded dictionary — an unlabelled string variable of the new labels,
  so no old value label lands on a new code.
- **Bucketing: `bucket`.** On a numeric (`u4`..`u64`, `f32`, `f64`,
  `decimal128`, or a derived column) or date-family column,
  `bucket: {ranges, range_table}` writes each value as the label of the
  range holding it. Give exactly one of inline `ranges`
  (`[{label, start, end}]`: JSON numbers on a numeric column, ISO date
  strings on a date-family one; an omitted or `null` bound is open) or
  `range_table`, a registered range table (`Extensions.RangeTables` /
  `PULSE_RANGE_TABLES_DIR`) of the column's kind. Both go through the
  shared range-compilation pass with its rules: a numeric range is
  half-open `[start, end)` and compared exactly (a `u64` past 2^53 or a
  `decimal128` never rounds across a bound); a date range is inclusive on
  both ends; a `datetime` is matched by its local day in the request
  `time_zone` (else `Options.DefaultTimeZone`, else UTC). The written
  type is the narrowest `categorical_*` holding every label, its
  dictionary the labels in DECLARED order (inline order, or the
  table's), unused labels included — inline ranges and a table holding
  the same ranges write identical columns. A null stays null; a value
  outside every range (or a NaN) follows `on_cast_error`
  (`PULSE_MATERIALIZE_CAST_FAILED` / `_NULLED`, `reason` `no_range` /
  `not_finite`, a date rendered as its ISO day). Order of operations:
  `bucket`, then `type` (a categorical width, or a numeric parse of the
  labels), then the nullability check. Refused before a record is read,
  and predicted identically (`details.column` + `path` on each): a
  source neither numeric nor date-family
  (`PULSE_MATERIALIZE_OUTPUT_INVALID`, `bucket_unsupported_type`), both
  or neither source (`PULSE_RANGE_SOURCE_AMBIGUOUS`), an unknown or
  hidden table (`PULSE_RANGE_TABLE_UNKNOWN`), a table of the other kind
  (`PULSE_RANGE_TABLE_KIND_MISMATCH`), and every range the shared pass
  refuses (`PULSE_RANGE_EMPTY` / `_INVALID` — a bound of the wrong JSON
  kind too — / `_DUPLICATE_LABEL` / `_OVERLAP`). Predict lists a risk
  when the ranges leave a gap or the column is a float. The column's
  SPSS variable is rebuilt — an unlabelled string variable of the range
  labels. `bucket` never touches `FEAT_BUCKETIZE`.
- **Projection is last.** Filters, `sort`, weights, features,
  attributes and windows read the full source schema plus the derived
  columns, so they may name a column the output drops.
- **A pure reshape is legal.** With `output` present a request needs no
  feature, attribute or window: it writes the filtered, sorted source
  under the new layout (`res.DerivedColumns` is empty).
- **Sidecars.** The source's SPSS metadata sidecar follows the layout,
  column for column: a dropped column removes its variable (and its
  weight, variable-set membership and any response set naming it), a
  renamed one keeps its codes and value labels under the new name, a
  duplicated one gets a copy of its variable under each extra name, and
  the order follows the output — so a `.sav` export puts every label on
  the right variable. What it cannot describe truthfully (a derived
  registry column kept without a column it reads, two names SPSS folds
  together) writes the cohort without a sidecar, with one
  `PULSE_SPSS_SIDECAR_INVALID` warning — never a stale one.
- **Container: `shards` and `elide_constants`.** `shards: {max_records}`
  writes a shard archive instead of a single file: the rows, in output
  order, cut into consecutive `max_records`-row shards
  (`part-00001.pulse`, …; the last one partial, an empty output one empty
  shard), published by one `CreateShardArchive` call so the archive
  appears whole or not at all. `pulse.Open` reads it as one cohort, rows
  in the single file's order. `res.Shards` names the entries.
  `max_records ≤ 0` is `PULSE_MATERIALIZE_OUTPUT_INVALID` (reason
  `shards_max_records`) before anything is read; a row that would open
  shard 65,536 fails the run with `PULSE_SHARD_COUNT_LIMIT`, nothing
  written. `elide_constants: true` stores every column holding one value
  on every written row once (`res.ElidedConstants` names them) — the file
  is `0x02` when anything is elided, decoding unchanged; with `shards`
  the decision is made once over every row and applied to every shard.
  An `output` holding only these keys keeps every column (a pure copy is
  legal). The SPSS metadata sidecar is carried as for a single file —
  beside the archive, fingerprinted over the archive's bytes (Inspect
  reads its suggested weight; `pulse export` of the archive, every
  format, writes exactly what the single-file output would, a `.sav`
  applying the sidecar; an anchored `archive.pulse#shard.pulse` exports
  that one shard under the canonical schema with the archive's sidecar)
  — and an elided column keeps its variable. `Overwrite`
  replaces an existing cohort or archive whole.
- **Parent groups: `groups`.** With an `output` block, a grouped
  (`0x02`) source keeps its parent groups: `groups` absent INHERITS each
  source group whose every member is written unchanged — a rename is
  fine, the group is declared again under the written names, same key —
  while a member dropped or re-coded (`type`, `recode`, `bucket`) leaves
  its group out with `PULSE_MATERIALIZE_GROUP_NOT_INHERITED`
  (`details.member`, `change`; its members become row fields). A list
  `groups: [{key, members}]` (`types.MaterializeGroup`, the import shape)
  declares exactly those groups over WRITTEN names and replaces
  inheritance; `groups: []` writes none. A bad declaration —
  `PULSE_GROUP_FIELD_UNKNOWN`, `_FIELD_CONFLICT`, `_DECLARATION_INVALID`,
  with `details.path` — is refused before anything is read, predict
  alike; a keyed group whose member varies within its key fails the run
  with `PULSE_GROUP_MEMBER_NOT_CONSTANT`. Every group passes import's
  viability gate over the written rows (archive-wide with `shards`);
  `res.Groups` carries each verdict (`admitted`, `low_ratio`,
  `dropped_too_narrow`). `MaterializeOptions.Strict` makes a group left
  out or dropped, and a low ratio, fatal. A grouped output decodes
  exactly as its ungrouped twin; without an `output` block nothing is
  inherited and the output stays ungrouped, byte for byte.
- **Absent `output`** writes exactly what Materialize always wrote, byte
  for byte. A feature profile without `capability:materialize_output`
  refuses a set `output` as an unknown field everywhere.

## Predict first: `PredictMaterialize`

`p.PredictMaterialize(ctx, req)` validates a Materialize request without
executing it: it reads the source's header and schema only (never a
record — a truncated or record-corrupt source predicts like the intact
one) and writes nothing. It returns the whole envelope, `Data` a
`*descriptor.MaterializePredictResult`:

- `columns` — the output schema the run writes, in output order: each
  column's `name`, `type`, `nullable`, `description`, the column it
  reads (`from`; `derived` when that is a feature, attribute or window
  output), its `dictionary` (truncated to 100 entries, as `Inspect`
  does) and a decimal's `precision` / `scale`;
- `derived_columns` — the run's `MaterializeResult.DerivedColumns`;
- `risks` — the checks only the records can settle, each
  `{column, path, code, check}` with the code the run fails with: a
  cast a stored value can fail (`check` such as `f64 → u8: integral,
  0 ≤ v ≤ 255`; `PULSE_MATERIALIZE_CAST_FAILED`, or
  `PULSE_MATERIALIZE_CAST_NULLED` — the warning — under
  `on_cast_error: "null"`), and a `nullable: false` column over one
  that may hold a null (`PULSE_MATERIALIZE_OUTPUT_NULL`), and a sharded
  output whose source holds more records than `max_records` × 65,535
  (`PULSE_SHARD_COUNT_LIMIT`);
- `container` — `{kind, max_records, max_shards, elide_constants, groups}`:
  `"file"` or `"shard_archive"`; `max_shards` is the most shards the run
  writes, `ceil(source records / max_records)`, reached when every
  record passes the filters; `groups` lists each parent group — `label`,
  `origin` (`inherited` / `declared`), `key`, `members`,
  `source_fields`, `verdict` (`planned`, `dropped_too_narrow`,
  `not_inherited` with `member` / `change`) — and a declared keyed group
  adds a `PULSE_GROUP_MEMBER_NOT_CONSTANT` risk;
- `schema_info` — the source's field names.

Every static refusal is a coded `env.Errors` entry with the run's own
code, message and details (`valid` false): the slot gates,
`PULSE_MATERIALIZE_NO_DERIVED`, `PULSE_MATERIALIZE_OUTPUT_UNDECLARED`,
the `output` block's refusals, an over-long written description
(`PULSE_IMPORT_DESCRIPTION_TOO_LONG`) and Process's own pre-flight and
operator checks. The run's pre-flight makes the same plan call
(`MaterializePlan`), so the two cannot drift; where one request carries
several faults the run stops at its first, predict lists them all. A
weak description of a WRITTEN column is a
`PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warning, as the run warns;
`Options.Strict` promotes every warning, `Options.EchoRequest` echoes
the request on `env.Request`. It reports as a `predict` operation.

## The two-step recipe: a regression on a window

Step 1 materialises the window (only derived columns and filters):

```go
zero := 0 // a cumulative frame: unbounded preceding through the current row
res, err := p.Materialize(ctx, &pulse.Request{
    Cohort: &types.Cohort{Filename: "weekly.pulse"},
    Windows: []*types.Window{{Type: types.WIN_RUNNING_SUM, Field: "x", Label: "cum_x",
        OrderBy: []types.OrderKey{{Field: "week"}},
        Frame:   &types.FrameSpec{Mode: "rows", Following: &zero}}},
}, "weekly_derived.pulse", pulse.MaterializeOptions{})
```

Step 2 points the next request at the output and names the column by
its label:

```go
resp, err := p.Process(ctx, &pulse.Request{
    Cohort: &types.Cohort{Filename: "weekly_derived.pulse"},
    Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y",
        Predictors: []string{"cum_x"}}},
})
```

Label every output a later request references; an unlabelled one takes
the operator's default output name (`res.DerivedColumns` lists each).
The output is a snapshot: re-materialise after the source changes.

## Re-running a reshape over each new cohort

A reshape is a request, so it can be stored as a
[request template](request-templating.md#a-stored-reshape-recipe) with the
cohort as a variable: author the `output` block once, render it per cohort,
predict, then materialize.

```go
req, err := p.RenderTemplateRequest("reshape/handoff", map[string]any{"cohort": "wave2.pulse"})
env, err := p.PredictMaterialize(ctx, req) // the output schema, no record read
res, err := p.Materialize(ctx, req, "wave2_handoff.pulse", pulse.MaterializeOptions{})
```

A `recode` map key the new cohort's dictionary lacks is
`PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL` at predict and at run: a recipe
meeting drifted data fails before it writes. The shipped example
`materialize_reshape_for_handoff` is a ready request.

## CLI

```sh
pulse api materialize -r req.json -o weekly_derived.pulse [--overwrite] [--strict] [--json]
pulse api materialize -r req.json --predict [--strict] [--json]
```

`req.json` is an ordinary request body. `--json` wraps the
`MaterializeResult` (`target`, `records`, `derived_columns`,
`warnings`, `invalidated_sidecars`) in the standard envelope and lifts
its warnings onto the envelope's `warnings`; a refusal keeps its own
code in `errors[0].code` (`pulse errors lookup CODE`). `--predict`
writes `PredictMaterialize`'s envelope as it is (no `-o` needed, nothing
written; `--strict` promotes its warnings) and exits 1 when it carries
an error. The leaf is a thin adapter over `Pulse.Materialize` /
`Pulse.PredictMaterialize`.

## MCP

`pulse_materialize` takes `{request, out, overwrite, predict}` — the
request NESTED under `request`, `out` relative to the data dir — and
returns the same result object. `predict: true` needs no `out`, writes
nothing and answers `{predict: {valid, schema_info, columns,
derived_columns, risks, errors, warnings}}`; a refused request is that
verdict, not a tool error. A request slot spread beside `out` (or a
misspelled argument) is `PULSE_REQUEST_UNKNOWN_FIELD` with a
suggestion; an unknown key inside `request` is refused as
`pulse_process` refuses it. Description-quality strict mode is CLI and
library only.

## Feature profiles

`capability:materialize` (which needs `capability:process`) gates the
tool and the CLI leaf; a profile without it does not mount
`pulse_materialize` and lists no `materialize` command. The library
methods themselves (`Materialize`, `PredictMaterialize`) are never hidden — an embedder calling them holds the
instance — but the instance's operator set still applies: a hidden
operator in the request is refused exactly as an unknown one.
