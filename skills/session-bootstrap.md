---
name: session-bootstrap
description: Canonical MCP session order — manifest once, then examples → predict → process → errors_lookup. Skill-name derivation from operator names. Use first on every new MCP session.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [pulse_manifest, pulse_examples_search, pulse_examples_get, pulse_skills_list, pulse_skills_get, pulse_inspect, pulse_predict, pulse_process, pulse_errors_lookup]
---

# Session bootstrap

The canonical order for an LLM driving Pulse over MCP. Do this once per session; cache; re-enter from step 3 for every user question.

## The order

1. **`pulse_manifest`** — once per session. Cache. Deterministic for a binary version. Carries: operator catalogs, field types, error codes, MCP tool list, `components_schemas`, skills index, extensions, capability blocks (Facet, Join, ProcessChain, Crosstab, Overlays).

2. **`pulse_inspect`** — once per cohort path. Returns schema (fields, types, descriptions, dictionaries). Side-effect: binds schema-aware enums into the session's action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_sample`, `pulse_facet`) so field-name arguments stay constrained to schema-resident values.

3. **`pulse_examples_search`** — for each user question. Search by `query` + `tags` + `category`. Returns name + summary. Examples are the canonical source of runnable Request JSON.

4. **`pulse_examples_get`** — fetch the candidate `body` (runnable Request JSON; `_meta` block stripped). Adapt cohort filename, field names, labels.

5. **`pulse_skills_get`** — on demand. Skills carry shape, gotchas, and design contract. Runnable JSON comes from examples, not skills. Default to `getting-started` if cold; otherwise use the skill-name derivation rules below.

6. **`pulse_predict`** — validate the adapted Request against the schema. Read `errors`, `warnings`, `data.suggestions`, `data.defaults_applied`, `data.streamable`, `data.streamable_reasons`. Iterate until clean.

7. **`pulse_process`** (or `pulse_compose` / `pulse_process_chain` / `pulse_facet` / `pulse_facet_schema` / `pulse_sample` / `pulse_lookup`) — execute. Returns `{format_version, data, errors, warnings}` plus the additive `data.components` family. `format_version` is `"1.1"`. For `pulse_compose` `data` is a `ComposedResponse` `{responses[], overlays[]}` — not the legacy `[]*Response`; per-slot Components rides each `responses[i]`, overlay diagnostics ride `overlays[i].warnings`.

8. **`pulse_errors_lookup`** — on any non-empty `errors[]` entry. Resolves `code` to canonical `message` + structured `fixups[]`. Do not paraphrase from memory — the lookup is authoritative.

## Skill-name derivation

When a manifest operator name or response key surfaces a topic, derive the skill name to fetch:

| Trigger | Skill to fetch |
|---|---|
| `AGG_*` operator name | `aggregation-guide` |
| `ATTR_*` | `attribute-composition` |
| `FILTER_*` | `aggregation-guide` (filtering section) |
| `GROUP_*` | `grouper-design` |
| `WIN_*` | `window-operations` |
| `FEAT_*` | `feature-engineering` |
| `TEST_*` (tier-1 or tier-2) | `statistical-testing` |
| `REG_*` | `regression-modeling` |
| `OVERLAY_*` | `overlay-system` |
| `synth_distributions[i].kind` | `synthetic-data` |
| `cohort_types[i].name` (field type) | `cohort-schema-design` |
| A `.sav` / `.zsav` source, or a cohort carrying a `.spss.json` sidecar | `spss-cohorts` |
| `mcp_tools[i].name` | `mcp-integration` |
| `pulse_lookup` / CLI `pulse index {build,list,verify,drop}` / `pulse api lookup` | `tool-lookup` (MCP surface), `cohort-schema-design` (sidecar format) |
| `error_codes[i]` | `pulse_errors_lookup` (not a skill — the tool is the surface) |
| Request slot: `Joins` | `join-design` |
| Request slot: `Crosstab` | `crosstab-guide` |
| Request slot: `Overlays` | `overlay-system` |
| Response slot: `data.components` (first sight) | `response-components` |
| Response slot: `Metadata.LabelBindings` | `label-display` |
| `ComposedRequest` | `compose-requests` |
| `ChainRequest` | `contributor-workflow` (ProcessChain section) |
| `FacetRequest` / `FacetSchemaRequest` | `facet-design` |
| Streaming, watch, request hashing | `streaming-and-watching` |
| Extension operator (anything in `manifest.extensions`) | `extension-points` |
| Predict failed — fix loop | `debugging-with-predict` |

