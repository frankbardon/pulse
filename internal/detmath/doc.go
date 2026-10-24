// Package detmath holds deterministic, architecture-independent ports of
// the Go standard library's transcendental kernels: Exp, Log, Log1p, Sin,
// Cos, Tan, Atan2 and Pow.
//
// # Why it exists
//
// The standard library's results for these functions depend on the
// machine, not just the input:
//
//   - math.Exp is assembly on amd64 and arm64, and the amd64 routine
//     picks an FMA path at run time when the CPU supports it
//     (exp_amd64.go: useFMA), so two amd64 machines can disagree.
//   - math.Log is assembly on amd64 and pure Go on arm64.
//   - The pure-Go Sin / Cos / Tan / Atan / Pow are compiled with
//     fused multiply-add contraction on arm64 and without it on amd64.
//
// Each function here is a line-for-line port of the PURE-GO path of the
// Go 1.26 source named in its doc comment, with one change: every
// floating-point product is wrapped in an explicit float64(...)
// conversion. The Go spec allows `a + b*c` to be contracted into a single
// fused multiply-add, possibly across statements, and an explicit
// conversion is the only construct that forbids it. With every product
// barriered the compiler emits the same sequence of individually rounded
// IEEE-754 operations on every architecture, so a result's bits are a
// function of the input alone. math.FMA is the wrong lever: it forces
// fusion everywhere, which is portable but changes amd64's values.
//
// math.Sqrt, math.Abs, math.Copysign, math.Frexp, math.Ldexp, math.Modf
// and the classification helpers are called directly: IEEE-754 requires
// Sqrt to be correctly rounded, and the rest are exact bit manipulation
// (Ldexp's subnormal scaling is a single multiply that feeds no add).
//
// Every special case (NaN, ±Inf, ±0, subnormals, overflow, underflow)
// follows math.*'s documented semantics. On linux/amd64, where the
// compiler never contracts, Log1p / Sin / Cos / Tan / Atan2 are bit-for-bit
// math.*; elsewhere results sit within the stdlib's own cross-build
// spread: 1 ulp, 2 for Exp (amd64's FMA assembly) and Tan (arm64
// contraction), 8 for Pow (it compounds Exp∘Log). Two amd64 assembly
// defects are not shared: math.Log of a subnormal returns about -709.09,
// and math.Exp overflows to +Inf from about 709.44 instead of 709.78. The gate is TestDetmath_GoldenBits (exact
// uint64 pins that must hold on amd64 and arm64 alike) plus
// TestDetmath_NoFMAContraction, which cross-compiles the package for
// arm64 with -d=fmahash=vy and fails on any contraction the compiler
// reports.
//
// The ported code is Copyright The Go Authors and is used under the BSD
// license reproduced in THIRD_PARTY_NOTICES.md at the repository root.
package detmath
