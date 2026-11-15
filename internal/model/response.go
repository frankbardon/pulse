package model

import (
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// response.go is response(fit, channel, spend) — the expression function
// that reads a media-mix fit's steady-state response to a constant
// spend on one channel: beta · curve(m · spend), beta the channel's
// coefficient, the curve the fit's saturation (Bound.Saturation; Hill
// hill(x, k, s) over its k:<channel> / s:<channel> coefficients unless a
// REG_MMM_BAYES fit's curve record names logistic, over lam:<channel>,
// or michaelis_menten, over k:<channel>) — a REG_MMM_BAYES fit's
// posterior means, as coef() reads them —, m the
// steady-state multiplier of the channel's adstock record (Adstock.
// SteadyState). The record is the fit's own (adstock.go); a channel
// without one is refused, never given a default kernel. Its static rule
// (CheckResponse) mirrors CheckCoef's: the runtime judges every call and
// predict every call whose fit and channel are literals by the same
// names.
//
// response(fit, channel, spend, geo) reads a REG_MMM_BAYES geo panel's
// geo: its own β (channel[geo]), kernel parameters (decay:channel[geo]
// …, on the channel record's kernel and max_lag) and curve (k:channel
// [geo] … with params.per_geo_curves, the shared curve otherwise) —
// every term coef() reads. Without geo a panel's response is the
// population's: the bare terms carry μ. A geo the fit does not name (any
// geo on a fit that is no panel) is PULSE_MODEL_TERM_UNKNOWN reason
// geo_unknown (CheckResponseGeo), statically wherever the fit's geos are
// known (Signature.GeosKnown).

// ResponseFunc is the expression function name, reserved: no embedder
// function may take it.
const ResponseFunc = "response"

// ReasonAdstockUnknown is response()'s PULSE_MODEL_TERM_UNKNOWN reason:
// the fit has no adstock record for the channel (details.channel; the
// channels it has one for under details.channels when known).
const ReasonAdstockUnknown = "adstock_unknown"

// ReasonGeoUnknown is response()'s PULSE_MODEL_TERM_UNKNOWN reason for a
// geo argument the fit does not name (details.geo; the fit's geos under
// details.geos, empty on a fit that is no geo panel).
const ReasonGeoUnknown = "geo_unknown"

// CheckResponse is the static rule over one response(fit, channel, …)
// call. In order: fit resolves in cat (PULSE_MODEL_UNKNOWN), it is a
// regression fit (PULSE_MODEL_KIND_MISMATCH), channel has an adstock
// record (PULSE_MODEL_TERM_UNKNOWN reason adstock_unknown) — judged only
// when the catalog knows the fit's records (Signature.AdstocksKnown; a
// static `fits` entry bound from a sweep does not: its winner's request
// is the run's). The first fault wins.
func CheckResponse(cat Catalog, fit, channel string) *errors.CodedError {
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(fit)
	}
	if !ok {
		return UnknownModel(fit, NamesOfKind(cat, types.FitKindRegression))
	}
	if sig.EffectiveKind() != types.FitKindRegression {
		return KindMismatch(fit, types.FitKindRegression, sig.EffectiveKind())
	}
	if sig.AdstocksKnown && !slices.Contains(sig.Adstocks, channel) {
		return AdstockUnknown(fit, channel, sig.Adstocks)
	}
	return nil
}

// AdstockUnknown is the response() refusal of a channel with no adstock
// record: PULSE_MODEL_TERM_UNKNOWN reason adstock_unknown.
func AdstockUnknown(fit, channel string, channels []string) *errors.CodedError {
	if channels == nil {
		channels = []string{}
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_TERM_UNKNOWN,
		fmt.Sprintf("%s(): fit %q has no adstock record for channel %q — only a media-mix channel adstocked by one WIN_DISTRIBUTED_LAG (REG_MMM_HILL), a REG_MMM_BAYES channel or an inline model's declared adstock has one", ResponseFunc, fit, channel),
		map[string]any{"fit": fit, "channel": channel, "reason": ReasonAdstockUnknown, "channels": slices.Clone(channels)})
}

// CheckResponseGeo is CheckResponse over a response(fit, channel, spend,
// geo) call, then geo: one of the fit's geos when the catalog knows them
// (Signature.GeosKnown), else PULSE_MODEL_TERM_UNKNOWN reason
// geo_unknown (GeoUnknown).
func CheckResponseGeo(cat Catalog, fit, channel, geo string) *errors.CodedError {
	if cerr := CheckResponse(cat, fit, channel); cerr != nil {
		return cerr
	}
	sig, _ := cat.Signature(fit)
	if sig.GeosKnown && !slices.Contains(sig.Geos, geo) {
		return GeoUnknown(fit, channel, geo, sig.Geos)
	}
	return nil
}

