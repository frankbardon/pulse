---
name: session-bootstrap
description: Canonical MCP session order — manifest once, then examples → predict → process → errors_lookup. Skill-name derivation from operator names. Use first on every new MCP session.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [pulse_manifest, pulse_examples_search, pulse_examples_get, pulse_skills_list, pulse_skills_get, pulse_inspect, pulse_predict, pulse_process, pulse_errors_lookup]
---

# Session bootstrap

Canonical order for an LLM driving Pulse over MCP. Steps 1–2 once, cached; re-enter from step 3 per user question.

| # | Call | Cadence | Returns / effect |
|---|---|---|---|
| 1 | `pulse_manifest` | once per session | operator catalogs, field types, error codes, MCP tool list, `components_schemas`, skills index, extensions, capability blocks (Facet, Join, ProcessChain, Crosstab, Overlays). Deterministic per binary version |
| 2 | `pulse_inspect` | once per cohort | schema (fields, types, descriptions, dictionaries). **Side-effect:** binds schema-aware enums into `pulse_process` / `pulse_predict` / `pulse_compose` / `pulse_sample` / `pulse_facet`, constraining field-name arguments to schema-resident values |
| 3 | `pulse_examples_search` | per question | name + summary, by `query` + `tags` + `category` |
| 4 | `pulse_examples_get` | per candidate | runnable Request JSON (`body`, `_meta` stripped). Adapt cohort filename / fields / labels — do not invent |
| 5 | `pulse_skills_get` | on demand | shape, gotchas, contract. Runnable JSON comes from examples, NOT skills. Cold start ⇒ `docs/src/getting-started/`, else derive below |
| 6 | `pulse_predict` | until clean | `errors`, `warnings`, `data.suggestions`, `data.defaults_applied`, `data.streamable`, `data.streamable_reasons` |
| 7 | `pulse_process` / `pulse_compose` / `pulse_process_chain` / `pulse_facet` / `pulse_facet_schema` / `pulse_sample` / `pulse_lookup` | execute | `{format_version, data, errors, warnings}` + additive `data.components`; `format_version` is `"1.1"` |
| 8 | `pulse_errors_lookup` | per unique `code` | canonical `message` + structured `fixups[]`. Authoritative — never paraphrase from memory |

**Step 7 Compose return shape.** `pulse_compose`'s `data` is a `ComposedResponse` `{responses[], overlays[]}` — NOT the legacy `[]*Response`. Per-slot Components ride each `responses[i]`; overlay diagnostics ride `overlays[i].warnings`.

## Skill-name derivation

Lowercase the operator family prefix and map through this table. Skills carry no runnable JSON; examples do.

| Trigger | Skill to fetch |
|---|---|
| `AGG_*` | `aggregation-guide` |
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
| a `.sav` / `.zsav` source, or a cohort carrying a `.spss.json` sidecar | `spss-cohorts` |
| `mcp_tools[i].name` | `tool-<name minus `pulse_`>` — one atomic skill per tool |
| `pulse_lookup` / `pulse index build` / `pulse index list` / `pulse index verify` / `pulse index drop` / `pulse api lookup` | `tool-lookup` (MCP surface), `cohort-schema-design` (sidecar format) |
| `error_codes[i]` | `pulse_errors_lookup` — the tool is the surface, not a skill |
| Request slot `Joins` | `join-design` |
| Request slot `Crosstab` | `crosstab-guide` |
| Request slot `Overlays` | `overlay-system` |
| Response slot `data.components` (first sight) | `response-components` |
| Response slot `Metadata.LabelBindings` | `label-display` |
| `ComposedRequest` | `compose-requests` |
| `ChainRequest` | `process-chain` |
| `FacetRequest` / `FacetSchemaRequest` | `facet-design` |
| streaming, watch, request hashing | `streaming-and-watching` |
| extension operator (anything in `manifest.extensions`) | `docs/src/internals/extension-points.md` |
| predict failed — fix loop | `docs/src/internals/debugging-predict.md` |

**`Response.Components` / `data.components` first sight.** Fetch `response-components` — canonical for the per-family shape (`aggregations[]`, `groupers[]`, `crosstab`, `filterers[]`, `run`), the orchestrator-filled universal floor (`{n, n_null}`, or `{total_n, n_null}` / `{n_in, n_out, n_null_input}` by family — NOT enumerated in `manifest.components_schemas[*].keys`), and the per-operator keys inside `Operator map[string]any` (or the cell map for crosstab cells), looked up under `manifest.components_schemas.aggregators[name].keys` / `.groupers[name].keys` / `.filterers[name].keys`. Additive-only; never bumps `format_version`. Never infer the shape from memory — it is bound to the binary version.

