package regression

import (
	stderrors "errors"
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// labeledRecord is a mockRecord that also answers StringValueChecked —
// the LabeledRecord extension a categorical cluster field is read
// through.
type labeledRecord struct {
	mockRecord
	labels map[string]string
}

func (r labeledRecord) StringValueChecked(name string) (string, bool, error) {
	s, ok := r.labels[name]
	return s, ok, nil
}

// covRecords is a small heteroskedastic series: t orders it, g holds
// four clusters of three rows, lbl the same clusters as labels.
func covRecords() []Record {
	out := make([]Record, 12)
	for i := range out {
		x := float64(i%5) + 0.25*float64(i)
		y := 1 + 0.8*x + 0.3*float64((i*7)%5-2)*x
		out[i] = labeledRecord{
			mockRecord: mockRecord{values: map[string]float64{"x": x, "y": y, "t": float64(i), "g": float64(i / 3)}},
			labels:     map[string]string{"lbl": "c" + strconv.Itoa(i/3)},
		}
	}
	return out
}

func covFit(t *testing.T, cov *types.RegressionCovariance, recs []Record) *types.RegressionResult {
	t.Helper()
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Vcov: true, Covariance: cov}
	eng, err := newOLSEngine(spec, nil)
	if err != nil {
		t.Fatalf("newOLSEngine: %v", err)
	}
	res, err := eng.(BufferedEngine).FitBuffered(recs)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	return res
}

// TestCovariance_HACLagZeroIsHC1: unweighted, Newey–West truncated at
// lag 0 is exactly HC1 (same meat, same N*/(N*−k)) when the rows are
// already in order — the two estimators share one sandwich.
func TestCovariance_HACLagZeroIsHC1(t *testing.T) {
	recs := covRecords()
	hc1 := covFit(t, &types.RegressionCovariance{Type: types.CovarianceHC1}, recs)
	hac := covFit(t, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t", Lags: types.LagsOf(0)}, recs)
	for k, se := range hc1.StdErrors {
		if hac.StdErrors[k] != se {
			t.Errorf("SE[%s]: hac lag 0 %v, hc1 %v", k, hac.StdErrors[k], se)
		}
	}
	if *hac.Covariance.Lags != 0 || hac.Covariance.OrderBy != "t" {
		t.Errorf("hac echo %+v", hac.Covariance)
	}
	// A shuffled row order sorts back by t: the same fit within
	// rounding. (A plain reversal would not do — the symmetric
	// Γ_l + Γ_lᵀ is invariant under it.)
	rev := make([]Record, len(recs))
	for i, r := range recs {
		rev[(i*5)%len(recs)] = r
	}
	hacRev := covFit(t, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t", Lags: types.LagsOf(2)}, rev)
	hacFwd := covFit(t, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t", Lags: types.LagsOf(2)}, recs)
	for k, se := range hacFwd.StdErrors {
		if math.Abs(hacRev.StdErrors[k]-se) > 1e-12*se {
			t.Errorf("SE[%s]: shuffled rows %v, ordered %v", k, hacRev.StdErrors[k], se)
		}
	}
}

// TestCovariance_SingletonClustersAreHC1: one row per cluster, CR1 is
// HC1 (G/(G−1)·(n−1)/(n−k) = n/(n−k)); a cluster keyed by label or by
// number gives the same fit, the label read through LabeledRecord.
func TestCovariance_SingletonClustersAreHC1(t *testing.T) {
	recs := covRecords()
	hc1 := covFit(t, &types.RegressionCovariance{Type: types.CovarianceHC1}, recs)
	one := covFit(t, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "t"}, recs)
	for k, se := range hc1.StdErrors {
		if math.Abs(one.StdErrors[k]-se) > 1e-12*se {
			t.Errorf("SE[%s]: singleton clusters %v, hc1 %v", k, one.StdErrors[k], se)
		}
	}
	if one.DF != 11 || one.Covariance.Clusters != 12 {
		t.Errorf("df %v clusters %d, want 11 / 12", one.DF, one.Covariance.Clusters)
	}
	byNum := covFit(t, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}, recs)
	byLbl := covFit(t, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "lbl"}, recs)
	for k, se := range byNum.StdErrors {
		if byLbl.StdErrors[k] != se {
			t.Errorf("SE[%s]: by label %v, by number %v", k, byLbl.StdErrors[k], se)
		}
	}
	if byNum.Covariance.Clusters != 4 || byNum.DF != 3 {
		t.Errorf("4 clusters: echo %+v df %v", byNum.Covariance, byNum.DF)
	}
}