// GeoUnknown is the response() refusal of a geo the fit does not name:
// PULSE_MODEL_TERM_UNKNOWN reason geo_unknown.
func GeoUnknown(fit, channel, geo string, geos []string) *errors.CodedError {
	if geos == nil {
		geos = []string{}
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_TERM_UNKNOWN,
		fmt.Sprintf("%s(): fit %q names no geo %q — only a REG_MMM_BAYES geo panel (params.geo) has geos, its own (outputs.hierarchy.geos); drop the geo argument for the population response", ResponseFunc, fit, geo),
		map[string]any{"fit": fit, "channel": channel, "geo": geo, "reason": ReasonGeoUnknown, "geos": slices.Clone(geos)})
}

// Response is response(fit, channel, spend) over the request's bound
// models: CheckResponse, then a failed fit's own refusal
// (FailedFitRefusal), then the fit's saturation curve
// (CurveResponseAt) over the channel's coefficient, its curve
// coefficients (MMMBayesCurvePrefixes of Bound.Saturation: k:<channel>
// and s:<channel> for Hill; coef()'s point reads, refused the way coef()
// refuses them) and its record's steady-state multiplier.
func Response(set *Set, fit, channel string, spend float64) (float64, *errors.CodedError) {
	if cerr := CheckResponse(set, fit, channel); cerr != nil {
		return math.NaN(), cerr
	}
	return response(set, fit, channel, "", false, spend)
}

// ResponseSlope is ∂response(fit, channel, spend)/∂spend over the same
// terms Response reads (and refused as it refuses them):
// CurveSlopeAt — the curve's slope at m · spend times β · m.
func ResponseSlope(set *Set, fit, channel string, spend float64) (float64, *errors.CodedError) {
	if cerr := CheckResponse(set, fit, channel); cerr != nil {
		return math.NaN(), cerr
	}
	t, cerr := responseTerms(set, fit, channel, "", false)
	if cerr != nil {
		return math.NaN(), cerr
	}
	return CurveSlopeAt(t.curve, t.beta, t.params, t.m, spend), nil
}

// ResponseGeoSlope is ResponseSlope over geo's own terms (ResponseGeo's).
func ResponseGeoSlope(set *Set, fit, channel, geo string, spend float64) (float64, *errors.CodedError) {
	if cerr := CheckResponseGeo(set, fit, channel, geo); cerr != nil {
		return math.NaN(), cerr
	}
	t, cerr := responseTerms(set, fit, channel, geo, true)
	if cerr != nil {
		return math.NaN(), cerr
	}
	return CurveSlopeAt(t.curve, t.beta, t.params, t.m, spend), nil
}

// ResponseGeo is response(fit, channel, spend, geo): CheckResponseGeo,
// then Response's figure over geo's own terms — β channel[geo], each
// kernel parameter prefix+channel[geo] on the channel record's kernel
// and max_lag, each curve parameter prefix+channel[geo] where the fit
// names it (params.per_geo_curves), the shared prefix+channel
// otherwise; each read as coef() reads it (and refused as coef()
// refuses it).
func ResponseGeo(set *Set, fit, channel, geo string, spend float64) (float64, *errors.CodedError) {
	if cerr := CheckResponseGeo(set, fit, channel, geo); cerr != nil {
		return math.NaN(), cerr
	}
	return response(set, fit, channel, geo, true, spend)
}

// response is Response's figure once the call is checked: the
// population's terms, or with hasGeo geo's (ResponseGeo).
func response(set *Set, fit, channel, geo string, hasGeo bool, spend float64) (float64, *errors.CodedError) {
	t, cerr := responseTerms(set, fit, channel, geo, hasGeo)
	if cerr != nil {
		return math.NaN(), cerr
	}
	return CurveResponseAt(t.curve, t.beta, t.params, t.m, spend), nil
}

// curveTerms are one response() call's figures short of the spend: the
// saturation curve, β, the curve parameters and the adstock record's
// steady-state multiplier m.
type curveTerms struct {
	curve  string
	beta   float64
	params []float64
	m      float64
}

// responseTerms reads response()'s terms once the call is checked: a
// failed fit's own refusal, the channel's adstock record (geo's kernel
// parameters with hasGeo), its steady-state multiplier, then β and the
// curve parameters as coef() reads them.
func responseTerms(set *Set, fit, channel, geo string, hasGeo bool) (curveTerms, *errors.CodedError) {
	var none curveTerms
	if se, failed := set.Failed(fit); failed {
		return none, FailedFitRefusal(fit, se)
	}
	b := set.byName[fit]
	a, ok := b.Adstock(channel)
	if !ok {
		return none, AdstockUnknown(fit, channel, nil)
	}
	// term is name's coefficient in geo (the population's without one).
	term := func(name string) string {
		if hasGeo {
			return MMMGeoTerm(name, geo)
		}
		return name
	}
	if hasGeo {
		kernel, ok := MMMBayesAdstockPrefixes(a.Kernel)
		if !ok {
			return none, AdstockUnknown(fit, channel, nil)
		}
		for _, p := range kernel {
			v, cerr := Coef(set, fit, term(p+channel), StatPoint)
			if cerr != nil {
				return none, cerr
			}
			a.setParam(p, v)
		}
	}
	m, err := a.SteadyState()
	if err != nil {
		return none, AdstockUnknown(fit, channel, nil)
	}
	curve := b.Saturation
	prefixes, ok := MMMBayesCurvePrefixes(curve)
	if !ok {
		return none, AdstockUnknown(fit, channel, nil)
	}
	terms := []string{term(channel)}
	for _, p := range prefixes {
		t := p + channel
		if hasGeo && b.Result != nil {
			if _, own := b.Result.Coefficients[term(t)]; own {
				// params.per_geo_curves: the geo's own curve.
				t = term(t)
			}
		}
		terms = append(terms, t)
	}
	v := make([]float64, len(terms))
	for i, t := range terms {
		c, cerr := Coef(set, fit, t, StatPoint)
		if cerr != nil {
			return none, cerr
		}
		v[i] = c
	}
	return curveTerms{curve: curve, beta: v[0], params: v[1:], m: m}, nil
}

