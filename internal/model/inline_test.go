package model

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/types"
)

// stubInline is a stand-in InlineNumberFunc (internal/scalarslot's
// InlineNumber imports this package): "params.<name>" reads a param, a
// numeric string is its value, "inf" is not finite, anything else does
// not compile.
func stubInline(params types.Params, src string, integer bool) (float64, *InlineNumberRefusal) {
	if name, ok := strings.CutPrefix(src, "params."); ok {
		v, ok := params[name].(float64)
		if !ok {
			return 0, &InlineNumberRefusal{Coded: errors.NewCodedErrorWithDetails(errors.PULSE_PARAMS_UNDECLARED, "unknown param "+name, map[string]any{"param": name})}
		}
		return v, nil
	}
	if src == "inf" {
		return 0, &InlineNumberRefusal{Reason: "not_finite", What: "yields +Inf, not a finite number", Details: map[string]any{"value": "+Inf"}}
	}
	if v, err := strconv.ParseFloat(src, 64); err == nil {
		if integer && math.Trunc(v) != v {
			return 0, &InlineNumberRefusal{Reason: "not_integer", What: "is not an integer", Details: map[string]any{"value": v}}
		}
		return v, nil
	}
	return 0, &InlineNumberRefusal{Reason: "invalid", What: "does not compile", Details: map[string]any{"compile_error": "bad"}}
}

const inlineOLS = `{"model_format_version": 1, "type": "REG_OLS", "target": "units",
	"predictors": ["price"], "terms": ["price"],
	"coefficients": {"(intercept)": "params.a", "price": -4},
	"std_errors": {"price": "0.5"}, "n_obs": "80"}`

const inlineHill = `{"model_format_version": 1, "type": "REG_MMM_HILL", "target": "sales",
	"predictors": ["tv", "price"], "terms": ["tv", "price"],
	"coefficients": {"(intercept)": 10, "tv": "params.beta", "price": -2, "k:tv": 50, "s:tv": 1.5},
	"features": ["sweep_axes"], "sweep_axes": {"decay": 0.6, "shape": "geometric"}}`

func inlineRef(name, body string) types.ModelRef {
	return types.ModelRef{Name: name, Inline: json.RawMessage(body)}
}

// TestInline_BindsCoefAndSweepAxis: an inline OLS and an inline Hill
// model load with their expression numbers resolved over the params,
// bind in the one namespace (coef() reads each term, sweep_axis() the
// declared axes), and the static catalog sees what the runtime binds —
// inline Source, the Hill coefficient names, the sweep axes.
func TestInline_BindsCoefAndSweepAxis(t *testing.T) {
	t.Parallel()

	rs := Refs{Models: []types.ModelRef{inlineRef("truth", inlineOLS), inlineRef("curve", inlineHill)},
		Params: types.Params{"a": 120.0, "beta": 3.25}}
	if cerr := CheckRefs(rs, nil); cerr != nil {
		t.Fatal(cerr)
	}
	loaded, cerr := LoadFiles(rs, nil, stubInline)
	if cerr != nil {
		t.Fatal(cerr)
	}
	set, err := (*Set)(nil).WithLoaded(rs, loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fit, term, stat string
		want            float64
	}{
		{"truth", InterceptKey, StatPoint, 120},
		{"truth", "price", StatPoint, -4},
		{"truth", "price", StatSE, 0.5},
		{"curve", "tv", StatPoint, 3.25},
		{"curve", "k:tv", StatPoint, 50},
		{"curve", "s:tv", StatPoint, 1.5},
	} {
		got, cerr := Coef(set, c.fit, c.term, c.stat)
		if cerr != nil || got != c.want {
			t.Errorf("coef(%s, %s, %s) = %v, %v; want %v", c.fit, c.term, c.stat, got, cerr, c.want)
		}
	}
	if v, cerr := SweepAxis(set, "curve", "decay"); cerr != nil || v != 0.6 {
		t.Errorf("sweep_axis(curve, decay) = %v, %v", v, cerr)
	}
	if v, cerr := SweepAxis(set, "curve", "shape"); cerr != nil || v != "geometric" {
		t.Errorf("sweep_axis(curve, shape) = %v, %v", v, cerr)
	}
	if loaded.Models[0].NObs != 80 {
		t.Errorf("n_obs %d, want 80", loaded.Models[0].NObs)
	}
	cat := CatalogWithLoaded(nil, rs, loaded)
	for _, name := range []string{"truth", "curve"} {
		sig, _ := cat.Signature(name)
		run, _ := set.Signature(name)
		if !sig.Source.Inline || !run.Source.Inline || sig.Source != run.Source {
			t.Errorf("%s: catalog source %+v, set source %+v, want inline", name, sig.Source, run.Source)
		}
	}
	if cerr := CheckCoef(cat, "curve", "k:tv", StatPoint); cerr != nil {
		t.Errorf("static coef(curve, k:tv): %v", cerr)
	}
	if cerr := CheckSweepAxis(cat, "curve", "decay"); cerr != nil {
		t.Errorf("static sweep_axis(curve, decay): %v", cerr)
	}
	if cerr := CheckSweepAxis(cat, "truth", "decay"); cerr == nil {
		t.Error("sweep_axis over an inline model without sweep_axes resolved")
	}
}

