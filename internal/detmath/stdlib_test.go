package detmath

import (
	"fmt"
	"math"
	"runtime"
	"testing"
)

// kernel1 / kernel2 pair a detmath function with its math.* reference.
type kernel1 struct {
	name string
	det  func(float64) float64
	std  func(float64) float64
	// inputs draws the kernel's sweep from the deterministic stream.
	inputs func(*sweepRand) float64
}

type kernel2 struct {
	name   string
	det    func(float64, float64) float64
	std    func(float64, float64) float64
	inputs func(*sweepRand) (float64, float64)
}

// sweepRand is a splitmix64 stream: a fixed, platform-independent input
// list, never wall-clock randomness. Every float it yields is built from
// integer bits and exact power-of-two scaling, so the inputs themselves
// carry no rounding and are identical on every architecture.
type sweepRand struct{ s uint64 }

func (r *sweepRand) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// unit returns a float in [1, 2) with 52 random mantissa bits.
func (r *sweepRand) unit() float64 {
	return math.Float64frombits(0x3ff0000000000000 | r.next()>>12)
}

// scaled returns ±[1,2)·2^e with e uniform in [lo, hi].
func (r *sweepRand) scaled(lo, hi int) float64 {
	e := lo + int(r.next()%uint64(hi-lo+1))
	v := math.Ldexp(r.unit(), e)
	if r.next()&1 == 1 {
		v = -v
	}
	return v
}

// positive returns +[1,2)·2^e with e uniform in [lo, hi].
func (r *sweepRand) positive(lo, hi int) float64 {
	return math.Abs(r.scaled(lo, hi))
}

func kernels1() []kernel1 {
	return []kernel1{
		{"Exp", Exp, math.Exp, func(r *sweepRand) float64 {
			// |x| < 709 keeps the result normal; the tails go below.
			return math.Ldexp(r.unit(), int(r.next()%10)) * signOf(r) // ≤ 2^10
		}},
		{"Log", Log, math.Log, func(r *sweepRand) float64 { return r.positive(-1074, 1023) }},
		{"Log1p", Log1p, math.Log1p, func(r *sweepRand) float64 {
			if r.next()&1 == 1 {
				return -math.Ldexp(r.unit(), -int(r.next()%60)-1) // (-1, 0)
			}
			return r.positive(-80, 1023)
		}},
		{"Sin", Sin, math.Sin, func(r *sweepRand) float64 { return r.scaled(-30, 60) }},
		{"Cos", Cos, math.Cos, func(r *sweepRand) float64 { return r.scaled(-30, 60) }},
		{"Tan", Tan, math.Tan, func(r *sweepRand) float64 { return r.scaled(-30, 60) }},
		// ±2^-60 … ±2^9: the |x| < 2^-54 identity, both k = ±1 reductions,
		// the k < 20 and k ≥ 20 rebuilds, and the -1 / +Inf saturations.
		{"Expm1", Expm1, math.Expm1, func(r *sweepRand) float64 { return r.scaled(-60, 9) }},
		{"Erf", Erf, math.Erf, func(r *sweepRand) float64 {
			if r.next()%8 == 0 { // the |x| < 2^-28 linear arms, subnormals included
				return r.scaled(-1074, -29)
			}
			return r.scaled(-6, 2) // every rational arm and the |x| ≥ 6 saturation
		}},
		{"Erfinv", Erfinv, math.Erfinv, func(r *sweepRand) float64 {
			if r.next()&1 == 1 { // the log tail: 1 - 2^-k, both r ≤ 5 and r > 5
				return signOf(r) * (1 - math.Ldexp(r.unit(), -int(r.next()%52)-2))
			}
			return r.scaled(-40, -1) // (-1, 1): the |x| ≤ 0.85 arm and the start of the tail
		}},
	}
}

func kernels2() []kernel2 {
	return []kernel2{
		{"Atan2", Atan2, math.Atan2, func(r *sweepRand) (float64, float64) {
			return r.scaled(-40, 40), r.scaled(-40, 40)
		}},
		{"Pow", Pow, math.Pow, func(r *sweepRand) (float64, float64) {
			x := r.positive(-8, 8)
			y := r.scaled(-6, 5)
			if r.next()%4 == 0 { // integer exponents exercise the squaring loop alone
				y = float64(int64(r.next()%61) - 30)
			}
			if r.next()%8 == 0 { // negative base with an integer exponent
				x = -x
				y = float64(int64(r.next()%21) - 10)
			}
			return x, y
		}},
	}
}

func signOf(r *sweepRand) float64 {
	if r.next()&1 == 1 {
		return -1
	}
	return 1
}