The general rule: lowercase the operator family prefix (`AGG`, `ATTR`, `FILTER`, ...) and map to the target skill via the table above. Skills do NOT carry runnable JSON; examples do.

## `Response.Components` — v0.20.0 first-sight protocol

When you see `data.components` (or `Response.Components` in a typed envelope) for the first time in a session:

1. Call `pulse_skills_get` with `name: "response-components"`.
2. It is the canonical reference for the per-family shape: `aggregations[]`, `groupers[]`, `crosstab`, `filterers[]`, `run`.
3. `format_version` is `"1.1"` — Components itself is additive-only and never bumps; the bump came from the Compose facade lift.
4. Universal floor `{n, n_null}` (or `{total_n, n_null}` / `{n_in, n_out, n_null_input}` depending on family) is filled by the orchestrator and is NOT enumerated in `manifest.components_schemas[*].keys`.
5. Per-operator keys appear inside `Operator map[string]any` (aggregations / groupers) or directly inside the cell map (crosstab cells). Look them up via `manifest.components_schemas.aggregators[name].keys` / `.groupers[name].keys` / `.filterers[name].keys`.

Do not infer Components shape from memory or external documentation — it is bound to the binary version and lives in the manifest plus the `response-components` skill.

## Authoring layers

- Manifest is the contract: operator names, parameter shapes, streamable hints, error codes.
- `pulse_inspect` is the field-name + value source.
- `pulse_examples_*` is the runnable-JSON source. Adapt; do not invent.
- Skills carry gotchas, slot-key naming, design rationale.
- `pulse_errors_lookup` is the per-code prose source.

## On failure

1. Read every `errors[]` / `warnings[]` entry — `{code, message, details}`.
2. `pulse_errors_lookup` each unique `code`. Cache.
3. Apply returned `fixups[]`. Re-`pulse_predict`. Iterate.
4. Re-`pulse_process` only when predict is clean.

## Environment

Directories auto-loaded from disk at `pulse.New` time:

- `PULSE_LABEL_TABLES_DIR` — output-time label tables. **Give it its own directory.** It parses *every* `*.json` beneath it as a label table and a file it cannot parse hard-fails `pulse.New`, so pointing it at a directory of cohorts trips over the `.spss.json` / `.meta.json` sidecars sitting there.
- `PULSE_RANGE_TABLES_DIR` — named labeled-date-range tables (`{label,start,end}` sets referenced by `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES`).
- `PULSE_TEMPLATES_DIR` — parameterised request templates; `os.PathListSeparator`-separated roots in precedence order, first root wins. Render via `RenderTemplate` / `RenderTemplateRequest`, then predict the rendered request. See the `request-templating` skill.

Both table kinds surface under `manifest.extensions.{label_tables,range_tables}` — check there before assuming a named table exists. Templates are not manifest-projected; enumerate them with `ListTemplates` (`Summary.Broken` flags a file that has gone malformed since load).

## Source-format CLI flags

Three per-format READ knobs the file itself cannot always answer. All ride `format.ReaderOptions`; every other format ignores them.