// TestCovariance_NullKeyDropsRow: a null cluster value drops the row
// from the fit itself (n_obs), like a null predictor.
func TestCovariance_NullKeyDropsRow(t *testing.T) {
	recs := covRecords()
	lr := recs[4].(labeledRecord)
	recs[4] = labeledRecord{mockRecord: mockRecord{values: lr.values, nulls: map[string]bool{"g": true}}, labels: lr.labels}
	res := covFit(t, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}, recs)
	if res.NObs != 11 {
		t.Errorf("n_obs = %d, want 11 (the null-cluster row dropped)", res.NObs)
	}
	classic := covFit(t, nil, recs)
	if classic.NObs != 12 {
		t.Errorf("classical n_obs = %d, want 12", classic.NObs)
	}
}

// TestCovariance_StreamingRefusesRobust: a robust spec never streams
// (RegressionSpec.Streamable); driven through UpdateRow / Finalize
// anyway, the engine refuses rather than report classical errors.
func TestCovariance_StreamingRefusesRobust(t *testing.T) {
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Covariance: &types.RegressionCovariance{Type: types.CovarianceHC0}}
	eng, err := newOLSEngine(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	se := eng.(StreamingEngine)
	for _, r := range covRecords() {
		if err := se.UpdateRow(r); err != nil {
			t.Fatal(err)
		}
	}
	_, err = se.Finalize()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("Finalize err = %v, want PROCESSING_INTERNAL", err)
	}
}

// TestCovariance_HCOmega: hc0 / hc1 weigh every row 1; hc2 / hc3 divide
// by (1−h) / (1−h)²; a leverage of 1 is undefined (NaN → null), never
// ±Inf.
func TestCovariance_HCOmega(t *testing.T) {
	if hcOmega(types.CovarianceHC0, 0.5) != 1 || hcOmega(types.CovarianceHC1, 0.5) != 1 {
		t.Error("hc0 / hc1 must weigh 1")
	}
	if hcOmega(types.CovarianceHC2, 0.5) != 2 || hcOmega(types.CovarianceHC3, 0.5) != 4 {
		t.Error("hc2 / hc3 at h = 0.5 must be 2 / 4")
	}
	for _, typ := range []types.CovarianceType{types.CovarianceHC2, types.CovarianceHC3} {
		if !math.IsNaN(hcOmega(typ, 1)) || !math.IsNaN(hcOmega(typ, 1+1e-16)) {
			t.Errorf("%s at h = 1 must be NaN", typ)
		}
	}
}

// glmBayesCovFit fits typ (REG_GLM poisson or REG_BAYES_LINEAR) over
// recs through the buffered path.
func glmBayesCovFit(t *testing.T, typ types.RegressionType, cov *types.RegressionCovariance, recs []Record) *types.RegressionResult {
	t.Helper()
	spec := &types.RegressionSpec{Type: typ, Target: "y", Predictors: []string{"x"}, Vcov: true, Covariance: cov}
	var (
		eng Engine
		err error
	)
	if typ == types.REG_GLM {
		spec.Family = "poisson"
		eng, err = newGLMEngine(spec, nil)
	} else {
		eng, err = newBayesLinearEngine(spec, nil)
	}
	if err != nil {
		t.Fatalf("new %s engine: %v", typ, err)
	}
	res, err := eng.(BufferedEngine).FitBuffered(recs)
	if err != nil {
		t.Fatalf("%s FitBuffered: %v", typ, err)
	}
	return res
}

