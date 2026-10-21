package reprosum

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
	"unsafe"
)

// The generators below are FMA-free: every value is built by math.Ldexp
// (exact) or a plain conversion, never a*b + c, so amd64 and arm64 draw the
// same inputs bit for bit.

type corpus struct {
	name string
	xs   []float64
}

func newRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15)) }

func signed(r *rand.Rand, v float64) float64 {
	if r.IntN(2) == 0 {
		return -v
	}
	return v
}

func uniformCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = r.Float64()
	}
	return xs
}

func signedUniformCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = signed(r, math.Ldexp(r.Float64(), r.IntN(20)))
	}
	return xs
}

// mixedCorpus spans roughly 1e-300 .. 1e300 (2^-996 .. 2^996).
func mixedCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = signed(r, math.Ldexp(0.5+r.Float64()/2, r.IntN(1993)-996))
	}
	return xs
}

// cancelCorpus is ill-conditioned: large values that cancel exactly,
// plus small residues that carry the whole sum.
func cancelCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, 0, n)
	for len(xs)+3 <= n {
		big := signed(r, math.Ldexp(r.Float64(), 60+r.IntN(40)))
		xs = append(xs, big, -big, math.Ldexp(r.Float64(), -r.IntN(30)))
	}
	r.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
	return xs
}

func subnormalCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = signed(r, math.Float64frombits(r.Uint64N(1<<52-1)+1))
	}
	return xs
}

func zerosCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, n)
	for i := range xs {
		switch r.IntN(4) {
		case 0:
			xs[i] = math.Copysign(0, -1)
		case 1:
			xs[i] = 0
		default:
			xs[i] = signed(r, math.Ldexp(r.Float64(), r.IntN(10)))
		}
	}
	return xs
}

// topBinTiesCorpus lives in the scaled top bin (index 0) and is built of
// odd multiples of 2^984, whose compressed slices land exactly half-way
// between grid points, mixed with odd multiples of 2^985, which land on
// the grid and flip the bin's parity: only the low-bit trick keeps the
// ties' rounding independent of what the bin already holds.
func topBinTiesCorpus(seed uint64, n int) []float64 {
	r := newRand(seed)
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = signed(r, math.Ldexp(float64(2*r.IntN(1<<20)+1), 984+r.IntN(2)))
	}
	return xs
}

func corpora() []corpus {
	return []corpus{
		{"uniform", uniformCorpus(1, 5000)},
		{"signed", signedUniformCorpus(2, 5000)},
		{"mixed_1e-300_1e300", mixedCorpus(3, 5000)},
		{"cancellation", cancelCorpus(4, 6000)},
		{"subnormal", subnormalCorpus(5, 3000)},
		{"signed_zeros", zerosCorpus(6, 3000)},
		{"subnormal_and_normal", append(subnormalCorpus(7, 1000), signedUniformCorpus(8, 1000)...)},
		{"top_bin_ties", topBinTiesCorpus(12, 2000)},
		{"near_max", []float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, 1, -math.MaxFloat64 / 3, 1e-300}},
	}
}

func sumOf(xs []float64) Sum {
	var s Sum
	for _, x := range xs {
		s.Add(x)
	}
	return s
}

// stateBits is the canonical (renormalised) state as raw words.
func stateBits(s Sum) [8]uint64 {
	s.renorm()
	var w [8]uint64
	for i := range Fold {
		w[i] = math.Float64bits(s.pri[i])
		w[Fold+i] = math.Float64bits(s.car[i])
	}
	w[6] = uint64(s.pending)
	w[7] = uint64(s.flags)
	return w
}

func shuffled(r *rand.Rand, xs []float64) []float64 {
	c := append([]float64(nil), xs...)
	r.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
	return c
}

