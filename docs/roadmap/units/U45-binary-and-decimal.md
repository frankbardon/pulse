---
id: U45
slug: binary-and-decimal
title: "A 0/1 flag and an exact decimal work wherever a category or a number does, and a formula can say what it means"
track: API & release
size: M
status: done
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

- [x] **#312** (Binary flags and decimals) `packed_bool` is accepted as a two-level category wherever one is: `TEST_PROP_Z`, `TEST_CHISQ` (rows or cols), `FEAT_TRAIN_TEST_SPLIT` `stratify`, and every `split_by`; skills and manifest `accepts_types` match the runtime — **delivered by E1**: one shared level accessor reads a `packed_bool` as the labels `"0"` / `"1"` (never as missing), the acceptance table, manifest `accepts_types`, the guided binder (a `packed_bool` binds level slots; k-group tests prefer a split with three or more levels) and the skills agree; proven end to end by E4-S2's showcase shapes
- [x] **#313** (Binary flags and decimals) Type-refusal codes say what they mean (a "must be categorical" refusal is not `PULSE_TEST_FIELD_NOT_NUMERIC`), messages do not repeat words ("field field"), and predict and runtime raise the same code for each refusal (feeds [U35](U35-predict-runtime-parity.md) #198) — **delivered by [U35](U35-predict-runtime-parity.md) (E2-S3)**: every "does not accept this type" refusal is `PULSE_FIELD_TYPE_UNSUPPORTED` on both sides, messages come from the one table (`internal/fieldaccept`), and the acceptance gate enumerates the refusal codes it sees
- [x] **#314** (Binary flags and decimals) `decimal128` coverage: `AGG_NULL_COUNT` reads the bitmap and needs no decimal math; `AGG_COUNT` returns an integer; a sum over an all-null group is `null`; `ATTR_FORMULA` can divide and compare decimals (decimal-aware operators or a working `float()`), and predict refuses what process would — **aggregator half delivered by E3-S1**: `AGG_NULL_COUNT` takes `decimal128` (the null bitmap only; predict and runtime agree, `TestDecimalAggregation_PredictMatchesRuntime`), and `AGG_COUNT` / `AGG_DISTINCT_COUNT` / `AGG_NULL_COUNT` over a decimal emit a JSON integer in every shape (`TestDecimalCounts_EmitIntegers`); the all-null sum stays `0` / `"0.00"` by owner decision (an empty sum is defined; `n == 0` in the components floor is the emptiness signal). **Formula half delivered by E3-S2** (owner option C): a decimal binds with its field scale and `+ - * /` / comparisons over decimal × decimal or decimal × number are exact (`/` at `max(s1, s2, 4)`, banker's), `float()` is the f64 escape, an `ATTR_FORMULA` decimal result downcasts to its float64 column; FILTER_EXPRESSION, ATTR_FORMULA, `total()` arguments and the null-row twin share one implementation (`internal/exprenv` decimal.go), and predict refuses an ill-typed decimal expression with the runtime's `PULSE_FORMULA_COMPILE_ERROR` (`TestFormulaCompile_DecimalPredictMatchesRuntime`, `TestDecimalFormula_ExactInFilterAndFormula`)
- [x] **#315** (Binary flags and decimals) Formula diagnostics: a compile failure carries expr-lang's reason in the message, and predict compiles every formula so the failure surfaces before the run — **delivered by E2-S2** (`PULSE_FORMULA_COMPILE_ERROR`, reason in the message and `details.compile_error`) **and E2-S3** (the field-reference walk — predict's and the runtime's entry check — type-compiles every `ATTR_FORMULA`, `FILTER_EXPRESSION` and `total()` argument through `internal/exprenv`; parity gate `TestFormulaCompile_PredictMatchesRuntime`)
- [x] **#316** (Binary flags and decimals) `ATTR_FORMULA` may return null, and the formula environment gains `exp`, `log`, `log1p`, `sqrt` and calendar helpers (`weekday`, `month_index`, `date_trunc`) evaluated through `internal/temporal` — null results and `exp` / `log` / `sqrt` were **delivered by U26**; **the rest by E4-S1**: `log1p` (overlay formulas admit it too), `weekday` (ISO 1 = Monday … 7), `month_index` (`year*12 + month-1`), `date_trunc(unit, day)` (`week` Monday start / `month` / `quarter` / `year`) over epoch days and `to_day(ts)` (a datetime's local day in the request's inherited zone, request `time_zone` → `Options.DefaultTimeZone` → UTC, carried to the formula / filter builders on the slot by `ZonedExpressions`), all in `internal/temporal`; reserved in `internal/exprenv`; a null argument answers null; a bare `datetime` passed to a day helper or a literal unknown unit is `PULSE_FORMULA_COMPILE_ERROR` at predict and run alike (`exprenv.CalendarGuard`, `TestFormulaCompile_CalendarPredictMatchesRuntime`), a per-row unknown unit `PROCESSING_CONFIG`; oracles `TestCalendarFormula_Oracle`, `TestCalendarDay_MatchesStdlib`, `TestToDay_Zone`; skill `calendar-formulas`
- [x] **#317** (Binary flags and decimals) Acceptance: the credit example drops its `outcome` twin and the `2.718… **` idiom; the A/B example stores binary metrics as `packed_bool`; the privacy example runs `TEST_CHISQ` on `churned` directly; the retail retention flags use a calendar helper instead of twelve hard-coded epoch-day ranges — *in-pulse proof (owner decision; the pulse-casebook rewrite is a separate follow-up): E4-S2 ships the four shapes as examples over a new closed-form fixture `subscribers.pulse` — `regression/18_logistic_flag_score` (REG_GLM binomial straight on a `packed_bool` `defaulted`, scored with `exp` in ATTR_FORMULA), `tests/35_prop_z_on_flag` (`TEST_PROP_Z` `success: "1"` on a `packed_bool` `converted`, `split_by` arm), `tests/36_chisq_on_flag` (`TEST_CHISQ` on a `packed_bool` `churned`), `attributes/21_retention_month_index` (`month_index` difference retention flags) and `attributes/22_decimal_margin` (exact decimal compare and ratio); each predicts clean under Strict and is pinned against a stdlib oracle (`TestShowcase_*` in `showcase_flags_money_test.go`, CLI `TestShowcase_CLIStrictFlagTests`)*

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
- S2: refusal-code naming and parity (and the guided binder)
- S3: skills

