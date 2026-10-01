---
name: tool-process-chain
kind: tool
description: Linear chain of stages; stage N+1 consumes stage N's output rows.
type: reference
applies_to: process-chain, process, mcp
---

## When to use

Source-rooted linear chain — collapses N round-trips into one open + N stage validations. Common: "group-then-regress", "filter-then-aggregate-then-attribute". Each stage's synthesized output schema feeds the next stage as its cohort.

## Input

`request` (string): JSON-encoded `pulse.ChainRequest`. Fields: `cohort.filename` (source for stage 0), `stages` ([]ChainStage with `name` + `request`). Stage 0 supplies the source cohort; later stages ignore their inner cohort field.

## Output

`descriptor.Envelope` wrapping per-stage `Response`s plus a whole-chain summary. Each stage's `Components` populates as usual. Chain-host overlays fold at the post-chain barrier.

## Gotchas

- MERGEABLE-ONLY at v1. Permitted: `AGG_COUNT`/`SUM`/`AVERAGE`/`MIN`/`MAX`/`RANGE`/`VARIANCE`/`STDDEV`/`DISTINCT_COUNT`/`NULL_COUNT`, `GROUP_CATEGORY`/`GROUP_RANGE`, row-local attributes (`ATTR_FORMULA`, `ATTR_DATE_PART`). Windows, features, statistical tests, regressions, two-pass attributes, `AGG_FREQUENCY`/`AGG_MODE`, and non-mergeable aggregators/groupers → `PULSE_CHAIN_NOT_MERGEABLE`. Fall back to `pulse_compose` / per-stage `pulse_process`.
- Synthesized schema between stages: grouper keys → `categorical_u32` columns; aggregator outputs → `f64`.

## See

- `process-chain` — full mergeability matrix and recipe library.
- `response-components` — per-stage emission contract.
- `overlay-system` — CHAIN-host overlays.
