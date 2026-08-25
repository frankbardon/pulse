---
name: grouper-design
description: Grouper slot semantics — multi-grouper composition (key product), fused crosstab eligibility, Group.Include inclusion-list, smart defaults per field type. Topical design; per-GROUP detail lives in atomic op-group-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [GROUP, Crosstab, groups]
---

# Grouper design

`groups` partitions records BEFORE aggregation; each `aggregations` entry folds inside the bucket. Per-GROUP detail lives in atomic `op-group-*` skills.

## Slot identity

`groups` is `[]types.Group`; entry `{type, field, label?, interval?, params?, include?}`. The grouper computes a bucket key per post-filter row; aggregators fold inside. Output rows carry the key as `<field>` (or `label`).

Pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Grouping runs AFTER attributes, so derived columns are addressable as `field`.

## Composition (key product)

With N entries (N ≥ 2), the engine forms the cartesian **key product**: each row receives a composite key `(g0, ..., gN-1)`. Empty `groups` collapses to one global bucket.

`Request.Crosstab` projects groupers onto a row × column matrix in `Response.Crosstab`; plain `Request.Groups` emits one flat row per composite key under `Response.Data`. Multi-grouper composition is the canonical cross-tab mechanism — reach for `Crosstab` only when margins + normalisation matter (`crosstab-guide`).

## Smart defaults

When an entry names `field` but omits `type`, the engine infers from the schema type:

| Field type | Default grouper |
|---|---|
| numeric (`u4`/`u*`, `f32`/`f64`, `decimal128`) | `GROUP_RANGE` (Interval=10) |
| `categorical_*`, `packed_bool` | `GROUP_CATEGORY` |
| `date` | `GROUP_DATE` (`component=day`) |
| `set_*` | `GROUP_SET_PER_ELEMENT` |

Defaults never cross categories. Predict reports filled slots at `data.defaults_applied`. Disable via `Options.DisableDefaults` / `--no-defaults`. Full table: `request-envelope`.

## `Group.Include` — inclusion list

`Group.Include []string` restricts a grouper to an allow-list of bucket keys. A row whose key (for `GROUP_SET_PER_ELEMENT`, each fan-out key) is not listed is skipped — identical to the null-skip path. Empty / nil → "no filter".

Supported by `GROUP_CATEGORY` (label string), `GROUP_SET_VALUE` (pipe-joined composite key), `GROUP_SET_PER_ELEMENT` (each fan-out label). Others ignore `Include` — use `FILTER_INCLUDE` on the source field. Streamability preserved: O(1) membership inside `KeyFor` / `KeysForRow`.

**Order-significant.** A non-empty `Include` also fixes emission order: buckets appear in listed order (plain grouped `Data` + `Components.buckets`, and each crosstab axis independently — an axis without `include` keeps alpha/dict order). Empty / nil preserves the prior alphabetical (or `GROUP_SET_PER_ELEMENT` dict-index) order — byte-identical. Zero-record include values are still dropped. Buffered + fused agree.

## Fused crosstab eligibility (`processing.CanFuseCrosstab`)

The fused path computes a row × column matrix in-decode (~30–47% faster; peak heap 8.8–20.8× lower than buffered across 25k→400k rows — quote peak heap, never `B/op`). Gate: `processing.CanFuseCrosstab(req, schema, ext)`. Activates when the cell aggregator is mergeable + non-recompute AND every row/column grouper implements EITHER per-record keying interface.

- `StreamableGrouper.KeyFor` (one bucket per record): `GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_DATE`, `GROUP_SET_VALUE`.
- `MultiKeyStreamingGrouper.KeysForRow` (N buckets per record): `GROUP_SET_PER_ELEMENT` — fusable at ANY axis position, on either or both axes, several per axis. Axis keys are the cartesian product of each position's key set; each accumulator updates once per distinct key at its own depth.
- Neither: `GROUP_QUANTILE` (needs a finalize-time sorted view) — the only grouper that forces buffered.

`Request.Overlays` does NOT force buffered — the fused exit folds layers through the same hook the buffered exit uses (`crosstab-guide`, `overlay-system`).

Embedder groupers opt in by implementing either interface and returning `ErrGrouperKeyNull` on nulls. Implementing neither stays correct — falls back to buffered RunCrosstab.

## Components contract (v0.20.0)

Every `Response.Components.Groupers[i]` carries the **universal floor** `total_n` (post-filter records partitioned across all buckets) + `n_null` (records that took the null/skip path). It differs from the aggregator pair `{n, n_null}` — groupers partition ALL post-filter records, not just non-null inputs.

Operator-specific keys ride in `Operator map[string]any`; authoritative per-operator lists on `ComponentSchema` (`descriptor/capabilities_groupers.go`, mirrored at `manifest.components_schemas.groupers[<op>].keys`) and in the atomic `op-group-*` skills.

Mergeability:

- **`Mergeable`** — `GROUP_CATEGORY`, `GROUP_DATE`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_SET_VALUE`, `GROUP_SET_PER_ELEMENT`. Components fold across streaming chunks via `ComponentsDelta`.
- **`None`** — `GROUP_QUANTILE` (`BufferedComponents=true`). Cutpoints need the sorted full input; components emit only on terminal buffered flush.

Full Components contract (typed shells, streaming chunk behaviour): `response-components`.

## Gotchas

- `GROUP_SET_PER_ELEMENT`: `sum(buckets[].count) > total_n` is correct; on a crosstab axis its margins are non-additive (a 3-label row counts 3× across row margins, once in the grand total) on BOTH paths.
- Empty-mask `set_*`: `GROUP_SET_VALUE` buckets under the empty key; `GROUP_SET_PER_ELEMENT` skips. Neither increments `n_null`.
- `GROUP_DATE` with `day_of_week` emits weekday names that lex-sort, not Sun→Sat — sort explicitly.
- Range / rounded / quantile reject categorical fields at construction; `GROUP_QUANTILE` forces buffered execution.

## See

- Recipes: `pulse_examples_search tags=["cohort-analysis"|"cross-tabulation"|"distribution-shape"|"survey"]` plus atomic `op-group-<name>`.
- `crosstab-guide` (Crosstab shape, margins, normalisation), `aggregation-design` (what folds inside the bucket), `request-envelope` (slot keys, smart defaults), `response-components` (typed `GrouperComponents`), `streaming-and-watching` (streamability).
