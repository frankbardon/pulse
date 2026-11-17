package model

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multinomial_test.go pins the bound REG_MULTINOMIAL fit (U68 E1-S2):
// its outcome rule (params.category, output probability / class, the
// log-odds link scale), its categories as the Signature's outcomes
// (bound, model file), its model-file section (feature categories),
// coef() over its "<category>:<term>" names, and the params.fit slots
// that cannot score it.

// multinomialFit is a REG_MULTINOMIAL fit of c on x1, x2: categories
// mid, lo, hi (dictionary order), reference lo.
func multinomialFit() (*types.RegressionSpec, *types.RegressionResult) {
	spec := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Name: "m", Target: "c", Predictors: []string{"x1", "x2"}}
	coefs := map[string]float64{}
	ses := map[string]float64{}
	v := 0.25
	for _, cat := range []string{"mid", "hi"} {
		for _, term := range []string{InterceptKey, "x1", "x2"} {
			coefs[MultinomialKey(cat, term)] = v
			ses[MultinomialKey(cat, term)] = v / 3
			v += 0.5
		}
	}
	res := &types.RegressionResult{Name: "m", Type: types.REG_MULTINOMIAL, Reference: "lo", Categories: []string{"mid", "lo", "hi"},
		Coefficients: coefs, StdErrors: ses, ConfLevel: 0.95, NObs: 90}
	res.CILower, res.CIUpper = map[string]float64{}, map[string]float64{}
	for k, b := range coefs {
		res.CILower[k], res.CIUpper[k] = b-1, b+1
	}
	return spec, res
}

func multinomialCatalog(t *testing.T) (FitCatalog, *Set) {
	t.Helper()
	spec, res := multinomialFit()
	cat := CatalogFromFits([]types.FitSpec{
		{Name: "m", Request: olsReq(spec)},
		{Name: "ols", Request: olsReq(&types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}})},
	})
	b, err := Bind("m", spec, res)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	set := &Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	return cat, set
}

func TestCheckScore_MultinomialOutcome(t *testing.T) {
	t.Parallel()
	cat, set := multinomialCatalog(t)
	cases := []struct {
		name, params string
		// static is predict's verdict over the `fits` entry (categories
		// unknown), runtime the bound fit's; "" admits.
		static, runtime string
		slot            string
	}{
		{"category (probability)", `{"fit":"m","category":"hi"}`, "", "", ""},
		{"explicit probability", `{"fit":"m","category":"mid","output":"probability"}`, "", "", ""},
		{"class", `{"fit":"m","output":"class"}`, "", "", ""},
		{"log-odds", `{"fit":"m","category":"hi","scale":"link"}`, "", "", ""},
		{"reference log-odds", `{"fit":"m","category":"lo","scale":"link"}`, "", "", ""},
		{"response scale", `{"fit":"m","category":"hi","scale":"response"}`, "", "", ""},
		{"default needs a category", `{"fit":"m"}`, ReasonOutcomeRequired, ReasonOutcomeRequired, ""},
		{"probability without category", `{"fit":"m","output":"probability"}`, ReasonOutcomeRequired, ReasonOutcomeRequired, ""},
		{"link without category", `{"fit":"m","scale":"link"}`, ReasonOutcomeRequired, ReasonOutcomeRequired, ""},
		{"unknown category", `{"fit":"m","category":"top"}`, "", ReasonOutcomeUnknown, "params.category"},
		{"unknown category on the link scale", `{"fit":"m","category":"top","scale":"link"}`, "", ReasonOutcomeUnknown, "params.category"},
		{"category beside class", `{"fit":"m","category":"hi","output":"class"}`, ReasonOutcomeUnused, ReasonOutcomeUnused, "params.category"},
		{"cumulative is ordinal's", `{"fit":"m","category":"hi","output":"cumulative"}`, ReasonOutputUnknown, ReasonOutputUnknown, ""},
		{"expected is ordinal's", `{"fit":"m","output":"expected"}`, ReasonOutputUnknown, ReasonOutputUnknown, ""},
		{"output beside link", `{"fit":"m","category":"hi","scale":"link","output":"probability"}`, ReasonOutputScale, ReasonOutputScale, ""},
		{"level is ordinal's", `{"fit":"m","level":"hi"}`, ReasonOutcomeUnsupported, ReasonOutcomeUnsupported, "params.level"},
		{"terms", `{"fit":"m","category":"hi","terms":["x1"]}`, ReasonTermsUnsupported, ReasonTermsUnsupported, ""},
		{"bound", `{"fit":"m","category":"hi","bound":"lower"}`, ReasonBoundUnsupported, ReasonBoundUnsupported, ""},
		{"category on OLS", `{"fit":"ols","category":"hi"}`, ReasonOutcomeUnsupported, "", "params.category"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, side := range []struct {
				name   string
				cat    Catalog
				reason string
			}{{"predict", cat, tc.static}, {"runtime", set, tc.runtime}} {
				if side.name == "runtime" && tc.params == `{"fit":"ols","category":"hi"}` {
					continue // the set binds no OLS fit
				}
				_, _, err := CheckScore(scoreAttr(tc.params), side.cat)
				if side.reason == "" {
					if err != nil {
						t.Fatalf("%s refused: %v", side.name, err)
					}
					continue
				}
				if err == nil || err.Code != errors.PULSE_MODEL_SCORE_INVALID || err.Details["reason"] != side.reason {
					t.Fatalf("%s: %v, want %s", side.name, err, side.reason)
				}
				if tc.slot != "" && err.Details["slot"] != tc.slot {
					t.Fatalf("%s details.slot %v, want %s", side.name, err.Details["slot"], tc.slot)
				}
			}
		})
	}
	_, _, err := CheckScore(scoreAttr(`{"fit":"m","category":"top"}`), set)
	if got, _ := err.Details["available"].([]string); !slices.Equal(got, []string{"mid", "lo", "hi"}) {
		t.Errorf("available %v, want the fit's categories", err.Details["available"])
	}
	_, _, err = CheckScore(scoreAttr(`{"fit":"m","scale":"link"}`), set)
	if err.Details["requires"] != "params.category" {
		t.Errorf("link without category requires %v", err.Details["requires"])
	}
}

