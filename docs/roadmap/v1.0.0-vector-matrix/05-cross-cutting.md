# 05 — Cross-cutting concerns

Policies that apply to every feature in documents 01–04. Most bugs in multivariate code are **silent wrong numbers**, not crashes. This document exists so each policy is decided once.

---

## X1. Weighting

- Every `MAT_*`, multivariate `TEST_*`, vector `ATTR_*` / `AGG_*` and `GROUP_KMEANS` accepts `weight: <field>`.
- **Weighted means and co-moments use frequency-weight semantics by default.** Effective sample size (Kish, `(Σw)² / Σw²`) is reported in `Components` and used for df in inferential output when `params.weight_kind: "probability"`. The difference changes p-values materially, so the choice must be explicit and documented in each atomic skill.
- Null or negative weight → the row is excluded with a counted `n_weight_invalid`, never coerced to 0 silently.
- **Gap to note:** today only `AGG_WEIGHTED_MEAN` takes a weight. A request-level default weight (`Request.Weight`) is worth considering as a separate small proposal — every survey request repeats it.

## X2. Missing data

| Mode | Behaviour | Guarantees |
|---|---|---|
| `listwise` (default) | row contributes only if all p members are non-null and non-NaN | result is PSD; one N |
| `pairwise` | each pair uses its own complete rows | maximal data use; result **may be non-PSD**; per-pair N in `auxiliary.n` |

- Non-PSD pairwise result → warning `PULSE_MATRIX_NOT_PSD`; downstream decompositions (PCA, Mahalanobis, partial correlation) **refuse** unless `params.repair: "nearest"` applies Higham's nearest-correlation projection, which is then reported as a warning with the Frobenius adjustment size.
- Listwise drop rate above `params.max_drop_share` (default none) → warning `PULSE_MATRIX_LISTWISE_HEAVY_DROP` with counts. Heavy listwise loss is the most common silent bias in survey multivariate work.
- Imputation (mean / EM) is **out of scope** for v1.0.0.

## X3. Numerical stability

- Accumulate centred moments (Welford/West), never raw `Σxy − ΣxΣy/n`.
- Decompositions go through `linalg` only; tolerances are one documented constant.
- Singular / near-singular covariance (p ≥ n, perfect collinearity, constant column) → coded error naming the offending variables where identifiable: `PULSE_MATRIX_SINGULAR` (`details.rank`, `details.condition_number`, `details.dependent_fields`). Constant column → existing `PULSE_TEST_VARIANCE_ZERO` style, reused as `PULSE_MATRIX_VARIANCE_ZERO`.
- Iterative methods (factor, k-means, IPF, Markov power iteration) report `iterations` and `converged` in components; non-convergence is a warning plus a flagged payload, never a silent best-effort.

## X4. Determinism

Goldens, request hashing, `Watch`, and LLM consumers all depend on byte-stable output.

- Eigen/singular vector sign: largest-magnitude component positive.
- Component / factor ordering: eigenvalue descending, ties by variable order.
- Factor rotation: fixed start, fixed iteration order.
- k-means: seeded k-means++, cluster labels renumbered by size.
- Merge order: shard and parallel-segment merges combine in a fixed tree order so floating-point results are identical regardless of `ShardWorkers` / `DecodeWorkers`. This needs an explicit test — the existing parallel arms already promise "parallel reducers answer like the serial arm" (cf. recent E6-S3 fix), and co-moment merges must meet the same bar, probably as "equal within 1 ulp-scaled tolerance" with the tolerance documented, or byte-equal via deterministic merge tree.

## X5. Execution modes

| Mode | Matrix support |
|---|---|
| Buffered Process | all |
| Streaming Process | co-moment-based `MAT_*` and multivariate tests (emit at terminal flush); rank-based correlation, `GROUP_KMEANS`, fitted `ATTR_*` force buffered — added to the forced-buffered list in `streaming-and-watching.md` |
| Parallel shards / decode | mergeable operators only, via `processing.CanMergeRequest` |
| Compose / ComposeParallel | per slot; COMPOSE-host overlays across slots |
| ProcessChain | mergeable only at v1 (existing chain rule) |
| Crosstab fused | unaffected — matrix overlays fold after finalisation |
| Projected decode | vectors expand to member fields in `NeededFields` |
| Joins | vectors may name joined fields after the join |

## X6. Error codes (new, `PULSE_MATRIX_*` / `PULSE_VECTOR_*` family)

Each needs a `codeMetadata` entry with Message + ≥1 Fixup.

