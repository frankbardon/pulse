# Weighting — weighted descriptive figures (U11)

Long form of the row-weighting contract. CLAUDE.md keeps only a pointer (Reference Docs index), the Update Demand row and the Components floor note in "Output Format Contract". **Load this file before touching ANY weight surface**: `types.Request.Weight`, a per-slot `weight`, `pulse.Options.DefaultWeight`, the resolver, the validation pass, an aggregator's weight classification, a refusal, the `sum_weights` / `n_eff` / `n_weight_invalid` floor keys, the extension `WeightAware` contract, the SPSS weight suggestion, or the `capability:weighting` feature.

**Status: skeleton.** Written at weighting-descriptive E1-S1 from the resolved interview so every later story has a contract home; each section is refined by the story that ships it, and anything marked *(planned)* does not exist in code yet. **Shipped (E1-S2): Surface, Resolution order, the weight-field rules and the three error codes** — no operator weights anything yet: `weightAwareOperators` (`internal/descriptor/weight_resolve.go`) is empty, so every resolved weight reports `skipped_not_weight_aware` until the operator stories list their operators there. When code and this file disagree, the story that changed the code owes the fix here in the same PR.

`format_version` stays `"1.1"` for every weight surface: each addition is additive `omitempty` / `omitzero`, and a request that carries no weight produces byte-identical output.

## Surface

| Slot | Shape | Notes |
|---|---|---|
| `types.Request.Weight` (`weight`) | `{field, kind}` (`types.WeightSpec`) | `omitempty`. `kind` ∈ `probability` (default) \| `frequency`. |
| Per-slot `weight` (`types.SlotWeight`, `omitzero`) | a field-name string, a `{field, kind}` object, or `null` | On `Aggregation` (so also the crosstab cell and `crosstab.margin_aggregations[i]`), `Test` (`tests` and `post_tests`), `RegressionSpec`, `Attribute` and `OverlaySpec` (tests, regressions, overlays and attributes mainly to opt OUT). Go spellings: `SlotWeightField(f)`, `SlotWeightOf(spec)`, `NullSlotWeight()`; zero value = absent. |
| `pulse.Options.DefaultWeight` | `*WeightSpec` | Validated at `pulse.New`; applies to Process / Compose / ProcessChain inner requests, never to `Facet` / `FacetSchema`. |

**Null ≠ absent.** An ABSENT per-slot `weight` inherits; an explicit `null` opts that slot out. The per-slot type is new to `types/` (no precedent): it carries its own `UnmarshalJSON` / `MarshalJSON` / `IsZero` and rides `omitzero`, and the distinction must survive every hop — `types.CanonicalHash`, the MCP strict decode and binder, and the payload JSON Schema (`internal/descriptor` `BuildPayloadSchema` special-cases it as `#/$defs/SlotWeight` = `oneOf [string, WeightSpec, null]`). A set weight with no `kind` marshals as the bare string (the object form without `kind` re-marshals as the string — same meaning); decoding refuses any other JSON shape and any object key but `field` / `kind`. The fields are unexported on purpose: the three states are reachable only through the constructors, so "null with a field" cannot be built. The MCP binder (`internal/mcp/bind.go`) declares the same union, its field enum drawn from the cohort's weight-capable columns (`descx.IsWeightFieldType`).

No new top-level slot on `ComposedRequest` or `ChainRequest`: each inner `Request` carries its own `weight`. `AGG_WEIGHTED_MEAN.params.weight_field` is slot-weight sugar (see "Aggregator classification").

**Weight field type.** Unsigned integers (`u4`, `u8` … `u64`) and floats (`f32`, `f64`) only. `decimal128`, `categorical_*`, `date`, `datetime`, `packed_bool` and `set_*` are `PROCESSING_CONFIG` with details `{slot, field, type}` (`slot` is `weight`, `<slot>.weight`, or the slot path for an applied default). An empty `field` or a `kind` other than `probability` / `frequency` is `PROCESSING_CONFIG` too (`{slot, field, kind}`), for `Options.DefaultWeight` already at `pulse.New` (`slot: "Options.DefaultWeight"`).

**Weight field existence.** An explicitly named weight (request or slot) is judged by the field-reference rule (`fieldRefWalk.checkWeights` in `internal/descriptor/field_refs.go`) against the SCHEMA, not the derived column set — an attribute label or feature output is no weight — and refused `SERVICE_VALIDATION` `{field, slot}` like any unknown name. A joined (prefixed) name resolves against the joined schema; a chain stage ≥ 1 against its synthesised input schema. The inherited `Options.DefaultWeight` is NOT a request reference: `ResolveWeights` judges its existence and type only on a slot where it is APPLIED, so an instance default never breaks a cohort lacking the column for a request that would not use it. Projection: `processing.NeededFields` adds the request and slot weight fields; the service's `neededFields` adds the instance default (which `NeededFields` cannot see), so projection never decodes a weight as null (`TestProjection_WeightColumnDecodes`).

