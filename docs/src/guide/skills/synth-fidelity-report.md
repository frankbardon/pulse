```yaml
name: synth-fidelity-report
description: The `pulse synth from-profile` tagged top-up contract (`_synthetic` column, new output path, `--rows` counts new rows) and the `--fidelity-report` sections — marginals, pairs, set fields — scored only over relationships generation actually applied.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, fidelity report, _synthetic, top-up]
requires: [capability:synth]
```

# Synth top-up and fidelity report

## Tagged top-up contract

`synth from-profile` (`SynthOptions.SourceCohort` / `--source`) always: (1) appends one `_synthetic` `packed_bool` field, `false` on copied rows, `true` on generated ones; (2) writes a **new** output path, distinct from `--source`, which is opened read-only and never mutated; (3) treats `--rows` as a count of NEW rows, never "top up to N total" (`--rows 500` against 200 source rows → 700 rows). Source records are re-encoded straight through, so structural rules reach GENERATED rows only. `SourceCohort` empty (`synth from-schema`) is the plain path — no tag column. Refusals: `PULSE_SYNTH_SOURCE_REQUIRED`, `PULSE_SYNTH_OUTPUT_REQUIRED`, `PULSE_SYNTH_OUTPUT_COLLISION`, `PULSE_SYNTH_ALREADY_TAGGED`, `PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`.

## Fidelity report

`--fidelity-report <path.json>` (only with `SourceCohort` set) writes a `synth.FidelityReport` after generation. Marginals: numeric via the KS test operator (`split_by: _synthetic`), categorical via the χ² test operator — existing operators, no new stat math. `_synthetic` is on-wire `packed_bool` and both operators need categorical, so the bridge presents it through a `categorical_u8` VIEW schema for that call only. That bridge lives in `pulse.go` (`writeSynthFidelityReport`), not `internal/synth/`, to avoid an import cycle: the internal `BuildFidelityReport` takes an injected `TestRunner`. A failed field test reports `error`, not `result`, without aborting the rest. Sections `omitempty` throughout, so a section whose capture flag was absent leaves the report byte-identical to before it existed.

**Every section scores ONLY relationships generation ACTUALLY APPLIED.** `spec` carries the full captured cross product; `generate()` applies the subset conflict resolution leaves. Scoring a conflict-dropped pair computes a delta against a relationship the generator never modelled — a number that LOOKS like evidence. `internal/synth.ResolveConflicts` re-runs the identical arbitration against the identical `*Spec`, so a dropped pair has no entry and its own conflict warning explains the absence ([`synth-conflicts`](synth-conflicts.md)).

`pairwise` is one `{a, b, source_rho, synthetic_rho, delta, n}` per surviving numeric-numeric pair, via the same `pearson` helper capture uses. It scores VALUE-scale `Spec.Correlations` only; residual correlations have their own section.

**A modelled numeric has no numeric-target pair entry, ever.** `categorical_numeric_pairwise` / `set_numeric_pairwise` score the pick-one pair sampler, which does not run for a modelled target; an entry would be a delta for a mechanism that never executed. An unmodelled numeric in the same profile keeps its pair and is scored there. The non-numeric-target pair sections are untouched by models.

**Set fields have their own sections and are never omitted.** `set_fields` is one `{field, options: [{value, source_frequency, synthetic_frequency, delta}]}` per `set_*` column, computed directly (no `TestRunner`: the KS test wants a scalar and the χ² test a partition, and a multi-select is neither), one entry per dictionary member in BIT order up to `MaxSetEntries()`. `set_categorical_pairwise` / `set_numeric_pairwise` / `set_set_pairwise` score the surviving conditional pairs. **All four read masks the same way capture does** — a wide column read through a `uint64` assertion returns an empty mask on BOTH partitions, so every delta comes back exactly `0.0` and the section certifies a generation it never measured. A silently-omitted wide column reads the same way: as nothing wrong ([`synth-set-fields`](synth-set-fields.md)).

The two structure-recovery sections — `models` and `model_residual_correlations`, asking whether the captured CONDITIONING survived rather than whether the rows look alike — are in [`synth-models`](synth-models.md).

## Warnings in the report

The report's `warnings` carry capture, translation and generation warnings, deduplicated — including generation-only facts such as which captured relationship a `--rules` run retired and which rule never fired. The CLI prints a counted summary to stderr (attention kinds first, expected outcomes counted separately) and, with `--fidelity-report`, the recovery headline `N model(s) checked, M flagged`.

## See

- [`synthetic-data`](synthetic-data.md) · [`synth-conflicts`](synth-conflicts.md) · [`synth-models`](synth-models.md) · [`synth-set-fields`](synth-set-fields.md).
- `docs/src/cli/synth-from-profile.md` — flags and a worked report.