// randomPartitionMerge splits xs into random chunks, sums each, then merges
// the partials pairwise in a random tree.
func randomPartitionMerge(r *rand.Rand, xs []float64) Sum {
	var parts []Sum
	for i := 0; i < len(xs); {
		n := 1 + r.IntN(max(1, len(xs)/3))
		if r.IntN(5) == 0 {
			n = 1 + r.IntN(3) // tiny chunks too
		}
		end := min(len(xs), i+n)
		parts = append(parts, sumOf(xs[i:end]))
		i = end
	}
	if r.IntN(3) == 0 {
		parts = append(parts, Sum{}) // an empty partial
	}
	for len(parts) > 1 {
		i := r.IntN(len(parts))
		j := r.IntN(len(parts) - 1)
		if j >= i {
			j++
		}
		parts[i].Merge(&parts[j])
		parts = append(parts[:j], parts[j+1:]...)
	}
	// A final merge into an empty sum: every result has gone through Merge,
	// so its raw state must already be canonical.
	var out Sum
	out.Merge(&parts[0])
	return out
}

// rawBits is the state exactly as stored, with no renormalisation.
func rawBits(s Sum) [8]uint64 {
	var w [8]uint64
	for i := range Fold {
		w[i] = math.Float64bits(s.pri[i])
		w[Fold+i] = math.Float64bits(s.car[i])
	}
	w[6] = uint64(s.pending)
	w[7] = uint64(s.flags)
	return w
}

func TestReproSum_PermutationInvariant(t *testing.T) {
	r := newRand(100)
	for _, c := range corpora() {
		t.Run(c.name, func(t *testing.T) {
			base := sumOf(c.xs)
			want, wantState := math.Float64bits(base.Float64()), stateBits(base)
			for k := range 25 {
				got := sumOf(shuffled(r, c.xs))
				if g := math.Float64bits(got.Float64()); g != want {
					t.Fatalf("permutation %d: bits %#x, want %#x", k, g, want)
				}
				if gs := stateBits(got); gs != wantState {
					t.Fatalf("permutation %d: state %x, want %x", k, gs, wantState)
				}
			}
		})
	}
}

func TestReproSum_PartitionMergeInvariant(t *testing.T) {
	r := newRand(200)
	for _, c := range corpora() {
		t.Run(c.name, func(t *testing.T) {
			base := sumOf(c.xs)
			want, wantState := math.Float64bits(base.Float64()), stateBits(base)
			for k := range 25 {
				got := randomPartitionMerge(r, shuffled(r, c.xs))
				if g := math.Float64bits(got.Float64()); g != want {
					t.Fatalf("partition %d: bits %#x, want %#x", k, g, want)
				}
				// Merge leaves the state canonical: raw == renormalised.
				if gs := rawBits(got); gs != wantState {
					t.Fatalf("partition %d: state %x, want %x", k, gs, wantState)
				}
			}
		})
	}
}

// A partial past the endurance (2048 pending deposits) must still merge
// exactly: Merge renormalises both sides first.
//
// The "bin_top" corpus is adversarial: every value sits just under the top
// of its bin and on the bin's grid (2^24 - 2^-15, bin 25), so each deposit
// lands whole and moves the primary word by almost the full
// endurance step and an un-renormalised operand would carry the merged
// primary out of its binade.
func TestReproSum_MergePastEndurance(t *testing.T) {
	binTop := make([]float64, 4*Endurance+5)
	for i := range binTop {
		binTop[i] = math.Ldexp(1, 24) - math.Ldexp(1, -15)
	}
	for _, c := range []corpus{
		{"signed", signedUniformCorpus(9, 3*Endurance+17)},
		{"bin_top", binTop},
	} {
		t.Run(c.name, func(t *testing.T) {
			xs := c.xs
			base := sumOf(xs)
			want := stateBits(base)
			exact, _ := exactSum(xs)
			if e := absErr(exact, base.Float64()); e > bound(len(xs), math.Abs(xs[0]), base.Float64()) {
				t.Fatalf("serial |err| %g over the bound", e)
			}
			for _, cut := range []int{1, Endurance - 1, Endurance, Endurance + 1, 2*Endurance - 1, 2 * Endurance, 2*Endurance + 1, 3*Endurance - 1, len(xs) - 1} {
				a, b := sumOf(xs[:cut]), sumOf(xs[cut:])
				a.Merge(&b)
				if got := rawBits(a); got != want {
					t.Fatalf("cut %d: state %x, want %x", cut, got, want)
				}
				b2, a2 := sumOf(xs[cut:]), sumOf(xs[:cut])
				b2.Merge(&a2)
				if got := rawBits(b2); got != want {
					t.Fatalf("cut %d (reversed): state %x, want %x", cut, got, want)
				}
			}
		})
	}
}

