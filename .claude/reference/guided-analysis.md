# Guided analysis — intents, Purpose, Interpretation, glossary

**How to load.** This file is an INDEX: the rules below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/guided-analysis/` your change touches; find one with `grep -l '<identifier>' .claude/reference/guided-analysis/*.md`. A section-qualified pointer at this file resolves through the section map.

The guided-analysis metadata lets Pulse say what each operator is for, and how to read what it returns, in words a non-statistician recognises: intents, per-operator `Purpose`, per-output `Interpretation`, a glossary and the `details.effect_size.*` keys.

- **Declared, never executed.** Nothing in the engine reads guidance to run a request. Public types live in `descriptor/guidance.go`; the registries live in `internal/descriptor/` beside `capabilities_*.go`, under the no-execute import ban.
- **Prose is pulled, never pushed.** No default payload (manifest, `Response`, `PredictResult`) carries guidance prose — only intent IDs and flags (`TestManifestGuidanceBudget`). Prose reaches readers on demand: the skill sections rendered at read time, `pulse_explain` / `pulse_recommend`, the virtual skills, and the book's Analysis Guide that `internal/docgen` renders (`make docs`, also `pulse docs export`).
- **Every registered operator needs a `Purpose`**, and an inferential one an `Interpretation` (`TestSkillsCoverAllPurposes`, `TestInterpretationCoversOutputs`). A Purpose, Interpretation, glossary or `ComponentSchema` edit ends with `make docs` in the same change (`TestDocsGeneratedCurrent`).
- **Built-in bands come only from the convention registry**, and every built-in prose string passes the prose lint.

Load it before changing any guidance registry or its validator (`internal/descriptor/` files named for intents, glossary, purposes, interpretations, conventions, guidance), the prose lint, the virtual skills (`internal/skills/virtual.go`), root `guidance.go` / `extensions_guidance.go`, a `details.effect_size.*` key, an overlay kind's `Inferential` flag, a shared distribution primitive (`internal/statdist`, the processing test-statistic helpers) or the R reference goldens.

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `guided-analysis/model-and-recommend.md` | Data model; Purpose limits; Follow-ups; Aliases (`Purpose.KnownAs`); Glossary limits; Intent taxonomy; Manifest projection; Recommend |
| `guided-analysis/explain-request.md` | Explain (Input; Output; Steps; Predict; Profiles + prose) |
| `guided-analysis/explain-results.md` | Explain results (Response mode; Result roots; Operator mode; Surfaces) |
| `guided-analysis/skills-and-docgen.md` | Glossary and the jargon rule; Virtual skills; Skills for an intent; Generated skill sections; Generated reference (docgen) |
| `guided-analysis/interpretation-and-gates.md` | Interpretation; Paths; Needs reading vs self-reading; Static validation; Prose lint; Runtime probes; Overlay `Inferential`; Shared p-value rules; Effect-size keys; Caveats; Gates; Two tiers over ONE validator per type, BOTH binding; Exemption ledger; Budget gate |
| `guided-analysis/conventions-and-oracle.md` | Convention registry; Fixture contract; Statistical primitives and the R oracle; Review record; Extension hook |
