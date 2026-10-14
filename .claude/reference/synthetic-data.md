# Synthetic data — conditional pair capture, per-numeric models, and structural rules

**How to load.** This file is an INDEX: the preamble below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/synthetic-data/` your change touches; find one with `grep -l '<identifier>' .claude/reference/synthetic-data/*.md`. A section-qualified pointer at this file resolves through the section map.

Relocated verbatim from CLAUDE.md (the former section `## Synthetic data: conditional pair capture and per-numeric models`; CLAUDE.md now keeps a short `## Synthetic data` pointer here). This is the binding contract for every `synth/` surface: profile capture, `SpecFromProfile` translation, generation, structural rules and the fidelity report. Load it before changing any of them — CLAUDE.md's Update Demand table names this file as a required companion.

`format_version` does NOT move for anything described here: `synth.Profile` / `synth.Spec` / `synth.FidelityReport` live in `synth/` (aliases over `internal/synth/`), not `types/`, so they are unreachable from `internal/descriptor.BuildPayloadSchema` and `descriptor/testdata/payload-schema.json` is untouched. Only the manifest golden moves, and only when an error code is added.

**Skill homes (U10 split).** The LLM-facing prose is split into budget-sized skills, all `requires: [capability:synth]`. `skills/synthetic-data.md` is the ENTRY skill (modes, spec shape, distribution registry, the determinism rule, a question → skill table) and routes to: `synth-profile-capture` (capture sections, `SpecFromProfile` reconstruction, categorical joint generation), `synth-marginals` (boolean + small-integer arms), `synth-set-fields` (set profiling, wide-rung mask reads, set generation), `synth-correlations` (the value-scale copula), `synth-conflicts` (claim order), `synth-fidelity-report` (tagged top-up + report sections) and `synth-determinism` (seed, capture map-order and float-fusion rules). `skills/synth-models.md` is likewise the ENTRY skill for `--fit-models` (capture + wire shape, the latent-scale rule, what models retire) and routes to `synth-model-selection` (top-K collapse, variance floor, thin-level shrinkage), `synth-model-draw` (composed draw, catch-all firing term, latent-scale effects), `synth-shape-fit` (`--fit-shape` capture + shape-fitted targets accepting conditioning), `synth-residual-correlations` (capture + correlated draw), `synth-model-recovery` (`models` section) and `synth-residual-recovery` (`model_residual_correlations` section). `skills/synth-structural-rules.md` is the ENTRY skill for `rules[]` / `constraints[]` (the five slots, declaration order, why the pass runs LAST) and routes to `synth-rule-expressions` (`constraints[]`, the row env, the coercion matrix, pre-rounding), `synth-rule-nulls` (`null_together`, `owns_nulls`), `synth-rule-validation` (eager codes, non-nullable refusal, never-fired), `synth-rule-claims` (the priority-0 pre-claim), `synth-rules-from-profile` (`--rules` / `--emit-spec` + the `--suggest-rules` detector table) and `synth-rule-detectors` (per-detector rules, emitted order). When a paragraph below says "Detail: `skills/<stem>.md`", edit the stem it names, not the entry skill.

## Contents

Paragraph order below is the file's order; each entry is that paragraph's bold lead-in.

1. Conditional pair capture (`pulse profile create --conditional`)
2. Thin-pair warning (generic mechanism)
3. Per-numeric linear models (`models` profile section)
4. Predictor selection is automatic, absolute, and has no user-facing override
5. Thin LEVELS inside an admitted predictor are SHRUNK, never dropped and never refused
6. `models` retires the numeric-target conditional arms, PER TARGET
7. Generation: the composed model draw
8. Shape-fitted numerics accept conditioning
9. Float fusion: capture and generation are architecture-independent by CONSTRUCTION, not by luck
10. Boolean marginals: a `packed_bool` reconstructs as `bernoulli`, never as a clamped normal
11. Small-integer marginals: a `u4`/`u8`/`u16`/`u32`/`u64` reconstructs from its own per-level HISTOGRAM (`discrete`), never as a clamped normal
12. Correlation reconstruction
13. Residual correlation capture (`--residual-correlations`)
14. The correlated residual draw (`Spec.ResidualCorrelations`)
15. Model recovery in the fidelity report (`FidelityReport.Models`)
16. Residual-correlation recovery + the numeric-arm disjointness (`FidelityReport.ModelResidualCorrelations`)
17. The terminal warning summary (`internal/synth.GroupWarnings`)
18. Structural rules (`Spec.Rules`, additive + `omitempty`)
19. The rule pass runs LAST, and that placement IS the semantics
20. `owns_nulls`: a rule can be the field's ONLY source of absence, and it ZEROES the own draw rather than applying the residual
21. A rule that DETERMINES a field pre-claims it at priority 0
22. `set_expr` and THE COERCION MATRIX
23. `null_together` NULLS A BLOCK AS ONE DECISION, and the FIRST named field wins
24. A rule that NEVER FIRES is reported, as an ATTENTION kind
25. `constant` is normalised to the field's row shape
26. The profile-derived spec is inspectable and modifiable (`--emit-spec` / `--rules`)
27. Detecting structural rules from the data (`profile create --suggest-rules`)
28. Co-missing blocks and always-null columns (the second `--suggest-rules` detector)
29. Exact dependencies (the third `--suggest-rules` detector)
30. Detection is calibrated against the motivating cohort, and the candidate file closes the loop
31. Wide set columns are captured member-for-member, and the mask read is the whole risk
32. Run-continuation is a row-ORDER measurement that rides the scan, and shard archives profile as one stream
33. Vector fields are ONE joint draw, never dim independent scalars

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `synthetic-data/capture-and-models.md` | The contract; Thin-pair warning (generic mechanism); Per-numeric linear models (`models` profile section); Predictor selection is automatic, absolute, and has no user-facing override; Thin LEVELS inside an admitted predictor are SHRUNK, never dropped and never refused; `models` retires the numeric-target conditional arms, PER TARGET; Generation: the composed model draw; Shape-fitted numerics accept conditioning; Float fusion: capture and generation are architecture-independent by CONSTRUCTION, not… |
| `synthetic-data/marginals-and-correlation.md` | Boolean marginals: a `packed_bool` reconstructs as `bernoulli`, never as a clamped normal; Small-integer marginals: a `u4`/`u8`/`u16`/`u32`/`u64` reconstructs from its own per-le…; Correlation reconstruction; Residual correlation capture (`--residual-correlations`) |
| `synthetic-data/residuals-and-fidelity.md` | The correlated residual draw (`Spec.ResidualCorrelations`); Model recovery in the fidelity report (`FidelityReport.Models`); Residual-correlation recovery + the numeric-arm disjointness (`FidelityReport.ModelResi…; The terminal warning summary (`internal/synth.GroupWarnings`) |
| `synthetic-data/structural-rules.md` | Structural rules (`Spec.Rules`, additive + `omitempty`); The rule pass runs LAST, and that placement IS the semantics; `owns_nulls`: a rule can be the field's ONLY source of absence, and it ZEROES the own d…; A rule that DETERMINES a field pre-claims it at priority 0; `set_expr` and THE COERCION MATRIX |
| `synthetic-data/rules-reporting-and-suggest.md` | `null_together` NULLS A BLOCK AS ONE DECISION, and the FIRST named field wins; A rule that NEVER FIRES is reported, as an ATTENTION kind; `constant` is normalised to the field's row shape; The profile-derived spec is inspectable and modifiable (`--emit-spec` / `--rules`); Detecting structural rules from the data (`profile create --suggest-rules`) |
| `synthetic-data/rule-detection.md` | Co-missing blocks and always-null columns (the second `--suggest-rules` detector); Exact dependencies (the third `--suggest-rules` detector); Detection is calibrated against the motivating cohort, and the candidate file closes th… |
| `synthetic-data/sets-order-and-vectors.md` | Wide set columns are captured member-for-member, and the mask read is the whole risk; Run-continuation is a row-ORDER measurement that rides the scan, and shard archives pro…; Vector fields are ONE joint draw, never dim independent scalars |