// ulpDiff is the distance between a and b in units in the last place,
// counted on the ordered integer line of float64 bit patterns. Two NaNs
// are 0 apart; a NaN and a number are maximally apart.
func ulpDiff(a, b float64) uint64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		if math.IsNaN(a) && math.IsNaN(b) {
			return 0
		}
		return math.MaxUint64
	}
	if a == b { // also +0 == -0
		return 0
	}
	oa, ob := ordered(a), ordered(b)
	if oa > ob {
		return uint64(oa - ob)
	}
	return uint64(ob - oa)
}

func ordered(x float64) int64 {
	b := int64(math.Float64bits(x))
	if b < 0 {
		return math.MinInt64 - b
	}
	return b
}

const sweepN = 200_000

// ulpBound is each kernel's tolerance against math.*. The reference is
// not a single function: math.* differs across architectures and CPUs,
// which is the whole reason detmath exists, so the bound is the spread
// the stdlib itself shows between its own builds, measured on this
// sweep (darwin/arm64 natively, linux/amd64 with FMA under docker):
//
//   - Exp 2: amd64's exp_amd64.s takes a run-time FMA path on CPUs that
//     have it and lands 2 ulp from the pure-Go algorithm (arm64's
//     assembly stays within 1).
//   - Tan 2: arm64 contracts the pure-Go rational kernel; numerator and
//     denominator each move, and the quotient ends 2 ulp away.
//   - Pow 8: the fractional part goes through Exp(yf·Log(x)), so each
//     ulp of Log / Exp disagreement is carried into a1 and then scaled
//     through the repeated-squaring chain and 1/a1, landing where the
//     result's binade magnifies it (worst measured: 6 ulp on arm64, 5
//     on amd64).
//   - Erfinv 8: arm64 contracts the stdlib's degree-7 Horner chains in
//     the |x| ≤ 0.85 arm, and numerator and denominator move together
//     (worst measured: 7 ulp on arm64; 0 on amd64, whose math.Log tail
//     agreed with detmath's on every sweep input).
//   - Erf 1: arm64 contracts the rational arms; on amd64 the |x| ≥ 1.25
//     arms call the FMA-path exp_amd64.s, 1 ulp from detmath.Exp.
//   - every other kernel 1.
var ulpBound = map[string]uint64{
	"Exp": 2, "Log": 1, "Log1p": 1, "Sin": 1, "Cos": 1, "Tan": 2, "Atan2": 1, "Pow": 8,
	"Expm1": 1, "Erf": 1, "Erfinv": 8,
}

// exactOnAMD64 are the kernels whose math.* twin is the very pure-Go
// source detmath ports, compiled without contraction on amd64. There the
// two must agree BIT FOR BIT: any difference is a mis-ported line, not
// rounding, so this is the port-fidelity check CI runs on linux/amd64.
var exactOnAMD64 = map[string]bool{"Expm1": true, "Log1p": true, "Sin": true, "Cos": true, "Tan": true, "Atan2": true}

func boundFor(name string) uint64 {
	if runtime.GOARCH == "amd64" && exactOnAMD64[name] {
		return 0
	}
	return ulpBound[name]
}

// stdlibAsmDefect reports an input where math.* on this build is WRONG,
// not merely a last-ulp away, so the comparison would test the stdlib
// rather than detmath. Both are amd64 assembly:
//
//   - log_amd64.s inlines Frexp without normalising a subnormal, so
//     math.Log(x) for 0 < x < 2^-1022 returns about -709.09 for every
//     such x (math.Log(5e-324) = -709.09; the true value is -744.44).
//   - exp_amd64.s overflows to +Inf from about x = 709.44, below the
//     true threshold 709.78 where e^x is still finite.
//
// detmath follows the pure-Go reference on both; TestDetmath_GoldenBits
// pins its values there (Log(5e-324), Exp(709.5), Exp(709.78)).
func stdlibAsmDefect(name string, x, std float64) bool {
	if runtime.GOARCH != "amd64" {
		return false
	}
	switch name {
	case "Log":
		return x > 0 && x < 0x1p-1022
	case "Exp":
		return math.IsInf(std, 1) && x < 7.09782712893383973096e+02
	}
	return false
}

