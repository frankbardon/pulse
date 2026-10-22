---
name: op-overlay-matrix-group-summary
kind: operator
category: OVERLAY
operator: OVERLAY_MATRIX_GROUP_SUMMARY
description: Summarize a grouped matrix result across its group buckets — per-cell mean, min, max, range and contributing-group count; the results are never changed.
type: reference
applies_to: process
examples_tags: [overlay, correlation]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` `matrix`. `Ref` `{"matrix": {"name": "<spec name>"}}` — the spec's `name`, else `<TYPE>_<vector>` / `<TYPE>`. `Level`/`Within` `0`. No params.

## Host shape

MATRIX_RESULT: a grouped request (`groups`) carrying the named matrix spec and no crosstab, on a process request or a Compose slot. Every group bucket's `primary` is read (`upper` expanded first)<!-- feature: capability:multiplicity -->, after any multiplicity correction<!-- /feature -->.

## Output

Primary MATRIX: per-cell unweighted mean across buckets. Parts `min`, `max`, `range`, `n_groups` (MATRIX each). Keys: the union of the buckets' row / column keys.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A null cell, or a key a bucket lacks, is skipped; no contributing bucket ⇒ `null` (`n_groups` 0).
- Every bucket counts once, whatever its size.
- No groups ⇒ `PULSE_OVERLAY_GROUPS_REQUIRED`; unknown name ⇒ `PULSE_OVERLAY_REFERENCE_UNKNOWN`.
- Refused on a chain stage and on the Compose, chain and facet overlay slots.

## See

- Skills: `overlay-system`, `op-mat-correlation`, `multivariate-design`.
