---
name: tool-examples-get
kind: tool
description: Fetch one runnable request example from the embedded library by name.
type: reference
applies_to: mcp
---

## When to use

After `pulse_examples_search` identifies a matching template. Returns the full record including a runnable `body` with the `_meta` annotation block already stripped — hand it straight to `pulse_predict` or `pulse_process` after renaming fields for the target cohort.

## Input

- `name` (string, required): example name from the `_meta.name` field (e.g. `t_test_one_sample`, `ols_simple`).

## Output

`descriptor.Envelope` wrapping the example record: `name`, `category`, `tags`, `operators`, `description`, `body` (runnable `types.Request` JSON). Missing example → MCP error `example "<name>" not found`.

## Gotchas

- `body` is `types.Request` — for `ComposedRequest` / `ChainRequest` / `FacetRequest` examples the library category encodes the target endpoint; the body is still a single-shape JSON, not a tagged union.
- Field names in the body match the example's seed cohort. Rename them for your target cohort before calling `pulse_process` / `pulse_predict`.
- The `_meta` block is stripped on emit so the body is directly runnable; the surrounding record carries the same metadata externally.

## See

- `tool-examples-search` — discovery sibling.
- `tool-process` / `tool-predict` — downstream execution.
- `request-envelope` — slot map for rebinding to a new cohort.
