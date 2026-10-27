package model

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

const extType types.RegressionType = "REG_X_CUSTOM"

// extResult is an extension regression's finished fit: coefficients
// named by the model (no intercept), outputs of every JSON shape —
// a non-finite float among them.
func extResult() (*types.RegressionSpec, *types.RegressionResult) {
	spec := &types.RegressionSpec{Type: extType, Target: "y", Predictors: []string{"x1", "x2"}}
	res := &types.RegressionResult{
		Name: "m", Type: extType, NObs: 12,
		Coefficients: map[string]float64{"scale": 2.5, "half_saturation": 0.1 + 0.2, "x1": math.Copysign(0, -1)},
		StdErrors:    map[string]float64{"scale": math.NaN(), "half_saturation": 0.01, "x1": 0.5},
		Outputs: map[string]any{
			"k":      0.7,
			"decays": []float64{0.7, 0.2, math.Inf(1)},
			"labels": map[string]any{"tv": "a", "n": 3},
			"undef":  math.NaN(),
			"ok":     true,
		},
	}
	return spec, res
}

// TestBind_ExtensionRegression: an extension regression's fit binds
// without an intercept or per-predictor coefficients (NaN where absent),
// keeping its whole result for its scorer; a built-in's still refuses.
func TestBind_ExtensionRegression(t *testing.T) {
	t.Parallel()

	spec, res := extResult()
	b, err := Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(b.Intercept) || !math.IsNaN(b.Coefficients[1]) || b.Coefficients[0] != 0 || b.Result != res {
		t.Fatalf("bound %+v", b)
	}
	if !reflect.DeepEqual(b.Predictors, []string{"x1", "x2"}) || b.Type != extType {
		t.Fatalf("bound predictors / type %v %s", b.Predictors, b.Type)
	}
	builtin := &types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{"x1": 1, "x2": 2}}
	if ce := coded(t, ignoreBound(Bind("q", spec, builtin))); ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("code = %s", ce.Code)
	}
}

// TestModelFile_ExtensionRoundTrip: an extension regression's fit saves
// as model_format_version 3 carrying its outputs, decodes back to the
// same coefficients bit for bit and to outputs equal to
// NormalizeOutputs of the fit's own — the one form its scorer sees —
// and re-encodes to the same bytes. A built-in fit stays version 1 with
// no outputs key.
func TestModelFile_ExtensionRoundTrip(t *testing.T) {
	t.Parallel()

	spec, res := extResult()
	f, err := NewFile("m", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if f.ModelFormatVersion != 3 || len(f.Outputs) != 5 {
		t.Fatalf("version %d outputs %v", f.ModelFormatVersion, f.Outputs)
	}
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeFile("m"+FileSuffix, data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := back.Encode()
	if !bytes.Equal(data, again) {
		t.Fatalf("re-encode differs:\n%s\n%s", data, again)
	}
	got := back.Result()
	for k, v := range res.Coefficients {
		if math.Float64bits(got.Coefficients[k]) != math.Float64bits(v) {
			t.Fatalf("coefficient %s = %v, want %v", k, got.Coefficients[k], v)
		}
	}
	want, err := NormalizeOutputs(res.Outputs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Outputs, want) {
		t.Fatalf("outputs %#v, want %#v", got.Outputs, want)
	}
	if want["undef"] != nil || !reflect.DeepEqual(want["decays"], []any{0.7, 0.2, nil}) || want["labels"].(map[string]any)["n"] != float64(3) {
		t.Fatalf("normalized outputs %#v", want)
	}
	b, err := back.Bind("loaded")
	if err != nil || b.Type != extType || !reflect.DeepEqual(b.Predictors, spec.Predictors) {
		t.Fatalf("bind: %v %+v", err, b)
	}

	// A built-in fit is unchanged: version 1, no outputs key.
	ospec, ores := fullFit()
	of, err := NewFile("o", ospec, ores, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := of.Encode()
	if of.ModelFormatVersion != 1 || bytes.Contains(enc, []byte(`"outputs"`)) {
		t.Fatalf("built-in file version %d / outputs key present", of.ModelFormatVersion)
	}
}

// TestCheckScoreFor: an instance's scorable view admits an extension
// fit with a scorer, which scores the whole model only; without one the
// fit is refused no_scorer. Built-ins judge as CheckScore does.
func TestCheckScoreFor(t *testing.T) {
	t.Parallel()

	cat := scoreCatalog()
	withScorer := func(t types.RegressionType) bool { return ScorableType(t) || t == "REG_X_CUSTOM" }
	cases := []struct {
		name     string
		attr     *types.Attribute
		scorable ScorableFunc
		reason   string
	}{
		{"extension with a scorer", scoreAttr(`{"fit":"ext"}`), withScorer, ""},
		{"extension point bound", scoreAttr(`{"fit":"ext","bound":"point"}`), withScorer, ""},
		{"extension without a scorer", scoreAttr(`{"fit":"ext"}`), ScorableType, ReasonNoScorer},
		{"nil view is the built-ins'", scoreAttr(`{"fit":"ext"}`), nil, ReasonNoScorer},
		{"extension terms", scoreAttr(`{"fit":"ext","terms":["x1"]}`), withScorer, ReasonTermsUnsupported},
		{"extension bound", scoreAttr(`{"fit":"ext","terms":null,"bound":"lower"}`), withScorer, ReasonBoundUnsupported},
		{"extension scale", scoreAttr(`{"fit":"ext","scale":"link"}`), withScorer, ReasonScaleNotGLM},
		{"built-in terms", scoreAttr(`{"fit":"ols","terms":["x1"]}`), withScorer, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := CheckScoreFor(tc.attr, cat, tc.scorable)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != errors.PULSE_MODEL_SCORE_INVALID || err.Details["reason"] != tc.reason {
				t.Fatalf("err = %v, want reason %s", err, tc.reason)
			}
			if tc.reason == ReasonNoScorer && err.Details["type"] != "REG_X_CUSTOM" {
				t.Fatalf("details = %v", err.Details)
			}
		})
	}
}

// TestCheckModelTypes: a loaded extension regression's file is refused
// PULSE_MODEL_REGRESSION_UNREGISTERED unless the instance registers its
// type; a built-in's always passes.
func TestCheckModelTypes(t *testing.T) {
	t.Parallel()

	spec, res := extResult()
	ext, err := NewFile("m", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	ospec, ores := fullFit()
	builtin, err := NewFile("o", ospec, ores, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	rs := refs("o", "o"+FileSuffix, "m", "dir/m"+FileSuffix)
	files := []*File{builtin, ext}
	registered := func(t types.RegressionType) bool { return t == extType }
	if cerr := CheckModelTypes(rs, files, registered); cerr != nil {
		t.Fatalf("registered: %v", cerr)
	}
	for _, reg := range []RegisteredFunc{nil, func(types.RegressionType) bool { return false }} {
		cerr := CheckModelTypes(rs, files, reg)
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_REGRESSION_UNREGISTERED {
			t.Fatalf("unregistered: %v", cerr)
		}
		want := map[string]any{"model": 1, "name": "m", "path": "dir/m" + FileSuffix, "type": string(extType)}
		if !reflect.DeepEqual(cerr.Details, want) {
			t.Fatalf("details %v, want %v", cerr.Details, want)
		}
	}
	if cerr := CheckLoadedTypes(Refs{Models: rs}, &Loaded{Models: files}, nil); cerr == nil {
		t.Fatal("CheckLoadedTypes must run CheckModelTypes")
	}
	if cerr := CheckLoadedTypes(Refs{}, nil, nil); cerr != nil {
		t.Fatal(cerr)
	}
}
