---
name: grouper-design
description: Grouper slot semantics — multi-grouper composition (key product), fused crosstab eligibility, Group.Include inclusion-list, smart defaults per field type. Topical design; per-GROUP detail lives in atomic op-group-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [GROUP, Crosstab, groups]
---

# Grouper design

`groups` partitions records BEFORE aggregation; each `aggregations` entry folds inside the bucket. Per-grouper detail lives in the atomic `op-group-*` skills.

## Slot identity

`groups` entry: `{type, field, label?, interval?, params?, include?}`. One bucket key per post-filter row; output rows carry it as `<field>` (or `label`).

Pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Grouping runs AFTER attributes, so derived columns are addressable as `field`.

## Choosing a grouper

Read the intent first (`pulse_skills_get intents`): `compare_groups`, `composition`, `change_over_time`, `segment` or `distribution_shape`. Manifest `components.groupers[]` carries each grouper's `intents`, `accepts_types` and `params` — filter on the field's type, then decide:

- **One bucket per distinct value** of a categorical, boolean or low-cardinality field? → the category grouper.
- **A number in bands?** Fixed-width bands with a `"low-high"` key, or bands keyed by their rounded-down floor — or EQUAL-COUNT bands (by rank, so equal values may straddle two)? Equal-count bands buffer the whole input.
- **Calendar periods** (hour, day, week — ISO or any start day, month, quarter, fiscal year, weekday)? → the date grouper. **Custom named periods** (campaign windows, irregular fiscal quarters)? → the date-ranges grouper.
- **A multi-select field?** One bucket per OPTION (a record lands in every option it chose — fan-out) or one bucket per exact COMBINATION?

Each grouper's `Purpose.NotFor` names the sibling to use when the choice is wrong.

## Composition (key product)

With N entries (N ≥ 2), the engine forms the cartesian **key product**: each row receives a composite key `(g0, ..., gN-1)`. Empty `groups` collapses to one global bucket.

Plain `groups` emit one flat row per composite key under `Response.Data` — the canonical cross-tab mechanism. Reach for `Request.Crosstab` only when margins or normalisation matter<!-- feature: capability:crosstab --> (`crosstab-guide`)<!-- /feature -->.

## Smart defaults

When an entry names `field` but omits `type`, the engine infers from the schema type:

| Field type | Default grouper |
|---|---|
<!-- feature: GROUP_RANGE -->
| numeric (`u4`/`u*`, `f32`/`f64`, `decimal128`) | `GROUP_RANGE` (Interval=10) |
<!-- /feature -->
<!-- feature: GROUP_CATEGORY -->
| `categorical_*`, `packed_bool` | `GROUP_CATEGORY` |
<!-- /feature -->
<!-- feature: GROUP_DATE -->
| `date`, `datetime` | `GROUP_DATE` (`component=day`) |
<!-- /feature -->
<!-- feature: GROUP_SET_PER_ELEMENT -->
| `set_*` | `GROUP_SET_PER_ELEMENT` |
<!-- /feature -->

Never overrides an explicit `type`; predict reports filled slots at `data.defaults_applied`; disable via `--no-defaults`. Full table: `request-envelope`.

## `Group.Include` — inclusion list

`Group.Include []string` restricts a grouper to an allow-list of bucket keys. A row whose key (for a fan-out grouper, each fan-out key) is not listed is skipped — identical to the null-skip path. Empty / nil → "no filter". Supported by:

- <!-- feature: GROUP_CATEGORY -->`GROUP_CATEGORY` — the label string.<!-- /feature -->
- <!-- feature: GROUP_SET_VALUE -->`GROUP_SET_VALUE` — the pipe-joined composite key.<!-- /feature -->
- <!-- feature: GROUP_SET_PER_ELEMENT -->`GROUP_SET_PER_ELEMENT` — each fan-out label.<!-- /feature -->

Other groupers ignore `include` — filter the source field with a value filter instead. Streamability is preserved (O(1) membership per record).

**Order-significant.** A non-empty `include` also fixes emission order: buckets appear in listed order (plain grouped `Data` + the grouper's `buckets` component, and each crosstab axis independently). Empty / nil keeps alphabetical (or dictionary-index) order. Zero-record include values are still dropped.

## Fused crosstab eligibility

A crosstab builds its grid in one decode pass (much lower peak memory) when every axis grouper keys records one at a time. Every built-in grouper does, EXCEPT the equal-count (quantile) grouper, which needs a finalize-time sorted view — the only grouper that forces a buffered crosstab. Fan-out groupers fuse at any axis position, on either or both axes; axis keys are the product of each position's key set. Overlays never prevent fusing (<!-- feature: capability:crosstab -->`crosstab-guide`, <!-- /feature -->`overlay-system`).

Embedder groupers opt in by implementing `extend.StreamingGrouper` or `extend.MultiKeyStreamingGrouper` and returning `extend.ErrGrouperKeyNull` on nulls; implementing neither stays correct and runs buffered.

## Components

Every `Response.Components.Groupers[i]` carries the **universal floor** `total_n` (post-filter records partitioned) + `n_null` (records that took the null / skip path) — ALL post-filter records, unlike the aggregator's non-null `n`. Operator-specific keys ride `operator`; per-grouper lists are in `manifest.components_schemas.groupers` and each atomic skill.

Components mergeability is read off the manifest: `mergeable` groupers fold across streaming chunks; a `none` grouper (equal-count bands) emits its keys only at the terminal flush (`buffered_components: true` in predict). Shape: `response-components`.

## Gotchas

- Fan-out: `sum(buckets[].count) > total_n` is correct; on a crosstab axis its margins are non-additive (a 3-option row counts 3× across row margins, once in the grand total).
- Empty-mask `set_*`: the combination grouper buckets it under the empty key; the per-option grouper skips it. Neither increments `n_null`.
- Weekday components emit names that lex-sort, not Sun→Sat — sort explicitly.
- Band groupers reject categorical fields at construction.

## See

- Recipes: `pulse_examples_search tags=["cohort-analysis"|"cross-tabulation"|"distribution-shape"|"survey"]`.
- <!-- feature: capability:crosstab -->`crosstab-guide` (Crosstab shape, margins, normalisation), <!-- /feature -->`aggregation-design` (what folds inside the bucket), `request-envelope` (slot keys, smart defaults), `response-components` (grouper floor), `streaming-and-watching` (streamability).
