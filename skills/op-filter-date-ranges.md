---
name: op-filter-date-ranges
description: Keep records whose date field falls inside any of a validated set of labeled date ranges.
kind: operator
category: FILTER
operator: FILTER_DATE_RANGES
type: reference
applies_to: process, compose, predict, facet, sample
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

`Params.ranges` (`[]{label,start,end}`): inline labeled ranges. `start`/`end` are ISO literals (`YYYY-MM-DD`); omit/null either for an open bound. Bounds inclusive. Fully validated. Ranges ride `Params json.RawMessage` — not `Values []string`.

## Inputs

`Field` — `date` only; non-`date` → `PROCESSING_CONFIG`.

## Output

Row-level predicate. Keep when the record's day-integer lies in any range; drop otherwise. The `label` is validated but plays no role in keep/drop. No emitted column.

## Components

Floor only. Universal `{n_in, n_out, n_null_input}` per `response-components`. Mergeable; counters fold by addition.

## Gotchas

- Null/missing date → dropped.
- Empty `ranges` → `PULSE_RANGE_EMPTY`; overlap/dup → `PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL`; bad literal or start>end → `PULSE_RANGE_INVALID`.
- Row-local streamable — auto-available to `facet` (`FacetRequest.Filterers`) and `sample`, single-pass.
- Named `table:` source not wired here (inline only).

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `op-group-date-ranges`, `response-components`, `facet-design`
