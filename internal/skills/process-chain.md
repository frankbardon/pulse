---
name: process-chain
description: ChainRequest source-rooted linear pipeline — mergeable-only v1 gate, per-stage Response.Components, dual-slot overlays (per-stage + whole-chain), StageRef resolution, shape-divergence warning.
type: guide
kind: design
applies_to: process-chain, predict
covers: [ChainRequest, pulse_process_chain, OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT, OVERLAY_DELTA_VS_STAGE, OVERLAY_INDEX_VS_STAGE]
requires: [capability:process_chain]
---

# Process chain

`ChainRequest` runs a linear pipeline rooted at one cohort: stage 0 reads the source; each later stage reads its predecessor's OUTPUT rows as a synthesised cohort. CLI `pulse api process-chain`; library `pulse.ProcessChain`; MCP `pulse_process_chain`.

## When to chain

Chain when a question is "aggregate, then aggregate the aggregates" — revenue per store, then the spread of store revenue per region; counts per respondent, then the distribution of those counts. It collapses N round-trips into one open + N stage validations. If the steps are independent of each other, batch them instead<!-- feature: capability:compose --> (`compose-requests`)<!-- /feature -->.

```jsonc
{
  "cohort": {"filename": "sales-2025.pulse"},
  "stages": [
    {"name": "per_store",
     "request": {"groups":       [{"type": "<grouper>", "field": "store"}],
                 "aggregations": [{"type": "<aggregator>", "field": "revenue", "label": "store_rev"}]}},
    {"name": "spread",
     "request": {"aggregations": [{"type": "<aggregator>", "field": "store_rev"}],
                 "sort": [{"field": "store_rev", "desc": true}]}}
  ]
}
```

`<…>` = any operator the stage gate admits (below). Only stage 0 names a cohort (the chain-level `cohort`); later stages' `cohort` is ignored. A later stage addresses earlier outputs by their LABEL (`store_rev`) or default output name.

## The stage gate (v1: mergeable only)

The bridge between stages is a synthesised cohort: grouper keys become `categorical_u32` columns, aggregator outputs become `f64`. A stage passes only if everything it runs fits that bridge — mergeable aggregators and groupers, row-local attributes, streamable filterers, no built-in aggregator over `decimal128`. **Read the admitted set off the manifest `process_chain` block** — `mergeable_aggregators`, `mergeable_groupers`, `row_local_attributes`, and `rejection_rules` for what is refused (windows, features, tests, regressions, two-pass attributes, order statistics). An extension operator passes iff declared `Mergeable`.

- **Smart defaults run first.** `{"field": "n"}` on a numeric field is gated as its default aggregator, and the next stage sees that default's output name.
- **A modal value on a categorical field chains its DICTIONARY INDEX** — the next stage sees a number, not the label.
- **Only stage 0 may join.** A later stage's `joins` ⇒ `PULSE_CHAIN_STAGE_JOIN {count, stage, stage_name}`<!-- feature: capability:joins --> (`join-design`)<!-- /feature -->.
- Failure: `PULSE_CHAIN_NOT_MERGEABLE` `"chain stage is not mergeable: <reason>"` `{stage_index, stage_name}`. Stages are gated as they run, so a refusal in stage 2 comes after stages 0–1 executed; a zone or join-count refusal carries `details.stage`.

When the gate refuses, run stage 0 as an ordinary request<!-- feature: capability:process --> (`pulse_process`)<!-- /feature --> and finish the later steps client-side.

## Per-stage `Response.Components`

Each stage emits its own `components` on `ChainResponse.stages[i]` — the universal floor (`{n, n_null}` per aggregator, `{total_n, n_null}` per grouper, `{n_in, n_out, n_null_input}` per filterer) plus operator keys. `ChainResponse.final` is the last stage's response. Overlays read these as inputs and never rewrite them (`response-components`).

## Dual-slot overlay design

Two independent overlay slots:

- **Per-stage** (`stages[i].request.overlays`) — the ordinary `Request.Overlays`, run at that stage's exit BEFORE the next stage reads its rows. Lands on `stages[i].overlays`. Nothing chain-specific.
- **Whole-chain** (`ChainRequest.overlays`, `ChainOverlaySpec`) — runs AFTER every stage finalises and compares two stage results: one kind per manifest `process_chain.overlay_kinds` entry, an index (`target / ref × 100`) and a delta (`target − ref`). Each spec needs `kind`, `scope`, `ref`, `target`. Lands on `ChainResponse.overlays` in declared order — never on `stages[i].overlays`.

<!-- feature: OVERLAY_INDEX_VS_STAGE, AGG_SUM, AGG_COUNT, GROUP_CATEGORY -->
A complete three-stage request with a whole-chain index: `pulse_examples_get chain-whole-chain-index-vs-stage`.
<!-- /feature -->

<!-- feature: capability:multiplicity -->
## Multiplicity

Each stage corrects its own request; no family spans stages, and `ChainOverlaySpec` takes no block (`multiple-comparisons`).
<!-- /feature -->

## `StageRef` resolution

`ref` and `target` each take exactly one of `index` (zero-based) or `name` (matches `stages[i].name`); both or neither is a configuration error. In Go, `Index` is a `*int` so stage 0 is distinguishable from "unset" — pass `&zero`.

An empty `target` defaults to the LAST stage. `ref` has no default — every spec names its baseline.

## Shape divergence

A whole-chain overlay fires only when ref and target produce the same host shape: `crosstab` ⇒ MATRIX, aggregations + groups ⇒ SERIES, aggregations alone ⇒ SCALAR. On a mismatch the runtime emits ONE `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` warning per spec (`details {target_shape, ref_shape, target_index, ref_index}`) and an empty layer in the target's shape — not fatal. Keep compared stages shape-identical: same groups, same aggregation count. Other whole-chain refusals: `PULSE_OVERLAY_KIND_UNKNOWN` (a kind outside `overlay_kinds`), `PULSE_OVERLAY_REFERENCE_UNKNOWN` / `PULSE_OVERLAY_TARGET_UNKNOWN` (index out of range, unmatched name, both or neither set).

## Hashing & echo

`ChainRequest.Hash()` is a 32-char canonical-JSON digest; `overlays` is `omitempty`, so an overlay-free chain hashes as it always did. `--echo-request` puts each stage's post-defaults form on `envelope.request`.

## See

<!-- feature: capability:compose -->`compose-requests` (independent slots) · <!-- /feature -->`overlay-system` (overlay hosts) · `response-components` · `streaming-and-watching` (`ChainRequest.Hash()` cache keys) · `docs/src/internals/adding-chain-predicate.md` (contributors).