// Float64 must read the canonical state. With large carries (built here
// by repeated self-merges) the conversion's rounding depends on how a
// value is split between primary and carry, so reading an
// un-renormalised state gives different bits for the same inputs.
func TestReproSum_Float64IsCanonical(t *testing.T) {
	for seed := uint64(1); seed < 300; seed++ {
		r := newRand(seed)
		var s Sum
		for range 50 {
			s.Add(signed(r, math.Ldexp(r.Float64(), r.IntN(60))))
		}
		for range 30 + int(seed%20) {
			s.Merge(&s)
		}
		for i := range 2000 {
			s.Add(signed(r, math.Ldexp(r.Float64(), r.IntN(60))))
			var c Sum
			c.Merge(&s)
			if g, w := math.Float64bits(s.Float64()), math.Float64bits(c.Float64()); g != w {
				t.Fatalf("seed %d add %d: Float64 %#x, canonical %#x", seed, i, g, w)
			}
		}
	}
}

// A zero input is skipped: the state of {+0, -0} is the empty state.
func TestReproSum_ZeroIsEmptyState(t *testing.T) {
	s := sumOf([]float64{0, math.Copysign(0, -1), 0})
	if rawBits(s) != rawBits(Sum{}) {
		t.Fatalf("state of zeros %x, want the empty state", rawBits(s))
	}
}

func TestReproSum_MergeSelf(t *testing.T) {
	xs := mixedCorpus(10, 777)
	s := sumOf(xs)
	s.Merge(&s)
	want := sumOf(append(append([]float64(nil), xs...), xs...))
	if stateBits(s) != stateBits(want) {
		t.Fatalf("s.Merge(&s) state %x, want %x", stateBits(s), stateBits(want))
	}
}

func TestReproSum_SpecialValues(t *testing.T) {
	nan, pinf, ninf := math.NaN(), math.Inf(1), math.Inf(-1)
	negZero := math.Copysign(0, -1)
	cases := []struct {
		name string
		xs   []float64
		want float64 // NaN means "is NaN"
	}{
		{"empty", nil, 0},
		{"neg_zero", []float64{negZero}, 0},
		{"neg_zeros", []float64{negZero, negZero, negZero}, 0},
		{"mixed_zeros", []float64{negZero, 0, negZero}, 0},
		{"cancel_to_zero", []float64{1, -1}, 0},
		{"cancel_to_zero_neg_first", []float64{-1, 1}, 0},
		{"nan", []float64{1, nan, 2}, nan},
		{"pos_inf", []float64{1, pinf, -1e308}, pinf},
		{"neg_inf", []float64{1, ninf, 1e308}, ninf},
		{"pos_and_neg_inf", []float64{pinf, 3, ninf}, nan},
		{"inf_and_nan", []float64{pinf, nan}, nan},
		{"repeated_pos_inf", []float64{pinf, pinf}, pinf},
		{"finite_overflow", []float64{math.MaxFloat64, math.MaxFloat64}, pinf},
		{"finite_neg_overflow", []float64{-math.MaxFloat64, -math.MaxFloat64}, ninf},
		// Naive left-to-right overflows to +Inf here; binned does not.
		{"overflow_then_cancel", []float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64}, math.MaxFloat64},
		{"max_plus_tiny", []float64{math.MaxFloat64, 1}, math.MaxFloat64},
		{"smallest_subnormal_twice_alone", []float64{math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64}, 0},
	}
	r := newRand(300)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k := range 8 {
				xs := tc.xs
				var got float64
				if k == 0 {
					s := sumOf(xs)
					got = s.Float64()
				} else {
					s := randomPartitionMerge(r, append(shuffled(r, xs), 0))
					got = s.Float64()
				}
				switch {
				case math.IsNaN(tc.want):
					if !math.IsNaN(got) {
						t.Fatalf("variant %d: got %v, want NaN", k, got)
					}
				case math.IsNaN(got):
					t.Fatalf("variant %d: got NaN, want %v", k, tc.want)
				case math.Float64bits(got) != math.Float64bits(tc.want):
					t.Fatalf("variant %d: got %v (%#x), want %v (%#x)", k, got, math.Float64bits(got), tc.want, math.Float64bits(tc.want))
				}
			}
		})
	}
}

