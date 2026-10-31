package sem

import (
	"math"
	"runtime"
	"testing"

	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/mat"
)

// pinnedFigure is one SEM figure computed from fixed inputs by SEM's
// own arithmetic only (no gonum factorization or product on the path).
type pinnedFigure struct {
	name string
	v    float64
}

// semPinnedFigures computes the SEM figures that are bit-identical on
// every GOARCH (U61 cross-arch determinism): the fit indices and
// chi-square p from a fit's test statistics, the Wald critical value,
// the bootstrap percentile bound, the defined-parameter dual-number
// value / SE / p / interval, the WLSMV scaled-shifted statistic and
// the gonum-order dot product. An end-to-end MAT_SEM figure is NOT
// among them: every fit runs gonum's mat.Mul / Inverse / LogDet, whose
// amd64 assembly and contracted arm64 Go differ in the last bits, and
// the optimiser carries that difference (E4-S4 pins those at a
// tolerance).
func semPinnedFigures(t *testing.T) []pinnedFigure {
	t.Helper()
	var out []pinnedFigure
	add := func(name string, v float64) { out = append(out, pinnedFigure{name, v}) }

	// lavaan's statistics over two groups' parts (lavaanCombine): AIC,
	// BIC, chi-square p, CFI, TLI, RMSEA (√G), NFI, AGFI.
	parts := []lavaanPart{
		{ndat: 21, nel: 21, baseParams: 6, fb: 1.7320508075688772, logLik: -812.375, gfi: 0.9412, n: 151},
		{ndat: 21, nel: 21, baseParams: 6, fb: 1.4142135623730951, logLik: -790.0625, gfi: 0.9287, n: 149},
	}
	st := lavaanCombine(parts, []float64{31.41592653589793, 27.18281828459045}, 26)
	add("lavaan.aic", st.AIC)
	add("lavaan.bic", st.BIC)
	add("lavaan.chi2_p", st.Chi2P)
	add("lavaan.chi2_baseline", st.Chi2Baseline)
	add("lavaan.cfi", st.CFI)
	add("lavaan.tli", st.TLI)
	add("lavaan.rmsea", st.RMSEA)
	add("lavaan.nfi", st.NFI)
	add("lavaan.gfi", st.GFI)
	add("lavaan.agfi", st.AGFI)
	cfi, tli, rmsea := lavaanIndices(84.22020230636542, 8, 225.5501030878478, 15, 32, 1)
	add("indices.cfi", cfi)
	add("indices.tli", tli)
	add("indices.rmsea", rmsea)
	add("chi2_p.84_8", 1-chiCDF(84.22020230636542, 8))
	add("chi2_p.1_169_9", 1-chiCDF(1.169, 9))

	// The Wald critical value off the default level, the normal-tail p.
	add("wald_crit.0_8001", Options{Level: 0.8001}.waldCrit())
	add("wald_crit.0_99", Options{Level: 0.99}.waldCrit())
	add("wald_p.z1_529", 2*(1-normCDF(math.Abs(1.529))))

	// The bootstrap percentile interval (boot:::norm.inter) over fixed
	// replicates at level 0.8, both bounds at a fractional rank (23·0.1,
	// 23·0.9), so interpolated on the normal-quantile scale.
	reps := []float64{0.41, 0.37, 0.52, 0.48, 0.33, 0.45, 0.50, 0.39, 0.44, 0.47,
		0.36, 0.55, 0.42, 0.40, 0.49, 0.38, 0.46, 0.43, 0.51, 0.35, 0.54, 0.34}
	add("boot.ci_lower", normInter(reps, 0.1))
	add("boot.ci_upper", normInter(reps, 0.9))
	add("boot.se", sampleSD(reps))

	// Defined parameters through the dual numbers (Pow, Exp, Log, /),
	// with their delta-method SE, p and Wald interval at level 0.9. The
	// inputs are ones where math.Pow / Exp / Log and detmath's differ on
	// arm64, so a stdlib call slipping back fails here.
	labels := map[string]labelValue{"a": {free: 0}, "b": {free: 1}, "c": {free: 2}, "d": {free: 3}}
	x0 := []float64{0.7072, 1.3, -0.8885, 0.5628}
	vcov := mat.NewSymDense(4, []float64{
		0.041, 0.012, -0.007, 0.002,
		0.012, 0.093, 0.004, -0.003,
		-0.007, 0.004, 0.027, 0.001,
		0.002, -0.003, 0.001, 0.019,
	})
	var defs []*definedParam
	for _, line := range []string{
		"pow := a^b",
		"ex := exp(c)",
		"lg := log(d)",
		"mix := a^b * exp(c) / (1 + log(d + 2)) - b^-1.5",
	} {
		d, err := parseDefinedLine(line)
		if err != nil {
			t.Fatal(err)
		}
		defs = append(defs, d)
	}
	for _, e := range evalDefined(defs, labels, x0, vcov, Options{Level: 0.9}.waldCrit()) {
		add("defined."+e.LVal+".value", e.Value)
		add("defined."+e.LVal+".se", e.SE)
		add("defined."+e.LVal+".p", e.P)
		add("defined."+e.LVal+".ci_lower", e.CILower)
		add("defined."+e.LVal+".ci_upper", e.CIUpper)
	}

	// WLSMV's scaled-shifted test statistic (lav_test_sb).
	sc := scaledShifted(41.23456789, 13, 15.75, 19.125)
	add("scaled_shifted.stat", sc.stat)
	add("scaled_shifted.shift", sc.shift)

	// The gonum-order dot product over 11 elements (two quads + tail).
	x := []float64{0.1, -2.3, 3.7, 4.11, -5.9, 0.6, 7.25, -8.5, 9.1, 0.01, 1.3}
	y := []float64{1.9, 0.7, -0.3, 2.2, 1.1, -4.4, 0.05, 3.3, -0.9, 8.8, 0.77}
	add("dot.11", dot(x, y))
	return out
}

