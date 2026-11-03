// Package gonumorder is an FMA-free pure-Go port of the operation order
// gonum v0.17.0 (gonum.org/v1/gonum, BSD-3-Clause; see
// THIRD_PARTY_NOTICES.md) runs on linux/amd64, for the BLAS and LAPACK
// routines two callers reach:
//
//   - linalg's SPD path (FactorSPD, SPDFactor.Solve / Inverse /
//     ConditionNumber, Mul): mat.Cholesky (Factorize, Cond,
//     SolveVecTo, InverseTo) and mat.Dense.Mul;
//   - internal/sem's model arithmetic (MAT_SEM fits, statistics and
//     scores): mat.Dense.Mul over dense and symmetric operands (Dgemm,
//     Dsymm), mat.VecDense.MulVec (Dgemv, Dsymv), mat.Dense.Inverse
//     (Dlange, Dgetrf, Dgecon, Dgetri) and mat.Cholesky.LogDet (with
//     the amd64 assembly math.Log it calls, LogAMD64).
//
// Why a port, not a call. gonum's BLAS kernels are SSE2 assembly on
// amd64 (internal/asm/f64: DotUnitary, DotInc, AxpyUnitary, AxpyInc,
// ScalUnitary, ScalInc, GemvN, GemvT, Ger) and pure Go elsewhere; on
// arm64 the Go compiler contracts that pure Go into fused multiply-adds,
// and the multi-accumulator assembly sums in a different order besides.
// So the same factorisation, product or inverse differed between the two
// in the last ulps, and an iterative fit carried the difference into its
// stop. These routines reproduce the amd64 summation order exactly —
// including each assembly kernel's lane and accumulator assignment — and
// write every product that feeds an addition as an explicit float64(...)
// conversion, which the Go specification forbids fusing. Result: amd64
// computes the bits gonum always computed there (TestSPD_MatchesGonumOrder
// in linalg, TestGonumOrder_MatchesGonum here and TestSEM_MatOpsMatchGonum
// in internal/sem assert bit equality against gonum on amd64), and arm64
// computes the same bits (TestSPD_BitPinned, TestGonumOrder_BitPinned).
//
// Scope. Only the shapes the two callers reach are ported: row-major
// storage with a leading dimension, upper symmetric storage, positive
// increments, GemvN with a unit-stride x, and Dgemm without
// Trans·Trans. Each routine names the gonum routine (and, where it is
// assembly on amd64, the kernel) it mirrors. Never "simplify" a loop or
// drop a conversion here: the operation order IS the contract.
//
// The package is a leaf over the standard library only
// (TestGonumOrderImportBoundary), so linalg can import it and stay a
// leaf itself.
package gonumorder
