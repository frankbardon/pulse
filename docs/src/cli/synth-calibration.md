# Synth calibration figures and design rationale

Companion to [`profile create`](profile-create.md), [`synth from-profile`](synth-from-profile.md)
and [`synth from-schema`](synth-from-schema.md).

This page is the archive for two things the `synthetic-data` and
`synth-structural-rules` skills deliberately do **not** carry:

1. **Calibration figures** — what each design decision measured, on the cohort it
   was measured on. A skill is read by an agent that needs the rules it cannot
   guess; a figure from one cohort is evidence for a past decision, not a rule.
2. **Rejected alternatives** — the design roads not taken, kept so a later
   reader does not re-open a closed question, or quietly re-introduce a defect
   that was already paid for once.

Unless a paragraph says otherwise, "the motivating cohort" is one 381,324-row,
122-field consumer-survey cohort (90 `packed_bool` fields, 14 small-integer
fields, 16 categoricals), and every figure is as measured at the time of the
change it justifies. Figures are **not** re-measured on every release; treat
them as dated evidence, and re-measure before quoting one as current.

## Marginals

### The boolean defect (`bernoulli` marginals)

A `packed_bool` falls to the profiler's numeric accumulator, and reconstructing
it as `normal(mean, std)` clamped to `[0, 1]` is wrong on the wire: under the
historical `value != 0` threshold `P(false)` is `Φ(−p/σ)`, so a 20%-prevalence
boolean generated at **69.1%**, a 50% one at **84.2%**, an 80% one at **97.7%**.

On the motivating cohort, where 90 of 122 fields are `packed_bool` and 90 of 105
captured models target one: mean prevalence error **0.4676**, with **89 of 90**
fields off by more than 0.05 — every 11–14% brand-attribute item generating at
~64%. Post-fix on the same profile: mean error **0.0032**, max **0.0116**, zero
fields off by more than 0.05.

Per-cell, the conditional-pair arm recurred once per cell and was worst exactly
where the signal is: a 5% cell generated at **41%**. A hand-authored continuous
distribution on a `packed_bool` is still biased (22.7% for a 0.2 field, because
clamping piles asymmetric mass on the near bound) but no longer inverted, since
`toBool` rounds at 0.5.

### The small-integer defect (`discrete` marginals)

Measured on the motivating cohort at 40,000 generated rows, source share →
generated share under the clamped normal:

| Field | Type / range | Level | Source | Generated |
|---|---|---|---|---|
| `familiarity` | u4, 1–7 (U-shaped) | 1 | 0.2526 | **0.1373** |
| `familiarity` | | 7 | 0.2198 | 0.1323 |
| `nps` | u4, 0–10 | 10 | 0.3200 | **0.2253** |
| `nps` | | 0 | 0.0284 | **0.0020** |
| `useCon` | u4, 1–6 | 6 | 0.3143 | 0.1806 |
| `useCon` | | 4 | 0.0778 | 0.2121 |
| `sow` | u16, 0–15 | 0 | 0.6469 | **0.3804** |

`sow` levels 10 and 12–15 were never drawn at all. `nps`'s mean came back
7.3463 against 7.5489 — 3% off while its top level was off by 28% of its own
size, which is how the defect survived. Post-fix on the same profile: 0.2493 /
0.2165, 0.3310 / 0.0269, 0.3154 / 0.0792, 0.6460 with every level present;
worst per-level error across the four fields **0.1153 → 0.0110**.

An identity-cell conditional pair without the same treatment measured a worst
per-level error of 0.0903.

`maxDiscreteLevels` (64) admits every coded scale by a wide margin (a `u4`'s
entire domain is 16, an NPS is 11, a 1–7 Likert is 7, `sow` is 16) and bounds
the document addition at 64 levels × 2 numbers per field — **+0.22%** on the
motivating cohort.

### Generated-latent variance (a modelled boolean's prevalence)

`Φ(u)` is uniform only when `Var(u) == 1`. At fit time it is by construction; at
generation time the predictors come from their own reconstructed marginals.
Measured on `aware` (captured `p` 0.7473906704010238):
`Var(prediction)/std²` is 0.045270 against the captured `R²` of 0.077878 — nine
of its 64 terms never fire on a generated row — so the latent variance is
0.970721 rather than 1.0033, and `P(aware == 0)` returns 0.247193 over 20 seeds
instead of 0.252609: a −0.0054 bias at ten standard errors.