## Authoring layers

Manifest = the contract (operator names, parameter shapes, streamable hints, error codes). `pulse_inspect` = field names + values. `pulse_examples_*` = runnable JSON. Skills = gotchas, slot-key naming, rationale. `pulse_errors_lookup` = per-code prose.

## On failure

Read every `errors[]` / `warnings[]` entry (`{code, message, details}`) → `pulse_errors_lookup` each unique `code` (cache) → apply `fixups[]` → re-`pulse_predict` → re-`pulse_process` only when predict is clean.

## Environment

Directory roots auto-loaded at `pulse.New` time:

- `PULSE_LABEL_TABLES_DIR` — output-time label tables. **Give it its own directory.** Every `*.json` beneath it is parsed as a label table and an unparseable file hard-fails `pulse.New`; Pulse's own `.spss.json` / `.meta.json` sidecars are excluded by suffix, nothing else is.
- `PULSE_RANGE_TABLES_DIR` — named labeled-date-range tables (`{label,start,end}` sets referenced by `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES`). Same sidecar exclusion.
- `PULSE_TEMPLATES_DIR` — parameterised request templates; `os.PathListSeparator`-separated roots in precedence order, first root wins. Render via `RenderTemplate` / `RenderTemplateRequest`, then predict the rendered request. See `request-templating`.

Both table kinds surface under `manifest.extensions.{label_tables,range_tables}` — check there before assuming a named table exists. Templates are NOT manifest-projected; enumerate with `ListTemplates` (`Summary.Broken` flags a file that has gone malformed since load).

## Source-format CLI flags

Per-format READ knobs the file cannot always answer itself; all ride `format.ReaderOptions` and every other format ignores them. Full model: `spss-cohorts`, `docs/src/cli/import-spss.md`.

| Flag | Format | Leaves | Must know |
|---|---|---|---|
| `--sheet` | Excel | `pulse import excel`, `import predict`, `import schema-template`, `pulse import auto`; `pulse_import` as `sheet` | — |
| `--spss-missing` (`auto` \| `null`, default `auto`) | SPSS `.sav` / `.zsav` | `pulse import spss`, `import predict`, `import schema-template`, `convert`, `convert predict` | `auto` nulls each numeric user-missing value (so `AGG_SUM` / `AGG_MEAN` never meet a refusal code) AND adds a `<var>_missing` sibling carrying WHY (`sysmis`, the value label, or the code); `null` = same nulls, reason gone. **A `.sav` import can therefore yield MORE columns than the file has variables** — count from `ReadHeader` / the returned schema, never the SPSS variable count. Bad value ⇒ `PULSE_SPSS_MISSING_MODE_INVALID`, never a silent default. Deliberately NOT on `pulse import auto` or `pulse_import` |
| `--charset` | SPSS `.sav` / `.zsav` | the `--spss-missing` leaves **plus** `pulse import auto` and `pulse_import` (as `charset`) | overrides the encoding the file declares about itself; decoding only. Reach for it on `PULSE_SPSS_CHARSET_INVALID` / `PULSE_SPSS_CHARSET_UNSUPPORTED` |

## Target-format CLI flags

