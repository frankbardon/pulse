---
name: synth-residual-correlations
description: Synth `--residual-correlations` — capturing the correlation among fitted model residuals (full submatrix, measured vs unmeasured), the correlated residual draw that lets a numeric be both conditioned and correlated, and its zero-predictor boundary.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [profile create, synth from-profile, residual_correlations, models]
requires: [capability:synth]
---

# Synth residual correlations

## Capture (`--residual-correlations`)

Requires `--fit-models`. The correlation structure among fitted model RESIDUALS. Once a numeric's systematic variation is explained by its predictors, what is free to move at generation time is the residual — and a raw correlation double-counts every predictor two targets share (two fields both driven by `region` correlate on raw values while their residuals may be independent). Computed off `FieldModel.Residuals` in memory after the fits: **no extra cohort read**.

Shape `{fields, pairs, unmeasured}` — sorted participant set, `{a,b,rho,n}` per MEASURED pair, `{a,b,n,reason}` per unmeasurable pair; exhaustive, non-overlapping. **Full submatrix, never top-K**: the consumer is a joint draw over every participant at once and a matrix is not a ranked list — an omitted pair is a hole the factorization must fill, not a weak pair. `--include-correlations` / `--correlation-top-k` keep their meaning (top-K pairs of RAW values); this shares neither flag and is NOT implied by `--fit-models`, being quadratic in modelled fields.

**Measured-zero and unmeasured are structurally different, never conventionally different.** A dense N×N array has one slot per pair, so "unmeasured" would have nowhere to live but a sentinel — and 0 is a perfectly ordinary measurement. Two lists keep the distinction across a JSON round trip, and an unmeasured entry carries **no `rho` key at all**. Two closed reasons: `insufficient_overlap` (fewer than `minResidualPairObservations` = 3 rows carry both residuals; two points always give ±1) and `no_variance` (overlapped, one side's residual constant — 0/0 is not 0). That floor is NOT `MinPairObservations` (30): 30 answers "stable enough to trust" and its answer is a WARNING; this answers "is there a measurement at all" and its answer is a GAP. They compose. Gaps get one summary warning; thin measured pairs warn thinnest-first, capped at 20. Capture is byte-reproducible (sorted participants, no map-order float folds).

**Participants = every model carrying a residual vector, INCLUDING zero-predictor models** — such a target still has a residual (its whole deviation from its own mean), and excluding it drops real structure for an unrelated reason. A model with a NIL residual vector (a `Profile` decoded from a document) is not a participant.

### The correlated residual draw

`SpecFromProfile` translates the MEASURED pairs onto `Spec.ResidualCorrelations`; the `unmeasured` list becomes NOTHING (an absent pair is completed-and-counted; a written zero would present a gap as a measurement). A modelled field named there reads its own component of ONE correlated standard-normal vector drawn per row (`internal/synth/residual_draw.go`) as the `z` of `Q(Φ(μ + σ·z))`; every other drawer takes its own fresh normal. That is the whole of how a field is **both conditioned and correlated** — predictors move `μ`, the shared vector correlates the residual, neither overwrites the other. Cholesky, assume-and-record policy and ridge report are the SAME machinery the value-scale matrix uses (`factorCorrelations`): one construction, two consumers, so a policy cannot land on one scale and miss the other.

Component order is **drawer order (= schema field order)**, never `residual_correlations` array order, or the cohort would depend on how the request was serialized. The vector consumes exactly one normal per participant, in component order, BEFORE the first drawer runs; a non-participant still consumes its own draw where it always did. Fewer than two surviving participants ⇒ no correlator, no draw, so a spec declaring none stays byte-identical. Participation is decided from the COMPILED drawers: a pair naming a field whose model was dropped is dropped with one aggregated warning. `validateSpec` refuses a hand-authored residual correlation naming an unmodelled field.

For a `normal` target the latent→value map is affine ⇒ realized value-scale residual correlation is the captured figure exactly. Under `lognormal` / `mixture` / `discrete` / `bernoulli` the RANK correlation is exact and Pearson attenuates.

**A value-scale `correlations` entry naming a modelled field is refused, permanently.** Realizing it would overwrite the model's output; rerouting it into the residual would apply every shared predictor a second time. `resolveConflicts` words it *is not applied … capture residual correlations (`profile create --residual-correlations`) to correlate a modelled field* — deliberately NOT a `conditional relationship conflict`, because no pair claimed the field.

**The zero-predictor target is the feature's real boundary.** Selection emits a zero-predictor model for a target nothing explained; capture admits it as a participant; `modelSpecFromProfile` DROPS it at translation, because a field nothing explains is better served by its captured conditional pair than by an empty model — so its captured residual pairs reach the spec as nothing. Reversing the drop would retire those fields' pairs in exchange, a worse trade, so the boundary is pinned by test rather than closed.

## See

- `synth-models` · `synth-model-draw` · `synth-correlations`.
- `synth-residual-recovery` — whether the correlations survived generation.
