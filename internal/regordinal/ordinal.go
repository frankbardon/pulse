// Package regordinal holds REG_ORDINAL, the proportional-odds ordinal
// regression (MASS::polr's cumulative link model), authored on the
// extension contract (extend.Regression) and registered by
// internal/regadapt as a model regression (internal/model.ModelRegression):
// its params, factory and model, the Brant test of the proportional-odds
// assumption (brant.go) and the robust covariances. The declaration
// (description, params, outputs) is internal/descriptor's
// model-regression table, which both predict and the engine adapter
// read; purpose and interpretation are the descriptor's built-in
// guidance.
package regordinal

import (
	stderrors "errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/regcov"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// Name is the ordinal regression's type.
const Name = types.REG_ORDINAL

// MaxLevels caps the response's levels: an ordinal response is a short
// ordered scale, and a continuous target would make every distinct
// value a threshold. Above it the slot fails alone.
const MaxLevels = 100

// MinLevels is polr's floor ("response must have 3 or more levels"):
// two levels are a binary logit (REG_GLM binomial).
const MinLevels = 3

// ThresholdSeparator joins two adjacent levels into a threshold's
// coefficient name, polr's zeta names ("low|mid").
const ThresholdSeparator = pmodel.OrdinalThresholdSeparator

// brantParams is params.brant: an opt-in block with no keys today.
type brantParams struct{}

// params is the params object.
type params struct {
	Link   string       `json:"link"`
	Levels []string     `json:"levels"`
	Brant  *brantParams `json:"brant"`
}

// OutputSchema is REG_ORDINAL's declared Outputs.
var OutputSchema = []descriptor.RegressionOutputMeta{
	{Key: "link", JSONType: "string", Description: "The link used."},
	{Key: "levels", JSONType: "array", Description: "The levels in fitted order, lowest first."},
	{Key: "thresholds", JSONType: "array", Description: "The threshold coefficients' names, in order (no p-value)."},
	{Key: "fitted_probabilities", JSONType: "object", Description: "Per level: the weighted mean fitted P(Y = level | x) over the fitted rows."},
	{Key: "brant", JSONType: "object", Description: "With params.brant: {statistic, df, p_value} omnibus ((K − 2)·p df) and terms per predictor (K − 2 df); null when a binary split separates."},
}

// OutputKeys are OutputSchema's keys, in order.
func OutputKeys() []string {
	out := make([]string, len(OutputSchema))
	for i, o := range OutputSchema {
		out[i] = o.Key
	}
	return out
}

// CovarianceTypes are the covariance.type values REG_ORDINAL computes:
// the observed-information inverse (classical), the score sandwich
// hc0 / hc1 and the cluster CR1. hc2 / hc3 need a hat matrix a
// cumulative link has no row-wise form of, and hac a series order; they
// are refused by the shared-slot table.
func CovarianceTypes() []types.CovarianceType {
	return []types.CovarianceType{types.CovarianceClassical, types.CovarianceHC0, types.CovarianceHC1, types.CovarianceCluster}
}

// configError is a PROCESSING_CONFIG refusal naming the regression.
func configError(spec *types.RegressionSpec, msg string, extra map[string]any) error {
	details := map[string]any{"regression": spec.Name, "type": string(Name)}
	for k, v := range extra {
		details[k] = v
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, string(Name)+": "+msg, details)
}

// decodeParams reads spec.Params strictly (a key typo is refused, never
// ignored) and checks every rule that reads the spec alone.
func decodeParams(spec *types.RegressionSpec) (params, link, error) {
	var p params
	if len(spec.Params) > 0 {
		if err := paramread.DecodeStrict(paramread.Regression, string(Name), spec.Params, &p); err != nil {
			return p, link{}, configError(spec, "params: "+err.Error(), map[string]any{"slot": "params"})
		}
	} else {
		paramread.Declare(paramread.Regression, string(Name), &p)
	}
	name := p.Link
	if name == "" {
		name = LinkLogit
	}
	l, ok := links[name]
	if !ok {
		return p, link{}, configError(spec, fmt.Sprintf("params.link %q is not one of %v", p.Link, Links()),
			map[string]any{"slot": "params.link", "link": p.Link, "allowed": Links()})
	}
	if p.Levels != nil {
		seen := map[string]bool{}
		for _, lv := range p.Levels {
			if lv == "" || seen[lv] {
				return p, link{}, configError(spec, fmt.Sprintf("params.levels: %q is empty or listed twice", lv),
					map[string]any{"slot": "params.levels", "level": lv})
			}
			seen[lv] = true
		}
		if len(p.Levels) < MinLevels || len(p.Levels) > MaxLevels {
			return p, link{}, configError(spec, fmt.Sprintf("params.levels lists %d levels; an ordinal response needs %d to %d", len(p.Levels), MinLevels, MaxLevels),
				map[string]any{"slot": "params.levels", "levels": len(p.Levels), "min": MinLevels, "max": MaxLevels})
		}
	}
	if p.Brant != nil && name != LinkLogit {
		return p, link{}, configError(spec, "params.brant tests the logit model's binary splits; it needs link logit, got "+name,
			map[string]any{"slot": "params.brant", "requires": "link logit", "link": name})
	}
	return p, l, nil
}

// Factory builds REG_ORDINAL's model: a buffered proportional-odds fit,
//
//	P(Y ≤ k | x) = F(ζ_k − xᵀβ),  k = 1..K−1,
//
// F the params.link cdf, by Newton–Raphson on the analytic gradient and
// observed information (fit.go) to the MLE, from β = 0 and the
// thresholds-only thresholds. The coefficient table is the predictors
// (spec order), then the K − 1 thresholds "<level>|<next level>" — no
// intercept (the thresholds carry it). Standard errors read the inverse
// observed information (classical) or a score sandwich (covariance hc0,
// hc1 = hc0·N*/(N* − q), cluster = CR1 G/(G − 1)·(N* − 1)/(N* − q), q =
// p + K − 1); each coefficient carries the Wald statistic estimate / SE,
// and each predictor a two-sided normal p-value (a threshold's null
// ζ = 0 is no hypothesis, so it carries none). Deviance is −2ℓ, the null
// deviance the thresholds-only fit's, pseudo-R² McFadden's; two_log_lik
// and aic (q parameters) ride an unweighted or frequency-weighted fit.
//
// Weights are prior weights w* (REG_GLM's rule): a frequency weight w*
// = w, the fit equal to the rep()-expanded one; a probability weight w*
// = w·n_eff/Σw (Kish), the frequency formulas on w*, with no likelihood
// figures.
//
// Data refusals fail the slot alone: fewer than three levels or more
// than MaxLevels on the fitted rows, a params.levels entry no fitted
// row carries, no more rows than parameters
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA), a singular information
// (PROCESSING_REGRESSION_RANK_DEFICIENT), no convergence
// (PROCESSING_REGRESSION_NO_CONVERGE, e.g. a predictor that separates
// the levels), fewer than two clusters (PROCESSING_REGRESSION_VCOV_UNSUPPORTED).
func Factory(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
	p, l, err := decodeParams(spec)
	if err != nil {
		return nil, err
	}
	m := &model{spec: spec, link: l, levels: slices.Clone(p.Levels), brant: p.Brant != nil,
		cov: spec.Covariance.EffectiveType()}
	if spec.Covariance != nil {
		m.cluster = spec.Covariance.Cluster
	}
	if w := spec.Weight.Spec(); w != nil {
		m.probability = w.EffectiveKind() == types.WeightKindProbability
	}
	return m, nil
}

