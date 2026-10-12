```yaml
name: grouper-design
description: Grouper slot semantics — multi-grouper composition (key product), fused crosstab eligibility, Group.Include inclusion-list, smart defaults per field type. Topical design; per-GROUP detail lives in atomic op-group-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [GROUP, Crosstab, groups]
```

# Grouper design

`groups` partitions records BEFORE aggregation; each `aggregations` entry folds inside the bucket. Per-grouper detail: atomic `op-group-*` skills.

## Slot identity

`groups` entry: `{type, field, interval?, params?, include?}`. Output rows carry the bucket key as `<field>`.

Pipeline: `features → filterers → attributes → groups → aggregations → windows → sort`; grouping runs AFTER attributes, so derived columns work as `field`.

## Choosing a grouper

Read the intent first (`pulse_skills_get intents`): `compare_groups`, `composition`, `change_over_time`, `segment` or `distribution_shape`. Manifest `components.groupers[]` carries each grouper's [`intents`](intents.md), `accepts_types` and `params`; filter on the field's type, then decide:

- **One bucket per distinct value** of a categorical, boolean or low-cardinality field? → the category grouper.
- **A number in bands?** Fixed-width bands (`"low-high"` key or rounded-down floor), or EQUAL-COUNT bands (by rank; equal values may straddle two; buffers the whole input)?
- **Calendar periods** (hour to fiscal year, weekday)? → the date grouper. **Custom named periods** (campaigns, irregular fiscal quarters)? → the date-ranges grouper.
- **A multi-select field?** One bucket per OPTION (fan-out: a record lands in every option it chose) or per exact COMBINATION?

**High cardinality.** Each distinct key is a bucket; past the instance `max_groups` the run is refused `PULSE_LIMIT_EXCEEDED`; `pulse_predict` flags a dictionary over it in `limit_findings`.

## Composition (key product)

With N ≥ 2 entries a row's key is the tuple `(g0, ..., gN-1)`; this is the canonical cross-tab mechanism (`Request.Crosstab` only when margins or normalisation matter — [`crosstab-guide`](crosstab-guide.md)). Empty `groups` = one bucket.

- **Rows:** one per OBSERVED combination (never an empty one), in each grouper's native key order, outer → inner. Every row carries every grouper's key column.
- **Null key** at any position drops the record from every row; each grouper still sees it once, so `n_null` matches its single-grouper run.
- **Fan-out** groupers multiply: a record lands in each key-set product.
- **Whole-set fit:** `GROUP_QUANTILE` and `GROUP_KMEANS` fit once over the filtered set, then cross; position never changes a record's band.
- **Refused:** two entries on one key column (`SERVICE_VALIDATION`); an extension key containing NUL (`PROCESSING_GROUP`); `OVERLAY_YOY` with N ≥ 2 (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`).
- **`max_groups`:** predict reports the PRODUCT of per-grouper estimates, an upper bound; the run enforces it on observed ones.
- **Downstream:** `sort`, `return`, windows, post-tests and grouped matrices follow the composite rows. Series overlays (YoY aside) run once per partition: the leading keys (`params.partition_by`; default all but the last, so put the date grouper last), never crossing a segment ([`overlay-system`](overlay-system.md)).

## Smart defaults

When an entry names `field` but omits `type`, the engine infers from the schema type:

| Field type | Default grouper |
|---|---|
| numeric (`u4`/`u*`, `f32`/`f64`, `decimal128`) | `GROUP_RANGE` (Interval=10) |
| `categorical_*`, `packed_bool` | `GROUP_CATEGORY` |
| `date`, `datetime` | `GROUP_DATE` (`component=day`) |
| `set_*` | `GROUP_SET_PER_ELEMENT` |

Never overrides an explicit `type`; predict reports filled slots at `data.defaults_applied`; `--no-defaults` disables. Table: [`request-envelope`](request-envelope.md).

## `Group.Include` — inclusion list

`Group.Include []string` allow-lists bucket keys. A row whose key (each fan-out key, for a fan-out grouper) is not listed is skipped, as on the null path. Empty / nil = no filter. Supported by:

- `GROUP_CATEGORY` — the label string.
- `GROUP_SET_VALUE` — the pipe-joined composite key.
- `GROUP_SET_PER_ELEMENT` — each fan-out label.

Other groupers ignore `include`; use a value filter instead.

**Order-significant.** A non-empty `include` also fixes emission order: listed order (grouped `Data`, the `buckets` component, each crosstab axis). Empty keeps alphabetical (or dictionary-index) order. Zero-record values are still dropped.

## Fused crosstab eligibility

A crosstab builds its grid in one decode pass (much lower peak memory) when every axis grouper keys records one at a time. Every built-in does EXCEPT the equal-count (quantile) grouper, which needs a finalize-time sorted view and forces a buffered crosstab. Fan-out groupers fuse at any axis position. Overlays never prevent fusing.

## Components

Every `Response.Components.Groupers[i]` carries the **universal floor** `total_n` (post-filter records partitioned) + `n_null` (records that took the null / skip path) — ALL post-filter records, unlike the aggregator's non-null `n`. Operator-specific keys ride `operator`; per-grouper lists are in `manifest.components_schemas.groupers` and each atomic skill.

Mergeability is in the manifest: `mergeable` groupers fold across streaming chunks; a `none` grouper (equal-count bands) emits at the terminal flush only. With N ≥ 2 there is one entry per slot and `groups[].group_key` is the tuple. Shape: [`response-components`](response-components.md).

## Gotchas

- Fan-out: `sum(buckets[].count) > total_n` is correct; on a crosstab axis its margins are non-additive (a 3-option row counts 3× across row margins, once in the grand total).
- Empty-mask `set_*`: the combination grouper buckets it under the empty key; the per-option grouper skips it. Neither increments `n_null`.
- Weekday names lex-sort, not Sun→Sat; sort explicitly.

## See

- Recipes: `pulse_examples_search tags=["cross-tabulation"|"survey"]`.
- [`crosstab-guide`](crosstab-guide.md) (Crosstab shape, margins, normalisation), [`aggregation-design`](aggregation-design.md) (what folds inside the bucket), [`request-envelope`](request-envelope.md) (slot keys, smart defaults), [`time-zones`](time-zones.md) (datetime day buckets, `hour`, `week_start`), [`response-components`](response-components.md) (grouper floor), [`streaming-and-watching`](streaming-and-watching.md) (streamability).
