package regmmm

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
)

// bayes_score.go is REG_MMM_BAYES's row scorer: what ATTR_REG_PREDICT,
// the engine holdout and a saved model file score through. It reads
// PRE-ADSTOCKED channel columns — each channel's spend already run
// through the fit's adstock record (adstock.<channel>, the kernel at its
// posterior-mean params, fixed_at posterior_mean; WIN_DISTRIBUTED_LAG
// with those params builds the column) — so a row scores alone, without
// the spend history. Per kept draw d of the fit's outputs.draws (chains
// in order, then draws),
//
//	ŷ_d = α_d + Σ_j γ_jd·z_j + Σ_c β_cd·curve(x_c; q_cd)
//
// on the original scale (the coefficient table's units: k in raw adstock
// units, λ per raw adstock unit), and the score is the mean over d — the
// posterior mean of the mean prediction at that adstock. A whole-model
// params.bound lower / upper scores the type-7 quantile of the ŷ_d at
// (1 ∓ credible_level) / 2 instead: the credible bound of the MEAN
// prediction (μ), not a predictive interval (no σ). The decay's own
// uncertainty is not in either: the inputs fix it at the posterior mean.
//
// The curve is read off the coefficient names: lam:<ch> is logistic,
// s:<ch> beside k:<ch> Hill, k:<ch> alone Michaelis–Menten. On a geo
// panel (outputs.hierarchy) the row's geo — the field's categorical
// label, or an unsigned integer's decimal code — picks its own
// intercept, β and (per_geo_curves) curve; a geo the fit does not hold
// scores from the population — each channel's population β and curve
// (the term's own name) and, per draw, the mean of the fit's geo
// intercepts (there is no pooled intercept) — and is counted in the
// scorer's GeoTally (PULSE_MMM_GEO_UNSEEN). A null channel, control or
// geo, a negative channel value, or a non-finite ŷ_d scores null.

// bayesTerms is one scoring context's per-draw parameters: the
// intercept, each control's γ, each channel's β and curve params.
type bayesTerms struct {
	alpha []float64
	gamma [][]float64
	beta  [][]float64
	q     [][][]float64 // [channel][curve param][draw]
}

// bayesScorer scores REG_MMM_BAYES rows over the fit's draws. It is
// immutable once built (its GeoTally aside, which is concurrency-safe).
type bayesScorer struct {
	channels, controls []string
	curve              saturationCurve
	nd                 int
	// level is the draws' credible_level; bound "", lower or upper.
	level float64
	bound string
	// pooled is the one-market fit's terms, or a panel's population.
	pooled *bayesTerms
	// geoField / geos are a panel's (nil on one market).
	geoField string
	geos     map[string]*bayesTerms
	tally    *pmodel.GeoTally
}

var (
	_ extend.RowScorer  = (*bayesScorer)(nil)
	_ pmodel.GeoTallier = (*bayesScorer)(nil)
)

// BayesScorer is REG_MMM_BAYES's registration Scorer: the posterior
// mean score (BayesBoundScorer with no bound).
func BayesScorer(fit extend.FittedRegression) (extend.RowScorer, error) {
	return BayesBoundScorer(fit, "")
}

