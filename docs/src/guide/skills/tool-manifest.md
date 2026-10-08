```yaml
name: tool-manifest
kind: tool
description: Bootstrap blob — call once per session for the operator + capability catalog.
type: reference
applies_to: manifest, mcp
```

## When to use

CALL FIRST in every session. Cache the result; reference it for every request-authoring decision. Source of truth for what operators, tests, regressions, synth distributions, MCP tools, and error codes exist in THIS deployment. Pair with `pulse_examples_search` for runnable templates.

## Input

No arguments. (CLI: `pulse manifest --json` accepts `--slim` for the same payload MCP serves.)

## Output

`descriptor.Envelope` shape (`format_version: "1.0"`). `data` is the manifest payload — `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim), `mcp_tools`, `cohort_types`, `skills`, `extensions`, plus capability blocks `Facet`, `Join`, `ProcessChain`, `Crosstab`, `Export`, `Import`, `Overlays` (a block is absent when this deployment does not offer that capability), `limits` (this server's effective resource limits — `{name, value, default, unit}`, `-1` = none) and `feature_set_digest` + `limits_digest` — key a cached manifest on both plus the build version. Sort-stable; golden-checked.

## Gotchas

- MCP path always serves the slim payload (no prose) to keep bootstrap context lean. Fetch per-operator prose via `pulse_skills_get` and per-error prose via `pulse_errors_lookup` on demand.
- Operator names in `components.aggregators[]` / `components.groupers[]` are the manifest catalog, NOT the request slot keys — request bodies use `"aggregations"` / `"groups"` (see [`tool-process`](tool-process.md) Gotchas).
- Per-operator `ComponentSchema` declarations live under `components_schemas.{aggregators,groupers,filterers}`.

## See

- [`session-bootstrap`](session-bootstrap.md) — bootstrap workflow and field cross-reference.
- [`request-envelope`](request-envelope.md) — `format_version` semantics and the standard envelope shape.
- [`response-components`](response-components.md) — `ComponentSchema` contract returned for emitting operators.