// TestSignature_MultinomialOutcomes: the bound fit and its loaded model
// file name the categories; predict's `fits` entry cannot (the data
// decide them).
func TestSignature_MultinomialOutcomes(t *testing.T) {
	t.Parallel()
	cat, set := multinomialCatalog(t)
	want := []string{"mid", "lo", "hi"}
	if s, _ := set.Signature("m"); !s.OutcomesKnown || !slices.Equal(s.Outcomes, want) {
		t.Errorf("bound outcomes %v (known %v)", s.Outcomes, s.OutcomesKnown)
	}
	if s, _ := cat.Signature("m"); s.OutcomesKnown {
		t.Errorf("predict knows the categories statically: %v", s.Outcomes)
	}
	spec, res := multinomialFit()
	f, err := NewFile("m", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := roundTrip(t, f)
	loaded := CatalogWithModels(nil, []types.ModelRef{{Name: "m", Path: "m" + FileSuffix}}, []*File{got})
	s, _ := loaded.Signature("m")
	if !s.OutcomesKnown || !slices.Equal(s.Outcomes, want) {
		t.Errorf("loaded outcomes %v (known %v)", s.Outcomes, s.OutcomesKnown)
	}
	b, err := got.Bind("m")
	if err != nil {
		t.Fatal(err)
	}
	if b.Result.Reference != "lo" || !slices.Equal(b.Result.Categories, want) {
		t.Errorf("reloaded result reference %q categories %v", b.Result.Reference, b.Result.Categories)
	}
	if !slices.Equal(s.Coefficients, b.Signature().Coefficients) || !s.CoefficientsKnown {
		t.Errorf("loaded coefficients %v, bound %v", s.Coefficients, b.Signature().Coefficients)
	}
}

// TestDecodeFile_CategoriesRefusals: the categories section's rule.
func TestDecodeFile_CategoriesRefusals(t *testing.T) {
	t.Parallel()
	spec, res := multinomialFit()
	f, err := NewFile("m", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	good, _ := f.Encode()
	if _, err := DecodeFile("ok", good); err != nil {
		t.Fatalf("the unedited file must decode: %v", err)
	}
	edit := func(fn func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		fn(m)
		b, _ := json.Marshal(m)
		return b
	}
	coefs := func(m map[string]any) map[string]any { return m["coefficients"].(map[string]any) }
	for name, doc := range map[string][]byte{
		"no categories":                edit(func(m map[string]any) { delete(m, "categories"); delete(m, "features") }),
		"categories unlisted":          edit(func(m map[string]any) { delete(m, "features") }),
		"one category":                 edit(func(m map[string]any) { m["categories"] = []string{"lo"} }),
		"duplicate category":           edit(func(m map[string]any) { m["categories"] = []string{"mid", "lo", "lo"} }),
		"empty category":               edit(func(m map[string]any) { m["categories"] = []string{"mid", "lo", ""} }),
		"reference outside categories": edit(func(m map[string]any) { m["reference"] = "top" }),
		"no reference":                 edit(func(m map[string]any) { delete(m, "reference") }),
		"reference without categories": edit(func(m map[string]any) {
			m["type"] = "REG_OLS"
			delete(m, "categories")
			delete(m, "features")
		}),
		"categories on OLS": edit(func(m map[string]any) {
			m["type"] = "REG_OLS"
			coefs(m)[InterceptKey] = 1
			coefs(m)["x1"] = 1
			coefs(m)["x2"] = 1
		}),
		"missing category intercept":    edit(func(m map[string]any) { delete(coefs(m), "hi:(intercept)") }),
		"missing category coefficient":  edit(func(m map[string]any) { delete(coefs(m), "mid:x2") }),
		"category coefficients missing": edit(func(m map[string]any) { m["categories"] = []string{"mid", "lo", "hi", "top"} }),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeFile("x"+FileSuffix, doc)
			ce := coded(t, err)
			if ce.Code != errors.PULSE_MODEL_FILE_INVALID || ce.Details["reason"] != ReasonFileSchema {
				t.Fatalf("%v, want PULSE_MODEL_FILE_INVALID schema", err)
			}
		})
	}
}