// TestDetmath_MatchesStdlibWithinULP sweeps every kernel across a fixed
// splitmix64 input stream and requires agreement with math.* within the
// kernel's ulpBound (bit-exact on amd64 for the pure-Go kernels). The
// residual is exactly the fused-multiply-add (arm64) or assembly
// (math.Exp everywhere, math.Log on amd64) difference detmath exists to
// remove; inputs where the stdlib assembly is outright wrong are counted
// and logged, never compared.
func TestDetmath_MatchesStdlibWithinULP(t *testing.T) {
	type outcome struct {
		exact, defects int
		worstArgs      string
		worstDiff      uint64
	}
	report := func(name string, o outcome) {
		bound := boundFor(name)
		t.Logf("%s on %s: %d/%d bit-identical to math.%s, worst %d ulp (bound %d), %d stdlib-defect inputs skipped",
			name, runtime.GOARCH, o.exact, sweepN, name, o.worstDiff, bound, o.defects)
		if o.worstDiff > bound {
			t.Errorf("%s: %s is %d ulp from math.%s (bound %d on %s)",
				name, o.worstArgs, o.worstDiff, name, bound, runtime.GOARCH)
		}
	}
	for i, k := range kernels1() {
		r := &sweepRand{s: uint64(1000 + i)}
		var o outcome
		for range sweepN {
			x := k.inputs(r)
			got, std := k.det(x), k.std(x)
			if stdlibAsmDefect(k.name, x, std) {
				o.defects++
				continue
			}
			d := ulpDiff(got, std)
			if d == 0 {
				o.exact++
			}
			if d > o.worstDiff {
				o.worstDiff = d
				o.worstArgs = fmt.Sprintf("%s(%v) = %v vs %v", k.name, x, got, std)
			}
		}
		report(k.name, o)
	}
	for i, k := range kernels2() {
		r := &sweepRand{s: uint64(2000 + i)}
		var o outcome
		for range sweepN {
			x, y := k.inputs(r)
			got, std := k.det(x, y), k.std(x, y)
			d := ulpDiff(got, std)
			if d == 0 {
				o.exact++
			}
			if d > o.worstDiff {
				o.worstDiff = d
				o.worstArgs = fmt.Sprintf("%s(%v, %v) = %v vs %v", k.name, x, y, got, std)
			}
		}
		report(k.name, o)
	}
}

// isSpecial reports a value whose bits a special-case rule fixes exactly:
// NaN, ±Inf, ±0.
func isSpecial(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0) || v == 0
}

// TestDetmath_SpecialCasesMatchStdlib holds detmath to math.*'s
// documented special-case semantics: on every golden row whose input or
// result is NaN, ±Inf or ±0, the two must agree exactly — NaN-ness, the
// infinity's sign and the zero's sign — except where the stdlib
// assembly is known wrong (stdlibAsmDefect).
func TestDetmath_SpecialCasesMatchStdlib(t *testing.T) {
	std := map[string]func(x, y float64) float64{
		"Exp":    func(x, _ float64) float64 { return math.Exp(x) },
		"Log":    func(x, _ float64) float64 { return math.Log(x) },
		"Log1p":  func(x, _ float64) float64 { return math.Log1p(x) },
		"Sin":    func(x, _ float64) float64 { return math.Sin(x) },
		"Cos":    func(x, _ float64) float64 { return math.Cos(x) },
		"Tan":    func(x, _ float64) float64 { return math.Tan(x) },
		"Atan2":  math.Atan2,
		"Pow":    math.Pow,
		"Expm1":  func(x, _ float64) float64 { return math.Expm1(x) },
		"Erf":    func(x, _ float64) float64 { return math.Erf(x) },
		"Erfinv": func(x, _ float64) float64 { return math.Erfinv(x) },
	}
	checked := 0
	for _, c := range goldenCases {
		got, want := eval(c), std[c.fn](c.x, c.y)
		specialIn := isSpecial(c.x) || (twoArg(c.fn) && isSpecial(c.y))
		if !specialIn && !isSpecial(got) && !isSpecial(want) {
			continue
		}
		if stdlibAsmDefect(c.fn, c.x, want) {
			continue
		}
		checked++
		switch {
		case math.IsNaN(got) || math.IsNaN(want):
			if math.IsNaN(got) != math.IsNaN(want) {
				t.Errorf("%s(%v, %v) = %v, math.%s = %v", c.fn, c.x, c.y, got, c.fn, want)
			}
		case specialIn || math.IsInf(got, 0) || math.IsInf(want, 0):
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("%s(%v, %v) = %v (0x%016x), math.%s = %v (0x%016x)",
					c.fn, c.x, c.y, got, math.Float64bits(got), c.fn, want, math.Float64bits(want))
			}
		default:
			// a finite input whose result rounds to ±0 (Exp(-745.1) is
			// 2.5e-324, nearer the smallest subnormal than zero; arm64's
			// exp assembly flushes it): a rounding edge, so the ulp bound.
			if ulpDiff(got, want) > boundFor(c.fn) {
				t.Errorf("%s(%v, %v) = %v, math.%s = %v", c.fn, c.x, c.y, got, c.fn, want)
			}
		}
	}
	if checked < 60 {
		t.Fatalf("only %d special-case rows checked; the golden table lost its edge cases", checked)
	}
}

// twoArg reports the kernels that read goldenCase.y.
func twoArg(fn string) bool { return fn == "Atan2" || fn == "Pow" }
