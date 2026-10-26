package mmmfixture

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// joint.go is the joint-truth scenario (mmm-estimation E4-S2): one
// market, JointWeeks weeks of four channel spends whose sales carry a
// known geometric adstock and Hill curve per channel plus two controls —
// the truth the pack's REG_MMM_HILL acceptance must recover — and the
// canonical two-call request bodies over it:
//
//  1. JointLHSSweep: an `lhs` sweep over the four channel decays, one
//     REG_MMM_HILL per slot with a holdout, ranked by holdout RMSE;
//  2. JointFitOptimize: `fits` (the final REG_MMM_HILL at the winning
//     decays, every week), a slot writing each week's fitted channel
//     contributions (coef() + hill(); summed over the weeks and divided
//     by the summed spend, each channel's ROI), and an `optimize` block
//     reallocating the current weekly budget across the channels within
//     ±50% of each channel's current spend.
//
// The second body is a template: its {"$var": "<name>"} placeholders
// (the winning decays and the data-derived budget, bounds and
// steady-state multipliers) are filled by Fill with JointVars, because a
// sweep winner is not bindable as a fit (the two-call flow).
//
// The truth: TV mid-pack (decay 0.6, shape 2, half-saturation at its
// steady-state adstock), search saturated (0.1, 1; high average, low
// marginal ROI), social underfunded (0.3, 1.5; the highest marginal ROI)
// and display weak (0.2, 1; the lowest ROI). On the true curves the
// optimal reallocation raises social to its +50% bound, cuts display to
// its −50% bound and leaves TV and search strictly inside theirs with
// equal marginal ROI.
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

// JointWeeks is the number of weekly rows; JointHoldoutFrom the first
// held-out week of the sweep (`week >= 131`, the last 26 weeks).
// JointMaxLag is every adstock window's max_lag; JointSeed the
// generator's PCG seed; JointLHSSamples JointLHSSweep's samples.
const (
	JointLHSSamples  = 2000
	JointWeeks       = 156
	JointHoldoutFrom = 131
	JointMaxLag      = 13
	JointSeed        = 60
)

// JointChannel is one channel's truth. Its contribution in week t is
// Beta · hill(a_t, HalfSat, Shape), a_t = Σ_{l=0..JointMaxLag} Decay^l ·
// spend_{t−l} (WIN_DISTRIBUTED_LAG geometric, normalize false); HalfSat
// is in adstock units. MeanSpend is the generator's nominal mean weekly
// spend (the realized mean is JointData.MeanSpend).
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

// JointLHSSweep is call 1: the lhs sweep of JointLHSSamples slots over
// the four decays (axes tv, search, social, display; each slot's
// default label is sample=<i>_tv=<v>_search=<v>_social=<v>_display=<v>),
// REG_MMM_HILL per slot with holdout `week >= 131`, ranked by
// regressions.mmm.holdout.rmse ascending. Each axis range is a
// per-channel prior, as a practitioner sets one: TV [0.3, 0.8] (a
// long-memory medium), the three digital channels [0, 0.4]. Its rng_seed
// is absent: the seed derives from the spec, so the body always draws
// the same slots. JointLHSSamples is above the default MaxComposeSlots
// (1000): a caller raises Options.Limits.MaxComposeSlots to run it.
//
//go:embed testdata/joint_lhs_sweep.json
var JointLHSSweep []byte

// JointFitOptimize is call 2, a template for Fill: the final fit at the
// winning decays, the contribution slot and the budget optimisation.
//
//go:embed testdata/joint_fit_optimize.json
var JointFitOptimize []byte

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

