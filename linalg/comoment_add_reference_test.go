package linalg

import (
	"math"
	"math/rand/v2"
	"testing"
)

// addListwiseReference is CoMoment.Add's Listwise arm exactly as it
// stood before matrix-extensions-hardening E8-S5 — one wd slice
// allocated per massed row — retained verbatim as the live oracle the
// allocation-free fold must match to the bit.
func addListwiseReference(c *CoMoment, x []float64, w float64) {
	for _, v := range x {
		if math.IsNaN(v) {
			return
		}
	}
	if !validWeight(w) {
		c.nInvalid++
		return
	}
	c.n++
	if w == 0 {
		return
	}
	c.nMass++
	wOld := c.w
	c.w += w
	c.w2 += float64(w * w)
	if wOld == 0 {
		copy(c.mean, x)
		return
	}
	wd := make([]float64, c.p)
	for i, v := range x {
		wd[i] = float64(w * (v - c.mean[i]))
	}
	for i := range c.mean {
		c.mean[i] += wd[i] / c.w
	}
	k := 0
	for i := 0; i < c.p; i++ {
		for j := i; j < c.p; j++ {
			c.m2[k] += float64(wd[i] * (x[j] - c.mean[j]))
			k++
		}
	}
}

// sameBits reports a and b bit-identical, or both NaN (a NaN's sign bit
// is architecture-dependent, so it is never compared).
func sameBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

// requireSameState fails t unless got's whole Listwise state equals
// want's to the bit.
func requireSameState(t *testing.T, row int, got, want *CoMoment) {
	t.Helper()
	if got.n != want.n || got.nMass != want.nMass || got.nInvalid != want.nInvalid {
		t.Fatalf("row %d: counts (n %d, mass %d, invalid %d), reference (%d, %d, %d)",
			row, got.n, got.nMass, got.nInvalid, want.n, want.nMass, want.nInvalid)
	}
	if !sameBits(got.w, want.w) || !sameBits(got.w2, want.w2) {
		t.Fatalf("row %d: W %v W2 %v, reference %v %v", row, got.w, got.w2, want.w, want.w2)
	}
	for i := range want.mean {
		if !sameBits(got.mean[i], want.mean[i]) {
			t.Fatalf("row %d: mean[%d] = %x, reference %x", row, i, math.Float64bits(got.mean[i]), math.Float64bits(want.mean[i]))
		}
	}
	for k := range want.m2 {
		if !sameBits(got.m2[k], want.m2[k]) {
			t.Fatalf("row %d: m2[%d] = %x, reference %x", row, k, math.Float64bits(got.m2[k]), math.Float64bits(want.m2[k]))
		}
	}
}

// TestCoMomentAddListwise_MatchesPreScratchReference (E8-S5): the
// Listwise fold that allocates nothing per row leaves every stored
// figure — counts, W, W2, each mean and each packed co-moment —
// bit-identical to the retained pre-change fold, row after row, live in
// this process (no stored bits: the kernel is FMA-free, so the two
// agree on every architecture). Rows mix missing cells, zero, negative,
// NaN and infinite weights, a massless start and wide magnitudes.
func TestCoMomentAddListwise_MatchesPreScratchReference(t *testing.T) {
	weights := []float64{0, -1, math.NaN(), math.Inf(1)}
	for _, p := range []int{0, 1, 2, 3, 8, 50} {
		for _, scale := range []float64{1, 1e-6, 1e9} {
			r := rand.New(rand.NewPCG(uint64(p)+1, math.Float64bits(scale)))
			got, _ := NewCoMoment(p, Listwise)
			want, _ := NewCoMoment(p, Listwise)
			missing := 0.1 / float64(max(p, 1)) // ~1 row in 10 incomplete
			for row := range 2000 {
				x := make([]float64, p)
				base := r.NormFloat64()
				for j := range x {
					if r.Float64() < missing {
						x[j] = math.NaN()
						continue
					}
					x[j] = float64(scale*(float64(0.6*base)+float64(0.8*r.NormFloat64()))) + float64(j)
				}
				w := 0.25 + float64(2*r.Float64())
				if row < 3 {
					w = 0 // a massless start: the first massed row seeds the mean
				} else if r.Float64() < 0.05 {
					w = weights[r.IntN(len(weights))]
				}
				got.Add(x, w)
				addListwiseReference(want, x, w)
				requireSameState(t, row, got, want)
			}
			if got.nMass < 1000 {
				t.Fatalf("p=%d scale=%g: only %d massed rows folded", p, scale, got.nMass)
			}
		}
	}
}

// TestCoMomentAddListwise_AllocatesNothing (E8-S5): a Listwise Add
// allocates nothing per row — the massed path folds in place.
func TestCoMomentAddListwise_AllocatesNothing(t *testing.T) {
	for _, p := range []int{1, 8, 50, 256} {
		c, _ := NewCoMoment(p, Listwise)
		x := make([]float64, p)
		y := make([]float64, p)
		for j := range x {
			x[j] = float64(j)
			y[j] = float64(2*j) + 1
		}
		c.Add(x, 1) // seeds the mean; every later row takes the full fold
		rows := [][]float64{y, x}
		i := 0
		if a := testing.AllocsPerRun(200, func() {
			c.Add(rows[i%2], 1.5)
			i++
		}); a != 0 {
			t.Errorf("p=%d: %v allocations per Listwise Add, want 0", p, a)
		}
	}
}
