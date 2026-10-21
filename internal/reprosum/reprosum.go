// Package reprosum is Pulse's binned reproducible summation kernel: a
// fixed-size float64 accumulator whose result is bit-identical under any
// order, partition or merge tree of the same multiset of inputs.
//
// It is a Go port of the scalar double-precision "binned" kernels of
// ReproBLAS v2.1.0 (Ahrens, Demmel, Nguyen), fold K = 3, bin width W = 40.
// The state is K primary words plus K carry words (6 float64, 48 bytes),
// plus a pending-deposit counter, a fast-path cache derived from the top
// bin, and the non-finite flags (56 bytes total).
//
// # Provenance and verification
//
// Ported from, and verified line by line against, the ReproBLAS source
// (https://github.com/peterahrens/ReproBLAS, master tarball, "Reproducible
// BLAS v2.1.0"): include/binned.h (DBWIDTH = 40, binned_DBMAXINDEX = 51,
// binned_DBENDURANCE = 1 << (53-40-2) = 2048, binned_DMCOMPRESSION =
// 2^-14, binned_DMEXPANSION = 2^14), src/binned/dmbins.c (bin constants),
// dindex.c / dmindex.c / dmindex0.c (bin indices), dmdupdate.c (raise the
// bin index), dmddeposit.c (deposit, including the "| 1" low-bit trick
// that makes every slice a round-to-nearest with no ties), dmrenorm.c
// (carry renormalisation), dmdmadd.c (merge), ddmconv.c (final
// conversion), dbbound.c (the error bound) and src/binnedBLAS/dmdsum.ccog
// (renormalise before the deposit count would pass binned_DBENDURANCE).
// The papers (Demmel & Nguyen, "Parallel Reproducible Summation", IEEE TC
// 2015; Ahrens, Demmel & Nguyen, EECS-2016-121) were NOT re-read for this
// port: every rule below is taken from the source.
//
// Deviations from ReproBLAS, all deliberate:
//
//   - Non-finite inputs never enter the bins. They set sticky flags
//     (NaN, +Inf, -Inf) and Float64 canonicalises: NaN or (+Inf and -Inf)
//     gives math.NaN(), else the infinity's sign. ReproBLAS poisons the
//     primary word with an IEEE add, whose NaN payload is not portable.
//   - Zero inputs are skipped, so the state of {0} equals the empty state
//     and every all-zero (or all -0) sum is +0.
//   - Add renormalises lazily, before the deposit that would pass the
//     endurance (2048), exactly as binnedBLAS_dmdsum does per block,
//     instead of after every deposit as binned_dmdadd does. Merge and
//     Float64 renormalise both operands first, which is ReproBLAS's
//     "renormalise before using any other function" contract.
//   - Add takes an unrolled fold-3 fast path when x needs no bin raise
//     and the top bin is not the scaled one; it performs the same
//     operations as binned_dmddeposit in the same order.
//   - Every product carries an explicit float64(...) conversion, so Go
//     can never fuse it into an FMA (arm64, ppc64le, s390x) and amd64 and
//     arm64 produce identical bits.
//
// # Accuracy
//
// From dbbound.c, for N summands with max |x_j| = X and true sum S:
//
//	|T - S| < N * 2^((1-K)*W) * max(X, 2^-1023) + 7ε/(1 - 6√ε - 7ε) * |S|
//
// with ε = 2^-53, K = 3, W = 40, i.e. N * 2^-80 * X + ~7ε|S|. It is not
// correctly rounded. Below the deepest bin (grid 2^-1055) contributions
// are rounded away deterministically, so an all-subnormal sum may differ
// from its exact value by up to N * 2^-1056 (the bound's floor term is
// smaller than that; ReproBLAS shares the limit).
//
// # Licence
//
// The ported kernels are derived from ReproBLAS, distributed under the
// following licence, which this notice retains as it requires:
//
//	Software Development License
//
//	Copyright © 2016, University of California All rights reserved.
//
//	Redistribution and use in source and binary forms, with or without
//	modification, are permitted provided that the following conditions
//	are met:
//
//	* Redistributions of source code must retain the above copyright
//	notice, this list of conditions and the disclaimer below;
//
//	* Redistributions in binary form must reproduce the above copyright
//	notice, this list of conditions and the disclaimer below in the
//	documentation and/or other materials provided with the distribution.
//
//	* Neither the name of the University of California nor the names of
//	its contributors may be used to endorse or promote products derived
//	from this software without specific prior written permission.
//
//	DISCLAIMER
//
//	THIS SOFTWARE IS PROVIDED BY UNIVERSITY OF CALIFORNIA AS IS AND ANY
//	EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
//	IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR
//	PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL UNIVERSITY OF CALIFORNIA BE
//	LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
//	CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
//	SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR
//	BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
//	WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE
//	OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN
//	IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
//
// The same notice ships with every binary distribution in
// THIRD_PARTY_NOTICES.md (scripts/release-dist.sh packages it).
package reprosum

