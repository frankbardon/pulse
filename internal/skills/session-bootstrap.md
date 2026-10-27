---
name: session-bootstrap
description: Canonical MCP session order — manifest once, then examples → predict → process → errors_lookup. Skill-name derivation from operator names. Use first on every new MCP session.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [pulse_manifest, pulse_examples_search, pulse_examples_get, pulse_skills_list, pulse_skills_get, pulse_inspect, pulse_predict, pulse_errors_lookup]
---

# Session bootstrap

Steps 1–2 once, cached; re-enter from step 3 per question.

| # | Call | Cadence | Returns / effect |
|---|---|---|---|
| 1 | `pulse_manifest` (`intent` scopes it to one question kind) | once per session | operator catalogs, field types, error codes, MCP tool list, skills index, extensions, capability blocks |
| 2 | `pulse_inspect` | once per cohort | schema (fields, types, descriptions, dictionaries). **Side-effect:** binds schema-aware enums into the field-name arguments of `pulse_predict`<!-- feature: capability:process --> / `pulse_process`<!-- /feature --><!-- feature: capability:compose --> / `pulse_compose`<!-- /feature --><!-- feature: capability:sample --> / `pulse_sample`<!-- /feature --><!-- feature: capability:facet --> / `pulse_facet`<!-- /feature -->, constraining them to schema-resident values |
| 3 | `pulse_examples_search` | per question | name + summary, by `query` + `tags` + `category`<!-- feature: capability:recommend -->; or `pulse_recommend` by `intents` ID<!-- /feature --> |
| 4 | `pulse_examples_get` | per candidate | runnable Request JSON (`body`, `_meta` stripped). Adapt cohort filename / fields / labels — do not invent |
| 5 | `pulse_skills_get` | on demand | shape, gotchas, contract; pick via `session-skill-routing`. Runnable JSON comes from examples, NOT skills |
| 6 | `pulse_predict` (bare request<!-- feature: capability:compose -->, or `composed`<!-- /feature --><!-- feature: capability:facet -->, or `facet`<!-- /feature --><!-- feature: capability:process_chain -->, or `chain`<!-- /feature -->) | until clean | `errors`, `warnings`, `data.suggestions`, `data.defaults_applied`, `data.streamable`, `data.streamable_reasons` |
| 7 | the execute tool the manifest's `mcp_tools` lists for the operation —<!-- feature: capability:process --> `pulse_process`<!-- /feature --><!-- feature: capability:compose --> / `pulse_compose`<!-- /feature --><!-- feature: capability:process_chain --> / `pulse_process_chain`<!-- /feature --><!-- feature: capability:facet --> / `pulse_facet` / `pulse_facet_schema`<!-- /feature --><!-- feature: capability:sample --> / `pulse_sample`<!-- /feature --><!-- feature: capability:lookup --> / `pulse_lookup`<!-- /feature --> | execute | `{format_version, data, errors, warnings}` + additive `data.components`; `format_version` is `"1.1"`<!-- feature: capability:explain -->; read via `pulse_explain`<!-- /feature --> |
| 8 | `pulse_errors_lookup` | per unique `code` | canonical `message` + structured `fixups[]`. Authoritative — never paraphrase from memory |

<!-- feature: capability:compose -->
**Step 7 Compose return shape.** `pulse_compose`'s `data` is a `ComposedResponse` `{responses[], overlays[]}` — NOT the legacy `[]*Response`. Per-slot Components ride each `responses[i]`; overlay diagnostics ride `overlays[i].warnings`.
<!-- /feature -->

## Which skill answers what

| Question | Skill |
|---|---|
| Which skill to fetch for an operator prefix, manifest entry, request / response slot or CLI leaf; reading `data.components` on first sight | `session-skill-routing` |
| Excel / SPSS read flags, parent groups on the managed import path, `.sav` write flags, `--float f32` | `session-format-flags` |
<!-- feature: capability:synth -->
| `pulse profile create` and `pulse synth from-profile` flags | `session-synth-flags` |
<!-- /feature -->

## Authoring layers

