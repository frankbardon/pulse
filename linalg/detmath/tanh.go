// Ported from FreeBSD msun src/s_tanh.c (fdlibm 5.3) — the Go standard
// library's tanh is a different (Cephes) algorithm on math.Exp — with
// every floating-point product wrapped in float64(...) and the
// exponential routed through this package's Expm1. See doc.go.

package detmath

import "math"

// The original C code and the long comment below are from FreeBSD's
// /usr/src/lib/msun/src/s_tanh.c and came with this notice.
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// Tanh(x)
// Return the Hyperbolic Tangent of x
//
// Method :
//				       x    -x
//				      e  - e
//	0. tanh(x) is defined to be -----------
//				       x    -x
//				      e  + e
//	1. reduce x to non-negative by tanh(-x) = -tanh(x).
//	2.  0      <= x <  2**-55 : tanh(x) := x*(one+x)
//					        -t
//	    2**-55 <  x <=  1     : tanh(x) := -----; t = expm1(-2x)
//					       t + 2
//						     2
//	    1      <= x <=  22    : tanh(x) := 1 - -----; t = expm1(2x)
//						   t + 2
//	    22     <  x <= INF    : tanh(x) := 1.
//
// Special cases:
//	tanh(NaN) is NaN;
//	only tanh(0)=0 is exact for finite argument.

// Tanh returns the hyperbolic tangent of x, with bits that are identical
// on every architecture and CPU.
//
// Port of FreeBSD msun s_tanh.c (fdlibm): with z = |x|,
//
//	z < 2^-55:   tanh(x) = x·(1+x)
//	z < 1:       t = Expm1(-2z), tanh = -t/(t+2)
//	z < 22:      t = Expm1(2z),  tanh = 1 - 2/(t+2)
//	z ≥ 22:      tanh = 1
//
// with tanh(-x) = -tanh(x). Expm1 keeps relative precision as z → 0,
// where e^2z - 1 would cancel. Special cases are the same as math.Tanh:
//
//	Tanh(±0) = ±0
//	Tanh(±Inf) = ±1
//	Tanh(NaN) = NaN
//
// math.Tanh is the Cephes rational/exp formulation, so the two are
// independent roundings of the same function: they agree within 2 ulp
// on the sweep in stdlib_test.go.
func Tanh(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return math.NaN()
	case math.IsInf(x, 1):
		return 1
	case math.IsInf(x, -1):
		return -1
	}
	z := math.Abs(x)
	var t float64
	switch {
	case z < 0x1p-55: // tanh(small) = small; also ±0
		return float64(x * (1 + x))
	case z < 1:
		e := Expm1(float64(-2 * z))
		t = -e / (e + 2)
	case z < 22:
		t = 1 - 2/(Expm1(float64(2*z))+2)
	default: // |x| ≥ 22: 1 - tiny rounds to 1
		t = 1
	}
	if x < 0 {
		return -t
	}
	return t
}