It is **not** the sampler or the write path, both of which are exact: 200 seeds
of a lone bernoulli field give +0.000046 (z = +0.28), and the same 122-field
spec with every model removed gives +0.000324 (z = +0.69).

## Correlations

A step/staircase copula participant holds its own marginal exactly and
attenuates the realised correlation. Measured on two real 7-level `u4` columns
(`regard` × `meaningfulness`) at a captured rho of +0.8400 over 200,000 rows:
Pearson **+0.8072**, Spearman **+0.8051**, both marginals within 0.003 per level.

`isNumericFieldType` was a hand-written list until it was derived from
`fieldTypeFromName`. It carried three `nullable_*` spellings no spec can declare
and omitted `u4`, which silently dropped every small-integer pair from
`Spec.Correlations`: on the motivating cohort that was **all 16** captured
numeric pairs and 11 of 14 integer columns, leaving the reconstructed
correlation structure empty with nothing saying so. On that cohort the fix is
byte-identical output — all 16 pairs enter and `resolveConflicts` drops all 16,
because every participant is already claimed by a model or a conditional pair —
so the visible change is 16 silent absences becoming 10 aggregated arbitration
warnings.

Assume-and-record: on a ten-field matrix built from a top-16 pair list, **29 of
45** pairs were asserted independent with nothing saying they had been asserted
rather than measured.

## Multi-predictor models

### Top-K collapse

On the motivating cohort the unbounded dummy expansion of 16 categoricals was
**2,420 columns** (`brand` alone 1,906 levels, 79% of the width) and all 105
numeric targets were skipped as too wide — `--fit-models` produced nothing at
all on real data. Collapsed, the same cohort is under 200 columns before any
statistical criterion runs.

### Rank-deficiency refit

The weakest-predictor refit (`maxModelRefitRounds`, 4) is the difference between
**23 skipped targets and 2** on the motivating cohort.

### End state after selection and shrinkage

| Figure | Before selection | After selection | After shrinkage |
|---|---|---|---|
| numeric targets modelled | 0 (all skipped at 2,420 columns) | 103 | **105** |
| carrying predictors | 0 | 53 | **55** |
| admitted (field, target) relationships | — | 76 | **113** |
| skipped targets | 105 | 2 | **0** |

The predecessor pick-one arms applied 86 pairs on the same cohort. The
shrinkage interaction, not a change to the variance floor, is the difference
between the last two columns: a ridge-augmented Gram is positive-definite, so
the two irreducibly-redundant targets fit with both nested candidates jointly
shrunk.

### Thin-level warning volume

Per-(target, level) emission restated **89 distinct levels in 477 lines** on the
motivating cohort, on a warnings slice `--conditional` alone already puts ~2,900
thin-pair lines on. Aggregated by (field, level) and capped at
`maxThinLevelWarnings` (20) thinnest-first, with a counted remainder.

Shrinkage retention as a pseudo-count (`alpha = 50 / n_obs`): 3 observations
keep 6% of the free coefficient, 30 keep 38%, 50 keep half, 500 keep 91%. A
clean design stays exact OLS, recovering an exactly-linear cohort to 1e-6.

### The `"other"` catch-all

Serialising the collapsed catch-all column as `numeric` rather than as a
`categorical_level` named `"other"` cost **105 captured models applied as 20**,
with a plausible cohort generated either way and the only signal a warning line
the CLI did not print at the time. Corrected, **55 apply** — exactly the 55
capture reports as predictor-carrying, the other 50 being zero-predictor models
dropped on purpose — and `Spec.ResidualCorrelations` grows from **190 pairs to
1,485** (= C(55,2)).

On a 20,000-row generation from that profile **18,198 rows carry
`brand = "other"`**: the dominant arm, not an edge case.

### Shape-fitted targets

On the motivating cohort **15 of 19** shape-fitted fields carry a model with
predictors once the exclusivity was removed, including all four motivating ones
(`nps`, `detractor`, `promoter`, `catSpend`). The mixture quantile inverse costs
~128 `math.Erf` calls per row, paid only by fields that are both shape-fitted
and modelled.

### Residual correlations

Capture is quadratic in modelled fields: 105 targets is **5,460 pairs**
(= C(105,2)). Translation drops the ~50 zero-predictor models, so **1,485**
(= C(55,2)) reach the spec.

Measured source vs generated at 50,000 rows: `regard ~ meaningfulness` 0.791
against a source 0.835; `people ~ promotion` 0.794 / 0.835; `price ~ promotion`
0.783 / 0.830. Before the catch-all fix these three sat at ~0.00 —
annihilation, not attenuation, with every marginal in the fidelity report
looking healthy throughout.

