# Adding an Overlay Kind (Embedders)

> **Embedding Pulse, not contributing to it.** This recipe registers an
> `OVERLAY_<NAMESPACE>_<NAME>` kind through
> `pulse.Options.Extensions.Overlays` and the public `extend` package.
> Built-in overlay kinds live in `internal/processing/` and embedders cannot
> import it. The field-by-field contract is in [Extension
> Points](extension-points.md); this page walks the order you meet it in,
> with one MATRIX kind that emits p-values.

**Audience:** an embedder who needs a derived layer over a finished result —
a per-cell test, a custom index — that behaves like a built-in everywhere:
predict, manifest, MCP, feature profiles, `multiplicity`, weights.

An overlay is a **read-only fold** over an already-materialised host. It
never re-reads a record and never mutates the host; the engine stamps the
layer's `Name`, `Kind`, `Scope` and `Ref` from the spec.

## 1. Pick the hosts

A kind runs on exactly the hosts whose handler you set on
`extend.OverlayHandler`:

| Handler | Host | Rides |
|---|---|---|
| `Matrix` (`MatrixOverlayFunc`) | a crosstab result (`MatrixOverlayHost`) | `Request.Overlays` with `Request.Crosstab`, and a Compose slot's own |
| `Series` (`SeriesOverlayFunc`) | a grouped result with no crosstab, scored on its FIRST aggregation (`SeriesOverlayHost`) | `Request.Overlays` |
| `Compose` (`ComposeOverlayFunc`) | the finalised slots a spec's `reference` / `targets` name (`ComposeOverlayHost`) | `ComposedRequest.Overlays` |

