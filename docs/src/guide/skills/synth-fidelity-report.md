```yaml
name: synth-fidelity-report
description: The `pulse synth from-profile` tagged top-up contract (`_synthetic` column, new output path, `--rows` counts new rows) and the `--fidelity-report` sections — every field type's marginal with effect sizes and rule notes, pairs, set fields, utility against the source — scored only over relationships generation actually applied.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, fidelity report, _synthetic, top-up, effect size, utility, --fidelity-model, rule_note]
requires: [capability:synth]
```

# Synth top-up and fidelity report

## Tagged top-up contract

`synth from-profile` (`SynthOptions.SourceCohort` / `--source`) always: (1) appends one `_synthetic` `packed_bool` field, `false` on copied rows, `true` on generated ones; (2) writes a **new** output path, distinct from `--source`, which is opened read-only and never mutated; (3) treats `--rows` as a count of NEW rows, never "top up to N total" (`--rows 500` against 200 source rows → 700 rows). Source records are re-encoded straight through, so structural rules reach GENERATED rows only. `SourceCohort` empty (`synth from-schema`) is the plain path — no tag column. Refusals: `PULSE_SYNTH_SOURCE_REQUIRED`, `PULSE_SYNTH_OUTPUT_REQUIRED`, `PULSE_SYNTH_OUTPUT_COLLISION`, `PULSE_SYNTH_ALREADY_TAGGED`, `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`.

## Fidelity report

`--fidelity-report <path.json>` (only with `SourceCohort` set) writes a `synth.FidelityReport` after generation, through the existing test operators. `fields[]` scores every scalar type: integers (incl. `u4`), floats and `date` (epoch days) by KS (`split_by: _synthetic`); `datetime` by KS over epoch seconds plus `shape` (UTC hour-of-day and weekday TVD — KS passes a twin with the right days at the wrong hours); categoricals and `packed_bool` by the χ² test. A failed test reports `error` and aborts nothing. Sections are `omitempty`: one whose capture flag was absent leaves the report byte-identical.

**Every section scores ONLY relationships generation ACTUALLY APPLIED.** `spec` carries the full captured cross product; `generate()` applies the subset conflict resolution leaves. Scoring a conflict-dropped pair computes a delta against a relationship the generator never modelled — a number that LOOKS like evidence. `internal/synth.ResolveConflicts` re-runs the identical arbitration against the identical `*Spec`, so a dropped pair has no entry and its own conflict warning explains the absence ([`synth-conflicts`](synth-conflicts.md)).

`pairwise` is one `{a, b, source_rho, synthetic_rho, delta, n}` per surviving numeric-numeric pair, via the same `pearson` helper capture uses. It scores VALUE-scale `Spec.Correlations` only; residual correlations have their own section.

**A modelled numeric has no numeric-target pair entry, ever.** `categorical_numeric_pairwise` / `set_numeric_pairwise` score the pick-one pair sampler, which does not run for a modelled target; an entry would be a delta for a mechanism that never executed. An unmodelled numeric in the same profile keeps its pair and is scored there. The non-numeric-target pair sections are untouched by models.

**Set fields have their own sections and are never omitted.** `set_fields` is one `{field, options: [{value, source_frequency, synthetic_frequency, delta, effect_size}]}` per `set_*` column, computed directly, one entry per dictionary member in BIT order up to `MaxSetEntries()`. `set_categorical_pairwise` / `set_numeric_pairwise` / `set_set_pairwise` score the surviving conditional pairs. **All four read masks the same way capture does** — a wide column read through a `uint64` assertion returns an empty mask on BOTH partitions, so every delta comes back exactly `0.0` and the section certifies a generation it never measured. A silently-omitted wide column reads the same way: as nothing wrong ([`synth-set-fields`](synth-set-fields.md)).

**Every marginal carries an effect size beside its p-value.** `effect_size {measure, value, magnitude, convention}`: Cohen's d (synthetic − source) for a KS field, Cramér's V (the χ² test's own) for a χ² one and per set option (its 2x2 table), banded on Cohen (1988) — `very small` / `small` / `medium` / `large`. At report sizes trivial gaps reject: **read the magnitude, not the p-value**. d reads location; the KS `statistic` is the largest CDF gap.

**A rule-induced difference says so.** Rules reach generated rows only, so a marginal flagged `small` or more on a field a rule writes (`set`, `set_expr`, `set_null`, a non-first `null_together` member) carries `rule_note` naming the rules: the rule may be the cause, not generation. A rule that never fired has its own warning.

## Utility against the source

Recovery sections compare the twin with the CAPTURED model; `utility` compares it with the SOURCE rows. A coefficient is a partial effect, a level table a marginal contrast carrying uncaptured confounders — `models` can call an effect recovered while churn-by-contract is compressed.

- `utility.target_rates` (automatic: applied model × categorical predictor): the target's mean per level on each side (a rate for `packed_bool`), `source_spread` / `synthetic_spread` (max − min, levels ≥ 30 rows on both), `spread_ratio`, ε² and `verdict` — `compressed` / `inflated` past 20% AND 2 SE, else `preserved`.
- `utility.model_fits` (opt-in `--fidelity-model <request.json>`, `SynthOptions.FidelityModel`): a `pulse process` request's regressions refit on source and generated rows — `statistic` (`r2`; `pseudo_r2` for `REG_GLM`), `source_fit`, `synthetic_fit`, `fit_ratio` and coefficients side by side. The source fit runs first; a refusal fails the call. No regressions or no `--fidelity-report`: `PULSE_SYNTH_FIDELITY_MODEL_INVALID`.

The two structure-recovery sections — `models` and `model_residual_correlations`, asking whether the captured CONDITIONING survived rather than whether the rows look alike — are in [`synth-models`](synth-models.md).

## Warnings in the report

The report's `warnings` carry capture, translation and generation warnings, deduplicated — including generation-only facts such as which captured relationship a `--rules` run retired and which rule never fired. The CLI prints a counted summary to stderr (attention kinds first, expected outcomes counted separately) and, with `--fidelity-report`, the recovery headline `N model(s) checked, M flagged`.

## See

- [`synthetic-data`](synthetic-data.md) · [`synth-conflicts`](synth-conflicts.md) · [`synth-models`](synth-models.md) · [`synth-set-fields`](synth-set-fields.md).
- `docs/src/cli/synth-from-profile.md` — flags and a worked report.
