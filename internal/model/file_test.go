package model

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// edgeFloats are the float64 values a model file must carry bit for bit.
var edgeFloats = []float64{
	0, math.Copysign(0, -1), 1, -1, 0.1 + 0.2, -3.5,
	math.MaxFloat64, -math.MaxFloat64,
	math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, // subnormal
	2.2250738585072009e-308, // largest subnormal
	2.2250738585072014e-308, // smallest normal
	1e-300, 123456789.12345679, 1e21, 1e-7,
	math.Inf(1), math.Inf(-1),
}

func sameBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

// TestFileFloat_RoundTripBitExact: every finite float64 (negative zero
// and subnormals included) and both infinities read back with the same
// bits; NaN is null and reads back NaN (never 0).
func TestFileFloat_RoundTripBitExact(t *testing.T) {
	t.Parallel()

	for _, v := range append(edgeFloats, math.NaN()) {
		b, err := json.Marshal(Float(v))
		if err != nil {
			t.Fatalf("marshal %v: %v", v, err)
		}
		var got Float
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if !sameBits(float64(got), v) {
			t.Errorf("%v (%s) read back as %v", v, b, float64(got))
		}
	}
	for v, want := range map[float64]string{math.NaN(): "null", math.Inf(1): `"+Inf"`, math.Inf(-1): `"-Inf"`, math.Copysign(0, -1): "-0"} {
		if b, _ := json.Marshal(Float(v)); string(b) != want {
			t.Errorf("%v encodes as %s, want %s", v, b, want)
		}
	}
	var f Float
	if err := json.Unmarshal([]byte(`"Infinity"`), &f); err == nil {
		t.Fatal(`"Infinity" must be refused`)
	}
}

// fullFit is an OLS fit whose every figure is an edge float: NaN
// (undefined) bounds and df, a NaN vcov entry, negative zero, subnormals.
func fullFit() (*types.RegressionSpec, *types.RegressionResult) {
	spec := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"a", "b"}, Vcov: true, Save: "m" + FileSuffix}
	res := &types.RegressionResult{
		Name: "m", Type: types.REG_OLS,
		Coefficients: map[string]float64{InterceptKey: 0.1 + 0.2, "a": math.Copysign(0, -1), "b": math.SmallestNonzeroFloat64},
		StdErrors:    map[string]float64{InterceptKey: 2.2250738585072009e-308, "a": 1e-300, "b": math.Inf(1)},
		CILower:      map[string]float64{InterceptKey: math.NaN(), "a": -math.MaxFloat64, "b": math.Inf(-1)},
		CIUpper:      map[string]float64{InterceptKey: math.NaN(), "a": 123456789.12345679, "b": math.Inf(1)},
		PValues:      map[string]float64{"a": 0.5},
		DF:           math.NaN(),
		ConfLevel:    0.9,
		NObs:         104,
		SumWeights:   97.25,
		NEff:         math.Nextafter(80, 81),
		R2:           0.75,
		Vcov: &types.MatrixValues{
			Kind: "covariance", Encoding: types.MatrixEncodingFull,
			RowKeys: []string{InterceptKey, "a", "b"}, ColumnKeys: []string{InterceptKey, "a", "b"},
			Values: [][]float64{{1, math.Copysign(0, -1), math.NaN()}, {math.Copysign(0, -1), 1e-300, 2}, {math.NaN(), 2, math.Inf(1)}},
		},
	}
	return spec, res
}

func roundTrip(t *testing.T, f *File) (*File, []byte) {
	t.Helper()
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFile("m"+FileSuffix, data)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	return got, data
}

func sameFloatMap(t *testing.T, what string, got, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d keys, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok || !sameBits(g, w) {
			t.Errorf("%s[%s] = %v (present %v), want %v", what, k, g, ok, w)
		}
	}
}

func sameFloats(t *testing.T, what string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len %d, want %d", what, len(got), len(want))
	}
	for i := range want {
		if !sameBits(got[i], want[i]) {
			t.Errorf("%s[%d] = %v, want %v", what, i, got[i], want[i])
		}
	}
}

