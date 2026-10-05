# 02 — Multiple-comparison correction

## One core, many consumers

A single package (`processing/multiplicity`, pure functions over `[]float64` p-values) implements:

| Method | Controls | Typical use |
|---|---|---|
| `bonferroni` | family-wise error | few, important comparisons |
| `holm` | family-wise error, uniformly more powerful than Bonferroni | **recommended default** for confirmatory work |
| `bh` (Benjamini–Hochberg) | false discovery rate | exploratory scans: crosstab significance letters, correlation matrices |
| `by` (Benjamini–Yekutieli) | FDR under arbitrary dependence | conservative FDR |
| `none` | — | explicit "I know" |

The results are deterministic: ties are ordered by original position, and adjusted p-values are capped at 1.

## Defining the family

Correction is only meaningful once you say *which tests count together*. That choice is explicit, and has a sensible default per surface:

```jsonc
{ "multiplicity": { "method": "holm", "family": "layer" } }
```

| `family` | The tests corrected together | Default for |
|---|---|---|
| `layer` | all p-values produced by one overlay layer (e.g. every pairwise cell comparison in one crosstab overlay) | pairwise and cell-significance overlays |
| `row` / `column` | per row or per column of a matrix host | opt-in, e.g. column-letter significance testing per banner |
| `request` | every inferential result in the request (`Tests`, `PostTests`, inferential overlays, `MAT_*` p-values) | opt-in |
| `matrix` | all cells of one `MatrixResult` | `MAT_CORRELATION` family |

**Where it is set:**
- `Request.Multiplicity` sets the request default.
- Each `OverlaySpec`, `Test` and `MatrixSpec` may carry its own `multiplicity`.
- `Options.DefaultMultiplicity` is the instance default. **Decided: the shipped default is `none`; correction is opt-in.** Today's numbers stay byte-identical, and the predict advisory makes uncorrected multiplicity visible.

## Output

**Additive and never destructive:** raw p-values stay exactly where they are. Corrected values ride beside them:

- `OverlaySummary` / series and matrix entries gain `p_adjusted` and `significant_adjusted` (at the layer's alpha), plus a `multiplicity {method, family, m}` block.
- `TestResult` gains `p_adjusted` and `multiplicity`.
- `MatrixResult.auxiliary` gains a `p_adjusted` matrix when the `MatrixSpec` carries `multiplicity`. **Decided:** the standalone `OVERLAY_CORR_PVALUE` planned in vector-matrix 04 is **dropped**. Matrix p-value correction is `MatrixSpec.multiplicity` and nothing else, so there is one mechanism.

## Guidance hooks

- Predict counts the inferential results in a request. When more than a threshold (default 10) share no correction, it emits the guided-analysis advisory `PULSE_ADVISORY_MANY_TESTS`, whose fixup sets `multiplicity`.
- Explain: when corrected values are present, it narrates the corrected significance; when absent with many tests, it says so ("12 comparisons were made without correction; about 0.6 would be significant by chance").
- Purpose metadata gains glossary terms `multiple-comparisons`, `family-wise-error` and `false-discovery-rate`.

## Execution

Correction is applied at the overlay/test fold, **after** all p-values in a family exist. It is therefore a terminal-flush operation, consistent with inferential overlays already being buffered-only. A family spanning several Compose slots is computed in the Compose post-slot fold.

## Gates

- **`TestMultiplicityReferenceValues`:** each method against published reference vectors (R `p.adjust` outputs, recorded as fixtures).
- **`TestMultiplicityNoneIsIdentity`:** `none`, or an absent `multiplicity`, produces byte-identical output to today.
- **Family-boundary tests:** a `layer` family never mixes p-values across layers; `row` never mixes across rows.

## Deliverables

- [x] `processing/multiplicity`: Bonferroni, Holm, BH, BY
- [x] `multiplicity {method, family}` on `Request`, `OverlaySpec`, `Test`, `MatrixSpec`; `Options.DefaultMultiplicity`
- [x] Families `layer` / `row` / `column` / `request` / `matrix`, including across Compose slots
- [x] Additive `p_adjusted` / `significant_adjusted` / `multiplicity` on overlay summaries, `TestResult` and `MatrixResult`
- [x] Predict advisory and Explain narration hooks; glossary terms
- [x] Reference-value, identity and family-boundary gates; topical skill `multiplicity-correction.md`

## Decisions

- **Shipped default:** `none`. Correction is opt-in per request, slot or instance (`Options.DefaultMultiplicity`). Changing a default that moves visible significance flags would conflict with the determinism promise in `STABILITY.md`.
- **`OVERLAY_CORR_PVALUE`:** dropped in favour of `MatrixSpec.multiplicity`.
- **Shipped as U13** ([unit](../units/U13-multiplicity.md)): regression coefficient p-values are out of scope and `TEST_TUKEY_HSD` is never corrected (already family-wise); the `compose` family pools every slot and Compose-host layer, other families stay per slot; ProcessChain is wired per stage (no family spans stages); the `matrix` family and `MatrixSpec.multiplicity` wait for the matrix units; the predict advisory is the data field `p_values` (threshold 10) rather than an advisory code, for the recommend layer to read.