## Resolution order

One shared resolver, `ResolveWeights(req, schema, defaultWeight, inst)` in `internal/descriptor/weight_resolve.go`, modelled on the time-zone resolver (`internal/descriptor/zone_resolve.go`), called by BOTH runtime and predict so they cannot disagree. The runtime runs it inside `Service.checkFieldRefs`, i.e. right after the field-reference rule passes, in every execution mode (Compose slots and chain stages each resolve their own Request, located by `details.request` / `details.stage`); predict and the Compose / chain validators run it at the same point:

1. the slot's own `weight` (`null` ⇒ opted out, stop);
2. the request's `weight`;
3. `Options.DefaultWeight`;
4. none.

Slot order (and `slot` spelling): `aggregations[i]`, `crosstab.cell`, `crosstab.margin_aggregations[i]`, `tests[i]`, `post_tests[i]`, `regressions[i]`, `attributes[i]`, `overlays[i]` (`operator` = the overlay kind). A slot with an empty `Type` is skipped. Predict reports the outcome per slot as `PredictResult.Weights` (`descriptor.ResolvedWeight`, `weights`, `omitempty`) — `{slot, operator, field, kind, status, source}`: `field` / `kind` are omitted on `opted_out` / `none` and `kind` is spelled out (`probability` when the spec left it empty); `status` ∈ `applied` | `skipped_not_weight_aware` | `opted_out` | `none` (`descriptor.WeightStatus*`); `source` ∈ `slot` | `request` | `options` | `none` (`descriptor.WeightSource*`; an opted-out slot's source is `slot`). The whole list is nil — the key absent — when neither the request, a slot (a `null` included) nor the instance names a weight, so an unweighted predict payload is unchanged. `applied` vs `skipped_not_weight_aware` reads `IsWeightAware(op)`; a hidden operator is never-registered, hence not aware. Refusals surface as predict errors, identical to runtime (`TestWeight_PredictMatchesRuntime`, `TestWeight_ValidatorsMatchRuntime`).

## Validation

| Weight value | Treatment |
|---|---|
| positive, finite | used |
| zero | valid; contributes nothing |
| null | excluded, counted (`null`) |
| negative | excluded, counted (`negative`) |
| NaN / ±Inf | excluded, counted (`nan_inf`) |
| non-integer under `kind: frequency` | excluded, counted (`non_integer_frequency`) |

Invalid weights are **never coerced**. Each weighted aggregator slot reports `n_weight_invalid`; each Response carries at most ONE `PULSE_WEIGHT_INVALID_ROWS` warning with `details{field, count, by_reason{null, negative, nan_inf, non_integer_frequency}}`, merged across buffered, streaming, shard-parallel, parallel-decode and fused-crosstab execution, and promoted to an error under `--strict`.

## Aggregator classification

| Class | Operators | Weighted meaning |
|---|---|---|
| weight-aware | `AGG_COUNT` | Σw over value-present rows |
| | `AGG_SUM`, `AGG_AVERAGE`, `AGG_WEIGHTED_MEAN` (alias of weighted `AGG_AVERAGE`) | Σwx; Σwx/Σw |
| | `AGG_VARIANCE` / `AGG_STDDEV` | population m2_w/Σw |
| | `AGG_WELFORD` | sample m2_w/(Σw−1) |
| | `AGG_SKEWNESS` / `AGG_KURTOSIS` | weighted population moments |
| | `AGG_MEDIAN` / `AGG_PERCENTILE` | expanded-index type 7 (below) |
| | `AGG_MODE` / `AGG_MODE_COUNT` | the max-Σw value / its Σw |
| | `AGG_FREQUENCY` | Σw of matches; share Σw_match/Σw |
| | `AGG_RATIO` | Σw·num / Σw·den |
| | `AGG_SET_FREQUENCY`, `AGG_SET_CARDINALITY_SUM`, `AGG_SET_CARDINALITY_AVG` | Σw per member; Σw·card; Σw·card/Σw |
| not weightable | `AGG_MIN`, `AGG_MAX`, `AGG_RANGE`, `AGG_DISTINCT_COUNT`, `AGG_DISTINCT_SUM`, `AGG_NULL_COUNT`, `AGG_SET_UNION`, `AGG_SET_INTERSECTION`, `AGG_SET_DISTINCT_VALUES`, `AGG_ZSCORE` | skipped under a default weight; an explicit weight is `PROCESSING_CONFIG` |
| U12-refuse | `AGG_CI_LOWER`, `AGG_CI_UPPER` | any weight in force ⇒ `PULSE_WEIGHT_UNSUPPORTED` |

**Weighted percentile (expanded-index type 7).** Sort by value; W = Σw; x₍ₖ₎ is the smallest xᵢ whose cumulative weight exceeds k; h = p·(W−1); interpolate linearly between x₍⌊h⌋₎ and x₍⌊h⌋+1₎. With integer frequency weights this equals type 7 on the physically expanded data. *(The clamp rule for non-integer W and the expanded-space `position*` Components shape are pinned by the implementing story, here and in `skills/op-agg-percentile.md`.)*

**`AGG_WEIGHTED_MEAN`.** Same implementation as weighted `AGG_AVERAGE`; the type name is preserved in output. `params.weight_field` is a slot-level weight with `kind: probability`: absent ⇒ inherits request / Options; differing from an explicit slot weight ⇒ `PROCESSING_CONFIG`; nothing resolves ⇒ `PROCESSING_CONFIG`. Not deprecated, and deliberately NOT gated by `capability:weighting`. Behaviour notes: the scalar shifts by at most a few ULP (Σwx/Σw replaces the incremental mean), and negative / NaN / Inf weights are now excluded and warned where they were previously accepted. Weighted `AGG_AVERAGE` gains the rich Components the weighted mean already emits (`m2_weighted`, `sum_weights_sq`, `weighted_variance`, `weighted_mean`, `sum_weighted`).

`decimal128` value field + any weight in force ⇒ `PULSE_WEIGHT_UNSUPPORTED`, even under a default weight (a silent skip would mix unweighted figures into a weighted table); opt out with `weight: null`.

Weighted Rich / Meta outputs that are integers when unweighted (`AGG_SET_FREQUENCY`'s map, `frequency.match_count`, `mode_count`) become floats only when weighted; the manifest `EmitsTypeNote` claims say so. Streamable / Mergeable flags do not change: a weighted mergeable operator stays streamable, shard-parallel and parallel-decode eligible (`CanMergeRequest`), and weighted percentile / median stay buffered exactly as unweighted.

## Crosstab

Every accumulator gets the weight on BOTH arms — buffered (`RunCrosstab`, `runCellAggregation`, `computeAuxMargins`) and fused (`FusedCrosstabState.Update`, `updateAuxMargins`, `finalizeCells`): cells, row / column / grand margins, partial-depth margins and auxiliary `margin_aggregations`. A weighted margin equals the weighted figure over the raw rows on both arms. **A record reaches an auxiliary margin only if it reached a CELL** — unchanged by weighting. `CellCounts` and margin counts stay raw ints; normalization divides a weighted cell by its weighted margin. The unweighted base beside weighted percentages is a `margin_aggregations` slot with `weight: null`. `crosstabfuse.Decide` is unchanged apart from weighted percentile / median staying buffered.

## Refusal rules

While a weight is in force on a slot, each of these is `PULSE_WEIGHT_UNSUPPORTED` at predict AND runtime (opt out per slot with `weight: null`):

- every `TEST_*` and every `REG_*`;
- `ATTR_ZSCORE`, `ATTR_TSCORE`, `ATTR_PERCENTILE`, `ATTR_NORMALIZED`;
- `GROUP_QUANTILE`;
- `AGG_CI_LOWER` / `AGG_CI_UPPER`;
- every overlay declared `Inferential: true` (`internal/descriptor/capabilities_overlay.go`) on a weighted host slot — sole exemption `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`, which accepts weighted `AGG_AVERAGE` or `AGG_WEIGHTED_MEAN` cells.

Windows (`WIN_*`): refused under an explicit weight, skipped under a default one. Filters, features, row-local attributes and other groupers are unaffected. Share and index overlays (`OVERLAY_SHARE_OF_*`, `OVERLAY_INDEX_VS_*`, panel index) read the host payload and are therefore weighted automatically. Facets are out of scope: no `FacetRequest.Weight`, and `Options.DefaultWeight` does not reach facet-population overlays. U12 inherits this list to lift.

## Floor keys

The universal aggregator floor `{n, n_null}` gains three keys, all `omitempty` and present ONLY on a weighted slot:

- `sum_weights` — Σw over the rows that contributed (spelling kept from `AGG_WEIGHTED_MEAN`; no `w_sum` alias);
- `n_eff` — Kish effective n, (Σw)²/Σw², `kind: probability` only;
- `n_weight_invalid` — rows excluded by validation.

`n` / `n_null` keep their value-presence meaning; counts stay raw ints (`CellCounts`, margin counts, grouper `total_n`, `ResponseMetadata` row counts). Absence of `sum_weights` is the only per-slot "unweighted" signal — there is no `weighted` marker and no skip warning; predict's per-slot status is the explanation. Declared as optional keys in the universal floor and `aggSchema`; extensions get them from the orchestrator and never emit them. Floor prose: `.claude/reference/response-components.md`.

## Operation-order exactness rule

Weighted formulas are written so that w = 1 reproduces the unweighted operation order bit for bit — e.g. `(w·δ)/Σw`, never `(w/Σw)·δ` — and the unweighted path is untouched when no weight is set. Gates *(planned)*: unity parity (all-ones weights byte-identical to unweighted for every weight-aware operator in buffered, streaming, grouped streaming, fused crosstab, buffered crosstab, shard-parallel and parallel-decode execution) and frequency-expansion parity (integer frequency weights equal physically duplicated rows — exact for counts / mode / median / percentile on integer data, 1e-12 relative otherwise). Reference fixtures (statsmodels `DescrStatsW`, numpy type 7 on `np.repeat`-expanded data) are generated offline and pinned in Go with a provenance comment; no Python in CI. Arm-comparison float tests otherwise use tolerances.

## Extension contract

- `WeightAware bool` on `AggregatorRegistration`, `AttributeRegistration` and `TestRegistration`; projected as manifest `weight_aware` (omitempty), stamped for built-ins the way `zone` is.
- `extend.Record.Weight() (float64, bool)` — the engine-resolved weight for the slot; only VALID weights reach it (invalid rows are filtered first); `ok == false` ⇒ no weight in force. The factory sees the resolved weight on its spec.
- A non-aware aggregator: explicit weight ⇒ `PULSE_EXTENSION_NOT_WEIGHT_AWARE`; default weight ⇒ skipped silently. A non-aware attribute or test: refused under any weight in force.
- Probe: a weight-aware factory must construct both with and without a weight.

Recipe: `docs/src/internals/extension-points.md`; engine wiring: `architecture.md` (Extension surface).

## SPSS suggestion

Inspect reads the SPSS metadata sidecar (`cohort.pulse.spss.json`, header-only — never records) and surfaces `suggested_weight {field, source: "spss_sidecar", kind: "probability"}` (additive, omitempty) on the result and envelope, the CLI leaf and `pulse_inspect`. Predict echoes it as DATA when no weight resolves — never a warning, so `--strict` stays safe. A stale sidecar (variable not in the schema) yields no suggestion, silently. A suggested weight is **never** auto-applied, including via `DefaultWeight`; there is no manifest-level suggestion (the manifest is instance-level). SPSS `WEIGHT BY` is frequency-like, while the default `kind` is `probability` — say so wherever the suggestion is documented. The predict / inspect contract amendment ("header + schema + sidecar metadata, never records") lands in `predict-inspect.md` with the implementing story.

## Feature profile

`capability:weighting` is a request-slot capability (`internal/descriptor/request_slots.go`, a `features.go` row with `Since` and a derived dependency on the request hosts). Hidden ⇒ a `weight` key is `PULSE_REQUEST_UNKNOWN_FIELD` at EVERY nesting level (top-level roots, per-slot, crosstab cell and margins, chain stages — new nested runtime gating), the payload schema hides the nested keys, `Options.DefaultWeight` is a `pulse.New` error, and `suggested_weight` / `weight_aware` are absent. `AGG_WEIGHTED_MEAN.params.weight_field` stays ungated (deliberate asymmetry). Contract: `feature-profiles.md`.

## Error codes

Each needs a `codeMetadata` entry (Message + ≥1 Fixup) and an `internal/descriptor/error_owners.go` owner. The three `PULSE_*` codes are registered (E1-S2) with owner `shared` — they move to the `capability:weighting` feature when it exists — and are not raised anywhere yet; the stories that raise them own their final details and fixup prose:

- `PULSE_WEIGHT_INVALID_ROWS` — warning; excluded rows with the by-reason breakdown.
- `PULSE_WEIGHT_UNSUPPORTED` — a refused surface under a weight; the fixup names the U12 gap and the `weight: null` opt-out.
- `PULSE_EXTENSION_NOT_WEIGHT_AWARE` — explicit weight on a non-aware extension aggregator.
- `PROCESSING_CONFIG` — bad weight field or type, explicit weight on a not-weightable operator, `weight_field` conflict or nothing resolving for `AGG_WEIGHTED_MEAN`.