// TestCoef_Multinomial: coef() reads a multinomial fit's
// "<category>:<term>" coefficients; a bare predictor or the intercept
// names none. Predict, which cannot know the categories, admits any
// "<name>:<term>" over the fit's terms and the runtime judges it.
func TestCoef_Multinomial(t *testing.T) {
	t.Parallel()
	cat, set := multinomialCatalog(t)
	_, res := multinomialFit()
	for _, tc := range []struct {
		term            string
		static, runtime bool // admitted
	}{
		{"hi:x1", true, true},
		{"mid:(intercept)", true, true},
		{"lo:x1", true, false}, // the reference carries none
		{"top:x2", true, false},
		{"hi:x9", false, false},
		{"x1", false, false},
		{InterceptKey, false, false},
	} {
		if err := CheckCoef(cat, "m", tc.term, StatPoint); (err == nil) != tc.static || (err != nil && err.Code != errors.PULSE_MODEL_TERM_UNKNOWN) {
			t.Errorf("predict coef(m, %s): %v, admitted want %v", tc.term, err, tc.static)
		}
		v, err := Coef(set, "m", tc.term, StatPoint)
		if (err == nil) != tc.runtime {
			t.Errorf("runtime coef(m, %s): %v, admitted want %v", tc.term, err, tc.runtime)
		}
		if err == nil && v != res.Coefficients[tc.term] {
			t.Errorf("coef(m, %s) = %v, want %v", tc.term, v, res.Coefficients[tc.term])
		}
	}
	if v, err := Coef(set, "m", "hi:x2", StatSE); err != nil || v != res.StdErrors["hi:x2"] {
		t.Errorf("coef(m, hi:x2, se) = %v %v", v, err)
	}
	if v, err := Coef(set, "m", "mid:x1", StatUpper); err != nil || v != res.CIUpper["mid:x1"] {
		t.Errorf("coef(m, mid:x1, upper) = %v %v", v, err)
	}
	err := CheckCoef(set, "m", "x1", StatPoint)
	if got, _ := err.Details["available"].([]string); len(got) != 6 || slices.Contains(got, InterceptKey) {
		t.Errorf("available %v, want the six category coefficients", err.Details["available"])
	}
}

// TestCheckFitReference_Multinomial: a params.fit fitted / residual /
// design slot scores the fit's default column, which on a multinomial
// fit reads a category the slot cannot name: type_unsupported, as before
// the fit was scorable.
func TestCheckFitReference_Multinomial(t *testing.T) {
	t.Parallel()
	cat, set := multinomialCatalog(t)
	for _, typ := range []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE} {
		for _, c := range []Catalog{cat, set} {
			_, _, err := CheckFitReference(&types.Attribute{Type: typ, Params: json.RawMessage(`{"fit":"m"}`)}, c, nil)
			if err == nil || err.Details["reason"] != ReasonTypeUnsupported || err.Details["type"] != string(types.REG_MULTINOMIAL) {
				t.Errorf("%s over a multinomial fit: %v, want type_unsupported", typ, err)
			}
		}
	}
}

func TestOutcomeScorerFor_MultinomialClass(t *testing.T) {
	t.Parallel()
	v := fixedVector{0.1, 0.5, 0.15, 0.25}
	for typ, want := range map[types.RegressionType]float64{types.REG_MULTINOMIAL: 1, types.REG_ORDINAL: 2} {
		rs, err := OutcomeScorerFor(typ, v, OutputClass, -1)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := rs.Score(oneRow{"x": 1}); !ok || got != want {
			t.Errorf("%s class %v, want %v", typ, got, want)
		}
	}
	// A whole-vector default binds a params.fit slot; one naming an
	// outcome does not.
	if !ScoresWholeVectorByDefault(types.REG_ORDINAL) || ScoresWholeVectorByDefault(types.REG_MULTINOMIAL) || !ScoresWholeVectorByDefault(types.REG_OLS) {
		t.Error("ScoresWholeVectorByDefault: want ordinal and OLS true, multinomial false")
	}
}