// SpecRefusal is Factory's refusal of spec, or nil when Factory builds:
// every check Factory makes reads the spec alone (the params decode, the
// link, the levels list, brant's link), so predict raises the very error
// the runtime does (internal/descriptor's model-regression declaration
// calls it).
func SpecRefusal(spec *types.RegressionSpec) *perr.CodedError {
	_, err := Factory(spec, nil)
	if err == nil {
		return nil
	}
	var ce *perr.CodedError
	if stderrors.As(err, &ce) {
		return ce
	}
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, err.Error(), map[string]any{"type": string(Name)})
}

// model is one slot's buffered fit.
type model struct {
	spec        *types.RegressionSpec
	link        link
	levels      []string // params.levels; nil = the data's own order
	brant       bool
	cov         types.CovarianceType
	cluster     string
	probability bool
}

// levelOf reads row rec's response: its label and its natural order key
// (a categorical's dictionary index, a number's value). A categorical
// reads its label; any other target formats its value as R's
// as.character does for a plain double ("3", "2.5").
func (m *model) levelOf(rec extend.Record) (label string, order float64, ok bool) {
	v, ok := rec.NumericValue(m.spec.Target)
	if !ok {
		return "", 0, false
	}
	if s, isCat := rec.StringValue(m.spec.Target); isCat {
		return s, v, true
	}
	return strconv.FormatFloat(v, 'g', -1, 64), v, true
}