### E2 — Formula diagnostics
- S1: one shared formula environment, `internal/exprenv`
- S2: `PULSE_FORMULA_COMPILE_ERROR` with the compiler's reason
- S3: predict and the runtime entry check type-compile every formula

### E3 — Exact decimals
- S1: decimal counts and `AGG_NULL_COUNT`
- S2: decimal formulas, built-ins, ternary / `??`

### E4 — Calendar formulas, proof and closure
- S1: calendar helpers, `to_day` zone
- S2: the four showcase shapes as in-pulse examples with oracles
- S3: roadmap and migration guide

## Acceptance criteria

- [x] One `packed_bool` outcome column serves a proportion test, a chi-square, a stratified split and a logistic fit
- [x] A formula dividing two `decimal128` fields runs, and a formula returning `nil` yields a null
- [x] The field-type acceptance gate (`TestFieldTypeAcceptance_PredictMatchesRuntime`, no ledger since U35 E2-S3) passes: no predict/runtime divergence
- [x] Showcase examples simplified where this unit allows (credit risk (no twin outcome column, idiomatic logistic score); A/B (binary metrics stored as flags); privacy-safe sharing (direct chi-square); retail (retention flags)): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [x] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- the field-type acceptance tests, `TestCodesHaveFixups`, `TestSkillsCoverAllOperatorComponents`, `TestNoZoneMathOutsideTemporal`, `TestPredict_Streamable_MatchesRuntime`
- New: a `packed_bool` matrix across tests/split, decimal formula arithmetic, formula null and function oracles
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- The touched operators' atomic skills and `internal/descriptor/capabilities_*.go`; `skills/expression-language.md`; `skills/financial-cohorts.md`; `make docs`
- Renamed or new error codes in `errors/fixup_metadata.go` and `error_owners.go`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Resolved (option C):** decimal formulas run as exact decimal arithmetic (`+ - * /`, ordering, equality), with `float()` as the explicit f64 escape; an `ATTR_FORMULA` decimal result downcasts to its float64 column.
- **Resolved:** a `packed_bool` level reads as the labels `"0"` / `"1"` (`success: "1"`). `TEST_CHISQ` orders row / column labels by first appearance (pre-existing, for categoricals and `packed_bool` alike), so `"0"` / `"1"` names the labels, not their order.
- **Resolved:** an empty decimal `AGG_SUM` stays `0` / `"0.00"` (an empty sum is defined; `n == 0` in the components floor is the emptiness signal). `ANOVA_RM` subject stays categorical-only.
- **Resolved:** `internal/exprbuiltins` merged into `internal/exprenv` (internal package; no public symbol moved).
- **Resolved:** `format_version` stays `"1.1"`; every break is a behaviour change on an existing shape, recorded in [Changes from U45](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u45-binary-and-decimal).
- **Resolved (E4-S2):** the #317 proof is in-pulse (examples over a closed-form `subscribers.pulse` fixture with stdlib oracles); the `pulse-casebook` rewrite is a separate follow-up (#469).

## Notes

- Accepted residual: `%` over a decimal reached only through an untyped container (`{"a": price}.a % 2`) fails at run time (its message names `float()`), not at compile. Stated in `skills/decimal-formulas.md`.
- Skill budget headroom: `statistical-testing`, `expression-language`, `financial-cohorts` and `streaming-and-watching` sit at or within a few bytes of the 6,000 B topical hard budget; the next addition to any of them needs a child skill or displaced text.
- `internal/guide/bind.go` keeps a `manyGroupTests` table that duplicates a minimum-group-count notion; if capabilities ever declare a min group count, the binder should read it.

## Handed on

| Item | To | Why |
|---|---|---|
| #467 `FilterToFileRequest` has no `TimeZone` field | [U50](U50-local-time.md) | a public-API addition (API golden, predicate hash, output name); `to_day` there reads only `Options.DefaultTimeZone` |
| #468 `FILTER_DATE_RANGES` on `FilterToFileWithRequest` is refused with an uncoded error | [U50](U50-local-time.md) | pre-existing; needs a coded refusal or a translation |
| #469 convert the casebook credit / A-B / privacy / retail examples, refresh README and `REPRODUCE.md` | [U32](U32-docs-audit.md) | the showcase lives in the separate casebook repository; the in-pulse proof is #317 |

## Delivered

Every behaviour break, with before and after, is in [Changes from U45](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u45-binary-and-decimal). `format_version` stays `"1.1"`.