At least one is required (`PULSE_EXTENSION_PARAM_INVALID`, reason
`missing_handler`). A spec over a host the kind does not run on — and every
spec on `ChainRequest.Overlays` or `FacetRequest.Overlays` — is refused
`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, at predict and at runtime alike.
Every extension kind runs buffered: `PredictResult.Streamable` is false with
the reason named.

## 2. Write the handler

A share-of-grand-total p grid. The handler reads the host through its
accessors (each returns a copy or a scalar; nothing aliases the response),
returns a `MATRIX` payload and nothing else:

```go
func pGrid(spec *types.OverlaySpec, host extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
    grand, ok := host.GrandTotal() // needs crosstab margins.grand
    if !ok || grand == 0 {
        return extend.OverlayOutput{}, fmt.Errorf("no grand total")
    }
    m := &types.MatrixPayload{
        RowHeader: host.RowHeader(), ColumnHeader: host.ColumnHeader(),
        CellLabel: "p",
    }
    for r := 0; r < host.Rows(); r++ {
        m.RowKeys = append(m.RowKeys, host.RowKey(r))
        row := make([]types.MatrixCell, host.Columns())
        for c := range row {
            if v, ok := host.Cell(r, c); ok { // false: absent, NaN, non-scalar
                row[c] = types.MatrixCell{Value: v / grand, Present: true}
            }
        }
        m.Cells = append(m.Cells, row)
    }
    for c := 0; c < host.Columns(); c++ {
        m.ColumnKeys = append(m.ColumnKeys, host.ColumnKey(c))
    }
    return extend.OverlayOutput{
        Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m},
    }, nil
}
```

Rules the handler lives under: deterministic and pure; `spec` is a copy you
may read but not keep; a `*errors.CodedError` return keeps its code, any
other error is returned as is; a panic is recovered
(`PROCESSING_PANIC`). The engine owns `PAdjusted` / `SignificantAdjusted`
(payload and summary) and `Parts`: setting any is
`PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH` (reason `engine_owned` /
`undeclared_parts`). The payload `Shape` must be one you declared and exactly
the matching arm set (reason `undeclared_shape` / `shape_arm_mismatch`).
`OverlayOutput.Warnings` need a non-empty `Code` and `Message`.

## 3. Register it

```go
p, err := pulse.New(pulse.Options{
    Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{{
        Name:         "OVERLAY_ACME_PGRID",
        Description:  "share-of-total p grid",
        Handler:      extend.OverlayHandler{Matrix: pGrid},
        Shapes:       []types.OverlayShape{types.OverlayShapeMatrix},
        Scopes:       []types.OverlayScope{types.OverlayScopeCell},
        Refs:         []extend.OverlayRefKind{extend.OverlayRefNone},
        Inferential:  true,
        EmitsPValues: true,
        Interpretation: []descriptor.Interpretation{{
            Field: "p_value", Means: "the chance of a share this far from even under no difference",
        }},
    }}},
})
```

What each declaration buys:

- `Shapes`, `Scopes`, `Refs` — a spec outside them is refused declaratively
  (`PULSE_OVERLAY_SCOPE_UNSUPPORTED`, `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`)
  before the handler runs. `Refs` empty reads as `[OverlayRefNone]`;
  `OverlayRefMargin` accepts `Ref.Margin`. `Level` / `Within` must be zero.
- `Params []ParamMeta` — declare the spec's `params` keys (unknown key:
  `PULSE_REQUEST_UNKNOWN_FIELD`; wrong type: `PULSE_REQUEST_PARAM_INVALID`).
  Undeclared params pass through unchecked.
- `Validate` / `ValidateCompose` — optional spec-only hooks, run after the
  declarative rules, once per spec, identically at predict and runtime.
- `WeightAware` — see step 5.
- `ReadsCellCountsOnly` — declare it only if the handler reads the cells'
  record counts and weight figures (`CellN`, the margin Ns, `CellSumWeights`,
  `CellNEff`) and never a cell or margin value. Undeclared, a spec over a
  crosstab whose cell is a categorical code (`AGG_MODE` of a categorical or
  `packed_bool` field) is refused `PROCESSING_CONFIG`, reason
  `categorical_code`, on the Process host and on a Compose crosstab slot alike.
- `HonoursField` / `HonoursPartitionBy` — opt the kind into the engine's
  series params. `HonoursField` (a `Series` or `Compose` handler): the host
  is scored on the aggregation `params.field` names, not the first one, and
  `SeriesOverlayHost.ValueLabel` names it. `HonoursPartitionBy` (a `Series`
  handler returning the `series` shape): over two or more groupers the
  handler runs once per `params.partition_by` partition, on a host of that
  partition's rows, and each partition's entries land back on their rows
  (one entry per partition row, else `PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH`,
  reason `partition_misaligned`). Both keys are then declared for the kind
  (do not list them in `Params`) and judged at predict and runtime alike
  (`PULSE_OVERLAY_FIELD_UNKNOWN`, `PULSE_OVERLAY_PARTITION_INVALID`); a
  Compose spec's `partition_by` is refused (reason `compose_host`). Unset,
  both keys are unknown params and the handler sees the first aggregation
  over the whole host.
- `DependsOn` — feature edges; the kind is a feature (host group from its
  handlers: `Matrix` needs `capability:crosstab`, `Series` / `Compose` need
  `capability:compose`), so a [feature profile](../library/feature-profiles.md)
  that omits it hides it everywhere.
- `Purpose` / `Interpretation` — guidance, validated at `pulse.New`
  (`PULSE_EXTENSION_PURPOSE_INVALID`). `Inferential` REQUIRES an
  `Interpretation`.

`pulse.New` then **probes** every handler once over a synthetic host (a 2x2
crosstab with components for `Matrix`, three groups for `Series`, a reference
and a target slot of each shape for `Compose`). A `Validate` refusal or panic
or a handler panic there is `PULSE_EXTENSION_FACTORY_PANIC` (`details.stage`
`validate` / `handler`, `details.host` for Series / Compose); a handler
*error* on the synthetic host is tolerated; an output the registration does
not declare is `PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH`.

## 4. P-values and `multiplicity`

`EmitsPValues` (requires `Inferential`) declares RAW p-values at the shape's
site: every present cell of a MATRIX layer (a float64 p grid, NaN for
untestable), every SERIES entry's `Summary.PValue`, a SCALAR layer's
`Summary.PValue`. Under a `multiplicity` block they join the request's
correction family like a built-in kind's; the engine writes the adjusted twins
beside the raw p, which never moves. A p off the site, outside [0, 1], or on a
registration without `EmitsPValues` is `PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH`
(`misplaced_p_values`, `invalid_p_value`, `undeclared_p_values`); the probe
also requires one p over its host (`missing_p_values`). Long form:
[Extension Points, Inferential overlay kinds and
p-values](extension-points.md#inferential-overlay-kinds-and-p-values).

## 5. Weights

A kind that does not declare `WeightAware` is refused
`PULSE_EXTENSION_NOT_WEIGHT_AWARE` under ANY weight resolved on its spec (its
own, the request's, or `Options.DefaultWeight`); `"weight": null` on the spec
opts out. Declare it only if the handler reads
`MatrixOverlayHost.WeightKind` / `CellSumWeights` / `CellNEff` and is correct
under either weight kind.

## 6. Use it

```json
{
  "cohort": {"filename": "sales.pulse"},
  "crosstab": {
    "rows": [{"type": "GROUP_CATEGORY", "field": "region"}],
    "cell": {"type": "AGG_SUM", "field": "qty", "label": "s"},
    "margins": {"rows": true, "columns": true, "grand": true}
  },
  "overlays": [{"kind": "OVERLAY_ACME_PGRID", "scope": "cell"}],
  "multiplicity": {"method": "holm"}
}
```

The layer lands in `Response.Overlays[i]` named `spec.Name`, else the
lower-cased kind without `OVERLAY_` (`acme_pgrid`). Check the registration
with `pulse manifest --json` (`extensions.overlays`) and `pulse predict`: both
judge the spec by the same declaration the runtime does.

## Where to look

| Concern | Place |
|---|---|
| public registration + handler types | `extensions.go` (`OverlayRegistration`), `extend/overlay.go` |
| lift onto the engine, snapshot, probe | `extensions_overlay.go` |
| the one request / output rule predict and runtime share | `internal/overlayhost/extension.go` |
| migration rows for each public symbol | `docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md` |