// TestCovariance_GLMBayesNullKeyDropsRow: on REG_GLM and
// REG_BAYES_LINEAR too, a null cluster or order_by value drops the row
// from the fit itself (n_obs) and so moves the coefficients, like a
// null predictor; the classical fit keeps it.
func TestCovariance_GLMBayesNullKeyDropsRow(t *testing.T) {
	for _, typ := range []types.RegressionType{types.REG_GLM, types.REG_BAYES_LINEAR} {
		for _, cov := range []*types.RegressionCovariance{
			{Type: types.CovarianceCluster, Cluster: "g"},
			{Type: types.CovarianceHAC, OrderBy: "t"},
		} {
			key := cov.Cluster + cov.OrderBy
			recs := covRecords()
			lr := recs[4].(labeledRecord)
			recs[4] = labeledRecord{mockRecord: mockRecord{values: lr.values, nulls: map[string]bool{key: true}}, labels: lr.labels}
			res := glmBayesCovFit(t, typ, cov, recs)
			if res.NObs != 11 {
				t.Errorf("%s %s: n_obs = %d, want 11 (the null-%s row dropped)", typ, cov.Type, res.NObs, key)
			}
			dropped := glmBayesCovFit(t, typ, nil, append(append([]Record(nil), recs[:4]...), recs[5:]...))
			if res.Coefficients["x"] != dropped.Coefficients["x"] {
				t.Errorf("%s %s: β = %v, the fit without the row %v", typ, cov.Type, res.Coefficients["x"], dropped.Coefficients["x"])
			}
			if classic := glmBayesCovFit(t, typ, nil, recs); classic.NObs != 12 {
				t.Errorf("%s classical n_obs = %d, want 12", typ, classic.NObs)
			}
		}
	}
}

// TestCovariance_GLMBayesHACLagZeroIsHC1: the GLM and Bayes engines
// feed the one sandwich too — Newey–West at lag 0 over rows already in
// order is HC1 to the bit, and hc3 ≥ hc2 ≥ hc0 on the diagonal (the
// leverage the engines hand it is in [0, 1)).
func TestCovariance_GLMBayesHACLagZeroIsHC1(t *testing.T) {
	recs := covRecords()
	for _, typ := range []types.RegressionType{types.REG_GLM, types.REG_BAYES_LINEAR} {
		hc1 := glmBayesCovFit(t, typ, &types.RegressionCovariance{Type: types.CovarianceHC1}, recs)
		hac := glmBayesCovFit(t, typ, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t", Lags: types.LagsOf(0)}, recs)
		for _, k := range []string{InterceptKey, "x"} {
			if hc1.StdErrors[k] != hac.StdErrors[k] {
				t.Errorf("%s %s: hc1 %v, hac(0) %v", typ, k, hc1.StdErrors[k], hac.StdErrors[k])
			}
		}
		hc0 := glmBayesCovFit(t, typ, &types.RegressionCovariance{Type: types.CovarianceHC0}, recs)
		hc2 := glmBayesCovFit(t, typ, &types.RegressionCovariance{Type: types.CovarianceHC2}, recs)
		hc3 := glmBayesCovFit(t, typ, &types.RegressionCovariance{Type: types.CovarianceHC3}, recs)
		for _, k := range []string{InterceptKey, "x"} {
			if !(hc0.StdErrors[k] < hc2.StdErrors[k] && hc2.StdErrors[k] < hc3.StdErrors[k]) {
				t.Errorf("%s %s: want hc0 < hc2 < hc3, got %v %v %v", typ, k, hc0.StdErrors[k], hc2.StdErrors[k], hc3.StdErrors[k])
			}
		}
	}
}

// TestCovariance_BayesStreamingRefusesRobust: REG_BAYES_LINEAR streams
// classically, but a robust spec driven through UpdateRow / Finalize is
// refused rather than reporting the posterior marginals.
func TestCovariance_BayesStreamingRefusesRobust(t *testing.T) {
	spec := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Covariance: &types.RegressionCovariance{Type: types.CovarianceHC0}}
	eng, err := newBayesLinearEngine(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	se := eng.(StreamingEngine)
	for _, r := range covRecords() {
		if err := se.UpdateRow(r); err != nil {
			t.Fatal(err)
		}
	}
	_, err = se.Finalize()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("Finalize err = %v, want PROCESSING_INTERNAL", err)
	}
}