import "math"

const (
	// Fold is K, the number of bins kept (ReproBLAS's default fold).
	Fold = 3
	// BinWidth is W, the bin width in bits (DBWIDTH).
	BinWidth = 40
	// Endurance is the number of deposits a state absorbs between
	// renormalisations (binned_DBENDURANCE).
	Endurance = 1 << (mantDig - BinWidth - 2)

	mantDig  = 53
	maxExp   = 1024
	minExp   = -1021
	expBias  = maxExp - 2 // EXP_BIAS: biased exponent of frexp's exponent 0
	maxIndex = (maxExp-minExp+mantDig-1)/BinWidth - 1
	maxFold  = maxIndex + 1

	// compression / expansion scale the top bin (index 0), whose
	// constant would otherwise overflow.
	compression = 1.0 / (1 << (mantDig - BinWidth + 1))
	expansion   = 1 << (mantDig - BinWidth + 1)

	expMask  = 0x7ff
	expShift = mantDig - 1
	// topExp is the biased exponent of a primary word in bin 0.
	topExp = maxExp + expBias

	flagNaN    uint8 = 1
	flagPosInf uint8 = 2
	flagNegInf uint8 = 4
)

// bins[i] is the extraction constant of bin i (binned_dmbins): 0.75 *
// 2^(1038 - 40 i) for i in 1..maxIndex, the scaled 1.5 * 2^1023 for bin 0,
// and bins[maxIndex] repeated for the folds below the deepest bin.
var bins = func() (b [maxIndex + maxFold]float64) {
	b[0] = float64(2.0 * math.Ldexp(0.75, maxExp-1))
	i := 1
	for ; i <= maxIndex; i++ {
		b[i] = math.Ldexp(0.75, maxExp+mantDig-BinWidth+1-i*BinWidth)
	}
	for ; i < len(b); i++ {
		b[i] = b[i-1]
	}
	return b
}()

// Sum is a binned reproducible float64 accumulator. The zero value is an
// empty sum. A Sum is not safe for concurrent use; build one per worker and
// Merge them.
type Sum struct {
	pri     [Fold]float64
	car     [Fold]float64
	pending uint16 // deposits since the last renormalisation
	// fast is a cache derived from pri[0], never independent state: 0
	// when the sum is empty or its top bin is the scaled bin 0, else
	// 2047 - 40*k for top bin k — the inputs whose biased exponent is
	// below it need no bin raise and deposit on the unscaled path.
	fast  int16
	flags uint8
}

// Add deposits x.
func (s *Sum) Add(x float64) {
	e := int(math.Float64bits(x)>>expShift) & expMask
	if e >= int(s.fast) || s.pending >= Endurance {
		s.addSlow(e, x)
		return
	}
	// Fast path, binned_dmddeposit for fold 3 on an unscaled top bin:
	// each bin extracts its slice of x by a round-to-nearest add against
	// the bin constant and the remainder falls to the next bin; the last
	// bin takes the rounded rest. A zero (or -0) input deposits nothing.
	// Each word is stored as soon as it is formed: the next Add's load
	// of pri[0] must not wait on the lower folds.
	m := s.pri[0]
	q := orLow(x) + m
	s.pri[0] = q
	x += m - q
	m = s.pri[1]
	q = orLow(x) + m
	s.pri[1] = q
	x += m - q
	s.pri[2] += orLow(x)
	s.pending++
}

