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
var builtinInterpretations = mergeInterpretations(statTestInterpretations, overlayInterpretations, regressionInterpretations, descriptiveInterpretations)

// mergeInterpretations joins the per-category Interpretation maps
// (statTestInterpretations in interpretations_stattests.go,
// overlayInterpretations in interpretations_overlays.go,
// regressionInterpretations in interpretations_regressions.go,
// descriptiveInterpretations in interpretations_descriptive.go),
// panicking when two maps declare the same operator.
func mergeInterpretations(maps ...map[string][]descriptor.Interpretation) map[string][]descriptor.Interpretation {
	out := map[string][]descriptor.Interpretation{}
	for _, m := range maps {
		for name, ins := range m {
			if _, dup := out[name]; dup {
				panic("descriptor: Interpretations for " + name + " declared by two category maps")
			}
			out[name] = ins
		}
	}
	return out
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
		Means: "The chance of seeing a result at least this extreme if the test's null hypothesis were true " +
			"(usually no difference or link; for a shape test, the stated shape) and its assumptions held. " +
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
