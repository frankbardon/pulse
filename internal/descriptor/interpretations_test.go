package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// inferentialOverlayKinds is the exact set of overlay kinds declared
// Inferential (PRD FR-14): the χ² family, KS, the p-value cell kinds,
// the vs-ref t / z kinds, the prop-z panel and every pairwise kind.
var inferentialOverlayKinds = []types.OverlayKind{
	types.OverlayKindChiSqCol, types.OverlayKindChiSqMatrix, types.OverlayKindChiSqRow,
	types.OverlayKindChiSqVsPop, types.OverlayKindChiSqVsRef,
	types.OverlayKindFisherExactCell, types.OverlayKindKSVsPop,
	types.OverlayKindPairwiseProbitT, types.OverlayKindPairwisePropZ,
	types.OverlayKindPairwiseTwoMeansZ, types.OverlayKindPairwiseWeightedTwoMeansZ,
	types.OverlayKindPairwiseWelchT,
	types.OverlayKindPropZCell, types.OverlayKindPropZPanel,
	types.OverlayKindTCell, types.OverlayKindTVsRef,
	types.OverlayKindZCell, types.OverlayKindZVsRef,
}

// expectedInterpretationFields is the coverage report's expectation for
// one inferential built-in: tests explain statistic + p_value plus every
// effect size they emit; regressions their coefficients and p-values
// (credible intervals for the Bayesian fit); inferential overlays the
// slot carrying their number per declared shape.
func expectedInterpretationFields(cat, name string) []string {
	switch cat {
	case "test":
		out := []string{"p_value", "statistic"}
		for _, k := range testEffectSizeKeys[name] {
			out = append(out, "details.effect_size."+k)
		}
		return out
	case "regression":
		if name == string(types.REG_BAYES_LINEAR) {
			return []string{"coefficients.*", "credible_intervals.*"}
		}
		return []string{"coefficients.*", "p_values.*"}
	case "overlay":
		var out []string
		for _, c := range OverlayCapabilities() {
			if string(c.Kind) != name || !c.Inferential {
				continue
			}
			for _, s := range c.Shapes {
				switch s {
				case types.OverlayShapeScalar:
					out = append(out, "scalar", "summary.p_value")
				case types.OverlayShapeMatrix:
					out = append(out, "cells.value")
				case types.OverlayShapeSeries:
					out = append(out, "summary.statistic")
				}
			}
		}
		return out
	}
	return nil
}

// TestInterpretationCoversOutputs is the two-tier Interpretation gate.
// Validity (binding): every built-in Interpretation passes
// ValidateInterpretations against its static output resolver — Field
// a well-formed path naming something the operator emits, Means or a
// known Shared rule set, Bands only with a Convention and ordered
// without overlap, Sign keys "+" / "-" — and every registry key is a
// registered built-in with at least one entry. Coverage (report-only):
// every inferential test family, regression and Inferential overlay kind
// lacking an Interpretation for its expected outputs is logged, grouped
// by category.
func TestInterpretationCoversOutputs(t *testing.T) {
	for _, v := range interpretationRegistryViolations(builtinInterpretations) {
		t.Error(v)
	}
	for name, ins := range builtinInterpretations {
		if _, ok := surfaceCategory(name); !ok {
			t.Errorf("Interpretation declared for unregistered name %s", name)
		}
		if len(ins) == 0 {
			t.Errorf("%s declares an empty Interpretation list", name)
		}
	}

	var report strings.Builder
	missing, total := 0, 0
	for _, s := range PurposeSurfaces() {
		switch s.Category {
		case "test", "regression", "overlay":
		default:
			continue
		}
		var lack []string
		for _, n := range s.Names {
			want := expectedInterpretationFields(s.Category, n)
			if len(want) == 0 {
				continue
			}
			total++
			have := map[string]bool{}
			for _, in := range builtinInterpretations[n] {
				have[in.Field] = true
			}
			var gaps []string
			for _, f := range want {
				if !have[f] {
					gaps = append(gaps, f)
				}
			}
			if len(gaps) > 0 {
				lack = append(lack, n+"["+strings.Join(gaps, " ")+"]")
			}
		}
		missing += len(lack)
		if len(lack) > 0 {
			report.WriteString("\n  " + s.Category + " (" + strconv.Itoa(len(lack)) + "): " + strings.Join(lack, ", "))
		}
	}
	t.Logf("Interpretation coverage: %d of %d inferential built-ins lack an Interpretation for an expected output:%s", missing, total, report.String())
}

