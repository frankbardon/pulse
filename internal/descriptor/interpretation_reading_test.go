package descriptor

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestValueOutputPath: the static `value` / `value.*` path resolves on
// the four value-path categories with the operator's real shape, is
// refused in the wrong shape or with a per-column key, and is refused on
// every category that has no primary value (filterers, groupers, tests,
// regressions, overlays).
func TestValueOutputPath(t *testing.T) {
	cases := []struct {
		op, field string
		want      FieldCheck
	}{
		// single-column, one per category
		{"AGG_SKEWNESS", "value", FieldStatic},
		{"ATTR_ZSCORE", "value", FieldStatic},
		{"WIN_RANK", "value", FieldStatic},
		{"FEAT_LOG", "value", FieldStatic},
		{"AGG_MODE_COUNT", "value", FieldStatic},
		{"AGG_SET_UNION", "value", FieldStatic},
		// multi-column
		{"AGG_WELFORD", "value.*", FieldStatic},
		{"AGG_SET_FREQUENCY", "value.*", FieldStatic},
		{"FEAT_POLY", "value.*", FieldStatic},
		{"FEAT_ONE_HOT", "value.*", FieldStatic},
		{"FEAT_DATE_FEATURES", "value.*", FieldStatic},
		// wrong shape
		{"AGG_SKEWNESS", "value.*", FieldUnknown},
		{"ATTR_ZSCORE", "value.*", FieldUnknown},
		{"AGG_WELFORD", "value", FieldUnknown},
		{"FEAT_POLY", "value", FieldUnknown},
		// per-column keys are not a reading
		{"AGG_WELFORD", "value.mean", FieldUnknown},
		{"FEAT_POLY", "value.x_poly_2", FieldUnknown},
		// aggregator components still resolve beside the value path
		{"AGG_SKEWNESS", "components.m3", FieldStatic},
		{"AGG_WELFORD", "components.n", FieldStatic},
		// nothing else resolves on attribute / window / feature
		{"ATTR_ZSCORE", "components.n", FieldUnknown},
		{"WIN_RANK", "statistic", FieldUnknown},
		// categories with no value path
		{"FILTER_INCLUDE", "value", FieldUnknown},
		{"GROUP_CATEGORY", "value", FieldUnknown},
		{"TEST_T", "value", FieldUnknown},
		{"REG_OLS", "value", FieldUnknown},
		{"OVERLAY_T_CELL", "value", FieldUnknown},
		{"OVERLAY_ZSCORE_CELL", "value.*", FieldUnknown},
		{"normal", "value", FieldUnknown},
	}
	for _, tc := range cases {
		if got, why := BuiltinOutputResolver(tc.op)(tc.field); got != tc.want {
			t.Errorf("%s %q: verdict %d (%s), want %d", tc.op, tc.field, got, why, tc.want)
		}
	}
	// Every value-path operator resolves exactly its own shape.
	for _, s := range PurposeSurfaces() {
		if !valuePathCategories[s.Category] {
			continue
		}
		for _, n := range s.Names {
			other := "value.*"
			if valueField(n) == "value.*" {
				other = "value"
			}
			if got, why := BuiltinOutputResolver(n)(valueField(n)); got != FieldStatic {
				t.Errorf("%s %s: %s", n, valueField(n), why)
			}
			if got, _ := BuiltinOutputResolver(n)(other); got != FieldUnknown {
				t.Errorf("%s resolves %s, the wrong shape", n, other)
			}
		}
	}
}

// TestMultiColumnOutputs pins the multi-column set: exactly these
// value-path operators read as `value.*`, each with the provenance of
// its shape. AGG_MODE_COUNT is single-column at run time (no Rich
// payload) and AGG_SET_UNION / _INTERSECTION emit one label list.
func TestMultiColumnOutputs(t *testing.T) {
	want := []string{"AGG_SET_FREQUENCY", "AGG_WELFORD", "FEAT_DATE_FEATURES", "FEAT_ONE_HOT", "FEAT_POLY"}
	var got []string
	for n, why := range multiColumnOutputs {
		got = append(got, n)
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s: multi-column entry has no provenance", n)
		}
		if cat, ok := surfaceCategory(n); !ok || !valuePathCategories[cat] {
			t.Errorf("%s: not a registered aggregator / attribute / window / feature", n)
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("multi-column operators = %v, want %v", got, want)
	}
}

