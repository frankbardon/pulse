```yaml
name: op-overlay-matrix-group-summary
kind: operator
category: OVERLAY
operator: OVERLAY_MATRIX_GROUP_SUMMARY
description: Summarize a grouped matrix result across its group buckets — per-cell mean, min, max, range and contributing-group count; the results are never changed.
type: reference
applies_to: process
examples_tags: [overlay, correlation]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Summarize a grouped matrix across its groups: each cell's mean, minimum, maximum and range, and how many groups fed it.

Questions it answers:

- How stable is each correlation across regions: what is its average, and how far apart are the extremes?
- Which pairs of measures relate strongly in some segments but weakly in others?

Use something else:

- `MAT_CORRELATION` when you want one matrix over every row, ignoring the groups.

## Params

`Scope` `matrix`. `Ref` `{"matrix": {"name": "<spec name>"}}` — the spec's `name`, else `<TYPE>_<vector>` / `<TYPE>`. `Level`/`Within` `0`. No params.

## Host shape

MATRIX_RESULT: a grouped request (`groups`) carrying the named matrix spec and no crosstab, on a process request or a Compose slot. Every group bucket's `primary` is read (`upper` expanded first), after any multiplicity correction.

## Output

Primary MATRIX: per-cell unweighted mean across buckets. Parts `min`, `max`, `range`, `n_groups` (MATRIX each). Keys: the union of the buckets' row / column keys.

## Reading the output

- `cells.value`: The cell's average across the groups: each group's matrix value for the pair, averaged with every group counting once.
  - Caveat: Read it beside parts.range: a mean over groups that disagree hides that disagreement.
- `parts.range.cells.value`: How far apart the groups sit on the cell: the largest group value minus the smallest (0 when one group fed it).
- `parts.n_groups.cells.value`: How many groups had a defined value for the cell; null cells elsewhere in the layer have 0 here.

## Gotchas

- A null cell, or a key a bucket lacks, is skipped; no contributing bucket ⇒ `null` (`n_groups` 0).
- Every bucket counts once, whatever its size.
- No groups ⇒ `PULSE_OVERLAY_GROUPS_REQUIRED`; unknown name ⇒ `PULSE_OVERLAY_REFERENCE_UNKNOWN`.
- Refused on a chain stage and on the Compose, chain and facet overlay slots.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-mat-correlation`](op-mat-correlation.md), [`multivariate-design`](multivariate-design.md).
