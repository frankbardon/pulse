---
id: U07
slug: guidance-metadata
title: "Pulse can describe what each operator is for, in plain language, without bloating payloads"
track: Guided analysis
size: L
status: done
depends_on: [U02, U02b]
soft_depends_on: [U04]
blocks: [U08]
todo_items: [36, 37, 38, 39, 40, 41, 42]
branch: guidance-metadata
---

# U07 — guidance-metadata

**Outcome:** Pulse can describe what each operator is for, in plain language, without bloating payloads.

**Track:** Guided analysis · **Size:** L · **Depends on:** [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md) · **Soft:** [U04](U04-profiles-model.md) · **Unblocks:** [U08](U08-guidance-backfill-inferential.md)

## Summary

Create the guided-analysis data model: intent taxonomy, `Purpose`, `Interpretation`, glossary, the extension `Purpose` hook, and the gates in report-only mode. Purpose prose is never inlined into default payloads (`TestManifestGuidanceBudget`).

## References

**Theme documents (read before starting):**
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P1–P4, P6
- [guided-analysis 00 — Overview & principles](../v1.0.0-guided-analysis/00-overview.md) — Principles (esp. 6: pulled, never pushed)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#36** (3. Guided analysis — metadata core › G1 — Metadata core) Intent taxonomy (`descriptor/intents.go`), projected to manifest `intents[]`
- [x] **#37** (3. Guided analysis — metadata core › G1 — Metadata core) `Purpose` type: plain line, intents, questions, use cases, NotFor, assumptions, level, glossary links
- [x] **#38** (3. Guided analysis — metadata core › G1 — Metadata core) `Interpretation` type: per-output meaning, labelled bands, conventions, caveats; shared p-value rules
- [x] **#39** (3. Guided analysis — metadata core › G1 — Metadata core) Glossary registry (about 60 terms) and the `pulse-skill://glossary` resource
- [x] **#40** (3. Guided analysis — metadata core › G1 — Metadata core) Extension `Purpose` hook
- [x] **#41** (3. Guided analysis — metadata core › G1 — Metadata core) Gates, starting report-only: `TestSkillsCoverAllPurposes`, `TestPurposeAlternativesResolve`, `TestPurposeQuestionsResolve`, `TestGlossaryTermsResolve`, `TestInterpretationCoversOutputs`
- [x] **#42** (3. Guided analysis — metadata core › G1 — Metadata core) `TestManifestGuidanceBudget`: no guidance prose in default payloads; manifest growth stays under about 4 KB

## Scope

**In scope**
- `descriptor/intents.go` + manifest `intents[]` and per-operator intent IDs
- `Purpose`, `Interpretation`, shared p-value rules, glossary registry (~60 terms) + `pulse-skill://glossary`
- Extension `Purpose` hook
- Report-only gates; `TestManifestGuidanceBudget` failing from day one

**Out of scope**
- Writing Purpose for existing operators (U08, U09)
- Rendering into skills/docs (U21)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-metadata/E<n>-S<m>): …`; close each epic with `milestone(guidance-metadata/E<n>): vertical slice complete — <epic title>`.

### E1 — Agents and embedders can browse Pulse's vocabulary
- S1: intent taxonomy (15 IDs, shapes) in the manifest — top-level `intents[]` + per-entry `intents`
- S2: plain-language glossary registry (~60 terms, jargon forms)
- S3: `glossary` / `intents` virtual skills on every skill surface; `pulse.Glossary()` / `pulse.Intents()`
### E2 — Operators declare their purpose, gated and kept out of payloads
- S1: built-in Purposes (exemplars `AGG_AVERAGE`, `TEST_ANOVA_F`, `TEST_PEARSON_R`) with two-tier validity / coverage gates; optional `_meta.intents` on examples
- S2: `TestManifestGuidanceBudget` — ≤4096 guidance bytes, no prose in default manifest / Response / PredictResult
### E3 — Test results report effect sizes
- S1: `TEST_CHISQ` `cramers_v` / `phi`, `TEST_PROP_Z` `cohens_h`, one-sample `TEST_T` `cohens_d`
- S2: ANOVA family `omega_squared` / `partial_eta_squared`
- S3: rank tests `epsilon_squared` / `rank_biserial`
### E4 — Results explain how to read them
- S1: `Interpretation` model, shared p-value rules, static validator, overlay `Inferential` flag
- S2: runtime probes proving declared Interpretation and effect-size keys are emitted
### E5 — Embedders attach guidance, and the contract is documented
- S1: optional `Purpose` (+ test `Interpretation`) on every extension registration → `PULSE_EXTENSION_PURPOSE_INVALID`
- S2: `.claude/reference/guided-analysis.md`, CLAUDE.md, skills, roadmap close-out

## Acceptance criteria

- [x] The manifest grows only by `intents[]` and per-operator intent IDs, within budget
- [x] The report-only gates print the full list of operators lacking Purpose, without failing CI
- [x] `pulse-skill://glossary` returns the glossary
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllPurposes`, `TestPurposeAlternativesResolve`, `TestPurposeQuestionsResolve`, `TestGlossaryTermsResolve`, `TestInterpretationCoversOutputs` (report-only)
- `TestManifestGuidanceBudget` (failing)

## Update Demand companions

- CLAUDE.md "Non-Skippable CI Gates" lists `TestSkillsCoverAllPurposes` by name
- `update-demand.md` row: new operator → its `Purpose`
- `.claude/reference/guided-analysis.md` (start it here)
- Manifest golden regenerated

## Human inputs & decisions

- None.

## Landed deviations

Contract of record is `.claude/reference/guided-analysis.md`; embedder prose is `docs/src/library/guided-analysis.md` and `docs/src/internals/extension-points.md` (Purpose and Interpretation).

- **Size M → L.** A third epic filled the effect-size gap (nine tests gained `details.effect_size.*` keys) so `Interpretation` had real outputs to describe, and a fourth landed `Interpretation` with static and runtime validation.
- **Registries live in `internal/descriptor/`** (`intents.go`, `glossary.go`, `purposes.go`, `interpretations.go`), not `descriptor/intents.go`; only the types are public (`descriptor/guidance.go`).
- **Glossary and intents are virtual skills** (kind `reference`, never pruned by a feature profile), so `pulse-skill://intents` ships alongside `pulse-skill://glossary`.
- **Validity gates are binding from day one; only coverage is report-only.** `TestManifestGuidanceBudget` binds (≤4096 bytes; about a quarter used).
- **Overlay `Inferential` is declared per kind** in the manifest, not inferred from names.
- **Extension guidance** validates at every `pulse.New` with the built-in validators; test Interpretation is structure-only (output keys are not probed).

## Handed to U08

- **Statistics-reviewer pass** over everything U07 wrote: the three exemplar Purposes (`AGG_AVERAGE`, `TEST_ANOVA_F`, `TEST_PEARSON_R`) — review their statistical phrasing — the glossary, the shared p-value rules and the effect-size formulas.
- **Coverage at hand-off:** 160 of 163 built-ins lack a Purpose; 40 of 42 inferential built-ins lack an Interpretation; no example carries `_meta.intents` yet.
- **Overlay probes:** `overlayInterpretationProbes` is empty; the first overlay Interpretation on a `summary.parameters.*` path needs a host fixture (recipe in `guided-analysis.md`).
- **CODEOWNERS** for statistical review lands with the reviewer.
