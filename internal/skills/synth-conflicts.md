---
name: synth-conflicts
description: Synth conditional relationship conflicts — the shared claim space, the fixed priority order `resolveConflicts` applies once per spec, how rule / model / shape pre-claims compose, and where the dropped-relationship warnings surface.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-schema, synth from-profile, conflicts, resolveConflicts]
requires: [capability:synth]
---

# Synth relationship conflicts

## Conditional relationship conflicts

Every relationship writes into one shared claim space: a field name for scalar targets (categorical-pair / categorical-numeric B side, set-numeric's numeric side, correlation participants), or `(field, option)` for a `set_*` bit. **Two options on the SAME set field are two DIFFERENT targets, never a conflict.** A whole-field claim subsumes that field's options; an option claim never reaches the whole field.

`resolveConflicts` (`internal/synth/conflict.go`) runs ONCE per `Spec` at generation setup, never per row, in the fixed `drawRow` priority order:

```
structural-rule pre-claim → linear-model pre-claim → shape-fit pre-claim
  → catPairs → catNumPairs → setSetPairs → setCatPairs → setNumPairs
  → correlations
```

A `Spec.Rules` entry that DETERMINES a field claims it first, because the rule pass runs last in `drawRow` and wins outright; which rule shapes qualify (and the four exclusions, each silent if got backwards) is in `synth-rule-claims`.

First claim wins; every later relationship naming that target is dropped and REPORTED, one warning each (`conditional relationship conflict: … is already claimed by …`), rather than resolving to "whichever stage runs last". `Spec.Correlations` is one joint claimant across all participants (single Cholesky draw) — losing one participant excludes only that field and `buildCorrelator` rebuilds from the survivors.

**The model and shape pre-claims compose; they do not compete.** The model claims first (one additive expression already accounts for every predictor, and a later overwrite would discard the whole account rather than layer onto it); a `--fit-shape` field claims second (its sampler already drew the value). A shape-fitted field carrying a model is claimed by the model, draws through the model, and gets its fitted mixture as `Q` — nothing dropped, no warning. The shape pre-claim still owns every `DistMixture` field no model took, and excludes a pair or correlation naming it with a `captured shape (--fit-shape)` warning.

A profile-derived spec never declares both a model and a numeric-target pair for one field (`SpecFromProfile` retires the pair per target); a HAND-AUTHORED spec that does is arbitrated here, the model winning. A value-scale correlation naming a modelled field is reported as not applied (capture residual correlations instead), not as a conflict — no pair claimed it.

## Where the warnings surface

`generate()` returns them on `Result.Warnings`; for `--fidelity-report`, `SpecFromProfile` reruns the pass at spec-composition time and merges with capture-time warnings into `FidelityWarnings`. Both always agree — same Spec, same order. The terminal summary counts an arbitrated claim as an EXPECTED outcome, not an attention item.

The arbitration is also why the fidelity report scores only relationships generation actually applied (`synth-fidelity-report`).

## See

- `synth-correlations` · `synth-models` · `synth-set-fields` · `synth-rule-claims`.