// sameBound compares every scoring figure of two Bounds bit for bit.
func sameBound(t *testing.T, got, want *Bound) {
	t.Helper()
	if got.Name != want.Name || got.Type != want.Type || got.Target != want.Target || got.Family != want.Family || got.Link != want.Link {
		t.Errorf("identity: got %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(got.Predictors, want.Predictors) || !reflect.DeepEqual(got.Terms, want.Terms) {
		t.Errorf("predictors/terms: got %v %v, want %v %v", got.Predictors, got.Terms, want.Predictors, want.Terms)
	}
	sameFloats(t, "intercept", []float64{got.Intercept, got.InterceptStdErr, got.InterceptCILower, got.InterceptCIUpper, got.ConfLevel},
		[]float64{want.Intercept, want.InterceptStdErr, want.InterceptCILower, want.InterceptCIUpper, want.ConfLevel})
	sameFloats(t, "coefficients", got.Coefficients, want.Coefficients)
	sameFloats(t, "std_errors", got.StdErrors, want.StdErrors)
	sameFloats(t, "ci_lower", got.CILower, want.CILower)
	sameFloats(t, "ci_upper", got.CIUpper, want.CIUpper)
	if (got.Vcov == nil) != (want.Vcov == nil) {
		t.Fatalf("vcov presence: got %v, want %v", got.Vcov != nil, want.Vcov != nil)
	}
	if want.Vcov != nil {
		if !reflect.DeepEqual(got.Vcov.RowKeys, want.Vcov.RowKeys) || !reflect.DeepEqual(got.Vcov.ColumnKeys, want.Vcov.ColumnKeys) ||
			got.Vcov.Kind != want.Vcov.Kind || got.Vcov.Encoding != want.Vcov.Encoding {
			t.Errorf("vcov axes: got %+v, want %+v", got.Vcov, want.Vcov)
		}
		for i := range want.Vcov.Values {
			sameFloats(t, "vcov row", got.Vcov.Values[i], want.Vcov.Values[i])
		}
	}
}

// TestModelFile_RoundTripBitExact: a saved fit parses back with every
// coefficient, standard error, interval bound, df, sample size and vcov
// entry bit-identical, and binds to the same Bound the in-request fit
// binds to.
func TestModelFile_RoundTripBitExact(t *testing.T) {
	t.Parallel()

	spec, res := fullFit()
	prov := Provenance{Cohort: "c.pulse", Fingerprint: "sha256:00", RequestDigest: "abc"}
	f, err := NewFile("m", spec, res, prov)
	if err != nil {
		t.Fatal(err)
	}
	got, data := roundTrip(t, f)
	r := got.Result()
	sameFloatMap(t, "coefficients", r.Coefficients, res.Coefficients)
	sameFloatMap(t, "std_errors", r.StdErrors, res.StdErrors)
	sameFloatMap(t, "ci_lower", r.CILower, res.CILower)
	sameFloatMap(t, "ci_upper", r.CIUpper, res.CIUpper)
	sameFloats(t, "scalars", []float64{r.DF, r.ConfLevel, r.SumWeights, r.NEff}, []float64{res.DF, res.ConfLevel, res.SumWeights, res.NEff})
	if r.NObs != res.NObs || got.Provenance != prov || got.ModelFormatVersion != 1 || r.Covariance != nil {
		t.Errorf("n_obs / provenance / version: %d %+v %d", r.NObs, got.Provenance, got.ModelFormatVersion)
	}
	for i := range res.Vcov.Values {
		sameFloats(t, "vcov", r.Vcov.Values[i], res.Vcov.Values[i])
	}
	want, err := Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := got.Bind("m")
	if err != nil {
		t.Fatal(err)
	}
	sameBound(t, loaded, want)

	// Deterministic: the same fit encodes to the same bytes, and a
	// re-encode of the parsed file is byte-identical.
	again, _ := f.Encode()
	re, _ := got.Encode()
	if !bytes.Equal(data, again) || !bytes.Equal(data, re) {
		t.Fatalf("encoding is not deterministic:\n%s\n%s\n%s", data, again, re)
	}
	for _, key := range []string{`"model_format_version": 1`, `"df": null`, `"+Inf"`, `"-0"`, `"provenance"`} {
		if key == `"-0"` {
			key = `-0`
		}
		if !strings.Contains(string(data), key) {
			t.Errorf("encoded file lacks %s:\n%s", key, data)
		}
	}
	// Not a scoring figure: p-values and R² are not saved.
	if strings.Contains(string(data), "p_values") || strings.Contains(string(data), "r2") {
		t.Errorf("file carries non-scoring figures:\n%s", data)
	}
}

// TestModelFile_RobustCovarianceRoundTrip: a fit whose standard errors
// came from a robust covariance (RegressionSpec.Covariance) saves the
// estimator that produced them — type, cluster field and level count,
// order_by field and resolved lag — as model_format_version 2, and
// reads it back on the Result and the Bound with every figure
// bit-identical; a classical fit stays version 1 with no covariance key.
func TestModelFile_RobustCovarianceRoundTrip(t *testing.T) {
	t.Parallel()

	lags := 3
	echoes := []*types.RegressionCovarianceResult{
		{Type: types.CovarianceHC3},
		{Type: types.CovarianceCluster, Cluster: "region", Clusters: 8},
		{Type: types.CovarianceHAC, OrderBy: "week", Lags: &lags},
	}
	for _, typ := range []types.RegressionType{types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR} {
		for _, echo := range echoes {
			t.Run(string(typ)+"/"+string(echo.Type), func(t *testing.T) {
				spec, res := fullFit()
				spec.Type, res.Type = typ, typ
				res.Covariance = echo
				f, err := NewFile("m", spec, res, Provenance{})
				if err != nil {
					t.Fatal(err)
				}
				got, data := roundTrip(t, f)
				if got.ModelFormatVersion != 2 || !strings.Contains(string(data), `"model_format_version": 2`) {
					t.Fatalf("version %d, want 2:\n%s", got.ModelFormatVersion, data)
				}
				r := got.Result()
				if !reflect.DeepEqual(r.Covariance, echo) {
					t.Fatalf("covariance read back %+v, want %+v", r.Covariance, echo)
				}
				if echo.Lags != nil && r.Covariance.Lags == echo.Lags {
					t.Error("the read-back lag aliases the saved result's")
				}
				sameFloatMap(t, "std_errors", r.StdErrors, res.StdErrors)
				sameFloatMap(t, "ci_lower", r.CILower, res.CILower)
				for i := range res.Vcov.Values {
					sameFloats(t, "vcov", r.Vcov.Values[i], res.Vcov.Values[i])
				}
				loaded, err := got.Bind("m")
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(loaded.Result.Covariance, echo) {
					t.Errorf("bound result covariance %+v, want %+v", loaded.Result.Covariance, echo)
				}
				re, _ := got.Encode()
				if !bytes.Equal(data, re) {
					t.Fatalf("re-encode differs:\n%s\n%s", data, re)
				}
			})
		}
	}
	spec, res := fullFit()
	f, _ := NewFile("m", spec, res, Provenance{})
	data, _ := f.Encode()
	if !strings.Contains(string(data), `"model_format_version": 1`) || strings.Contains(string(data), `"covariance":`) {
		t.Fatalf("a classical fit must stay version 1 without a covariance key:\n%s", data)
	}
}

// TestModelFile_DispersionRoundTrip: a classical vcov fit carrying its
// estimated dispersion (RegressionResult.Dispersion — REG_OLS σ̂², a
// gamma GLM's Pearson χ²/df; NaN when undefined) saves as
// model_format_version 4 and reads it back bit-exactly, so the loaded
// fit's influence face carries it as the in-request fit's does; a fit
// without one stays version 1, and its loaded OLS face has no
// dispersion (an earlier build's file: CheckFitDesign refuses it).
func TestModelFile_DispersionRoundTrip(t *testing.T) {
	t.Parallel()

	for _, phi := range []float64{1.2575235440954267, math.NaN()} {
		spec, res := fullFit()
		res.Dispersion = phi
		f, err := NewFile("m", spec, res, Provenance{})
		if err != nil {
			t.Fatal(err)
		}
		got, data := roundTrip(t, f)
		if got.ModelFormatVersion != 4 || !strings.Contains(string(data), `"dispersion": `) {
			t.Fatalf("version %d, want 4 with a dispersion key:\n%s", got.ModelFormatVersion, data)
		}
		if r := got.Result(); !sameBits(r.Dispersion, phi) {
			t.Fatalf("dispersion read back %v, want %v", r.Dispersion, phi)
		}
		want, _ := Bind("m", spec, res)
		loaded, err := got.Bind("m")
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.Design.Dispersion || loaded.Design != want.Design {
			t.Fatalf("loaded face %+v, in-request face %+v", loaded.Design, want.Design)
		}
		sig := CatalogWithModels(nil, []types.ModelRef{{Name: "m", Path: "m" + FileSuffix}}, []*File{got})["m"]
		if sig.Influence != want.Design {
			t.Fatalf("predict face %+v, runtime face %+v", sig.Influence, want.Design)
		}
	}
	spec, res := fullFit()
	f, _ := NewFile("m", spec, res, Provenance{})
	if f.ModelFormatVersion != 1 {
		t.Fatalf("a fit without a dispersion writes version %d, want 1", f.ModelFormatVersion)
	}
	b, _ := f.Bind("m")
	if spec.Type == types.REG_OLS && b.Design.Dispersion {
		t.Fatal("a version-1 OLS file reports a dispersion it does not carry")
	}
}

// TestModelFile_SelectionResampleGLM: each fit shape saves what scoring
// needs — selection the retained predictors (with the candidate set as
// terms, a dropped candidate scoring 0), resample its Wald figures
// without df, a GLM its family and link.
func TestModelFile_SelectionResampleGLM(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		spec      *types.RegressionSpec
		res       *types.RegressionResult
		wantPreds []string
		wantTerms []string
	}{
		{
			name: "selection",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b", "c"}, Selection: "forward", Criterion: "aic"},
			res: &types.RegressionResult{Type: types.REG_OLS, Selection: "forward", SelectedFeatures: []string{"c", "a"},
				Coefficients: map[string]float64{InterceptKey: 1, "c": 2, "a": -0.5},
				StdErrors:    map[string]float64{InterceptKey: 0.1, "c": 0.2, "a": 0.3}, DF: 50, ConfLevel: 0.95},
			wantPreds: []string{"c", "a"},
			wantTerms: []string{"a", "b", "c"},
		},
		{
			name: "resample",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a"}, Resample: "bootstrap"},
			res: &types.RegressionResult{Type: types.REG_OLS, Resample: "bootstrap",
				Coefficients: map[string]float64{InterceptKey: 1, "a": 2},
				StdErrors:    map[string]float64{InterceptKey: 0.1, "a": 0.2},
				CILower:      map[string]float64{InterceptKey: 0.8, "a": 1.6}, CIUpper: map[string]float64{InterceptKey: 1.2, "a": 2.4}, ConfLevel: 0.95},
			wantPreds: []string{"a"},
			wantTerms: []string{"a"},
		},
		{
			name: "glm",
			spec: &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"a"}, Family: "poisson"},
			res: &types.RegressionResult{Type: types.REG_GLM, Family: "poisson", Link: "log",
				Coefficients: map[string]float64{InterceptKey: 0.25, "a": 0.125}},
			wantPreds: []string{"a"},
			wantTerms: []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := NewFile("m", tc.spec, tc.res, Provenance{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.Predictors, tc.wantPreds) || !reflect.DeepEqual(f.Terms, tc.wantTerms) {
				t.Fatalf("saved predictors %v terms %v, want %v %v", f.Predictors, f.Terms, tc.wantPreds, tc.wantTerms)
			}
			got, _ := roundTrip(t, f)
			want, err := Bind("m", tc.spec, tc.res)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := got.Bind("m")
			if err != nil {
				t.Fatal(err)
			}
			sameBound(t, loaded, want)
			if got.Resample != tc.res.Resample || got.Selection != tc.spec.Selection || got.DF != Float(tc.res.DF) {
				t.Errorf("modifiers: %+v", got)
			}
		})
	}
	// A dropped selection candidate still scores 0 from the loaded model.
	spec, res := cases[0].spec, cases[0].res
	f, _ := NewFile("sel", spec, res, Provenance{})
	got, _ := roundTrip(t, f)
	b, _ := got.Bind("sel")
	set := &Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	if v, err := Coef(set, "sel", "b", StatPoint); err != nil || v != 0 {
		t.Fatalf("dropped candidate = %v, %v; want 0", v, err)
	}
}