// TestInline_Refusals: every refusal of an inline entry, with its code
// and reason — the entry rule (both / neither), a number expression's
// own refusal located at its field, the model-file codec's, the type
// and the coefficient names.
func TestInline_Refusals(t *testing.T) {
	t.Parallel()

	edit := func(body string, from, to string) string {
		if !strings.Contains(body, from) {
			t.Fatalf("fixture lacks %q", from)
		}
		return strings.Replace(body, from, to, 1)
	}
	cases := []struct {
		name   string
		ref    types.ModelRef
		code   errors.Code
		reason string
		field  string
	}{
		{"path and inline", types.ModelRef{Name: "m", Path: "m" + FileSuffix, Inline: json.RawMessage(inlineOLS)}, errors.PULSE_MODEL_REF_INVALID, ReasonRefPathAndInline, ""},
		{"neither", types.ModelRef{Name: "m"}, errors.PULSE_MODEL_REF_INVALID, ReasonSavePathEmpty, ""},
		{"number does not compile", inlineRef("m", edit(inlineOLS, `"price": -4`, `"price": "+-"`)), errors.PULSE_MODEL_REF_INVALID, "inline_invalid", "coefficients.price"},
		{"number not finite", inlineRef("m", edit(inlineOLS, `"price": -4`, `"price": "inf"`)), errors.PULSE_MODEL_REF_INVALID, "inline_not_finite", "coefficients.price"},
		{"n_obs not an integer", inlineRef("m", edit(inlineOLS, `"n_obs": "80"`, `"n_obs": "80.5"`)), errors.PULSE_MODEL_REF_INVALID, "inline_not_integer", "n_obs"},
		{"unknown param keeps its code", inlineRef("m", edit(inlineOLS, `params.a`, `params.zz`)), errors.PULSE_PARAMS_UNDECLARED, "", "coefficients.(intercept)"},
		{"no version", inlineRef("m", edit(inlineOLS, `"model_format_version": 1,`, ``)), errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing, ""},
		{"unknown key", inlineRef("m", edit(inlineOLS, `"n_obs"`, `"bogus": 1, "n_obs"`)), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema, ""},
		{"type unsupported", inlineRef("m", edit(inlineOLS, `REG_OLS`, `REG_ORDINAL`)), errors.PULSE_MODEL_REF_INVALID, ReasonInlineTypeUnsupported, ""},
		{"unknown term", inlineRef("m", edit(inlineOLS, `"price": -4`, `"price": -4, "region": 1`)), errors.PULSE_MODEL_REF_INVALID, ReasonInlineTermUnknown, ""},
		{"unknown std_errors term", inlineRef("m", edit(inlineOLS, `{"price": "0.5"}`, `{"cost": "0.5"}`)), errors.PULSE_MODEL_REF_INVALID, ReasonInlineTermUnknown, ""},
		{"hill without its shape", inlineRef("m", edit(inlineHill, `, "s:tv": 1.5`, ``)), errors.PULSE_MODEL_REF_INVALID, ReasonInlineTermMissing, ""},
		{"hill with no channel", inlineRef("m", edit(edit(inlineHill, `, "k:tv": 50`, ``), `, "s:tv": 1.5`, ``)), errors.PULSE_MODEL_REF_INVALID, ReasonInlineTermMissing, ""},
		{"hill curve term of no predictor", inlineRef("m", edit(inlineHill, `"s:tv": 1.5`, `"s:tv": 1.5, "k:radio": 3`)), errors.PULSE_MODEL_REF_INVALID, ReasonInlineTermUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := Refs{Models: []types.ModelRef{tc.ref}, Params: types.Params{"a": 120.0, "beta": 3.25}}
			cerr := CheckRefs(rs, nil)
			if cerr == nil {
				_, cerr = LoadFiles(rs, nil, stubInline)
			}
			if cerr == nil || cerr.Code != tc.code {
				t.Fatalf("got %v, want %s", cerr, tc.code)
			}
			if tc.reason != "" && cerr.Details["reason"] != tc.reason {
				t.Errorf("reason %v, want %s (%s)", cerr.Details["reason"], tc.reason, cerr.Message)
			}
			if tc.field != "" && cerr.Details["field"] != tc.field {
				t.Errorf("field %v, want %s", cerr.Details["field"], tc.field)
			}
			if cerr.Details["model"] != 0 || cerr.Details["name"] != "m" {
				t.Errorf("details %v locate no entry", cerr.Details)
			}
			if p, ok := cerr.Details["path"]; ok && p != "" && tc.ref.Path == "" {
				t.Errorf("an inline refusal carries a path: %v", cerr.Details)
			}
		})
	}
}

