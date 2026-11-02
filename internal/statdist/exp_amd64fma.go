package statdist

import "math"

// ExpAMD64FMA is e^x computed by a portable port of the FMA path of Go's
// math/exp_amd64.s (the `avxfma` branch of archExp; byte-identical in
// go1.26.1 and go1.27.2): Shibata's SLEEF-derived method, a two-part
// Cody–Waite reduction by k·ln2, a /16 scaling, a degree-8 Taylor
// polynomial and four (r+2)·r squarings back up.
//
// Why it exists. Stdlib math.Exp is not one function on amd64: archExp
// picks, at run time, between FMA and non-FMA instruction sequences
// (math.useFMA = cpu.X86.HasAVX && cpu.X86.HasFMA), and arm64 has its
// own assembly. So math.Exp's bits depend on the GOARCH and on the x86
// CPU. Every fused step here is an explicit math.FMA, which Go defines
// as the correctly rounded a·b+c on every GOARCH (hardware or software),
// and every other product is a float64(...) barrier, so this function
// returns the FMA-hardware amd64 bits of math.Exp on every architecture
// and CPU. It is NOT detmath.Exp (fdlibm), which differs from either
// stdlib arm by an ulp.
//
// It keeps amd64's quirks too: x in [~709.44, 709.78] (k rounds to 1024)
// returns +Inf although e^x is finite, exactly as math.Exp does there.
//
// Its callers are the REG_GLM logit and poisson links, whose IRLS
// carries an ulp of Exp to ~1e-12 relative in the fitted figures:
// pinning amd64's FMA-path bits keeps CI's (FMA-capable linux/amd64)
// figures where they were while every other arch converges onto them.
// TestStatdist_ExpAMD64FMA pins the bits on every arch and, on an amd64
// CPU with AVX and FMA, checks equality with math.Exp itself.
func ExpAMD64FMA(x float64) float64 {
	const (
		log2e    = 1.4426950408889634073599246810018920
		ln2u     = 0.69314718055966295651160180568695068359375
		ln2l     = 0.28235290563031577122588448175013436025525412068e-12
		overflow = 7.09782712893384e+02
	)
	b := math.Float64bits(x)
	if b&^(1<<63) >= 0x7FF0000000000000 { // NaN or ±Inf
		if b == 0xFFF0000000000000 {
			return 0
		}
		return x
	}
	if x > overflow {
		return math.Inf(1)
	}
	// CVTSD2SL: round to nearest even; out of int32 range it yields the
	// "integer indefinite" MinInt32, which underflows to 0 below.
	t := float64(x * log2e)
	k := int32(math.MinInt32)
	if t >= -2147483648.5 && t < 2147483647.5 {
		k = int32(math.RoundToEven(t))
	}
	kf := float64(k)
	r := math.FMA(-ln2u, kf, x)
	r = math.FMA(-ln2l, kf, r)
	r = float64(r * 0.0625)
	p := 2.4801587301587301587e-5
	p = math.FMA(p, r, 1.9841269841269841270e-4)
	p = math.FMA(p, r, 1.3888888888888888889e-3)
	p = math.FMA(p, r, 8.3333333333333333333e-3)
	p = math.FMA(p, r, 4.1666666666666666667e-2)
	p = math.FMA(p, r, 1.6666666666666666667e-1)
	p = math.FMA(p, r, 0.5)
	p = math.FMA(p, r, 1.0)
	r = float64(r * p)
	for range 3 {
		r = float64(r * (r + 2.0))
	}
	r = math.FMA(r, r+2.0, 1.0)
	// r · 2^k, through the denormal two-step when k + 1023 <= 0.
	e := int64(k) + 0x3FF
	if e <= 0 {
		if e < -52 {
			return 0
		}
		r = float64(r * math.Float64frombits(uint64(e+0x3FE)<<52))
		e = 1
	} else if e >= 0x7FF {
		return math.Inf(1)
	}
	return float64(r * math.Float64frombits(uint64(e)<<52))
}
