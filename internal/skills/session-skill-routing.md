---
name: session-skill-routing
description: Which skill to fetch for a given trigger — operator family prefixes, manifest catalog entries, request and response slots, request roots, CLI leaves and plain-language questions — plus how to read data.components on first sight.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [skill-name derivation, skills, routing, components]
---

# Skill routing

Step 5 of `session-bootstrap` (`pulse_skills_get` on demand). Skills carry no runnable JSON; examples do.

## Skill-name derivation

Lowercase the operator family prefix and map through this table.

| Trigger | Skill to fetch |
|---|---|
| `AGG_*` | `aggregation-design` |
| `ATTR_*` | `attribute-composition` |
| `FILTER_*` | `aggregation-design` |
| `GROUP_*` | `grouper-design` |
| `WIN_*` | `window-design` |
| `FEAT_*` | `feature-engineering` |
| `TEST_*` (tier-1 or tier-2) | `statistical-testing` |
| `REG_*` | `regression-modeling` |
| `OVERLAY_*` | `overlay-system` |
<!-- feature: capability:synth -->
| `synth_distributions[i].kind` | `synthetic-data` |
<!-- /feature -->
| `cohort_types[i].name` (field type) | `cohort-schema-design` |
<!-- feature: io_format:spss -->
| a `.sav` / `.zsav` source, or a cohort carrying a `.spss.json` sidecar | `spss-cohorts` |
<!-- /feature -->
| `mcp_tools[i].name` | `tool-<name minus `pulse_`>` — one atomic skill per tool |
<!-- feature: capability:lookup -->
| `pulse_lookup` / `pulse index build` / `pulse index list` / `pulse index verify` / `pulse index drop` / `pulse api lookup` | `tool-lookup` (MCP surface), `cohort-sidecar-index` (sidecar format) |
<!-- /feature -->
<!-- feature: capability:dedup -->
| a join-shaped cohort already on disk — `pulse_dedup` (`suggest_groups` alone is read-only; `groups` + `out` converts without touching the original) / `pulse dedup COHORT --group KEY:MEMBER,… [--out P] [--suggest-groups]` | `tool-dedup`, `cohort-parent-groups` |
<!-- /feature -->
<!-- feature: capability:widen -->
| a set column out of option headroom — `pulse widen COHORT --field F --to set_u128\|set_u256` rewrites it in place (single-file cohorts only; no MCP tool) | `cohort-schema-design` (set rungs), `cohort-sharding` (archives auto-widen) |
<!-- /feature -->
| a plain-language question with no operator in mind | `intents` (`pulse-skill://intents`) — question kinds, then the manifest entries whose `intents` match |
<!-- feature: capability:recommend -->
| a question kind known, request not — `pulse_recommend` / `pulse recommend --intent ID [--cohort C --field F …]` returns ranked drafts (bound to the cohort's fields and predict-checked when given one) | `tool-recommend` |
<!-- /feature -->
<!-- feature: capability:explain -->
| what a request will do, or what a result found, in plain words — `pulse_explain` / `pulse explain --request F \| --response F [--request F]` returns findings with verdicts and caveats | `tool-explain` |
<!-- /feature -->
| a statistical term in a result or skill | `glossary` (`pulse-skill://glossary`) |
| `error_codes[i]` | `pulse_errors_lookup` — the tool is the surface, not a skill |
<!-- feature: capability:joins -->
| Request slot `Joins` | `join-design` |
<!-- /feature -->
<!-- feature: capability:crosstab -->
| Request slot `Crosstab` | `crosstab-guide` |
<!-- /feature -->
| Request slot `Overlays` | `overlay-system` |
<!-- feature: capability:weighting -->
| Request / per-slot `weight`, `Options.DefaultWeight`, predict `weights`, `suggested_weight`, `PULSE_WEIGHT_*` | `weighting` |
<!-- /feature -->
| Response slot `data.components` (first sight) | `response-components` |
<!-- feature: capability:labels -->
| Response slot `Metadata.LabelBindings` | `label-display` |
<!-- /feature -->
<!-- feature: capability:compose -->
| `ComposedRequest` | `compose-requests` |
<!-- /feature -->
<!-- feature: capability:process_chain -->
| `ChainRequest` | `process-chain` |
<!-- /feature -->
<!-- feature: capability:facet -->
| `FacetRequest` / `FacetSchemaRequest` | `facet-design` |
<!-- /feature -->
| streaming, watch, request hashing | `streaming-and-watching` |
| extension operator (anything in `manifest.extensions`) | `docs/src/internals/extension-points.md` |
| predict failed — fix loop | `docs/src/internals/debugging-predict.md` |

## Components on first sight

**`Response.Components` / `data.components` first sight.** Fetch `response-components` — canonical for the per-family shape (`aggregations[]`, `groupers[]`, `crosstab`, `filterers[]`, `run`), the orchestrator-filled universal floor (`{n, n_null}`, or `{total_n, n_null}` / `{n_in, n_out, n_null_input}` by family — NOT enumerated in `manifest.components_schemas[*].keys`), and the per-operator keys inside `Operator map[string]any` (or the cell map for crosstab cells), looked up under `manifest.components_schemas.aggregators[name].keys` / `.groupers[name].keys` / `.filterers[name].keys`. Additive-only; never bumps `format_version`. Never infer the shape from memory — it is bound to the binary version.
