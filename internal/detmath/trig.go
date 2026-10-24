// Copyright 2011 The Go Authors (sin.go, tan.go) and 2018 (trig_reduce.go).
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in THIRD_PARTY_NOTICES.md (section
// "The Go standard library") at the repository root.
//
// Ported from Go 1.26 src/math/sin.go, src/math/tan.go and
// src/math/trig_reduce.go (the pure-Go paths) with every floating-point
// product wrapped in float64(...) so it cannot be contracted into a
// fused multiply-add. See doc.go.

package detmath

import (
	"math"
	"math/bits"
)

// The original C code, the long comment, and the constants below are
// from http://netlib.sandia.gov/cephes/cmath/sin.c, available from
// http://www.netlib.org/cephes/cmath.tgz.
// The go code is a simplified version of the original C.
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na.net

// sin coefficients
var _sin = [...]float64{
	1.58962301576546568060e-10, // 0x3de5d8fd1fd19ccd
	-2.50507477628578072866e-8, // 0xbe5ae5e5a9291f5d
	2.75573136213857245213e-6,  // 0x3ec71de3567d48a1
	-1.98412698295895385996e-4, // 0xbf2a01a019bfdf03
	8.33333333332211858878e-3,  // 0x3f8111111110f7d0
	-1.66666666666666307295e-1, // 0xbfc5555555555548
}

// cos coefficients
var _cos = [...]float64{
	-1.13585365213876817300e-11, // 0xbda8fa49a0861a9b
	2.08757008419747316778e-9,   // 0x3e21ee9d7b4e3f05
	-2.75573141792967388112e-7,  // 0xbe927e4f7eac4bc6
	2.48015872888517045348e-5,   // 0x3efa01a019c844f5
	-1.38888888888730564116e-3,  // 0xbf56c16c16c14f91
	4.16666666666665929218e-2,   // 0x3fa555555555554b
}

// tan coefficients
var _tanP = [...]float64{
	-1.30936939181383777646e4, // 0xc0c992d8d24f3f38
	1.15351664838587416140e6,  // 0x413199eca5fc9ddd
	-1.79565251976484877988e7, // 0xc1711fead3299176
}
var _tanQ = [...]float64{
	1.00000000000000000000e0,
	1.36812963470692954678e4,  // 0x40cab8a5eeb36572
	-1.32089234440210967447e6, // 0xc13427bc582abc96
	2.50083801823357915839e7,  // 0x4177d98fc2ead8ef
	-5.38695755929454629881e7, // 0xc189afe03cbe5a31
}

// Pi/4 split into three parts for extended-precision Cody-Waite
// reduction.
const (
	pi4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000
	pi4B = 3.77489470793079817668e-8  // 0x3e64442d00000000
	pi4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170
)

// sinPoly evaluates the sine kernel z + z·zz·S(zz) on the reduced
// argument, in the exact operation order of the stdlib expression.
func sinPoly(z, zz float64) float64 {
	p := float64(_sin[0]*zz) + _sin[1]
	p = float64(p*zz) + _sin[2]
	p = float64(p*zz) + _sin[3]
	p = float64(p*zz) + _sin[4]
	p = float64(p*zz) + _sin[5]
	return z + float64(float64(z*zz)*p)
}

// cosPoly evaluates the cosine kernel 1 - zz/2 + zz²·C(zz) on the
// reduced argument, in the exact operation order of the stdlib
// expression.
func cosPoly(zz float64) float64 {
	p := float64(_cos[0]*zz) + _cos[1]
	p = float64(p*zz) + _cos[2]
	p = float64(p*zz) + _cos[3]
	p = float64(p*zz) + _cos[4]
	p = float64(p*zz) + _cos[5]
	return (1.0 - float64(0.5*zz)) + float64(float64(zz*zz)*p)
}

// reduceOctant maps x ≥ 0 to its octant j and reduced argument z.
// tanMode skips the j &= 7 step, as the stdlib tan does.
func reduceOctant(x float64, tanMode bool) (uint64, float64) {
	if x >= reduceThreshold {
		return trigReduce(x)
	}
	j := uint64(float64(x * (4 / math.Pi))) // integer part of x/(Pi/4), as integer for tests on the phase angle
	y := float64(j)                         // integer part of x/(Pi/4), as float

	// map zeros to origin
	if j&1 == 1 {
		j++
		y++
	}
	if !tanMode {
		j &= 7 // octant modulo 2Pi radians (360 degrees)
	}
	z := ((x - float64(y*pi4A)) - float64(y*pi4B)) - float64(y*pi4C) // Extended precision modular arithmetic
	return j, z
}

// Cos returns the cosine of the radian argument x, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go cos in Go 1.26 src/math/sin.go. Special cases are
// the same as math.Cos:
//
//	Cos(±Inf) = NaN
//	Cos(NaN) = NaN
func Cos(x float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsInf(x, 0):
		return math.NaN()
	}

	// make argument positive
	sign := false
	x = math.Abs(x)

	j, z := reduceOctant(x, false)

	// reflect in x axis
	if j > 3 {
		j -= 4
		sign = !sign
	}
	if j > 1 {
		sign = !sign
	}

	zz := float64(z * z)
	var y float64
	if j == 1 || j == 2 {
		y = sinPoly(z, zz)
	} else {
		y = cosPoly(zz)
	}
	if sign {
		y = -y
	}
	return y
}

