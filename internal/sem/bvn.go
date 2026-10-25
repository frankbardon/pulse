package sem

import (
	"math"

	"github.com/frankbardon/pulse/internal/detmath"
)

// The bivariate-normal CDF: a line-for-line, FMA-free port of Alan
// Genz's MVBVU (mvtdstpack.f — the routine R's pbivnorm, and through it
// lavaan's polychoric correlations, call), after Drezner & Wesolowsky
// (1989) with Genz and Ge's double-precision extensions for |ρ| near 1.
// Gauss–Legendre rules of 6, 12 and 20 points over Sheppard's arcsine
// integral for |ρ| < 0.925, and the Drezner–Wesolowsky expansion about
// |ρ| = 1 beyond. Its oracle is testdata/bvn_reference.json (R's
// pbivnorm, and an independent adaptive quadrature of Sheppard's
// integral), held to 1e-12 absolute (TestPolychoric_BVNMatchesReference).

// bvnW / bvnX are the Gauss–Legendre half-rules of MVBVU's DATA blocks
// (N = 6, 12, 20: 3, 6 and 10 points each).
var (
	bvnW = [3][10]float64{
		{0.1713244923791705, 0.3607615730481384, 0.4679139345726904},
		{0.4717533638651177e-01, 0.1069393259953183, 0.1600783285433464,
			0.2031674267230659, 0.2334925365383547, 0.2491470458134029},
		{0.1761400713915212e-01, 0.4060142980038694e-01, 0.6267204833410906e-01,
			0.8327674157670475e-01, 0.1019301198172404, 0.1181945319615184,
			0.1316886384491766, 0.1420961093183821, 0.1491729864726037,
			0.1527533871307259},
	}
	bvnX = [3][10]float64{
		{-0.9324695142031522, -0.6612093864662647, -0.2386191860831970},
		{-0.9815606342467191, -0.9041172563704750, -0.7699026741943050,
			-0.5873179542866171, -0.3678314989981802, -0.1252334085114692},
		{-0.9931285991850949, -0.9639719272779138, -0.9122344282513259,
			-0.8391169718222188, -0.7463319064601508, -0.6360536807265150,
			-0.5108670019508271, -0.3737060887154196, -0.2277858511416451,
			-0.7652652113349733e-01},
	}
)

// BivariateNormalCDF is P(X ≤ h, Y ≤ k) for a standard bivariate normal
// pair with correlation r (pbivnorm(h, k, r)). An infinite limit reduces
// exactly to the univariate CDF (or 0); |r| > 1 or a NaN argument is
// NaN.
func BivariateNormalCDF(h, k, r float64) float64 {
	switch {
	case math.IsNaN(h) || math.IsNaN(k) || math.IsNaN(r) || math.Abs(r) > 1:
		return math.NaN()
	case math.IsInf(h, -1) || math.IsInf(k, -1):
		return 0
	case math.IsInf(h, 1) && math.IsInf(k, 1):
		return 1
	case math.IsInf(h, 1):
		return stdNormCDF(k)
	case math.IsInf(k, 1):
		return stdNormCDF(h)
	}
	return bvnUpper(-h, -k, r)
}

