```yaml
name: panel-n-sources
description: The sample-size modes of the Compose-host proportion-z panel — the four n_source modes, the load-bearing n_within_depth pointer, the row partition gate, the distinct-key admission and its wholesale refusals, and why the pairwise spellings are not aliases.
type: guide
kind: design
applies_to: compose, predict
covers: [OVERLAY, PanelOverlayParams, n_source, n_within_depth]
requires: [OVERLAY_PROP_Z_PANEL]
```

# Panel sample-size sources

`OVERLAY_PROP_Z_PANEL` tests every pair of N+1 Compose slots per cell. `params` decode into `PanelOverlayParams`; absent, `{}` and the explicit default stay byte-identical to the pre-params baseline. This file is the panel's n vocabulary; the test, its output and its warnings: [`op-overlay-prop-z-panel`](op-overlay-prop-z-panel.md). The intra-crosstab pairwise kinds have their own vocabulary: [`pairwise-n-sources`](pairwise-n-sources.md).

## The four `n_source` modes

`n_source` picks each SLOT's n at the tested coordinate. "Row" is that slot's own row axis, never a pair axis.

| `n_source` | Each slot's n | Fallback |
|---|---|---|
| `row_margin_value` (default) | the row-margin VALUE off the payload | none needed: a missing or zero margin reads as n = 0 (each pair with that slot ⇒ NaN + `PULSE_OVERLAY_REF_ZERO`), never the cell value |
| `cell_n_unweighted` | the counted `n` from the slot's `Components.Crosstab`, BY KEY | none: unreadable ⇒ skip the cell |
| `row_margin_value_within` | the SAME payload row margin, optionally summed over a row-key prefix | none |
| `row_margin_distinct_within` | the row margin out of `RowMarginComponents`, as the cell aggregator's distinct-KEY cardinality | none |

`row_margin_distinct_within` is the distinct-KEY sibling of the value-within mode — a different CARRIER, hence not spelled `row_margin_value_distinct_within`. Use it when one respondent contributes several records and n must be RESPONDENTS.

## `n_within_depth`

Read by the two `_within` modes alone (`types.PanelNSourcesUsingWithinDepth()`), and the pointer (`*int`) is load-bearing: omitted ⇒ the exact per-slot row margin, no summing; `d` ⇒ margins summed over that slot's rows agreeing on the first `d+1` dims. Depth `0` ≠ omitted.

- Negative, or set with any other mode ⇒ `PULSE_OVERLAY_PARAM_MISSING` on both arms.
- Past a slot's row depth ⇒ the same code at RUNTIME only, naming that slot. Slots may declare DIFFERENT row depths; one out-of-range slot refuses the whole spec (dropping it would change `M`).

## The row partition gate

An explicit depth SUMS across rows, so they must partition the key set. A fan-out grouper (`GroupType.FansOut()`, or an extension declaring it) on ANY slot's row axis at a depth `> n_within_depth` ⇒ `PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`, both arms, `details` `panel_index` / `slot_index` / `slot_label` / `dim_index`; one offending slot refuses the whole spec. Inside the prefix is fine (it multiplies slabs, not cells); an omitted depth is never gated. The pairwise `n_within` is ungated because record counts are additive — a panel row margin is whatever the slot's cell aggregator emitted, so this host cannot claim that.

## Not the pairwise spellings

`row_margin_n`, `n_within` and `n_within_distinct` are pairwise spellings, NOT aliases: unknown here ⇒ `PULSE_OVERLAY_PARAM_MISSING` on both arms. The pairwise `n_within` sums `CellCounts` over a pair-axis slab at ONE fixed opposite index — one column; the panel's leg sums a slot's ROW margins across ALL columns, so the two differ by roughly the column count.

## Admission of the distinct mode

`row_margin_distinct_within` is admitted UP FRONT, per slot, on the cell aggregator's EXACT identity signature — the sum-of-distinct aggregator (figure on `distinct_count`) and the distinct count (`cardinality`) only. The modal aggregators also emit a key spelled `distinct_count`, but theirs counts distinct answer CODES, so presence is never the test. Anything else ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` naming the observed aggregator, the admitted set and `panel_index` / `slot_index` / `slot_label`. RUNTIME only — predict cannot see materialised components.

Two WHOLESALE refusals, never a per-slot drop (dropping changes `M`): one unadmitted slot, AND slots that are each admitted but name DIFFERENT aggregators — keys-that-summed and distinct non-null values are not the same UNIT, and a pair's two legs must never be counted in different units.

## Components gating

`cell_n_unweighted` and `row_margin_distinct_within` need components on EVERY slot: disabled or non-crosstab ⇒ `PULSE_OVERLAY_COMPONENTS_REQUIRED`, unresolved ⇒ `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`. Mode-scoped — the default never gates.

An unemitted distinct MARGIN is not a zero-sized one: the coordinate skips (`n_missing`) rather than testing against 0.

## Weighted slots

The value modes read p̂ = cell / margin (Σw) and n = the base's N* (margin or slab Σw under frequency, Kish `n_eff` under probability). `cell_n_unweighted` / `row_margin_distinct_within` on a weighted slot are `PROCESSING_CONFIG`, predict AND runtime; so is a probability slot read with components disabled ([`weighting`](weighting.md)).

## See

[`op-overlay-prop-z-panel`](op-overlay-prop-z-panel.md) · [`pairwise-n-sources`](pairwise-n-sources.md) · [`overlay-system`](overlay-system.md) · [`compose-requests`](compose-requests.md) · [`crosstab-guide`](crosstab-guide.md).
