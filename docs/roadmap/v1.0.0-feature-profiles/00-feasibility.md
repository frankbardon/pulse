# Feature Profiles — feasibility

**Status:** proposal · **Target:** v1.0.0 · **Question asked:** can an embedder declare which functionality one Pulse instance offers, with the manifest, skills and documents reacting to that configuration? The default must remain "everything enabled".

## Decisions so far

| # | Decision |
|---|---|
| 1 | **No config → everything.** Without a profile, every feature is enabled, including features added in future releases. Output is byte-identical to today. |
| 2 | **A config is a complete declaration (allowlist).** A non-default profile states its whole feature set; it is never "everything minus X". A feature added in a later Pulse release is **not** enabled in an existing profile until that profile names it. |
| 3 | **Disabled means invisible.** A disabled feature looks, from every surface, *as if it was never built*. There is no "disabled" error, no "disabled" list, no hint, no banner. |
| 4 | **Instance-wide only.** One profile per `*Pulse` instance, fixed at `pulse.New`. No per-request or per-tenant narrowing. |
| 5 | **Self-description is always present.** Manifest, payload schema, skills, examples, errors lookup, inspect and predict exist in every profile (each rendering the instance's view). |
| 6 | **The CLI is not profiled.** It is admin-facing and always shows the full surface. Profiles apply to the library (`pulse.New`) and the MCP server (`pulse mcp --profile`). |
| 7 | **Skills stay readable; discovery shrinks.** Every skill remains fetchable by exact name, but the *discoverable ontology* (lists, search, intents, `## See` links, example search, recommendations) only contains enabled features. Topical skills are written for progressive disclosure so they don't expose operators the reader hasn't been routed to. See 01, P4a. |

## Verdict: feasible

Pulse already has the right seam. The **extension snapshot pattern** makes the manifest, predict and MCP tool binding per-instance rather than process-global:

- `Pulse.Manifest` calls `descriptor.BuildManifestWithExtensions(p.svc.ExtensionsSnapshot())`.
- Predict receives `PredictOptions.Extensions`.
- MCP binds with `mcp.BindSessionToolsWithExtensions`.

Extensions use that seam to make an instance *larger* than the built-ins. A profile uses the same seam to make it *smaller*.

Decision 3 is the part that raises cost. A hidden feature can't simply be "flagged" anywhere. Every surface has to be produced as if the feature's registration were absent, including prose that merely *mentions* it. The table reflects that.

| Surface | Reacts to config today? | Work needed | Difficulty |
|---|---|---|---|
| Manifest | yes, per-instance via snapshot | build from the instance's feature set; nothing about hidden features | **Low** |
| Predict / runtime | yes, per-instance (extensions) | resolve names against the instance's feature set only; a hidden name takes the **same path as a name that never existed** | **Low–Medium** |
| Payload JSON Schema | no — static `BuildPayloadSchema()` over `types.All*Types()` | per-instance variant with enums and request slots for the instance's features only | **Medium** |
| MCP tools / resources / prompts | no — registered from global `toolmeta` and static prompt list | register only the instance's tools, prompts and resources; filtered enums in tool input schemas | **Medium** |
| Atomic skills (`op-*`, `tool-*`, `type-*`) | no — global `skills.List()` / `Get()` | hidden features' skills drop out of every **discovery** path (list, search, `## See`, intents); direct get by exact name still works (decision 7) | **Low** |
| **Topical skills** (`overlay-system`, `statistical-testing`, `crosstab-guide`, …) | no | rewrite for progressive disclosure (concepts and criteria, operators reached through the ontology) plus fences for any residual operator mentions (below) | **Medium–High** — the largest single cost |
| Examples | no — global `examples.Search` | search omits any example whose `_meta.operators` names a hidden feature; direct get follows the skills rule | **Low** |
| Error codes | no — global list | hide codes owned *only* by hidden features from the list and from `pulse errors lookup` | **Low–Medium** (needs a code → owning-feature map) |
| CLI | built once in `buildApp()` | **none** — the CLI is admin-facing and not profiled (decision 6); only `pulse mcp` accepts `--profile` | **None** |
| Smart defaults | static table | a default whose target is hidden simply doesn't apply, the same as when no default exists | **Low** |
| Guided analysis (Recommend, Explain, intents, `NotFor`, glossary) | planned | rendered from the instance's feature set only; no "unavailable here" wording | **Low** if designed in now |
| Published docs site | static build | documents the default (full) feature set; per-profile reference is **generated** (`pulse docs export`) for the embedder to host | n/a |

## The hard parts

### 1. Invisibility in prose (topical skills, generated guides)

*Superseded in part by decision 7: the chosen approach is progressive disclosure first, fences as a backstop — see 01, P4a. The analysis below is kept for context.*
Atomic skills map one-to-one to a feature, so hiding them is exact. Topical skills are prose that names many operators. For example, `overlay-system.md` enumerates overlay families, and `statistical-testing.md` compares tests. Under decision 3 they can't be served as-is, and a banner would itself reveal hidden features. Options:

- **(a) Fence-and-render.** Topical skills mark operator-specific spans, e.g. `<!-- feature: OVERLAY_CHISQ_* -->…<!-- /feature -->`. The instance renders them with hidden spans removed. A gate checks that **every operator name appearing in a topical skill sits inside a matching fence**. That makes the build guarantee that nothing leaks. Lists and tables need fences per row.
- **(b) Rewrite topical skills to name no operators.** Point at the manifest and atomic skills instead. This is simpler to keep correct, but it loses the comparative guidance ("use X instead of Y when…") that makes topical skills worth reading.

**Recommendation: (a).** It costs a one-time fencing pass over about 25 topical files plus a gate, and preserves the guidance. The same fence syntax serves the guided-analysis question guides and the generated reference export.

### 2. "As if never there" must still not run
A hidden operator must never execute, and it must never be **silently ignored**. Dropping an unknown slot and returning partial results would be a wrong answer with no signal. So a request naming a hidden feature gets exactly the response it would get **if that name had never been registered**: whatever Pulse returns today for an unknown aggregation, test or overlay type, with the same code and message shape. There is no new code. Profile-specific information never appears in it.

**Gate.** A parity test builds a profile, then compares two responses byte-for-byte: a request using a hidden name, and the same request using a fabricated name that never existed. They must be identical. This works across manifest, schema, predict, process, the MCP tool list, skill and example *discovery*, and errors lookup. The CLI and direct skill/example get by exact name are out of scope (decisions 6 and 7).

### 3. Allowlists and upgrades
Because a profile is a complete declaration, it can't use open-ended patterns: `TEST_*` would silently pick up new tests in a later release, breaking decision 2. The recommendation in 01 is **exact names only**, with tooling that writes the full list for you. Each feature still carries a `Since` version, so `pulse profile diff` can tell an embedder what is new since their profile was written. That is new metadata, but cheap and gate-able.

### 4. Dependencies
Some features need others. `OVERLAY_{T,Z}_CELL` read `AGG_WELFORD` components, the `ATTR_REG_*` attributes need a regression, and `TEST_TUKEY_HSD` follows ANOVA. A profile that enables one without the other is incoherent. Because a profile must be complete, the right response is a configuration error **at `pulse.New`**, aimed at the embedder (not visible to the instance's users), naming exactly what is missing. A `pulse profile check` tool reports this ahead of time.

### 5. Goldens
The default (full) manifest, schema and skill goldens are unchanged, because no config means today's output. A few **profile goldens** cover filtering, alongside the invisibility parity gate.

## Estimated size

**One to one-and-a-half epics.** The core plumbing is moderate, comparable to the extensions work, and reuses its snapshot path. The invisibility requirement adds the topical-skill rewrite and fencing pass with its gate, and the error-code ownership map. Leaving the CLI unprofiled (decision 6) removes the CLI-tree work. As before, building the vector-matrix and guided-analysis themes *with* profiles from their first commit is much cheaper than retrofitting.

Continue to [01 — Design & phasing](01-design.md).
