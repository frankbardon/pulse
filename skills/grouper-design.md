---
name: grouper-design
description: Grouper slot semantics — multi-grouper composition (key product), fused crosstab eligibility, Group.Include inclusion-list, smart defaults per field type. Topical design; per-GROUP detail lives in atomic op-group-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [GROUP, Crosstab, groups]
---

# Grouper design

`groups` partitions records BEFORE aggregation; each `aggregations` entry folds inside the bucket. Design contract here; per-GROUP detail (`GROUP_CATEGORY`, `GROUP_DATE`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_QUANTILE`, `GROUP_SET_VALUE`, `GROUP_SET_PER_ELEMENT`) lives in atomic `op-group-*` skills.

## Slot identity

`groups` is `[]types.Group`. Entry: `{type, field, label?, interval?, params?, include?}`. The grouper computes a bucket key per post-filter row; aggregators fold inside. Output rows carry the key as `<field>` (or `label`).

Pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Grouping runs AFTER attributes — derived columns are addressable as `field`.

## Composition (key product)

With N entries (N ≥ 2), the engine forms the cartesian **key product**: each row receives a composite key `(g0, ..., gN-1)`. Empty `groups` collapses to one global bucket.

- **`Request.Crosstab`** projects two named groupers onto a row × column matrix in `Response.Crosstab`.
- **Plain `Request.Groups`** emits one flat row per composite key under `Response.Data`.

Multi-grouper composition is the canonical cross-tab mechanism; reach for `Crosstab` only when margins + normalisation matter (`crosstab-guide`).

## Smart defaults

When an entry names `field` but omits `type`, the engine infers from schema type:

| Field type | Default grouper |
|---|---|
| numeric (`u4`/`u*`, `f32`/`f64`, `decimal128`) | `GROUP_RANGE` (Interval=10) |
| `categorical_*`, `packed_bool` | `GROUP_CATEGORY` |
| `date` | `GROUP_DATE` (`component=day`) |
| `set_*` | `GROUP_SET_PER_ELEMENT` |

Defaults never cross categories. Predict reports filled slots under `data.defaults_applied`. Disable via `Options.DisableDefaults` / `--no-defaults`. Full table: `request-envelope`.

## `Group.Include` — inclusion list

`Group.Include []string` restricts a grouper to an allow-list of bucket keys. Rows whose key (or, for `GROUP_SET_PER_ELEMENT`, each fan-out key) is not in `Include` are skipped — identical to the null-skip path. Empty / nil → "no filter".

Supported by `GROUP_CATEGORY` (label string), `GROUP_SET_VALUE` (pipe-joined composite key), `GROUP_SET_PER_ELEMENT` (each fan-out label independently). Others ignore `Include` — use `FILTER_INCLUDE` on the source field instead. Streamability preserved — O(1) membership inside `KeyFor` / `KeysForRow`.

**Order-significant.** A non-empty `Include` also fixes emission order: buckets appear in listed order (plain grouped `Data` + `Components.buckets`, and each crosstab axis independently — an axis without `include` keeps alpha/dict order). Empty / nil `Include` preserves the prior alphabetical (or `GROUP_SET_PER_ELEMENT` dict-index) order — byte-identical. Zero-record include values are still dropped. Centralized in `orderKeysByInclude` (plain grouped) + `orderCompositeKeysByAxisInclude` (crosstab axes), shared by buffered + fused.

## Fused crosstab eligibility (`processing.CanFuseCrosstab`)

The fused path computes a row × column matrix in-decode (~30–47% faster than buffered RunCrosstab). Activates when BOTH axis groupers implement `processing.StreamableGrouper` AND the aggregator family is mergeable. `processing.CanFuseCrosstab(req, schema, ext)` is the gate.

Implementers: `GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_DATE`, `GROUP_SET_VALUE`. Non-implementers: `GROUP_QUANTILE` (global rank), `GROUP_SET_PER_ELEMENT` (multi-key fan-out via `MultiKeyStreamingGrouper`).

Embedder groupers opt into fusion by implementing `StreamableGrouper` and returning `ErrGrouperKeyNull` on null inputs. Missing the interface stays correct — falls back to buffered RunCrosstab.

## Components contract (v0.20.0)

Every `Response.Components.Groupers[i]` carries the **universal floor**: `total_n` (records partitioned across all buckets, post-filter) + `n_null` (records that landed in the null/skip path). The floor differs from the aggregator pair `{n, n_null}` — groupers partition ALL post-filter records, not just non-null inputs.

Operator-specific keys ride in `Operator map[string]any`. Authoritative key lists per operator on `ComponentSchema` in `descriptor/capabilities_groupers.go` and mirrored at `manifest.components_schemas.groupers[<op>].keys`. Per-GROUP detail in atomic `op-group-*` skills.

Mergeability axis:

- **`Mergeable`** — `GROUP_CATEGORY`, `GROUP_DATE`, `GROUP_RANGE`, `GROUP_ROUNDED`, `GROUP_SET_VALUE`, `GROUP_SET_PER_ELEMENT`. Components fold across streaming chunks via `ComponentsDelta`.
- **`None`** — `GROUP_QUANTILE` (`BufferedComponents=true`). Cutpoints need the sorted full input; components emit only on terminal buffered flush.

Per-operator key lists live in the atomic `op-group-*` skills and `ComponentSchema` (e.g. `GROUP_QUANTILE` is `None` — terminal-only; `GROUP_SET_PER_ELEMENT`'s `total_label_observations` may exceed `total_n`). Full Components contract — typed shells, streaming chunk behaviour — lives in `response-components`.

## Gotchas

- `GROUP_SET_PER_ELEMENT`: `sum(buckets[].count) > total_n` is correct.
- Empty-mask `set_*`: `GROUP_SET_VALUE` buckets under the empty key; `GROUP_SET_PER_ELEMENT` skips. Neither increments `n_null`.
- `GROUP_DATE` with `day_of_week` emits weekday names that lex-sort, not Sun→Sat — sort explicitly.
- Range / rounded / quantile reject categorical fields at construction.
- `GROUP_QUANTILE` forces buffered execution.

## See

- Recipes: `pulse_examples_search tags=["cohort-analysis"]`, `tags=["cross-tabulation"]`, `tags=["distribution-shape"]`, `tags=["survey"]` plus atomic `op-group-<name>`.
- `crosstab-guide` — `Request.Crosstab` shape, margins, normalisation.
- `aggregation-design` — what folds inside the bucket.
- `request-envelope` — slot keys, smart defaults.
- `response-components` — typed `GrouperComponents` + mergeability axis.
- `streaming-and-watching` — fused vs buffered paths, streamability.
