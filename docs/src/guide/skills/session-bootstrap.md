```yaml
name: session-bootstrap
description: Canonical MCP session order — manifest once, then examples → predict → process → errors_lookup. Skill-name derivation from operator names. Use first on every new MCP session.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [pulse_manifest, pulse_examples_search, pulse_examples_get, pulse_skills_list, pulse_skills_get, pulse_inspect, pulse_predict, pulse_errors_lookup]
```

# Session bootstrap

Steps 1–2 once, cached; re-enter from step 3 per question.

| # | Call | Cadence | Returns / effect |
|---|---|---|---|
| 1 | `pulse_manifest` (`intent` scopes it to one question kind) | once per session | operator catalogs (each entry's [`intents`](intents.md) = the question kinds it answers), field types, error codes, MCP tool list, skills index, extensions, capability blocks |
| 2 | `pulse_inspect` | once per cohort | schema (fields, types, descriptions, dictionaries). **Side-effect:** binds schema-aware enums into the field-name arguments of `pulse_predict` / `pulse_process` / `pulse_compose` / `pulse_sample` / `pulse_facet`, constraining them to schema-resident values |
| 3 | `pulse_examples_search` | per question | name + summary, by `query` + `tags` + `category`; or `pulse_recommend` by `intents` ID |
| 4 | `pulse_examples_get` | per candidate | runnable Request JSON (`body`, `_meta` stripped). Adapt cohort filename / fields / labels — do not invent |
| 5 | `pulse_skills_get` | on demand | shape, gotchas, contract; pick via [`session-skill-routing`](session-skill-routing.md). Runnable JSON comes from examples, NOT skills |
| 6 | `pulse_predict` (bare request, or `composed`, or `facet`, or `chain`) | until clean | `errors`, `warnings`, `data.suggestions`, `data.defaults_applied`, `data.streamable`, `data.streamable_reasons` |
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
- `pulse-author-request`: question → intent prompt, tool or request.
- `pulse-<intent>` (per analytic intent): inspect, recommend, confirm, process, explain.

## On failure

Read every `errors[]` / `warnings[]` entry (`{code, message, details}`) → `pulse_errors_lookup` each unique `code` (cache) → apply `fixups[]` → re-`pulse_predict` → re-execute only when predict is clean. An unknown key (any depth) is `PULSE_REQUEST_UNKNOWN_FIELD`: fix `details.path`.

Scripts can trust the CLI exit status (all but `pulse mcp`): `0` ok, warnings allowed; `1` fatal (`errors` under `--json`, `--strict` escalations too); `2` usage error, no envelope.

## Environment

Roots auto-loaded at `pulse.New`:

- `PULSE_LABEL_TABLES_DIR` — output-time label tables. **Use its own dir:** every `*.json` must parse as one (else `pulse.New` fails); only Pulse's sidecars are skipped.
- `PULSE_RANGE_TABLES_DIR` — named `{label,start,end}` sets for range operators. Same rule.
- `PULSE_POPULATIONS_DIR` — `*.pulse-population.json` files, bound in every request by stem.
- `PULSE_TEMPLATES_DIR` — parameterised request templates; `os.PathListSeparator`-separated roots in precedence order, first root wins. Render via `RenderTemplate` / `RenderTemplateRequest`, then predict the rendered request. See [`request-templating`](request-templating.md).

A name on disk and in code: `PULSE_EXTENSION_DUPLICATE`. `PULSE_BENCH_DIR` is benchmark-only.

Managed imports (`PULSE_IMPORTS_DIR`) persist each handle's read settings in a sidecar (`format`, `source_path`, `ttl_seconds`, `column_type_overrides`, `source_tz`, `column_source_tz`, `dst_policy`, `--vector` folds); a handle read in one zone is never reused for another — re-import with `overwrite: true`. See [`time-zones`](time-zones.md).

Server-side (`pulse mcp` / an embedder's `mcpserve`): `PULSE_MCP_NO_COHORT_SCAN` (flag `--no-cohort-scan`) skips enumerating `pulse://<path>` resources at startup. **There an empty `resources/list` is not evidence of no cohorts** — `resources/read pulse://<path>` and `pulse_inspect` still resolve; ask for the path. Tool responses default to the `standard` preset (no `components`; host flag `--return`); send `"return": {"preset": "full"}` for everything — see [`response-shaping`](response-shaping.md). The host caps resources (flag `--limit name=value`); plan within `manifest.limits` (`-1` = none), re-fetch the manifest on a new `limits_digest`; a breach is `PULSE_LIMIT_EXCEEDED`. Host diagnostics (`--log-level`, `--metrics-addr`) never reach tool responses.

Both table kinds surface under `manifest.extensions.{label_tables,range_tables}` — check there first. Templates are NOT manifest-projected; enumerate with `ListTemplates` (`Summary.Broken`: a since-malformed file).

## Cross-links

[`request-envelope`](request-envelope.md) (envelope, slot keys, defaults, streamability) · [`synthetic-data`](synthetic-data.md) (synth modes, multi-predictor models, correlations, determinism) · [`synth-structural-rules`](synth-structural-rules.md) (`rules[]` / `constraints[]`: gating, masking, derived fields) · [`response-components`](response-components.md) · [`response-shaping`](response-shaping.md) (`return`: trim a response, cap float digits) · `tool-*` (one atomic skill per MCP tool, full argument shape) · `docs/src/internals/debugging-predict.md` · `docs/src/getting-started/` (cold start) · [`spss-cohorts`](spss-cohorts.md).