// TestDecodeFile_Refusals: malformed JSON, a missing or mistyped
// version, an unknown version, unknown keys and structural faults are
// each refused with their code and reason.
func TestDecodeFile_Refusals(t *testing.T) {
	t.Parallel()

	spec, res := fullFit()
	f, _ := NewFile("m", spec, res, Provenance{})
	good, _ := f.Encode()
	edit := func(fn func(m map[string]any)) string {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		fn(m)
		b, _ := json.Marshal(m)
		return string(b)
	}
	cases := []struct {
		name   string
		doc    string
		code   errors.Code
		reason string
	}{
		{"not json", `{"model_format_version": 1,`, errors.PULSE_MODEL_FILE_INVALID, ReasonFileJSON},
		{"array", `[1]`, errors.PULSE_MODEL_FILE_INVALID, ReasonFileJSON},
		{"no version", edit(func(m map[string]any) { delete(m, "model_format_version") }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"null version", edit(func(m map[string]any) { m["model_format_version"] = nil }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"string version", edit(func(m map[string]any) { m["model_format_version"] = "1" }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"future version", edit(func(m map[string]any) { m["model_format_version"] = 5 }), errors.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"v4 without dispersion", edit(func(m map[string]any) { m["model_format_version"] = 4 }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v1 with dispersion", edit(func(m map[string]any) { m["dispersion"] = 1.5 }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v4 without vcov", edit(func(m map[string]any) {
			m["model_format_version"] = 4
			m["dispersion"] = 1.5
			delete(m, "vcov")
		}), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v4 with covariance", edit(func(m map[string]any) {
			m["model_format_version"] = 4
			m["dispersion"] = 1.5
			m["covariance"] = map[string]any{"type": "hc3"}
		}), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v3 with a built-in type", edit(func(m map[string]any) { m["model_format_version"] = 3 }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v1 with an extension type", edit(func(m map[string]any) { m["type"] = "REG_X_CUSTOM" }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v1 with outputs", edit(func(m map[string]any) { m["outputs"] = map[string]any{"k": 1} }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v3 with no coefficient", edit(func(m map[string]any) {
			m["model_format_version"] = 3
			m["type"] = "REG_X_CUSTOM"
			m["coefficients"] = map[string]any{}
		}), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v2 without covariance", edit(func(m map[string]any) { m["model_format_version"] = 2 }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"v1 with covariance", edit(func(m map[string]any) { m["covariance"] = map[string]any{"type": "hc3"} }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"classical covariance", edit(func(m map[string]any) {
			m["model_format_version"] = 2
			m["covariance"] = map[string]any{"type": "classical"}
		}), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"unknown covariance type", edit(func(m map[string]any) {
			m["model_format_version"] = 2
			m["covariance"] = map[string]any{"type": "hc9"}
		}), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"unknown covariance key", edit(func(m map[string]any) {
			m["model_format_version"] = 2
			m["covariance"] = map[string]any{"type": "hc3", "kernel": "bartlett"}
		}), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"zero version", edit(func(m map[string]any) { m["model_format_version"] = 0 }), errors.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"unknown key", edit(func(m map[string]any) { m["coefficient"] = 1 }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"bad float string", edit(func(m map[string]any) { m["df"] = "NaN" }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no type", edit(func(m map[string]any) { delete(m, "type") }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no target", edit(func(m map[string]any) { m["target"] = "" }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no intercept", edit(func(m map[string]any) { delete(m["coefficients"].(map[string]any), InterceptKey) }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"predictor without coefficient", edit(func(m map[string]any) { delete(m["coefficients"].(map[string]any), "b") }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"predictor outside terms", edit(func(m map[string]any) { m["terms"] = []string{"a"}; m["selection"] = "forward" }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"terms differ without selection", edit(func(m map[string]any) { m["terms"] = []string{"a", "b", "c"} }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"duplicate predictor", edit(func(m map[string]any) { m["predictors"] = []string{"a", "a"}; m["terms"] = []string{"a", "a"} }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"ragged vcov", edit(func(m map[string]any) { m["vcov"].(map[string]any)["values"] = [][]float64{{1}} }), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeFile("x"+FileSuffix, []byte(tc.doc))
			ce := coded(t, err)
			if ce.Code != tc.code {
				t.Fatalf("code = %s, want %s (%v)", ce.Code, tc.code, err)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, err)
			}
			if ce.Details["path"] != "x"+FileSuffix {
				t.Fatalf("details.path = %v", ce.Details["path"])
			}
		})
	}
	if _, err := DecodeFile("ok", good); err != nil {
		t.Fatalf("the unedited file must decode: %v", err)
	}
	_, err := DecodeFile("p", []byte(edit(func(m map[string]any) { m["model_format_version"] = 7 })))
	if ce := coded(t, err); !reflect.DeepEqual(ce.Details["supported"], []int{1, 2, 3, 4}) || ce.Details["version"] != 7 {
		t.Fatalf("version details = %v", ce.Details)
	}
}

// TestCheckSavePath: the save-path rule shared by predict and the
// runtime.
func TestCheckSavePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path   string
		reason string
	}{
		{"m.pulse-model.json", ""},
		{"models/mmm.pulse-model.json", ""},
		{"/abs/m.pulse-model.json", ""},
		{"a/../m.pulse-model.json", ""},
		{"./m.pulse-model.json", ""},
		{"", ReasonSavePathEmpty},
		{"   ", ReasonSavePathEmpty},
		{"m.json", ReasonSaveSuffix},
		{"m.pulse-model.json.bak", ReasonSaveSuffix},
		{"m.pulse-model.json/", ReasonSaveSuffix},
		{"models/.pulse-model.json", ReasonSaveNameMissing},
		{".pulse-model.json", ReasonSaveNameMissing},
		{"../m.pulse-model.json", ReasonSaveEscapesRoot},
		{"a/../../m.pulse-model.json", ReasonSaveEscapesRoot},
	}
	for _, tc := range cases {
		cerr := CheckSavePath(tc.path)
		if tc.reason == "" {
			if cerr != nil {
				t.Errorf("%q: unexpected refusal %v", tc.path, cerr)
			}
			continue
		}
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_SAVE_INVALID || cerr.Details["reason"] != tc.reason || cerr.Details["path"] != tc.path {
			t.Errorf("%q: got %#v, want reason %s", tc.path, cerr, tc.reason)
		}
	}
	regs := []*types.RegressionSpec{nil, {Save: "ok" + FileSuffix}, {}, {Save: "bad.json"}, {Save: "../x" + FileSuffix}}
	cerr := CheckRegressionSaves(regs)
	if cerr == nil || cerr.Details["regression"] != 3 || cerr.Details["reason"] != ReasonSaveSuffix {
		t.Fatalf("first fault = %#v, want regression 3 suffix", cerr)
	}
	if CheckRegressionSaves(regs[:3]) != nil {
		t.Fatal("valid saves refused")
	}
}

