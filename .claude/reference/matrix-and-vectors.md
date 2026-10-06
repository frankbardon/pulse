# Matrix and vectors — the `linalg` core, its backend policy and the blocked merge (U15)

CLAUDE.md names `linalg` in the public package list and points here. This file is the long form of the numerical contract every matrix and vector feature builds on. **Load it before touching `linalg/`, a caller of it (synth's correlator, the regression SPD path), `internal/processing/block_merge.go`, the parallel-decode segment split, or any future `MAT_*` / vector surface.** Package layout and the import gates: `.claude/reference/architecture.md` (Public vs internal, Surface guards). Engine wiring of the blocked merge: `.claude/reference/execution-modes.md` (Blocked merge). Embedder-facing guide: `docs/src/library/linalg.md`.

U15 shipped no request slot, response slot, operator, manifest block or skill. The only user-visible addition is two error codes in the manifest error list. Synth and regression output did not move by one bit.

## Package surface

`linalg/` is public and frozen-additive (`TestPublicAPIGolden`). It holds Pulse-owned value types (`Matrix` dense row-major, `Sym` packed upper triangle, `Vec`) and three groups of routines:

| Group | Routines | Backend | Bit contract |
|---|---|---|---|
| Reference SPD kernels | `Cholesky`, `CholeskyRidge` (+ `RidgeSchedule`, `DefaultRidgeSchedule`), `SolveSPD`, `InverseSPD` | pure Go, FMA-free | identical bits on every architecture |
| Co-moments | `CoMoment` (`NewCoMoment`, `Add`, `Merge`, `Clone`, `N`, `W`, `NEff`, `NWeightInvalid`, `PairN`, `PairW`, `Mean`, `Cov`, `Corr`; modes `Listwise` / `Pairwise`), `MergeBlockSize`, `MergeTree` | pure Go, FMA-free | identical bits on every architecture |
| Decompositions | `SymEigen`, `SVD`, `QR`, `Rank`, `ConditionNumber` | gonum, canonicalised | decisions (order, sign, rank) portable; values may differ in the last ulps across architectures |
| Gonum SPD path | `FactorSPD` → `SPDFactor` (`Solve`, `Inverse`, `ConditionNumber`, `N`), `Mul`, `ConditionTolerance` | thin gonum wrappers | equal to the raw gonum call bit for bit on ONE machine; not across architectures |

No gonum type appears on the exported surface: gonum is pre-1.0 and the surface is frozen. `TestLinalgSurfaceNamesNoGonum` (`internal/apigolden`) asserts the golden's `linalg` section carries no `gonum` token; `TestLinalgImportBoundary` (`linalg/boundary_test.go`) keeps the package a leaf over the standard library, gonum and Pulse's `errors`.

## Backend policy — why two backends

A routine is a reference kernel when something downstream has a BIT contract on it; otherwise gonum backs it.

- **Synth seed reproducibility.** Same seed, same bytes, on every machine. Synth's correlator factors a correlation matrix and draws `u = L·z`, so any drift in `L` reaches every correlated value. Synth's hand-rolled Cholesky was already arch-independent, and it had to stay that way.
- **Merge determinism.** The blocked merge tree promises serial == parallel bitwise. That holds only if `Add` and `Merge` produce the same bits wherever they run.
- **Regression was already arch-dependent.** It factored through gonum's `mat.Cholesky` before U15, and gonum has amd64 assembly. Routing it through `FactorSPD` / `SPDFactor` / `Mul` keeps gonum underneath, which is why its output is byte-identical to the pre-U15 build on the same machine. The internal regression types stayed on gonum where converting would add a rounding.

**Why a gonum-backed Cholesky could not replace synth's.** A pre-implementation study (2026-10-05, gonum v0.17.0, darwin/arm64) swapped synth's factor loop for gonum's `Factorize` and compared bitwise:

- **Different operation order, in at least three places.** gonum accumulates a dot product and then subtracts it (`Ddot`), where synth subtracts one term at a time. gonum scales a row by a reciprocal (`Dscal(1/ajj)`), where synth divides. Above an order of about 64, gonum switches to a blocked `Dsyrk` / `Dgemm` / `Dtrsm` path. Its amd64 assembly also unrolls with multiple accumulators.
- **Results.**
  - 96% of 7,800 random SPD matrices (order 2–40) differed in at least one bit of `L`, typically by a few ulps; the largest relative difference was 1.1e-8, on tiny off-diagonals.
  - On exactly rank-deficient unit-diagonal inputs, 21% of the factor-or-fail decisions differed. That changes the ridge retry count and the reported accumulated ridge.
- **The existing suite passed anyway.** All 58 packages stayed green with the swap, because nothing pinned the factor bitwise. The bitwise pins U15 added close that hole: `TestCholesky_BitIdenticalToSynthReference` and `TestCholeskyRidge_BitIdenticalToSynthReference` (`linalg/cholesky_reference_test.go`), plus the synth draw pin.

**FMA-free means:** every product that feeds an addition or subtraction is written `float64(a*b)`. The Go specification forbids fusing an explicitly converted product into a fused multiply-add, and arm64 and amd64 otherwise make different fusion choices. Never remove one of these conversions. Never "simplify" a reference kernel's loop order either: the operation order IS the contract.

**Reference Cholesky operation order.** Lower-triangular Banachiewicz (row by row). For `j ≤ i`, start from `S[i][j]` and subtract `L[i][k]·L[j][k]` for k ascending, one term at a time. Then take the square root on the diagonal, or do a true division by `L[j][j]` off it. The factor fails iff a diagonal pivot is ≤ 0. A NaN pivot is NOT ≤ 0, so it propagates into `L` exactly as synth's original comparison did; the gonum `FactorSPD` refuses it instead.

**Ridge schedule.** `CholeskyRidge` retries with `DefaultRidgeSchedule()`: eight increments `10^(t-6)` for t = 0..7 (1e-6 … 1e1), each added to the diagonal on top of the last. It returns the ACCUMULATED ridge, 0 when the first attempt succeeds. When every attempt fails, the error carries the schedule total of 11.111111 together with `attempts`. Synth surfaces a positive ridge as its existing warning and still raises `SERVICE_VALIDATION` on exhaustion; the linalg code does not leak through.

## Tolerances, order and sign

- **`Epsilon`** = 2⁻⁵². **`RankTolerance(rows, cols, sigmaMax)`** = `max(rows, cols)·Epsilon·σ_max`. This is the ONE rank tolerance, the LAPACK / NumPy `matrix_rank` default. A singular value at or below it is zero. `Rank(m, tol)` uses it unless `tol > 0`; `ConditionNumber` is `+Inf` for a matrix that is rank-deficient under it.
- **Order.** Eigenvalues and singular values are descending. Values within the rank tolerance of their neighbour form a tie cluster, ordered by the index of each vector's dominant component, lowest first, so ties keep the original variable order.
- **Sign.** Every eigenvector, and every right singular vector, is flipped so that its largest-magnitude component is positive. `U` and `V` flip together. A magnitude tie, within `DominanceTolerance` = 2⁻²⁶ relative, goes to the lowest index. `QR` flips so that `R`'s diagonal is non-negative. Without this rule gonum may return either sign, and goldens flake across platforms.
- **`ConditionTolerance`** = 1e16, gonum's own threshold. An `SPDFactor` whose condition estimate exceeds it still factors, but its `Solve` and `Inverse` refuse.
- **Co-moment merge tolerance.** A merged split is not bit-identical to one serial pass, because float addition is not associative. It agrees within 1e-10 relative to scale (means, covariances, correlations, `W`, `NEff`). Counts are exact. Bit identity across worker counts comes from the blocked tree below, not from `Merge` alone.

## Errors

Both codes are `*errors.CodedError`, owned `shared`, and listed in the manifest. Each carries a `codeMetadata` Message and fixups in `errors/fixup_metadata.go`.

- **`PULSE_MATRIX_SHAPE_MISMATCH`** is raised for operands whose dimensions disagree: non-square, ragged, a vector length different from the matrix order, data length ≠ rows×cols, a nil operand, or two `CoMoment`s of different `P` or mode merged. Details name the dimensions.
- **`PULSE_MATRIX_SINGULAR`** is raised when a matrix cannot be factored, solved against, inverted or decomposed. Details vary by routine:

| Routine | Details |
|---|---|
| `Cholesky` / `SolveSPD` / `InverseSPD` | `pivot` |
| `CholeskyRidge` exhausted | `attempts`, `ridge` |
| `FactorSPD` | `reason` = `not_positive_definite`, `n` |
| `SPDFactor.Solve` / `Inverse` | `reason` = `ill_conditioned` + `condition_number` (`+Inf` on breakdown), or `reason` = `backend_error` |
| gonum decompositions | `reason` = `non_finite` (a NaN / ±Inf element) or `no_convergence` |

No routine populates `rank` today (U16 or a later unit decides whether to). Callers that own a domain code keep it: synth still raises `SERVICE_VALIDATION`, and regression keeps its rank-deficient codes and `gonum_error` detail text.

## `CoMoment` weight and missing semantics

- **Update.** Weighted Welford–West: the first row with mass sets the mean exactly, so a constant column has variance exactly 0. **Merge** uses the exact Chan–Golub–LeVeque combine. `Merge` mutates the receiver; `MergeTree` mutates nothing.
- **Missing values.** A NaN in `x` means missing. `Listwise` skips the whole row. `Pairwise` skips per pair: each pair keeps its own count, weight, means and co-moments. A row the mode admits nowhere is not counted, and its weight is never judged.
- **Weights.** Weights follow `.claude/reference/weighting.md` (Validation). A NaN, ±Inf or negative `w` skips the row and increments `NWeightInvalid`. **`w = 0` is valid: the row counts toward `N` (and `PairN`) but adds no mass** to `W`, the means or the co-moments. `NEff` is Kish's `(Σw)²/Σw²`.
- **Porting caveat.** The engine's weighted aggregators (`internal/processing/aggregator_weighted*.go`, the weighted Welford path) treat `w == 0` as NOT contributing: such rows are skipped before the row count. Porting an existing weighted reducer onto `CoMoment` therefore moves its row count on zero-weight rows. Decide that change deliberately, and gate it, rather than inheriting it.
- **Outputs.** `Cov(ddof)` is `M2/(W − ddof)`, NaN where `W − ddof ≤ 0`; probability-weight corrections are the caller's job. `Corr` is clamped to [−1, 1]. A variable with zero spread has NaN for every entry touching it, its own diagonal included, never 0. `Mean` of a variable with no mass is NaN.
- **Concurrency.** A `CoMoment` is not safe for concurrent use: give each worker its own and merge them.

## Blocked merge-tree contract

The contract is that one answer has the same bits under every concurrency knob.

- **Blocks.** Fold the row at ABSOLUTE record index `r` into block `r / MergeBlockSize` (4096), never into "this worker's partial". `MergeBlockSize` is part of the bit contract: changing it changes merged bits.
- **Tree.** `MergeTree(blocks)` merges level-wise by position, `(b0·b1)`, `(b2·b3)`, …, carrying an odd tail up unmerged. Five blocks merge as `((b0·b1)·(b2·b3))·b4`. The shape depends only on the sequence length, and `Merge` is FMA-free, so the root is a pure function of the block sequence.
- **Engine.** Parallel-decode segment boundaries snap to multiples of 4096 records, so no block straddles two workers. `DecodeCallbackFactory` receives the segment's absolute start record. Every record source stamps `(shard, record)` on the `Record`.
- **Opt-in.** The opt-in is the internal interface `processing.BlockMerger`. Its state is per-block `CoMoment`s; partitions combine by disjoint union, and `Finalize` runs a two-level tree: `MergeTree` over each shard's blocks, then over the per-shard roots in shard order. Every other reducer keeps its existing merge path unchanged. `BlockMerger` is NOT reachable through `extend`, so extensions cannot opt in yet. Full wiring: `.claude/reference/execution-modes.md` (Blocked merge).
- **Guarantee.** Serial == any `DecodeWorkers` == any `ShardWorkers` count, bitwise, on every architecture. A one-shard archive equals its single-file twin bitwise.
- **Caveat.** A MULTI-shard archive and the single file holding the same rows cut blocks at different places, because block numbering restarts per shard. They therefore agree to the merge tolerance but may differ in the last bits. Do not write a test that demands bit equality across that pair.
- **Unstamped records are refused.** A row built by a join (`copyStateInto`) or by a ProcessChain intermediate stage carries no position. A `BlockMerger` refuses such a row with `PROCESSING_INTERNAL` rather than guessing.
- **Gate U16 inherits.** `TestCoMomentMergeTree_WorkerInvariant` (`internal/service/comoment_merge_tree_test.go`) drives a `_test.go`-only CoMoment reducer through the real dispatch:
  - serial vs `DecodeWorkers` 2/3/7/8, on cohorts that end mid-block and on block boundaries;
  - serial vs `ShardWorkers` 2/3/8 on an 8-shard archive;
  - listwise and pairwise, each weighted and unweighted.

  The first `MAT_*` operator must pass the same invariance through its real registration.

## Virtual vectors — `Request.Vectors` (U16)

`Request.Vectors []types.VectorSpec` (`{name, fields | pattern, labels?, coerce?}`, additive `omitempty`, gated by `capability:matrices`) names a battery of numeric columns once per request. **The one resolver is `internal/vectors`** — a no-execute leaf (stdlib + `encoding` / `errors` / `types`) that predict, the runtime and projection all call, so the three cannot disagree. Consumers (the matrix slot and its operators) read members through it: `vectors.Resolve(req.Vectors, schema)` → `[]Resolved{Name, Members, Labels, Coerce}` index-aligned with the specs; `vectors.Find` by name; `vectors.ResolveSpec(at, spec, schema)` for an inline field list (an operator slot's own `fields`, with its own path in `at`).

- **Shape.** `name` non-empty and unique per request; exactly one of `fields` / `pattern`; `coerce` ∈ `types.AllVectorCoerces()` (`binary`). Violations are `PULSE_VECTOR_INVALID` (`details.reason` = `empty_name` / `fields_and_pattern` / `no_fields_or_pattern` / `unknown_coerce` / `empty_field_entry` / `bad_glob` / `bad_pattern`); a repeated name is `PULSE_VECTOR_DUPLICATE` (`indices`).
- **Order (axis order).** A `fields` entry without `*` `?` `[` is a literal and keeps the caller's position; a glob entry (`path.Match`, whole name) expands IN PLACE to its matches in schema order; `pattern` is a Go regexp, UNANCHORED, matches in schema order. A member repeated after expansion is `PULSE_VECTOR_DUPLICATE` (`field`); no member at all is `PULSE_VECTOR_EMPTY`. A literal the schema lacks is the field-reference refusal `SERVICE_VALIDATION` (`field`, `vector`, `slot` = `vectors[i].fields[j]`).
- **Member types.** u4 / u8 / u16 / u32 / u64 / f32 / f64; `packed_bool` only under `coerce: "binary"`; categorical, `set_*`, `date`, `datetime`, `decimal128` refused permanently — `PULSE_VECTOR_MEMBER_TYPE` naming `field` + `field_type` (`vectors.MemberTypeAllowed`).
- **Labels.** One per member or `PULSE_VECTOR_LABELS_MISMATCH` (`labels`, `members`, `resolved`); absent → the member names.
- **Order of checks.** Spec by spec in request order; per spec: name, duplicate name, shape, coerce, expansion, empty, member types, labels. First failure wins.
- **Where it runs.** Step 6 of the field-reference walk (`internal/descriptor/field_refs.go`), so the runtime (`Service.checkFieldRefs`, every execution mode, after zones), predict and the Compose / chain validators refuse identically, against the schema the request executes over (joined or chain-stage schema included). Projection: `processing.NeededFields` adds `vectors.Members` (an unresolvable vector widens). Predict echoes `PredictResult.ResolvedVectors` `{name: [members]}` (omitted when none or refused).
- **Unreferenced warning.** `vectors.Referenced(req)` is the ONE reference set; a vector outside it is `PULSE_VECTOR_UNREFERENCED` — a predict warning and a `Response.Warnings` entry (`Service.process`, after the multiplicity fold). Nothing references a vector until the matrix slot lands; that slot MUST add its references to `Referenced`.
- **Hashing.** `Request.Hash()` is schema-free and hashes vectors AS WRITTEN; a vector-free request hashes byte-identically to the pre-slot form (`TestRequestHash_VectorFreeByteIdentical`). Resolved-member identity is `vectors.Normalize(req, schema)`: a shallow copy whose vectors carry their resolved `fields` (pattern cleared), so a pattern and its equivalent explicit list hash alike (`TestNormalize_HashesResolvedMembers`). No runtime site hashes a normalized request yet.
- **Codes.** All `PULSE_VECTOR_*` are owned by `capability:matrices` (a hidden capability refuses the slot first). `PULSE_VECTOR_UNKNOWN` is reserved for an operator slot naming an undefined vector (`vector`, `slot`, `defined`).

## Open edges (handed on)

- **U16 decisions.**
  - What `MAT_*` does on join and chain paths, where records are unstamped (stamp them, or refuse the request at predict).
  - Whether `PULSE_MATRIX_SINGULAR` should gain `details.rank` / `details.condition_number` on the reference Cholesky and `FactorSPD` paths.
  - The remaining `PULSE_MATRIX_*` codes (for example `PULSE_MATRIX_NOT_PSD`).
- **Mergeable reducers.** Existing mergeable reducers (the Welford family and the other `MergeOnline` reducers) still use the per-segment `MergeOnline` path, which is equal only within ulps. Migrating them onto the blocked tree is open, and it hits the `w = 0` caveat above.
- **Extension opt-in.** Exposing `BlockMerger` through `extend` is open.
