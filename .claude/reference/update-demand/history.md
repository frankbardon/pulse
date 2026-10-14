# Update Demand — history (NOT loaded by default)

Decision history and superseded wording relocated out of the live slices. Do not load this file for a change; every live row is in a per-area slice named by the index `.claude/reference/update-demand.md`. Kept so the record survives. Below: the pre-compression CLAUDE.md table's introduction and the rows that duplicate a live row VERBATIM (every other pre-compression row moved, unedited, to its area slice under "Category rows").

## The CLAUDE.md table, pre-compression (relocated verbatim at E1-S3)

CLAUDE.md's `## The Update Demand` was compressed to one row per category at E1-S3 — several of its
rows had grown into paragraphs, and the section is loaded on every session. What follows is that
section exactly as it stood before the compression: the per-category rows that were merged, the full
companion file lists that now read "the sites in the reference row", and the crosstab
`margin_aggregations` and `ComposedResponse` rows in full. Nothing here was edited; where a row below
and a row in the exhaustive table above disagree in wording, the table above is the live one and this
block is the record of what CLAUDE.md used to carry.

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

This is the compressed surface — the full per-contract trigger table lives at `.claude/reference/update-demand.md`.

| If you change... (category) | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator | `skills/op-agg-<kebab>.md` (atomic skill) + `internal/descriptor/capabilities_aggregators.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered attribute | `skills/op-attr-<kebab>.md` (atomic skill) + `internal/descriptor/capabilities_attributes.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered filterer | `skills/op-filter-<kebab>.md` (atomic skill) + `internal/descriptor/capabilities_filterers.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered window operator | `skills/op-win-<kebab>.md` (atomic skill) + `internal/descriptor/capabilities_window_ops.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllWindowTypes`, `TestManifestOperatorsComplete` |
| A registered feature operator | `skills/op-feat-<kebab>.md` (atomic skill) + `internal/descriptor/capabilities_features.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered statistical test (`TEST_*`) or tier-2 variant | `skills/op-test-<kebab>.md` (atomic skill) + `types/streamability.go` + `internal/descriptor/capabilities_tests.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestStreamability_TestsKnown`, `TestManifestTestsComplete` |
| A registered regression (`REG_*`) or modifier | `skills/op-reg-<kebab>.md` (atomic skill; modifiers ship as `skills/op-reg-mod-<kebab>.md`) + `internal/descriptor/capabilities_regressions.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllRegressions`, `TestManifestRegressionsComplete` |
| A registered synth distribution | `skills/op-synth-<kebab>.md` (atomic skill) + `internal/descriptor/capabilities_distributions.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllSynthDistributions`, `TestManifestDistributionsComplete` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + Fixups — plus an `internal/descriptor/error_owners.go` owner entry (feature names, or `shared`; a Message may name only owning features) | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete`, `TestErrorOwners_Complete`, `TestErrorMessagesNameOnlyOwners` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| Any Request slot, Response slot, capability block, or Execution-mode wiring | See `.claude/reference/update-demand.md` for the per-slot trigger row | per-slot test suites cited there |

Defer the doc/skill update to "a follow-up PR" and the follow-up will not happen. Update in the same PR or do not merge.
