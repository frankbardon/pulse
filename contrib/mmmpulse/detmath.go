// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in GO_LICENSE.txt in this directory.
//
// detmath.go is a copy of Pulse's internal/detmath Exp, Log and Pow:
// line-for-line ports of the pure-Go paths of Go 1.26 src/math/exp.go,
// log.go and pow.go with every floating-point product wrapped in
// float64(...) so it cannot be contracted into a fused multiply-add,
// and with Pow's Exp / Log calls routed to the ports. The standard
// library's math.Exp and math.Log are assembly on amd64 / arm64 (and
// amd64 picks an FMA path at run time), so their bits depend on the
// machine; these depend on the input alone, which keeps a REG_MMM_HILL
// fit bit-identical across architectures. A pack cannot import Pulse's
// internal packages, hence the copy; TestMMMPack_DetmathGoldenBits pins
// it to the same bits as internal/detmath's own golden.

package mmmpulse

import "math"

// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_exp.c
// and came with this notice. The go code is a simplified
// version of the original C.
//
// ====================================================
// Copyright (C) 2004 by Sun Microsystems, Inc. All rights reserved.
//
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================

// detExp returns e**x, the base-e exponential of x, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go exp in Go 1.26 src/math/exp.go. Special cases are
// the same as math.Exp:
//
//	detExp(+Inf) = +Inf
//	detExp(NaN) = NaN
//
// Very large values overflow to 0 or +Inf.
// Very small values underflow to 1.
func detExp(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10
		Log2e = 1.44269504088896338700e+00

		Overflow  = 7.09782712893383973096e+02
		Underflow = -7.45133219101941108420e+02
		NearZero  = 1.0 / (1 << 28) // 2**-28
	)

	// special cases
	switch {
	case math.IsNaN(x):
		return x
	case x > Overflow: // handles case where x is +∞
		return math.Inf(1)
	case x < Underflow: // handles case where x is -∞
		return 0
	case -NearZero < x && x < NearZero:
		return 1 + x
	}

	// reduce; computed as r = hi - lo for extra precision.
	var k int
	switch {
	case x < 0:
		k = int(float64(Log2e*x) - 0.5)
	case x > 0:
		k = int(float64(Log2e*x) + 0.5)
	}
	hi := x - float64(float64(k)*Ln2Hi)
	lo := float64(float64(k) * Ln2Lo)

	// compute
	return detExpmulti(hi, lo, k)
}

// detExpmulti returns e**r × 2**k where r = hi - lo and |r| ≤ ln(2)/2.
func detExpmulti(hi, lo float64, k int) float64 {
	const (
		P1 = 1.66666666666666657415e-01  /* 0x3FC55555; 0x55555555 */
		P2 = -2.77777777770155933842e-03 /* 0xBF66C16C; 0x16BEBD93 */
		P3 = 6.61375632143793436117e-05  /* 0x3F11566A; 0xAF25DE2C */
		P4 = -1.65339022054652515390e-06 /* 0xBEBBBD41; 0xC5D26BF1 */
		P5 = 4.13813679705723846039e-08  /* 0x3E663769; 0x72BEA4D0 */
	)

	r := hi - lo
	t := float64(r * r)
	c := r - float64(t*(P1+float64(t*(P2+float64(t*(P3+float64(t*(P4+float64(t*P5)))))))))
	y := 1 - ((lo - float64(r*c)/(2-c)) - hi)
	return math.Ldexp(y, k)
}

// The original C code, the long comment, and the constants
// below are from FreeBSD's /usr/src/lib/msun/src/e_log.c
// and came with this notice. The go code is a simpler
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

// detLog returns the natural logarithm of x, with bits that are identical
// on every architecture and CPU.
//
// Port of the pure-Go log in Go 1.26 src/math/log.go. Special cases are
// the same as math.Log:
//
//	detLog(+Inf) = +Inf
//	detLog(0) = -Inf
//	detLog(x < 0) = NaN
//	detLog(NaN) = NaN
func detLog(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01 /* 3fe62e42 fee00000 */
		Ln2Lo = 1.90821492927058770002e-10 /* 3dea39ef 35793c76 */
		L1    = 6.666666666666735130e-01   /* 3FE55555 55555593 */
		L2    = 3.999999999940941908e-01   /* 3FD99999 9997FA04 */
		L3    = 2.857142874366239149e-01   /* 3FD24924 94229359 */
		L4    = 2.222219843214978396e-01   /* 3FCC71C5 1D8E78AF */
		L5    = 1.818357216161805012e-01   /* 3FC74664 96CB03DE */
		L6    = 1.531383769920937332e-01   /* 3FC39A09 D078C69F */
		L7    = 1.479819860511658591e-01   /* 3FC2F112 DF3E5244 */
	)

	// special cases
	switch {
	case math.IsNaN(x) || math.IsInf(x, 1):
		return x
	case x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	}

	// reduce
	f1, ki := math.Frexp(x)
	if f1 < math.Sqrt2/2 {
		f1 = float64(f1 * 2)
		ki--
	}
	f := f1 - 1
	k := float64(ki)

	// compute
	s := f / (2 + f)
	s2 := float64(s * s)
	s4 := float64(s2 * s2)
	t1 := float64(s2 * (L1 + float64(s4*(L3+float64(s4*(L5+float64(s4*L7)))))))
	t2 := float64(s4 * (L2 + float64(s4*(L4+float64(s4*L6)))))
	R := t1 + t2
	hfsq := float64(float64(0.5*f) * f)
	return float64(k*Ln2Hi) - ((hfsq - (float64(s*(hfsq+R)) + float64(k*Ln2Lo))) - f)
}

