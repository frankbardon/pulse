---
name: synthetic-data
description: Entry point for synthetic data — `pulse synth from-schema` vs `pulse synth from-profile` (CLI + library; no MCP tool), the spec shape, the distribution registry, the determinism rule, and which focused synth skill answers each question.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-schema, synth from-profile, profile create, synth, distributions, correlations, models, determinism]
requires: [capability:synth]
---

# Synthetic data

Pulse synthesizes deterministic `.pulse` cohorts via the CLI leaves `pulse synth from-schema` / `pulse synth from-profile` (library `Pulse.Synth`; no MCP tool).

This is the CONTRACT surface — rules not inferable from the code you are editing, whose violation is SILENT. The measurements behind each rule and every closed design question: `docs/src/cli/synth-calibration.md`.

Synth does not emit `Response.Components` — it writes a `.pulse` file.

## Which skill answers what

| Question | Skill |
|---|---|
| What does `profile create` capture; what does each field rebuild as? | `synth-profile-capture` |
| Why `packed_bool` → `bernoulli`, small integer → `discrete`? | `synth-marginals` |
| `set_*` (multi-select) profiling, pairs, generation | `synth-set-fields` |
| `correlations`: which marginals / types may join | `synth-correlations` |
| Two relationships name one field — which wins? | `synth-conflicts` |
| Several drivers conditioning one numeric (`--fit-models`) | `synth-models` |
| Gating, masking, deriving a field (`rules[]`, `constraints[]`) | `synth-structural-rules` |
| The `_synthetic` top-up and `--fidelity-report` | `synth-fidelity-report` |
| Byte-identical output; why a profile can drift | `synth-determinism` |
| One distribution's params | atomic `op-synth-<kind>` |

## Two modes

| Mode | Input | When |
|---|---|---|
| `pulse synth from-schema` | hand-written JSON spec | caller knows desired shape — fixtures, CI seeds, demos |
| `pulse synth from-profile` | profile JSON + the source cohort it was captured from | tagged top-up: add rows matching a real cohort's marginals, source untouched |

**Privacy.** Synth does NOT preserve privacy. A profile without DP noise leaks the empirical distribution — top-K categoricals reveal rare values, percentiles reveal ranges, coefficients expose structure. Add a calibrated noise mechanism if the source is sensitive.

## Schema-mode spec

```json
{
  "row_count": 100000,
  "fields": [
    {"name": "user_id", "type": "u64", "distribution": "monotonic_from", "params": {"start": 1}},
    {"name": "age", "type": "u8", "distribution": "normal", "params": {"mean": 35, "std": 12}},
    {"name": "country", "type": "categorical_u8", "distribution": "weighted_categorical", "params": {"values": ["US","UK"], "weights": [0.6,0.4]}},
    {"name": "amount", "type": "f64", "distribution": "lognormal", "params": {"mu": 4.2, "sigma": 0.8}}
  ],
  "constraints": [{"expr": "amount >= 0"}],
  "max_rejection_rate": 0.5
}
```

Verify with `pulse_inspect`. `constraints[]` (reject-and-redraw) and `rules[]` (gate, mask, derive): `synth-structural-rules` — read it before a spec gates, masks or derives a field rather than merely distributing one.

### Distribution registry

Registry: the manifest's `synth_distributions`; params and clamp semantics per kind in `op-synth-<kind>`.

`uniform` `[min,max)` · `normal` (optional `min`/`max` clamp) · `lognormal` · `exponential` · `poisson` · `pareto` · `bernoulli` · `monotonic_from` (ignores RNG — primary keys) · `weighted_categorical` (uniform when `weights` absent) · `uniform_date` (inclusive both ends) · `regex` (no backreferences) · `mixture` (≥2 components; bimodal or skewed shapes a single `normal` collapses).

Three carry rules you cannot guess:

- `discrete` — `values` strictly ASCENDING + optional `weights`. The exact per-level histogram of an integer column; what `profile create` reconstructs every capped integer field from (`synth-marginals`).
- `constant` — coerced ONCE at spec parse to the field's ROW shape: bool → 1/0 on a scalar, option-name array or object → a `set_*` mask, string REQUIRED for a categorical, verbatim exact string for `decimal128` (`ParseDecimal128`). The only sampler valued from the document, hence the only one that could put a Go `bool` where the expr environment promises `float64`; a shape the row cannot hold is refused at parse.
- `set_bernoulli` — `options` also PRE-REGISTERS the field's dictionary at schema-build time. A determinism requirement, not an optimisation (`synth-set-fields`).

19 of the 20 field types are reachable — **`datetime` is NOT**: `fieldTypeFromName` (`internal/synth/writer.go`) has no case for it, so `"type": "datetime"` refuses with `unknown field type`. Use `date` (epoch days) or `u64` epoch seconds. `decimal128` needs `params.scale` matching the declared scale (banker's rounding). Bit-packed (`u4`, `packed_bool`) use one byte per row in the writer. `nullable: true` opts into the null bitmap; nulls NEVER ride an inline sentinel.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file; same `(--input, --seed)` MUST produce a byte-identical profile document. Any change breaking either is a contract break. Seed splitting, `Seed == 0`, null-draw order, map-order folds and float fusion: `synth-determinism`.

## Library embedding

`pulse.Pulse.Synth` / `pulse.Pulse.Profile` route through the embedded filesystem — `pulse.New(pulse.Options{FS: afero.NewMemMapFs()})` for hermetic tests.

## Gotchas

- Constraints + `monotonic_from`: monotonic ignores RNG, so a rejected row still increments the counter.
- **A model coefficient is latent-scale** (data units only when `Q` is `normal`); a quantized modelled target attenuates at write time — `synth-models`.
- `--fit-models` does not imply `--residual-correlations`, and neither implies `--conditional`. `--fit-shape` composes with all of them.
- `weighted_categorical` weights normalize at sample time; absent weights default to uniform.

## See

- The focused skills in the table above, plus atomic `op-synth-<kind>`.
- `cohort-schema-design` — field types, dictionaries, null bitmap.
- `regression-modeling` — the OLS engine both model capture and the recovery refit drive.
- `pulse_errors_lookup` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