// addSlow is Add's general path: non-finite and zero inputs, a bin raise,
// a due renormalisation and the scaled top bin.
func (s *Sum) addSlow(e int, x float64) {
	if e == expMask {
		s.addNonFinite(x)
		return
	}
	if x == 0 {
		return
	}
	if s.pending >= Endurance {
		s.renorm()
	}
	s.update(e, x)
	s.deposit(x)
	s.pending++
}

// AddWeighted deposits w*x, rounded to float64 before the deposit (an FMA
// barrier: the product is never fused into the bin arithmetic).
func (s *Sum) AddWeighted(w, x float64) {
	s.Add(float64(w * x))
}

// Merge adds o into s. The result equals, bit for bit, the state reached by
// adding o's inputs to s one by one in any order.
func (s *Sum) Merge(o *Sum) {
	x := *o
	x.renorm()
	s.renorm()
	s.flags |= x.flags
	if x.pri[0] == 0 {
		return
	}
	if s.pri[0] == 0 {
		s.pri, s.car, s.fast = x.pri, x.car, x.fast
		return
	}
	xi := primaryIndex(x.pri[0])
	yi := primaryIndex(s.pri[0])
	shift := yi - xi
	if shift > 0 {
		// x holds the larger bins: shift s down and add x onto it.
		bb := bins[yi:]
		for i := Fold - 1; i >= shift; i-- {
			s.pri[i] = x.pri[i] + (s.pri[i-shift] - bb[i-shift])
			s.car[i] = x.car[i] + s.car[i-shift]
		}
		for i := 0; i < shift && i < Fold; i++ {
			s.pri[i] = x.pri[i]
			s.car[i] = x.car[i]
		}
	} else {
		bb := bins[xi:]
		for i := -shift; i < Fold; i++ {
			s.pri[i] += x.pri[i+shift] - bb[i+shift]
			s.car[i] += x.car[i+shift]
		}
	}
	s.setFast()
	s.renorm()
}

// Float64 returns the sum. It is a function of the multiset of inputs only:
// any order, partition or merge tree gives the same bits. An empty or
// all-zero sum is +0. NaN, or both infinities, give math.NaN(); one
// infinity gives that infinity; a finite sum past math.MaxFloat64 gives
// ±Inf.
func (s *Sum) Float64() float64 {
	if s.flags != 0 {
		switch {
		case s.flags&flagNaN != 0, s.flags&(flagPosInf|flagNegInf) == flagPosInf|flagNegInf:
			return math.NaN()
		case s.flags&flagPosInf != 0:
			return math.Inf(1)
		default:
			return math.Inf(-1)
		}
	}
	c := *s
	c.renorm()
	return c.convert()
}

func (s *Sum) addNonFinite(x float64) {
	switch {
	case x != x:
		s.flags |= flagNaN
	case x > 0:
		s.flags |= flagPosInf
	default:
		s.flags |= flagNegInf
	}
}

// valueIndex is binned_dindex for a finite non-zero x whose biased
// exponent is e.
func valueIndex(e int, x float64) int {
	if e == 0 {
		_, fe := math.Frexp(x)
		return min((maxExp-fe)/BinWidth, maxIndex)
	}
	return (maxExp + expBias - e) / BinWidth
}

// primaryIndex is binned_dmindex: the bin index of a non-zero primary word.
func primaryIndex(p float64) int {
	e := int(math.Float64bits(p)>>expShift) & expMask
	return (maxExp + mantDig - BinWidth + 1 + expBias - e) / BinWidth
}

// update is binned_dmdupdate: raise the state's bins so x's leading slice
// lands in bin 0 of the fold when x is larger than everything seen.
func (s *Sum) update(e int, x float64) {
	xi := valueIndex(e, x)
	if s.pri[0] == 0 {
		bb := bins[xi:]
		for i := range Fold {
			s.pri[i] = bb[i]
			s.car[i] = 0
		}
		s.setFast()
		return
	}
	shift := primaryIndex(s.pri[0]) - xi
	if shift <= 0 {
		return
	}
	i := Fold - 1
	for ; i >= shift; i-- {
		s.pri[i] = s.pri[i-shift]
		s.car[i] = s.car[i-shift]
	}
	bb := bins[xi:]
	for j := 0; j < i+1; j++ {
		s.pri[j] = bb[j]
		s.car[j] = 0
	}
	s.setFast()
}

