# Materializing Derived Columns

A derived column — a window, a feature output, an attribute — exists
only inside the request that computes it. Regressions, groupers,
filters, joins and tests read cohort FIELDS, so none of them can name
one. `Pulse.Materialize` writes the derived columns as ordinary fields
of a NEW cohort; the next request reads them like any other field.

## Library

`Pulse.Materialize(ctx, req, out, opts)` turns a request's derived
columns into ordinary fields of a NEW single-file cohort, so later
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
  weight and `time_zone`. Any other slot is refused with
  `PULSE_MATERIALIZE_SLOT_UNSUPPORTED` (`details.slot`); a request with
  no feature, attribute or window with `PULSE_MATERIALIZE_NO_DERIVED`;
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
- **Writing.** Always an ungrouped (`0x01`) single file, written
  atomically through `NewCohortBuilder`. The source's SPSS metadata
  sidecar is carried — rewritten with a plain numeric variable per
  derived column (a previous-state column copies its state variable's
  codes and value labels) and re-fingerprinted, so value labels survive to
  `pulse export spss`; a stale or unreadable one is dropped with a warning
  carrying its own code. No other sidecar is carried. An existing target needs `Overwrite`; replacing one reports
  its stale sidecars in `InvalidatedSidecars` plus a
  `PULSE_MATERIALIZE_SIDECARS_INVALIDATED` warning. `Strict` escalates
  a weak description on the output schema (the generated ones always
  pass; an undescribed source field does not). A limit breach, a
  refusal or a write failure leaves nothing at the target.

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

## CLI

```sh
pulse api materialize -r req.json -o weekly_derived.pulse [--overwrite] [--strict] [--json]
```

`req.json` is an ordinary request body. `--json` wraps the
`MaterializeResult` (`target`, `records`, `derived_columns`,
`warnings`, `invalidated_sidecars`) in the standard envelope and lifts
its warnings onto the envelope's `warnings`; a refusal keeps its own
code in `errors[0].code` (`pulse errors lookup CODE`). The leaf is a
thin adapter over `Pulse.Materialize`.

## MCP

`pulse_materialize` takes `{request, out, overwrite}` — the request
NESTED under `request`, `out` relative to the data dir — and returns
the same result object. A request slot spread beside `out` (or a
misspelled argument) is `PULSE_REQUEST_UNKNOWN_FIELD` with a
suggestion; an unknown key inside `request` is refused as
`pulse_process` refuses it. Description-quality strict mode is CLI and
library only.

## Feature profiles

`capability:materialize` (which needs `capability:process`) gates the
tool and the CLI leaf; a profile without it does not mount
`pulse_materialize` and lists no `materialize` command. The library
method itself is never hidden — an embedder calling it holds the
instance — but the instance's operator set still applies: a hidden
operator in the request is refused exactly as an unknown one.