// readingListProblems reports every descriptive built-in of surfaces in
// neither or both lists, every list entry that is not a registered
// descriptive built-in, every duplicate, and every needs-reading entry
// outside the value-path categories (it could never be read).
func readingListProblems(surfaces []PurposeSurface, needs, self []string) []string {
	var out []string
	descriptive := map[string]string{}
	for _, s := range surfaces {
		switch s.Category {
		case "test", "regression", "matrix", "overlay":
			// Structured results: an Interpretation reads them through
			// their own resolver, never a `value` path.
			continue
		}
		for _, n := range s.Names {
			descriptive[n] = s.Category
		}
	}
	count := func(list []string, label string) map[string]bool {
		in := map[string]bool{}
		for _, n := range list {
			if in[n] {
				out = append(out, n+" is listed twice in "+label)
			}
			in[n] = true
			if _, ok := descriptive[n]; !ok {
				out = append(out, n+" in "+label+" is not a registered descriptive built-in")
			}
		}
		return in
	}
	inNeeds, inSelf := count(needs, "needs-reading"), count(self, "self-reading")
	for _, n := range needs {
		if cat, ok := descriptive[n]; ok && !valuePathCategories[cat] {
			out = append(out, n+" is needs-reading but "+cat+" operators have no value path")
		}
	}
	names := make([]string, 0, len(descriptive))
	for n := range descriptive {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		switch {
		case inNeeds[n] && inSelf[n]:
			out = append(out, n+" is in both the needs-reading and the self-reading list")
		case !inNeeds[n] && !inSelf[n]:
			out = append(out, n+" is in neither the needs-reading nor the self-reading list: classify it")
		}
	}
	return out
}

// TestDescriptiveReadingLists (binding): every descriptive built-in —
// aggregator, attribute, filterer, grouper, window, feature, synth
// distribution — is in exactly one of needsReadingOperators /
// selfReadingOperators, and the needs-reading list is exactly the one
// agreed for U09.
func TestDescriptiveReadingLists(t *testing.T) {
	for _, p := range readingListProblems(PurposeSurfaces(), needsReadingOperators, selfReadingOperators) {
		t.Error(p)
	}
	want := []string{
		"AGG_CI_LOWER", "AGG_CI_UPPER", "AGG_KURTOSIS", "AGG_PERCENTILE", "AGG_RATIO",
		"AGG_SKEWNESS", "AGG_STDDEV", "AGG_VARIANCE", "AGG_WEIGHTED_MEAN", "AGG_WELFORD", "AGG_ZSCORE",
		"ATTR_NORMALIZED", "ATTR_PERCENTILE", "ATTR_REG_FITTED", "ATTR_REG_LEVERAGE",
		"ATTR_REG_RESIDUAL", "ATTR_TSCORE", "ATTR_ZSCORE",
		"FEAT_FREQUENCY_ENCODE", "FEAT_LOG", "FEAT_POLY", "FEAT_SQRT", "FEAT_TARGET_ENCODE",
		"WIN_DELTA", "WIN_DENSE_RANK", "WIN_EWMA", "WIN_MOVING_AVG", "WIN_PCT_CHANGE",
		"WIN_RANK", "WIN_RUNNING_AVG",
	}
	got := slices.Sorted(slices.Values(needsReadingOperators))
	if !slices.Equal(got, want) {
		t.Errorf("needs-reading = %v, want %v", got, want)
	}
}

// TestDescriptiveReadingLists_Falsifiers: an operator in neither list,
// in both, an unregistered or duplicate entry, a needs-reading filterer
// and a newly registered operator each fail.
func TestDescriptiveReadingLists_Falsifiers(t *testing.T) {
	without := func(list []string, drop string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(n string) bool { return n == drop })
	}
	withNew := func() []PurposeSurface {
		out := PurposeSurfaces()
		for i := range out {
			if out[i].Category == "grouper" {
				out[i].Names = append(slices.Clone(out[i].Names), "GROUP_BRAND_NEW")
			}
		}
		return out
	}
	cases := []struct {
		name        string
		surfaces    []PurposeSurface
		needs, self []string
		fragment    string
	}{
		{"neither", PurposeSurfaces(), without(needsReadingOperators, "AGG_SKEWNESS"), selfReadingOperators, "AGG_SKEWNESS is in neither"},
		{"neither (self-reading filterer dropped)", PurposeSurfaces(), needsReadingOperators, without(selfReadingOperators, "FILTER_NULL"), "FILTER_NULL is in neither"},
		{"both", PurposeSurfaces(), needsReadingOperators, append(slices.Clone(selfReadingOperators), "AGG_SKEWNESS"), "AGG_SKEWNESS is in both"},
		{"unregistered", PurposeSurfaces(), append(slices.Clone(needsReadingOperators), "AGG_GHOST"), selfReadingOperators, "AGG_GHOST in needs-reading is not a registered"},
		{"not descriptive", PurposeSurfaces(), needsReadingOperators, append(slices.Clone(selfReadingOperators), "TEST_T"), "TEST_T in self-reading is not a registered"},
		{"duplicate", PurposeSurfaces(), append(slices.Clone(needsReadingOperators), "AGG_ZSCORE"), selfReadingOperators, "AGG_ZSCORE is listed twice"},
		{"needs-reading without a value path", PurposeSurfaces(), append(slices.Clone(needsReadingOperators), "FILTER_NULL"), without(selfReadingOperators, "FILTER_NULL"), "FILTER_NULL is needs-reading but filterer"},
		{"newly registered", withNew(), needsReadingOperators, selfReadingOperators, "GROUP_BRAND_NEW is in neither"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probs := readingListProblems(tc.surfaces, tc.needs, tc.self)
			if len(probs) != 1 || !strings.Contains(probs[0], tc.fragment) {
				t.Errorf("want exactly one problem containing %q, got %v", tc.fragment, probs)
			}
		})
	}
}
