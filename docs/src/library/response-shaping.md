# Response Shaping

A `return` block on a request chooses which parts of the response come
back and how many significant digits floats carry on the wire. It exists
to keep responses small for callers (agents especially) that read a
fraction of them. A request with no `return` is byte-identical to before,
hashes identically, and `format_version` stays `"1.1"`.

```go
resp, err := p.Process(ctx, &types.Request{
    // Cohort, Aggregations, ... as for any request.
    Return: &types.Return{
        Preset:    types.ReturnPresetMinimal,
        Include:   []string{"metadata.total_rows"},
        Precision: 4,
    },
})
```

## Resolution

Preset, then `include` adds, then `exclude` removes; exclude wins. An
`include` with no preset starts from an empty base. `exclude` or
`precision` alone shape the full response. Top-level `warnings` and every
nested `warnings` whose parent is emitted stay unless excluded.

Presets are `full` (identity), `standard` (data, warnings, metadata,
trimmed tests and regressions, no components) and `minimal` (the primary
result only). The manifest `return_presets` block lists each preset's
paths expanded against the instance.

Paths use the Response JSON names at any depth: `.` between keys, `[*]`
into array elements, a trailing `*` on a map-key segment as a prefix
glob. `data[*].<column>` selects output columns. The full grammar and
the preset table are in [Payload JSON Schema](../contract/payload-schema.md)
(Return slot).

## What the caller sees

- An excluded part is **absent** on the wire, never `null`, and zero or
  nil on the Go value.
- The shaped response is a **projection**: it is not schema-valid
  against the full payload schema. Do not validate it against
  `pulse://schema`.
- A response whose plan changes something carries
  `returned {preset, digest, precision?}`. It cannot be excluded and its
  `digest` equals the one predict returns.
- `precision` (1 to 17 significant digits) is wire-only. Integers,
  `decimal128` strings and NaN (`null`) are untouched, and counts stay
  exact: count-aggregation columns, count crosstab cells, and
  `matrices[*].auxiliary.n`. Extension aggregators are never exempt.

## Errors

| Code | When |
|---|---|
| `PULSE_RETURN_INVALID` | unknown preset, precision outside 1 to 17, malformed path, naming `returned`, a non-overlay path in a Compose-level block |
| `PULSE_RETURN_PATH_UNKNOWN` | a path this instance's Response does not carry, including a hidden feature's path |
| `PULSE_RETURN_PATH_UNMATCHED` | warning, buffered runs only: an include through an open map matched nothing |

`PredictResult.Return` resolves and validates the block before any
record is read.

## Defaults and precedence

`Options.DefaultReturn`, else the feature profile's `return`, else
`full`, applies to every request that has no block. A request block
replaces the default entirely. See [`pulse.New` & Options](options.md)
and [Feature Profiles](feature-profiles.md).

The MCP tools add one layer on top: a request without a block is shaped
by `gosdk.Config.DefaultReturn` / `mcpserve.Options.DefaultReturn` /
`pulse mcp --return`, else the instance default above, else the
built-in `standard` preset — never `full`. A request block still wins;
`full` on the host restores the unshaped output. See
[`pulse mcp --return`](../cli/mcp.md#--return).

`disable_components` is shorthand: alone it only skips computing
components (byte-identical, no marker); with a `return` layer it adds
`exclude: ["components"]`. An engine `DisableComponents` sticks: a
request `return` never re-opens it, so components are not computed and
the block gains `exclude: ["components"]`. Only an explicit request
`disable_components: false` turns them back on.

## Excluded parts are not computed

An excluded part is skipped at execution, not only on the wire: the
run does less work, and a kept figure never changes. An excluded
overlay layer, test or post-test runs no handler, and an excluded
`regressions` slot fits nothing, so none raises its refusals or
warnings (a rank-deficient fit, a low effective sample size);
`Predict` still validates them. A layer, test or post-test that
belongs to a `multiplicity` family is computed anyway, so every kept
`p_adjusted` matches the full run. The execution path is chosen from
the full request: excluding a test never moves a request onto the
parallel or shard path, and a chain stage carrying tests is still
refused.

## Surfaces

- `Process`: shapes the one response.
- `Compose`: each slot's `return` shapes `responses[i]`;
  `ComposedRequest.Return` shapes top-level overlays only.
- `ProcessChain`: each stage's `return` is applied after the whole
  chain, so a stage that excludes `data` still feeds the next stage.
- Streaming: rows carry only the selected columns; `returned` rides the
  terminal chunk only; excluding `data` yields no rows. See
  [Streaming & ProcessStream](streaming.md).
