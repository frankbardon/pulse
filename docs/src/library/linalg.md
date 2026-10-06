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
| Running means and covariances, mergeable | `CoMoment`, `MergeTree` | **yes**: pure Go, FMA-free |
| Factor once, then solve many right-hand sides, with a condition check | `FactorSPD` → `SPDFactor.Solve` / `Inverse` / `ConditionNumber`; `Mul` | no; gonum-backed, but identical bits run to run on one machine |
| Eigen, singular value, QR, rank, condition number | `SymEigen`, `SVD`, `QR`, `Rank`, `ConditionNumber` | values may differ in the last ulps; order, sign and rank decisions are fixed |

The reference routines write every product that feeds an addition as an explicit `float64(a*b)` conversion, which stops the compiler from fusing it into a fused multiply-add. That is why arm64 and amd64 agree bit for bit. Use them when "same input, same seed, same bytes" matters to you.

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

- **Missing values.** A `NaN` in `x` is missing. `Listwise` drops the row. `Pairwise` drops it only for the pairs it touches; `PairN(i, j)` / `PairW(i, j)` report each pair's count and weight.
- **Weights.**
  - A `NaN`, `±Inf` or negative weight skips the row and is counted in `NWeightInvalid()`.
  - A weight of `0` is valid: the row counts in `N()` but carries no mass.
  - `W()` is the weight sum, and `NEff()` is Kish's effective sample size. Use weight `1` for unweighted data.
- **Undefined figures.** These are `NaN`, never 0:
  - a covariance entry whose `W − ddof ≤ 0`;
  - every correlation touching a constant variable;
  - the mean of a variable with no mass.
- **Concurrency.** A `CoMoment` is not safe for concurrent use. Give each goroutine its own accumulator and `Merge` them.

### Merging with identical bits regardless of worker count

`a.Merge(b)` is exact algebra, but floating-point addition is not associative. So a different split of the same rows agrees only to about 1e-10 relative. To get the *same bits* however the work was divided:

1. Fold the row at absolute index `r` into a per-block accumulator for block `r / linalg.MergeBlockSize` (4096 rows). Each block's rows must be added in order.
2. Combine the blocks, ordered by block index, with `linalg.MergeTree(blocks)`. It uses a fixed binary tree, so the result depends only on the block sequence.

Pulse's engine uses exactly this scheme internally, so its parallel and serial runs agree bit for bit.

## Errors

Every failure is an `*errors.CodedError`. Look up its prose with `pulse errors lookup CODE`.

- **`PULSE_MATRIX_SHAPE_MISMATCH`**: dimensions that do not fit, a nil operand, or merging accumulators of different size or mode. A wrong-length `x` passed to `CoMoment.Add` panics instead.
- **`PULSE_MATRIX_SINGULAR`**: the matrix cannot be factored, solved, inverted or decomposed. `details.reason` says why where the routine knows:
  - `not_positive_definite` or `ill_conditioned` (with `condition_number`);
  - `non_finite` (a `NaN` / `Inf` element in a decomposition);
  - `no_convergence`.

  A reference Cholesky failure carries `pivot`; an exhausted ridge schedule carries `attempts` and `ridge`.