Four `.sav` WRITE knobs, one per `spss.WriterOptions` field. All on `pulse export spss`; `--ignore-sidecar`, `--uncompressed`, `--sanitize-names` also on `convert` / `convert predict` (`convert`'s `--charset` is the SOURCE charset, so the write charset is export-only). Full model: `spss-cohorts` (Writing `.sav`), `docs/src/cli/export-spss.md`.

| Flag | Must know |
|---|---|
| `--ignore-sidecar` | synthesise the dictionary from the `.pulse` schema alone. Suppresses the sidecar **read**, not just the staleness verdict — a healthy sidecar is ignored too (`PULSE_SPSS_SIDECAR_IGNORED`, which cannot say which refusal it silenced). **Cannot round-trip a cohort still carrying a derived MD `set_*` column** ⇒ `PULSE_SPSS_NAME_COLLISION`; export without it |
| `--uncompressed` | flat 8-byte elements instead of SPSS bytecode; losslessly equivalent. Does **not** select ZSAV — emitting that is `PULSE_SPSS_COMPRESSION_UNSUPPORTED` |
| `--charset` (export leaf only) | charset written AND declared. Default: the source's own declared spelling; UTF-8 with no SPSS provenance. Set it when the cohort holds text that codepage cannot express (else `PULSE_SPSS_CHARSET_UNENCODABLE`) |
| `--sanitize-names` | rewrite names a `.sav` cannot carry (space, bracket, hyphen, leading digit) instead of refusing. **Refusal is the default on purpose**; this is the opt-in for the synthesised path. Deterministic, collision-safe, every rename reported as `PULSE_SPSS_NAME_SANITIZED` (full `field → name` list). Inert on the sidecar path |

`pulse export spss` **refuses** `--include` and `--labels` rather than ignoring them (`PULSE_SPSS_EXPORT_UNSUPPORTED`): the writer encodes from raw cohort storage, not the rendered row stream those transform. Narrow or relabel into a cohort first.

## Profile-capture CLI flags

Four independent, additive `pulse profile create` knobs; each adds an `omitempty` section, none implies another, all four omitted reproduces the pre-flag document byte-for-byte. Detail: `synthetic-data`, `docs/src/cli/profile-create.md`.

| Flag | Adds |
|---|---|
| `--conditional` | pick-one conditional pair sections (numeric-numeric, categorical-categorical, categorical-numeric, three `set_*` arms) |
| `--fit-shape` | 2-component Gaussian mixture per numeric on a BIC win; generates as `mixture`, not `normal` |
| `--fit-models` | one linear model per numeric, regressed on admitted categorical levels + set options. **How several drivers condition ONE numeric at once.** RETIRES the numeric-target conditional pairs for the targets it lands on — per target, never per document; the three non-numeric arms are untouched, so `--conditional` + `--fit-models` keeps both halves |
| `--residual-correlations` (needs `--fit-models`) | full correlation submatrix among fitted residuals, so a numeric can be both conditioned and correlated with a sibling |

## Synth-generation CLI flags

Two additive `pulse synth from-profile` knobs (both absent reproduces pre-flag output byte-for-byte) plus one on `pulse profile create`. Detail: `synth-structural-rules`, `docs/src/cli/synth-from-profile.md`, `docs/src/cli/profile-create.md`.

| Flag | Must know |
|---|---|
| `--emit-spec <path>` | the spec that actually generated (AFTER any `--rules` merge), indented JSON, not a rendering — fed to `synth from-schema` at the same seed it reproduces the same rows. **The only way to see which captured models survived translation, which distribution each field reconstructed to, and which conditional pairs were retired**, and how you learn the field names, types and floors a rule must be written against. Written before generation, so a failing run still leaves it |
| `--rules <path>` | the only way to reach the `rules[]` layer from the profile path. A **bare JSON array of rule objects — the `rules` key's own value**, so a rule moves between spec and file by cut and paste; `{"rules": […]}` is refused, not read as zero rules. REPLACES `Spec.Rules` (a derived spec carries none). Eager validation naming the FILE — e.g. `PULSE_SYNTH_RULE_FIELD_UNKNOWN` with `details.path`, never a bare parse error |
| `--suggest-rules <path>` (on `pulse profile create`) | DETECTS rules on the same scan (no extra cohort read) and writes the bare array `--rules` consumes unmodified: GATING (`set_null`), CO-MISSING blocks (`null_together`, admitted on identical null PATTERN — an identical null RATE is never enough) and EXACT DEPENDENCIES (`set_expr`; `packed_bool`/`u4` targets, `categorical_*`/`packed_bool`/`u4` source of ≤16 levels, band edges DISCOVERED from the data). Near misses, almost-determined pairs, always-null and constant columns land in `warnings`, never proposed. Gating first, dependency LAST — declaration order is applied order. **PROPOSED, never applied**: detection finds the STATISTICAL gate, a human knows the SEMANTIC one. Measurements ride `_evidence`, a rule's one INERT slot (an evidence-only rule is still `PULSE_SYNTH_RULE_EMPTY`). The profile document gains no section. Numeric gates emit through `round()` — a bare `==` reads the pre-rounding float and under-fires |

**A model coefficient is a LATENT-scale quantity**, not data units: it shifts the standard-normal `μ` in `value = Q(Φ(μ + σ·z))`, so it is non-linear in value space for every non-normal `Q`. Never report one as "this many points on the scale". Detail: `synthetic-data`.

## Cross-links

`request-envelope` (envelope shape, slot keys, smart defaults, streamability) · `synthetic-data` (synth modes, multi-predictor models, correlations, determinism) · `synth-structural-rules` (`rules[]` / `constraints[]`: gating, masking, derived fields) · `response-components` · `tool-*` (one atomic skill per MCP tool, full argument shape) · `docs/src/internals/debugging-predict.md` · `docs/src/getting-started/` (cold-start fallback) · `spss-cohorts`.
