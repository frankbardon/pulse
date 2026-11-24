# Synthetic data — conditional pair capture, per-numeric models, and structural rules

**How to load.** This file is an INDEX: the preamble below plus a slice map. Load it, then ONLY the slice(s) under `.claude/reference/synthetic-data/` your change touches; find one with `grep -l '<identifier>' .claude/reference/synthetic-data/*.md`. A section-qualified pointer at this file resolves through its slices.

The binding contract for every `synth/` surface: profile capture, `SpecFromProfile` translation, generation, structural rules and the fidelity report. Load it before changing any of them.

**Every failure recorded here was SILENT**: the run succeeded and a number was quietly wrong. Each rule in the slices carries the one-line reason it exists; do not relax a rule without reading that reason. Recurring lessons, stated once:

- **Derive, never transcribe.** A predicate, type set or flag that can be computed from the schema, the field spec or the code's own enum must be — hand-kept lists and wire flags drift silently.
- **Cover every writer.** A fix to how one stage writes a field type must reach every stage that can write it (own sampler, conditional pair, model, copula), or the others stay wrong with no signal.
- **Abandon, never truncate.** When a capture exceeds its cap it records nothing for that field and says so; a partial view presents an arbitrary subset as the structure.
- **An unrecognised case is ATTENTION, never a benign default.** A `default:` arm that guesses is how features have silently stopped working.
- **Byte determinism per `(spec, seed)` on every architecture.** Products feeding adds are `float64(a*b)`; transcendentals and variates go through `linalg/detmath`; RNG consumption depends only on the spec; new stages that need draws use their own counter-based stream.
- **Absent = byte-identical.** Every new section, slot and flag is additive `omitempty`; a document or spec that does not use it produces the bytes it did before.

`format_version` does NOT move for anything here: `synth.Profile` / `synth.Spec` / `synth.FidelityReport` live in `synth/` (aliases over `internal/synth/`), not `types/`, so they are unreachable from `internal/descriptor.BuildPayloadSchema` and `descriptor/testdata/payload-schema.json` is untouched. Only the manifest golden moves, and only when an error code is added.

**Skill homes.** The LLM-facing prose lives in budget-sized skills, all `requires: [capability:synth]`. Three ENTRY skills route to per-topic skills: `skills/synthetic-data.md` (modes, spec shape, distribution registry, determinism), `skills/synth-models.md` (`--fit-models` and everything model-related) and `skills/synth-structural-rules.md` (`rules[]` / `constraints[]`). Each entry skill's routing table names its per-topic stems; when a slice says "Detail: `skills/<stem>.md`", edit that stem, not the entry skill.

## Slice map

| Slice | Covers |
|---|---|
| `synthetic-data/capture-and-models.md` | conditional pair capture, the thin-pair warning, the `models` section, predictor selection, thin-level shrinkage, per-target retirement of numeric pairs, the composed model draw, shape-fitted conditioning, float fusion |
| `synthetic-data/marginals-and-correlation.md` | boolean (`bernoulli`, probit capture) and small-integer (`discrete`) marginals, correlation reconstruction and unmeasured entries, residual correlation capture |
| `synthetic-data/residuals-and-fidelity.md` | the correlated residual draw, model and residual-correlation recovery, the terminal warning summary, utility against the source, effect sizes and per-type marginal scoring |
| `synthetic-data/structural-rules.md` | `Spec.Rules` and eager validation, the rule pass running last, `owns_nulls`, priority-0 claims, `set_expr` and the coercion matrix, the row environment's functions |
| `synthetic-data/rules-reporting-and-suggest.md` | `null_together`, never-firing rules, `constant` normalisation, `--emit-spec` / `--rules`, the `--suggest-rules` gating detector |
| `synthetic-data/rule-detection.md` | the co-missing and dependency detectors, emitted order, calibration |
| `synthetic-data/sets-order-and-vectors.md` | wide set capture, run-continuation and shard archives, vector fields |
| `synthetic-data/cross-row-time-and-draws.md` | `row()` / `lag()`, local-time datetimes and time effects, the capture clock, rule draws and helper fields, pair arbitration and the noise floor, synthetic-only twins |
| `synthetic-data/disclosure.md` | the fidelity report's `disclosure` section |
| `synthetic-data/rule-lookup.md` | `lookup()` in rule expressions and the spec's identity |
