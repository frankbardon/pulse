package regression

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// olsNNLSCase is one ols_nnls.json case (scripts/reference/gen_ols_nnls.R).
type olsNNLSCase struct {
	Name           string      `json:"name"`
	Predictors     []string    `json:"predictors"`
	Nonnegative    []string    `json:"nonnegative"`
	Weighted       bool        `json:"weighted"`
	Rows           [][]float64 `json:"rows"`
	Coefficients   []float64   `json:"coefficients"`
	Active         []string    `json:"active"`
	R2             float64     `json:"r2"`
	AdjR2          float64     `json:"adj_r2"`
	ResidualStdErr float64     `json:"residual_std_err"`
}

func loadOLSNNLSReference(t *testing.T) []olsNNLSCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "ols_nnls.json"))
	if err != nil {
		t.Fatalf("read ols_nnls.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []olsNNLSCase     `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode ols_nnls.json: %v", err)
	}
	if doc.Packages["nnls"] == "" || len(doc.Cases) == 0 {
		t.Fatal("ols_nnls.json has no nnls version or no cases")
	}
	return doc.Cases
}

func (c olsNNLSCase) records() []Record {
	p := len(c.Predictors)
	recs := make([]Record, len(c.Rows))
	for i, row := range c.Rows {
		m := map[string]float64{"y": row[p], "w": row[p+1]}
		for j, name := range c.Predictors {
			m[name] = row[j]
		}
		recs[i] = mockRecord{values: m}
	}
	return recs
}

// spec is the bounded fit; plain drops the bounds.
func (c olsNNLSCase) spec(mut func(*types.RegressionSpec)) *types.RegressionSpec {
	spec := &types.RegressionSpec{
		Type: types.REG_OLS, Target: "y", Predictors: c.Predictors,
		Bounds: &types.RegressionBounds{Nonnegative: c.Nonnegative},
	}
	if c.Weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	if mut != nil {
		mut(spec)
	}
	return spec
}

func nnlsCase(t *testing.T, name string) olsNNLSCase {
	t.Helper()
	for _, c := range loadOLSNNLSReference(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("ols_nnls.json has no case %q", name)
	return olsNNLSCase{}
}

// TestReferenceOracle_OLSNonnegative: REG_OLS bounds.nonnegative matches
// R nnls (KKT- and lm.fit-checked in the generator) on every case — no
// binding, one / two binding, a free negative slope, near-collinear,
// frequency-weighted: coefficients, the held predictors, R², adjusted R²
// and the residual standard error within relative 1e-10. A held slope is
// exactly 0; a binding fit reports every standard error, p-value and
// interval null, a non-binding one finite ones.
func TestReferenceOracle_OLSNonnegative(t *testing.T) {
	t.Parallel()

	for _, c := range loadOLSNNLSReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			res, err := newOLS(t, c.spec(nil)).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			for j, key := range coefficientKeys(c.Predictors) {
				if got := res.Coefficients[key]; !oracleClose(got, c.Coefficients[j], 1e-10) {
					t.Errorf("β[%s] = %.17g, oracle %.17g", key, got, c.Coefficients[j])
				}
			}
			for _, name := range c.Active {
				if res.Coefficients[name] != 0 {
					t.Errorf("β[%s] = %v, oracle holds it at 0", name, res.Coefficients[name])
				}
			}
			if !slices.Equal(res.ActiveBounds, c.Active) && (len(res.ActiveBounds) != 0 || len(c.Active) != 0) {
				t.Errorf("ActiveBounds = %v, oracle %v", res.ActiveBounds, c.Active)
			}
			for _, f := range []struct {
				name      string
				got, want float64
			}{{"r2", res.R2, c.R2}, {"adj_r2", res.AdjR2, c.AdjR2}, {"residual_std_err", res.ResidualStdErr, c.ResidualStdErr}} {
				if !oracleClose(f.got, f.want, 1e-10) {
					t.Errorf("%s = %.17g, oracle %.17g", f.name, f.got, f.want)
				}
			}
			binding := len(c.Active) != 0
			for _, key := range coefficientKeys(c.Predictors) {
				for _, m := range []map[string]float64{res.StdErrors, res.PValues, res.CILower, res.CIUpper} {
					v, ok := m[key]
					if !ok {
						t.Fatalf("key %s missing from an inference map", key)
					}
					if binding != math.IsNaN(v) {
						t.Errorf("binding=%v but %s inference figure = %v", binding, key, v)
					}
				}
			}
		})
	}
}

