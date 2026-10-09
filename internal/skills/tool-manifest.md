---
name: tool-manifest
kind: tool
description: Bootstrap blob — call once per session for the operator + capability catalog.
type: reference
applies_to: manifest, mcp
---

## When to use

CALL FIRST IN EVERY SESSION.
Cache the result; reference it for every request-authoring decision. Source of truth for every operator, tool and error code THIS deployment offers. Pair with `pulse_examples_search` for runnable templates.

## Input

Optional `intent` (an intent-taxonomy ID, e.g. `compare_groups`): the manifest scoped to it — only operators serving it, its ranked skills, the intent record in `scope.intent`. Fixed sections (commands, error codes, cohort types, tool list, limits, `components_schemas`, ...) are absent and named in `elided`. Cache per intent. Unknown: `PULSE_RECOMMEND_INTENT_UNKNOWN`. (CLI: `pulse manifest --json` with `--slim` = the MCP payload, `--intent ID`.)

## Output

`descriptor.Envelope`; `data` = `commands`, `components`, `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes*`, `mcp_tools`, `cohort_types`, `skills`, `extensions`, capability blocks (`facet`, `join`, `process_chain`, `crosstab`, `export`, `import`, `overlays`; absent when not offered), `limits` (`{name, value, default, unit}`, `-1` = none), `feature_set_digest` + `limits_digest` — the cache key with the build version (and the intent). Sort-stable.

## Gotchas

- MCP always serves the slim payload (no prose). Fetch per-operator prose via `pulse_skills_get` and per-error prose via `pulse_errors_lookup` on demand.
- Operator names in `components.aggregators[]` / `components.groupers[]` are the manifest catalog, NOT the request slot keys — request bodies use `"aggregations"` / `"groups"`<!-- feature: capability:process --> (see `tool-process` Gotchas)<!-- /feature -->.
- Each operator entry carries its `component_schema`; the unscoped manifest also keys them by name under `components_schemas`.

## See

- `session-bootstrap` — bootstrap workflow and field cross-reference.
- `request-envelope` — `format_version` semantics and the standard envelope shape.
- `response-components` — `ComponentSchema` contract returned for emitting operators.
