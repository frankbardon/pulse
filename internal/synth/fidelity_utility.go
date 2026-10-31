package synth

import (
	"bytes"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// This file is the UTILITY half of the fidelity report (FR-16): does the
// twin answer an analyst's question the way the SOURCE does?
//
// # Why the models section cannot answer it
//
// `models` (fidelity_models.go) refits each applied model on the
// synthetic partition and compares the result with the CAPTURED
// coefficients. That is the right instrument for "did generation apply
// what capture measured", and it is blind to the gap between what
// capture measured and what the source says. A coefficient is a PARTIAL
// effect; an analyst's churn-by-contract table is a MARGINAL contrast,
// which in the source also carries every confounder contract travels
// with (tenure, autopay). A twin can reproduce the captured partial
// effect exactly — `models` reports it recovered — while its marginal
// contrast is compressed against the source, because the confounding
// was never captured. Only a comparison against the source rows can see
// that, and the merged output cohort carries them (`_synthetic` false).
//
// Two comparisons, both source against synthetic:
//
//   - target_rates (automatic): for every applied model and every
//     categorical field among its predictors, the target's mean per
//     level — a rate for a packed_bool target — on each partition, the
//     spread across levels, its ratio and a verdict.
//   - model_fits (opt-in, Options.FidelityModel): a supplied request's
//     regressions refit on each partition, fit statistic and
//     coefficients side by side. The refit runs through the engine at
//     the facade (synth cannot import processing); this file only folds
//     the two results (CompareModelFits).

// UtilityContrastTolerance is the relative change in a target's spread
// across levels at which the twin's contrast is judged compressed or
// inflated: a synthetic spread below (1 − tolerance) × the source's is
// compressed, above (1 + tolerance) × inflated — and only when the gap
// also exceeds utilitySEMultiple standard errors of the two spreads, so
// a thin level's noise is never read as a lost contrast.
const UtilityContrastTolerance = 0.20

// utilitySEMultiple scales the spreads' combined standard error into the
// second half of the verdict band.
const utilitySEMultiple = 2.0

// utilityMinLevelN is the support a level needs on BOTH partitions to
// enter a spread. A level a few rows carry has a mean that is mostly
// noise, and max − min over noisy means is biased upward on each side.
const utilityMinLevelN = 30

// Verdicts TargetRateFidelity.Verdict carries.
const (
	UtilityVerdictPreserved  = "preserved"
	UtilityVerdictCompressed = "compressed"
	UtilityVerdictInflated   = "inflated"
)

// Fit statistics UtilityModelFit.Statistic names.
const (
	UtilityFitPseudoR2 = "pseudo_r2"
	UtilityFitR2       = "r2"
)

// UtilityFidelity is FidelityReport.Utility.
type UtilityFidelity struct {
	// TargetRates is one entry per (applied model target, categorical
	// predictor field). Absent when no applied model has a categorical
	// predictor.
	TargetRates []*TargetRateFidelity `json:"target_rates,omitempty"`
	// ModelFits is one entry per regression of the supplied
	// Options.FidelityModel request. Absent without one.
	ModelFits []*UtilityModelFit `json:"model_fits,omitempty"`
}

// TargetRateFidelity is one target's mean by the levels of one
// categorical predictor, source beside synthetic.
type TargetRateFidelity struct {
	Target string `json:"target"`
	By     string `json:"by"`
	// Levels is every level either partition carries, in dictionary
	// order.
	Levels []*TargetRateLevel `json:"levels,omitempty"`
	// SourceSpread / SyntheticSpread are max − min of the level means
	// over the levels both partitions carry at least utilityMinLevelN
	// rows of; SpreadRatio is synthetic ÷ source.
	SourceSpread    float64 `json:"source_spread,omitempty"`
	SyntheticSpread float64 `json:"synthetic_spread,omitempty"`
	SpreadRatio     float64 `json:"spread_ratio,omitempty"`
	// SourceEpsilonSquared / SyntheticEpsilonSquared are the share of
	// the target's variance the levels explain on each partition
	// (adjusted η², ε², over the same levels): the effect size of the
	// grouping, which does not grow with n.
	SourceEpsilonSquared    float64 `json:"source_epsilon_squared,omitempty"`
	SyntheticEpsilonSquared float64 `json:"synthetic_epsilon_squared,omitempty"`
	// Verdict is UtilityVerdictPreserved, UtilityVerdictCompressed or
	// UtilityVerdictInflated — see UtilityContrastTolerance.
	Verdict string `json:"verdict,omitempty"`
	// Error is set instead of the spreads when fewer than two levels
	// carry utilityMinLevelN rows on both partitions.
	Error string `json:"error,omitempty"`
}

// TargetRateLevel is one level's target mean on each partition — the
// rate of a packed_bool target.
type TargetRateLevel struct {
	Level         string  `json:"level"`
	SourceN       int     `json:"source_n"`
	SourceMean    float64 `json:"source_mean,omitempty"`
	SyntheticN    int     `json:"synthetic_n"`
	SyntheticMean float64 `json:"synthetic_mean,omitempty"`
}

// UtilityModelFit is one supplied regression refit on the source and
// on the synthetic partition.
type UtilityModelFit struct {
	Name   string               `json:"name,omitempty"`
	Type   types.RegressionType `json:"type"`
	Target string               `json:"target,omitempty"`
	// Statistic names the fit figure compared: UtilityFitPseudoR2 for a
	// REG_GLM (McFadden), UtilityFitR2 otherwise.
	Statistic string `json:"statistic,omitempty"`
	// SourceFit / SyntheticFit are that statistic on each partition and
	// FitRatio is synthetic ÷ source — the twin's share of the source's
	// explanatory power.
	SourceFit     float64 `json:"source_fit,omitempty"`
	SyntheticFit  float64 `json:"synthetic_fit,omitempty"`
	FitRatio      float64 `json:"fit_ratio,omitempty"`
	SourceNObs    int     `json:"source_n_obs,omitempty"`
	SyntheticNObs int     `json:"synthetic_n_obs,omitempty"`
	// Coefficients is every term either fit estimated, sorted by name.
	Coefficients []*UtilityCoefficient `json:"coefficients,omitempty"`
	// SourceError / SyntheticError carry a side's refusal; the other
	// side still reports.
	SourceError    string `json:"source_error,omitempty"`
	SyntheticError string `json:"synthetic_error,omitempty"`
}

// UtilityCoefficient is one term's estimate on each partition.
type UtilityCoefficient struct {
	Term              string  `json:"term"`
	Source            float64 `json:"source"`
	Synthetic         float64 `json:"synthetic"`
	SourceStdError    float64 `json:"source_std_error,omitempty"`
	SyntheticStdError float64 `json:"synthetic_std_error,omitempty"`
	// Ratio is synthetic ÷ source: the share of the source's effect the
	// twin keeps. Absent when the source coefficient is zero.
	Ratio float64 `json:"ratio,omitempty"`
	// Missing names the partition ("source" / "synthetic") whose fit did
	// not estimate this term; its figures are then zero.
	Missing string `json:"missing,omitempty"`
}

// utility returns report.Utility, creating it.
func (r *FidelityReport) utility() *UtilityFidelity {
	if r.Utility == nil {
		r.Utility = &UtilityFidelity{}
	}
	return r.Utility
}

// targetRatePair is one (target, by) accumulation.
type targetRatePair struct {
	target, by string
	byField    *encoding.Field
	// levels[part][levelID] accumulate the target per partition.
	levels [2]map[uint32]*marginalMoments
}

// BuildTargetRates appends one TargetRateFidelity per (applied model
// target, categorical predictor field) onto report.Utility.TargetRates.
// Which models were applied is asked of generation's own compiler, as
// BuildModelFidelity does; a target or predictor that is a helper
// (`"output": false`) field has no column to read and no entry.
func BuildTargetRates(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, spec *Spec) {
	if report == nil || spec == nil || len(spec.Models) == 0 {
		return
	}
	_, wfs, err := buildSchema(spec)
	if err != nil {
		return
	}
	drawers, _, err := buildModelDrawers(resolveConflicts(spec).models, wfs)
	if err != nil || len(drawers) == 0 {
		return
	}
	var pairs []*targetRatePair
	for _, d := range drawers {
		if mergedSchema.Field(d.field) == nil {
			continue
		}
		seen := map[string]bool{}
		for i := range d.predictors {
			p := &d.predictors[i]
			if p.numeric || p.isSet || seen[p.field] {
				continue
			}
			seen[p.field] = true
			f := mergedSchema.Field(p.field)
			if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
				continue
			}
			pairs = append(pairs, &targetRatePair{
				target: d.field, by: p.field, byField: f,
				levels: [2]map[uint32]*marginalMoments{{}, {}},
			})
		}
	}
	if len(pairs) == 0 {
		return
	}

	rr := encx.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	wide := make(map[string]any, len(mergedSchema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		part := 0
		if values[SyntheticFieldName] != 0 {
			part = 1
		}
		for _, p := range pairs {
			if nulls[p.target] || nulls[p.by] {
				continue
			}
			y := values[p.target]
			if math.IsNaN(y) {
				continue
			}
			id := uint32(values[p.by])
			m := p.levels[part][id]
			if m == nil {
				m = &marginalMoments{}
				p.levels[part][id] = m
			}
			m.add(y)
		}
	}

	u := report.utility()
	for _, p := range pairs {
		u.TargetRates = append(u.TargetRates, p.render())
	}
}

// levelStat is one level's figures on one partition.
type levelStat struct {
	n              int
	mean, variance float64
}

func statOf(m *marginalMoments) levelStat {
	if m == nil || m.n == 0 {
		return levelStat{}
	}
	if m.n == 1 {
		return levelStat{n: 1, mean: m.shift + m.sum}
	}
	mean, v, _ := m.meanVar()
	return levelStat{n: m.n, mean: mean, variance: v}
}

// render folds one pair's accumulations into its report entry.
func (p *targetRatePair) render() *TargetRateFidelity {
	entry := &TargetRateFidelity{Target: p.target, By: p.by}
	ids := make([]uint32, 0, len(p.levels[0])+len(p.levels[1]))
	for _, part := range p.levels {
		for id := range part {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var supported [2][]levelStat
	var prev uint32
	for i, id := range ids {
		if i > 0 && id == prev {
			continue
		}
		prev = id
		src, syn := statOf(p.levels[0][id]), statOf(p.levels[1][id])
		entry.Levels = append(entry.Levels, &TargetRateLevel{
			Level:   p.byField.Dictionary.Resolve(id),
			SourceN: src.n, SourceMean: finiteOrZero(src.mean),
			SyntheticN: syn.n, SyntheticMean: finiteOrZero(syn.mean),
		})
		if src.n >= utilityMinLevelN && syn.n >= utilityMinLevelN {
			supported[0] = append(supported[0], src)
			supported[1] = append(supported[1], syn)
		}
	}
	if len(supported[0]) < 2 {
		entry.Error = "fewer than two levels carry enough rows on both partitions to compare a contrast"
		return entry
	}

	srcSpread, srcSE := levelSpread(supported[0])
	synSpread, synSE := levelSpread(supported[1])
	entry.SourceSpread = srcSpread
	entry.SyntheticSpread = synSpread
	if srcSpread > 0 {
		entry.SpreadRatio = synSpread / srcSpread
	}
	entry.SourceEpsilonSquared = levelEpsilonSquared(supported[0])
	entry.SyntheticEpsilonSquared = levelEpsilonSquared(supported[1])

	gap := srcSpread - synSpread
	band := float64(utilitySEMultiple * math.Sqrt(float64(srcSE*srcSE)+float64(synSE*synSE)))
	switch {
	case synSpread < float64((1-UtilityContrastTolerance)*srcSpread) && gap > band:
		entry.Verdict = UtilityVerdictCompressed
	case synSpread > float64((1+UtilityContrastTolerance)*srcSpread) && -gap > band:
		entry.Verdict = UtilityVerdictInflated
	default:
		entry.Verdict = UtilityVerdictPreserved
	}
	return entry
}

// levelSpread returns max − min of the level means and the standard
// error of that difference (the two extreme levels' own sampling
// errors, combined).
func levelSpread(levels []levelStat) (spread, se float64) {
	hi, lo := 0, 0
	for i, l := range levels {
		if l.mean > levels[hi].mean {
			hi = i
		}
		if l.mean < levels[lo].mean {
			lo = i
		}
	}
	h, l := levels[hi], levels[lo]
	se = math.Sqrt(h.variance/float64(h.n) + l.variance/float64(l.n))
	return h.mean - l.mean, se
}

// levelEpsilonSquared is ε² of the target grouped by the levels, through
// the same groupedStrength pair arbitration uses.
func levelEpsilonSquared(levels []levelStat) float64 {
	cats := make([]CategoricalNumericCategorySpec, len(levels))
	for i, l := range levels {
		cats[i] = CategoricalNumericCategorySpec{N: l.n, Mean: l.mean, Std: math.Sqrt(l.variance)}
	}
	eps, ok := groupedStrength(cats)
	if !ok {
		return 0
	}
	return finiteOrZero(eps)
}

// finiteOrZero maps a non-finite figure to 0, which every consuming
// field omits: the report is written with encoding/json, which refuses
// NaN and ±Inf outright.
func finiteOrZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// CompareModelFits folds one supplied request's regressions, fitted on
// the source (src) and on the synthetic partition (syn), into
// UtilityModelFit entries appended onto report.Utility.ModelFits.
// srcErr / synErr are a whole side's refusal (the run did not happen);
// a single regression's own failure rides its RegressionResult.Error.
// Results pair by position: both runs executed the same request.
func CompareModelFits(report *FidelityReport, req *types.Request, src, syn *types.Response, srcErr, synErr error) {
	if report == nil || req == nil {
		return
	}
	var srcRegs, synRegs []*types.RegressionResult
	if src != nil {
		srcRegs = src.Regressions
	}
	if syn != nil {
		synRegs = syn.Regressions
	}
	n := len(req.Regressions)
	if len(srcRegs) > n {
		n = len(srcRegs)
	}
	if len(synRegs) > n {
		n = len(synRegs)
	}
	u := report.utility()
	for i := 0; i < n; i++ {
		entry := &UtilityModelFit{}
		if i < len(req.Regressions) && req.Regressions[i] != nil {
			spec := req.Regressions[i]
			entry.Name, entry.Type, entry.Target = spec.Name, spec.Type, spec.Target
		}
		var s, y *types.RegressionResult
		if i < len(srcRegs) {
			s = srcRegs[i]
		}
		if i < len(synRegs) {
			y = synRegs[i]
		}
		entry.SourceError = utilitySideError(s, srcErr)
		entry.SyntheticError = utilitySideError(y, synErr)
		if entry.SourceError == "" && s != nil {
			if entry.Name == "" {
				entry.Name = s.Name
			}
			if entry.Type == "" {
				entry.Type = s.Type
			}
		}
		okS, okY := entry.SourceError == "", entry.SyntheticError == ""
		if okS {
			entry.SourceNObs = s.NObs
		}
		if okY {
			entry.SyntheticNObs = y.NObs
		}
		ref := s
		if !okS {
			ref = y
		}
		if ref != nil && (okS || okY) {
			entry.Statistic = UtilityFitR2
			if ref.Type == types.REG_GLM || ref.Family != "" {
				entry.Statistic = UtilityFitPseudoR2
			}
			if okS {
				entry.SourceFit = finiteOrZero(fitStatistic(s, entry.Statistic))
			}
			if okY {
				entry.SyntheticFit = finiteOrZero(fitStatistic(y, entry.Statistic))
			}
			if okS && okY && entry.SourceFit != 0 {
				entry.FitRatio = finiteOrZero(entry.SyntheticFit / entry.SourceFit)
			}
		}
		if okS || okY {
			entry.Coefficients = compareCoefficients(s, y, okS, okY)
		}
		u.ModelFits = append(u.ModelFits, entry)
	}
}

// utilitySideError is one side's failure text, "" when it fitted.
func utilitySideError(r *types.RegressionResult, runErr error) string {
	switch {
	case runErr != nil:
		return runErr.Error()
	case r == nil:
		return "the run returned no result for this regression"
	case r.Error != nil:
		if r.Error.Message != "" {
			return r.Error.Message
		}
		return r.Error.Code
	}
	return ""
}

func fitStatistic(r *types.RegressionResult, statistic string) float64 {
	if statistic == UtilityFitPseudoR2 {
		return r.PseudoR2
	}
	return r.R2
}

// compareCoefficients pairs every term either side estimated, sorted by
// name so the map iteration order never reaches the document.
func compareCoefficients(s, y *types.RegressionResult, okS, okY bool) []*UtilityCoefficient {
	terms := map[string]bool{}
	if okS {
		for k := range s.Coefficients {
			terms[k] = true
		}
	}
	if okY {
		for k := range y.Coefficients {
			terms[k] = true
		}
	}
	names := make([]string, 0, len(terms))
	for k := range terms {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]*UtilityCoefficient, 0, len(names))
	for _, name := range names {
		c := &UtilityCoefficient{Term: name}
		var inS, inY bool
		if okS {
			c.Source, inS = s.Coefficients[name]
			c.SourceStdError = s.StdErrors[name]
		}
		if okY {
			c.Synthetic, inY = y.Coefficients[name]
			c.SyntheticStdError = y.StdErrors[name]
		}
		c.Source, c.Synthetic = finiteOrZero(c.Source), finiteOrZero(c.Synthetic)
		c.SourceStdError, c.SyntheticStdError = finiteOrZero(c.SourceStdError), finiteOrZero(c.SyntheticStdError)
		switch {
		case !inS:
			c.Missing = "source"
		case !inY:
			c.Missing = "synthetic"
		case c.Source != 0:
			c.Ratio = finiteOrZero(c.Synthetic / c.Source)
		}
		out = append(out, c)
	}
	return out
}
