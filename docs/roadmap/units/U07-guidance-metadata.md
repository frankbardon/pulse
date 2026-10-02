---
id: U07
slug: guidance-metadata
title: "Pulse can describe what each operator is for, in plain language, without bloating payloads"
track: Guided analysis
size: M
status: not-started
depends_on: [U02, U02b]
soft_depends_on: [U04]
blocks: [U08]
todo_items: [36, 37, 38, 39, 40, 41, 42]
branch: guidance-metadata
---

# U07 — guidance-metadata

**Outcome:** Pulse can describe what each operator is for, in plain language, without bloating payloads.

**Track:** Guided analysis · **Size:** M · **Depends on:** [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md) · **Soft:** [U04](U04-profiles-model.md) · **Unblocks:** [U08](U08-guidance-backfill-inferential.md)

## Summary

Create the guided-analysis data model: intent taxonomy, `Purpose`, `Interpretation`, glossary, the extension `Purpose` hook, and the gates in report-only mode. Purpose prose is never inlined into default payloads (`TestManifestGuidanceBudget`).

## References

**Theme documents (read before starting):**
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P1–P4, P6
- [guided-analysis 00 — Overview & principles](../v1.0.0-guided-analysis/00-overview.md) — Principles (esp. 6: pulled, never pushed)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#36** (3. Guided analysis — metadata core › G1 — Metadata core) Intent taxonomy (`descriptor/intents.go`), projected to manifest `intents[]`
- [ ] **#37** (3. Guided analysis — metadata core › G1 — Metadata core) `Purpose` type: plain line, intents, questions, use cases, NotFor, assumptions, level, glossary links
- [ ] **#38** (3. Guided analysis — metadata core › G1 — Metadata core) `Interpretation` type: per-output meaning, labelled bands, conventions, caveats; shared p-value rules
- [ ] **#39** (3. Guided analysis — metadata core › G1 — Metadata core) Glossary registry (about 60 terms) and the `pulse-skill://glossary` resource
- [ ] **#40** (3. Guided analysis — metadata core › G1 — Metadata core) Extension `Purpose` hook
- [ ] **#41** (3. Guided analysis — metadata core › G1 — Metadata core) Gates, starting report-only: `TestSkillsCoverAllPurposes`, `TestPurposeAlternativesResolve`, `TestPurposeQuestionsResolve`, `TestGlossaryTermsResolve`, `TestInterpretationCoversOutputs`
- [ ] **#42** (3. Guided analysis — metadata core › G1 — Metadata core) `TestManifestGuidanceBudget`: no guidance prose in default payloads; manifest growth stays under about 4 KB

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

### E1 — Intents and purpose have a home
- S1: intent taxonomy with shape descriptors; manifest projection (IDs only)
- S2: `Purpose` + `Interpretation` types; shared p-value interpretation
- S3: glossary registry + resource

### E2 — Guidance is gated and kept out of default payloads
- S1: report-only coverage gates listing missing operators
- S2: `TestManifestGuidanceBudget` (≤ ~4 KB growth; no prose in default manifest/response/predict)
- S3: extension `Purpose` hook on `extend`-registered operators (the `extend` package is landed; absent → "no guidance" and never recommended)

## Acceptance criteria

- [ ] The manifest grows only by `intents[]` and per-operator intent IDs, within budget
- [ ] The report-only gates print the full list of operators lacking Purpose, without failing CI
- [ ] `pulse-skill://glossary` returns the glossary
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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