// clusterOf is row rec's cluster key: a categorical's label, else the
// value; false when null.
func (m *model) clusterOf(rec extend.Record) (string, bool) {
	if s, ok := rec.StringValue(m.cluster); ok {
		return s, true
	}
	v, ok := rec.NumericValue(m.cluster)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return "", false
	}
	return strconv.FormatFloat(v, 'g', -1, 64), true
}

// AdmitRow is the model's own listwise rule beyond the engine's
// (target, predictors, weight): a row whose response params.levels does
// not list is missing — R's factor(y, levels = ...) makes it NA and
// polr drops it — and so is a row whose covariance cluster is null, as
// on the built-ins. The adapter counts only the rows it admits (n_obs).
func (m *model) AdmitRow(rec extend.Record, _ pmodel.AdmitToken) bool {
	if m.levels != nil {
		label, _, ok := m.levelOf(rec)
		if !ok || !slices.Contains(m.levels, label) {
			return false
		}
	}
	if m.cov == types.CovarianceCluster {
		if _, ok := m.clusterOf(rec); !ok {
			return false
		}
	}
	return true
}

// insufficient is a PROCESSING_REGRESSION_INSUFFICIENT_DATA failure.
func insufficient(msg string, details map[string]any) error {
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, string(Name)+": "+msg, details)
}

