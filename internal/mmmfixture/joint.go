package mmmfixture

import (
	"math"
	"math/rand/v2"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// joint.go is the joint-truth scenario (mmm-estimation E4-S2): one
// market, JointWeeks weeks of four channel spends whose sales carry a
// known geometric adstock and Hill curve per channel plus two controls —
// the truth the REG_MMM_HILL / REG_MMM_BAYES operator tests fit.
//
// The truth: TV mid-pack (decay 0.6, shape 2, half-saturation at its
// steady-state adstock), search saturated (0.1, 1; high average, low
// marginal ROI), social underfunded (0.3, 1.5; the highest marginal ROI)
// and display weak (0.2, 1; the lowest ROI).
//
// Spend is independent per channel and per week — TV, search and
// display flighted (on 60%, 75%, 50% of weeks), social always on; an on
// week spends 0.2–1.8× the channel's mean over its on-rate — so both the
// lag structure and the whole curve are informed (dark weeks pin a
// saturated channel's level, which the intercept would otherwise
// absorb). Noise is σ = 2 on sales of about 1,150. Every product
// feeding an addition is float64()-barriered and the transcendentals
// are linalg/detmath's, so the cohort's bits
// are a function of JointSeed alone on every architecture.

// JointCohortFile is the cohort filename the joint-truth bodies name.
const JointCohortFile = "mmm_joint.pulse"

// JointWeeks is the number of weekly rows; JointMaxLag is the true
// adstock's max_lag; JointSeed the generator's PCG seed.
const (
	JointWeeks  = 156
	JointMaxLag = 13
	JointSeed   = 60
)

// JointChannel is one channel's truth. Its contribution in week t is
// Beta · hill(a_t, HalfSat, Shape), a_t = Σ_{l=0..JointMaxLag} Decay^l ·
// spend_{t−l} (WIN_DISTRIBUTED_LAG geometric, normalize false); HalfSat
// is in adstock units. MeanSpend is the generator's nominal mean weekly
// spend.
type JointChannel struct {
	Name      string
	Decay     float64
	Shape     float64
	HalfSat   float64
	Beta      float64
	MeanSpend float64
	// OnRate is the share of weeks the channel spends (1: always on).
	OnRate float64
}

// JointChannels are the four channels, in schema and predictor order.
var JointChannels = []JointChannel{
	{Name: "tv", Decay: 0.6, Shape: 2, HalfSat: 250, Beta: 300, MeanSpend: 100, OnRate: 0.6},
	{Name: "search", Decay: 0.1, Shape: 1, HalfSat: 18, Beta: 160, MeanSpend: 40, OnRate: 0.75},
	{Name: "social", Decay: 0.3, Shape: 1.5, HalfSat: 180, Beta: 400, MeanSpend: 50, OnRate: 1},
	{Name: "display", Decay: 0.2, Shape: 1, HalfSat: 75, Beta: 100, MeanSpend: 60, OnRate: 0.5},
}

// The controls and noise: sales = JointIntercept + Σ contributions +
// JointPriceEffect · price + JointPromoEffect · promo + N(0, JointNoise²).
const (
	JointIntercept   = 1000
	JointPriceEffect = -20
	JointPromoEffect = 40
	JointNoise       = 2
)

// JointData is the generated cohort and its stored truth.
type JointData struct {
	// Rows are the cohort rows in Schema field order
	// (pulse.CohortRow-compatible), week order.
	Rows [][]any
	// Spend and Contribution are each channel's weekly spend and true
	// sales contribution, by channel name, week order.
	Spend        map[string][]float64
	Contribution map[string][]float64
}

// JointSchema is the joint-truth cohort schema: week, the four channel
// spends, price, promo and sales.
func JointSchema() encoding.Schema {
	f64 := func(name, desc string) encoding.Field {
		return encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: desc}
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number since the start of the series"},
		f64("tv", "Weekly television spend in thousands of dollars"),
		f64("search", "Weekly paid search spend in thousands of dollars"),
		f64("social", "Weekly paid social spend in thousands of dollars"),
		f64("display", "Weekly display advertising spend in thousands of dollars"),
		f64("price", "Average shelf price in dollars"),
		f64("promo", "One when a promotion ran that week, else zero"),
		f64("sales", "Weekly sales revenue in thousands of dollars"),
	}}
}

// JointHill is the Hill curve the truth uses, x^s / (x^s + k^s), as
// 1 / (1 + (k/x)^s) through detmath.Pow — the form hill() and the pack
// compute; 0 at x ≤ 0.
func JointHill(x, k, s float64) float64 {
	if x <= 0 {
		return 0
	}
	return 1 / (1 + detmath.Pow(k/x, s))
}

// Joint generates the joint-truth cohort (deterministic: JointSeed).
func Joint() JointData { return JointWithSeed(JointSeed) }

// JointWithSeed generates the joint-truth scenario — the same channels,
// curves, controls and noise — from generator seed seed: fresh spend,
// price, promo and noise draws over the same truth. JointWithSeed(
// JointSeed) is Joint(). A repeated-simulation study draws its
// replicates from it; the bits are a function of seed alone on every
// architecture, as Joint's are.
func JointWithSeed(seed uint64) JointData {
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	n := JointWeeks
	d := JointData{Spend: map[string][]float64{}, Contribution: map[string][]float64{}}
	for _, c := range JointChannels {
		x := make([]float64, n)
		for w := range x {
			on := rng.Float64() < c.OnRate
			u := float64(rng.Float64()*1.6) + 0.2
			if on {
				x[w] = float64(c.MeanSpend / c.OnRate * u)
			}
		}
		d.Spend[c.Name] = x
	}
	price, promo := make([]float64, n), make([]float64, n)
	for w := range price {
		price[w] = 10 + float64(0.6*detmath.Sin(float64(2*math.Pi*float64(w))/52)) + float64(0.3*(float64(rng.Float64())-0.5))
		if rng.Float64() < 0.12 {
			promo[w] = 1
		}
	}
	for _, c := range JointChannels {
		x, contrib := d.Spend[c.Name], make([]float64, n)
		for t := range x {
			a, wt := 0.0, 1.0
			for l := 0; l <= JointMaxLag && t-l >= 0; l++ {
				a += float64(wt * x[t-l])
				wt = float64(wt * c.Decay)
			}
			contrib[t] = float64(c.Beta * JointHill(a, c.HalfSat, c.Shape))
		}
		d.Contribution[c.Name] = contrib
	}
	for t := range n {
		sales := float64(JointIntercept)
		for _, c := range JointChannels {
			sales += d.Contribution[c.Name][t]
		}
		sales += float64(JointPriceEffect*price[t]) + float64(JointPromoEffect*promo[t]) + float64(JointNoise*detmath.NormFloat64(rng))
		row := []any{uint64(t + 1)}
		for _, c := range JointChannels {
			row = append(row, d.Spend[c.Name][t])
		}
		d.Rows = append(d.Rows, append(row, price[t], promo[t], sales))
	}
	return d
}

// TrueROI is channel's true return on spend over the whole series: its
// summed true contribution over its summed spend.
func (d JointData) TrueROI(channel string) float64 {
	c, s := 0.0, 0.0
	for t := range d.Spend[channel] {
		c += d.Contribution[channel][t]
		s += d.Spend[channel][t]
	}
	return c / s
}