- `--sheet` (Excel) — `pulse import excel`, `import predict`, `import schema-template`, `import auto`. The `pulse_import` MCP tool carries it as `sheet`.
- `--spss-missing` (SPSS `.sav` / `.zsav`), `auto` | `null`, default `auto` — `pulse import spss`, `import predict`, `import schema-template`, `convert`, `convert predict`. `auto` nulls every numeric user-missing value in its analytic column (so `AGG_SUM` / `AGG_MEAN` never add a refusal code) and adds a generated `<var>_missing` sibling carrying WHY — `sysmis`, the value label, or the code. `null` suppresses the siblings: same nulls, reason gone. **A `.sav` import may therefore yield more columns than the file has variables** — read `ReadHeader` / the returned schema, never the SPSS variable count. An unrecognised value is `PULSE_SPSS_MISSING_MODE_INVALID`, never a silent default. **Deliberately NOT on `pulse import auto` or `pulse_import`**, and that asymmetry with `--charset` is a decision, not a gap: the default is the fidelity-preserving mode and the only alternative discards information, so the dedicated leaf is where asking for it is an explicit act.
- `--charset` (SPSS `.sav` / `.zsav`) — `pulse import spss`, `import predict`, `import schema-template`, `convert`, `convert predict`. Overrides the encoding the file declares about itself; decoding only, the declaration is still retained. Reach for it on `PULSE_SPSS_CHARSET_INVALID` or `PULSE_SPSS_CHARSET_UNSUPPORTED` — most often a file transcoded by one tool and re-saved by another keeps a stale record `7/20` name, or declares nothing and fails the strict UTF-8 default on its first 8-bit byte. Also on `pulse import auto` and the `pulse_import` MCP tool as `charset` — unlike `--spss-missing`, because without it such a file cannot be imported through the managed pool by any means.

## Target-format CLI flags

Four `.sav` WRITE knobs, each one field of `spss.WriterOptions`. They ride `pulse export spss`; `--ignore-sidecar`, `--uncompressed` and `--sanitise-names` are also on `convert` / `convert predict` (`convert` reads `--charset` for the SOURCE, so the write charset is settable only on the export leaf). Every other target format ignores them.

- `--ignore-sidecar` (SPSS) — do not read the metadata sidecar beside the source cohort; synthesise the dictionary from the `.pulse` schema alone. It suppresses the **read**, not merely the staleness verdict: a healthy sidecar is ignored too, and an unreadable one cannot block. Raises `PULSE_SPSS_SIDECAR_IGNORED`, which deliberately cannot say which refusal it silenced. **It cannot round-trip a cohort whose derived MD `set_*` column is still present** — that column's dictionary entries are its constituents' field names, so synthesis mints duplicate indicator variables and stops on `PULSE_SPSS_NAME_COLLISION`. Export without the flag instead.
- `--uncompressed` (SPSS) — flat 8-byte elements instead of SPSS's bytecode compression. Losslessly equivalent; bytecode is the default because it is what SPSS's own SAVE writes. Does **not** select ZSAV — emission of that is `PULSE_SPSS_COMPRESSION_UNSUPPORTED`.
- `--charset` (SPSS, on `pulse export spss` only) — the charset the emitted file is written in AND declares. Default: whatever the source declared, in the source's own spelling; UTF-8 for a cohort with no SPSS provenance. Set it when the cohort now holds text the source's codepage cannot express (otherwise `PULSE_SPSS_CHARSET_UNENCODABLE`).
- `--sanitise-names` (SPSS) — rewrite cohort field names a `.sav` cannot carry (a space, bracket, hyphen, leading digit) instead of refusing. **The refusal is the default on purpose**; this is the opt-in for the synthesised path, where a CSV header's spaces are ordinary. Renames are deterministic and collision-safe against names that were already legal, and every one is reported as `PULSE_SPSS_NAME_SANITISED` with the full `field → name` list. Inert on the sidecar path — those names came from SPSS.

Note two things `pulse export spss` refuses rather than ignores: `--include` and `--labels`. The `.sav` writer encodes from the cohort's raw storage, not from the rendered row stream those two transform, so honouring them would emit something other than what was asked for (`PULSE_SPSS_EXPORT_UNSUPPORTED`). Narrow or relabel into a cohort first, then export it.

## Cross-links

- `request-envelope` — envelope shape, slot keys, smart defaults, streamability flag.
- `response-components` — v0.20.0 Components family + per-operator key tables.
- `mcp-integration` — every registered tool, full per-tool argument shape.
- `debugging-with-predict` — predict loop in detail.
- `getting-started` — vocabulary + pipeline order primer (cold-start fallback).
- `spss-cohorts` — the whole `.sav` fidelity model, read and write.
