```yaml
name: windows-first
description: Windows-first — aggregate, group or cross-tabulate a window column (a lag, change, running total or adstock) by running the windows over the records before the groups and aggregations; triggers, weights, refusals and the predict plan.
type: guide
kind: design
applies_to: process, compose, predict
covers: [WIN, windows, aggregations, groups, crosstab, post_window]
```

# Windows-first

By default windows run AFTER the groups and aggregations, over their output rows (a lag of a grouped total). A request runs **windows-first** when one of its aggregations, groupers, or crosstab axes / cells / margin aggregations reads a window label as `field` (or as a built-in column param) — directly, through a post-window attribute that reads one ([`attribute-composition`](attribute-composition.md)), or through a bound model whose terms read one. The windows then run over the filtered record rows first, and the groups, aggregations and crosstab read the window column off each record, exactly as they would read a stored cohort column. Nothing to switch on: the read is the trigger.

## Ungrouped

The mean order-to-order change in revenue:

```json
"windows": [{"type": "WIN_DELTA", "field": "revenue", "label": "revenue_change",
             "order_by": [{"field": "order_date"}, {"field": "id"}], "params": {"periods": 1}}],
"aggregations": [{"type": "AGG_AVERAGE", "field": "revenue_change", "label": "mean_change"}]
```

The first record has no predecessor: its change is null, skipped by the aggregation and counted in its Components `n_null`.

## Grouped

The running revenue total reached by each quarter's end:

```json
"windows": [{"type": "WIN_RUNNING_SUM", "field": "revenue", "label": "revenue_to_date",
             "order_by": [{"field": "order_date"}, {"field": "id"}],
             "frame": {"mode": "rows", "preceding": null, "following": 0}}],
"groups": [{"type": "GROUP_DATE", "field": "order_date", "params": {"component": "quarter"}}],
"aggregations": [{"type": "AGG_MAX", "field": "revenue_to_date", "label": "revenue_to_date"}]
```

The windows see every filtered record in one sequence, not one per group. To restart a sequence per group (a per-market lag), put the group field in the window's `partition_by`. A grouper may key on a window label too.

## Crosstab

Axes, cells and margins partition the window-carrying records. A crosstab window no axis, cell or margin aggregation reads is `PULSE_WINDOW_INVALID` `details.reason: "crosstab_window_unread"` `{window_index}`: a crosstab writes no output row for a window to run over. Regressions beside a crosstab run on the buffered path and are fitted.

## Weights

A window has no weighted form, so a request-level `weight` on a request with windows is `PROCESSING_CONFIG` (`… has no weighted form and carries no slot weight; drop the request weight`); an instance default weight is skipped on the window. Weight the aggregations instead: a slot `weight` on each aggregation.

## Output and predict

- The response rows are the aggregation / group / crosstab rows; no window column is emitted. Components are in declared slot order, as on any request.
- Always buffered, never the fused crosstab. Predict says so in `streamable_reasons` and reports the plan in `post_window` `{windows_first, window_labels, attributes}` ([`tool-predict`](tool-predict.md)).

## Gotchas

- A sort key or post-test naming a window label is refused: the output rows carry only group keys and aggregation labels. So is a window reading an aggregation's output label (`windows_first_reads_aggregate`), a window-label `weight`, and a tier-1 test reading a window label.
- Not windows-first, a window naming a record column (`field`, `order_by`, `partition_by`) is refused `details.reason: "window_over_output_rows"`: the output rows carry only group keys and aggregation labels.
- Built-in windows refuse a categorical field, so a grouper cannot key on a lagged category (`PULSE_FIELD_TYPE_UNSUPPORTED`); an extension window writing strings is `PROCESSING_CONFIG` `{field, value_type}`.
- A window label read only through an extension aggregator's own field inputs does not trigger the mode: it is an unknown field. Name the window label in the slot's `field`.
- A window label equal to a schema field or vector name is that field, not the window.

## See

- `pulse_examples_search tags=[window-operator, buffered-pipeline]`
- [`window-design`](window-design.md) — partition, order, frame.
- [`attribute-composition`](attribute-composition.md) — post-window attributes.
- [`aggregation-design`](aggregation-design.md), [`grouper-design`](grouper-design.md), [`crosstab-guide`](crosstab-guide.md), [`weighting`](weighting.md).
- [`mmm-workflow`](mmm-workflow.md) — contribution by quarter.
