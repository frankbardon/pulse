```yaml
name: synthetic-data
description: Entry point for synthetic data — `pulse synth from-schema` vs `pulse synth from-profile` (CLI + library; no MCP tool), the spec shape, the distribution registry, the determinism rule, and which focused synth skill answers each question.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-schema, synth from-profile, profile create, synth, distributions, correlations, models, determinism]
requires: [capability:synth]
```

# Synthetic data

Pulse synthesizes deterministic `.pulse` cohorts via the CLI leaves `pulse synth from-schema` / `pulse synth from-profile` (library `Pulse.Synth`; no MCP tool).

This is the CONTRACT surface — rules not inferable from the code you are editing, whose violation is SILENT. Measurements behind each rule and every closed design question: `docs/src/cli/synth-calibration.md`.

Synth writes a `.pulse` file, never `Response.Components`.

## Which skill answers what

| Question | Skill |
|---|---|
| What does `profile create` capture; what does each field rebuild as? | [`synth-profile-capture`](synth-profile-capture.md) |
| Why `packed_bool` → `bernoulli`, small integer → `discrete`? | [`synth-marginals`](synth-marginals.md) |
| `set_*` (multi-select) profiling, pairs, generation | [`synth-set-fields`](synth-set-fields.md) |
| `correlations`: which marginals / types may join | [`synth-correlations`](synth-correlations.md) |
| Two relationships name one field — which wins? | [`synth-conflicts`](synth-conflicts.md) |
| Several drivers conditioning one numeric (`--fit-models`) | [`synth-models`](synth-models.md) |
| Gating, masking, deriving a field, reading earlier rows (`rules[]`, `constraints[]`, `lag`) | [`synth-structural-rules`](synth-structural-rules.md) |
| Drawing in a rule (`uniform()`, `bernoulli(p)`); helper fields (`"output": false`) | [`synth-rule-draws`](synth-rule-draws.md) |
| The `_synthetic` top-up and `--fidelity-report` | [`synth-fidelity-report`](synth-fidelity-report.md) |
| How close a twin is to real rows (disclosure) | [`synth-disclosure-report`](synth-disclosure-report.md) |
| Byte-identical output; why a profile can drift | [`synth-determinism`](synth-determinism.md) |
| `--fit-shape` mixtures | [`synth-shape-fit`](synth-shape-fit.md) |
| `datetime`: local hours / weekdays, per-row zones, DST, trends / seasons / windows | [`synth-datetime`](synth-datetime.md) |
| One distribution's params | atomic `op-synth-<kind>` |

## Two modes

| Mode | Input | When |
|---|---|---|
| `pulse synth from-schema` | hand-written JSON spec | caller knows the shape — fixtures, CI seeds, demos |
| `pulse synth from-profile` | profile JSON + the source cohort it was captured from | tagged top-up: add rows matching a real cohort's marginals, source untouched |

**Privacy.** A profile-derived twin is NOT differentially private and carries NO formal privacy guarantee: no noise is added, so a row can repeat a real one and the profile leaks rare categorical values, percentile ranges and coefficient structure. The `disclosure` section (exact-match rate, DCR) is an audit aid, not a guarantee.

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

**Strict:** spec, rules file and profile refuse an unknown key or a `params` key the distribution does not read (`rate` → `lambda`): `PULSE_REQUEST_UNKNOWN_FIELD`, `details.path`, a suggestion.

`constraints[]` (reject-and-redraw) and `rules[]` (gate, mask, derive): read [`synth-structural-rules`](synth-structural-rules.md) before a spec gates, masks or derives a field.

### Distribution registry

Registry: the manifest's `synth_distributions`; params and clamp semantics per kind in `op-synth-<kind>`.

`uniform` `[min,max)` · `normal` (optional `min`/`max` clamp) · `lognormal` · `exponential` · `poisson` · `pareto` · `bernoulli` · `monotonic_from` (ignores RNG — primary keys) · `weighted_categorical` (uniform when `weights` absent) · `uniform_date` (inclusive both ends) · `uniform_datetime` (zoned local time) · `regex` (no backreferences) · `mixture` (≥2 components; bimodal or skewed shapes a single `normal` collapses).

Three carry rules you cannot guess:

- `discrete` — `values` strictly ASCENDING + optional `weights`. The exact per-level histogram of an integer column; what `profile create` reconstructs every capped integer field from ([`synth-marginals`](synth-marginals.md)).
- `constant` — coerced ONCE at spec parse to the field's ROW shape: bool → 1/0 on a scalar, option-name array or object → a `set_*` mask, string REQUIRED for a categorical, verbatim exact string for `decimal128` (`ParseDecimal128`). A shape the row cannot hold is refused at parse.
- `set_bernoulli` — `options` also PRE-REGISTERS the field's dictionary at schema-build time. A determinism requirement, not an optimisation ([`synth-set-fields`](synth-set-fields.md)).

Every field type is reachable. A **vector** (`vec_f32` / `vec_f64`) needs a `vector` block and is ONE joint draw, never dim scalars: [`vector-fields`](vector-fields.md). `decimal128` takes field-level `precision` / `scale` (banker's rounding). Bit-packed (`u4`, `packed_bool`) use one byte per row in the writer. `nullable: true` opts into the null bitmap; nulls NEVER ride an inline sentinel.

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file; same `(--input, --seed)` MUST produce a byte-identical profile document. Seed splitting, `Seed == 0`, null-draw order, map-order folds and float fusion: [`synth-determinism`](synth-determinism.md).

## Gotchas

- `Pulse.Synth` / `Pulse.Profile` use the instance filesystem (`afero.NewMemMapFs()` in hermetic tests).
- `monotonic_from` counts ACCEPTED rows: no gaps under constraints.
- **A model coefficient is latent-scale** (data units only when `Q` is `normal`); a quantized modelled target attenuates at write time — [`synth-models`](synth-models.md).
- `--fit-models` does not imply `--residual-correlations`, and neither implies `--conditional`. `--fit-shape` composes with all of them.

## See

- The focused skills in the table above, plus atomic `op-synth-<kind>`.
- [`cohort-schema-design`](cohort-schema-design.md) — field types, dictionaries, null bitmap.
- [`regression-modeling`](regression-modeling.md) — the OLS engine both model capture and the recovery refit drive.
- `pulse_errors_lookup` — `PULSE_SYNTH_*` / `PULSE_PROFILE_*` recovery.