## Fidelity report

### Probit-score restoration

Refusing a staircase target outright (rather than scoring both sides on the
calibrated interval-midpoint probit score) measured as an instrumentation loss
of:

| Figure | Refused | Calibrated |
|---|---|---|
| comparable targets | 2 of 53 | **53 of 53** |
| compared predictor terms | 150 of 2,499 | **2,343 of 2,499** |
| step-quantile errors | 51 | **0** |
| flagged terms | 7 | 97 |

Measured `score_retention` across 2,193 calibrated terms: min 0.075, median
0.456, p75 0.814; ≈0.54 at `K = 2` against 0.83–0.92 at `K = 7`.

Report-wide on the motivating cohort at 40,000 rows: 53 model entries, 53
comparable, 40 flagged; 2,499 predictor entries, 2,343 compared, 97 flagged, 156
unestimable — 143 of those the top-K ranking mismatch, 7 constant columns, 6
below the score's resolution.

`model_residual_correlations` is not restored by the same move and stays at **1
compared pair**, the rest counted under `no_model_fit`. The total is C(P, 2) over
`P` applied models, so the participant count has to be quoted with the figure:
1 of **1,378** at the 53-applied-model capture the table above comes from, and 1
of **1,653** re-measured on a later tree at 58 applied models. Same finding,
different participant count.

### Unidentified terms

Not the `"other"` catch-all — all 54 catch-all terms fire, 3,992–9,046 rows
each. The design's top-K and `Categorical.Top`'s rank on different bases (the
design by frequency within the rows the fit listwise-admitted for that target,
the marginal over the whole cohort). Both keep 32 and they disagree: 9 of
`brand`'s design levels, 4 of `ageExact`'s and 2 of `category`'s are absent from
the marginal that generates them — 138 of 2,235 entries in one run, 168 of
2,769 in another.

### Quantized-target attenuation

Every modelled target on the motivating cohort is `packed_bool` or a small
integer, and 30 of 55 models flag; 18 of those are `packed_bool` and 9 are `u4`.
`peopleAware` recovers 0.11 against a captured 0.556 on 1,447 firing rows at an
SE of 0.021 — measured, not noise. That is a property of the generated cohort,
not of the instrument.

### Disjointness and cost

55 fields in `models`, 46 in `categorical_numeric_pairwise`, **intersection 0**.

`model_residual_correlations` on 55 participants: 1,485 compared, 147 flagged,
mean delta 0.055, max 0.490, 0 unmeasured, +6 KB and ~5s on an existing 1.65 MB
/ ~2 min report. The largest gaps sit on `packed_bool` and small-integer targets
(`aware` × `familiarity` 0.738 → 0.248) — the same quantization attenuation the
`models` section reports on the same fields.

A modelled numeric's mean spread across a predictor's levels is visibly
compressed against source (`nps` across `educationLevel`: source 1.85,
synthetic 0.73). Recovered ≈ captured means that compression is the legitimate
partial-effect-vs-marginal-contrast gap; recovered itself attenuated means a
real fault.

## Structural rules

### `null_together`

The motivating profile's `nps` / `promoter` / `passive` / `detractor` block
shares `null_rate` 0.8260193431307760 exactly and came back all-present in **45
of 40,000** rows against the ~6,960 the block actually has, because
`0.174⁴ ≠ 0.174`. With the rule: **6,809–6,994 across five seeds, and zero
partial blocks**.

### `owns_nulls`

