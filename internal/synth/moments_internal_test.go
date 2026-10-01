package synth

import (
	"math"
	"runtime"
	"sort"
	"testing"
)

// The FMA-contraction gate.
//
// Go permits `a + b*c` to be contracted into a single fused
// multiply-add. arm64 does it, amd64 does not, so the same source gives
// different last bits on different CPUs — and a profile document is an
// artefact users commit, share and diff. The barriers that forbid
// contraction are the explicit `float64(...)` conversions wrapped around
// products in the capture formulas (see moments.go for the full rule).
// Nothing in the type system protects them: deleting one still compiles,
// still passes every tolerance-based test in this package, and quietly
// makes `pulse profile create` machine-dependent again.
//
// These tests are that protection. Each pairs a real capture function
// against a `//go:noinline` twin holding the pre-barrier source verbatim
// — the form the compiler is free to fuse — and asserts:
//
//  1. the production function equals the BARRIERED value (the gate: it
//     fails the moment a barrier is dropped on a contracting build), and
//  2. the fused twin DISAGREES with the barriered one on inputs where
//     contraction is observable (the anti-vacuity check: without it a
//     future edit to the sample data could silently reduce the gate to
//     comparing two identical numbers).
//
// The gate can only detect on a contracting architecture. On amd64 the
// "fused" twin is not actually fused and every comparison is trivially
// satisfied, which is why the probe result is logged rather than
// assumed. A green run on amd64 proves nothing about the barriers.

// fusedSub / barrieredSub are the minimal discriminator pair, used to
// establish whether this build contracts at all.
//
//go:noinline
func fusedSub(a, b, c float64) float64 { return a - b*c }

//go:noinline
func barrieredSub(a, b, c float64) float64 { return a - float64(b*c) }

// fmaGateSamples reproduces the two numeric columns of the package's
// model fixture cohort in exact integer arithmetic, so the gate runs on
// values with the same magnitudes and mantissa occupancy a real capture
// sees. Integer accumulation with a single closing division is exact on
// every architecture, so these inputs are themselves fusion-free —
// otherwise the gate's own data would drift with the CPU.
func fmaGateSamples() (spend, visits []float64) {
	spend = make([]float64, 0, 1200)
	visits = make([]float64, 0, 1200)
	for r := 0; r < 1200; r++ {
		region := r % 3
		tier := (r / 3) % 2
		h := 4000 + 1100*region + 600*tier
		if r%4 == 0 {
			h += 2500
		}
		h += (r%13)*25 + ((r*7919)%97)*5
		spend = append(spend, float64(h)/100)
		vh := 300 + 200*tier - 50*region + ((r*104729)%53)*3
		visits = append(visits, float64(vh)/100)
	}
	return spend, visits
}

// contractionObservable reports whether this build contracts a
// multiply-subtract on the gate's own sample moments.
func contractionObservable() bool {
	spend, _ := fmaGateSamples()
	count, sum, sumSq := runningMoments(spend)
	mean := sum / float64(count)
	return fusedSub(sumSq, mean, sum) != barrieredSub(sumSq, mean, sum)
}

// runningMoments is the (count, sum, sumSq) triple the capture path
// builds, with the same barrier profile.go uses on sumSq.
func runningMoments(xs []float64) (int, float64, float64) {
	var sum, sumSq float64
	for _, v := range xs {
		sum += v
		sumSq += float64(v * v)
	}
	return len(xs), sum, sumSq
}

// TestFloatFusion_ProbeReportsWhetherThisBuildContracts is diagnostic,
// not an assertion. A reader debugging a cross-architecture document
// diff needs this fact stated, and a green run must not be mistaken for
// "no fusion anywhere" when the architecture simply cannot fuse.
func TestFloatFusion_ProbeReportsWhetherThisBuildContracts(t *testing.T) {
	obs := contractionObservable()
	t.Logf("GOARCH=%s contracts a-b*c into an FMA on the gate's samples: %v", runtime.GOARCH, obs)
	if !obs {
		t.Logf("the gates in this file cannot detect a dropped barrier on this architecture; " +
			"linux/amd64 CI plus an arm64 developer machine together cover it")
	}
}

// fusedSampleVariance is sampleVariance's pre-barrier source.
//
//go:noinline
func fusedSampleVariance(count int, sum, sumSq float64) float64 {
	if count < 2 {
		return 0
	}
	mean := sum / float64(count)
	variance := (sumSq - mean*sum) / float64(count-1)
	if variance < 0 {
		variance = 0
	}
	return variance
}

// TestFloatFusion_SampleVarianceIsBarriered pins the barrier in
// sampleVariance — the single expression every captured `std` in a
// profile document and every `std` in a fidelity report flows through.
func TestFloatFusion_SampleVarianceIsBarriered(t *testing.T) {
	spend, _ := fmaGateSamples()
	count, sum, sumSq := runningMoments(spend)

	mean := sum / float64(count)
	barriered := barrieredSub(sumSq, mean, sum) / float64(count-1)
	fused := fusedSampleVariance(count, sum, sumSq)

	if contractionObservable() && fused == barriered {
		t.Fatal("the gate has gone vacuous: the fused and barriered forms now agree on these " +
			"samples, so a dropped barrier would no longer be detected")
	}
	if got := sampleVariance(count, sum, sumSq); got != barriered {
		t.Errorf("sampleVariance is not fusion-free: got %.20g, barriered %.20g, fused %.20g\n"+
			"the float64(mean*sum) barrier in moments.go is load-bearing — without it a captured "+
			"profile document differs between arm64 and amd64", got, barriered, fused)
	}
}

