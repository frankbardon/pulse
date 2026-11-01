# Linear Algebra (`linalg`)

`github.com/frankbardon/pulse/linalg` is Pulse's linear-algebra core. Pulse's own synthetic-data generator and regression engine use it, and embedders can import it directly. It is a leaf package: it imports only the standard library, gonum and Pulse's `errors`. It exposes no gonum type, so a gonum upgrade can never break your code.

```go
import "github.com/frankbardon/pulse/linalg"
```

## Types

| Type | Shape | Build with |
|---|---|---|
| `Matrix` | dense, row-major | `NewMatrix(rows, cols, data)`, `NewMatrixFromRows([][]float64)` |
| `Sym` | symmetric; stores the upper triangle, packed | `NewSym(n, packed)`, `NewSymFromRows([][]float64)` (reads the lower triangle) |
| `Vec` | vector | `NewVec([]float64)` |

Accessors: `At`, `Set`, `Rows` / `Cols` / `N` / `Len`, `ToRows`, `RowMajor`, `Slice`. Constructors copy their input, and a malformed shape returns an error, never a panic. An out-of-range `At` / `Set` panics like a slice index.

## Two backends: which routine to pick

| Need | Routine | Same bits on every architecture? |
|---|---|---|
| Factor, solve or invert a symmetric positive-definite matrix, with reproducible output | `Cholesky`, `CholeskyRidge`, `SolveSPD`, `InverseSPD` | **yes**: pure Go, FMA-free |
| Solve a general (non-symmetric or indefinite) square system, with reproducible output | `Solve`, `FactorLU` → `LUFactor.Solve` / `SolveMatrix` / `Inverse` / `ConditionNumber` | **yes** for the values: pure Go, FMA-free (the singularity decision uses the rank tolerance) |
| Running means and covariances, mergeable | `CoMoment`, `MergeTree` | **yes**: pure Go, FMA-free |
| Factor once, then solve many right-hand sides, with a condition check | `FactorSPD` → `SPDFactor.Solve` / `Inverse` / `ConditionNumber`; `Mul` | **yes**: pure Go, FMA-free, in gonum's amd64 operation order (equal to gonum bit for bit on amd64) |
| Eigen, singular value, QR, rank, condition number | `SymEigen`, `SVD`, `QR`, `Rank`, `ConditionNumber` | values may differ in the last ulps; order, sign and rank decisions are fixed |