// detIsOddInt reports whether x is an odd integer.
func detIsOddInt(x float64) bool {
	if math.Abs(x) >= (1 << 53) {
		// 1 << 53 is the largest exact integer in the float64 format.
		// Any number outside this range will be truncated before the
		// decimal point and therefore will always be an even integer.
		return false
	}

	xi, xf := math.Modf(x)
	return xf == 0 && int64(xi)&1 == 1
}

// detPow returns x**y, the base-x exponential of y, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go pow in Go 1.26 src/math/pow.go; the fractional
// part of y goes through this package's Exp and Log rather than
// math.Exp / math.Log, which are assembly on amd64 / arm64. Special
// cases are the same as math.Pow (in order):
//
//	detPow(x, ±0) = 1 for any x
//	detPow(1, y) = 1 for any y
//	detPow(x, 1) = x for any x
//	detPow(NaN, y) = NaN
//	detPow(x, NaN) = NaN
//	detPow(±0, y) = ±Inf for y an odd integer < 0
//	detPow(±0, -Inf) = +Inf
//	detPow(±0, +Inf) = +0
//	detPow(±0, y) = +Inf for finite y < 0 and not an odd integer
//	detPow(±0, y) = ±0 for y an odd integer > 0
//	detPow(±0, y) = +0 for finite y > 0 and not an odd integer
//	detPow(-1, ±Inf) = 1
//	detPow(x, +Inf) = +Inf for |x| > 1
//	detPow(x, -Inf) = +0 for |x| > 1
//	detPow(x, +Inf) = +0 for |x| < 1
//	detPow(x, -Inf) = +Inf for |x| < 1
//	detPow(+Inf, y) = +Inf for y > 0
//	detPow(+Inf, y) = +0 for y < 0
//	detPow(-Inf, y) = detPow(-0, -y)
//	detPow(x, y) = NaN for finite x < 0 and finite non-integer y
func detPow(x, y float64) float64 {
	switch {
	case y == 0 || x == 1:
		return 1
	case y == 1:
		return x
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case x == 0:
		switch {
		case y < 0:
			if math.Signbit(x) && detIsOddInt(y) {
				return math.Inf(-1)
			}
			return math.Inf(1)
		case y > 0:
			if math.Signbit(x) && detIsOddInt(y) {
				return x
			}
			return 0
		}
	case math.IsInf(y, 0):
		switch {
		case x == -1:
			return 1
		case (math.Abs(x) < 1) == math.IsInf(y, 1):
			return 0
		default:
			return math.Inf(1)
		}
	case math.IsInf(x, 0):
		if math.IsInf(x, -1) {
			return detPow(1/x, -y) // detPow(-0, -y)
		}
		switch {
		case y < 0:
			return 0
		case y > 0:
			return math.Inf(1)
		}
	case y == 0.5:
		return math.Sqrt(x)
	case y == -0.5:
		return 1 / math.Sqrt(x)
	}

	yi, yf := math.Modf(math.Abs(y))
	if yf != 0 && x < 0 {
		return math.NaN()
	}
	if yi >= 1<<63 {
		// yi is a large even int that will lead to overflow (or underflow to 0)
		// for all x except -1 (x == 1 was handled earlier)
		switch {
		case x == -1:
			return 1
		case (math.Abs(x) < 1) == (y > 0):
			return 0
		default:
			return math.Inf(1)
		}
	}

	// ans = a1 * 2**ae (= 1 for now).
	a1 := 1.0
	ae := 0

	// ans *= x**yf
	if yf != 0 {
		if yf > 0.5 {
			yf--
			yi++
		}
		a1 = detExp(float64(yf * detLog(x)))
	}

	// ans *= x**yi
	//   by repeated squaring and multiplying by bits of yi.
	//   accumulate powers of two into exp.
	x1, xe := math.Frexp(x)
	for i := int64(yi); i != 0; i >>= 1 {
		if xe < -1<<12 || 1<<12 < xe {
			// catastrophic overflow - let Ldexp handle it
			ae += xe
			break
		}
		if i&1 == 1 {
			a1 = float64(a1 * x1)
			ae += xe
		}
		x1 = float64(x1 * x1)
		xe <<= 1
		if x1 < .5 {
			x1 += x1
			xe--
		}
	}

	// ans = a1 * 2**ae
	// if y < 0 { ans = 1 / ans }
	// but in the opposite order
	if y < 0 {
		a1 = 1 / a1
		ae = -ae
	}
	return math.Ldexp(a1, ae)
}
