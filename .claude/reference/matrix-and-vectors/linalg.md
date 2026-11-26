# Matrix and vectors — the `linalg` core: metrics, backends, tolerances, errors, CoMoment, merge tree

Per-topic slice of the matrix-and-vectors contract. Index (package surface + section map): `.claude/reference/matrix-and-vectors.md` — load it first, then only the slice(s) your change touches.

## Metric registry — `linalg/metric`

`linalg/metric` is the ONE definition of every distance / similarity metric (`metric.Names()` / the manifest's `matrix.metrics` is the list). Public, a leaf over stdlib + `errors` + `linalg` + `linalg/detmath` (`TestMetricImportBoundary`); `Metric` is an opaque handle, every returned slice a copy. Manifest block built by `metricCapabilities` in `internal/descriptor/capabilities_matrix.go`; `consumers` lists only consumers the instance offers (`TestManifestMetricsComplete`). No consumer keeps a local metric list or kernel — expr functions, `GROUP_KMEANS`, `MAT_DISTANCE`, the vector attributes and `OVERLAY_CORRESPONDENCE` all resolve through the registry.

- **Direction contract.** Each metric declares `Direction` (native) and `Forms`; `cosine` alone has both (distance `1 − cos`, the cosine clamped to [−1, 1] first). A consumer asks for a form; a metric without it is `PULSE_VECTOR_METRIC_UNKNOWN` (`reason` `no_form`), an unknown name `unregistered`, a metric the consumer does not accept `not_accepted`; an empty name `PULSE_VECTOR_METRIC_REQUIRED`. Never a silent transform.
- **NaN policy.** Pairwise-complete (NaN = missing; ±Inf ordinary). The element sums (`Scaled` metrics) are rescaled `s / (p_observed / p)` as R `stats::dist()` does, skipped when nothing is missing so a complete vector's bits never move; chebyshev (a max) is not rescaled. Compositional metrics close the observed sub-composition. `mahalanobis` is `Complete` (a sub-block of the inverse is not the inverse of the sub-block). Zero overlap → NaN, no error; length mismatch → `*LengthMismatchError`, never NaN.
- **Domains.** A domain violation is NaN + `*DomainError` (aitchison zero → `ReasonZero`; the consumer nulls and counts the row). Fitted stats arrive explicitly in `Stats` (`SD`, `InverseCovariance`, `ColumnMasses`); unusable ones are `*StatsError`.
- **Per-consumer acceptance** (`Consumers`, `Accepted`, `ResolveFor`) is a table in the package; read it there. A consumer may narrow a metric's forms (expr exposes `cosine` in similarity form only). `expr` lists exactly the metrics an expression function exposes — a new expr metric function adds its metric here, or the manifest lies. An overlay kind is offered as a consumer iff the instance lists it under `overlays`. Every registered metric has ≥ 1 consumer (`TestConsumerAcceptanceRules`).
- **Kind defaults.** `metric.Default(kind, form)` is the kind → default table; `metric.DefaultFor(consumer, kind, form, admit)` is that default when the consumer accepts it, else the first accepted metric that suits the kind and is no poor fit. Users: the Vector kind bullet in `vectors.md`.
- **Bits.** FMA-free `float64(a*b)`, left-to-right; aitchison's logarithm is `detmath.Log`. Check contraction with `go build -gcflags='github.com/frankbardon/pulse/linalg/metric=-d=fmahash=vy' -a ./linalg/metric/`. Oracle: `scripts/reference/gen_metric.R` → `TestMetricsMatchROracle`.

## Backend policy — why two backends

A routine is a reference kernel when something downstream has a BIT contract on it; otherwise gonum backs it.

- **Synth seed reproducibility.** Same seed, same bytes, on every machine. Synth's correlator factors a correlation matrix and draws `u = L·z`, so any drift in `L` reaches every correlated value.
- **Merge determinism.** The blocked merge tree promises serial == parallel bitwise, which holds only if `Add` and `Merge` produce the same bits wherever they run.
- **Regression and `MAT_SEM` keep gonum's operation order.** `FactorSPD` / `SPDFactor` / `Mul` are an FMA-free pure-Go PORT of gonum v0.17.0's amd64 operation order (`internal/gonumorder`: the LAPACK routines plus each SSE2 BLAS kernel's lane and accumulator order). Not the reference Cholesky: that is a different order, and swapping it in moved REG_* figures by up to 4e9 ulp. Gates: `TestSPD_MatchesGonumOrder` (bit-equal to gonum on amd64; binds in CI's linux/amd64 run) and `TestSPD_BitPinned` (one SHA-256 of the corpus on every architecture). Refusals use the same pivot test and condition estimate against `ConditionTolerance`.

**Why a gonum-backed Cholesky cannot replace synth's.** gonum's order differs in at least three places (accumulate-then-subtract `Ddot`, reciprocal scaling, a blocked path above order ~64). Measured on 7,800 random SPD matrices: 96% differed in at least one bit of `L`, and 21% of factor-or-fail decisions on rank-deficient inputs differed (changing the ridge retry count) — while the whole suite stayed green, because nothing pinned the factor. Pins: `TestCholesky_BitIdenticalToSynthReference`, `TestCholeskyRidge_BitIdenticalToSynthReference`, plus the synth draw pin.

**FMA-free means:** every product that feeds an addition or subtraction is written `float64(a*b)`. The Go specification forbids fusing an explicitly converted product, and arm64 and amd64 otherwise make different fusion choices. Never remove one of these conversions, and never "simplify" a reference kernel's loop order: the operation order IS the contract.

**Enforced repo-wide.** `TestCrossArchFMAReport_RepoWide` (heavy) and `TestCrossArchBannedMath_RepoWide` (fast) in `internal/testutil/fmagate` fail on any unbarriered contractible product or banned stdlib transcendental in a non-test package that the reviewed allowlist `internal/testutil/fmagate/testdata/numeric-allowlist.txt` does not excuse with a reason. Gate prose: `update-demand/gates-cross-arch.md` (Cross-arch determinism gates).

**Reference Cholesky operation order.** Lower-triangular Banachiewicz (row by row). For `j ≤ i`, start from `S[i][j]` and subtract `L[i][k]·L[j][k]` for k ascending, one term at a time; then the square root on the diagonal, or a true division by `L[j][j]` off it. The factor fails iff a diagonal pivot is ≤ 0. A NaN pivot is NOT ≤ 0, so it propagates into `L`; `FactorSPD` refuses it instead.

**Ridge schedule.** `CholeskyRidge` retries with `DefaultRidgeSchedule()`: eight increments `10^(t-6)` for t = 0..7 (1e-6 … 1e1), each added on top of the last. It returns the ACCUMULATED ridge, 0 when the first attempt succeeds; exhaustion carries the schedule total 11.111111 and `attempts`. Synth surfaces a positive ridge as its warning and raises `SERVICE_VALIDATION` on exhaustion.

## Tolerances, order and sign

- **`Epsilon`** = 2⁻⁵². **`RankTolerance(rows, cols, sigmaMax)`** = `max(rows, cols)·Epsilon·σ_max` — the ONE rank tolerance (LAPACK / NumPy `matrix_rank` default). A singular value at or below it is zero. `ConditionNumber` is `+Inf` for a matrix rank-deficient under it.
- **Order.** Eigenvalues and singular values descend. Values within the rank tolerance of a neighbour form a tie cluster, ordered by each vector's dominant-component index, so ties keep variable order.
- **Sign.** Every eigenvector and right singular vector is flipped so its largest-magnitude component is positive (`U` and `V` flip together; a tie within `DominanceTolerance` = 2⁻²⁶ relative goes to the lowest index). `QR` flips so `R`'s diagonal is non-negative. Without this, gonum may return either sign and goldens flake across platforms.
- **One singular decision** (`internal/processing/singular_rule.go`). Every multivariate surface needing a covariance-shaped matrix's inverse, determinant or full rank asks `rankDeficient` / `singularUnderRule`: singular when a diagonal entry is ≤ 0 or non-finite, or when the smallest eigenvalue of the CORRELATION form is ≤ `RankTolerance(p, p, λ_max)` (scale-free). A tiny positive Cholesky pivot left by rounding is not invertibility — "singular only when `InverseSPD` fails" calls a near-exact dependency invertible. Values stay on the reference kernels; an inversion that still fails is singular too. Oracle: `TestSingularRule_EverySurfaceAgreesWithROracle`. New surfaces must use this rule, not their own.
- **General solve** (`linalg/lu.go`). Doolittle LU with partial pivoting (largest |a[i][k]|, tie → lowest row), true-division multipliers, FMA-free updates; `SolveMatrix` column j equals `Solve` on column j bit for bit, `Inverse` is `SolveMatrix` on the identity. **Singularity policy:** refused iff rank-deficient under `RankTolerance(n, n, σ_max)` (plus a defensive exact-zero pivot); a full-rank ill-conditioned matrix still solves and reports κ. A pivot-magnitude proxy was rejected because it solved near-singular inputs to garbage. Round-trip tolerance: ‖A·x − b‖∞ ≤ 8·n·ε·(‖A‖∞·‖x‖∞ + ‖b‖∞). Gate: `TestSolve_BitPinned`.
- **`ConditionTolerance`** = 1e16, gonum's threshold. An `SPDFactor` whose condition estimate exceeds it still factors, but its `Solve` and `Inverse` refuse.
- **Co-moment merge tolerance.** A merged split is not bit-identical to one serial pass (float addition is not associative); it agrees within 1e-10 relative to scale. Counts are exact. Bit identity across worker counts comes from the blocked tree, not from `Merge` alone.

## Errors

Both codes are `*errors.CodedError`, owned `shared`, with `codeMetadata` in `errors/fixup_metadata.go`.

- **`PULSE_MATRIX_SHAPE_MISMATCH`** — operands whose dimensions disagree (non-square, ragged, length ≠ order, data length ≠ rows×cols, nil, or two `CoMoment`s of different `P` or mode merged). Details name the dimensions.
- **`PULSE_MATRIX_SINGULAR`** — a matrix cannot be factored, solved, inverted or decomposed. Details vary by routine:

| Routine | Details |
|---|---|
| `Cholesky` / `SolveSPD` / `InverseSPD` | `pivot` + the singular diagnostics below |
| `FactorLU` / `Solve` | `reason` = `singular`, `n`, `rank`, `condition_number` (`+Inf`), + `pivot` on an exact-zero pivot; or `reason` = `non_finite` |
| `CholeskyRidge` exhausted | `attempts`, `ridge` |
| `FactorSPD` | `reason` = `not_positive_definite`, `n` + the singular diagnostics below |
| `SPDFactor.Solve` / `Inverse` | `reason` = `ill_conditioned` + `condition_number` (`+Inf` on breakdown) |
| gonum decompositions | `reason` = `non_finite` or `no_convergence` |

**Singular diagnostics.** A failed reference `Cholesky` or `FactorSPD` on a FINITE matrix adds, from one gonum `SymEigen` on the failure path only (`linalg/singular.go`; the factor's bits are untouched): `rank` (under the one rank tolerance), `condition_number` (`+Inf`, wire `null`, when rank < n) and, when rank < n, `dependent_indices` (axes with a component above √ε in some null-space eigenvector). These values are gonum-backed: same-machine stable, not bit-stable across architectures. A matrix operator wraps the error through `processing.singularMatrixError`, which adds `matrix` and `dependent_fields` without mutating the linalg error. Callers owning a domain code keep it (synth `SERVICE_VALIDATION`, regression's rank-deficient codes).

## `CoMoment` weight and missing semantics

- **Update.** Weighted Welford–West: the first row with mass sets the mean exactly, so a constant column has variance exactly 0. **Merge** uses the exact Chan–Golub–LeVeque combine. `Merge` mutates the receiver; `MergeTree` mutates nothing.
- **Missing values.** NaN in `x` means missing. `Listwise` skips the whole row; `Pairwise` skips per pair (each pair keeps its own count, weight, means, co-moments). A row the mode admits nowhere is not counted and its weight is never judged.
- **Weights** follow `.claude/reference/weighting.md` (Validation). A NaN, ±Inf or negative `w` skips the row and increments `NWeightInvalid`. **`w = 0` is valid: the row counts toward `N` (and `PairN`) but adds no mass.** `NEff` is Kish's `(Σw)²/Σw²`.
- **Per-pair accessors.** `PairW2` (the pair's Σw², merged by addition); `PairNEff` = exactly `PairW²/PairW2` (the arithmetic `weighting.KishNEff` uses; 0 with no mass); `PairM2` (the raw Σw(xi−mi)(xj−mj), so `Cov(ddof)` = `PairM2/(PairW − ddof)` to the bit — the only raw per-pair accessor); `PairNMass` (rows with w > 0, so inference row floors match a weighted single-pair test, while the components floor `PairN` keeps counting weight-0 rows). Under Listwise each returns the global figure.
- **Listwise state capture / restore.** `ListwiseState()` returns the complete stored state (raw mean, raw `M2`; copies) and `NewCoMomentFromListwise(s)` rebuilds it bit-identically for every later output, `Add` and `Merge`. Pairwise has no single state; a malformed state is `PULSE_MATRIX_SHAPE_MISMATCH`. This is the fitted-vector-state boundary: a fit is captured once and rebuilt wherever it is scored, and derived blocks are recomputed, never stored.
- **Porting caveat.** The engine's weighted aggregators treat `w == 0` as NOT contributing (skipped before the row count). Porting a weighted reducer onto `CoMoment` therefore moves its row count on zero-weight rows — decide that deliberately and gate it.
- **Outputs.** `Cov(ddof)` = `M2/(W − ddof)`, NaN where `W − ddof ≤ 0`; probability-weight corrections are the caller's job. `Corr` is the one-root C_ij/√(M2_ii·M2_jj) (`TEST_PEARSON_R`'s arithmetic), falling back to two roots when the product leaves the normal range, clamped to [−1, 1]. A zero-spread variable is NaN in every entry touching it, its diagonal included, never 0. `Mean` with no mass is NaN.
- **Concurrency.** Not safe for concurrent use: one per worker, then merge.
- **No per-row allocation.** Listwise `Add` walks variables last to first so each deviation reads the OLD mean in one register; `TestCoMomentAddListwise_AllocatesNothing` pins it.
- **State size.** Predict sizes one state as `vectors.Matrix.AccumulatorBytes` (`internal/vectors/matrix_rules.go`). A word added to the CoMoment structs moves that estimate; `TestAccumulatorBytesMatchesCoMomentLayout` derives the counts by reflection and fails until `matrix_rules.go` follows.

## Blocked merge-tree contract

One answer has the same bits under every concurrency knob.

- **Blocks.** Fold the row at GLOBAL record ordinal `r` (in a shard archive: the shard's start plus the in-shard index) into block `r / MergeBlockSize` (4096), never into "this worker's partial". `MergeBlockSize` is part of the bit contract: changing it changes merged bits.
- **Tree.** `MergeTree(blocks)` merges level-wise by position, `(b0·b1)`, `(b2·b3)`, …, carrying an odd tail up unmerged (five blocks: `((b0·b1)·(b2·b3))·b4`). The shape depends only on the sequence length and `Merge` is FMA-free, so the root is a pure function of the block sequence.
- **Engine.** Parallel-decode segment boundaries snap to multiples of 4096. Every record source stamps `(shard, shardStart, record)` on the `Record`. A shard edge inside a block arrives as one fragment per shard: the fragment holding the block's first ordinal folds as rows arrive, others buffer and replay in shard order, so every block is the one sequential fold of its rows.
- **Opt-in.** The internal interface `processing.BlockMerger`; `Finalize` rebuilds each block then runs one `MergeTree` in block order. Other reducers keep their merge path. It is NOT reachable through `extend`: an extension `MAT_*` is finalize-only over its slot's merged tree and inherits the bit contract. Wiring: `.claude/reference/execution-modes.md` (Blocked merge).
- **Guarantee.** Serial == any `DecodeWorkers` == any `ShardWorkers` == any shard split, bitwise, on every architecture (uneven, 1-row and empty shards included). The same holds for the `AGG_VEC_*` aggregators.
- **Unstamped records are refused.** A join row or a ProcessChain intermediate row carries no position; a `BlockMerger` refuses it with `PROCESSING_INTERNAL` rather than guessing.
- **Gates.** `TestMatrixInvariance_{Archive,Heavy}` (every `MAT_*`, listwise and pairwise, weighted and not; serial vs decode / shard worker counts, archive vs single-file twin); `TestExtensionMatrixInvariance_{Archive,Heavy}` for an extension through public registration only.

## Parity tolerance rationale

`MAT_CORRELATION` agrees with `TEST_PEARSON_R` within `1e-12 + 1e-12·|r|`, not byte for byte: the matrix is FMA-free and block-merged, the test runs its own Welford recurrence. Bitwise parity holds in one case (unweighted, serial, ≤ 4096 rows, test `field` the earlier member), which is why `CoMoment.Corr` uses the one-root form; `TestMatrixCorrelation_PearsonParity` asserts it on every architecture.

## Cross-architecture detail

- `math/rand/v2` `NormFloat64` / `ExpFloat64` reject-sample through `math.Exp`, which differs between arm64 and amd64; use uniform draws or `detmath.NormFloat64` / `detmath.ExpFloat64` for generator output feeding a bit-exact fixture.
- Generators fuse too: arm64 turns `rng.NormFloat64()*k + c` into one FMA, amd64 does not. Write `float64(rng.NormFloat64()*k) + c`.
- gonum SymEigen / SVD / QR and their consumers keep a few-ulp residual; compare stored fixtures within tolerance. Residual list: `docs/src/library/linalg.md` (Not covered).
- Proof is the committed `internal/fingerprint` golden (generated on arm64) plus CI's linux/amd64 run. Nothing committed may depend on Docker.
- Aid only: `GOOS=linux GOARCH=amd64 go test -c -o t.amd64 ./<pkg>`, then `docker run --rm --platform linux/amd64 -v "$PWD":/w -w /w/<pkg> golang ./t.amd64`. Rosetta reproduces the FMA class but emits the arm64-style NaN, so a NaN sign-bit failure shows only in amd64 CI.

## Open edges

- Matrices inside crosstab arms are refused (`PULSE_MATRIX_HOST_CONFLICT`); a matrix-only chain stage (no aggregator) is refused by `ChainRefusal`.
- The other mergeable reducers (the Welford family, other `MergeOnline` reducers) still merge per segment, equal only within ulps. Moving them onto the blocked tree hits the `w = 0` porting caveat above.