// Non-finite inputs never touch the bins: the finite part of the state is
// the state of the finite inputs alone.
func TestReproSum_NonFiniteLeavesBinsAlone(t *testing.T) {
	xs := signedUniformCorpus(11, 500)
	clean := sumOf(xs)
	dirty := sumOf(append(append([]float64{math.Inf(1)}, xs...), math.NaN(), math.Inf(-1)))
	if clean.pri != dirty.pri || clean.car != dirty.car {
		t.Fatal("non-finite inputs changed the bins")
	}
	if dirty.flags != flagNaN|flagPosInf|flagNegInf {
		t.Fatalf("flags %b, want all three", dirty.flags)
	}
}

func TestReproSum_AddWeighted(t *testing.T) {
	r := newRand(400)
	var a, b Sum
	for range 2000 {
		w := math.Ldexp(r.Float64(), r.IntN(8))
		x := signed(r, math.Ldexp(r.Float64(), r.IntN(30)-15))
		a.AddWeighted(w, x)
		p := float64(w * x)
		b.Add(p)
	}
	if stateBits(a) != stateBits(b) {
		t.Fatal("AddWeighted(w, x) differs from Add(float64(w*x))")
	}
}

func TestReproSum_StateSize(t *testing.T) {
	if sz := unsafe.Sizeof(Sum{}); sz > 64 {
		t.Fatalf("Sum is %d bytes, budget 64", sz)
	}
	if sz := unsafe.Sizeof(Sum{}.pri) + unsafe.Sizeof(Sum{}.car); sz != 48 {
		t.Fatalf("bin state is %d bytes, want 48 (K=3 primaries + carries)", sz)
	}
}

func TestReproSum_Constants(t *testing.T) {
	// The ReproBLAS constants this port is pinned to (binned.h).
	if maxIndex != 51 || Endurance != 2048 || compression != 1.0/16384 || expansion != 16384 {
		t.Fatalf("maxIndex %d endurance %d compression %v expansion %v", maxIndex, Endurance, compression, expansion)
	}
	if bins[0] != math.Ldexp(1.5, 1023) || bins[1] != math.Ldexp(0.75, 998) || bins[maxIndex] != math.Ldexp(0.75, -1002) {
		t.Fatalf("bin constants moved: %v %v %v", bins[0], bins[1], bins[maxIndex])
	}
	for i := 1; i <= maxIndex; i++ {
		if primaryIndex(bins[i]) != i {
			t.Fatalf("primaryIndex(bins[%d]) = %d", i, primaryIndex(bins[i]))
		}
	}
	if primaryIndex(bins[0]) != 0 {
		t.Fatalf("primaryIndex(bins[0]) = %d", primaryIndex(bins[0]))
	}
}

// exactSum returns the exact sum (as a big.Float) and its float64 rounding.
func exactSum(xs []float64) (*big.Float, float64) {
	acc := new(big.Float).SetPrec(4096)
	for _, x := range xs {
		acc.Add(acc, new(big.Float).SetPrec(4096).SetFloat64(x))
	}
	f, _ := acc.Float64()
	return acc, f
}

func absErr(exact *big.Float, got float64) float64 {
	d := new(big.Float).SetPrec(4096).SetFloat64(got)
	d.Sub(d, exact)
	f, _ := d.Abs(d).Float64()
	return f
}

// bound is binned_dbbound for fold 3.
func bound(n int, maxAbs, s float64) float64 {
	eps := math.Ldexp(1, -53)
	return float64(float64(max(maxAbs, math.Ldexp(0.5, minExp-1))*math.Ldexp(0.5, (1-Fold)*BinWidth+1))*float64(n)) +
		float64((7*eps)/(1-6*math.Sqrt(eps)-7*eps)*math.Abs(s))
}

