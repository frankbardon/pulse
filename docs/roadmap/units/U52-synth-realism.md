---
id: U52
slug: synth-realism
title: "Pulse can synthesize time series, outcomes drawn from a model, and privacy-checked twins without outside tooling"
track: API & release
size: L
status: not-started
depends_on: []
soft_depends_on: [U50]
blocks: [U32]
todo_items: [362, 363, 364, 365, 366, 367, 368, 369, 370]
branch: synth-realism
---

# U52 — synth-realism

**Outcome:** Pulse can synthesize time series, outcomes drawn from a model, and privacy-checked twins without outside tooling.

**Track:** API & release · **Size:** L · **Depends on:** none · **Soft:** [U50](U50-local-time.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

Pulse's synthesizer generated four of the six example datasets on its own. Two needed outside generators: the multi-time-zone data, because synth has no `datetime` type and no time-dependent effects (a trend over dates, a planted incident window, local business-hour profiles), and the credit data, which used synth but had to keep helper columns because rules have no random draw. The privacy example found the twin workflow's limits: `--fit-models` takes no numeric predictors; conditional-pair arbitration is first-declared, so an unrelated first field claims every relationship (churn pseudo-R² 0.198 → 0.0002 without `--fit-models`); dependencies inside one set field are not captured (5,113 impossible TV-without-broadband rows); the fidelity report scores against the captured model, not the source, so it reports churn "recovered" while churn by contract is compressed; marginal tests at n = 80,000 flag trivial differences; there is no synthetic-only output, no constraints on the profile path and no disclosure metric.

**Source of inspiration:** the showcase examples under `docs/showcase/` ([multi-time-zone ops](../../showcase/multi-timezone-ops/README.md), [credit risk model](../../showcase/credit-risk-model/README.md), [privacy-safe sharing](../../showcase/privacy-safe-sharing/README.md), [A/B test readout](../../showcase/ab-test-readout/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/synthetic-data.md` (load first: every failure it records was silent)
- `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md`, `skills/synth-rule-expressions.md`, `skills/synth-profile-capture.md`, `skills/synth-fidelity-report.md`, `skills/synth-set-fields.md`, `skills/join-design.md`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#362** (Synth realism) A synth `datetime` type with zone-aware generation, sharing `internal/temporal`
- [ ] **#363** (Synth realism) Time-dependent effects: trends and seasonality over a date field and windowed effects (a planted incident between two dates), as distributions or rule functions
- [ ] **#364** (Synth realism) Seeded random draws inside rules (`uniform()`, `bernoulli(p)`) so an outcome drawn from a model needs no helper columns, plus helper fields that are not written to the output
- [ ] **#365** (Synth realism) Spec idioms and warnings: a declared, rule-assigned categorical needs no sampler; the `owns_nulls` warning fires in the right direction
- [ ] **#366** (Synth realism) Profile capture: numeric predictors in `--fit-models`; conditional-pair arbitration by association strength (Cramér's V / adjusted η²) or author choice; dependencies between options of one set field
- [ ] **#367** (Synth realism) Fidelity report: a source-versus-synthetic utility section (per-level target rates for captured categoricals, or a refit of a supplied model on both), effect-size thresholds beside p-values for marginals, scoring for `packed_bool` / `u4` / `date` / `set_*` marginals, and a note when a rule causes a flag
- [ ] **#368** (Synth realism) Sharing workflow: `--synthetic-only` output from the profile path, `constraints[]` accepted on the profile path, and disclosure metrics (exact-match rate, distance to closest record) beside the fidelity report; differential privacy is a decision below
- [ ] **#369** (Synth realism) Docs: join keys never match on null (`join-design.md`), and synth rules bind `set_*` as a map while query formulas bind a list, cross-linked
- [ ] **#370** (Synth realism) Acceptance: the multi-time-zone and credit datasets regenerate from synth specs alone with no helper columns; the privacy twin keeps the source's churn drivers (pseudo-R² gap closed materially, TV-without-broadband at zero) and ships with a disclosure report

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

## Human inputs & decisions

- **Open:** add a differential-privacy noise mechanism to profile capture for v1, or document that twins carry no formal privacy guarantee and rely on the disclosure metrics.
- **Open:** set fields as join keys (today refused), which the disclosure check needed.
