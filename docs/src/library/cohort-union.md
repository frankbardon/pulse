# Cohort Union

A **cohort union** reads several cohorts as one record stream. Each
member is a cohort and a label; every record carries its member's label
in a virtual categorical field, so a single request can group, filter,
test or regress across cohorts — survey waves, regional files, a
before / after pair, or a real cohort beside its synthetic twin —
instead of running one Compose slot per cohort and stitching the
results with overlays.

```go
resp, err := p.Process(ctx, &pulse.Request{
	Union: []types.UnionMember{
		{Cohort: &types.Cohort{Filename: "wave1.pulse"}, Label: "2024"},
		{Cohort: &types.Cohort{Filename: "wave2.pulse"}, Label: "2025"},
	},
	Groups:       []*types.Group{{Field: types.DefaultUnionSourceField}}, // "_source"
	Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "spend"}},
})
```

The same request as JSON (`pulse api process`, MCP `pulse_process`):

```json
{
  "union": [
    {"cohort": {"filename": "wave1.pulse"}, "label": "2024"},
    {"cohort": {"filename": "wave2.pulse"}, "label": "2025"}
  ],
  "union_source_field": "wave",
  "groups": [{"field": "wave"}],
  "aggregations": [{"type": "AGG_AVERAGE", "field": "spend"}]
}
```

## The request shape

- `union` replaces `cohort` on the root that carries it. Both set is
  `PULSE_UNION_COHORT_CONFLICT`.
- Each member needs a cohort and a label. Labels are non-empty and
  unique (`PULSE_UNION_LABEL_INVALID`); a member without a cohort is
  `PULSE_UNION_MEMBER_INVALID`.
- `union_source_field` names the virtual field (default `_source`). It
  is a non-nullable categorical appended after every member field; its
  dictionary is the labels in declared order (entry index = member
  index). Grouped output still follows the grouper's normal sort, not
  member order: members `zeta, alpha` group as `alpha, zeta`. Setting
  it without a
  union, or to a name a member already uses, is
  `PULSE_UNION_SOURCE_FIELD_INVALID`.

## How the members merge

A union is merged exactly like a shard archive, on the logical view of
each member (parent groups expanded): the first member is canonical;
field names, types, vector dims and categorical widths must agree; set
dictionaries widen; categorical dictionaries merge within their width;
nullability and the default zone field never change. A member that
breaks a rule is refused with `PULSE_UNION_SCHEMA_MISMATCH`, whose
details name the member, its label and the archive rule
(`archive_code`). Descriptions that differ produce a warning, not an
error. A member may be a shard archive: its shards are read in order,
and the flattened member count is capped at the shard-archive ceiling
(`PULSE_UNION_MEMBER_LIMIT`).

Nothing is written to disk: the union is an in-memory view, rebuilt per
request.

## Where a union is read

| Surface | Notes |
|---|---|
| `Process`, `ProcessStream` | streaming, buffered, crosstab and shard-parallel arms all read a union |
| `Compose`, `ComposeParallel` | any slot may read a union |
| `ProcessChain` | the chain root's `union` is stage 0's source |
| `FacetSchema` | facet the source field to count records per member |
| `SampleWithRequest` | rows in member order; MCP `pulse_sample` takes `union` in place of `path` |
| `Materialize` | the source field is written as an ordinary column |
| `Lookup` | always scans the members in order — a union never reads or writes a sidecar index |
| a join | the union is the LEFT side (the request's own cohort) |
| every predict root | header and schema only: `record_count` is the members' total, `union` lists each member |

A union is never a join's right side (`PULSE_UNION_JOIN_RIGHT`) and a
member is never itself a union (`PULSE_UNION_NESTED`); both are refused
when the request is decoded.

## Comparing a cohort with its synthetic twin

There are two recipes, and which one applies depends on where you are.

**Inside the secure zone: the tagged output.** `pulse synth
from-profile --source customers.pulse` (without `--synthetic-only`)
writes one cohort holding the source rows and the generated rows,
marked by the boolean `_synthetic` column. Group or split by it:

```json
{
  "cohort": {"filename": "customers_tagged.pulse"},
  "groups": [{"field": "_synthetic"}],
  "aggregations": [{"type": "AGG_AVERAGE", "field": "spend"}]
}
```

This needs no union, and it is the same split the `--fidelity-report`
is built on. The tagged cohort contains real rows: it must not leave the
zone.

**With a separate, shareable twin: the union.** Once the twin is its
own file (`--synthetic-only`), union it with the source and group by the
source field:

```json
{
  "union": [
    {"cohort": {"filename": "customers.pulse"}, "label": "source"},
    {"cohort": {"filename": "twin.pulse"}, "label": "twin"}
  ],
  "groups": [{"field": "_source"}, {"field": "region"}],
  "aggregations": [{"type": "AGG_AVERAGE", "field": "spend"}]
}
```

A test or crosstab over `_source` compares the two distributions
directly in one slot. Because the twin is written with the source's
schema, the members always merge.

## Feature profiles

The union slots ride `capability:union`. An instance whose feature
profile omits it has no `union` / `union_source_field` key on any root,
refuses either key as `PULSE_REQUEST_UNKNOWN_FIELD`, and lists no
`PULSE_UNION_*` code. See [Feature Profiles](feature-profiles.md).

## Observability

An operation over a union reports its `cohort` (log attribute,
`observe.OperationInfo.Cohort`, span attribute) as the members' cohort
paths in declared order joined by `+`. Labels are never recorded. See
[Observability](observability.md).
