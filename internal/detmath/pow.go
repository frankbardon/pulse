// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in THIRD_PARTY_NOTICES.md (section
// "The Go standard library") at the repository root.
//
// Ported from Go 1.26 src/math/pow.go (the pure-Go pow path) with every
// floating-point product wrapped in float64(...) so it cannot be
// contracted into a fused multiply-add, and with its Exp / Log calls
// routed to this package's deterministic kernels. See doc.go.

package detmath

import "math"

// isOddInt reports whether x is an odd integer.
func isOddInt(x float64) bool {
	if math.Abs(x) >= (1 << 53) {
		// 1 << 53 is the largest exact integer in the float64 format.
		// Any number outside this range will be truncated before the
		// decimal point and therefore will always be an even integer.
		return false
	}

	xi, xf := math.Modf(x)
	return xf == 0 && int64(xi)&1 == 1
}

// Pow returns x**y, the base-x exponential of y, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go pow in Go 1.26 src/math/pow.go; the fractional
// part of y goes through this package's Exp and Log rather than
// math.Exp / math.Log, which are assembly on amd64 / arm64. Special
// cases are the same as math.Pow (in order):
//
//	Pow(x, ±0) = 1 for any x
//	Pow(1, y) = 1 for any y
//	Pow(x, 1) = x for any x
//	Pow(NaN, y) = NaN
//	Pow(x, NaN) = NaN
//	Pow(±0, y) = ±Inf for y an odd integer < 0
//	Pow(±0, -Inf) = +Inf
//	Pow(±0, +Inf) = +0
//	Pow(±0, y) = +Inf for finite y < 0 and not an odd integer
//	Pow(±0, y) = ±0 for y an odd integer > 0
//	Pow(±0, y) = +0 for finite y > 0 and not an odd integer
//	Pow(-1, ±Inf) = 1
//	Pow(x, +Inf) = +Inf for |x| > 1
//	Pow(x, -Inf) = +0 for |x| > 1
//	Pow(x, +Inf) = +0 for |x| < 1
//	Pow(x, -Inf) = +Inf for |x| < 1
//	Pow(+Inf, y) = +Inf for y > 0
//	Pow(+Inf, y) = +0 for y < 0
//	Pow(-Inf, y) = Pow(-0, -y)
//	Pow(x, y) = NaN for finite x < 0 and finite non-integer y
func Pow(x, y float64) float64 {
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
			if math.Signbit(x) && isOddInt(y) {
				return math.Inf(-1)
			}
			return math.Inf(1)
		case y > 0:
			if math.Signbit(x) && isOddInt(y) {
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
			return Pow(1/x, -y) // Pow(-0, -y)
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
		a1 = Exp(float64(yf * Log(x)))
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