| Code | When |
|---|---|
| `PULSE_VECTOR_EMPTY` | vector resolves to zero fields |
| `PULSE_VECTOR_MEMBER_TYPE` | non-numeric member |
| `PULSE_VECTOR_UNKNOWN` | operator names an undeclared vector |
| `PULSE_VECTOR_DIM_MISMATCH` | two vectors in one operation differ in length |
| `PULSE_MATRIX_DIM_EXCEEDED` | p above `MaxMatrixDim` |
| `PULSE_MATRIX_SINGULAR` | rank-deficient where inverse/Cholesky needed |
| `PULSE_MATRIX_NOT_PSD` | pairwise result non-PSD (warning; error downstream without repair) |
| `PULSE_MATRIX_VARIANCE_ZERO` | constant member |
| `PULSE_MATRIX_INSUFFICIENT_N` | n ≤ p (or below operator minimum) |
| `PULSE_MATRIX_NOT_CONVERGED` | iterative fit did not converge (warning) |
| `PULSE_MATRIX_LISTWISE_HEAVY_DROP` | warning |
| `PULSE_VECTOR_METRIC_UNSUITED` | metric poorly matched to the vector `kind` (e.g. raw cosine on a rating scale) — warning, error under `--strict` |
| `PULSE_VECTOR_METRIC_UNKNOWN` | metric not in the registry |
| `PULSE_OVERLAY_MATRIX_NOT_SQUARE`, `PULSE_OVERLAY_MATRIX_SHAPE_MISMATCH`, `PULSE_OVERLAY_RAKE_NOT_CONVERGED` | overlay family — raised with their own code per the overlay rule |

## X7. Extension points

- New registration kind `MatrixOpRegistration` in `pulse.Options.Extensions`, with `ComponentSchema`, `FieldInputs` (vector expansion), `Mergeable`, and an `OutputKeys` declaration.
- Naming policy regex adds `MAT` to the category alternation: `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH|MAT)_…`. Reserved namespaces unchanged.
- Probe-validation constructs each factory against a synthetic schema with a 3-member vector.
- Extension operators receive the same accumulator API (`comoment.Accumulator`) so an embedder writing a custom multivariate statistic gets streaming/merge for free.

## X8. MCP surface

- No new tool is required: matrices ride `pulse_process` / `pulse_compose` / `pulse_process_chain` responses. The manifest remains the source-of-truth tool count.
- The `pulse_predict` output is particularly valuable here: an agent can ask "how big is this matrix and what will it cost" before running it.
- Skills: one topical design skill `skills/multivariate-design.md` ("which matrix operator answers my question" decision table, missing-data and weighting guidance, the multiple-comparison warning) and `skills/matrix-overlays.md` (or a section in `overlay-system.md` if budget allows).

## X9. Update Demand impact (to plan PR sizes realistically)

Each committed item drags companions. A rough inventory:

| Change | Companions |
|---|---|
| Each new `MAT_*` | `skills/op-mat-<kebab>.md`, `descriptor/capabilities_matrix.go`, an `examples/` tag, components schema keys under `## Components`; **new gates**: `TestSkillsCoverAllMatrixOps`, `TestManifestMatrixOpsComplete` (gate names to be listed in CLAUDE.md "Non-Skippable CI Gates") |
| `Request.Vectors`, `Request.Matrices`, `Response.Matrices` | new rows in `.claude/reference/update-demand.md`; CLAUDE.md "Output Format Contract"; `docs/src/contract/payload-schema.md`; regenerate payload-schema golden |
| `vec_f32` / `vec_f64` | `byte-layout.md`, `type-vec-f32.md`, `type-vec-f64.md`, `cohort-schema-design.md`, CLAUDE.md byte-layout invariants (type count 20 → 22) |
| New overlay kinds | `op-overlay-*.md`, `types.AllOverlayKinds()`, `OverlayStreamability` row |
| New error codes | `errors/codes.go`, `errors/fixup_metadata.go` |
| `MAT` extension category | `docs/src/internals/extension-points.md`, CLAUDE.md naming-policy regex |
| CLI import `--vector` flag | `docs/src/cli/flags.md`, `skills/session-bootstrap.md` if agent-relevant |

**CLAUDE.md size budget (50,000 bytes):** this theme will add a new category, a new Response slot and two field types to always-loaded prose. Plan for a new `.claude/reference/matrix-and-vectors.md` holding the long form from day one, with CLAUDE.md carrying only the always-load half (category name, slot names, the PSD/missing-data rule, the determinism conventions) and a pointer.
