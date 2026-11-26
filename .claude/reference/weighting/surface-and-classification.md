# Weighting — Surface, Resolution order, Threading, Validation, Classification by family

Slice of `.claude/reference/weighting.md` — load the index first, then only the slice(s) your change touches.

## Surface

| Slot | Shape | Notes |
|---|---|---|
| `types.Request.Weight` (`weight`) | `{field, kind}` (`types.WeightSpec`) | `omitempty`. `kind` ∈ `probability` (default) \| `frequency`. |
| Per-slot `weight` (`types.SlotWeight`, `omitzero`) | a field-name string, a `{field, kind}` object, or `null` | Carried by the aggregation (and so the crosstab cell and each `margin_aggregations` slot), test, regression, attribute, overlay, group and matrix slots — mostly so a slot can opt OUT. `Window` and `ComposeOverlaySpec` carry none. Go: `SlotWeightField(f)`, `SlotWeightOf(spec)`, `NullSlotWeight()`; zero value = absent. |
| `pulse.Options.DefaultWeight` | `*WeightSpec` | Validated at `pulse.New`; applies to Process / Compose / ProcessChain inner requests, never to `Facet` / `FacetSchema`. |

**Null ≠ absent.** An ABSENT per-slot `weight` inherits; an explicit `null` opts that slot out. `SlotWeight` carries its own `UnmarshalJSON` / `MarshalJSON` / `IsZero` and rides `omitzero`; the distinction must survive every hop — `types.CanonicalHash`, the MCP strict decode and binder (`internal/mcp/bind.go`, field enum from `descx.IsWeightFieldType`), and the payload schema (`#/$defs/SlotWeight` = `oneOf [string, WeightSpec, null]`). A set weight with no `kind` marshals as the bare string; decode refuses any other JSON shape or object key. The fields are unexported so "null with a field" cannot be built.

No top-level weight slot on `ComposedRequest` or `ChainRequest`: each inner `Request` carries its own. `AGG_WEIGHTED_MEAN.params.weight_field` is slot-weight sugar.

**Weight field type.** Unsigned integers (`u4` … `u64`) and floats (`f32`, `f64`) only; any other type is `PROCESSING_CONFIG` `{slot, field, type}` (`slot` is `weight`, `<slot>.weight`, or the slot path of an applied default). An empty `field` or an unknown `kind` is `PROCESSING_CONFIG` `{slot, field, kind}` — for `Options.DefaultWeight` already at `pulse.New` (`slot: "Options.DefaultWeight"`).

**Weight field existence.** An explicitly named weight is judged by the field-reference rule (`fieldRefWalk.checkWeights`, `internal/descriptor/field_refs.go`) against the SCHEMA, not the derived columns — an attribute label or feature output is no weight — and refused `SERVICE_VALIDATION` `{field, slot}`. Joined names resolve against the joined schema; a chain stage ≥ 1 against its synthesised input schema. `Options.DefaultWeight` is judged only on a slot where it is APPLIED, so an instance default never breaks a cohort lacking the field for a request that would not use it. Projection must decode every weight field — the service's `neededFields` adds the instance default that `processing.NeededFields` cannot see; otherwise the weight decodes as null silently (`TestProjection_WeightColumnDecodes`).

## Resolution order

One resolver, `ResolveWeights` (`internal/descriptor/weight_resolve.go`), called by runtime (inside `Service.checkFieldRefs`, every mode) and by predict and the Compose / chain validators at the same point, so they cannot disagree (`TestWeight_PredictMatchesRuntime`, `TestWeight_ValidatorsMatchRuntime`):

1. the slot's own `weight` (`null` ⇒ opted out, stop);
2. the request's `weight`;
3. `Options.DefaultWeight`;
4. none.

Slots are walked in a fixed order with a fixed `slot` spelling (read `ResolveWeights`; new slot kinds are APPENDED so earlier reports keep their order). A slot with an empty `Type` is skipped. Predict reports per slot as `PredictResult.Weights` (`{slot, operator, field, kind, status, source}`; `status` / `source` values are the `descriptor.WeightStatus*` / `WeightSource*` constants; `kind` spelled out). The list is nil — key absent — when nothing names a weight, so an unweighted predict payload is unchanged. `applied` vs `skipped_not_weight_aware` reads `weighting.ClassOf(op)`; a hidden operator is never-registered, hence not aware.

## Threading

**Design: the resolved weight rides the aggregator's SPEC.** `processing.StampWeights` returns a copy of the request whose every aggregation slot carries its RESOLVED weight (set, kind spelled out, when a weight applies to an aware operator; `null` otherwise), folding the same precedence and class table as `ResolveWeights`, so it never refuses. A request naming no weight is returned as the same pointer, so the unweighted path executes the unchanged object; the caller's request is never mutated (echo and `CanonicalHash` see what was sent). Every engine entry and both parallel reducers stamp, so every mode builds aggregators off one spec. A factory reads `slotWeight(agg)` and returns the weighted form or the unchanged unweighted type. Why the spec, not the record: per-slot weights differ within one request.

`internal/weighting` is the leaf both halves read: `ClassOf` / `IsAware` (class table) and `Classify` (per-row validity).

