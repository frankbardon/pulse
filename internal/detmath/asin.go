// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in THIRD_PARTY_NOTICES.md (section
// "The Go standard library") at the repository root.
//
// Ported from Go 1.26 src/math/asin.go (the pure-Go asin path, over the
// pure-Go satan in atan.go) with every floating-point product wrapped in
// float64(...) so it cannot be contracted into a fused multiply-add. See
// doc.go.

package detmath

import "math"

// Asin returns the arcsine, in radians, of x, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go asin in Go 1.26 src/math/asin.go. Special cases
// are the same as math.Asin:
//
//	Asin(±0) = ±0
//	Asin(x) = NaN if x < -1 or x > 1
func Asin(x float64) float64 {
	if x == 0 {
		return x // special case
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if x > 1 {
		return math.NaN() // special case
	}

	temp := math.Sqrt(1 - float64(x*x))
	if x > 0.7 {
		temp = math.Pi/2 - satan(temp/x)
	} else {
		temp = satan(x / temp)
	}

	if sign {
		temp = -temp
	}
	return temp
}
