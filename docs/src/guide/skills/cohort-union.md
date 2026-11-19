```yaml
name: cohort-union
description: Reading several labelled cohorts as one record stream — the union and union_source_field request slots, the virtual source field, the merge rules and refusals, which surfaces read a union, and comparing a real cohort with its synthetic twin in one slot.
type: guide
kind: design
applies_to: process, compose, sample, facet, predict
covers: [union, union_source_field, _source, UnionMember]
requires: [capability:union]
```

# Cohort union

A `union` reads several cohorts — waves, regions, before / after, a real cohort and its synthetic twin — as ONE record stream, in place of `cohort`. Every record carries its member's label in a virtual categorical field, so one slot groups, filters, tests or regresses across cohorts instead of one slot per cohort.

```json
{
  "union": [
    {"cohort": {"filename": "customers.pulse"}, "label": "source"},
    {"cohort": {"filename": "twin.pulse"}, "label": "twin"}
  ],
  "groups": [{"field": "_source"}],
  "aggregations": [{"type": "AGG_AVERAGE", "field": "spend"}]
}
```

## The source field

- Named by `union_source_field`, default `_source`; appended LAST to the merged schema.
- A non-nullable categorical whose dictionary is the labels in declared order (entry index = member index). Grouped output still follows the grouper's normal sort, not member order: members `zeta, alpha` group as `alpha, zeta`. Use it like any categorical field: group, filter, crosstab axis, test split, regression predictor.
- Labels: non-empty, unique. A member field already named like the source field is refused — rename the source field.

## Merge rules (the shard-archive rules)

The first member is canonical. Field names, types, vector dims and categorical widths must agree; set dictionaries widen; categorical dictionaries merge within their width; nullability and the default zone field never change. A disagreement is `PULSE_UNION_SCHEMA_MISMATCH` naming the member and label (`details.archive_code` is the archive rule it broke). Descriptions that differ only warn. A member may itself be a shard archive: its shards flatten into the union, and the flattened count is capped (`PULSE_UNION_MEMBER_LIMIT`, `manifest.union.max_members`).

## Where a union is read

The request's own root, on every surface that reads one cohort: process and its stream, each Compose slot, a process chain's root (stage 0's source), the rich facet, sample, materialize (the source field is written as a column), point lookup (always a scan — a union never reads a sidecar index) and a join's LEFT side. Predict sizes it from member headers alone: `record_count` is the members' total, `union` lists each member's count. `manifest.union.surfaces` lists the instance's surfaces.

Records arrive member by member in declared order, so an order-sensitive result (a window, a sample) follows that order unless you sort.

## Refusals

| Request | Code |
|---|---|
| `cohort` and `union` together | `PULSE_UNION_COHORT_CONFLICT` |
| member without a cohort | `PULSE_UNION_MEMBER_INVALID` |
| empty or repeated label | `PULSE_UNION_LABEL_INVALID` |
| `union_source_field` without `union`, or naming a member field | `PULSE_UNION_SOURCE_FIELD_INVALID` |
| a union on a join's right side | `PULSE_UNION_JOIN_RIGHT` |
| a member (or its cohort) carrying its own `union` | `PULSE_UNION_NESTED` |

Predict and the run refuse the same request with the same code; validate with `pulse_predict` first.

## Real vs synthetic twin

Two files, one slot: union the source and the twin, group by the source field, and read both sides in one response. A test or crosstab over the source field compares the two distributions directly — what a Compose of one slot per cohort could only approximate with overlays.

**Inside the secure zone, prefer the tagged output.** `synth from-profile` without `--synthetic-only` writes ONE cohort holding the source rows and the generated rows, told apart by the `_synthetic` boolean column. Group or split by `_synthetic` for the same comparison with no union at all — the fidelity report is built on exactly that. That cohort contains real rows: it never leaves the zone. The union is the recipe once the twin is a separate, shareable file (an analyst re-running checks against a twin; governance comparing the shared twin with the source it came from).

## Gotchas

- `union` replaces `cohort`; never send both.
- The source field is a field of the request: name it in `groups` / `filterers` exactly as `union_source_field` spells it.
- Weights resolve on the merged schema: every member needs the weight field.
- A point lookup over a union scans every member; build no index for it.
- Union members cannot be unions; list every cohort in the one union.

## See

[`cohort-sharding`](cohort-sharding.md) (the archive merge rules a union reuses) · [`join-design`](join-design.md) · [`compose-requests`](compose-requests.md) · [`facet-design`](facet-design.md) · [`synth-fidelity-report`](synth-fidelity-report.md) (the tagged output) · [`tool-sample`](tool-sample.md) · [`tool-lookup`](tool-lookup.md)
