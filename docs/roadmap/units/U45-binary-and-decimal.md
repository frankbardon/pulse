---
id: U45
slug: binary-and-decimal
title: "A 0/1 flag and an exact decimal work wherever a category or a number does, and a formula can say what it means"
track: API & release
size: M
status: in-progress
depends_on: []
soft_depends_on: [U35]
blocks: [U32]
todo_items: [312, 313, 314, 315, 316, 317]
branch: binary-and-decimal
---

# U45 — binary-and-decimal

**Outcome:** A 0/1 flag and an exact decimal work wherever a category or a number does, and a formula can say what it means.

**Track:** API & release · **Size:** M · **Depends on:** none · **Soft:** [U35](U35-predict-runtime-parity.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

Two field types kept forcing workarounds. A `packed_bool` outcome (the natural type for converted, churned, defaulted) is refused by `TEST_PROP_Z`, `TEST_CHISQ` and `FEAT_TRAIN_TEST_SPLIT` `stratify`, so the A/B example stored binary metrics as `no`/`yes` categoricals and the credit example kept a second `outcome` copy of its target. A `decimal128` money field cannot be divided in `ATTR_FORMULA`, `AGG_NULL_COUNT` refuses it (predict only warns), `AGG_COUNT` over it returns a string, and a sum over an all-null group returns `"0.00"`. The formula language also lacks `exp`/`log`, cannot return null, and hides its compile error, which pushed the credit example into `2.718281828459045 ** z` and forced request-level filters where a null was meant.

This unit makes both types first-class across operators and makes the formula language expressive enough for the scores and indicators the examples needed.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([A/B test readout](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/ab-test-readout/README.md), [credit risk model](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/credit-risk-model/README.md), [privacy-safe sharing](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/privacy-safe-sharing/README.md), [brand tracker](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/brand-tracker/README.md), [retail at scale](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/retail-at-scale/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/predict-inspect.md` and the field-type acceptance gate (`field_type_acceptance_test.go`; the ledger was deleted by U35 E2-S3)
- `skills/financial-cohorts.md`, `skills/expression-language.md`, `skills/op-attr-formula.md`, `skills/op-test-prop-z.md`, `skills/op-test-chisq.md`, `skills/op-feat-train-test-split.md`
- CLAUDE.md "Undefined figures are `null`"

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#312** (Binary flags and decimals) `packed_bool` is accepted as a two-level category wherever one is: `TEST_PROP_Z`, `TEST_CHISQ` (rows or cols), `FEAT_TRAIN_TEST_SPLIT` `stratify`, and every `split_by`; skills and manifest `accepts_types` match the runtime
- [x] **#313** (Binary flags and decimals) Type-refusal codes say what they mean (a "must be categorical" refusal is not `PULSE_TEST_FIELD_NOT_NUMERIC`), messages do not repeat words ("field field"), and predict and runtime raise the same code for each refusal (feeds [U35](U35-predict-runtime-parity.md) #198) — **delivered by [U35](U35-predict-runtime-parity.md) (E2-S3)**: every "does not accept this type" refusal is `PULSE_FIELD_TYPE_UNSUPPORTED` on both sides, messages come from the one table (`internal/fieldaccept`), and the acceptance gate enumerates the refusal codes it sees
- [ ] **#314** (Binary flags and decimals) `decimal128` coverage: `AGG_NULL_COUNT` reads the bitmap and needs no decimal math; `AGG_COUNT` returns an integer; a sum over an all-null group is `null`; `ATTR_FORMULA` can divide and compare decimals (decimal-aware operators or a working `float()`), and predict refuses what process would
- [x] **#315** (Binary flags and decimals) Formula diagnostics: a compile failure carries expr-lang's reason in the message, and predict compiles every formula so the failure surfaces before the run — **delivered by E2-S2** (`PULSE_FORMULA_COMPILE_ERROR`, reason in the message and `details.compile_error`) **and E2-S3** (the field-reference walk — predict's and the runtime's entry check — type-compiles every `ATTR_FORMULA`, `FILTER_EXPRESSION` and `total()` argument through `internal/exprenv`; parity gate `TestFormulaCompile_PredictMatchesRuntime`)
- [ ] **#316** (Binary flags and decimals) `ATTR_FORMULA` may return null, and the formula environment gains `exp`, `log`, `log1p`, `sqrt` and calendar helpers (`weekday`, `month_index`, `date_trunc`) evaluated through `internal/temporal`
- [ ] **#317** (Binary flags and decimals) Acceptance: the credit example drops its `outcome` twin and the `2.718… **` idiom; the A/B example stores binary metrics as `packed_bool`; the privacy example runs `TEST_CHISQ` on `churned` directly; the retail retention flags use a calendar helper instead of twelve hard-coded epoch-day ranges

## Scope

**In scope**
- Operator acceptance of `packed_bool` and `decimal128`
- Error code naming and predict/runtime code parity for these refusals
- Formula functions, null results and compile diagnostics

**Out of scope**
- New statistical tests
- Zone math anywhere but `internal/temporal` (`TestNoZoneMathOutsideTemporal`)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(binary-and-decimal/E<n>-S<m>): …`; close each epic with `milestone(binary-and-decimal/E<n>): vertical slice complete — <epic title>`.

### E1 — Binary flags are categories
- S1: `packed_bool` acceptance in tests and the split; skills and manifest
- S2: refusal-code naming and parity

### E2 — Decimals and formulas
- S1: `decimal128` in null count, count, sums and formulas
- S2: formula null results, math and calendar functions, compile diagnostics in predict
- S3: convert the credit, A/B, privacy and retail steps

## Acceptance criteria

- [ ] One `packed_bool` outcome column serves a proportion test, a chi-square, a stratified split and a logistic fit
- [ ] A formula dividing two `decimal128` fields runs, and a formula returning `nil` yields a null
- [ ] The field-type acceptance gate (`TestFieldTypeAcceptance_PredictMatchesRuntime`, no ledger since U35 E2-S3) passes: no predict/runtime divergence
- [ ] Showcase examples simplified where this unit allows (credit risk (no twin outcome column, idiomatic logistic score); A/B (binary metrics stored as flags); privacy-safe sharing (direct chi-square); retail (retention flags)): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- the field-type acceptance tests, `TestCodesHaveFixups`, `TestSkillsCoverAllOperatorComponents`, `TestNoZoneMathOutsideTemporal`, `TestPredict_Streamable_MatchesRuntime`
- New: a `packed_bool` matrix across tests/split, decimal formula arithmetic, formula null and function oracles
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- The touched operators' atomic skills and `internal/descriptor/capabilities_*.go`; `skills/expression-language.md`; `skills/financial-cohorts.md`; `make docs`
- Renamed or new error codes in `errors/fixup_metadata.go` and `error_owners.go`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** decimal formulas as exact decimal arithmetic or an explicit `float()` downcast. Exactness is the reason the type exists, so exact operators are preferable where expr-lang allows.
