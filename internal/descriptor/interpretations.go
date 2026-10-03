package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// builtinInterpretations is the per-operator Interpretation registry:
// how to read each output field of an inferential operator. Keys follow
// builtinPurposes — the TEST_* family (one entry covers both tiers,
// since post-test twins emit identical keys), the REG_* type, the
// OVERLAY_* kind, or an AGG_* / GROUP_* / FILTER_* constant whose
// Response.Components keys are read. Each Field is a normalised output
// path checked by ValidateInterpretations against what the operator
// emits (BuiltinOutputResolver). Like Purpose prose, Interpretation
// prose is served on demand and never inlined into a default payload.
var builtinInterpretations = map[string][]descriptor.Interpretation{
	"TEST_ANOVA_F":   interpTestAnovaF,
	"TEST_PEARSON_R": interpTestPearsonR,
}

// SharedPValue is the shared rule-set key every p-value Interpretation
// cites through Interpretation.Shared.
const SharedPValue = "p-value"

// sharedInterpretations are the rule sets an Interpretation may cite by
// key instead of repeating the reading. Field is left empty on a shared
// rule set: the citing Interpretation names the field (a p-value can sit
// in p_value, summary.p_value, scalar or cells.value).
var sharedInterpretations = map[string]descriptor.Interpretation{
	SharedPValue: {
		Means: "The chance of seeing a result at least this extreme if there were truly no difference or link. " +
			"Below the chosen alpha (0.05 unless the request sets another) the result is called significant.",
		Caveats: []string{
			"Significant is not the same as important: with enough rows a trivial difference is significant, so read the effect size for how big it is.",
			"Not significant does not mean there is no difference: the data may simply be too few or too noisy to detect one.",
			"When more than one test runs, some will be significant by chance alone; adjust for multiple comparisons or treat isolated hits with caution.",
		},
	},
}

// SharedInterpretation returns the shared rule set registered under key.
func SharedInterpretation(key string) (descriptor.Interpretation, bool) {
	in, ok := sharedInterpretations[key]
	return in, ok
}

// SharedInterpretationKeys returns every shared rule-set key, sorted.
func SharedInterpretationKeys() []string {
	out := make([]string, 0, len(sharedInterpretations))
	for k := range sharedInterpretations {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// InterpretationsOf returns a copy of the built-in Interpretations
// declared for name (a manifest entry name; tests by family).
func InterpretationsOf(name string) ([]descriptor.Interpretation, bool) {
	ins, ok := builtinInterpretations[name]
	if !ok {
		return nil, false
	}
	return append([]descriptor.Interpretation(nil), ins...), true
}

// BuiltinInterpretations returns a copy of the built-in Interpretation
// registry.
func BuiltinInterpretations() map[string][]descriptor.Interpretation {
	out := make(map[string][]descriptor.Interpretation, len(builtinInterpretations))
	for k, v := range builtinInterpretations {
		out[k] = append([]descriptor.Interpretation(nil), v...)
	}
	return out
}

// testEffectSizeKeys declares, per TEST_* family, the
// details.effect_size keys the test emits (both tiers). It is the
// declared side the runtime probe (internal/processing) holds two-way
// against the real factories, and the coverage report's expectation of
// which effect sizes an Interpretation should explain.
var testEffectSizeKeys = map[string][]string{
	"TEST_ANOVA_F":        {"eta_squared", "omega_squared"},
	"TEST_ANOVA_RM":       {"partial_eta_squared"},
	"TEST_ANOVA_WELCH":    {"omega_squared"},
	"TEST_CHISQ":          {"cramers_v", "phi"},
	"TEST_KRUSKAL_WALLIS": {"epsilon_squared"},
	"TEST_MANN_WHITNEY_U": {"rank_biserial"},
	"TEST_PAIRED_T":       {"cohens_d"},
	"TEST_PROP_Z":         {"cohens_h"},
	"TEST_T":              {"cohens_d"},
	"TEST_WELCH":          {"cohens_d"},
	"TEST_WILCOXON_SR":    {"rank_biserial"},
	"TEST_Z_TWO_SAMPLE":   {"cohens_d"},
}

// EffectSizeKeysByTest returns a copy of the declared per-family effect
// size keys. Some keys are conditional (TEST_CHISQ phi only on a 2x2
// table; TEST_T cohens_d on every variant, from different formulas), so
// a probe must pick fixtures that trigger each.
func EffectSizeKeysByTest() map[string][]string {
	out := make(map[string][]string, len(testEffectSizeKeys))
	for k, v := range testEffectSizeKeys {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func bandBound(v float64) *float64 { return &v }

// The exemplar Interpretations. Every other inferential built-in is
// listed by the TestInterpretationCoversOutputs coverage report until
// it declares its own.
var (
	interpTestAnovaF = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "F compares how far apart the group averages are with how much rows vary inside each group; " +
				"larger values mean the groups differ by more than within-group noise would explain.",
			Caveats: []string{
				"F says whether some groups differ, not which ones: run TEST_TUKEY_HSD to find the pairs.",
			},
		},
		{Field: "p_value", Shared: SharedPValue},
		{
			Field: "details.effect_size.eta_squared",
			Means: "The share of all variation in the measure that group membership accounts for in this sample, from 0 to 1.",
			Bands: conventionBands(ConventionCohenEta2), Convention: conventionCitation(ConventionCohenEta2),
			Caveats: []string{
				"Eta squared overstates the effect in small samples; prefer omega squared when reporting.",
			},
		},
		{
			Field: "details.effect_size.omega_squared",
			Means: "A less biased estimate of the share of variation group membership accounts for, " +
				"adjusted for sample size and the number of groups.",
			Bands: conventionBands(ConventionCohenEta2), Convention: conventionCitation(ConventionCohenEta2),
			Caveats: []string{
				"Omega squared can come out slightly below zero when the groups barely differ; read that as a negligible effect.",
			},
		},
	}

	interpTestPearsonR = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "r measures how closely the two fields follow a straight line together, from -1 to +1; 0 means no straight-line link.",
			Bands: conventionBands(ConventionCohenR),
			Abs:   conventionOf(ConventionCohenR).Abs, Convention: conventionCitation(ConventionCohenR),
			Sign: map[string]string{
				"+": "the two fields tend to rise together",
				"-": "one field tends to fall as the other rises",
			},
			Caveats: []string{
				"Correlation is not causation: a third factor may drive both fields.",
				"An r near zero rules out only a straight-line link; a curved relationship can still be strong.",
			},
		},
		{Field: "p_value", Shared: SharedPValue},
		{
			Field: "details.ci_low",
			Means: "Lower end of the confidence interval for r at the 1 - alpha level (95% by default).",
			Caveats: []string{
				"With fewer than four pairs, or a perfect r, the interval collapses to r itself.",
			},
		},
		{
			Field: "details.ci_high",
			Means: "Upper end of the confidence interval for r at the 1 - alpha level (95% by default).",
		},
	}
)
