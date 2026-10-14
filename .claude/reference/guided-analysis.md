# Guided analysis — intents, Purpose, Interpretation, glossary

**How to load.** This file is an INDEX: the preamble below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/guided-analysis/` your change touches; find one with `grep -l '<identifier>' .claude/reference/guided-analysis/*.md`. A section-qualified pointer at this file resolves through the section map.

The guided-analysis metadata model (roadmap U07) lets Pulse say what each operator is for, and how to read what it returns, in words a non-statistician recognises. **All of it is DECLARATION, never execution:** nothing in the engine reads it to run a request, and guidance prose is PULLED on demand — never pushed into a default payload (roadmap principle 6). CLAUDE.md "Guided analysis" carries the always-load half; this file is the contract.

Load it before changing any of: `descriptor/guidance.go`, `internal/descriptor/{intents,glossary,purposes,purpose_validate,interpretations,interpretation_validate,guidance_prose,guidance_skills,conventions}.go` (plus the per-category `purposes_*.go` / `interpretations_*.go` files and `testdata/conventions.json`), the prose lint (`guidance_lint_test.go`; `purposeTexts` covers `not_for[i].when` and `follow_ups[i].when`), `internal/skills/virtual.go`, root `guidance.go` / `extensions_guidance.go`, a `details.effect_size.*` key, an overlay kind's `Inferential` flag, a shared distribution primitive (`internal/statdist`, `internal/processing/test_stat.go` / `test_studentized.go`, the normal helpers) or the R reference goldens.

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `guided-analysis/model-and-recommend.md` | Data model; Purpose limits; Follow-ups; Aliases (`Purpose.KnownAs`, U23); Glossary limits; Intent taxonomy; Manifest projection; Recommend (U22) |
| `guided-analysis/explain-and-skills.md` | Explain (U22); Glossary and the jargon rule; Virtual skills; Skills for an intent (U23 E2-S3); Generated skill sections; Generated reference (docgen) |
| `guided-analysis/interpretation-and-gates.md` | Interpretation; Paths; Needs reading vs self-reading; Static validation; Prose lint; Runtime probes; Overlay `Inferential`; Shared p-value rules; Effect-size keys; Caveats; Gates; Two tiers over ONE validator per type, BOTH binding; Exemption ledger; Budget gate |
| `guided-analysis/conventions-and-oracle.md` | Convention registry; Fixture contract; Statistical primitives and the R oracle; Review record; Extension hook; Status (U21 delivered); Out of scope (owned later) |
