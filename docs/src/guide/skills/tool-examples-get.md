```yaml
name: tool-examples-get
kind: tool
description: Fetch one runnable request example from this server's library by name.
type: reference
applies_to: mcp
```

## When to use

CALL AFTER A SEARCH FINDS A MATCHING EXAMPLE, TO GET ITS RUNNABLE BODY.
The full record's `body` has the `_meta` annotation block already stripped — hand it straight to `pulse_predict` or `pulse_process` after renaming fields for the target cohort.

## Input

- `name` (string, required): example name from the `_meta.name` field (e.g. `t_test_one_sample`).

## Output

The example record (no envelope): `name`, `category`, `tags`, `operators`, `description`, `intents`, `question` + `interpretation` (each when declared — a story declares both; absent otherwise), `body` (the runnable request as a JSON object). Missing example → coded tool error `SERVICE_VALIDATION` (`details.argument: "name"`, `details.name`) — also what an example using a feature this server hides answers. Serves the same library as `pulse_examples_search` (embedder examples included).

## Gotchas

- `body` is the request of the tool that runs it, told apart by its root keys, not by `category`: `requests` → `pulse_compose`, `stages` → `pulse_process_chain` (chain examples sit under `overlays` too), `fields` → `pulse_facet_schema`, `output` → `pulse_materialize`, otherwise a plain request. To predict a non-plain body, pass it under `pulse_predict`'s `composed` / `chain` / `facet` root.
- Field names in the body match the example's seed cohort. Rename them for your target cohort before calling `pulse_predict` / `pulse_process`.
- The body's `cohort` names the example's fixture cohort by its filename relative to this server's data directory (no `data_dir`); point it at your cohort (`pulse_cohorts_list`). To run it unchanged, the server's data directory must hold the fixture cohorts: `pulse examples data --out DIR` (or `Pulse.WriteExampleData`) writes every one from data embedded in the binary; serve it with `pulse mcp --data-dir DIR`.

## See

- [`tool-examples-search`](tool-examples-search.md) — discovery sibling.
- [`tool-process`](tool-process.md) / [`tool-predict`](tool-predict.md) — downstream execution.
- [`request-envelope`](request-envelope.md) — slot map for rebinding to a new cohort.
