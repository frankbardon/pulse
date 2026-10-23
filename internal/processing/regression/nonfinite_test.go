package regression

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// nonFiniteRows is a 40-row fixture (y continuous, k a count, x1 / x2
// predictors, c a five-level cluster, t a strictly increasing order) with
// the cell at each dirty position replaced by fill — or made null when
// fill is nil. Products are float64()-barriered (generator rule).
func nonFiniteRows(dirty map[int][]string, fill *float64) []Record {
	out := make([]Record, 40)
	for i := range out {
		f := float64(i)
		x1 := float64(3*math.Sin(f/3)) + float64(0.1*f)
		x2 := float64(2*math.Cos(f/5)) - float64(0.05*f)
		vals := map[string]float64{
			"x1": x1,
			"x2": x2,
			"y":  1 + float64(2*x1) - float64(0.5*x2) + float64(0.7*math.Sin(float64(7*f))),
			"k":  float64(int(3 + x1 + math.Abs(x2))),
			"c":  float64(i % 5),
			"t":  f + 1,
		}
		rec := mockRecord{values: vals}
		for _, name := range dirty[i] {
			if fill == nil {
				if rec.nulls == nil {
					rec.nulls = map[string]bool{}
				}
				rec.nulls[name] = true
				continue
			}
			vals[name] = *fill
		}
		out[i] = rec
	}
	return out
}

// nonFiniteSpecs covers every built-in engine family and every
// regression row read: OLS (plain, ridge, cluster / HAC covariance),
// REG_GLM, REG_BAYES_LINEAR, the resample modifiers (bootstrap and
// jackknife_by) and selection's refit.
func nonFiniteSpecs() []*types.RegressionSpec {
	preds := []string{"x1", "x2"}
	return []*types.RegressionSpec{
		{Name: "ols", Type: types.REG_OLS, Target: "y", Predictors: preds, Vcov: true},
		{Name: "ridge", Type: types.REG_OLS, Target: "y", Predictors: preds, Penalty: "l2", Alpha: 0.5},
		{Name: "cluster", Type: types.REG_OLS, Target: "y", Predictors: preds,
			Covariance: &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "c"}},
		{Name: "hac", Type: types.REG_OLS, Target: "y", Predictors: preds,
			Covariance: &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t"}},
		{Name: "glm", Type: types.REG_GLM, Target: "k", Predictors: preds, Family: "poisson", Link: "log"},
		{Name: "bayes", Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: preds},
		{Name: "boot", Type: types.REG_OLS, Target: "y", Predictors: preds, Resample: "bootstrap", BootstrapIters: 40, RNGSeed: 7},
		{Name: "jack", Type: types.REG_OLS, Target: "y", Predictors: preds, Resample: "jackknife", JackknifeBy: "c"},
		{Name: "select", Type: types.REG_OLS, Target: "y", Predictors: preds, Selection: "backward", Criterion: "aic"},
	}
}

func nonFiniteSchema() *encoding.Schema {
	s := schemaWithFields("y", "k", "x1", "x2", "t")
	s.Fields = append(s.Fields, encoding.Field{Name: "c", Type: encoding.FieldTypeU8})
	return s
}