// bvnUpper is MVBVU: P(X > sh, Y > sk) for correlation r, both limits
// finite.
func bvnUpper(sh, sk, r float64) float64 {
	const twoPi = 6.283185307179586
	var ng, lg int
	switch ar := math.Abs(r); {
	case ar < 0.3:
		ng, lg = 0, 3
	case ar < 0.75:
		ng, lg = 1, 6
	default:
		ng, lg = 2, 10
	}
	h, k := sh, sk
	hk := float64(h * k)
	bvn := 0.0
	if math.Abs(r) < 0.925 {
		hs := float64((float64(h*h) + float64(k*k)) / 2)
		asr := detmath.Asin(r)
		for i := 0; i < lg; i++ {
			sn := detmath.Sin(float64(asr*(bvnX[ng][i]+1)) / 2)
			bvn += float64(bvnW[ng][i] * detmath.Exp((float64(sn*hk)-hs)/(1-float64(sn*sn))))
			sn = detmath.Sin(float64(asr*(-bvnX[ng][i]+1)) / 2)
			bvn += float64(bvnW[ng][i] * detmath.Exp((float64(sn*hk)-hs)/(1-float64(sn*sn))))
		}
		return float64(float64(bvn*asr)/(2*twoPi)) + float64(stdNormCDF(-h)*stdNormCDF(-k))
	}
	if r < 0 {
		k = -k
		hk = -hk
	}
	if math.Abs(r) < 1 {
		as := float64((1 - r) * (1 + r))
		a := math.Sqrt(as)
		bs := float64((h - k) * (h - k))
		c := (4 - hk) / 8
		d := (12 - hk) / 16
		// A*EXP(-(BS/AS+HK)/2)*(1 - C*(BS-AS)*(1-D*BS/5)/3 + C*D*AS*AS/5)
		poly := 1 - float64(float64(c*(bs-as))*(1-float64(d*bs)/5))/3 + float64(float64(float64(c*d)*as)*as)/5
		bvn = float64(float64(a*detmath.Exp(-(bs/as+hk)/2)) * poly)
		if hk > -160 {
			b := math.Sqrt(bs)
			// EXP(-HK/2)*SQRT(TWOPI)*MVPHI(-B/A)*B*(1 - C*BS*(1 - D*BS/5)/3)
			tail := 1 - float64(float64(c*bs)*(1-float64(d*bs)/5))/3
			bvn -= float64(float64(float64(float64(detmath.Exp(-hk/2)*math.Sqrt(twoPi))*stdNormCDF(-b/a))*b) * tail)
		}
		a /= 2
		for i := 0; i < lg; i++ {
			xs := float64(a * (bvnX[ng][i] + 1))
			xs = float64(xs * xs)
			rs := math.Sqrt(1 - xs)
			e1 := detmath.Exp(-bs/float64(2*xs)-hk/(1+rs)) / rs
			e2 := float64(detmath.Exp(-(bs/xs+hk)/2) * (1 + float64(float64(c*xs)*(1+float64(d*xs)))))
			bvn += float64(float64(a*bvnW[ng][i]) * (e1 - e2))
			om := -bvnX[ng][i] + 1
			xs = float64(float64(as*float64(om*om)) / 4)
			rs = math.Sqrt(1 - xs)
			e3 := detmath.Exp(-(bs/xs + hk) / 2)
			e4 := detmath.Exp(-float64(hk*(1-rs))/float64(2*(1+rs))) / rs
			bvn += float64(float64(float64(a*bvnW[ng][i])*e3) * (e4 - (1 + float64(float64(c*xs)*(1+float64(d*xs))))))
		}
		bvn = -bvn / twoPi
	}
	if r > 0 {
		bvn += stdNormCDF(-math.Max(h, k))
	}
	if r < 0 {
		bvn = -bvn + math.Max(0, stdNormCDF(-h)-stdNormCDF(-k))
	}
	return bvn
}

// bvnRhoLimit is lav_dbinorm's clamp: the density is evaluated at
// |ρ| ≤ 0.9999.
const bvnRhoLimit = 0.9999

// bvnDensity is the standard bivariate normal density φ₂(u, v; ρ) as
// lavaan's lav_dbinorm evaluates it (|ρ| clamped to 0.9999).
func bvnDensity(u, v, rho float64) float64 {
	if math.Abs(rho) > bvnRhoLimit {
		rho = math.Copysign(bvnRhoLimit, rho)
	}
	r := 1 - float64(rho*rho)
	q := float64(u*u) - float64(float64(2*rho)*float64(u*v)) + float64(v*v)
	return float64(float64(1/float64(float64(2*math.Pi)*math.Sqrt(r))) * detmath.Exp(float64(-0.5*q)/r))
}

// bvnGuv is lavaan's lav_dbinorm_guv: ∂ log φ₂(u, v; ρ) / ∂ρ (no clamp).
func bvnGuv(u, v, rho float64) float64 {
	r := 1 - float64(rho*rho)
	q := float64(u*u) - float64(float64(2*rho)*float64(u*v)) + float64(v*v)
	return (float64(float64(u*v)*r) - float64(rho*q) + float64(rho*r)) / float64(r*r)
}