// BayesBoundScorer builds the scorer of a fit for an ATTR_REG_PREDICT
// params.bound: "" or point scores the posterior mean, lower / upper the
// credible bound of the mean prediction. A fit without outputs.draws is
// PULSE_MODEL_SCORE_INVALID reason draws_required; draws that do not
// read as this model's are PROCESSING_INTERNAL.
func BayesBoundScorer(fit extend.FittedRegression, bound string) (extend.RowScorer, error) {
	raw, ok := fit.Outputs[pmodel.MMMDrawsKey].(map[string]any)
	if !ok {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_MODEL_SCORE_INVALID,
			fmt.Sprintf("%s scorer: the fit carries no posterior draws; fit it with params.return_draws true", BayesName),
			map[string]any{"reason": pmodel.ReasonDrawsRequired, "type": string(BayesName)})
	}
	idx, cols, level, err := readScoreDraws(raw)
	if err != nil {
		return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
			fmt.Sprintf("%s scorer: outputs.draws: %v", BayesName, err), map[string]any{"type": string(BayesName)})
	}
	sc := &bayesScorer{level: level, nd: len(cols[0])}
	if bound == pmodel.BoundLower || bound == pmodel.BoundUpper {
		sc.bound = bound
	}
	has := func(name string) bool { _, ok := idx[name]; return ok }
	for _, x := range fit.Predictors {
		switch {
		case has(pmodel.MMMLogisticLambdaPrefix + x):
			sc.curve = logisticCurve{}
		case has(pmodel.MMMShapePrefix+x) && has(pmodel.MMMHalfSaturationPrefix+x):
			sc.curve = hillCurveModel{}
		case has(pmodel.MMMHalfSaturationPrefix + x):
			sc.curve = mmCurve{}
		default:
			sc.controls = append(sc.controls, x)
			continue
		}
		sc.channels = append(sc.channels, x)
	}
	if len(sc.channels) == 0 {
		return nil, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
			fmt.Sprintf("%s scorer: no predictor carries a curve coefficient", BayesName), map[string]any{"type": string(BayesName)})
	}
	col := func(name string) ([]float64, error) {
		j, ok := idx[name]
		if !ok {
			return nil, fmt.Errorf("%s scorer: outputs.draws has no %q", BayesName, name)
		}
		return cols[j], nil
	}
	// terms reads one context: intercept and per-channel names given by
	// the naming functions, controls shared.
	terms := func(alpha []float64, beta, curve func(string) string) (*bayesTerms, error) {
		t := &bayesTerms{alpha: alpha}
		for _, z := range sc.controls {
			g, err := col(z)
			if err != nil {
				return nil, err
			}
			t.gamma = append(t.gamma, g)
		}
		for _, ch := range sc.channels {
			b, err := col(beta(ch))
			if err != nil {
				return nil, err
			}
			t.beta = append(t.beta, b)
			var qs [][]float64
			for _, cq := range sc.curve.Params() {
				q, err := col(curve(cq.prefix + ch))
				if err != nil {
					return nil, err
				}
				qs = append(qs, q)
			}
			t.q = append(t.q, qs)
		}
		return t, nil
	}
	self := func(s string) string { return s }
	geoField, geos := scoreHierarchy(fit.Outputs)
	if geoField == "" {
		alpha, err := col(interceptName)
		if err == nil {
			sc.pooled, err = terms(alpha, self, self)
		}
		if err != nil {
			return nil, perr.WrapCodedError(err, perr.PROCESSING_INTERNAL, "reading the draws")
		}
		return sc, nil
	}
	sc.geoField, sc.geos = geoField, make(map[string]*bayesTerms, len(geos))
	mean := make([]float64, sc.nd)
	for _, g := range geos {
		alpha, err := col(pmodel.MMMGeoTerm(interceptName, g))
		if err != nil {
			return nil, perr.WrapCodedError(err, perr.PROCESSING_INTERNAL, "reading the draws")
		}
		for d, v := range alpha {
			mean[d] += v
		}
		geoName := func(term string) string { return pmodel.MMMGeoTerm(term, g) }
		curveName := func(term string) string {
			if has(geoName(term)) {
				return geoName(term)
			}
			return term
		}
		t, err := terms(alpha, geoName, curveName)
		if err != nil {
			return nil, perr.WrapCodedError(err, perr.PROCESSING_INTERNAL, "reading the draws")
		}
		sc.geos[g] = t
	}
	for d := range mean {
		mean[d] /= float64(len(geos))
	}
	pooled, err := terms(mean, self, self)
	if err != nil {
		return nil, perr.WrapCodedError(err, perr.PROCESSING_INTERNAL, "reading the draws")
	}
	sc.pooled = pooled
	sc.tally = pmodel.NewGeoTally(geoField, geos)
	return sc, nil
}

// scoreHierarchy is a geo panel's field and geos (outputs.hierarchy),
// "" on one market.
func scoreHierarchy(outs map[string]any) (string, []string) {
	h, ok := outs["hierarchy"].(map[string]any)
	if !ok {
		return "", nil
	}
	field, _ := h["geo"].(string)
	var geos []string
	if list, ok := h["geos"].([]any); ok {
		for _, g := range list {
			if s, ok := g.(string); ok {
				geos = append(geos, s)
			}
		}
	}
	if field == "" || len(geos) == 0 {
		return "", nil
	}
	return field, geos
}

