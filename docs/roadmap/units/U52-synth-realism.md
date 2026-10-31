---
id: U52
slug: synth-realism
title: "Pulse can synthesize time series, outcomes drawn from a model, and privacy-checked twins without outside tooling"
track: API & release
size: L
status: done
depends_on: []
soft_depends_on: [U50]
blocks: [U32]
todo_items: [362, 363, 364, 365, 366, 367, 368, 369, 370, 450, 457, 594, 595, 596, 597]
branch: synth-realism
---

# U52 — synth-realism

**Outcome:** Pulse can synthesize time series, outcomes drawn from a model, and privacy-checked twins without outside tooling.

**Track:** API & release · **Size:** L · **Depends on:** none · **Soft:** [U50](U50-local-time.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

Pulse's synthesizer generated four of the six example datasets on its own. Two needed outside generators: the multi-time-zone data, because synth has no `datetime` type and no time-dependent effects (a trend over dates, a planted incident window, local business-hour profiles), and the credit data, which used synth but had to keep helper columns because rules have no random draw. The privacy example found the twin workflow's limits: `--fit-models` takes no numeric predictors; conditional-pair arbitration is first-declared, so an unrelated first field claims every relationship (churn pseudo-R² 0.198 → 0.0002 without `--fit-models`); dependencies inside one set field are not captured (5,113 impossible TV-without-broadband rows); the fidelity report scores against the captured model, not the source, so it reports churn "recovered" while churn by contract is compressed; marginal tests at n = 80,000 flag trivial differences; there is no synthetic-only output, no constraints on the profile path and no disclosure metric.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([multi-time-zone ops](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/multi-timezone-ops/README.md), [credit risk model](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/credit-risk-model/README.md), [privacy-safe sharing](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/privacy-safe-sharing/README.md), [A/B test readout](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/ab-test-readout/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/synthetic-data.md` (load first: every failure it records was silent)
- `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md`, `skills/synth-rule-expressions.md`, `skills/synth-profile-capture.md`, `skills/synth-fidelity-report.md`, `skills/synth-set-fields.md`, `skills/join-design.md`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#362** (Synth realism) A synth `datetime` type with zone-aware generation, sharing `internal/temporal` (landed in U52 (synth-realism))
- [x] **#363** (Synth realism) Time-dependent effects: trends and seasonality over a date field and windowed effects (a planted incident between two dates), as distributions or rule functions (landed in U52 (synth-realism))
- [x] **#364** (Synth realism) Seeded random draws inside rules (`uniform()`, `bernoulli(p)`) so an outcome drawn from a model needs no helper columns, plus helper fields that are not written to the output (landed in U52 (synth-realism))
- [x] **#365** (Synth realism) Spec idioms and warnings: a declared, rule-assigned categorical needs no sampler; the `owns_nulls` warning fires in the right direction (landed in U52 (synth-realism))
- [x] **#366** (Synth realism) Profile capture: numeric predictors in `--fit-models`; conditional-pair arbitration by association strength (Cramér's V / adjusted η²) or author choice; dependencies between options of one set field (landed in U52 (synth-realism))
- [x] **#367** (Synth realism) Fidelity report: a source-versus-synthetic utility section (per-level target rates for captured categoricals, or a refit of a supplied model on both), effect-size thresholds beside p-values for marginals, scoring for `packed_bool` / `u4` / `date` / `set_*` marginals, and a note when a rule causes a flag (landed in U52 (synth-realism))
- [x] **#368** (Synth realism) Sharing workflow: `--synthetic-only` output from the profile path, `constraints[]` accepted on the profile path, and disclosure metrics (exact-match rate, distance to closest record) beside the fidelity report; differential privacy is a decision below (landed in U52 (synth-realism))
- [x] **#369** (Synth realism) Docs: join keys never match on null (`join-design.md`), and synth rules bind `set_*` as a map while query formulas bind a list, cross-linked (landed in U52 (synth-realism))
- [x] **#370** (Synth realism) Acceptance: the multi-time-zone and credit datasets regenerate from synth specs alone with no helper columns; the privacy twin keeps the source's churn drivers (pseudo-R² gap closed materially, TV-without-broadband at zero) and ships with a disclosure report (landed in U52 (synth-realism))
- [x] **#450** (Synth realism) Vector synthesis: generate a `vec_f32` / `vec_f64` field (dim, kind and scale range honoured; per-element marginals plus the inter-element correlation captured from a profile), and drop the `PULSE_SYNTH_VECTOR_UNSUPPORTED` refusal on `from-schema`, `from-profile`, `profile create` and augment. Found in U29 (E3-S4) (landed in [U29](U29-vector-field-types.md))

## Scope

**In scope**
- `datetime` and time effects
- Random draws and helper fields in rules
- Profile capture and fidelity report upgrades
- Sharing workflow and disclosure metrics

**Out of scope**
- A formal privacy guarantee unless the differential-privacy decision says otherwise

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(synth-realism/E<n>-S<m>): …`; close each epic with `milestone(synth-realism/E<n>): vertical slice complete — <epic title>`.

### E1 — Time-aware synthesis
- S1: `datetime`; time trends, seasonality and windows
- S2: random draws and helper fields in rules

### E2 — Faithful twins
- S1: numeric predictors; arbitration by strength; within-set dependencies
- S2: utility section, effect sizes, wider marginal scoring

### E3 — Shareable twins
- S1: `--synthetic-only`, constraints, disclosure metrics
- S2: convert the three examples' data paths

## Acceptance criteria

- [ ] The multi-time-zone and credit examples need no outside generator and no helper columns
- [ ] The privacy twin's logistic pseudo-R² is within an agreed tolerance of the source's
- [ ] Every new synth surface is deterministic for a fixed seed (byte-identical regeneration)
- [ ] Showcase examples simplified where this unit allows (multi-time-zone ops (data from synth); credit risk (no helper columns); privacy-safe sharing (faithful twin, synthetic-only file, disclosure report); A/B (no seed scan if effects are planted exactly)): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllSynthDistributions`, the synth determinism and fidelity suites, `TestNoZoneMathOutsideTemporal`
- New: datetime generation and DST tests, rule random-draw determinism, set-dependency capture, utility-section and disclosure-metric tests
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `synthetic-data.md` (load first), `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md` and the touched synth skills
- `docs/src/cli/flags.md` and `commandBindings` for new flags; `features.go` rows
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Handed on from U29

- **Vector synthesis (#450) — DONE in [U29](U29-vector-field-types.md).** U29 first made every synth surface refuse `vec_f32` / `vec_f64`, then lifted it before release in the same effort: `FieldSpec.Vector` (dim, kind, scale range, labels, a distribution family per element, the inter-element correlation through the scalar copula), the `FieldProfile.Vector` capture, augment, and the fidelity report's `vector_fields` arm. The refusal code never shipped and was removed. Contract: `.claude/reference/synthetic-data.md` (Vector fields).

## Human inputs & decisions

- **Decided (synth-realism PRD):** no differential-privacy mechanism for v1. A profile-derived twin is documented as not differentially private and carrying no formal privacy guarantee; the `disclosure` section (exact-match rate, DCR) is an audit aid, not a guarantee (`skills/synthetic-data.md`, `skills/synth-disclosure-report.md`, `docs/src/cli/synth-from-profile.md`).
- **Decided (synth-realism PRD):** set fields stay refused as join keys; the disclosure check measures over all output columns without a join.

## Handed on from U35

Filed from [U35](U35-predict-runtime-parity.md); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#457** Synth writer: add a dedicated test that a non-nullable field's null-bitmap bit stays 0 (the writer guards it in code; `TestCohortBuilder_NonNullableShardInNullableArchive` relies on it, nothing pins the synth path). Found in U35 (E1-S1, #207) (landed in U52 (synth-realism))

## Follow-ups filed by this unit

Open items found during synth-realism; tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#594** `datetime` profile capture keeps only UTC weekday / hour marginals (no per-day or per-month volume intensity, no local-zone profile). Found in U52 (E1-S2)
- [ ] **#595** The `disclosure` DCR pass has no limit of its own beyond the sample cap and the `max_estimated_memory` charge; a dedicated comparisons limit is an owner call. Found in U52 (E3-S2)
- [ ] **#596** The `utility.model_fits` coefficient `ratio` is unstable when the source coefficient is near 0 (documented, not changed). Found in U52 (E2-S3)
- [ ] **#597** Profile pair arbitration has no minimum strength: two pairs contending for one target with strengths both ≈ 0 resolve arbitrarily (a no-effect `region` took `contract` instead of `plan`); add a floor or a declared-order tie-break below it. Found in U52 (E3-S4)
