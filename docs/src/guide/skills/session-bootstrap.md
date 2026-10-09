```yaml
name: session-bootstrap
description: Canonical MCP session order — manifest once, then examples → predict → process → errors_lookup. Skill-name derivation from operator names. Use first on every new MCP session.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [pulse_manifest, pulse_examples_search, pulse_examples_get, pulse_skills_list, pulse_skills_get, pulse_inspect, pulse_predict, pulse_errors_lookup]
```

# Session bootstrap

Canonical order for an LLM driving Pulse over MCP. Steps 1–2 once, cached; re-enter from step 3 per user question.

| # | Call | Cadence | Returns / effect |
|---|---|---|---|
| 1 | `pulse_manifest` | once per session | operator catalogs (each entry's [`intents`](intents.md) = the question kinds it answers), field types, error codes, MCP tool list, `components_schemas`, skills index, extensions, capability blocks (Facet, Join, ProcessChain, Crosstab, Overlays). Deterministic per binary version |
| 2 | `pulse_inspect` | once per cohort | schema (fields, types, descriptions, dictionaries). **Side-effect:** binds schema-aware enums into the field-name arguments of `pulse_predict` / `pulse_process` / `pulse_compose` / `pulse_sample` / `pulse_facet`, constraining them to schema-resident values |
| 3 | `pulse_examples_search` | per question | name + summary, by `query` + `tags` + `category`; or `pulse_recommend` by `intents` ID |
| 4 | `pulse_examples_get` | per candidate | runnable Request JSON (`body`, `_meta` stripped). Adapt cohort filename / fields / labels — do not invent |
| 5 | `pulse_skills_get` | on demand | shape, gotchas, contract; pick via [`session-skill-routing`](session-skill-routing.md). Runnable JSON comes from examples, NOT skills |
| 6 | `pulse_predict` | until clean | `errors`, `warnings`, `data.suggestions`, `data.defaults_applied`, `data.streamable`, `data.streamable_reasons` |
| 7 | the execute tool the manifest's `mcp_tools` lists for the operation — `pulse_process` / `pulse_compose` / `pulse_process_chain` / `pulse_facet` / `pulse_facet_schema` / `pulse_sample` / `pulse_lookup` | execute | `{format_version, data, errors, warnings}` + additive `data.components`; `format_version` is `"1.1"`; read via `pulse_explain` |
| 8 | `pulse_errors_lookup` | per unique `code` | canonical `message` + structured `fixups[]`. Authoritative — never paraphrase from memory |

**Step 7 Compose return shape.** `pulse_compose`'s `data` is a `ComposedResponse` `{responses[], overlays[]}` — NOT the legacy `[]*Response`. Per-slot Components ride each `responses[i]`; overlay diagnostics ride `overlays[i].warnings`.

## Which skill answers what

| Question | Skill |
|---|---|
| Which skill to fetch for an operator prefix, manifest entry, request / response slot or CLI leaf; reading `data.components` on first sight | [`session-skill-routing`](session-skill-routing.md) |
| Excel / SPSS read flags, parent groups on the managed import path, `.sav` write flags | [`session-format-flags`](session-format-flags.md) |
| `pulse profile create` and `pulse synth from-profile` flags | [`session-synth-flags`](session-synth-flags.md) |

## Authoring layers

Manifest = the contract. `pulse_inspect` = field names + values. `pulse_examples_*` = runnable JSON. Skills = gotchas, slot-key naming, rationale. `pulse_errors_lookup` = per-code prose. Prompts:

- `pulse-bootstrap`: this order.
- `pulse-author-request`: question → request.
- `pulse-<intent>` (per analytic intent): inspect, recommend, confirm, process, explain.

## On failure

Read every `errors[]` / `warnings[]` entry (`{code, message, details}`) → `pulse_errors_lookup` each unique `code` (cache) → apply `fixups[]` → re-`pulse_predict` → re-execute only when predict is clean.

## Environment

Directory roots auto-loaded at `pulse.New` time:

- `PULSE_LABEL_TABLES_DIR` — output-time label tables. **Give it its own directory.** Every `*.json` beneath it is parsed as a label table and an unparseable file hard-fails `pulse.New`; Pulse's own `.spss.json` / `.meta.json` sidecars are excluded by suffix, nothing else is.
- `PULSE_RANGE_TABLES_DIR` — named labeled-date-range tables (`{label,start,end}` sets referenced by the date-range grouper and filter). Same sidecar exclusion.
- `PULSE_TEMPLATES_DIR` — parameterised request templates; `os.PathListSeparator`-separated roots in precedence order, first root wins. Render via `RenderTemplate` / `RenderTemplateRequest`, then predict the rendered request. See [`request-templating`](request-templating.md).

Managed imports (`PULSE_IMPORTS_DIR`) persist each handle's read settings in a sidecar (`format`, `source_path`, `ttl_seconds`, `column_type_overrides`, `source_tz`, `column_source_tz`, `dst_policy`); a handle read in one zone is never reused for another — re-import with `overwrite: true`. See [`time-zones`](time-zones.md).

Server-side (`pulse mcp` / an embedder's `mcpserve`): `PULSE_MCP_NO_COHORT_SCAN` (flag `--no-cohort-scan`) suppresses the startup walk that enumerates `pulse://<path>` resources. **On such a server `resources/list` names no cohorts and that is not evidence there are none** — a `resources/read pulse://<path>` and `pulse_inspect` both still resolve, so ask the user for the cohort path instead of concluding the data root is empty. Tool responses default to the `standard` preset (no `components`; host flag `--return`); send `"return": {"preset": "full"}` for everything — see [`response-shaping`](response-shaping.md). The host caps resources (flag `--limit name=value`); read the effective caps from `manifest.limits` (`-1` = none), plan within them, and re-fetch the manifest when `limits_digest` changes — a breach is `PULSE_LIMIT_EXCEEDED`. Host diagnostics (`--log-level`, `--metrics-addr`) never reach tool responses.

Both table kinds surface under `manifest.extensions.{label_tables,range_tables}` — check there before assuming a named table exists. Templates are NOT manifest-projected; enumerate with `ListTemplates` (`Summary.Broken` flags a file that has gone malformed since load).

## Cross-links

[`request-envelope`](request-envelope.md) (envelope shape, slot keys, smart defaults, streamability) · [`synthetic-data`](synthetic-data.md) (synth modes, multi-predictor models, correlations, determinism) · [`synth-structural-rules`](synth-structural-rules.md) (`rules[]` / `constraints[]`: gating, masking, derived fields) · [`response-components`](response-components.md) · [`response-shaping`](response-shaping.md) (`return`: trim a response, cap float digits) · `tool-*` (one atomic skill per MCP tool, full argument shape) · `docs/src/internals/debugging-predict.md` · `docs/src/getting-started/` (cold-start fallback) · [`spss-cohorts`](spss-cohorts.md).
