---
id: U44
slug: strict-input
title: "Pulse never silently ignores what it is given: a misspelled key, a dropped row or a failed run is always visible"
track: API & release
size: M
status: not-started
depends_on: []
soft_depends_on: [U35]
blocks: [U32]
todo_items: [301, 302, 303, 304, 305, 306, 307, 308, 309, 310, 311, 375, 397, 460, 462]
branch: strict-input
---

# U44 — strict-input

**Outcome:** Pulse never silently ignores what it is given: a misspelled key, a dropped row or a failed run is always visible.

**Track:** API & release · **Size:** M · **Depends on:** none · **Soft:** [U35](U35-predict-runtime-parity.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

Across the six showcase examples the most expensive failures were silent ones. A misspelled request key (`"filters"` for `"filterers"`, `"direction"` in a sort, `"unit"` for `GROUP_DATE`'s `component`) ran without it and returned a plausible wrong answer, even under `--strict`. A synth spec's `rho` (the documented correlation key) was ignored, so a planted correlation came out as zero. A CSV import dropped 1,167 rows with an empty `errors` and `warnings` and exit status 0. `bootstrap_iters: 200` silently became 1,000. And every CLI leaf exits 0 under `--json` even when the envelope carries a fatal error, so a scripted pipeline cannot notice.

This unit makes the input contract strict and the failure contract visible: unknown keys are coded refusals everywhere (the CLI path matches the MCP strict decode that already raises `PULSE_REQUEST_UNKNOWN_FIELD`), partial imports warn, requested knobs are honoured and echoed, and exit status follows the envelope. It also fixes the shipped examples that only worked because a wrong key was ignored, and tidies the output inconsistencies every example tripped over.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([brand tracker](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/brand-tracker/README.md), [A/B test readout](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/ab-test-readout/README.md), [retail at scale](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/retail-at-scale/README.md), [multi-time-zone ops](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/multi-timezone-ops/README.md), [privacy-safe sharing](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/privacy-safe-sharing/README.md), [credit risk model](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/credit-risk-model/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- CLAUDE.md "Output Format Contract" (the envelope and coded errors) and "Error handling"
- `.claude/reference/update-demand.md` (error-code row, `--json` envelope row)
- `.claude/reference/feature-profiles.md` (where `PULSE_REQUEST_UNKNOWN_FIELD` is raised today)
- `docs/src/cli/` (documented exit codes), `skills/session-bootstrap.md`, `skills/synth-correlations.md`, `skills/label-display.md`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#301** (Strict input) Unknown keys are refused at every request root and nested spec (sort entries, slot `params`, grouper, overlay and test params, Compose / Chain / Facet roots) in predict and process, with a did-you-mean suggestion; the CLI path matches the MCP strict decode (`PULSE_REQUEST_UNKNOWN_FIELD`). Repros: `"filters"`, `sort[].direction`, `GROUP_DATE params.unit`
- [x] **#302** (Strict input) Synth specs decode strictly: unknown spec keys and distribution params are refused (`exponential {rate}` today silently uses `lambda: 1`); `correlations[]` accepts `rho` as the skill documents (or the skill is corrected), never a silent zero
- [x] **#303** (Strict input) Every CLI leaf exits non-zero when the envelope carries a fatal error, under `--json` too, as `docs/src/cli` documents; the envelope itself is unchanged
- [x] **#304** (Strict input) Imports never drop rows silently: a partial import puts a coded warning with the dropped-row count and the first failure into `warnings` (e.g. a blank cell on a non-nullable `set_*` column), and `--strict` makes it an error
- [x] **#305** (Strict input) Requested knobs are honoured and echoed: `bootstrap_iters` is used as given (`<= 0` means the default 1,000, not a floor), and `RegressionResult` reports replicates requested and succeeded
- [x] **#306** (Strict input) Label bindings apply to crosstab axis keys and pairwise-overlay pair keys, or the request draws a coded `PULSE_LABEL_NOT_APPLIED` warning; never accepted-and-ignored
- [x] **#307** (Strict input) Overlay-layer warnings follow the `{code, message, details}` contract (today `Code` / `Message` / `Details`) and runtime reason strings carry no predecessor prefix (`ORBIT_STATS_SKIP_POOLED_SE_INVALID`); `TestNoOrbitPrefix*` covers runtime strings. Fix the shipped examples that relied on ignored input: `overlays/40_crosstab_pairwise_prop_z.json` (empty layer) and `crosstab/11_date_grouper_axis.json` (`unit`) — *partial (E1-S4): reason strings renamed `PULSE_STATS_SKIP_*`, `TestNoOrbitPrefix*` scan every non-test Go string literal, `40_crosstab_pairwise_prop_z.json` normalized (non-empty layer, asserted) and `11_date_grouper_axis.json` fixed (E1-S2); `OverlayWarning` json tags `{code, message, details}` landed in E3-S1*
- [ ] **#308** (Strict input) Path and flag hygiene: a relative `Options.DataDir` is absolutised at `New` (or refused there), `api lookup -i` overrides a request file's cohort as its help states, and `cohort inspect` accepts `-i` like every other leaf
- [x] **#309** (Strict input) Warnings that matter at run time are not predict-only: the process envelope repeats `PULSE_FEAT_TARGET_LEAKAGE_RISK`, and `pulse explain` carries predict's warnings — *E2-S5: every run-time warning reaches the CLI envelope's top-level `warnings` and every stream; predict-facet's small `discrete_top_k` carries the runtime's code; `rng_seed: 0` is deterministic per request*
- [ ] **#310** (Strict input) Output consistency: `cohort inspect` shows nullability and `decimal128` precision/scale; `sample` / `lookup` rows print `date` / `datetime` as calendar strings; Compose responses echo each slot's `label`; import / export / shard `--json` data uses snake_case keys and type names (wire decision below) — *partial (E3-S1): import / export / convert / shard `--json` data snake_case with type names, fixed in place (`format_version` stays "1.1")*
- [ ] **#311** (Strict input) Acceptance: a table test misspells a key in every slot family and gets a coded refusal; all six showcase examples run under `--strict` with nothing ignored, and their scripts drop the `.errors | length` checks in favour of exit status — *partial (E1-S4): in-pulse table test `TestStrictInput_EveryFamilyAndRootRefusesAMisspelledKey` (every derived slot family × every root, CLI predict + process and MCP predict + runtime, synth spec, every template target); the showcase-example run under `--strict` and the script changes remain (casebook follow-up)*

## Scope

**In scope**
- Strict decode for every request root, nested spec and synth spec
- Exit status, partial-import warnings, knob echo
- Label application to crosstabs
- The shipped-example fixes
- Output consistency items above

**Out of scope**
- New features; this unit only makes existing contracts strict and visible
- Predict/runtime parity beyond the items named here ([U35](U35-predict-runtime-parity.md) owns the general ledger)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(strict-input/E<n>-S<m>): …`; close each epic with `milestone(strict-input/E<n>): vertical slice complete — <epic title>`.

### E1 — Nothing is ignored
- S1: strict decode for request roots and nested specs, with did-you-mean
- S2: strict synth spec decode; `rho`
- S3: shipped-example fixes and the runtime-string prefix gate

### E2 — Failures are visible
- S1: exit status follows the envelope on every leaf
- S2: partial-import warning; `bootstrap_iters` honoured and echoed; label application or warning
- S3: run-time warnings repeated; explain carries predict warnings

### E3 — Outputs are consistent
- S1: inspect nullability and precision; calendar dates in rows; Compose label echo; envelope key casing
- S2: convert the showcase scripts to exit-status checks and `--strict`

## Acceptance criteria

- [ ] Every misspelling in the examples' history (`filters`, `direction`, `unit`, `rho`, `rate`) is a coded refusal in predict and process
- [ ] `pulse api process -r bad.json --json; echo $?` prints a non-zero status for a fatal envelope
- [ ] The six showcase examples pass under `--strict`
- [ ] Showcase examples simplified where this unit allows (every example's run script (exit status instead of envelope parsing); the brand tracker's crosstab label workaround): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestManifestErrorCodesComplete`, `TestNoOrbitPrefix`, `TestNoOrbitPrefixes`, `TestPayloadSchemaGolden`
- New: a misspelled-key table test across every slot family; an exit-status test per CLI leaf; a partial-import warning test
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `errors/fixup_metadata.go` + `internal/descriptor/error_owners.go` for any new code (`PULSE_LABEL_NOT_APPLIED`, a partial-import warning code)
- `docs/src/cli/` exit-code tables and `skills/session-bootstrap.md` (scripts can trust exit status)
- `skills/synth-correlations.md`, `skills/label-display.md`, the fixed example files
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** strict by default, or strict only under `--strict` with a warning otherwise. A silent wrong answer argues for strict by default before v1 freezes the surface.
- **Resolved (E3-S1):** the import / export / shard envelope key rename (`RowsImported` → `rows_imported`) shipped IN PLACE with no `format_version` bump — an owner exception to "renames bump", recorded in the embedder migration doc.

## Inherited from U41

Filed from [U41 handed on](U41-derived-cohort.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#375** — `pulse import auto` (managed import) takes `--float`, `--schema` and `--ignore-extra` like `import <fmt>`; today they reach the managed import only through `ImportSpec` and MCP `pulse_import`. Found in U41 (E1-S2).

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#397** — In a plain (non-sweep) compose slot, an unknown grouper key such as `"label"` on `GROUP_RANGE` is silently ignored, while a sweep body's strict decode refuses it. A plain request should refuse unknown grouper keys the same way, or the two paths should be unified. Found in U42 (E4-S1) while converting the MMM fixture (`docs/roadmap/fixtures/mmm-harbor-pine/req/10`–`11` used to carry `"label"`).

## Handed on from U35

Filed from [U35](U35-predict-runtime-parity.md); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#460** Auto labels (`Options.AutoLabels`) are not injected on `ProcessChain` stages >= 1 (explicit label bindings are applied since U35 E3-S2); a stage's rows stay un-labelled unless bound. Documented in the chain-join-facet slice. Found in U35 (E3-S2)
- [x] **#462** Facet runtime warnings carry no details: `FacetResult.Warnings` is `[]string`, so the runtime's `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` rides as `"CODE: message"` while predict carries `details`. Make the facet warnings coded entries (additive wire change). Found in U35 (E5-S3)