func TestReproSum_Accuracy(t *testing.T) {
	type acc struct {
		name string
		gen  func(seed uint64) []float64
	}
	sets := []acc{
		{"uniform", func(s uint64) []float64 { return uniformCorpus(s, 4000) }},
		{"signed", func(s uint64) []float64 { return signedUniformCorpus(s, 4000) }},
		{"mixed_1e-300_1e300", func(s uint64) []float64 { return mixedCorpus(s, 2000) }},
		{"cancellation", func(s uint64) []float64 { return cancelCorpus(s, 3000) }},
		{"decimal_tenths", func(s uint64) []float64 {
			xs := make([]float64, 10000)
			for i := range xs {
				xs[i] = 0.1
			}
			return xs
		}},
	}
	for _, set := range sets {
		t.Run(set.name, func(t *testing.T) {
			var binnedTotal, naiveTotal float64
			for seed := uint64(1); seed <= 20; seed++ {
				xs := set.gen(seed*1000 + 7)
				exact, rounded := exactSum(xs)
				s := sumOf(xs)
				got := s.Float64()
				var naive, maxAbs float64
				for _, x := range xs {
					naive += x
					maxAbs = max(maxAbs, math.Abs(x))
				}
				e := absErr(exact, got)
				if b := bound(len(xs), maxAbs, rounded); e > b {
					t.Fatalf("seed %d: |err| %g exceeds the documented bound %g", seed, e, b)
				}
				binnedTotal += e
				naiveTotal += absErr(exact, naive)
			}
			if binnedTotal > naiveTotal {
				t.Fatalf("binned total |err| %g worse than naive %g", binnedTotal, naiveTotal)
			}
			t.Logf("total |err| binned %g, naive %g", binnedTotal, naiveTotal)
		})
	}
}

// Subnormal contributions below the deepest bin's grid (2^-1055) round
// away deterministically; the absolute error stays under N * 2^-1056 + 1 ulp.
func TestReproSum_SubnormalErrorFloor(t *testing.T) {
	for seed := uint64(1); seed <= 10; seed++ {
		xs := subnormalCorpus(seed, 2000)
		exact, _ := exactSum(xs)
		s := sumOf(xs)
		e := absErr(exact, s.Float64())
		if limit := float64(len(xs)) * math.Ldexp(1, -1056); e > limit {
			t.Fatalf("seed %d: subnormal |err| %g exceeds %g", seed, e, limit)
		}
	}
}

// Long runs push the carries through many renormalisations; the sum of
// 1e6 copies of a value must stay within the bound (naive drifts).
func TestReproSum_LongRunCarries(t *testing.T) {
	const n = 1_000_000
	for _, v := range []float64{0.1, -3.7, 1e300, math.Ldexp(1, -1000)} {
		var s Sum
		var naive float64
		for range n {
			s.Add(v)
			naive += v
		}
		exact := new(big.Float).SetPrec(4096).SetFloat64(v)
		exact.Mul(exact, new(big.Float).SetInt64(n))
		want, _ := exact.Float64()
		e := absErr(exact, s.Float64())
		if b := bound(n, math.Abs(v), want); e > b {
			t.Fatalf("v=%g: |err| %g exceeds bound %g", v, e, b)
		}
		if e > absErr(exact, naive) {
			t.Fatalf("v=%g: binned |err| %g worse than naive %g", v, e, absErr(exact, naive))
		}
	}
}

// The fast-path cache is derived state: after any sequence of adds and
// merges it equals what pri[0] implies, or a later add would deposit on
// the wrong path or skip a needed bin raise.
func TestReproSum_FastCacheConsistent(t *testing.T) {
	r := newRand(700)
	check := func(s *Sum, what string) {
		t.Helper()
		c := *s
		c.setFast()
		if c.fast != s.fast {
			t.Fatalf("%s: fast %d, pri[0] implies %d", what, s.fast, c.fast)
		}
	}
	for range 200 {
		var s Sum
		check(&s, "empty")
		for range 50 {
			switch r.IntN(4) {
			case 0:
				s.Add(signed(r, math.Ldexp(r.Float64(), r.IntN(2098)-1074)))
				check(&s, "add")
			case 1:
				o := sumOf(mixedCorpus(r.Uint64(), 1+r.IntN(5)))
				s.Merge(&o)
				check(&s, "merge")
			case 2:
				var e Sum
				e.Merge(&s)
				check(&e, "merge into empty")
			default:
				s.Add(signed(r, math.Ldexp(r.Float64(), r.IntN(40))))
				check(&s, "small add")
			}
		}
	}
}
