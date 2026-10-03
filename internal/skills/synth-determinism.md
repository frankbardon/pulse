---
name: synth-determinism
description: The synth determinism contract — byte-identical `.pulse` output per spec and seed, byte-identical profile documents per input and seed, and the two non-RNG threats to capture (map iteration order, float fusion).
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-schema, synth from-profile, profile create, determinism, seed]
requires: [capability:synth]
---

# Synth determinism

## Determinism contract

Same `(spec, opts.Seed)` MUST produce a byte-identical `.pulse` file. Any sampler change breaking that is a contract break.

Seed splitting uses a 64-bit avalanche; seeds differing by 1 give uncorrelated streams. `Seed == 0` is stable, not "random". `nullableSampler` always draws the inner value FIRST, then the null mask — the stream is invariant to which rows are null.

Every per-row stage consumes a FIXED number of draws whatever it decides: each model drawer takes exactly one normal per row unconditionally (even at zero residual std), in SCHEMA field order, never in `models` array order; the rule pass consumes NO RNG; the mixture quantile is a fixed-count bisection, never a tolerance-terminated search. A spec declaring no models, residual correlations or rules is byte-identical to pre-slot output. The stream legitimately moves only for a reason a warning names (a rule that pre-claims a field retires its model, and that model's draw).

**Capture is held to the same bar**: same `(--input, --seed)` MUST produce a byte-identical profile document, or the pipeline is only deterministic downstream of a spec that itself drifts. Two threats, neither the RNG:

1. **Map iteration order.** Anywhere capture folds several accumulators into one shared bucket — canonically the top-K collapse folding out-of-top-K categories into `"other"` — the fold MUST walk SORTED keys: float addition is not associative and Go randomizes map iteration. The `(sumSq - mean*sum)/(n-1)` variance form amplifies rather than absorbs the last-bit difference, so a map-order fold surfaces as a ~1e-10 relative drift in the emitted `std`. Signature: only `"other"` entries move, because only `"other"` has more than one source. Sort — do NOT switch to compensated summation, which is still order-dependent in principle.
2. **Float fusion.** Go may contract `a + b*c` into one FMA; arm64 does, amd64 does not, and the contraction is permitted ACROSS statements. Every product in a capture or generation formula therefore carries an explicit `float64(...)` conversion — the only construct that forbids contraction — and `internal/synth/moments_internal_test.go` fails if one is dropped (it can only DETECT on a contracting architecture, so it is silent on CI by construction). `math.FMA` is the wrong lever: it forces fusion everywhere. **Test fixtures computing float columns are part of the contract** — accumulate in integer units and divide once. Residual, stated not hidden: `--fit-shape` (`math.Exp`/`math.Log`) and `--fit-models` (the regression engine, not yet fusion-free) can still differ in last bits across architectures.

Ordering anywhere in capture or generation is spec-derived or sorted, never map-derived — a document's array order is a serialization artifact and must not choose a draw.

## See

- `synthetic-data` · `synth-models` · `synth-set-fields` (dictionary pre-registration).
- `docs/src/cli/profile-create.md` — reproducibility.