// Fit reads the admitted rows, orders the levels, fits and reports.
func (m *model) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	n, p := rows.Len(), len(m.spec.Predictors)
	d := &design{x: make([][]float64, n), y: make([]int, n), w: make([]float64, n), p: p}
	labels := make([]string, n)
	order := map[string]float64{}
	clusters := make([]string, n)
	sumW, sumW2 := 0.0, 0.0
	for i := range n {
		rec := rows.At(i)
		label, ord, ok := m.levelOf(rec)
		if !ok {
			return extend.RegressionOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
				string(Name)+": an admitted row has no response", nil)
		}
		labels[i] = label
		order[label] = ord
		d.x[i] = make([]float64, p)
		for j, name := range m.spec.Predictors {
			d.x[i][j], _ = rec.NumericValue(name)
		}
		w, weighted := rec.Weight()
		if !weighted {
			w = 1
		}
		d.w[i] = w
		sumW += w
		sumW2 += float64(w * w)
		if m.cov == types.CovarianceCluster {
			clusters[i], _ = m.clusterOf(rec)
		}
	}
	levels, err := m.resolveLevels(order)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	d.k = len(levels)
	index := make(map[string]int, len(levels))
	for k, lv := range levels {
		index[lv] = k
	}
	for i, lb := range labels {
		d.y[i] = index[lb]
	}
	// N* and the prior weights w*: Σw (frequency; n unweighted), or Kish
	// n_eff with w* = w·n_eff/Σw (probability).
	nStar := sumW
	if m.probability {
		nStar = float64(sumW*sumW) / sumW2
		c := nStar / sumW
		for i := range d.w {
			d.w[i] = float64(d.w[i] * c)
		}
	}
	q := d.q()
	if !(nStar > float64(q)) {
		return extend.RegressionOutput{}, insufficient(fmt.Sprintf("needs more rows than its %d parameters (predictors + levels − 1), got %v", q, nStar),
			map[string]any{"reason": "too_few_rows", "n": nStar, "required": q + 1})
	}
	thresholds := make([]string, d.k-1)
	for k := range thresholds {
		thresholds[k] = levels[k] + ThresholdSeparator + levels[k+1]
		if slices.Contains(m.spec.Predictors, thresholds[k]) {
			return extend.RegressionOutput{}, configError(m.spec, fmt.Sprintf("predictor %q collides with a threshold coefficient name", thresholds[k]),
				map[string]any{"slot": "predictors", "predictor": thresholds[k]})
		}
	}
	sol, fail := d.newton(m.link)
	if fail != nil {
		code := perr.PROCESSING_REGRESSION_NO_CONVERGE
		msg := fmt.Sprintf("the maximum-likelihood fit did not converge (%s after %d iterations); a predictor may separate the levels", fail.reason, fail.iters)
		if fail.reason == "singular_information" {
			code = perr.PROCESSING_REGRESSION_RANK_DEFICIENT
			msg = "the information matrix is singular: a predictor is constant or a linear combination of the others"
		}
		return extend.RegressionOutput{}, perr.NewCodedErrorWithDetails(code, string(Name)+": "+msg,
			map[string]any{"reason": fail.reason, "iterations": fail.iters})
	}
	inv, err := linalg.InverseSPD(sol.info)
	if err != nil {
		return extend.RegressionOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
			string(Name)+": the information matrix at the fit is singular", map[string]any{"reason": "singular_information"})
	}
	vcov := inv.ToRows()
	if m.cov != types.CovarianceClassical {
		if vcov, err = m.robust(d, sol.theta, vcov, clusters, nStar); err != nil {
			return extend.RegressionOutput{}, err
		}
	}
	return m.output(d, sol, vcov, levels, thresholds), nil
}

// resolveLevels orders the response's levels: params.levels as given
// (each must be carried by a fitted row), else the observed labels by
// their natural order (a categorical's dictionary index, a number's
// value).
func (m *model) resolveLevels(order map[string]float64) ([]string, error) {
	var levels []string
	if m.levels != nil {
		for _, lv := range m.levels {
			if _, ok := order[lv]; !ok {
				return nil, insufficient(fmt.Sprintf("params.levels lists %q, which no fitted row carries", lv),
					map[string]any{"reason": "empty_level", "level": lv})
			}
		}
		levels = slices.Clone(m.levels)
	} else {
		for lv := range order {
			levels = append(levels, lv)
		}
		slices.SortFunc(levels, func(a, b string) int {
			switch oa, ob := order[a], order[b]; {
			case oa < ob:
				return -1
			case oa > ob:
				return 1
			}
			return 0
		})
	}
	if len(levels) < MinLevels {
		return nil, insufficient(fmt.Sprintf("the fitted rows carry %d response levels; an ordinal response needs at least %d (two are a binary logit: REG_GLM binomial)", len(levels), MinLevels),
			map[string]any{"reason": "fewer_than_three_levels", "levels": len(levels), "min": MinLevels})
	}
	if len(levels) > MaxLevels {
		return nil, insufficient(fmt.Sprintf("the fitted rows carry %d response levels, above the %d an ordinal response takes (a continuous target is no ordinal scale)", len(levels), MaxLevels),
			map[string]any{"reason": "too_many_levels", "levels": len(levels), "max": MaxLevels})
	}
	return levels, nil
}