// Sin returns the sine of the radian argument x, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go sin in Go 1.26 src/math/sin.go. Special cases are
// the same as math.Sin:
//
//	Sin(±0) = ±0
//	Sin(±Inf) = NaN
//	Sin(NaN) = NaN
func Sin(x float64) float64 {
	switch {
	case x == 0 || math.IsNaN(x):
		return x // return ±0 || NaN()
	case math.IsInf(x, 0):
		return math.NaN()
	}

	// make argument positive but save the sign
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	j, z := reduceOctant(x, false)

	// reflect in x axis
	if j > 3 {
		sign = !sign
		j -= 4
	}
	zz := float64(z * z)
	var y float64
	if j == 1 || j == 2 {
		y = cosPoly(zz)
	} else {
		y = sinPoly(z, zz)
	}
	if sign {
		y = -y
	}
	return y
}

// Tan returns the tangent of the radian argument x, with bits that are
// identical on every architecture and CPU.
//
// Port of the pure-Go tan in Go 1.26 src/math/tan.go. Special cases are
// the same as math.Tan:
//
//	Tan(±0) = ±0
//	Tan(±Inf) = NaN
//	Tan(NaN) = NaN
func Tan(x float64) float64 {
	switch {
	case x == 0 || math.IsNaN(x):
		return x // return ±0 || NaN()
	case math.IsInf(x, 0):
		return math.NaN()
	}

	// make argument positive but save the sign
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	j, z := reduceOctant(x, true)
	zz := float64(z * z)

	var y float64
	if zz > 1e-14 {
		n := float64(_tanP[0]*zz) + _tanP[1]
		n = float64(n*zz) + _tanP[2]
		d := zz + _tanQ[1]
		d = float64(d*zz) + _tanQ[2]
		d = float64(d*zz) + _tanQ[3]
		d = float64(d*zz) + _tanQ[4]
		y = z + float64(z*(float64(zz*n)/d))
	} else {
		y = z
	}
	if j&2 == 2 {
		y = -1 / y
	}
	if sign {
		y = -y
	}
	return y
}

// reduceThreshold is the maximum value of x where the reduction using
// Pi/4 in 3 float64 parts still gives accurate results. This threshold
// is set by y*C being representable as a float64 without error where y
// is given by y = floor(x * (4 / Pi)) and C is the leading partial
// term of 4/Pi. Since the leading term (PI4A) has 30 significant bits,
// y should have <= 23 bits for y*C to be exactly representable,
// so y < 1<<29.
const reduceThreshold = 1 << 29