// TestCheckFits_SavePaths: a fit's own save and its regression's save
// are judged before any fit runs, located by details.fit.
func TestCheckFits_SavePaths(t *testing.T) {
	t.Parallel()

	badReg := olsReq(&types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Save: "m.json"})
	cases := []struct {
		name  string
		fits  []types.FitSpec
		fit   int
		extra string
	}{
		{"fit save", []types.FitSpec{{Name: "a", Request: olsReq(ols()), Save: "ok" + FileSuffix}, {Name: "b", Request: olsReq(ols()), Save: "../b" + FileSuffix}}, 1, ""},
		{"regression save", []types.FitSpec{{Name: "a", Request: badReg}}, 0, "regression"},
		// A save fault is judged before a later duplicate name.
		{"before collision", []types.FitSpec{{Name: "a", Request: olsReq(ols())}, {Name: "a", Request: olsReq(ols()), Save: "x"}}, 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := coded(t, CheckFits(tc.fits))
			if ce.Code != errors.PULSE_MODEL_SAVE_INVALID || ce.Details["fit"] != tc.fit || ce.Details["name"] != tc.fits[tc.fit].Name {
				t.Fatalf("got %#v", ce)
			}
			if tc.extra != "" {
				if _, ok := ce.Details[tc.extra]; !ok {
					t.Fatalf("details lack %s: %v", tc.extra, ce.Details)
				}
			}
		})
	}
	if err := CheckFits([]types.FitSpec{{Name: "a", Request: olsReq(ols()), Save: "m" + FileSuffix}}); err != nil {
		t.Fatalf("valid save refused: %v", err)
	}
}
