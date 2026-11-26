---
name: tool-examples-get
kind: tool
description: Fetch one runnable request example from this server's library by name.
type: reference
applies_to: mcp
---

## When to use

CALL AFTER A SEARCH FINDS A MATCHING EXAMPLE, TO GET ITS RUNNABLE BODY.
The full record's `body` has the `_meta` annotation block already stripped — hand it straight to `pulse_predict`<!-- feature: capability:process --> or `pulse_process`<!-- /feature --> after renaming fields for the target cohort.

## Input

- `name` (string, required): example name from the `_meta.name` field<!-- feature: AGG_AVERAGE, AGG_COUNT, TEST_T --> (e.g. `t_test_one_sample`)<!-- /feature -->.

## Output

The example record (no envelope): `name`, `category`, `tags`, `operators`, `description`, `intents`, `question` + `interpretation` (each when declared — a story declares both; absent otherwise), `body` (the runnable request as a JSON object). Missing example → coded tool error `SERVICE_VALIDATION` (`details.argument: "name"`, `details.name`) — also what an example using a feature this server hides answers. Serves the same library as `pulse_examples_search` (embedder examples included).

## Gotchas

- `body` is the request of the tool that runs it, told apart by its root keys, not by `category`: `requests`<!-- feature: capability:compose --> → `pulse_compose`<!-- /feature -->, `stages`<!-- feature: capability:process_chain --> → `pulse_process_chain`<!-- /feature --> (chain examples sit under `overlays` too), `fields`<!-- feature: capability:facet --> → `pulse_facet_schema`<!-- /feature -->, `output`<!-- feature: capability:materialize --> → `pulse_materialize`<!-- /feature -->, otherwise a plain request. To predict a non-plain body, pass it under `pulse_predict`'s `composed` / `chain` / `facet` root.
- Field names in the body match the example's seed cohort. Rename them for your target cohort before calling `pulse_predict`<!-- feature: capability:process --> / `pulse_process`<!-- /feature -->.
- The body's `cohort` names the example's fixture cohort (`data_dir` included); point it at your own cohort.

## See

- `tool-examples-search` — discovery sibling.
- `tool-process` / `tool-predict` — downstream execution.
- `request-envelope` — slot map for rebinding to a new cohort.