// SteadyState is the adstock a constant weekly spend of 1 settles to
// under a geometric decay: Σ_{l=0..JointMaxLag} decay^l.
func SteadyState(decay float64) float64 {
	m, w := 0.0, 1.0
	for range JointMaxLag + 1 {
		m += w
		w = float64(w * decay)
	}
	return m
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
		price[w] = 10 + float64(0.6*detmath.Sin(float64(2*math.Pi*float64(w))/52)) + float64(0.3*(rng.Float64()-0.5))
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

// MeanSpend is channel's realized mean weekly spend — the current
// allocation the optimisation starts from.
func (d JointData) MeanSpend(channel string) float64 {
	s := 0.0
	for _, v := range d.Spend[channel] {
		s += v
	}
	return s / float64(len(d.Spend[channel]))
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

// JointVars are the variables of JointFitOptimize for winning decays
// (by channel name): decay_<c> (each adstock window's decay), mult_<c>
// (SteadyState of that decay: a constant weekly spend x adstocks to
// mult·x, so the objective evaluates each fitted curve at steady
// state), mean_<c> (the current spend, each variable's start), lo_<c> /
// hi_<c> (0.5 / 1.5 × the current spend) and budget (the current total
// weekly spend).
func (d JointData) JointVars(decays map[string]float64) map[string]float64 {
	v := map[string]float64{}
	budget := 0.0
	for _, c := range JointChannels {
		mean := d.MeanSpend(c.Name)
		budget += mean
		v["decay_"+c.Name] = decays[c.Name]
		v["mult_"+c.Name] = SteadyState(decays[c.Name])
		v["mean_"+c.Name] = mean
		v["lo_"+c.Name] = float64(0.5 * mean)
		v["hi_"+c.Name] = float64(1.5 * mean)
	}
	v["budget"] = budget
	return v
}

// TrueResponse is the true steady-state weekly sales response to a
// weekly allocation x (by channel name): Σ_c β_c · hill(m_c·x_c, k_c,
// s_c) with m_c the true decay's SteadyState.
func TrueResponse(x map[string]float64) float64 {
	f := 0.0
	for _, c := range JointChannels {
		f += c.Beta * JointHill(SteadyState(c.Decay)*x[c.Name], c.HalfSat, c.Shape)
	}
	return f
}

// TrueOptimum is the oracle: the allocation maximising TrueResponse with
// the budget spent and every channel within [lo, hi] of JointVars,
// found independently of Pulse's solver — the Lagrangian dual, a
// bisection on the shadow price λ where each channel takes the spend
// maximising β·hill(m·x) − λ·x over a fine grid of its bounds (refined by
// golden section), so a non-concave stretch of an S-curve is handled.
// It returns the allocation, λ and the lift over the current spend.
func (d JointData) TrueOptimum() (alloc map[string]float64, lambda, lift float64) {
	v := d.JointVars(map[string]float64{})
	best := func(c JointChannel, lam float64) float64 {
		m, lo, hi := SteadyState(c.Decay), v["lo_"+c.Name], v["hi_"+c.Name]
		g := func(x float64) float64 { return c.Beta*JointHill(m*x, c.HalfSat, c.Shape) - lam*x }
		const n = 4000
		bx, bg := lo, g(lo)
		for i := 1; i <= n; i++ {
			x := lo + (hi-lo)*float64(i)/n
			if gx := g(x); gx > bg {
				bx, bg = x, gx
			}
		}
		a, b := max(lo, bx-(hi-lo)/n), min(hi, bx+(hi-lo)/n)
		for range 100 {
			m1, m2 := b-(b-a)*0.618033988749895, a+(b-a)*0.618033988749895
			if g(m1) < g(m2) {
				a = m1
			} else {
				b = m2
			}
		}
		return (a + b) / 2
	}
	allocate := func(lam float64) (map[string]float64, float64) {
		x, sum := map[string]float64{}, 0.0
		for _, c := range JointChannels {
			x[c.Name] = best(c, lam)
			sum += x[c.Name]
		}
		return x, sum
	}
	lo, hi := 0.0, 100.0
	for range 200 {
		lambda = (lo + hi) / 2
		if _, s := allocate(lambda); s > v["budget"] {
			lo = lambda
		} else {
			hi = lambda
		}
	}
	alloc, _ = allocate(lambda)
	cur := map[string]float64{}
	for _, c := range JointChannels {
		cur[c.Name] = v["mean_"+c.Name]
	}
	return alloc, lambda, TrueResponse(alloc) - TrueResponse(cur)
}

// Fill substitutes every {"$var": "<name>"} object in body by vars[name]
// (a JSON number); a placeholder with no variable, or a variable with
// no placeholder, is an error.
func Fill(body []byte, vars map[string]float64) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	var missing []string
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			if name, ok := t["$var"].(string); ok && len(t) == 1 {
				val, ok := vars[name]
				if !ok {
					missing = append(missing, name)
					return t
				}
				used[name] = true
				return val
			}
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	v = walk(v)
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("mmmfixture: no variable for placeholder(s) %v", slices.Compact(missing))
	}
	for name := range vars {
		if !used[name] {
			return nil, fmt.Errorf("mmmfixture: variable %q has no placeholder", name)
		}
	}
	return json.Marshal(v)
}