func validInterpretationFixture() []descriptor.Interpretation {
	lo, hi := 0.1, 0.3
	return []descriptor.Interpretation{
		{Field: "statistic", Means: "the statistic", Bands: []descriptor.Band{
			{Max: &lo, Label: "small"}, {Min: &lo, Max: &hi, Label: "medium"}, {Min: &hi, Label: "large"},
		}, Abs: true, Convention: "Cohen (1988)", Sign: map[string]string{"+": "up", "-": "down"}},
		{Field: "p_value", Shared: SharedPValue},
		{Field: "details.effect_size.eta_squared", Means: "share explained"},
	}
}

// TestValidateInterpretations_Rules falsifies every validity rule with a
// bad fixture: each broken Interpretation reports exactly the rule it
// breaks, and the valid fixture reports nothing.
func TestValidateInterpretations_Rules(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	if v := ValidateInterpretations("TEST_ANOVA_F", validInterpretationFixture(), BuiltinOutputResolver("TEST_ANOVA_F")); len(v) != 0 {
		t.Fatalf("valid fixture reported violations: %v", v)
	}
	cases := []struct {
		name string
		op   string
		in   descriptor.Interpretation
		rule InterpretationRule
	}{
		{"unknown struct path", "TEST_ANOVA_F", descriptor.Interpretation{Field: "statistics", Means: "m"}, InterpretationRuleFieldUnknown},
		{"non-numeric struct path", "TEST_ANOVA_F", descriptor.Interpretation{Field: "label", Means: "m"}, InterpretationRuleFieldUnknown},
		{"details itself", "TEST_ANOVA_F", descriptor.Interpretation{Field: "details", Means: "m"}, InterpretationRuleFieldUnknown},
		{"nested scalar", "TEST_ANOVA_F", descriptor.Interpretation{Field: "p_value.low", Means: "m"}, InterpretationRuleFieldUnknown},
		{"r2 on GLM", "REG_GLM", descriptor.Interpretation{Field: "r2", Means: "m"}, InterpretationRuleFieldUnknown},
		{"p_values on Bayes", "REG_BAYES_LINEAR", descriptor.Interpretation{Field: "p_values.*", Means: "m"}, InterpretationRuleFieldUnknown},
		{"pseudo_r2 on OLS", "REG_OLS", descriptor.Interpretation{Field: "pseudo_r2", Means: "m"}, InterpretationRuleFieldUnknown},
		{"map without pattern", "REG_OLS", descriptor.Interpretation{Field: "coefficients", Means: "m"}, InterpretationRuleFieldUnknown},
		{"map with predictor key", "REG_OLS", descriptor.Interpretation{Field: "coefficients.age", Means: "m"}, InterpretationRuleFieldUnknown},
		{"scalar on matrix-only overlay", "OVERLAY_T_CELL", descriptor.Interpretation{Field: "scalar", Means: "m"}, InterpretationRuleFieldUnknown},
		{"cells on scalar-only overlay", "OVERLAY_CHISQ_VS_POP", descriptor.Interpretation{Field: "cells.value", Means: "m"}, InterpretationRuleFieldUnknown},
		{"unknown summary key", "OVERLAY_CHISQ_MATRIX", descriptor.Interpretation{Field: "summary.d", Means: "m"}, InterpretationRuleFieldUnknown},
		{"unknown component key", "AGG_AVERAGE", descriptor.Interpretation{Field: "components.bogus", Means: "m"}, InterpretationRuleFieldUnknown},
		{"category without outputs", "ATTR_ZSCORE", descriptor.Interpretation{Field: "statistic", Means: "m"}, InterpretationRuleFieldUnknown},
		{"unregistered operator", "TEST_GHOST", descriptor.Interpretation{Field: "statistic", Means: "m"}, InterpretationRuleFieldUnknown},
		{"empty field", "TEST_ANOVA_F", descriptor.Interpretation{Means: "m"}, InterpretationRuleField},
		{"uppercase segment", "TEST_ANOVA_F", descriptor.Interpretation{Field: "details.Effect", Means: "m"}, InterpretationRuleField},
		{"leading wildcard", "REG_OLS", descriptor.Interpretation{Field: "*", Means: "m"}, InterpretationRuleField},
		{"inner wildcard", "TEST_ANOVA_F", descriptor.Interpretation{Field: "details.*.x", Means: "m"}, InterpretationRuleField},
		{"duplicate field", "TEST_ANOVA_F", descriptor.Interpretation{Field: "statistic", Means: "again"}, InterpretationRuleField},
		{"no means", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df"}, InterpretationRuleMeans},
		{"unknown shared key", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Shared: "q-value"}, InterpretationRuleShared},
		{"bands without convention", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Bands: []descriptor.Band{{Min: ptr(0), Label: "any"}}}, InterpretationRuleConvention},
		{"convention without bands", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "Cohen (1988)"}, InterpretationRuleConvention},
		{"overlapping bands", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "c", Bands: []descriptor.Band{
			{Min: ptr(0), Max: ptr(0.5), Label: "a"}, {Min: ptr(0.3), Max: ptr(1), Label: "b"}}}, InterpretationRuleBands},
		{"unordered bands", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "c", Bands: []descriptor.Band{
			{Min: ptr(0.5), Max: ptr(1), Label: "a"}, {Min: ptr(0), Max: ptr(0.5), Label: "b"}}}, InterpretationRuleBands},
		{"open below in the middle", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "c", Bands: []descriptor.Band{
			{Max: ptr(0.5), Label: "a"}, {Max: ptr(1), Label: "b"}}}, InterpretationRuleBands},
		{"open above in the middle", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "c", Bands: []descriptor.Band{
			{Min: ptr(0), Label: "a"}, {Min: ptr(1), Label: "b"}}}, InterpretationRuleBands},
		{"empty band", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "c", Bands: []descriptor.Band{
			{Min: ptr(1), Max: ptr(1), Label: "a"}}}, InterpretationRuleBands},
		{"unlabelled band", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Convention: "c", Bands: []descriptor.Band{
			{Min: ptr(0)}}}, InterpretationRuleBands},
		{"bad sign key", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Sign: map[string]string{"up": "x"}}, InterpretationRuleSign},
		{"empty sign meaning", "TEST_ANOVA_F", descriptor.Interpretation{Field: "df", Means: "m", Sign: map[string]string{"+": " "}}, InterpretationRuleSign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ins := append(validInterpretationFixture(), tc.in)
			got := ValidateInterpretations(tc.op, ins, BuiltinOutputResolver(tc.op))
			if tc.op != "TEST_ANOVA_F" {
				// Only the injected entry is judged on a non-test operator.
				got = ValidateInterpretations(tc.op, []descriptor.Interpretation{tc.in}, BuiltinOutputResolver(tc.op))
			}
			if len(got) != 1 || got[0].Rule != tc.rule || got[0].Field != tc.in.Field {
				t.Errorf("want exactly one %q violation on %q, got %v", tc.rule, tc.in.Field, got)
			}
		})
	}
}

