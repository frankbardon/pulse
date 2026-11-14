package regmmm

import (
	"math/rand/v2"

	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// bayes_geo_fixture_test.go is the simulated geo panel the geo tests
// read: geoSimGeos geos (codes 1, 2, 3), geoSimWeeks weeks each, two
// channels (tv, search) with geometric adstock and one shared Hill curve
// per channel, a per-geo baseline and Gaussian noise. Each geo's tv decay
// and both channels' β differ (the partial pooling's targets); every
// product feeding an add is barriered and the normal draws are detmath's,
// so the panel's bits are the seed's alone on every architecture.

const (
	geoSimGeos  = 3
	geoSimWeeks = 104
)

// geoSimTruth is one geo's true parameters.
type geoSimTruth struct {
	base             float64
	tvDecay, srDecay float64
	tvBeta, srBeta   float64
}

// The truth: tv's decay 0.4 – 0.6 and β 60 – 90 across the geos, search
// fast-decaying with β 30 – 40; Hill k = 150 (tv) and 60 (search) in raw
// adstock units, s = 1.5 and 1.2.
var geoSimTruths = [geoSimGeos]geoSimTruth{
	{base: 200, tvDecay: 0.4, srDecay: 0.2, tvBeta: 60, srBeta: 30},
	{base: 260, tvDecay: 0.5, srDecay: 0.25, tvBeta: 75, srBeta: 35},
	{base: 320, tvDecay: 0.6, srDecay: 0.3, tvBeta: 90, srBeta: 40},
}

const (
	geoSimTVK, geoSimTVS = 150.0, 1.5
	geoSimSRK, geoSimSRS = 60.0, 1.2
	geoSimNoise          = 4.0
)

// geoSimRow is one geo-week.
type geoSimRow struct {
	geo, week         float64
	tv, search, sales float64
}

// geoSim is the panel and each geo's true ROI per channel (summed
// contribution over summed spend), [geo][tv, search], and the all-geo
// truth.
type geoSim struct {
	rows      []geoSimRow
	roi       [geoSimGeos][2]float64
	pooledROI [2]float64
}

// geoSimAdstock is the geometric adstock of x at decay over lags
// 0..defaultMaxLag (lag 0 first, products barriered).
func geoSimAdstock(x []float64, decay float64) []float64 {
	a := make([]float64, len(x))
	for t := range x {
		for l := 0; l <= defaultMaxLag && l <= t; l++ {
			a[t] += float64(detmath.Pow(decay, float64(l)) * x[t-l])
		}
	}
	return a
}

// simulateGeoPanel draws the panel from seed.
func simulateGeoPanel(seed uint64) geoSim {
	rng := rand.New(rand.NewPCG(seed, 0x67656f70616e656c))
	var sim geoSim
	var contrib, spendAll [2]float64
	for g, tr := range geoSimTruths {
		tv, sr := make([]float64, geoSimWeeks), make([]float64, geoSimWeeks)
		for w := range tv {
			if rng.Float64() < 0.7 {
				tv[w] = float64(rng.Float64()*200) + 30
			}
			if rng.Float64() < 0.6 {
				sr[w] = float64(rng.Float64()*90) + 10
			}
		}
		atv, asr := geoSimAdstock(tv, tr.tvDecay), geoSimAdstock(sr, tr.srDecay)
		var c, s [2]float64
		for w := range tv {
			htv := mmmfixture.JointHill(atv[w], geoSimTVK, geoSimTVS)
			hsr := mmmfixture.JointHill(asr[w], geoSimSRK, geoSimSRS)
			c[0] += float64(tr.tvBeta * htv)
			c[1] += float64(tr.srBeta * hsr)
			s[0] += tv[w]
			s[1] += sr[w]
			y := tr.base + float64(tr.tvBeta*htv) + float64(tr.srBeta*hsr) + float64(geoSimNoise*detmath.NormFloat64(rng))
			sim.rows = append(sim.rows, geoSimRow{geo: float64(g + 1), week: float64(w + 1), tv: tv[w], search: sr[w], sales: y})
		}
		for ch := range 2 {
			sim.roi[g][ch] = c[ch] / s[ch]
			contrib[ch] += c[ch]
			spendAll[ch] += s[ch]
		}
	}
	for ch := range 2 {
		sim.pooledROI[ch] = contrib[ch] / spendAll[ch]
	}
	return sim
}

// probe is the panel as probe rows (the geo a numeric code).
func (s geoSim) probe() probeRows {
	out := make(probeRows, len(s.rows))
	for i, r := range s.rows {
		out[i] = probeRow{"geo": r.geo, "week": r.week, "tv": r.tv, "search": r.search, "sales": r.sales}
	}
	return out
}
