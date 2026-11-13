package regmmm

import (
	"fmt"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// bayes_lift.go is REG_MMM_BAYES's lift-test likelihood
// (params.lift_tests): a measured experiment enters as data,
//
//	lift ~ Normal(Δ(window, spend + spend_delta), se),
//
// with Δ the model's own incremental outcome over the experiment's
// window — that channel's adstock and curve re-run with the spend
// changed over [start, end], the change summed over the window's rows
// only (the carry-over tail past end is excluded, as an experiment's
// readout window excludes it):
//
//	Δ = max|y| · β_c · Σ_{t=start..end} (h_c(ã′_t) − h_c(ã_t)),
//
// ã′ the scaled adstock of the changed spend (the channel's own peak
// spend scaling, so the curve is the fitted one). spend_delta is the
// TOTAL spend change over the window, spread across its periods in
// proportion to the channel's spend there (spend · (1 + spend_delta /
// window spend): −window spend is the channel switched off), or evenly
// over a window that never spent. lift and se are in the outcome's
// units, lift the outcome with the change minus the outcome without it
// (a switch-off lift is negative). The term and its analytic gradient
// are part of the log density, so the MAP start and NUTS see them.

// liftTestWire is one params.lift_tests entry.
type liftTestWire struct {
	Channel    string   `json:"channel"`
	Geo        *string  `json:"geo"`
	Start      *int64   `json:"start"`
	End        *int64   `json:"end"`
	SpendDelta *float64 `json:"spend_delta"`
	Lift       *float64 `json:"lift"`
	SE         *float64 `json:"se"`
}

// bayesLiftSpec is one validated lift test, its window still in
// params.time units.
type bayesLiftSpec struct {
	channel    int
	start, end int64
	spendDelta float64
	lift, se   float64
}

// The lift-test refusals' details.reason.
const (
	reasonLiftGeo              = "lift_geo_unsupported"
	reasonLiftChannel          = "lift_channel_unknown"
	reasonLiftWindowMissing    = "lift_window_missing"
	reasonLiftWindowInvalid    = "lift_window_invalid"
	reasonLiftSpendDeltaAbsent = "lift_spend_delta_missing"
	reasonLiftSpendDeltaZero   = "lift_spend_delta_zero"
	reasonLiftMissing          = "lift_missing"
	reasonLiftSE               = "lift_se_invalid"
	reasonLiftWindowRange      = "lift_window_out_of_range"
	reasonLiftSpendNegative    = "lift_spend_negative"
)

// liftSpecs validates params.lift_tests against the slot's channels:
// spec-only, so predict raises the same refusal. A test with geo is
// refused (the model fits one market); a channel outside
// params.channels, a missing or reversed window, a missing or zero
// spend_delta, a missing lift and an se that is missing or not positive
// are PROCESSING_CONFIG (slot params.lift_tests, details index and
// reason).
func liftSpecs(spec *types.RegressionSpec, tests []liftTestWire, channels []string) ([]bayesLiftSpec, error) {
	var out []bayesLiftSpec
	for i, w := range tests {
		refuse := func(reason, msg string, extra map[string]any) error {
			details := map[string]any{"index": i, "reason": reason}
			for k, v := range extra {
				details[k] = v
			}
			return bayesSlotError(spec, "params.lift_tests", fmt.Sprintf("params.lift_tests[%d]: %s", i, msg), details)
		}
		if w.Geo != nil {
			return nil, refuse(reasonLiftGeo, "geo applies only to a geo model; this model fits one market's series — drop geo", nil)
		}
		c := -1
		for j, name := range channels {
			if name == w.Channel {
				c = j
			}
		}
		if c < 0 {
			return nil, refuse(reasonLiftChannel, fmt.Sprintf("channel %q is not one of params.channels", w.Channel), map[string]any{"channel": w.Channel})
		}
		if w.Start == nil || w.End == nil {
			return nil, refuse(reasonLiftWindowMissing, "start and end (the experiment's first and last params.time, inclusive) are required", nil)
		}
		if *w.Start > *w.End {
			return nil, refuse(reasonLiftWindowInvalid, fmt.Sprintf("start %d is after end %d", *w.Start, *w.End), map[string]any{"start": *w.Start, "end": *w.End})
		}
		if w.SpendDelta == nil {
			return nil, refuse(reasonLiftSpendDeltaAbsent, "spend_delta (the experiment's total spend change over the window; the channel switched off is minus its window spend) is required", nil)
		}
		if *w.SpendDelta == 0 {
			return nil, refuse(reasonLiftSpendDeltaZero, "spend_delta is 0: a test that changes no spend measures nothing", nil)
		}
		if w.Lift == nil {
			return nil, refuse(reasonLiftMissing, "lift (the measured change in the outcome over the window, in outcome units) is required", nil)
		}
		if w.SE == nil || !(*w.SE > 0) {
			return nil, refuse(reasonLiftSE, "se (the lift's standard error, in outcome units) must be > 0", nil)
		}
		out = append(out, bayesLiftSpec{channel: c, start: *w.Start, end: *w.End, spendDelta: *w.SpendDelta, lift: *w.Lift, se: *w.SE})
	}
	return out, nil
}

// liftPriorOverlap is every channel (params.channels order) with both an
// explicit roi prior and a lift test.
func liftPriorOverlap(lifts []bayesLiftSpec, channels []string, priors map[string]channelPriorWire) []string {
	var out []string
	for c, name := range channels {
		if p, ok := priors[name]; !ok || p.ROI == nil {
			continue
		}
		for _, l := range lifts {
			if l.channel == c {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// liftTest is one lift test on the fitted rows: the channel, the window
// as row indices [from, to] (inclusive), the channel's raw spend over
// rows 0..to with the experiment's change applied, and the measured
// lift and its standard error in original units.
type liftTest struct {
	channel  int
	from, to int
	spend    []float64
	lift, se float64
}

// liftSpend is x over rows 0..to with spend_delta applied over [from,
// to]: each period scaled by 1 + delta / window spend, or, when the
// window never spent, delta / periods added to each. A change that
// takes more than the window's spend is refused (the factor is clamped
// to 0 within 1e-9 of −1, so a switch-off computed from rounded figures
// lands on exactly zero spend).
func liftSpend(x []float64, from, to int, delta float64) ([]float64, error) {
	out := make([]float64, to+1)
	copy(out, x[:to+1])
	total := 0.0
	for t := from; t <= to; t++ {
		total += x[t]
	}
	if total > 0 {
		f := 1 + delta/total
		if f < 0 {
			if f < -1e-9 {
				return nil, fmt.Errorf("spend_delta %v takes more than the window's spend %v", delta, total)
			}
			f = 0
		}
		for t := from; t <= to; t++ {
			out[t] = float64(x[t] * f)
		}
		return out, nil
	}
	if delta < 0 {
		return nil, fmt.Errorf("spend_delta %v is negative but the channel spends nothing in the window", delta)
	}
	per := delta / float64(to-from+1)
	for t := from; t <= to; t++ {
		out[t] = per
	}
	return out, nil
}

// newLiftTest resolves one lift test onto the model's rows (window rows
// from..to); a window outside the rows or a change below zero spend is
// PROCESSING_CONFIG.
func (m *bayesModel) newLiftTest(channel, from, to int, delta, lift, se float64) (liftTest, error) {
	if channel < 0 || channel >= m.nc || from < 0 || to < from || to >= m.n || !(se > 0) || !finite(lift) {
		return liftTest{}, bayesConfigError(fmt.Sprintf("REG_MMM_BAYES: lift test on channel %d rows [%d, %d] is outside the model (%d rows, %d channels) or has no positive se",
			channel, from, to, m.n, m.nc), map[string]any{"reason": reasonLiftWindowRange})
	}
	sp, err := liftSpend(m.spend[channel], from, to, delta)
	if err != nil {
		return liftTest{}, bayesConfigError("REG_MMM_BAYES: "+err.Error(), map[string]any{"reason": reasonLiftSpendNegative})
	}
	return liftTest{channel: channel, from: from, to: to, spend: sp, lift: lift, se: se}, nil
}

// liftDelta is Σ_{t∈window} (h′_t − h_t) and, with grad, its partials
// in each kernel parameter (dp) and curve parameter (dq), at the
// channel state st (the fitted spend's curve with its partials) and the
// constrained kernel parameters ad.
func (m *bayesModel) liftDelta(l liftTest, st channelState, ad []float64, grad bool) (d float64, dp, dq []float64) {
	sp := m.curveOn(l.spend, l.channel, ad, st.q, grad)
	if grad {
		dp, dq = make([]float64, m.np), make([]float64, m.nq)
	}
	for t := l.from; t <= l.to; t++ {
		d += sp.h[t] - st.h[t]
		for i := range dp {
			dp[i] += sp.dhp[i][t] - st.dhp[i][t]
		}
		for i := range dq {
			dq[i] += sp.dhq[i][t] - st.dhq[i][t]
		}
	}
	return d, dp, dq
}

// liftTerm is lift test l's log likelihood, −½·((lift − Δ)/se)² (the
// −log se constant dropped), with its partials added through add: Δ =
// max|y|·β·D on the channel's state st, β derived in ROI mode
// (β = ROI·share/Σh, so a kernel or curve parameter moves Δ through Σh
// too) or sampled in β mode; e is the channel's constrained effect.
func (m *bayesModel) liftTerm(l liftTest, st channelState, ad []float64, e float64, add func(int, float64)) float64 {
	c := l.channel
	d, dp, dq := m.liftDelta(l, st, ad, true)
	bd := float64(st.beta * d)
	r := (l.lift - float64(m.yScale*bd)) / l.se
	// ∂/∂(β·D) of −½r², on the scaled outcome.
	g := float64(r*m.yScale) / l.se
	if m.isBeta[c] {
		add(m.iEffect(c), float64(g*d))
	} else {
		add(m.iEffect(c), float64(g*bd)/e)
	}
	// ∂(β·D)/∂φ = β·∂D/∂φ + D·∂β/∂φ, with ∂β/∂φ = −β·Σ_t ∂h_t/∂φ / Σh in
	// ROI mode and 0 in β mode (likewise for the curve's parameters).
	sum := func(v []float64) float64 {
		s := 0.0
		for _, x := range v {
			s += x
		}
		return s
	}
	for i, v := range dp {
		dd := float64(st.beta * v)
		if !m.isBeta[c] {
			dd -= float64(bd*sum(st.dhp[i])) / st.hSum
		}
		add(m.iAdstock(i, c), float64(g*dd))
	}
	for i, v := range dq {
		dd := float64(st.beta * v)
		if !m.isBeta[c] {
			dd -= float64(bd*sum(st.dhq[i])) / st.hSum
		}
		add(m.iCurve(i, c), float64(g*dd))
	}
	return -float64(0.5 * float64(r*r))
}

// liftPrediction is lift test i's Δ at θ in original units (the
// model's figure the measured lift is compared with).
func (m *bayesModel) liftPrediction(theta []float64, i int) float64 {
	l := m.lifts[i]
	v := make([]float64, m.Dim())
	for j := range v {
		v[j] = m.priors[j].transform().Forward(theta[j]).Y
	}
	ad, q := m.adstockParams(v, l.channel), m.curveParams(v, l.channel)
	st := m.curve(l.channel, ad, q, false)
	b := v[m.iEffect(l.channel)]
	if !m.isBeta[l.channel] {
		b = float64(b*m.spendShare[l.channel]) / st.hSum
	}
	d, _, _ := m.liftDelta(l, st, ad, false)
	return float64(m.yScale * float64(b*d))
}

// liftConfigError is a lift test's data refusal (a window outside the
// fitted rows, a change below zero spend), slot params.lift_tests.
func liftConfigError(i int, reason, msg string, extra map[string]any) error {
	details := map[string]any{"slot": "params.lift_tests", "index": i, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, fmt.Sprintf("params.lift_tests[%d]: %s", i, msg), details)
}