// TestSEM_ArithmeticBitsPinned is the cross-architecture gate on SEM's
// own arithmetic (U61): every figure of semPinnedFigures must equal its
// pinned bits exactly on every GOARCH. They run on linalg/detmath,
// internal/statdist and explicit float64(...) barriers; with math.Log /
// Erfc / Pow / Exp, gonum distuv and contractible products they sat
// apart by an ulp or more between arm64 and amd64. Pinned on
// darwin/arm64; CI's linux/amd64 run checks the same bits.
func TestSEM_ArithmeticBitsPinned(t *testing.T) {
	t.Parallel()

	want := map[string]uint64{
		"lavaan.aic":           0x40a971c000000000,
		"lavaan.bic":           0x40aa3258c096e79c,
		"lavaan.chi2_p":        0x3eae30a41b900000,
		"lavaan.chi2_baseline": 0x407d841eb0b42306,
		"lavaan.cfi":           0x3feceaefef008ab2,
		"lavaan.tli":           0x3fea3881e021040e,
		"lavaan.rmsea":         0x3fc10d9672651317,
		"lavaan.nfi":           0x3fec0784c87e69e7,
		"lavaan.gfi":           0x3fedeb73a4cbb52e,
		"lavaan.agfi":          0x3fea8a0f9096bb99,
		"indices.cfi":          0x3fe46a745e4bd819,
		"indices.tli":          0x3fd48f34619c6a60,
		"indices.rmsea":        0x3fe175f96083a6b9,
		"chi2_p.84_8":          0x3cff000000000000,
		"wald_crit.0_99":       0x40049b4c64d69161,
		"boot.ci_lower":        0x3fd5f8fa61f123c6,
		"boot.ci_upper":        0x3fe111430eb2b345,
		"boot.se":              0x3fb102f70b64398e,
		"scaled_shifted.stat":  0x4041016ad5bff738,
		"scaled_shifted.shift": 0x3f8e227bc78a4800,
		"chi2_p.1_169_9":       0x3feff74ecf7a6ca6,
		"wald_crit.0_8001":     0x3ff4826702ea5b6a,
		"wald_p.z1_529":        0x3fc0296eeca40308,
		"defined.pow.value":    0x3fe4657ebc140f7d,
		"defined.pow.se":       0x3fcde988a8dcefde,
		"defined.pow.p":        0x3f7a237829bad000,
		"defined.pow.ci_lower": 0x3fd0313a9dd0a23c,
		"defined.pow.ci_upper": 0x3ff05930149fe6ee,
		"defined.ex.value":     0x3fda5248a0af8046,
		"defined.ex.se":        0x3fb14cda1b49555d,
		"defined.ex.p":         0x3e13e9a000000000,
		"defined.ex.ci_lower":  0x3fd33514948cf69d,
		"defined.ex.ci_upper":  0x3fe0b7be566904f7,
		"defined.lg.value":     0xbfe26503e261994c,
		"defined.lg.se":        0x3fcf5982972bfaaf,
		"defined.lg.p":         0x3f9360d4508c14c0,
		"defined.lg.ci_lower":  0xbfef4936685d5b30,
		"defined.lg.ci_upper":  0xbfc6034571975d9e,
		"defined.mix.value":    0xbfe14481a40cc989,
		"defined.mix.se":       0x3fceb748d34a5363,
		"defined.mix.p":        0x3f991f1c53715f40,
		"defined.mix.ci_lower": 0xbfede5fe91671ca2,
		"defined.mix.ci_upper": 0xbfc28c12dac9d9be,
		"dot.11":               0xc042b4083126e978,
	}
	got := semPinnedFigures(t)
	if len(got) != len(want) {
		t.Errorf("%d figures computed, %d pinned", len(got), len(want))
	}
	for _, f := range got {
		w, ok := want[f.name]
		if !ok {
			t.Errorf("%s = %v (%#016x) is not pinned", f.name, f.v, math.Float64bits(f.v))
			continue
		}
		if math.IsNaN(f.v) || math.Float64bits(f.v) != w {
			t.Errorf("%s = %v (%#016x), want %v (%#016x)", f.name, f.v, math.Float64bits(f.v), math.Float64frombits(w), w)
		}
	}
}

// TestSEM_DotMatchesGonumOrder: dot replaces gonum's floats.Dot on the
// SEM gradient and information paths. On amd64 (gonum's assembly
// kernel, never fused) it must give floats.Dot's bits exactly over every
// length class, so swapping it in moved no amd64 figure; elsewhere
// gonum's pure-Go Dot sums in another order (and may be contracted), so
// only agreement to rounding is checked.
func TestSEM_DotMatchesGonumOrder(t *testing.T) {
	t.Parallel()

	exact := runtime.GOARCH == "amd64"
	for n := 0; n <= 37; n++ {
		x, y := make([]float64, n), make([]float64, n)
		for i := range x {
			x[i] = math.Sin(float64(3*i+n)) * float64(1+i%5)
			y[i] = math.Cos(float64(7*i-n)) / float64(1+i%3)
		}
		got, ref := dot(x, y), floats.Dot(x, y)
		if exact && math.Float64bits(got) != math.Float64bits(ref) {
			t.Errorf("n=%d: dot = %v (%#016x), gonum floats.Dot = %v (%#016x)", n, got, math.Float64bits(got), ref, math.Float64bits(ref))
		}
		if !exact && math.Abs(got-ref) > 1e-13*(1+math.Abs(ref)) {
			t.Errorf("n=%d: dot = %v, gonum floats.Dot = %v", n, got, ref)
		}
	}
}
