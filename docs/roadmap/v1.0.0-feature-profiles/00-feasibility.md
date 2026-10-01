# Feature Profiles — feasibility

**Status:** proposal · **Target:** v1.0.0 · **Question asked:** can an embedder turn whole families of functionality on or off, with the manifest, skills and documents reacting to that configuration? The default must remain "everything enabled".

## Verdict: feasible, at moderate cost

Pulse already has the architectural seam this needs. The **extension snapshot pattern** makes the manifest, predict and MCP tool binding *per-instance* rather than process-global:

- `Pulse.Manifest` calls `descriptor.BuildManifestWithExtensions(p.svc.ExtensionsSnapshot())`.
- Predict receives `PredictOptions.Extensions`.
- MCP binds with `mcp.BindSessionToolsWithExtensions`.

All three already react to *adding* operators per instance. A feature profile is the same mechanism run in reverse: a per-instance snapshot that *removes* operators. The work is mostly threading one more value through the paths extensions already travel, plus closing a few places that still read global state.

| Surface | Reacts to config today? | Work needed | Difficulty |
|---|---|---|---|
| Manifest (`pulse_manifest`, `pulse manifest`) | per-instance via snapshot | filter components, tests, overlays, capability blocks and commands by the profile | **Low** |
| Predict | per-instance via snapshot | refuse disabled operators with a coded error | **Low** |
| Runtime enforcement | — | one validation choke point before execution (not the ~28 registry lookup sites in `processing/` and `service/`) | **Low–Medium** |
| MCP tools | registered from the global `toolmeta` list | skip disabled tools in `registerTools`; strip disabled enum values from tool input schemas | **Medium** |
| MCP prompts (`pulse-bootstrap`, `pulse-author-request` exist today) | static | filter by profile; generated intent prompts (guided analysis) inherit it | **Low** |
| Atomic skills (`op-*`, `tool-*`, `type-*`) | global `skills.List()` / `skills.Get()` via `//go:embed` | profile-aware `List` / `Get`; atomic skills map 1:1 to an operator via frontmatter, so filtering is exact | **Low** |
| Topical skills (`overlay-system`, `statistical-testing`, …) | global | prose mentions many operators and cannot be filtered precisely; see the risks below | **Medium–High** |
| Examples library | global `examples.Search` | hide examples whose `_meta.operators` include anything disabled (the tags already exist and are gate-enforced) | **Low** |
| Payload JSON Schema (`pulse schema`, `pulse://schema`) | static `BuildPayloadSchema()`; enums come from `types.All*Types()` | add a profile-aware variant; the golden stays pinned to the default (full) profile | **Medium** |
| Error code list | global | leave the full list (harmless), or filter codes owned only by disabled features | **Low** |
| Smart defaults | static table in `descriptor/defaults.go` | a default that resolves to a disabled operator must fall through, not silently pick it | **Low** |
| Guided analysis (Recommend, Explain, `NotFor`, intents) | n/a (planned) | never recommend or suggest a disabled operator; drop intents with no enabled operators | **Low** if designed in now |
| CLI | leaves built once in `buildApp()` | leaves for disabled capabilities print a coded "disabled by profile" error (or hide under `--profile`) | **Low** |
| Published docs site (mdBook) | static | **cannot react at runtime**; see below | n/a — different approach |

**About the published documentation.** The docs site at frankbardon.github.io/pulse is a static build. It documents the full feature set and should keep doing so. "Documents react to configuration" is achieved two other ways:
1. **Runtime documentation already is the manifest + skills + examples.** These are what agents and integrators actually query, and they react fully.
2. **Profile-scoped reference generation.** A `pulse docs export --profile <file>` command (or library `p.ExportReference(dir)`) writes the operator catalog, question guides and glossary (from the guided-analysis theme) as Markdown for *this* profile. An embedder can then ship it inside their own product docs. It reuses the generator the guided-analysis theme already needs.

## What already exists that becomes part of this

Pulse has scattered `Disable*` switches today: `Options.DisableDefaults`, `DisableComponents`, `DisableProjection`, `gosdk.Config.DisableCohortScan` / `PULSE_MCP_NO_COHORT_SCAN`. These are **behaviour** toggles (how a feature runs), not **availability** toggles (whether it exists). The profile should present both in one place without breaking the existing fields. The existing fields keep working and are mirrored into the profile's `behaviour` section.

## The hard parts (where the real cost is)

1. **Feature dependencies.** Features are not independent, and a naive toggle produces silent breakage:
   - `OVERLAY_{T,Z}_CELL` read `AGG_WELFORD` components;
   - `ATTR_REG_FITTED` / `_RESIDUAL` / `_LEVERAGE` need a `REG_*`;
   - `OVERLAY_CHISQ_*` mirror `TEST_CHISQ`;
   - smart defaults pick `AGG_SUM` / `GROUP_RANGE`;
   - `TEST_TUKEY_HSD` is the follow-up to ANOVA;
   - planned `MAT_*` items build on `MAT_COVARIANCE`;
   - Compose overlays need Compose.

   A **dependency graph** must be declared (once, in `descriptor/`). Profile validation at `pulse.New` then either refuses an incoherent profile (`PULSE_PROFILE_DEPENDENCY`) or auto-disables the dependants with a warning. Refusing is recommended, because it is explicit and nothing surprises the embedder.
2. **Topical skill prose.** For example, `overlay-system.md` names families of overlay kinds in running text. Three options:
   - (a) serve it unchanged with a profile banner ("operators named here may be unavailable; the manifest is authoritative");
   - (b) mark operator-specific paragraphs with `<!-- op: OVERLAY_CHISQ_* -->` fences and strip them when disabled;
   - (c) hide a topical skill entirely when *all* its operators are disabled.

   Recommendation: **(c) + (a)** for v1.0.0, and (b) only if real embedders complain. The skills already say "never hardcode; the manifest is authoritative", so the banner is consistent with existing guidance.
3. **Golden discipline.** Manifest, payload-schema and skill goldens stay pinned to the default (full) profile. Add a small set of **profile goldens** (e.g. "survey-basic", "no-inferential", "descriptive-only") so filtering is tested without multiplying every golden.
4. **Coverage gates** (`TestSkillsCoverAll*`, `TestEveryOperatorHasAnExampleTag`) keep running against the full registry. Profiles never weaken the build-time contract, only what one instance exposes.
5. **Enforcement must be real.** Hiding an operator from the manifest while it still executes would be a leak. Embedders using profiles for licensing, tiering or tenant isolation will rely on this. Enforcement belongs at the single request-validation choke point used by every entry point (Process, Compose, ProcessChain, Facet, ProcessStream, Watch, templates at render, Recommend). It must be covered by a **parity gate** stating "anything absent from this instance's manifest is refused by this instance's runtime".

## Estimated size

Roughly **one epic of moderate size** (5–7 stories). It is comparable to the original extensions work, and smaller, because the snapshot plumbing exists. If the guided-analysis and vector-matrix themes are built with profiles in mind from the start, they add little marginal cost. Retrofitting later would cost more, so ordering matters (see 01).

Continue to [01 — Design & phasing](01-design.md).