The reference routines and the SPD path write every product that feeds an addition as an explicit `float64(a*b)` conversion, which stops the compiler from fusing it into a fused multiply-add. That is why arm64 and amd64 agree bit for bit. The SPD path keeps the operation order of the gonum calls it replaced (LAPACK `Dpotrf` / `Dpocon` / `Dpotrs` / `Dpotri` and gonum's amd64 assembly kernels), so its factor, solve, inverse, condition estimate and refusals are exactly what gonum gave on amd64; the reference `Cholesky` is a different algorithm order and gives different last bits. Use them when "same input, same seed, same bytes" matters to you.

## Cholesky with a ridge

```go
s, _ := linalg.NewSymFromRows([][]float64{{1, 0.9}, {0.9, 1}})
L, err := linalg.Cholesky(s) // lower triangular, s = L·Lᵀ

// Nearly singular? Retry with a growing diagonal ridge.
L, ridge, err := linalg.CholeskyRidge(s, nil) // nil = DefaultRidgeSchedule()
if ridge > 0 {
	// s was not positive definite; ridge is the TOTAL added to the diagonal
}
```

`DefaultRidgeSchedule()` makes eight attempts. Before each retry it adds the next increment, `1e-6`, `1e-5`, …, `1e1`. The returned ridge is the sum of the increments actually added. Pass your own `RidgeSchedule` to change the steps.

## General solve (LU)

```go
a, _ := linalg.NewMatrixFromRows([][]float64{{1, -0.5, 0}, {-0.5, 1, -0.5}, {0, -0.5, 1}})
x, err := linalg.Solve(a, linalg.NewVec([]float64{1, 1, 1})) // x = (3, 4, 3)

// Many right-hand sides: factor once.
f, err := linalg.FactorLU(a)
X, err := f.SolveMatrix(B) // column j of X solves a·x = column j of B
inv, err := f.Inverse()
k := f.ConditionNumber()   // 2-norm condition number, always finite
```

`FactorLU` is LU with partial pivoting (P·A = L·U, the largest pivot in each column, ties to the lowest row). It refuses a matrix that is rank-deficient under the rank tolerance below (exactly where `ConditionNumber` is `+Inf`) with `PULSE_MATRIX_SINGULAR`, so you get an error, never a `NaN` solution. A full-rank but ill-conditioned matrix still solves; expect to lose about log10(κ) digits, and check `f.ConditionNumber()` if you need a stricter gate. A solve meets the round-trip tolerance ‖A·x − b‖∞ ≤ 8·n·`Epsilon`·(‖A‖∞·‖x‖∞ + ‖b‖∞). For a symmetric positive-definite matrix, `SolveSPD` is the cheaper reference route.

## Eigen, SVD, QR and rank

```go
eig, err := linalg.SymEigen(s)  // eig.Values (descending), eig.Vectors (columns)
sv, err := linalg.SVD(m)        // sv.U, sv.Values, sv.V
qr, err := linalg.QR(m)         // qr.Q, qr.R
r, err := linalg.Rank(m, 0)     // 0 = the default tolerance
k, err := linalg.ConditionNumber(m)
```

gonum's raw output is canonicalised so every decision reads the same everywhere:

- **Order.** Values are descending. Values tied within the rank tolerance keep the original variable order.
- **Sign.** Each eigenvector and right singular vector is flipped so its largest-magnitude component is positive. A tie within `DominanceTolerance` goes to the lowest index. `QR` makes `R`'s diagonal non-negative.
- **Rank.** One tolerance, `RankTolerance(rows, cols, σmax) = max(rows, cols)·Epsilon·σmax`, the same default as LAPACK and NumPy. `ConditionNumber` is `+Inf` when the matrix is rank-deficient under it.

## Co-moments

`CoMoment` accumulates weighted means and the covariance matrix of `p` variables in one pass, and merges exactly.

```go
acc, _ := linalg.NewCoMoment(3, linalg.Listwise) // or linalg.Pairwise
for _, row := range rows {
	acc.Add(row.x, row.w) // len(row.x) == 3
}
cov := acc.Cov(1)  // sample covariance (frequency weights)
corr := acc.Corr() // C/√(M2_x·M2_y), TEST_PEARSON_R's arithmetic
mean := acc.Mean()
```

- **Missing values.** A `NaN` in `x` is missing. `Listwise` drops the row. `Pairwise` drops it only for the pairs it touches; `PairN(i, j)` / `PairW(i, j)` / `PairW2(i, j)` report each pair's count, weight sum and squared-weight sum, `PairNMass(i, j)` the pair's rows with a positive weight (`PairN` less its weight-0 rows), and `PairNEff(i, j)` its Kish effective sample size `PairW²/PairW2`. Under `Listwise` they equal `N()`, `W()`, the global positive-weight count, the global Σw² and `NEff()` for every pair.
- **Raw co-moment.** `PairM2(i, j)` returns the pair's weighted co-moment Σw(xi−mi)(xj−mj) over the rows the pair admits — the numerator `Cov` divides, so `Cov(ddof)` at `(i, j)` equals `PairM2(i, j) / (PairW(i, j) − ddof)` exactly wherever that denominator is positive. `PairM2(i, i)` is variable `i`'s own Σw(xi−mi)². It is symmetric, 0 for a pair with no mass, and an extension matrix operator (`extend.MatrixOperator`) reads it to build a statistic `Cov` / `Corr` do not cover.
- **Weights.**
  - A `NaN`, `±Inf` or negative weight skips the row and is counted in `NWeightInvalid()`.
  - A weight of `0` is valid: the row counts in `N()` but carries no mass.
  - `W()` is the weight sum, and `NEff()` is Kish's effective sample size. Use weight `1` for unweighted data.
- **Undefined figures.** These are `NaN`, never 0:
  - a covariance entry whose `W − ddof ≤ 0`;
  - every correlation touching a constant variable;
  - the mean of a variable with no mass.
- **Concurrency.** A `CoMoment` is not safe for concurrent use. Give each goroutine its own accumulator and `Merge` them.
- **Capture and restore.** `acc.ListwiseState()` returns a `Listwise` accumulator's complete stored state (`ListwiseState`: the counts, `W`, `W2`, the raw mean and the raw co-moment `M2` as a `*Sym`), and `linalg.NewCoMomentFromListwise(state)` rebuilds it. Every output, and every later `Add` or `Merge`, is bit-identical to the original's, so a fit can be captured once and scored elsewhere without storing its derived blocks. A `Pairwise` accumulator has no single state; a malformed state is `PULSE_MATRIX_SHAPE_MISMATCH`.

### Merging with identical bits regardless of worker count

`a.Merge(b)` is exact algebra, but floating-point addition is not associative. So a different split of the same rows agrees only to about 1e-10 relative. To get the *same bits* however the work was divided:

1. Fold the row at absolute index `r` into a per-block accumulator for block `r / linalg.MergeBlockSize` (4096 rows). Each block's rows must be added in order.
2. Combine the blocks, ordered by block index, with `linalg.MergeTree(blocks)`. It uses a fixed binary tree, so the result depends only on the block sequence.

Pulse's engine uses exactly this scheme internally, so its parallel and serial runs agree bit for bit. An extension matrix operator (`extend.MatrixOperator`, see [Vectors and Matrices](matrices.md#your-own-matrix-operator)) never merges anything itself: its `Finalize` receives the co-moment this tree produced, so a pure `Finalize` returns the same bits under every `DecodeWorkers` / `ShardWorkers` count and shard split (gate `TestExtensionMatrixInvariance_{Archive,Heavy}`).

## Distance and similarity metrics (`linalg/metric`)

`github.com/frankbardon/pulse/linalg/metric` is the one registry of distance and similarity metrics. Every metric Pulse computes between two vectors is defined there once, and every surface that takes a metric name resolves it through the registry. `pulse manifest --json` lists it under `matrix.metrics`.

```go
import "github.com/frankbardon/pulse/linalg/metric"

m, err := metric.Resolve("cosine", metric.Similarity) // or metric.Distance: 1 - cos, clamped to [0, 2]
s, err := m.Eval(metric.Similarity, a, b, nil)
```

- **The 15 metrics.** Plain: `euclidean`, `sq_euclidean`, `manhattan`, `chebyshev`, `cosine`, `correlation`, `hellinger`, `aitchison`. Set / binary (0 / 1 values, or `SetCounts` through `EvalSets`): `jaccard`, `dice`, `hamming`, `overlap`. Fitted: `std_euclidean`, `mahalanobis`, `chi_square`. A fitted metric never estimates anything: you pass the SD vector, the inverse covariance (`*linalg.Sym`) or the column masses in `metric.Stats`.
- **Direction.** Each metric declares its native direction and the forms it can answer in. Asking for a form it lacks (for example `euclidean` as a similarity) is refused with `PULSE_VECTOR_METRIC_UNKNOWN`; nothing is converted silently. `cosine` is the one metric with both forms. An empty name is `PULSE_VECTOR_METRIC_REQUIRED`.
- **Missing values.** `NaN` marks a missing element and is dropped pairwise. The element-sum distances (`euclidean`, `sq_euclidean`, `manhattan`, `std_euclidean`, `hamming`) are rescaled by `p / p_observed`, as R's `stats::dist()` does. No complete pair gives `NaN`. `mahalanobis` needs every element. Vectors of unequal length are a `*metric.LengthMismatchError`.
- **Domains.** `hellinger`, `aitchison` and `chi_square` read non-negative values; an `aitchison` zero part is undefined. A violation returns `NaN` with a `*metric.DomainError`, so you can null the row and count it.
- **Kinds and consumers.** `KindsSuited`, `PoorFit` and `Consumers` (`metric.Accepted`, `metric.ResolveFor`) say which vector kinds a metric suits and which surfaces accept it. `chi_square`'s one consumer is `OVERLAY_CORRESPONDENCE` (`metric.ConsumerCorrespondence`): its `row_distance` / `column_distance` parts are the pairwise chi-square profile distances over the analysis's own masses.
- **Kind defaults.** `metric.Default(kind, form)` is the metric a vector of that kind defaults to (distance: measure `std_euclidean`, scale `euclidean`, composition `aitchison`, binary `hamming`; similarity: scale `correlation`, composition `cosine`, binary `jaccard`, none for measure). `metric.DefaultFor(consumer, kind, form, admit)` is that default when the consumer accepts it, else the first metric the consumer accepts that suits the kind and is no poor fit. A request's `vectors[i].kind` (`types.VectorKind`) drives the same table: it fills an operator's missing `metric` (smart defaults) and flags an explicit poor fit with `PULSE_VECTOR_METRIC_UNSUITED`. The manifest lists the table under `matrix.vector_kinds`.

Arithmetic is FMA-free and left to right, and `aitchison` takes its logarithm from `linalg/detmath`, so every metric gives identical bits on every architecture.

## Deterministic math (`linalg/detmath`)

`github.com/frankbardon/pulse/linalg/detmath` holds the transcendental functions whose bits are the same on every architecture: `Exp`, `Expm1`, `Log`, `Log1p`, `Pow`, `Sin`, `Cos`, `Tan`, `Atan2`, `Asin`, `Atanh`, `Tanh`, `Erf`, `Erfc`, `Erfinv` and `Lgamma`, plus the ziggurat variates `NormFloat64` / `ExpFloat64`, which consume a `*rand.Rand` (`math/rand/v2`) exactly as its own methods do. The standard library's versions depend on the machine. `math.Exp` and `math.Log` are assembly, and amd64 picks an FMA path at run time. arm64 also fuses multiply-adds in the pure-Go kernels.

```go
import "github.com/frankbardon/pulse/linalg/detmath"

w := detmath.Exp(-detmath.Pow(lag/scale, shape)) // same bits on arm64 and amd64
lg, sign := detmath.Lgamma(x)
```

Each function is a line-for-line port of the Go 1.26 pure-Go source with every product wrapped in `float64(...)`. `Tanh` is the exception: it ports fdlibm's `s_tanh.c` on `Expm1`, because the stdlib's version calls `math.Exp`. `Erfc` computes the complementary function directly, not as `1 - Erf`, so its upper tail keeps full relative precision down to subnormal results (`Erfc(27)` is about `5.2e-319`, where `1 - Erf(27)` is `0`). `Atanh` is `0.5·Log1p(…)` on this package's `Log1p`. Special cases (`NaN`, `±Inf`, `±0`, subnormals) follow `math.*`. Results sit within the stdlib's own cross-build spread: 1 ulp for most functions, 2 for `Exp`, `Tan`, `Tanh` and `Asin`, 3 for `Lgamma`, 4 for `Erfc`, and 8 for `Pow` and `Erfinv`. Pulse's expression functions, `FEAT_LOG`, `WIN_DISTRIBUTED_LAG` weights, the Student-t p-values, quantiles and confidence bounds, and `optimize` all run on it. Use it in an extension operator or model pack whose output must match across architectures. Also wrap every product that feeds an addition in `float64(...)` in your own code.

### What is covered across architectures

"Covered" means a figure has the same float64 bits on arm64 and amd64 for the same input. Pulse gets there by computing every transcendental with `detmath` and writing every product that feeds an addition as `float64(a*b)`. Where a gonum routine sat on a result path, Pulse replaced it with a port that has no fused multiply-adds.

**Covered:**

- **`detmath` kernels**, including `Erfc`, `Atanh` and `Tanh`.
- **Pulse's distribution helpers:** the normal quantile, the chi-square CDF and survival function (incomplete gamma), and the Student-t chain. GLM links use a portable port of amd64's FMA-path `math.Exp` (see Not covered for why).
- **Every `TEST_*` operator**, parametric and nonparametric, including the Fisher-exact, Tukey and Shapiro–Wilk tails.
- **Aggregators, windows, features, attributes and overlays**, including the Welford family, weighted inference and the `OVERLAY_FISHER_EXACT_CELL` cell test.
- **Matrix operators' own arithmetic:** co-moments, `MAT_CORRELATION` and its inference, the `MAT_CORRELATION` determinant, partial correlation, distance metrics (`aitchison` included), and the ridge schedule. The factor-analysis ML and rotation steps are FMA-free too, but they start from a gonum eigen decomposition, so `MAT_FACTOR` figures are listed under Not covered.
- **The `linalg` SPD path:** `FactorSPD` / `SPDFactor` / `Mul`. This is a port of gonum v0.17.0's amd64 operation order with no fused multiply-adds. On amd64 it gives exactly the bits gonum gave; arm64 now gives the same bits.
- **Regression:** OLS, GLM, ridge, lasso / elastic net, Bayes-linear, HC and cluster standard errors, and Newey–West (HAC). The automatic HAC lag is an exact integer.
- **`MAT_SEM`'s own arithmetic:** the distribution replacements, the `:=` defined-parameter derivatives, the polychoric and WLSMV weight blocks.
- **The shared MMM fixture** (`internal/mmmfixture`).

**How it is enforced:**

- **Two repo-wide gates in `internal/testutil/fmagate`.** They scan every non-test package except `cmd/` and `internal/cli`, and fail on any site that the reviewed allowlist `testdata/numeric-allowlist.txt` does not excuse with a reason. They also fail on a stale allowlist entry.
  - `TestFMAReport_RepoWide` (heavy tier) finds products the compiler fuses.
  - `TestBannedMathReport_RepoWide` (fast tier) finds banned stdlib transcendentals.
- **Per-package `*_NoFMAContraction` gates** in `detmath`, `statdist`, `optimize`, `mcmc`, `sem`, `distlagparams`, `regmmm` and `mmmfixture`.
- **A fingerprint golden.** `internal/fingerprint` pins the exact bits of covered figures, end to end and from direct calls, in the normal test tier. The golden is generated on arm64, and CI's linux/amd64 run checks it.
- **A tolerance golden.** `TestDecomposition_Tolerance` pins the figures that are not covered (below) at 1e-12 relative.

**Not covered** (accepted residuals; the arm64 vs amd64 spreads below were measured):

- **gonum's eigen, SVD and QR internals.** gonum's amd64 assembly and its arm64 pure Go add in a different order, and no barrier changes that. Ordering, sign and rank decisions are fixed (see Eigen, SVD, QR and rank above), but the values can differ in their last ulps. The same applies to every figure computed from those values: `MAT_PCA`, `MAT_FACTOR`, `TEST_MANOVA`, `MAT_COLLINEARITY`, `ATTR_PC_SCORE` / `ATTR_FACTOR_SCORE` and `OVERLAY_CORRESPONDENCE`. They are pinned at 1e-12 relative, not bit for bit. The scan cannot see fused multiply-adds inside gonum code.
- **`MAT_SEM` fits.** Every fit runs gonum matrix calls (`Mul`, `Inverse`, `LogDet`, Cholesky), and the optimiser amplifies their last-ulp differences:
  - A fit that stops early (default SLSQP `ftol` 1e-6) differs by about 7.9e-4 relative. With `ftol` 1e-15 the gap is still 2.9e-8 on a two-factor CFA.
  - A `fisher` fit can stop on the objective-decrease (`small`) test. It reports `converged: true` on both architectures, but after a different number of iterations (18 vs 20), and its estimates differ by about 3.8e-9. `converged: true` reached this way means the parameters are accurate to about √eps only.
  - A `fisher` + rank-2 fit can flip its `converged` flag, with 44 vs 43 iterations.
  - Other spreads: `meanstructure` about 1.3e-2; multi-group fits about 8e-8; `WLSMV` about 3.5e-9; bootstrap figures near zero up to about 1 relative.
  - A path model that runs the same number of iterations on both architectures differs by 1.3e-12 on z. Only fully converged fits are pinned (at 1e-12).
- **The stdlib `math.Exp` on amd64 depends on the CPU.** It picks an FMA or a non-FMA code path at run time. Pulse's GLM links use `ExpAMD64FMA`, a portable port that pins the FMA-hardware bits. Any remaining stdlib `Exp` would follow the CI runner's CPU.
- **Allowlisted sites.** These are the offline example-CSV generator (`internal/examples/fixtures/gen.go`, never compiled into Pulse; every figure reads the committed CSV) and the test helper `invariance.Within`, whose tolerance bound is never a result figure.

## Errors

Every failure is an `*errors.CodedError`. Look up its prose with `pulse errors lookup CODE`.

- **`PULSE_MATRIX_SHAPE_MISMATCH`**: dimensions that do not fit, a nil operand, or merging accumulators of different size or mode. A wrong-length `x` passed to `CoMoment.Add` panics instead.
- **`PULSE_MATRIX_SINGULAR`**: the matrix cannot be factored, solved, inverted or decomposed. `details.reason` says why where the routine knows:
  - `not_positive_definite` or `ill_conditioned` (with `condition_number`);
  - `non_finite` (a `NaN` / `Inf` element in a decomposition);
  - `no_convergence`.

  A refused `FactorLU` / `Solve` carries `reason` = `singular`, `n`, `rank` and `condition_number` (`+Inf`), plus `pivot` for an exactly zero pivot. A reference Cholesky failure carries `pivot`; an exhausted ridge schedule carries `attempts` and `ridge`.
  A failed `Cholesky` (so `SolveSPD` / `InverseSPD`) or `FactorSPD` on a finite matrix also carries `rank`, `condition_number` (`+Inf`, `null` on the wire, when rank-deficient) and, for a rank-deficient one, `dependent_indices`: the axis indices in the linear dependency. A matrix operator names them as `dependent_fields`.
