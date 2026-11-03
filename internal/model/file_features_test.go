package model

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// wantFeatures asserts a decoded file (and its bytes) is
// model_format_version 1 listing exactly want, in registry order — and,
// for none, that the document carries no features key at all.
func wantFeatures(t *testing.T, got *File, data []byte, want ...string) {
	t.Helper()
	if got.ModelFormatVersion != 1 || !strings.Contains(string(data), `"model_format_version": 1,`) {
		t.Fatalf("version %d, want 1:\n%s", got.ModelFormatVersion, data)
	}
	if len(want) == 0 {
		if len(got.Features) != 0 || bytes.Contains(data, []byte(`"features"`)) {
			t.Fatalf("features %v, want none and no features key:\n%s", got.Features, data)
		}
		return
	}
	if !reflect.DeepEqual(got.Features, want) {
		t.Fatalf("features %v, want %v:\n%s", got.Features, want, data)
	}
}

// TestModelFile_FeatureRegistry: the registry names the four sections
// in the order the writer lists them, and each governs a real document
// key of File — the one entry a new section adds.
func TestModelFile_FeatureRegistry(t *testing.T) {
	t.Parallel()

	want := []string{FeatureRobustCovariance, FeatureOutputs, FeatureDispersion, FeatureTheta}
	if got := FileFeatures(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FileFeatures() = %v, want %v", got, want)
	}
	keys := map[string]bool{}
	rt := reflect.TypeOf(File{})
	for i := 0; i < rt.NumField(); i++ {
		keys[strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for _, ft := range fileFeatures {
		if !keys[ft.key] || ft.present == nil {
			t.Errorf("feature %s governs key %q, which File does not carry (or has no presence test)", ft.name, ft.key)
		}
	}
}

// TestModelFile_FeatureCombinationsRoundTrip: every combination of
// sections a fit can carry writes model_format_version 1 listing exactly
// those features, decodes back with every figure bit-identical, binds,
// and re-encodes to the same bytes.
func TestModelFile_FeatureCombinationsRoundTrip(t *testing.T) {
	t.Parallel()

	nb := func(spec *types.RegressionSpec, res *types.RegressionResult) {
		spec.Type, spec.Family, spec.Link = types.REG_GLM, "negative_binomial", "log"
		res.Type, res.Family, res.Link = types.REG_GLM, "negative_binomial", "log"
		res.Theta = 2.6694094873181985
	}
	hc3 := &types.RegressionCovarianceResult{Type: types.CovarianceHC3}
	cases := []struct {
		name string
		fit  func() (*types.RegressionSpec, *types.RegressionResult)
		want []string
	}{
		{"none", fullFit, nil},
		{"robust_covariance", func() (*types.RegressionSpec, *types.RegressionResult) {
			s, r := fullFit()
			r.Covariance = hc3
			return s, r
		}, []string{FeatureRobustCovariance}},
		{"dispersion", func() (*types.RegressionSpec, *types.RegressionResult) {
			s, r := fullFit()
			r.Dispersion = 1.2575235440954267
			return s, r
		}, []string{FeatureDispersion}},
		{"theta", func() (*types.RegressionSpec, *types.RegressionResult) {
			s, r := fullFit()
			nb(s, r)
			return s, r
		}, []string{FeatureTheta}},
		{"robust_covariance+theta", func() (*types.RegressionSpec, *types.RegressionResult) {
			s, r := fullFit()
			nb(s, r)
			r.Covariance = hc3
			return s, r
		}, []string{FeatureRobustCovariance, FeatureTheta}},
		{"dispersion+theta", func() (*types.RegressionSpec, *types.RegressionResult) {
			s, r := fullFit()
			nb(s, r)
			s.Theta, s.Dispersion = 1.7, types.NegBinDispersionEstimated
			r.Theta, r.Dispersion = 1.7, 1.2832190573164217
			return s, r
		}, []string{FeatureDispersion, FeatureTheta}},
		{"outputs", extResult, []string{FeatureOutputs}},
		{"robust_covariance+outputs", func() (*types.RegressionSpec, *types.RegressionResult) {
			s, r := extResult()
			r.Covariance = hc3
			return s, r
		}, []string{FeatureRobustCovariance, FeatureOutputs}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec, res := tc.fit()
			f, err := NewFile("m", spec, res, Provenance{Cohort: "c.pulse"})
			if err != nil {
				t.Fatal(err)
			}
			got, data := roundTrip(t, f)
			wantFeatures(t, got, data, tc.want...)
			r := got.Result()
			sameFloatMap(t, "coefficients", r.Coefficients, res.Coefficients)
			sameFloatMap(t, "std_errors", r.StdErrors, res.StdErrors)
			sameFloatMap(t, "ci_lower", r.CILower, res.CILower)
			sameFloatMap(t, "ci_upper", r.CIUpper, res.CIUpper)
			sameFloats(t, "scalars", []float64{r.DF, r.ConfLevel, r.SumWeights, r.NEff, r.Dispersion, r.Theta},
				[]float64{res.DF, res.ConfLevel, res.SumWeights, res.NEff, res.Dispersion, res.Theta})
			if res.Vcov != nil {
				for i := range res.Vcov.Values {
					sameFloats(t, "vcov", r.Vcov.Values[i], res.Vcov.Values[i])
				}
			}
			if !reflect.DeepEqual(r.Covariance, res.Covariance) {
				t.Errorf("covariance %+v, want %+v", r.Covariance, res.Covariance)
			}
			loaded, err := got.Bind("m")
			if err != nil {
				t.Fatal(err)
			}
			if BuiltinRegression(res.Type) {
				want, err := Bind("m", spec, res)
				if err != nil {
					t.Fatal(err)
				}
				sameBound(t, loaded, want)
			}
			re, _ := got.Encode()
			if !bytes.Equal(data, re) {
				t.Fatalf("re-encode differs:\n%s\n%s", data, re)
			}
		})
	}
}

// TestDecodeFile_LegacyVersions: a legacy version-1 document (no
// features key, as every earlier build wrote a plain fit) decodes, and
// so does an empty or null features list; versions 2 to 6 are refused
// PULSE_MODEL_VERSION_UNSUPPORTED with a re-save message — no alias.
func TestDecodeFile_LegacyVersions(t *testing.T) {
	t.Parallel()

	legacy := `{"model_format_version": 1, "name": "m", "type": "REG_OLS", "target": "y", "predictors": ["x"], "terms": ["x"],
		"coefficients": {"(intercept)": 1.5, "x": -0.25}, "n_obs": 40, "provenance": {}}`
	for _, doc := range []string{legacy, strings.Replace(legacy, `"n_obs"`, `"features": [], "n_obs"`, 1), strings.Replace(legacy, `"n_obs"`, `"features": null, "n_obs"`, 1)} {
		f, err := DecodeFile("old"+FileSuffix, []byte(doc))
		if err != nil {
			t.Fatalf("legacy v1 must decode: %v\n%s", err, doc)
		}
		b, err := f.Bind("m")
		if err != nil || b.Intercept != 1.5 || b.Coefficients[0] != -0.25 {
			t.Fatalf("bind %+v, %v", b, err)
		}
	}
	// The plain-fit writer stays byte-identical to the legacy shape.
	spec, res := fullFit()
	f, _ := NewFile("m", spec, res, Provenance{})
	good, _ := f.Encode()
	for v := 2; v <= 6; v++ {
		doc := bytes.Replace(good, []byte(`"model_format_version": 1,`), []byte(`"model_format_version": `+string(rune('0'+v))+`,`), 1)
		_, err := DecodeFile("old"+FileSuffix, doc)
		ce := coded(t, err)
		if ce.Code != errors.PULSE_MODEL_VERSION_UNSUPPORTED || ce.Details["version"] != v || !reflect.DeepEqual(ce.Details["supported"], []int{1}) {
			t.Fatalf("v%d: %v (%v)", v, ce.Details, err)
		}
		if !strings.Contains(ce.Message, "re-save") {
			t.Fatalf("v%d message lacks the re-save fixup: %s", v, ce.Message)
		}
		if _, ok := ce.Details["unknown_features"]; ok {
			t.Fatalf("v%d carries unknown_features: %v", v, ce.Details)
		}
	}
}

// TestDecodeFile_UnknownFeature: a version-1 file listing a feature this
// build does not know is PULSE_MODEL_VERSION_UNSUPPORTED naming it under
// details.unknown_features — judged before the strict decode, so the
// unknown section's own key is not mistaken for a schema fault.
func TestDecodeFile_UnknownFeature(t *testing.T) {
	t.Parallel()

	spec, res := fullFit()
	res.Dispersion = 1.25
	f, _ := NewFile("m", spec, res, Provenance{})
	good, _ := f.Encode()
	var m map[string]any
	if err := json.Unmarshal(good, &m); err != nil {
		t.Fatal(err)
	}
	m["features"] = []string{"future_section", FeatureDispersion, "other_section", "future_section"}
	m["future_section"] = map[string]any{"k": 1}
	doc, _ := json.Marshal(m)
	_, err := DecodeFile("new"+FileSuffix, doc)
	ce := coded(t, err)
	if ce.Code != errors.PULSE_MODEL_VERSION_UNSUPPORTED {
		t.Fatalf("code %s (%v)", ce.Code, err)
	}
	if !reflect.DeepEqual(ce.Details["unknown_features"], []string{"future_section", "other_section"}) ||
		ce.Details["version"] != 1 || ce.Details["path"] != "new"+FileSuffix ||
		!reflect.DeepEqual(ce.Details["supported_features"], FileFeatures()) {
		t.Fatalf("details %v", ce.Details)
	}
}
