// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in THIRD_PARTY_NOTICES.md (section
// "The Go standard library") at the repository root.
//
// Ported from Go 1.26 src/math/atanh.go (the pure-Go atanh path) with
// every floating-point product wrapped in float64(...) and the logarithm
// routed through this package's Log1p rather than math.Log1p. See doc.go.

package detmath

import "math"

// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_atanh.c
// and came with this notice. The go code is a simplified
// version of the original C.
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
//
// __ieee754_atanh(x)
// Method :
//	1. Reduce x to positive by atanh(-x) = -atanh(x)
//	2. For x>=0.5
//	            1              2x                          x
//	atanh(x) = --- * log(1 + -------) = 0.5 * log1p(2 * --------)
//	            2             1 - x                      1 - x
//
//	For x<0.5
//	atanh(x) = 0.5*log1p(2x+2x*x/(1-x))
//
// Special cases:
//	atanh(x) is NaN if |x| > 1 with signal;
//	atanh(NaN) is that NaN with no signal;
//	atanh(+-1) is +-INF with signal.

// Atanh returns the inverse hyperbolic tangent of x, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go atanh in Go 1.26 src/math/atanh.go (FreeBSD msun
// e_atanh.c): atanh(x) = 0.5·Log1p(2x + 2x·x/(1-x)) for |x| < 0.5 and
// 0.5·Log1p(2x/(1-x)) above, odd-reflected, on this package's Log1p.
// Special cases are the same as math.Atanh:
//
//	Atanh(1) = +Inf
//	Atanh(±0) = ±0
//	Atanh(-1) = -Inf
//	Atanh(x) = NaN if x < -1 or x > 1
//	Atanh(NaN) = NaN
//
// On linux/amd64, where the compiler never contracts, it is bit-for-bit
// math.Atanh; elsewhere within 1 ulp (arm64 contracts math.Log1p's
// polynomial).
func Atanh(x float64) float64 {
	const NearZero = 1.0 / (1 << 28) // 2**-28
	// special cases
	switch {
	case x < -1 || x > 1 || math.IsNaN(x):
		return math.NaN()
	case x == 1:
		return math.Inf(1)
	case x == -1:
		return math.Inf(-1)
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var temp float64
	switch {
	case x < NearZero:
		temp = x
	case x < 0.5:
		temp = x + x
		temp = float64(0.5 * Log1p(temp+float64(temp*x)/(1-x)))
	default:
		temp = float64(0.5 * Log1p((x+x)/(1-x)))
	}
	if sign {
		temp = -temp
	}
	return temp
}