**A declaration keyed on the resolved weight.** A WIN_FORMULA over output rows types an `AGG_SET_FREQUENCY` cell as its integer count map or, when the slot resolves a weight, a Σw float map. The field-reference walk runs on the AUTHORED request, so it reads `Options.DefaultWeight` from `InstanceSnapshot.DefaultWeight` — one declaration on predict, pre-flight and factory (`TestWinFormula_SetFrequencyCellFollowsResolvedWeight`).

## Validation

| Weight value | Treatment |
|---|---|
| positive, finite | used |
| zero | valid; contributes nothing |
| null | excluded, counted (`null`) |
| negative | excluded, counted (`negative`) |
| NaN / ±Inf | excluded, counted (`nan_inf`) |
| non-integer under `kind: frequency` | excluded, counted (`non_integer_frequency`) |

Invalid weights are **never coerced** (`weighting.Classify`; a zero weight is skipped too, so a leading zero cannot divide by a zero Σw). Each weighted aggregator slot reports `n_weight_invalid` = its VALUE-PRESENT rows excluded for an invalid weight (a null-value row is `n_null`; its weight is not judged). Each Response carries one `PULSE_WEIGHT_INVALID_ROWS` warning per weight field that excluded rows — `details{field, count, by_reason{null, negative, nan_inf, non_integer_frequency}}`, all four reasons always present — counting FILTER-PASSING rows with an invalid weight whether or not the value is present; a fractional weight counts as `non_integer_frequency` when any slot reads that field as `frequency`. `processing.WeightRowTally` tallies it from the stamped aggregation slots (top-level, crosstab cell, every margin slot — an auxiliary-only weight warns too) and merges it across every execution mode and both crosstab arms. `Strict` turns it into an error with the same code and details.

## Classification by family

The class of every built-in is code, not prose: `internal/weighting` `operatorClasses` (plus per-SPEC classes in `weighting.AttributeClassOf`) and the overlay table `overlayWeightClasses` in `internal/descriptor/weight_resolve.go`. The manifest exposes it as `weight_kinds` — `["frequency","probability"]` = both kinds, `["frequency"]` = frequency only, absent = not aware — and the coverage harnesses (`assertWeightKindCoverage`) read the manifest, so table and manifest cannot drift. Read the table for a given operator; the rules it encodes:

- **Four outcomes.** Both kinds; frequency only (probability ⇒ `PULSE_WEIGHT_UNSUPPORTED` naming the kind); refused (`PULSE_WEIGHT_UNSUPPORTED`, permanent, `details.reason` from `weighting.RefusalReason` / the overlay table's `reason`); not weightable / skipped (stamped `null`).
- **Per-SPEC classes.** Some operators' class depends on params, so manifest `weight_kinds` can't express it: an attribute naming `params.fit` scores a bound fit and is skipped (the fit's own weight applied when it ran); `ATTR_REG_RESIDUAL` of kind `standardized` / `studentized` and `ATTR_REG_INFLUENCE` are frequency-only (one copy of the `rep()` expansion, `weighting.ObservationKindReason`); `ATTR_FORMULA` calling `total(expr)` is frequency-only (`details.function: "total"`, `weighting.FormulaTotalKindReason`) while its type stays row-local.
- **Decimal.** Any weight-aware aggregator over a `decimal128` value field is refused (`value_field`, `type`; no `reason`).
- **Not weightable.** Order / distinct / null-count / set-union aggregators, `ATTR_NORMALIZED` / `ATTR_PREVIOUS_STATE` (a value read off another record) and row-local attributes and groupers are skipped. `AGG_CONSTANT` is skipped on a default and `PROCESSING_CONFIG` (`weight_not_accepted`) on an explicit weight.
- **Windows** refuse a REQUEST weight with `PROCESSING_CONFIG`; the default is skipped. Filterers and features are skipped. Post-tests read aggregated rows and refuse any weight.
- **Regressions.** `resample: bootstrap` and `selection` are refused on every `REG_*`; `resample: jackknife` runs weighted (JK1) where the model supports it. Likelihood figures (`two_log_lik`, `aic`, LR tests) exist under frequency only — under probability they would be pseudo-likelihoods and are absent.
- **Overlays.** Descriptive kinds (shares, indices, deltas, `OVERLAY_CORRESPONDENCE`, `OVERLAY_MARKOV`, `OVERLAY_RETENTION`, `OVERLAY_RAKE`) read the weighted host payload as is and carry no class (scale-invariant). Facet-host kinds never see a weight; a set spec `weight` is refused. `OVERLAY_FISHER_EXACT_CELL` is refused also when only its HOST cell carries a probability weight.
- **Extensions** declaring `WeightAware` apply under BOTH kinds (`OperatorMeta` has no `weight_kinds`, by decision); see "Extension contract".

"Refused" means PERMANENT: no built-in is a pending `ClassRefuse`. "Has no weighted form yet" is reachable only by an `Inferential` overlay kind with no `overlayWeightClasses` entry — a NEW inferential kind lands there until its weighted form exists. The one rule behind "both kinds" (N*, w*, the frequency formula on w*) is "Weighted inference".