// TestNonFiniteInput_DropsListwiseLikeNull is the E1-S3 rule: a NaN or
// ±Inf in a regression's target, a predictor, the cluster or the
// order_by field is missing data — every engine fits exactly what it
// fits with a null in the same cell (n excludes the row), result for
// result, bit for bit on the wire.
func TestNonFiniteInput_DropsListwiseLikeNull(t *testing.T) {
	dirty := map[int][]string{3: {"y", "k"}, 7: {"x1"}, 11: {"x2"}, 15: {"c"}, 19: {"t"}}
	specs := nonFiniteSpecs()
	schema := nonFiniteSchema()
	fit := func(recs []Record) ([]*types.RegressionResult, []error) {
		t.Helper()
		res, errs, err := FitEachBufferedWith(specs, schema, recs, nil)
		if err != nil {
			t.Fatalf("FitEachBufferedWith: %v", err)
		}
		return res, errs
	}
	wantRes, wantErrs := fit(nonFiniteRows(dirty, nil))
	for i, s := range specs {
		if wantErrs[i] != nil {
			t.Fatalf("%s: null reference fit failed: %v", s.Name, wantErrs[i])
		}
	}
	for _, fill := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		gotRes, gotErrs := fit(nonFiniteRows(dirty, &fill))
		for i, s := range specs {
			if gotErrs[i] != nil {
				t.Errorf("fill %v %s: fit failed: %v", fill, s.Name, gotErrs[i])
				continue
			}
			want, err := types.MarshalFinite(wantRes[i])
			if err != nil {
				t.Fatal(err)
			}
			got, err := types.MarshalFinite(gotRes[i])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("fill %v %s: result differs from the null fit\n got %s\nwant %s", fill, s.Name, got, want)
			}
		}
	}
	// The listwise n: y / x1 / x2 always drop their row; c and t drop
	// theirs only for the spec that keys rows by them.
	wantN := map[string]int{"ols": 37, "ridge": 37, "cluster": 36, "hac": 36, "glm": 37, "bayes": 37, "boot": 37, "jack": 36, "select": 37}
	for i, s := range specs {
		if wantRes[i].NObs != wantN[s.Name] {
			t.Errorf("%s: n_obs = %d, want %d", s.Name, wantRes[i].NObs, wantN[s.Name])
		}
	}
}

// TestNonFiniteInput_AttributeRefitDropsLikeNull: the ATTR_REG_* refit
// (FitForAttribute) treats a non-finite target or predictor as null.
func TestNonFiniteInput_AttributeRefitDropsLikeNull(t *testing.T) {
	dirty := map[int][]string{3: {"y"}, 7: {"x1"}, 11: {"x2"}}
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}}
	want, err := FitForAttribute(spec, nonFiniteRows(dirty, nil), true)
	if err != nil {
		t.Fatalf("null refit: %v", err)
	}
	for _, fill := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		got, err := FitForAttribute(spec, nonFiniteRows(dirty, &fill), true)
		if err != nil {
			t.Fatalf("fill %v: refit: %v", fill, err)
		}
		if got.NObs != want.NObs || got.NObs != 37 || got.Intercept != want.Intercept {
			t.Errorf("fill %v: refit n=%d b0=%v, null refit n=%d b0=%v (want n 37)", fill, got.NObs, got.Intercept, want.NObs, want.Intercept)
		}
		for j, v := range want.Coefficients {
			if got.Coefficients[j] != v {
				t.Errorf("fill %v: coef %d = %v, null refit %v", fill, j, got.Coefficients[j], v)
			}
		}
	}
}

// TestNonFiniteInput_ScoresNull: a bound model scores a row with a
// non-finite column as a null score, exactly like a null column.
func TestNonFiniteInput_ScoresNull(t *testing.T) {
	recs := scoreGLMRecords()
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "c", Predictors: []string{"x1", "x2"}}
	res, err := newOLS(t, spec).FitBuffered(recs)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	s := mustScorer(t, mustBind(t, spec, res), nil, "")
	for _, fill := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		row := mockRecord{values: map[string]float64{"x1": 1, "x2": fill}}
		if v, ok := s.Score(row); ok {
			t.Errorf("fill %v: score = %v, want null", fill, v)
		}
	}
}

// TestNonFiniteInput_ReadsGoThroughInputValue keeps the rule at one
// point: no production file in this package reads a row's number with
// a raw NumericValue call except record.go (InputValue itself) and
// weight.go (a non-finite weight is classified NaNInf and counted as an
// invalid weight, weighting.Classify). A new raw read would let a NaN
// reach a fit again.
func TestNonFiniteInput_ReadsGoThroughInputValue(t *testing.T) {
	raw := regexp.MustCompile(`\.NumericValue\(`)
	allowed := map[string]bool{"record.go": true, "weight.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}
			if raw.MatchString(line) {
				t.Errorf("%s:%d reads a raw NumericValue; use InputValue (a NaN / ±Inf input is missing data): %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
