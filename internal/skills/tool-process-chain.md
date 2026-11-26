---
name: tool-process-chain
kind: tool
description: Linear chain of stages; stage N+1 consumes stage N's output rows.
type: reference
applies_to: process-chain, process, mcp
---

## When to use

CALL WHEN EACH STAGE SHOULD READ THE PREVIOUS STAGE'S OUTPUT ROWS.
Collapses N round-trips into one open + N stage validations. Common: "group-then-regress", "filter-then-aggregate-then-attribute". Each stage's synthesized output schema is the next stage's cohort.

## Input

The chain's keys at top level (no wrapper): `cohort` (`{filename}`, the source<!-- feature: capability:union -->, or `union`<!-- /feature -->), `stages` (`[{name, request}]`; a slot placed directly on a stage is `PULSE_REQUEST_UNKNOWN_FIELD`), optional `overlays` (CHAIN-host)<!-- feature: capability:params -->, `params`<!-- /feature -->. Every stage reads the one before; a `cohort` inside a stage's `request` is ignored. A stage's `request.return` shapes that stage only after the whole chain ran (the next stage still reads full rows); `final` follows the last stage's.

## Output

`{stages: [...], final, overlays}` (no envelope): one response per stage in order (a process response), `final` = the last stage's, `overlays` = the CHAIN-host layers folded after the last stage.

## Gotchas

- MERGEABLE-ONLY. Permitted: exactly the manifest `process_chain` block's `mergeable_aggregators`, `mergeable_groupers` and `row_local_attributes`. Windows, features, statistical tests, regressions, two-pass attributes and non-mergeable aggregators/groupers → `PULSE_CHAIN_NOT_MERGEABLE` (`details.stage_index`, `stage_name`); the stage count is capped by `process_chain.max_stages`. Fall back to per-stage requests<!-- feature: capability:compose --> (batched by `pulse_compose`)<!-- /feature -->.
- Synthesized schema between stages: grouper keys → `categorical_u32` columns; aggregator outputs → `f64` (an expanded `AGG_VEC_*`: one per element column `<label>[<elem>]`); a NaN cell → null.

## See

- `process-chain` — full mergeability matrix and recipe library.
- `response-components` — per-stage emission contract.
- `overlay-system` — CHAIN-host overlays.