// SteadyState is the record's steady-state multiplier m: the sum of its
// kernel weights over lags 0..MaxLag (Weights), added in lag order — the
// adstocked value a constant spend x settles at is m · x. Geometric
// unnormalised gives (1 − d^(L+1)) / (1 − d); a normalised kernel 1 (to
// rounding).
func (a Adstock) SteadyState() (float64, error) {
	w, err := a.Weights()
	if err != nil {
		return math.NaN(), err
	}
	m := 0.0
	for _, x := range w {
		m += x
	}
	return m, nil
}

// ResponseAt is beta · hill(m · spend, k, s), the media-mix fits' Hill
// curve 1 / (1 + (k/x)^s) with detmath.Pow (the hill() expression
// function's formula and power, bit for bit), each product barriered so
// no FMA fuses it. Outside the curve's domain — x < 0, k ≤ 0, s ≤ 0, a
// NaN anywhere — it is NaN, an undefined figure (null on the wire).
func ResponseAt(beta, k, s, m, spend float64) float64 {
	x := float64(m * spend)
	if !(x >= 0) || !(k > 0) || !(s > 0) || math.IsNaN(beta) {
		return math.NaN()
	}
	h := 1 / (1 + detmath.Pow(k/x, s))
	return float64(beta * h)
}

// CurveSlopeAt is ∂CurveResponseAt/∂spend: beta · m · curve'(m · spend)
// — Hill s·q / x with q = u·h², u = (k/x)^s, h = 1 / (1 + u) (the
// limit at x = 0: 0 for s > 1, 1/k at s = 1, +Inf below); logistic
// λ · 2e / (1 + e)², e = e^(−λx); michaelis_menten k / (k + x)² —
// on detmath's transcendentals with every product barriered. Wherever
// CurveResponseAt is NaN so is the slope.
func CurveSlopeAt(curve string, beta float64, params []float64, m, spend float64) float64 {
	if math.IsNaN(CurveResponseAt(curve, beta, params, m, spend)) {
		return math.NaN()
	}
	x := float64(m * spend)
	var d float64
	switch {
	case curve == "" || curve == MMMSaturationHill:
		k, s := params[0], params[1]
		switch {
		case x == 0:
			switch {
			case s == 1:
				d = 1 / k
			case s < 1:
				d = math.Inf(1)
			}
		case math.IsInf(x, 1):
		default:
			u := detmath.Pow(k/x, s)
			h := 1 / (1 + u)
			q := float64(float64(u*h) * h)
			d = float64(s*q) / x
		}
	case curve == MMMSaturationLogistic:
		lam := params[0]
		e := detmath.Exp(-float64(lam * x))
		den := float64((1 + e) * (1 + e))
		d = float64(lam*float64(2*e)) / den
	case curve == MMMSaturationMichaelisMenten:
		k := params[0]
		d = k / float64((k+x)*(k+x))
	}
	return float64(float64(beta*m) * d)
}

// CurveResponseAt is beta · curve(m · spend) under saturation curve
// ("" or hill: ResponseAt over params k, s; logistic: params λ,
// (1 − e^(−λx)) / (1 + e^(−λx)); michaelis_menten: params k,
// x / (k + x)), with detmath's transcendentals and every product
// barriered, so it is REG_MMM_BAYES's own curve bit for bit. Outside
// the curve's domain — x < 0, a parameter ≤ 0, a NaN anywhere, an
// unknown curve or a wrong parameter count — it is NaN.
func CurveResponseAt(curve string, beta float64, params []float64, m, spend float64) float64 {
	switch {
	case (curve == "" || curve == MMMSaturationHill) && len(params) == 2:
		return ResponseAt(beta, params[0], params[1], m, spend)
	case curve == MMMSaturationLogistic && len(params) == 1:
		x, lam := float64(m*spend), params[0]
		if !(x >= 0) || !(lam > 0) || math.IsNaN(beta) {
			return math.NaN()
		}
		u := float64(lam * x)
		h := -detmath.Expm1(-u) / (1 + detmath.Exp(-u))
		return float64(beta * h)
	case curve == MMMSaturationMichaelisMenten && len(params) == 1:
		x, k := float64(m*spend), params[0]
		if !(x >= 0) || !(k > 0) || math.IsNaN(beta) {
			return math.NaN()
		}
		return float64(beta * (x / (k + x)))
	}
	return math.NaN()
}