// TestValidateInterpretations_AcceptsEveryAnchor: each anchor family
// resolves a representative good path, and map-valued paths are
// Deferred to the runtime probe.
func TestValidateInterpretations_AcceptsEveryAnchor(t *testing.T) {
	cases := []struct {
		op, field string
		want      FieldCheck
	}{
		{"TEST_T", "statistic", FieldStatic},
		{"TEST_T", "df", FieldStatic},
		{"TEST_T", "reject_null", FieldStatic},
		{"TEST_T", "details.effect_size.cohens_d", FieldDeferred},
		{"TEST_T", "details.ci_low", FieldDeferred},
		{"REG_OLS", "r2", FieldStatic},
		{"REG_OLS", "p_values.*", FieldStatic},
		{"REG_GLM", "pseudo_r2", FieldStatic},
		{"REG_BAYES_LINEAR", "credible_intervals.*", FieldStatic},
		{"OVERLAY_CHISQ_VS_REF", "scalar", FieldStatic},
		{"OVERLAY_T_CELL", "cells.value", FieldStatic},
		{"OVERLAY_T_VS_REF", "summary.statistic", FieldStatic},
		{"OVERLAY_CHISQ_MATRIX", "summary.p_value", FieldStatic},
		{"OVERLAY_CHISQ_MATRIX", "summary.parameters.df", FieldDeferred},
		{"AGG_AVERAGE", "components.n", FieldStatic},
		{"GROUP_CATEGORY", "components.total_n", FieldStatic},
		{"FILTER_INCLUDE", "components.n_out", FieldStatic},
	}
	for _, tc := range cases {
		if got, why := BuiltinOutputResolver(tc.op)(tc.field); got != tc.want {
			t.Errorf("%s %q: verdict %d (%s), want %d", tc.op, tc.field, got, why, tc.want)
		}
	}
	// An operator's own ComponentSchema keys resolve too.
	for _, o := range aggregatorCapabilities() {
		for _, k := range o.ComponentSchema.Keys {
			if got, why := BuiltinOutputResolver(o.Name)("components." + k.Name); got != FieldStatic {
				t.Errorf("%s components.%s: %s", o.Name, k.Name, why)
			}
		}
	}
}

