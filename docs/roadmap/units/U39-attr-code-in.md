---
id: U39
slug: code-in-attribute
title: "A top-box share of the whole base is one attribute and one weighted mean on the fused crosstab path"
track: Guided analysis
size: S
status: done
depends_on: []
soft_depends_on: []
blocks: []
todo_items: [240, 241, 242, 243, 244, 245]
branch: code-in-attribute
---

# U39 — code-in-attribute

**Outcome:** A top-box share of the whole base is one attribute and one weighted mean on the fused crosstab path.

**Track:** Guided analysis · **Size:** S · **Depends on:** none · **Unblocks:** none

## Summary

"What share of the whole base picked 4 or 5?" needs a per-row 0/1 indicator whose weighted mean is the share. The existing building blocks each fell short:

- `FILTER_INCLUDE` on the codes shrinks the base, so every cell reads 100%.
- `ATTR_FORMULA` (`field in [...] ? 1 : 0`) gives the right number but declines crosstab fusion, so the grid runs the slower buffered arm.
- `ATTR_SET_HAS` tests a set-field member, not a categorical or integer code.

`ATTR_CODE_IN` is a built-in row-local attribute that emits `1` when a field's value is one of a list of codes, else `0`. It reads only `Field`, so it streams and fuses with no gate change, and `AGG_WEIGHTED_MEAN` over its label is the top-box share of the whole base.

Numbering note: appended as U39 after U38, not renumbered. This unit was not in the original plan.

## Decisions

- **Types:** categorical_* (matched by LABEL, through the dictionary) and unsigned integers `u4`–`u64` (matched by value, through `matchValueKey`). No floats (equality is a trap), no dates (`FILTER_DATE_RANGES` owns them), no `packed_bool` (already 0/1), no decimal or set.
- **Absent categorical code:** matches nothing, silently at runtime. A wave routinely lacks some codes, so a runtime warning would be noise.
- **Predict flag:** new code `PULSE_ATTR_CODE_NOT_IN_DICTIONARY`, a warning by default and an error under `--strict`, one per slot listing the missing codes, categorical only.
- **Impossible integer codes** (not a whole number, or out of the type's range): refused with `PROCESSING_CONFIG` at factory time and mirrored in predict.
- **Null input:** yields `0`, no `null_as` param. Attributes cannot emit null; rows to exclude are filtered first.
- **Codes param:** strings or JSON integers, non-empty, duplicates deduped.
- **`target` / `predictors` misuse:** refused with a fixup pointing at `label`, because `target` is the regression slot and would otherwise be silently ignored.
- **No `ATTR_CODE_VALUE` sibling.** Low value, and it hides a schema-design issue (a count stored as categorical).
- **Fusion:** no gate change. A built-in row-local, non-two-pass attribute reading only `Field` already fuses.

## What shipped

- **Operator:** `ATTR_CODE_IN` (row-local, `packed_bool` output, streamable), with its registry entry, manifest row, `Purpose`, feature row and example profile entry; atomic skill `op-attr-code-in` and a top-box example.
- **Predict:** the `PULSE_ATTR_CODE_NOT_IN_DICTIONARY` warning, and the runtime refusals re-derived in `internal/descriptor/predict.go` (which may not import `internal/processing`). A table-driven parity test (`TestCodeIn_PredictRefusalsMatchRuntime`) pins the two copies together.
- **Acceptance proof:** an end-to-end suite through the public facade on a hermetic weighted cohort. `ATTR_CODE_IN` + `AGG_WEIGHTED_MEAN` on a region x wave crosstab equals the hand-computed `sum(w | code in set) / sum(w)` BIT-EXACT (0/1 indicator, dyadic weights, so every partial sum is exact); it runs the `fused_crosstab` arm with predict `CrosstabFusable == true`, the fusion-disabled buffered arm gives the same bits, and it differs from `FILTER_INCLUDE` (every cell 1.0) and matches the `ATTR_FORMULA` oracle cell for cell.

## Deviations

- **Derived-column refusal surfaced by predict.** `Field` must be a cohort schema field; a column derived by an earlier attribute or feature is refused (`PROCESSING_CONFIG` "unknown field"). The predict-parity story (E1-S3) exposed it; it is documented as a skill gotcha, not changed.
- **Manifest golden regenerated twice.** The root manifest golden needed a second regeneration after the skill landed (E1-G1 fix), because E1-S1 regenerated it before E1-S2 added the skill and example, both of which the manifest lists.

## Follow-ups

- A general `target` / `predictors` refusal on every non-regression attribute (today only `ATTR_CODE_IN` refuses them; the rest ignore the slots silently).
- Optional: move the `ATTR_CODE_IN` parse and accept rules into a neutral shared package (like `internal/datepart`) so predict and runtime share one implementation instead of the predict copy pinned by `TestCodeIn_PredictRefusalsMatchRuntime`.
- Observed, pre-existing: the plain grouped (non-crosstab) path uses only `Request.Groups[0]`. The `.claude/reference/` notes say so, but the user-facing request docs do not; U35 owns multi-entry `Groups` (#200). No new work here.

## Gates & tests

- `TestCodeIn_PredictRefusalsMatchRuntime`, the field-type acceptance row, the fusion parity matrix row, and the end-to-end top-box suite.
- Update Demand gates: `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestEveryOperatorHasAnExampleTag`, `TestSkillsCoverAllPurposes`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestDocsGeneratedCurrent`.

## Release

Rolls into `v1.0.0-alpha.6` with the unreleased U12–U21 work. No `format_version` change.