// robust is the score sandwich V = B·M·B, B the classical vcov: M = Σ
// w*·s sᵀ (hc0; hc1 scaled N*/(N* − q)) or Σ_g S_g S_gᵀ, S_g = Σ_{i∈g}
// w*·s_i (cluster, scaled G/(G − 1)·(N* − 1)/(N* − q)).
func (m *model) robust(d *design, theta []float64, bread [][]float64, clusters []string, nStar float64) ([][]float64, error) {
	q := d.q()
	meat := make([][]float64, q)
	for j := range meat {
		meat[j] = make([]float64, q)
	}
	s := make([]float64, q)
	scale := 1.0
	switch m.cov {
	case types.CovarianceCluster:
		var keys []string
		sums := map[string][]float64{}
		for i := range d.y {
			d.score(d.eval(m.link, theta, i, d.linear(theta, i)), i, s)
			g, ok := sums[clusters[i]]
			if !ok {
				g = make([]float64, q)
				sums[clusters[i]] = g
				keys = append(keys, clusters[i])
			}
			for j, v := range s {
				g[j] += float64(d.w[i] * v)
			}
		}
		if len(keys) < 2 {
			return nil, regcov.TooFewClusters(m.spec, len(keys))
		}
		for _, key := range keys {
			g := sums[key]
			for j := range g {
				for k := 0; k <= j; k++ {
					meat[j][k] += float64(g[j] * g[k])
				}
			}
		}
		G := float64(len(keys))
		scale = float64(G/(G-1)) * ((nStar - 1) / (nStar - float64(q)))
	default:
		for i := range d.y {
			d.score(d.eval(m.link, theta, i, d.linear(theta, i)), i, s)
			for j := range s {
				ws := float64(d.w[i] * s[j])
				for k := 0; k <= j; k++ {
					meat[j][k] += float64(ws * s[k])
				}
			}
		}
		if m.cov == types.CovarianceHC1 {
			scale = nStar / (nStar - float64(q))
		}
	}
	for j := range meat {
		for k := j + 1; k < q; k++ {
			meat[j][k] = meat[k][j]
		}
	}
	v := matMul(matMul(bread, meat), bread)
	for j := range v {
		for k := range v[j] {
			v[j][k] = float64(v[j][k] * scale)
		}
	}
	// Symmetrize: the two triangles of B·M·B differ in rounding only.
	for j := range v {
		for k := 0; k < j; k++ {
			v[k][j] = v[j][k]
		}
	}
	return v, nil
}

// output maps the solution onto the extension output.
func (m *model) output(d *design, sol *solution, vcov [][]float64, levels, thresholds []string) extend.RegressionOutput {
	q := d.q()
	names := append(slices.Clone(m.spec.Predictors), thresholds...)
	out := extend.RegressionOutput{Coefficients: make([]extend.RegressionCoefficient, q)}
	for j, name := range names {
		est := sol.theta[j]
		se := math.Sqrt(vcov[j][j])
		z := est / se
		c := extend.RegressionCoefficient{Name: name, Estimate: est, StdErr: extend.Figure(se), Statistic: extend.Figure(z)}
		if j < d.p {
			c.PValue = extend.Figure(twoSidedNormalP(z))
		}
		out.Coefficients[j] = c
	}
	if m.spec.Vcov {
		out.Vcov = vcov
	}
	dev := -float64(2 * sol.ll)
	null := -float64(2 * d.nullLoglik())
	out.Fit = extend.RegressionFit{
		Deviance:     extend.Figure(dev),
		NullDeviance: extend.Figure(null),
		PseudoR2:     extend.Figure(1 - dev/null),
		Iterations:   sol.iters,
	}
	if !m.probability {
		out.Fit.TwoLogLik = extend.Figure(float64(2 * sol.ll))
		out.Fit.AIC = extend.Figure(dev + float64(2*q))
	}
	probs := d.fittedProbabilities(m.link, sol.theta)
	fp := make(map[string]any, d.k)
	for k, lv := range levels {
		fp[lv] = probs[k]
	}
	out.Outputs = map[string]any{
		"link":                 m.link.name,
		"levels":               slices.Clone(levels),
		"thresholds":           slices.Clone(thresholds),
		"fitted_probabilities": fp,
	}
	if m.brant {
		b := d.brant()
		terms := make(map[string]any, d.p)
		for a, name := range m.spec.Predictors {
			t := b.terms[a]
			terms[name] = map[string]any{"statistic": t.stat, "df": t.df, "p_value": t.p}
		}
		out.Outputs["brant"] = map[string]any{"statistic": b.stat, "df": b.df, "p_value": b.p, "terms": terms}
	}
	return out
}
