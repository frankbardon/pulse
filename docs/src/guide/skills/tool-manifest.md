```yaml
name: tool-manifest
kind: tool
description: Bootstrap blob — call once per session for the operator + capability catalog.
type: reference
applies_to: manifest, mcp
```

## When to use

CALL FIRST IN EVERY SESSION.
Unscoped it is hundreds of KB: scope by `intent`, cache per intent. Source of truth for every operator, tool and error code THIS deployment offers. Pair with `pulse_examples_search` for runnable templates.

## Input

Optional `intent` (an ID from the [`intents`](intents.md) skill): the manifest scoped to it — only operators serving it, its ranked skills, the intent record in `scope.intent`. Fixed sections (commands, error codes, cohort types, tool list, limits, `components_schemas`, ...) are absent and named in `elided`. Cache per intent. Unknown: `PULSE_RECOMMEND_INTENT_UNKNOWN`. (CLI: `pulse --json --slim` is the same payload; `--intent ID`.)

## Output

The manifest object (no envelope; the CLI wraps the same data in one): `commands`, `components`, `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes*`, `mcp_tools`, `cohort_types`, `skills`, `extensions`, `matrices` + `matrix`, [`intents`](intents.md), `return_presets`, `example_categories` / `example_tags`, capability blocks (`facet`, `join`, `union`, `process_chain`, `crosstab`, `export`, `import`, `overlays`; absent when not offered), `limits` (`{name, value, default, unit}`, `-1` = none), `feature_set_digest` + `limits_digest` — the cache key with the build version (and the intent). Sort-stable.

## Gotchas

- MCP serves the slim payload: every `description` blank (`component_schema` keys too), param scale and meaning included. Fetch `op-<kebab>` via `pulse_skills_get` first; error prose via `pulse_errors_lookup`.
- Operator names in `components.aggregators[]` / `components.groupers[]` are the manifest catalog, NOT the request slot keys — request bodies use `"aggregations"` / `"groups"` (see [`tool-process`](tool-process.md) Gotchas).
- Each operator entry carries its `component_schema`; the unscoped manifest also keys them by name under `components_schemas`.
- `enum_values_by` (keyed by the `enum_by` param's value, default first) narrows an enum (REG_GLM `link` by `family`).
- A param entry with `names_column: true` names a record column (cohort field or computed column; a `list` one per element). Read the flag, not `type`: some column params are `type: "string"`. Every `type: "field"` entry carries it.

## See

- [`session-bootstrap`](session-bootstrap.md) — bootstrap workflow and field cross-reference.
- [`request-envelope`](request-envelope.md) — `format_version` semantics and the standard envelope shape.
- [`response-components`](response-components.md) — `ComponentSchema` contract returned for emitting operators.