// fusedPearson is pearson's pre-barrier source.
//
//go:noinline
func fusedPearson(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 2 {
		return math.NaN()
	}
	var sumA, sumB float64
	for i := 0; i < n; i++ {
		sumA += a[i]
		sumB += b[i]
	}
	mA := sumA / float64(n)
	mB := sumB / float64(n)
	var num, dA, dB float64
	for i := 0; i < n; i++ {
		da := a[i] - mA
		db := b[i] - mB
		num += da * db
		dA += da * da
		dB += db * db
	}
	if dA == 0 || dB == 0 {
		return math.NaN()
	}
	return num / math.Sqrt(dA*dB)
}

// barrieredPearson is the same computation with the barriers restated
// here, so the gate does not verify pearson against itself.
//
//go:noinline
func barrieredPearson(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 2 {
		return math.NaN()
	}
	var sumA, sumB float64
	for i := 0; i < n; i++ {
		sumA += a[i]
		sumB += b[i]
	}
	mA := sumA / float64(n)
	mB := sumB / float64(n)
	var num, dA, dB float64
	for i := 0; i < n; i++ {
		da := a[i] - mA
		db := b[i] - mB
		num += float64(da * db)
		dA += float64(da * da)
		dB += float64(db * db)
	}
	if dA == 0 || dB == 0 {
		return math.NaN()
	}
	return num / math.Sqrt(dA*dB)
}

// TestFloatFusion_PearsonIsBarriered pins the barriers in pearson, which
// produces every `rho` in a profile document's `pairwise` and
// `conditional.numeric_pairs` sections.
func TestFloatFusion_PearsonIsBarriered(t *testing.T) {
	a, b := fmaGateSamples()

	fused := fusedPearson(a, b)
	barriered := barrieredPearson(a, b)

	if contractionObservable() && fused == barriered {
		t.Fatal("the gate has gone vacuous: the fused and barriered accumulations now agree on " +
			"these samples, so a dropped barrier would no longer be detected")
	}
	if got := pearson(a, b); got != barriered {
		t.Errorf("pearson is not fusion-free: got %.20g, barriered %.20g, fused %.20g\n"+
			"the float64(da*db) barriers in profile.go are load-bearing — without them every "+
			"captured rho differs between arm64 and amd64", got, barriered, fused)
	}
}

// fusedPercentile is computePercentiles' pre-barrier source for a single
// quantile. The offending shape is cross-statement — `idx := q * n` on
// one line feeding `idx - float64(lo)` on another — which is the form
// reviewers most often mistake for already-safe because the product sits
// on its own line. The Go spec permits fusion across statements.
//
//go:noinline
func fusedPercentile(sorted []float64, q float64) float64 {
	idx := q * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// TestFloatFusion_PercentileInterpolationIsBarriered pins the two
// barriers in computePercentiles, which produce the `percentiles` block
// of every numeric field in a profile document.
func TestFloatFusion_PercentileInterpolationIsBarriered(t *testing.T) {
	sorted, _ := fmaGateSamples()
	sort.Float64s(sorted)

	qs := []float64{0.01, 0.05, 0.1, 0.25, 0.37, 0.5, 0.63, 0.75, 0.9, 0.95, 0.99}
	got := computePercentiles(sorted, qs)
	if len(got) != len(qs) {
		t.Fatalf("computePercentiles returned %d values, want %d", len(got), len(qs))
	}

	discriminated := false
	for i, q := range qs {
		if fusedPercentile(sorted, q) != got[i] {
			discriminated = true
		}
	}
	if contractionObservable() && !discriminated {
		t.Fatal("the gate has gone vacuous: no quantile in the set separates the fused form " +
			"from the production one, so a dropped barrier would no longer be detected")
	}

	// The gate itself: every value must be the barriered one. Restated
	// here rather than delegated, so this does not verify the function
	// against itself.
	for i, q := range qs {
		last := float64(len(sorted) - 1)
		idx := float64(q * last)
		lo := int(math.Floor(idx))
		hi := int(math.Ceil(idx))
		want := sorted[lo]
		if lo != hi {
			frac := idx - float64(lo)
			want = float64(sorted[lo]*(1-frac)) + float64(sorted[hi]*frac)
		}
		if got[i] != want {
			t.Errorf("computePercentiles(q=%v) is not fusion-free: got %.20g, barriered %.20g, fused %.20g\n"+
				"the float64(q*last) and float64(sorted[lo]*(1-frac)) barriers in profile.go are load-bearing",
				q, got[i], want, fusedPercentile(sorted, q))
		}
	}
}