// TestValidateInterpretations_StructureOnly: a nil resolver (the
// extension path) still checks path syntax and content but never
// judges existence.
func TestValidateInterpretations_StructureOnly(t *testing.T) {
	ins := []descriptor.Interpretation{
		{Field: "statistics", Means: "m"},
		{Field: "details.effect_size.hedges_g", Means: "m"},
	}
	if v := ValidateInterpretations("TEST_EXT_THING", ins, nil); len(v) != 0 {
		t.Errorf("structure-only mode judged existence: %v", v)
	}
	bad := []descriptor.Interpretation{{Field: "Statistic", Shared: "nope"}}
	got := ValidateInterpretations("TEST_EXT_THING", bad, nil)
	rules := map[InterpretationRule]bool{}
	for _, v := range got {
		rules[v.Rule] = true
	}
	if !rules[InterpretationRuleField] || !rules[InterpretationRuleShared] || len(got) != 2 {
		t.Errorf("structure-only mode: want field + shared violations, got %v", got)
	}
}

// TestRegressionOutputs_AreResultTags: the per-regression-type
// applicability table names real RegressionResult JSON keys and covers
// every registered regression, two-way.
func TestRegressionOutputs_AreResultTags(t *testing.T) {
	tags := jsonTags(reflect.TypeOf(types.RegressionResult{}))
	for reg, keys := range regressionOutputs {
		for _, k := range keys {
			if _, ok := tags[k]; !ok {
				t.Errorf("%s: %q is not a RegressionResult JSON key", reg, k)
			}
		}
	}
	var regs []string
	for _, r := range regressionCapabilities() {
		regs = append(regs, r.Name)
		if _, ok := regressionOutputs[r.Name]; !ok {
			t.Errorf("regression %s has no applicability row", r.Name)
		}
	}
	for reg := range regressionOutputs {
		if !slices.Contains(regs, reg) {
			t.Errorf("applicability row %s names no registered regression", reg)
		}
	}
}

// TestOverlayCapabilities_InferentialFlag pins the declared Inferential
// set exactly and the manifest projection: "inferential":true rides the
// inferential kinds only and is omitted (omitempty) on every other kind.
// Its bytes count toward TestManifestGuidanceBudget.
func TestOverlayCapabilities_InferentialFlag(t *testing.T) {
	want := map[types.OverlayKind]bool{}
	for _, k := range inferentialOverlayKinds {
		want[k] = true
	}
	for _, c := range OverlayCapabilities() {
		if c.Inferential != want[c.Kind] {
			t.Errorf("%s: Inferential = %v, want %v", c.Kind, c.Inferential, want[c.Kind])
		}
	}
	raw, err := json.Marshal(BuildManifest().Overlays)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		kind := types.OverlayKind(e["kind"].(string))
		v, has := e["inferential"]
		switch {
		case want[kind] && v != true:
			t.Errorf("manifest %s: inferential = %v, want true", kind, v)
		case !want[kind] && has:
			t.Errorf("manifest %s: carries inferential key, want it omitted", kind)
		}
		if want[kind] {
			seen++
		}
	}
	if seen != len(inferentialOverlayKinds) {
		t.Errorf("manifest lists %d inferential kinds, want %d", seen, len(inferentialOverlayKinds))
	}
}

