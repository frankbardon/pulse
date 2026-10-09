```yaml
name: session-skill-routing
description: Which skill to fetch for a given trigger — operator family prefixes, manifest catalog entries, request and response slots, request roots, CLI leaves and plain-language questions — plus how to read data.components on first sight.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict, manifest
covers: [skill-name derivation, skills, routing, components]
```

# Skill routing

Step 5 of [`session-bootstrap`](session-bootstrap.md) (`pulse_skills_get` on demand). Skills carry no runnable JSON; examples do.

## Skill-name derivation

Lowercase the operator family prefix and map through this table.

| Trigger | Skill to fetch |
|---|---|
| `AGG_*` | [`aggregation-design`](aggregation-design.md) |
| `ATTR_*` | [`attribute-composition`](attribute-composition.md) |
| `FILTER_*` | [`aggregation-design`](aggregation-design.md) |
| `GROUP_*` | [`grouper-design`](grouper-design.md) |
| `WIN_*` | [`window-design`](window-design.md) |
| `FEAT_*` | [`feature-engineering`](feature-engineering.md) |
| `TEST_*` (tier-1 or tier-2) | [`statistical-testing`](statistical-testing.md) |
| `REG_*` | [`regression-modeling`](regression-modeling.md) |
| `OVERLAY_*` | [`overlay-system`](overlay-system.md) |
| `synth_distributions[i].kind` | [`synthetic-data`](synthetic-data.md) |
| `cohort_types[i].name` (field type) | [`cohort-schema-design`](cohort-schema-design.md) |
| a `.sav` / `.zsav` source, or a cohort carrying a `.spss.json` sidecar | [`spss-cohorts`](spss-cohorts.md) |
| `mcp_tools[i].name` | `tool-<name minus `pulse_`>` — one atomic skill per tool |
| `pulse_lookup` / `pulse index build` / `pulse index list` / `pulse index verify` / `pulse index drop` / `pulse api lookup` | [`tool-lookup`](tool-lookup.md) (MCP surface), [`cohort-sidecar-index`](cohort-sidecar-index.md) (sidecar format) |
| a join-shaped cohort already on disk — `pulse_dedup` (`suggest_groups` alone is read-only; `groups` + `out` converts without touching the original) / `pulse dedup COHORT --group KEY:MEMBER,… [--out P] [--suggest-groups]` | [`tool-dedup`](tool-dedup.md), [`cohort-parent-groups`](cohort-parent-groups.md) |
| a set column out of option headroom — `pulse widen COHORT --field F --to set_u128\|set_u256` rewrites it in place (single-file cohorts only; no MCP tool) | [`cohort-schema-design`](cohort-schema-design.md) (set rungs), [`cohort-sharding`](cohort-sharding.md) (archives auto-widen) |
| a plain-language question with no operator in mind | [`intents`](intents.md) (`pulse-skill://intents`) — question kinds, then the manifest entries whose [`intents`](intents.md) match |
| a question kind known, request not — `pulse_recommend` / `pulse recommend --intent ID [--cohort C --field F …]` returns ranked drafts (bound to the cohort's fields and predict-checked when given one) | [`tool-recommend`](tool-recommend.md) |
| what a request will do, or what a result found, in plain words — `pulse_explain` / `pulse explain --request F \| --response F [--request F]` returns findings with verdicts and caveats | [`tool-explain`](tool-explain.md) |
| a statistical term in a result or skill | [`glossary`](glossary.md) (`pulse-skill://glossary`) |
| `error_codes[i]` | `pulse_errors_lookup` — the tool is the surface, not a skill |
| Request slot `Joins` | [`join-design`](join-design.md) |
| Request slot `Crosstab` | [`crosstab-guide`](crosstab-guide.md) |
| Request slot `Overlays` | [`overlay-system`](overlay-system.md) |
| Request / per-slot `weight`, `Options.DefaultWeight`, predict `weights`, `suggested_weight`, `PULSE_WEIGHT_*` | [`weighting`](weighting.md) |
| Response slot `data.components` (first sight) | [`response-components`](response-components.md) |
| Response slot `Metadata.LabelBindings` | [`label-display`](label-display.md) |
| `ComposedRequest` | [`compose-requests`](compose-requests.md) |
| `ChainRequest` | [`process-chain`](process-chain.md) |
| `FacetRequest` / `FacetSchemaRequest` | [`facet-design`](facet-design.md) |
| streaming, watch, request hashing | [`streaming-and-watching`](streaming-and-watching.md) |
| extension operator (anything in `manifest.extensions`) | `docs/src/internals/extension-points.md` |
| predict failed — fix loop | `docs/src/internals/debugging-predict.md` |

## Components on first sight

**`Response.Components` / `data.components` first sight.** Fetch [`response-components`](response-components.md) — canonical for the per-family shape (`aggregations[]`, `groupers[]`, `crosstab`, `filterers[]`, `run`), the orchestrator-filled universal floor (`{n, n_null}`, or `{total_n, n_null}` / `{n_in, n_out, n_null_input}` by family — NOT enumerated in `manifest.components_schemas[*].keys`), and the per-operator keys inside `Operator map[string]any` (or the cell map for crosstab cells), looked up under `manifest.components_schemas.aggregators[name].keys` / `.groupers[name].keys` / `.filterers[name].keys`. Additive-only; never bumps `format_version`. Never infer the shape from memory — it is bound to the binary version.