// TestRegOLS_BoundsNoBindingIsPlainOLS: a bounded fit whose unconstrained
// solution already satisfies every bound is plain OLS bit for bit — the
// whole result, vcov included, on the buffered and streaming paths.
func TestRegOLS_BoundsNoBindingIsPlainOLS(t *testing.T) {
	t.Parallel()

	c := nnlsCase(t, "no_binding")
	withVcov := func(s *types.RegressionSpec) { s.Vcov = true }
	plain := c.spec(func(s *types.RegressionSpec) { s.Bounds, s.Vcov = nil, true })
	want, err := newOLS(t, plain).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	got, err := newOLS(t, c.spec(withVcov)).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	wb, _ := types.MarshalFinite(want)
	gb, _ := types.MarshalFinite(got)
	if string(wb) != string(gb) || !reflect.DeepEqual(want, got) {
		t.Fatalf("bounded no-binding fit differs from plain OLS:\n got  %s\n want %s", gb, wb)
	}

	// Streaming: UpdateRow / Finalize equals the buffered result.
	eng, err := newOLSEngine(c.spec(withVcov), schemaWithFields(append([]string{"y"}, c.Predictors...)...))
	if err != nil {
		t.Fatal(err)
	}
	se := eng.(StreamingEngine)
	for _, r := range c.records() {
		if err := se.UpdateRow(r); err != nil {
			t.Fatal(err)
		}
	}
	streamed, err := se.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(streamed, got) {
		t.Fatal("streaming bounded fit differs from the buffered one")
	}
}

// TestRegOLS_BoundsBindingNullsInference: a binding fit keeps its point
// estimates and R² and reports null inference — standard errors,
// p-values, intervals on every key and, under vcov, an all-null
// covariance and correlation with the keys kept; ActiveBounds names the
// held predictors. Streaming equals buffered.
func TestRegOLS_BoundsBindingNullsInference(t *testing.T) {
	t.Parallel()

	c := nnlsCase(t, "binding_mixed_free")
	spec := c.spec(func(s *types.RegressionSpec) { s.Vcov = true })
	res, err := newOLS(t, spec).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.ActiveBounds, []string{"x2"}) {
		t.Fatalf("ActiveBounds = %v, want [x2]", res.ActiveBounds)
	}
	if res.Coefficients["x3"] >= 0 {
		t.Errorf("free x3 = %v; an unbounded slope keeps its negative sign", res.Coefficients["x3"])
	}
	if res.Vcov == nil || res.Correlation == nil {
		t.Fatal("vcov: true on a binding fit keeps the (all-null) covariance")
	}
	keys := coefficientKeys(c.Predictors)
	if !slices.Equal(res.Vcov.RowKeys, keys) {
		t.Errorf("vcov keys = %v, want %v", res.Vcov.RowKeys, keys)
	}
	for i, row := range res.Vcov.Values {
		for j, v := range row {
			if !math.IsNaN(v) || !math.IsNaN(res.Correlation.Values[i][j]) {
				t.Errorf("vcov[%d][%d] = %v, corr %v; want null", i, j, v, res.Correlation.Values[i][j])
			}
		}
	}
	if !(res.R2 > 0) || !(res.ResidualStdErr > 0) {
		t.Errorf("R² %v, residual SE %v: point summaries stand", res.R2, res.ResidualStdErr)
	}

	eng, err := newOLSEngine(spec, schemaWithFields(append([]string{"y"}, c.Predictors...)...))
	if err != nil {
		t.Fatal(err)
	}
	se := eng.(StreamingEngine)
	for _, r := range c.records() {
		if err := se.UpdateRow(r); err != nil {
			t.Fatal(err)
		}
	}
	streamed, err := se.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := types.MarshalFinite(streamed)
	bb, _ := types.MarshalFinite(res)
	if string(sb) != string(bb) {
		t.Fatalf("streaming bounded fit differs:\n %s\n %s", sb, bb)
	}
}