Manifest = the contract. `pulse_inspect` = field names + values. `pulse_examples_*` = runnable JSON. Skills = gotchas, slot-key naming, rationale. `pulse_errors_lookup` = per-code prose. Prompts:

- <!-- feature: mcp_extra:prompt_bootstrap -->`pulse-bootstrap`: this order.<!-- /feature -->
- <!-- feature: mcp_extra:prompt_author_request -->`pulse-author-request`: question → intent prompt, tool or request.<!-- /feature -->
- <!-- feature: capability:recommend, capability:explain, capability:process -->`pulse-<intent>` (per analytic intent): inspect, recommend, confirm, process, explain.<!-- /feature -->

## On failure

Read every `errors[]` / `warnings[]` entry (`{code, message, details}`) → `pulse_errors_lookup` each unique `code` (cache) → apply `fixups[]` → re-`pulse_predict` → re-execute only when predict is clean. An unknown key (any depth) is `PULSE_REQUEST_UNKNOWN_FIELD`: fix `details.path`.

Scripts can trust the CLI exit status (all but `pulse mcp`): `0` ok, warnings allowed; `1` fatal (`errors` under `--json`, `--strict` escalations too); `2` usage error, no envelope.

## Environment

Roots auto-loaded at `pulse.New`:

- `PULSE_LABEL_TABLES_DIR` — output-time label tables. **Use its own dir:** every `*.json` must parse (else `pulse.New` fails); Pulse's sidecars are skipped.
- `PULSE_RANGE_TABLES_DIR` — named `{label,start,end}` sets for range operators. Same rule.
- `PULSE_POPULATIONS_DIR` — `*.pulse-population.json` files, bound in every request by stem.
- `PULSE_TEMPLATES_DIR` — parameterised request templates; `os.PathListSeparator`-separated roots in precedence order, first root wins. Render (`RenderTemplate` / `RenderTemplateRequest`), then predict.<!-- feature: capability:templates --> See `request-templating`.<!-- /feature -->

A name on disk and in code: `PULSE_EXTENSION_DUPLICATE`. `PULSE_BENCH_DIR` is benchmark-only.

Managed imports (`PULSE_IMPORTS_DIR`) persist each handle's read settings in a sidecar (`format`, `source_path`, `ttl_seconds`, `column_type_overrides`, `source_tz`, `column_source_tz`, `source_tz_field`, `dst_policy`, `--vector` folds); a handle is never reused across zones — re-import with `overwrite: true`. See `time-zones`.

Server-side (`pulse mcp` / an embedder's `mcpserve`): `PULSE_MCP_NO_COHORT_SCAN` (flag `--no-cohort-scan`) skips enumerating `pulse://<path>` resources at startup. **There an empty `resources/list` is not evidence of no cohorts** — `resources/read pulse://<path>` and `pulse_inspect` still resolve; ask for the path. Tool responses default to the `standard` preset (no `components`; host flag `--return`); send `"return": {"preset": "full"}` for everything — see `response-shaping`. The host caps resources (global flag `--limit name=value`, on every CLI leaf); plan within `manifest.limits` (`-1` = none), re-fetch the manifest on a new `limits_digest`; a breach is `PULSE_LIMIT_EXCEEDED`. Host diagnostics (`--log-level`, `--metrics-addr`) and worker caps (`--shard-workers`, `--decode-workers`) never reach tool responses.

Both table kinds surface under `manifest.extensions.{label_tables,range_tables}`. Templates are NOT manifest-projected; enumerate with `ListTemplates` (`Summary.Broken`: a since-malformed file).

## Cross-links

`request-envelope` (envelope, slot keys, defaults, streamability)<!-- feature: capability:synth --> · `synthetic-data` (synth modes, multi-predictor models, correlations, determinism)<!-- /feature --><!-- feature: capability:synth --> · `synth-structural-rules` (`rules[]` / `constraints[]`: gating, masking, derived fields)<!-- /feature --> · `response-components` · `response-shaping` (`return`: trim a response, cap float digits) · `tool-*` (one atomic skill per MCP tool, full argument shape) · `docs/src/internals/debugging-predict.md` · `docs/src/getting-started/` (cold start)<!-- feature: io_format:spss --> · `spss-cohorts`<!-- /feature -->.
