---
name: tool-examples-search
kind: tool
description: Search the runnable request-example library for templates matching a question.
type: reference
applies_to: mcp
---

## When to use

CALL BEFORE AUTHORING A REQUEST.
Find a runnable template that matches the user's question; clone its body via `pulse_examples_get` and rename fields for the target cohort. The example library is curated, runnable, and stays in lockstep with the operator surface — prefer it over inferring request shapes from documentation or source code.

## Input

- `query` (string, optional): plain words. One word is a case-insensitive substring of name, description or operators. Several words must each match a whole word (lower-cased, no stemming) of name, description, operators, intents or tags. An operator alias (`anova`, `chisq.test`, `pearson`) or a phrase from an intent's sounds (`move together`) also finds that operator's or intent's examples when nothing matches literally.
- `intent` (string, optional): an intent-taxonomy ID (`compare_groups`, `relationship`, …); results must declare it. Unknown: `PULSE_RECOMMEND_INTENT_UNKNOWN`, `details.valid` lists the IDs.
- `tags` (string[], optional): ANDed list of canonical taxonomy tags (e.g. `time-series`, `experiment-analysis`, `tier-1-test`, `regression`, `ols`, `logistic`).
- `category` (string, optional): exact directory — `aggregations`, `attributes`, `crosstab`, `facet`, `features`, `filterers`, `groupers`<!-- feature: capability:matrices -->, `matrices`<!-- /feature -->, `overlays`, `regression`, `tests`, `windows`.

## Output

`descriptor.Envelope` wrapping lightweight summaries: `name`, `category`, `tags`, `operators` (sorted), `description`, `intents` (absent when none). Ranked by distinct matched fields, then name. Empty filter returns the full library list. Use the `name` to fetch the runnable body via `pulse_examples_get`.

## Gotchas

- Tags are ANDed, not ORed. Two tags → results carrying both.
- `category` is an exact directory match, not a substring.
- Literal matches win: synonyms are consulted only when no example matches the words themselves.
- A deployment may add examples for its own operators, under its own categories; they search, read and prune exactly like the built-in ones.
- The summary does NOT include the runnable body; you MUST follow up with `pulse_examples_get`.

## See

- `tool-examples-get` — fetch runnable body for one named example.
- `session-bootstrap` — example-library role in the request-authoring workflow.
- `tool-manifest` — operator catalog companion.