// Bit layout constants of an IEEE-754 float64, as in src/math/bits.go.
const (
	fShift = 64 - 11 - 1
	fMask  = 0x7FF
	fBias  = 1023
)

// trigReduce implements Payne-Hanek range reduction by Pi/4 for
// x > 0. It returns the integer part mod 8 (j) and the fractional
// part (z) of x / (Pi/4).
//
// Port of src/math/trig_reduce.go. The implementation is based on:
// "ARGUMENT REDUCTION FOR HUGE ARGUMENTS: Good to the Last Bit"
// K. C. Ng et al, March 24, 1992
// The simulated multi-precision calculation of x*B uses 64-bit integer
// arithmetic, so it is already exact; only the final scale by Pi/4 is
// floating point.
func trigReduce(x float64) (j uint64, z float64) {
	const PI4 = math.Pi / 4
	if x < PI4 {
		return 0, x
	}
	// Extract out the integer and exponent such that,
	// x = ix * 2 ** exp.
	ix := math.Float64bits(x)
	exp := int(ix>>fShift&fMask) - fBias - fShift
	ix &^= fMask << fShift
	ix |= 1 << fShift
	// Use the exponent to extract the 3 appropriate uint64 digits from
	// mPi4, B ~ (z0, z1, z2), such that the product leading digit has
	// the exponent -61. Note, exp >= -53 since x >= PI4 and
	// exp < 971 for maximum float64.
	digit, bitshift := uint(exp+61)/64, uint(exp+61)%64
	z0 := (mPi4[digit] << bitshift) | (mPi4[digit+1] >> (64 - bitshift))
	z1 := (mPi4[digit+1] << bitshift) | (mPi4[digit+2] >> (64 - bitshift))
	z2 := (mPi4[digit+2] << bitshift) | (mPi4[digit+3] >> (64 - bitshift))
	// Multiply mantissa by the digits and extract the upper two digits
	// (hi, lo).
	z2hi, _ := bits.Mul64(z2, ix)
	z1hi, z1lo := bits.Mul64(z1, ix)
	z0lo := z0 * ix
	lo, c := bits.Add64(z1lo, z2hi, 0)
	hi, _ := bits.Add64(z0lo, z1hi, c)
	// The top 3 bits of hi give j.
	j = hi >> 61
	// Extract the fraction and find its magnitude.
	hi = hi<<3 | lo>>61
	lz := uint(bits.LeadingZeros64(hi))
	e := uint64(fBias - (lz + 1))
	// Clear implicit mantissa bit and shift into place.
	hi = (hi << (lz + 1)) | (lo >> (64 - (lz + 1)))
	hi >>= 64 - fShift
	// Include the exponent and convert to a float.
	hi |= e << fShift
	z = math.Float64frombits(hi)
	// Map zeros to origin.
	if j&1 == 1 {
		j++
		j &= 7
		z--
	}
	// Multiply the fractional part by pi/4.
	return j, float64(z * PI4)
}

// mPi4 is the binary digits of 4/pi as a uint64 array,
// that is, 4/pi = Sum mPi4[i]*2^(-64*i)
// 19 64-bit digits and the leading one bit give 1217 bits
// of precision to handle the largest possible float64 exponent.
var mPi4 = [...]uint64{
	0x0000000000000001,
	0x45f306dc9c882a53,
	0xf84eafa3ea69bb81,
	0xb6c52b3278872083,
	0xfca2c757bd778ac3,
	0x6e48dc74849ba5c0,
	0x0c925dd413a32439,
	0xfc3bd63962534e7d,
	0xd1046bea5d768909,
	0xd338e04d68befc82,
	0x7323ac7306a673e9,
	0x3908bf177bf25076,
	0x3ff12fffbc0b301f,
	0xde5e2316b414da3e,
	0xda6cfd9e4f96136e,
	0x9e8c7ecd3cbfd45a,
	0xea4f758fd7cbe2f6,
	0x7a0e73ef14a525d4,
	0xd7f6bf623f1aba10,
	0xac06608df8f6d757,
}