A 50-field `{"when": "aware == 0"}` gate that is exactly right about every gated
row took `regard` from a captured 0.2526 to **0.4369**, and the five `*Aware`
gates took `people` from 0.4273 to **0.7506** — every gated cell correct, every
rate wrong, because the marginal `null_rate` keeps firing beneath the gate and
the two compose as `g + (1−g)·r`. With `"owns_nulls": true` all fifty land on
**0.2455** (the gate's own firing rate) with the gate still exact on 491,200 of
491,200 gated cells; `people` lands on 0.4208 against a captured 0.4273.

Suppression is a wrapper that discards the null verdict, never a rebuild without
the nullable wrapper: against an un-owned run at one seed the other 72 fields
are cell-identical and the only change is **381,351 null flags removed** on
non-gated rows, 0 added, 0 values moved.

Divergence noise term: at 200 rows a correct claim on a 0.25 field misses by
0.02 about half the time, which is why the warning needs both the 0.02 threshold
and two standard errors.

### Pre-rounding

A three-band NPS classification written straight off the pre-rounding float sets
exactly one flag per row and merely disagrees with the number beside it —
**476 of 3,486** scored rows at 20,000 rows.

`round` versus `int`, measured on the motivating profile at 40,000 rows:

| Normalisation | `nps` rows moved | Band disagreements repaired |
|---|---|---|
| none | — | 0 of 1,029 |
| `round(nps)` | **0** | **1,029** |
| `int(nps)` | 3,018 of 6,944 scored | 0 |

`int` reaches band agreement by dropping respondents a point. Same shape on a
gate: `familiarity <= 1` fires on 1,843 rows raw, 2,739 behind `round()` with the
column unchanged, and 3,824 behind `int()`, which gets there by dropping 1,085
respondents a point.

An ungated `set_expr` clears the null mask on every row: the four-field NPS
block goes from 6,944 present to 40,000 of 40,000.

The never-fired message's third arm exists because the pre-rounding advice is
wrong for most survey columns: on the motivating cohort **103 of 123 fields**
round on write and draw exact values, against 3 that carry the hazard.

### Rule claims

An unconditional `set_expr` over `promoter` takes the applied models from 55 to
54, drops its 54 residual-correlation pairs, and removes its `models` fidelity
entry. A `set_null` over the same field changes none of the three. The
documented pre-rounding remedy `{"set_expr": {"nps": "round(nps)"}}` reads its
own target, so it claims nothing — claiming there would strip `nps`'s model and
all 54 of its residual correlations.

### `--suggest-rules`

On the motivating cohort the three detectors emit **14 candidates** in one file
(`--rules` consumes it unmodified) for +7.5% CPU over the two-detector figure and
no additional cohort read:

- **Gating (8).** `aware` gating 63 fields — a strict superset of the 50-field
  block, the extra 13 being the 0.2649 cluster. It also proposes
  `round(familiarity) == 1` with evidence identical to sixteen digits: both gate
  the same 96,326 rows with zero exceptions either way, so nothing in the data
  separates them. `gated_share` 0.2526093295989762 matched 50 fields'
  `null_rate` to ten digits — the figure that identified this cohort's gate by
  hand. A bare `==` under-fires silently: 531 of 901 rows in the committed
  regression.
- **Co-missing blocks (4).** 50 fields at 0.2526093295989762, **26 at
  0.3564055763602606** (the cluster no single gate could explain), 13 at
  0.2648718674932603, and the four-field NPS block at 0.8260193431307760 — plus
  one always-null column (`lgbt`, null on 20,000 of 20,000 generated rows) and
  four near misses at 0.9989 / 0.9798 / 0.9788 / 0.9537. The 27th field at
  0.3564, which research had counted into the cluster by rate, is **not** null on
  the same rows (145 disagree) — the exact case an identical-rate test would
  have fabricated.
- **Dependencies (2).** `nps -> {detractor <= 6, passive 7..8, promoter >= 9}`
  on 66,343 co-present rows, with the standard 9-10 / 7-8 / 0-6 NPS definition as
  the detector's output rather than its input; and
  `aware = round(familiarity) >= 2` on all 381,324 rows, which answers the proxy
  question the gating detector could only report.

Eleven further findings are reported rather than proposed: 5 fields too wide to
gate, 3,932 co-missing relationships restating the blocks, 4 near blocks, 5
fields too wide for dependency, 3 constant columns.

### Ordering has teeth

Five `*Aware` gates sit inside the 50-field block and one `set_expr` writes a
sixth gate's field. Under detector preference alone, **8,693 of 20,000**
generated rows carried an answer to a question the same file said was never
asked; **0** after the writer-before-reader reorder, with the coherence table
unmoved either way (which is why the orphan count is its own gate). A dependency
emitted first rather than last leaves 940 orphan rows against 0.

### The accepted candidate file, fed back

Edited only by deletion (the `familiarity` proxy gate, the `useCon` gate and the
`familiarity -> aware` dependency removed) and fed back through
`synth from-profile --rules` at 40,000 rows:

| Coherence check | Without rules | With the candidate file |
|---|---|---|
| all four NPS fields present together | 39 | **6,968** (target ~6,959) |
| all four null together | 18,560 | 33,032 |
| `nps` present, flag null | 6,845 | **0** |
| `nps` null, flag present | 14,556 | **0** |
| mixed blocks | 21,401 | **0** |
| exactly one flag set | 113 | 6,968 |
| band agreement | 16 | **6,968 (100%)** |
| non-gated rows carrying all 50 fields | 0 | 22,572 |

All 50 gated fields are null on 9,878 of 9,878 `aware == 0` rows with none
present. Every candidate fires on a non-zero row count (lowest 5,491 of 40,000)
and no `rule never fired` line is raised.

Two deletions are themselves findings. Accepting `familiarity -> aware` moves
`aware`'s generated marginal from 0.2469 to 0.1373, because an exactly-determined
dependency inherits its **source's** reconstruction error. Keeping the `useCon`
gate over the NPS block double-counts against the block's own null draw,
collapsing `nps` presence from 6,884 to 1,098 — a gate repairs co-missingness
only, which is what `owns_nulls` exists for.

## Rejected alternatives

Each of these was considered and closed. Re-opening one needs a reason the
original did not have.

| Alternative | Why not |
|---|---|
| Value-space model draw (draw the marginal, then add the prediction) | destroys the fitted marginal it was meant to protect. Must not be quietly re-introduced |
| Statistical significance as the predictor-admission gate | at 381k rows every candidate is significant, including ones explaining a thousandth of the variance — "all predictors enter" in disguise, and silently, since every coefficient it admits is real |
| Incremental (residual-on-residual) candidate scoring | makes the admitted set depend on candidate order; redundancy is resolved by the solver's refit instead |
| A functional-dependency probe for collinear predictors | the dependency that bites is LINEAR, not functional, so the probe misses it while claiming to have handled it |
| Sharing `MinPairObservations` (30) for thin LEVELS | different questions ("enough co-occurrences to mean anything" vs. "enough rows for a free coefficient"); one constant would couple two unrelated tunings forever |
| Refusing an incomplete `correlations` list | an incomplete list is the normal input; zero is the only completion that adds no structure the caller did not ask for. Silence was the defect, not the completion |
| Compensated (Kahan) summation for reproducible capture | still order-dependent in principle; sort the keys instead |
| `math.FMA` for architecture-independent floats | forces fusion everywhere — it changes amd64's values rather than preserving them |
| Inverse-Mills recovery for a step target | reports a large false attenuation for a generation path that is exactly correct |
| An ordered-probit refit for the recovery | more efficient, not more correct; `processing/regression` has `binomial`+`probit` as a reserved, unimplemented link |
| A tolerance-terminated mixture quantile inverse | makes the answer depend on how many steps a given `p` needed, breaking byte-determinism; Newton additionally explodes where the density underflows between modes |
| One `set` map with a `{"$expr": …}` marker | leaves `{"set": {"region": "west"}}` undecidable between a literal and an identifier |
| A two-phase rule pass (evaluate gates, re-run dependent stages) | doubles the stage surface and needs a rule↔model dependency order no document declares |
| Append mode for `--rules` | a derived spec carries nothing to append to; it would only invent an ordering question |
| Auto-applying `--suggest-rules` candidates | a detected pattern can be a coincidence of the sample, and detection finds the statistical gate while a human knows the semantic cause |
| Grouping co-missing blocks by identical `null_rate` | two unrelated fields can share a rate to sixteen digits and overlap by chance; admission is identical null PATTERN |
| A per-field null-pattern hash | answers only the yes/no question, so a near block is invisible and indistinguishable from an unrelated field |
| Truncating (rather than abandoning) an over-cap histogram, level map or field set | every share below the cut becomes a share of an arbitrary subset — the defect the detector exists to remove |
| Index-based handles in `_evidence` | the file is edited by deletion and every index below a deleted line shifts |
| Read-side rehabilitation of the old `numeric` catch-all spelling | would freeze the bug into the file format; re-capture is the answer |
| Topologically sorting `rules[]` | declaration order is the one ordering an author can read off the document |
| Refusing a `null_together` member that is not nullable | the gated-block idiom names a never-null field first on purpose |

## Related

- [`profile create`](profile-create.md) — capture flags and the full document shape.
- [`synth from-profile`](synth-from-profile.md) — generation, `--rules`, `--emit-spec`, the fidelity report.
- [`synth from-schema`](synth-from-schema.md) — the hand-authored spec grammar.
- `skills/synthetic-data.md` — the contract statements an agent must not guess.
- `skills/synth-structural-rules.md` — the `rules[]` / `constraints[]` contract.