// setFast recomputes the fast-path cache from pri[0].
func (s *Sum) setFast() {
	if s.pri[0] == 0 {
		s.fast = 0
		return
	}
	k := primaryIndex(s.pri[0])
	if k == 0 {
		s.fast = 0
		return
	}
	s.fast = int16(expMask - BinWidth*k)
}

// orLow sets the lowest mantissa bit of x so no deposit rounds on a tie
// (the "q.l |= 1" of binned_dmddeposit).
func orLow(x float64) float64 {
	return math.Float64frombits(math.Float64bits(x) | 1)
}

// deposit is binned_dmddeposit for fold 3: each bin extracts its slice of x
// by a round-to-nearest add against the bin constant, and the remainder
// falls to the next bin; the last bin takes the rounded rest.
func (s *Sum) deposit(x float64) {
	var m, q float64
	if int(math.Float64bits(s.pri[0])>>expShift)&expMask == topExp {
		m = s.pri[0]
		q = orLow(float64(x*compression)) + m
		s.pri[0] = q
		m -= q
		m = float64(m * (expansion * 0.5))
		x += m
		x += m
	} else {
		m = s.pri[0]
		q = orLow(x) + m
		s.pri[0] = q
		m -= q
		x += m
	}
	m = s.pri[1]
	q = orLow(x) + m
	s.pri[1] = q
	m -= q
	x += m
	s.pri[2] += orLow(x)
}

// renorm is binned_dmrenorm: move each primary word back into
// [1.5, 1.75) * ufp, carrying whole quarter-ufp units into the carry word.
// The value is unchanged; afterwards (pri, car) is the canonical form.
func (s *Sum) renorm() {
	s.pending = 0
	if s.pri[0] == 0 {
		return
	}
	for i := range Fold {
		l := math.Float64bits(s.pri[i])
		s.car[i] += float64(int((l>>(mantDig-3))&3) - 2)
		l &^= 1 << (mantDig - 3)
		l |= 1 << (mantDig - 2)
		s.pri[i] = math.Float64frombits(l)
	}
}

// convert is binned_ddmconv on a renormalised state: sum the carries and
// the primaries' offsets from their bin constants, largest bin first, with
// the top bins scaled down so the partial sums cannot overflow early.
func (s *Sum) convert() float64 {
	if s.pri[0] == 0 {
		return 0
	}
	xi := primaryIndex(s.pri[0])
	bb := bins[xi:]
	var y float64
	if xi <= (3*mantDig)/BinWidth {
		scaleDown := math.Ldexp(0.5, 1-(2*mantDig-BinWidth))
		scaleUp := math.Ldexp(0.5, 1+(2*mantDig-BinWidth))
		scaled := max(min(Fold, (3*mantDig)/BinWidth-xi), 0)
		i := 0
		if xi == 0 {
			y += float64(s.car[0] * float64(float64(float64(bb[0]/6.0)*scaleDown)*expansion))
			y += float64(s.car[1] * float64(float64(bb[1]/6.0)*scaleDown))
			y += float64(float64((s.pri[0]-bb[0])*scaleDown) * expansion)
			i = 2
		} else {
			y += float64(s.car[0] * float64(float64(bb[0]/6.0)*scaleDown))
			i = 1
		}
		for ; i < scaled; i++ {
			y += float64(s.car[i] * float64(float64(bb[i]/6.0)*scaleDown))
			y += float64((s.pri[i-1] - bb[i-1]) * scaleDown)
		}
		if i == Fold {
			y += float64((s.pri[Fold-1] - bb[Fold-1]) * scaleDown)
			return float64(y * scaleUp)
		}
		up := float64(y * scaleUp)
		if math.IsInf(up, 0) {
			return up
		}
		y = up
		for ; i < Fold; i++ {
			y += float64(s.car[i] * float64(bb[i]/6.0))
			y += s.pri[i-1] - bb[i-1]
		}
		y += s.pri[Fold-1] - bb[Fold-1]
		return y
	}
	y += float64(s.car[0] * float64(bb[0]/6.0))
	for i := 1; i < Fold; i++ {
		y += float64(s.car[i] * float64(bb[i]/6.0))
		y += s.pri[i-1] - bb[i-1]
	}
	y += s.pri[Fold-1] - bb[Fold-1]
	return y
}