// TestInterpretationExemplars pins the exemplar Interpretations (PRD
// FR-15): TEST_ANOVA_F reads F, the shared p-value and both effect
// sizes on Cohen's labelled bands; TEST_PEARSON_R reads r on absolute
// Cohen bands with sign meanings, the shared p-value and its interval.
func TestInterpretationExemplars(t *testing.T) {
	byField := func(name string) map[string]descriptor.Interpretation {
		ins, ok := InterpretationsOf(name)
		if !ok {
			t.Fatalf("%s declares no Interpretation", name)
		}
		out := map[string]descriptor.Interpretation{}
		for _, in := range ins {
			out[in.Field] = in
		}
		return out
	}
	anova := byField("TEST_ANOVA_F")
	if anova["statistic"].Means == "" {
		t.Error("TEST_ANOVA_F statistic has no Means")
	}
	for _, f := range []string{"details.effect_size.eta_squared", "details.effect_size.omega_squared"} {
		if in := anova[f]; len(in.Bands) == 0 || in.Convention == "" {
			t.Errorf("TEST_ANOVA_F %s: want labelled-convention bands, got %+v", f, in)
		}
	}
	pearson := byField("TEST_PEARSON_R")
	r := pearson["statistic"]
	if !r.Abs || len(r.Bands) == 0 || r.Convention == "" || r.Sign["+"] == "" || r.Sign["-"] == "" {
		t.Errorf("TEST_PEARSON_R statistic: want absolute Cohen bands and both sign meanings, got %+v", r)
	}
	for _, f := range []string{"details.ci_low", "details.ci_high"} {
		if pearson[f].Means == "" {
			t.Errorf("TEST_PEARSON_R %s has no Means", f)
		}
	}
	for name, m := range map[string]map[string]descriptor.Interpretation{"TEST_ANOVA_F": anova, "TEST_PEARSON_R": pearson} {
		if m["p_value"].Shared != SharedPValue {
			t.Errorf("%s p_value: Shared = %q, want %q", name, m["p_value"].Shared, SharedPValue)
		}
	}
}

// TestSharedPValueRules (PRD FR-10): the shared p-value rule set says
// what alpha means, that significant is not important, that not
// significant is not "no difference", and warns about multiple
// comparisons.
func TestSharedPValueRules(t *testing.T) {
	in, ok := SharedInterpretation(SharedPValue)
	if !ok {
		t.Fatal("no shared p-value rule set")
	}
	if !strings.Contains(in.Means, "alpha") {
		t.Errorf("Means does not explain alpha: %q", in.Means)
	}
	body := strings.ToLower(strings.Join(in.Caveats, " "))
	for _, want := range []string{"not the same as important", "does not mean there is no difference", "multiple comparisons"} {
		if !strings.Contains(body, want) {
			t.Errorf("shared p-value caveats miss %q", want)
		}
	}
	if in.Field != "" || in.Shared != "" {
		t.Errorf("a shared rule set names no field and cites no other set, got %+v", in)
	}
	if !slices.Equal(SharedInterpretationKeys(), []string{SharedPValue}) {
		t.Errorf("SharedInterpretationKeys = %v", SharedInterpretationKeys())
	}
}

// TestDeclaredInterpretationFields pins the seam the runtime probes walk:
// every declared pair, sorted, with map-valued paths marked Deferred.
func TestDeclaredInterpretationFields(t *testing.T) {
	got := DeclaredInterpretationFields()
	n := 0
	for _, ins := range builtinInterpretations {
		n += len(ins)
	}
	if len(got) != n {
		t.Fatalf("DeclaredInterpretationFields lists %d pairs, registry declares %d", len(got), n)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		return got[i].Name < got[j].Name || (got[i].Name == got[j].Name && got[i].Field < got[j].Field)
	}) {
		t.Error("pairs not sorted by name then field")
	}
	for _, p := range got {
		if p.Category != "test" && p.Category != "overlay" {
			t.Errorf("%s: category %q", p.Name, p.Category)
		}
		wantDeferred := strings.HasPrefix(p.Field, "details.") || strings.HasPrefix(p.Field, "summary.parameters.")
		if p.Deferred != wantDeferred {
			t.Errorf("%s %s: Deferred = %v, want %v", p.Name, p.Field, p.Deferred, wantDeferred)
		}
	}
}

// TestEffectSizeKeysByTest: the declared per-family effect sizes name
// registered test families and, together, exactly the effect-size keys
// the glossary covers (effectSizeKeys).
func TestEffectSizeKeysByTest(t *testing.T) {
	union := map[string]bool{}
	for fam, keys := range EffectSizeKeysByTest() {
		if cat, ok := surfaceCategory(fam); !ok || cat != "test" {
			t.Errorf("%s is not a registered test family", fam)
		}
		for _, k := range keys {
			union[k] = true
		}
	}
	var got []string
	for k := range union {
		got = append(got, k)
	}
	sort.Strings(got)
	if !slices.Equal(got, effectSizeKeys) {
		t.Errorf("declared effect-size keys %v, want %v", got, effectSizeKeys)
	}
}
