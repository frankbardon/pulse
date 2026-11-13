package model

import (
	"slices"

	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// A model regression is a built-in REG_* type authored on the
// extension contract (extend.Regression): it is registered in every
// instance and listed in types.AllRegressionTypes, but its fit is shaped
// like an extension regression's — it names its own coefficients beside
// the intercept and the predictors, a saved fit is a model file version
// 3, and its scorer scores the whole model (no params.terms, no
// lower / upper bound). Unlike an extension's, its coefficient names are
// declared: ModelRegressionTerms derives them from the spec alone, so
// predict validates a coef() term statically.

// The curve-coefficient prefixes of the media-mix models: channel c's
// Hill half-saturation is the coefficient MMMHalfSaturationPrefix + c,
// its shape MMMShapePrefix + c, and under REG_MMM_BAYES its adstock
// kernel's params (MMMBayesAdstockPrefixes): the decay MMMDecayPrefix + c
// (geometric, delayed), the peak MMMPeakPrefix + c (delayed), the
// Weibull shape MMMAdstockShapePrefix + c and scale
// MMMAdstockScalePrefix + c (weibull_cdf, weibull_pdf), and its
// saturation curve's params (MMMBayesCurvePrefixes): Hill's k: and s:,
// Michaelis–Menten's half-saturation k:, the logistic curve's rate
// MMMLogisticLambdaPrefix + c; REG_MMM_BAYES's noise SD is MMMSigmaName.
const (
	MMMHalfSaturationPrefix = "k:"
	MMMShapePrefix          = "s:"
	MMMLogisticLambdaPrefix = "lam:"
	MMMDecayPrefix          = "decay:"
	MMMPeakPrefix           = "peak:"
	MMMAdstockShapePrefix   = "shape:"
	MMMAdstockScalePrefix   = "scale:"
	MMMSigmaName            = "sigma"
)

// MMMBayesAdstockPrefixes is the coefficient prefixes of REG_MMM_BAYES's
// adstock kernel params, in the coefficient table's order, for kernel
// (params.adstock; "" is the default, geometric): decay: for geometric,
// decay: then peak: for delayed, shape: then scale: for weibull_cdf and
// weibull_pdf. ok is false for any other kernel name.
func MMMBayesAdstockPrefixes(kernel string) (prefixes []string, ok bool) {
	switch kernel {
	case "", distlagparams.KernelGeometric:
		return []string{MMMDecayPrefix}, true
	case distlagparams.KernelDelayed:
		return []string{MMMDecayPrefix, MMMPeakPrefix}, true
	case distlagparams.KernelWeibullCDF, distlagparams.KernelWeibullPDF:
		return []string{MMMAdstockShapePrefix, MMMAdstockScalePrefix}, true
	}
	return nil, false
}

// REG_MMM_BAYES's saturation curves (params.saturation; absent is
// hill): one curve for every channel, on the channel's adstock.
const (
	// MMMSaturationHill is 1 / (1 + (k/a)^s), the default.
	MMMSaturationHill = "hill"
	// MMMSaturationLogistic is (1 − e^(−λa)) / (1 + e^(−λa)).
	MMMSaturationLogistic = "logistic"
	// MMMSaturationMichaelisMenten is a / (k + a).
	MMMSaturationMichaelisMenten = "michaelis_menten"
)

// MMMBayesSaturations are the params.saturation values, in manifest
// order (a fresh slice).
func MMMBayesSaturations() []string {
	return []string{MMMSaturationHill, MMMSaturationLogistic, MMMSaturationMichaelisMenten}
}

// MMMBayesCurvePrefixes is the coefficient prefixes of REG_MMM_BAYES's
// saturation curve params, in the coefficient table's order, for
// saturation (params.saturation; "" is the default, hill): k: then s:
// for hill, lam: for logistic, k: for michaelis_menten. ok is false for
// any other curve name.
func MMMBayesCurvePrefixes(saturation string) (prefixes []string, ok bool) {
	switch saturation {
	case "", MMMSaturationHill:
		return []string{MMMHalfSaturationPrefix, MMMShapePrefix}, true
	case MMMSaturationLogistic:
		return []string{MMMLogisticLambdaPrefix}, true
	case MMMSaturationMichaelisMenten:
		return []string{MMMHalfSaturationPrefix}, true
	}
	return nil, false
}

// MMMBayesSaturation is a REG_MMM_BAYES spec's params.saturation as its
// curve record carries it: "" for the default (absent or hill), the
// name as written otherwise (the factory refuses an unknown one). Any
// other type, or params that do not decode, read "".
func MMMBayesSaturation(spec *types.RegressionSpec) string {
	if spec == nil || spec.Type != types.REG_MMM_BAYES || len(spec.Params) == 0 {
		return ""
	}
	var p struct {
		Saturation *string `json:"saturation"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Saturation == nil || *p.Saturation == MMMSaturationHill {
		return ""
	}
	return *p.Saturation
}

// modelRegressions are the model regressions, in AllRegressionTypes
// order.
var modelRegressions = []types.RegressionType{types.REG_MMM_BAYES, types.REG_MMM_HILL, types.REG_ORDINAL}

// AdmitToken is the parameter that makes a model's row-admission hook
// (a model regression's AdmitRow(extend.Record, AdmitToken) bool, read by
// internal/regadapt) unimplementable outside this module: only a model
// regression owns a listwise rule beyond the engine's — REG_ORDINAL's
// response outside params.levels, its null cluster — so the adapter
// counts n_obs over the rows the model actually fits.
type AdmitToken struct{}

// OrdinalThresholdSeparator joins two adjacent levels into a REG_ORDINAL
// threshold's coefficient name, "<level>|<next level>" (polr's zeta).
const OrdinalThresholdSeparator = "|"

// ModelRegression reports whether t is a model regression: a built-in
// authored on the extension contract (see the file comment).
func ModelRegression(t types.RegressionType) bool {
	return slices.Contains(modelRegressions, t)
}

// ModelRegressions returns the model regressions, in a fresh slice.
func ModelRegressions() []types.RegressionType {
	return slices.Clone(modelRegressions)
}

// ModelRegressionTerms is a model regression spec's term catalog: the
// coefficient names its fit reports beyond the intercept and the
// predictors, derived from the spec alone, and whether they are known.
// REG_MMM_HILL names "k:<channel>" for every params.channels entry, then
// "s:<channel>" for each; REG_MMM_BAYES names its params.adstock
// kernel's params per channel (MMMBayesAdstockPrefixes: "decay:<channel>"
// for the default geometric kernel; an unknown kernel reports false),
// then its params.saturation curve's (MMMBayesCurvePrefixes: "k:<channel>"
// then "s:<channel>" for the default Hill curve; an unknown curve
// reports false) for each, then "sigma"; REG_ORDINAL
// names its thresholds "<level>|<next level>", known only when
// params.levels fixes the levels (otherwise the data does). A spec whose
// params do not decode (its factory refuses it), or a type that is no
// model regression, reports false — the caller then treats the names as
// unknown.
func ModelRegressionTerms(spec *types.RegressionSpec) ([]string, bool) {
	if spec != nil && spec.Type == types.REG_ORDINAL {
		return ordinalThresholds(spec)
	}
	if spec == nil || (spec.Type != types.REG_MMM_HILL && spec.Type != types.REG_MMM_BAYES) {
		return nil, false
	}
	var p struct {
		Channels   []string `json:"channels"`
		Adstock    *string  `json:"adstock"`
		Saturation *string  `json:"saturation"`
	}
	// A lenient look: the model's factory decodes (and records) params.
	if len(spec.Params) == 0 || paramread.Probe(spec.Params, &p) != nil || len(p.Channels) == 0 {
		return nil, false
	}
	prefixes := []string{MMMHalfSaturationPrefix, MMMShapePrefix}
	if spec.Type == types.REG_MMM_BAYES {
		kernel := ""
		if p.Adstock != nil {
			kernel = *p.Adstock
		}
		ad, ok := MMMBayesAdstockPrefixes(kernel)
		if !ok {
			return nil, false
		}
		saturation := ""
		if p.Saturation != nil {
			saturation = *p.Saturation
		}
		curve, ok := MMMBayesCurvePrefixes(saturation)
		if !ok {
			return nil, false
		}
		prefixes = append(ad, curve...)
	}
	out := make([]string, 0, len(prefixes)*len(p.Channels)+1)
	for _, prefix := range prefixes {
		for _, c := range p.Channels {
			out = append(out, prefix+c)
		}
	}
	if spec.Type == types.REG_MMM_BAYES {
		out = append(out, MMMSigmaName)
	}
	return out, true
}

// ordinalThresholds is REG_ORDINAL's threshold names from params.levels
// (false without the list, or when params do not decode).
func ordinalThresholds(spec *types.RegressionSpec) ([]string, bool) {
	var p struct {
		Levels []string `json:"levels"`
	}
	if len(spec.Params) == 0 || paramread.Probe(spec.Params, &p) != nil || len(p.Levels) < 2 {
		return nil, false
	}
	out := make([]string, 0, len(p.Levels)-1)
	for k := 0; k+1 < len(p.Levels); k++ {
		out = append(out, p.Levels[k]+OrdinalThresholdSeparator+p.Levels[k+1])
	}
	return out, true
}

// ModelRegressionFieldInputs is the fields a model regression spec reads
// per record beyond its target and predictors, derived from the spec
// alone — a built-in's twin of an extension's FieldInputs hook. The
// runtime projection decodes them and predict's field-reference walk
// refuses an unknown one. REG_MMM_BAYES reads params.time; every other
// type, and a spec whose params do not decode, reads none (nil).
func ModelRegressionFieldInputs(spec *types.RegressionSpec) []string {
	if spec == nil || spec.Type != types.REG_MMM_BAYES || len(spec.Params) == 0 {
		return nil
	}
	var p struct {
		Time string `json:"time"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Time == "" {
		return nil
	}
	return []string{p.Time}
}
