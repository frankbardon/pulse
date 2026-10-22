---
name: tool-examples-search
kind: tool
description: Search the runnable request-example library for templates matching a question.
type: reference
applies_to: mcp
---

## When to use

CALL BEFORE AUTHORING A REQUEST.
Find a runnable template matching the question; clone its body via `pulse_examples_get` and rename fields for the target cohort. Examples are runnable and in lockstep with the operators — prefer them over inferring request shapes. The library is this server's own: shipped examples plus any the embedder registered, minus any using a feature this server hides.

## Input

- `query` (string, optional): plain words. One word is a case-insensitive substring of name, description or operators; several must each match a whole word (no stemming) of name, description, operators, intents or tags. Fallbacks, in order: an operator alias (`anova`, `pearson`) or intent phrase (`move together`); then a near-miss spelling (`regon` → `region`; one edit at 4–7 letters, two at 8+, exact at ≤3).
- `intent` (string, optional): an intent-taxonomy ID (`compare_groups`, `relationship`, …); results must declare it. Unknown: `PULSE_RECOMMEND_INTENT_UNKNOWN`, `details.valid` lists the IDs.
- `tags` (string[], optional): ANDed list of canonical taxonomy tags (e.g. `time-series`, `experiment-analysis`, `tier-1-test`, `regression`, `ols`, `logistic`).
- `category` (string, optional): exact directory — `aggregations`, `attributes`<!-- feature: capability:compose_sweep -->, `compose`<!-- /feature -->, `crosstab`, `facet`, `features`, `filterers`, `groupers`<!-- feature: capability:matrices -->, `matrices`<!-- /feature -->, `overlays`, `regression`, `tests`, `windows`.

## Output

`descriptor.Envelope` wrapping lightweight summaries: `name`, `category`, `tags`, `operators` (sorted), `description`, `intents` (absent when none), `question` + `interpretation` (each when declared — a story declares both). Ranked by distinct matched fields, then name. Empty filter returns the full library list. Use the `name` to fetch the runnable body via `pulse_examples_get`.

## Gotchas

- Tags are ANDed, not ORed. Two tags → results carrying both.
- `category` is an exact directory match, not a substring.
- Literal matches win over aliases, aliases over near-misses.
- Embedder examples (own categories too) search and prune like built-ins; the `pulse examples search` CLI leaf reads the built-in library only.
- The summary does NOT include the runnable body; you MUST follow up with `pulse_examples_get`.

## See

- `tool-examples-get` (the runnable body), `session-bootstrap` (the authoring workflow), `tool-manifest`.