// readScoreDraws reads outputs.draws in its JSON form
// (pmodel.ReadMMMDraws — the reader a posterior optimize block shares):
// name → index, each coefficient's pooled draws (chains in order, then
// draws; a null draw is NaN) and the credible level.
func readScoreDraws(raw map[string]any) (map[string]int, [][]float64, float64, error) {
	d, err := pmodel.ReadMMMDraws(raw)
	if err != nil {
		return nil, nil, 0, err
	}
	return d.Index, d.Cols, d.Level, nil
}

// Columns satisfies extend.RowScorer: the channels then the controls
// (each a predictor); a panel's geo field is read beside them
// (Signature.ScoreFields).
func (s *bayesScorer) Columns() []string {
	return append(slices.Clone(s.channels), s.controls...)
}

// GeoTally satisfies model.GeoTallier: a panel's unseen-geo count, nil
// on one market.
func (s *bayesScorer) GeoTally() *pmodel.GeoTally { return s.tally }

// geoRow is the engine row's categorical and code readers (an
// extend.ScoreRow carries numbers only; the engine's record has both).
type geoRow interface {
	StringValue(name string) (string, bool)
	Uint64Value(name string) (uint64, bool)
}

// rowGeo is the row's geo: the field's label, else its unsigned integer
// code in decimal; ok false for a null, a row without the readers, or a
// value no code reads.
func (s *bayesScorer) rowGeo(row extend.ScoreRow) (string, bool) {
	gr, ok := row.(geoRow)
	if !ok {
		return "", false
	}
	if v, ok := gr.StringValue(s.geoField); ok {
		return v, true
	}
	v, ok := row.NumericValue(s.geoField)
	if !ok || !(v >= 0) || v != math.Trunc(v) {
		return "", false
	}
	u, ok := gr.Uint64Value(s.geoField)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(u, 10), true
}

// Score satisfies extend.RowScorer: a row from an unseen geo is counted
// only when it scores.
func (s *bayesScorer) Score(row extend.ScoreRow) (float64, bool) {
	t, unseen := s.pooled, ""
	if s.geos != nil {
		g, ok := s.rowGeo(row)
		if !ok {
			return 0, false
		}
		if gt, ok := s.geos[g]; ok {
			t = gt
		} else {
			unseen = g
		}
	}
	v, ok := s.score(t, row)
	if ok && unseen != "" {
		s.tally.Add(unseen)
	}
	return v, ok
}

// score is the row's score under the context t.
func (s *bayesScorer) score(t *bayesTerms, row extend.ScoreRow) (float64, bool) {
	z := make([]float64, len(s.controls))
	for j, name := range s.controls {
		v, ok := row.NumericValue(name)
		if !ok {
			return 0, false
		}
		z[j] = v
	}
	x := make([]float64, len(s.channels))
	for c, name := range s.channels {
		v, ok := row.NumericValue(name)
		if !ok || v < 0 {
			return 0, false
		}
		x[c] = v
	}
	preds := make([]float64, s.nd)
	q := make([]float64, len(s.curve.Params()))
	for d := range preds {
		y := t.alpha[d]
		for j := range z {
			y += float64(t.gamma[j][d] * z[j])
		}
		for c := range x {
			for i := range q {
				q[i] = t.q[c][i][d]
			}
			h, _ := s.curve.Eval(x[c], q, nil)
			y += float64(t.beta[c][d] * h)
		}
		if !finite(y) {
			return 0, false
		}
		preds[d] = y
	}
	switch s.bound {
	case pmodel.BoundLower, pmodel.BoundUpper:
		slices.Sort(preds)
		p := (1 - s.level) / 2
		if s.bound == pmodel.BoundUpper {
			p = (1 + s.level) / 2
		}
		return quantile7(preds, p), true
	}
	sum := 0.0
	for _, y := range preds {
		sum += y
	}
	return sum / float64(s.nd), true
}