// TestInline_LiteralBodyAsWritten: a body with no expression is decoded
// from its own bytes — no evaluator needed — and binds exactly like the
// same document read from a file.
func TestInline_LiteralBodyAsWritten(t *testing.T) {
	t.Parallel()

	body := encodedFullFit(t)
	rs := Refs{Models: []types.ModelRef{{Name: "m", Inline: body}}}
	loaded, cerr := LoadFiles(rs, nil, nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	file, cerr := LoadFiles(Refs{Models: refs("m", "m"+FileSuffix)}, memRead(map[string][]byte{"m" + FileSuffix: body}), nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	a, _ := loaded.Models[0].Encode()
	b, _ := file.Models[0].Encode()
	if string(a) != string(b) {
		t.Errorf("inline and file decode differ:\n%s\n%s", a, b)
	}
}

// TestInline_ScoringRefused: ATTR_REG_PREDICT and a params.fit
// attribute naming an inline model are refused inline_not_scorable; the
// same model loaded from a file scores.
func TestInline_ScoringRefused(t *testing.T) {
	t.Parallel()

	rs := Refs{Models: []types.ModelRef{inlineRef("truth", inlineOLS)}, Params: types.Params{"a": 120.0}}
	loaded, cerr := LoadFiles(rs, nil, stubInline)
	if cerr != nil {
		t.Fatal(cerr)
	}
	cat := CatalogWithLoaded(nil, rs, loaded)
	set, err := (*Set)(nil).WithLoaded(rs, loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Catalog{cat, set} {
		_, _, cerr := CheckScore(&types.Attribute{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(`{"fit":"truth"}`)}, c)
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_SCORE_INVALID || cerr.Details["reason"] != ReasonInlineNotScorable {
			t.Errorf("predict score: %v", cerr)
		}
		_, _, cerr = CheckFitReference(&types.Attribute{Type: types.ATTR_REG_FITTED, Params: json.RawMessage(`{"fit":"truth"}`)}, c, nil)
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_SCORE_INVALID || cerr.Details["reason"] != ReasonInlineNotScorable || cerr.Details["attribute"] != string(types.ATTR_REG_FITTED) {
			t.Errorf("fitted: %v", cerr)
		}
	}
	data, _ := loaded.Models[0].Encode()
	frs := Refs{Models: refs("truth", "t"+FileSuffix)}
	fl, cerr := LoadFiles(frs, memRead(map[string][]byte{"t" + FileSuffix: data}), nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	if _, _, cerr := CheckScore(&types.Attribute{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(`{"fit":"truth"}`)}, CatalogWithLoaded(nil, frs, fl)); cerr != nil {
		t.Errorf("the same model from a file: %v", cerr)
	}
}

// TestInline_CollisionNamesInlineSource: an inline entry's name
// colliding with a fit reports the inline source.
func TestInline_CollisionNamesInlineSource(t *testing.T) {
	t.Parallel()

	fits := CatalogFromFits([]types.FitSpec{{Name: "truth", Request: olsReq(ols())}})
	cerr := CheckRefs(Refs{Models: []types.ModelRef{inlineRef("truth", inlineOLS)}}, fits)
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_NAME_COLLISION {
		t.Fatalf("got %v", cerr)
	}
	srcs, _ := cerr.Details["sources"].([]any)
	if len(srcs) != 2 || srcs[1].(map[string]any)["inline"] != true {
		t.Errorf("sources %v", cerr.Details["sources"])
	}
}

// TestInline_NumberPathsMatchExprSlots: internal/exprslots names every
// inline number position InlineNumberPaths lists (and no other), so a
// template never splices into one and the reads walker reports it.
func TestInline_NumberPathsMatchExprSlots(t *testing.T) {
	t.Parallel()

	var slots [][]string
	for _, s := range exprslots.All() {
		if s.Name == exprslots.SlotInlineModelNumber {
			slots = append(slots, s.Tail[2:])
		}
	}
	want := InlineNumberPaths()
	key := func(p []string) string { return strings.Join(p, ".") }
	got := make([]string, len(slots))
	for i, s := range slots {
		got[i] = key(s)
	}
	exp := make([]string, len(want))
	for i, s := range want {
		exp[i] = key(s)
	}
	slices.Sort(got)
	slices.Sort(exp)
	if !slices.Equal(got, exp) {
		t.Errorf("exprslots inline tails %v, InlineNumberPaths %v", got, exp)
	}
}

// TestInline_BayesKernelTerms: an inline REG_MMM_BAYES model's terms
// follow the adstock kernel its coefficients name — decay: alone
// (geometric), decay: and peak: (delayed), shape: and scale: (Weibull)
// — so each loads, and a delayed or Weibull term beside the geometric
// set (or a peak: without its decay:) is refused, never accepted as a
// stray coefficient.
func TestInline_BayesKernelTerms(t *testing.T) {
	t.Parallel()

	body := func(adstock string) string {
		return `{"model_format_version": 1, "type": "REG_MMM_BAYES", "target": "sales",
	"predictors": ["tv"], "terms": ["tv"],
	"coefficients": {"(intercept)": 10, "tv": 3, "k:tv": 50, "s:tv": 1.5, "sigma": 2` + adstock + `}}`
	}
	for _, tc := range []struct {
		name, adstock string
		ok            bool
	}{
		{"geometric", `, "decay:tv": 0.5`, true},
		{"delayed", `, "decay:tv": 0.5, "peak:tv": 1.5`, true},
		{"weibull", `, "shape:tv": 1.2, "scale:tv": 3`, true},
		{"geometric with a peak", `, "decay:tv": 0.5, "shape:tv": 1.2`, false},
		{"peak without decay", `, "peak:tv": 1.5`, false},
		{"weibull without scale", `, "shape:tv": 1.2`, false},
	} {
		rs := Refs{Models: []types.ModelRef{inlineRef("m", body(tc.adstock))}}
		cerr := CheckRefs(rs, nil)
		if cerr == nil {
			_, cerr = LoadFiles(rs, nil, stubInline)
		}
		if (cerr == nil) != tc.ok {
			t.Errorf("%s: %v, want ok %v", tc.name, cerr, tc.ok)
		}
	}
}