// TestRegOLS_BoundsBootstrap: bounds under resample bootstrap refits the
// bounded model per replicate: the point estimate is the bounded fit's
// (the held slope 0), and the replicate standard errors, p-values and
// intervals replace the nulls — finite on every key.
func TestRegOLS_BoundsBootstrap(t *testing.T) {
	t.Parallel()

	c := nnlsCase(t, "binding_one")
	base, err := newOLS(t, c.spec(nil)).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	boot, err := newOLS(t, c.spec(func(s *types.RegressionSpec) {
		s.Resample, s.BootstrapIters, s.RNGSeed, s.Vcov = "bootstrap", 200, 7, true
	})).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range coefficientKeys(c.Predictors) {
		if boot.Coefficients[key] != base.Coefficients[key] {
			t.Errorf("β[%s] = %v under bootstrap, bounded fit %v", key, boot.Coefficients[key], base.Coefficients[key])
		}
		for name, m := range map[string]map[string]float64{"se": boot.StdErrors, "p": boot.PValues, "lo": boot.CILower, "hi": boot.CIUpper} {
			if v := m[key]; math.IsNaN(v) || math.IsInf(v, 0) {
				t.Errorf("%s[%s] = %v under bootstrap; want finite", name, key, v)
			}
		}
	}
	if boot.Vcov == nil || math.IsNaN(boot.Vcov.Values[1][1]) {
		t.Error("bootstrap vcov should be the finite replicate covariance")
	}
	// The held slope's replicates are bounded too: an unbounded refit
	// would centre them on the negative OLS slope, inflating the spread
	// far beyond the bounded replicates'.
	unb, err := newOLS(t, c.spec(func(s *types.RegressionSpec) {
		s.Bounds, s.Resample, s.BootstrapIters, s.RNGSeed = nil, "bootstrap", 200, 7
	})).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	if !(boot.StdErrors["x2"] < unb.StdErrors["x2"]) {
		t.Errorf("bounded replicate SE(x2) %v not below the unbounded %v", boot.StdErrors["x2"], unb.StdErrors["x2"])
	}
}

// TestRegOLS_BoundsSingularRefused: a rank-deficient design goes through
// the existing singular-fit refusal, bounded or not.
func TestRegOLS_BoundsSingularRefused(t *testing.T) {
	t.Parallel()

	c := nnlsCase(t, "binding_one")
	recs := c.records()
	for i, r := range recs {
		m := r.(mockRecord).values
		m["dup"] = m["x2"]
		recs[i] = mockRecord{values: m}
	}
	spec := &types.RegressionSpec{
		Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2", "dup"},
		Bounds: &types.RegressionBounds{Nonnegative: []string{"x2", "dup"}},
	}
	_, err := newOLS(t, spec).FitBuffered(recs)
	if ce := asCoded(err); ce == nil || ce.Code != errors.PROCESSING_REGRESSION_RANK_DEFICIENT {
		t.Fatalf("err = %v, want PROCESSING_REGRESSION_RANK_DEFICIENT", err)
	}
}

// TestRegOLS_BoundsRefusals: the engine refuses what regbounds.Check
// refuses (predict parity lives in internal/service).
func TestRegOLS_BoundsRefusals(t *testing.T) {
	t.Parallel()

	base := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{
			Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b"},
			Bounds: &types.RegressionBounds{Nonnegative: []string{"a"}},
		}
		mut(s)
		return s
	}
	for name, spec := range map[string]*types.RegressionSpec{
		"unknown name": base(func(s *types.RegressionSpec) { s.Bounds.Nonnegative = []string{"zz"} }),
		"ridge":        base(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 1 }),
		"selection":    base(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }),
		"robust":       base(func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1} }),
	} {
		if err := ValidateRegression(spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
