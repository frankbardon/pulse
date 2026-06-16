---
name: tool-examples-search
kind: tool
description: Search the runnable request-example library for templates matching a question.
type: reference
applies_to: mcp
---

## When to use

CALL BEFORE AUTHORING A REQUEST. Find a runnable template that matches the user's question; clone its body via `pulse_examples_get` and rename fields for the target cohort. The example library is curated, runnable, and stays in lockstep with the operator surface — prefer it over inferring request shapes from documentation or source code.

## Input

- `query` (string, optional): case-insensitive substring across name, description, and operators.
- `tags` (string[], optional): ANDed list of canonical taxonomy tags (e.g. `time-series`, `experiment-analysis`, `tier-1-test`, `regression`, `ols`, `logistic`).
- `category` (string, optional): exact directory — `aggregations`, `attributes`, `features`, `filterers`, `groupers`, `regression`, `tests`, `windows`.

## Output

`descriptor.Envelope` wrapping lightweight summaries: `name`, `category`, `tags`, `operators` (sorted), `description`. Empty filter returns the full library list. Use the `name` to fetch the runnable body via `pulse_examples_get`.

## Gotchas

- Tags are ANDed, not ORed. Two tags → results carrying both.
- `category` is an exact directory match, not a substring.
- The summary does NOT include the runnable body; you MUST follow up with `pulse_examples_get`.

## See

- `tool-examples-get` — fetch runnable body for one named example.
- `session-bootstrap` — example-library role in the request-authoring workflow.
- `tool-manifest` — operator catalog companion.
