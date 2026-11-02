package regordinal

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// fakeRecord is an extend.Record over a map: a key absent is null; a
// string value is a categorical label.
type fakeRecord map[string]any

func (r fakeRecord) Schema() *encoding.Schema { return nil }
func (r fakeRecord) IsNull(f string) bool     { _, ok := r[f]; return !ok }
func (r fakeRecord) NumericValue(f string) (float64, bool) {
	switch v := r[f].(type) {
	case float64:
		return v, true
	case string:
		return 0, true
	}
	return 0, false
}
func (r fakeRecord) Uint64Value(f string) (uint64, bool) {
	v, ok := r.NumericValue(f)
	return uint64(v), ok
}
func (r fakeRecord) StringValue(f string) (string, bool) { s, ok := r[f].(string); return s, ok }
func (r fakeRecord) SetMaskValue(string) (encoding.SetMask, bool) {
	return encoding.SetMask{}, false
}
func (r fakeRecord) DecimalValue(string) (encoding.Decimal128, bool) {
	return encoding.Decimal128{}, false
}
func (r fakeRecord) VectorValue(string) ([]float64, bool) { return nil, false }
func (r fakeRecord) Weight() (float64, bool)              { return 0, false }

// TestAdmitRow: the model's own listwise rule — a response
// params.levels does not list, and a null covariance cluster, are
// missing; every other row is admitted (the engine judged target,
// predictors and weight already).
func TestAdmitRow(t *testing.T) {
	t.Parallel()
	mk := func(params string, cov *types.RegressionCovariance) *model {
		s := &types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Predictors: []string{"x"}, Covariance: cov}
		if params != "" {
			s.Params = json.RawMessage(params)
		}
		e, err := Factory(s, nil)
		if err != nil {
			t.Fatal(err)
		}
		return e.(*model)
	}
	plain := mk("", nil)
	listed := mk(`{"levels":["1","2","3"]}`, nil)
	labels := mk(`{"levels":["low","mid","high"]}`, nil)
	cluster := mk("", &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"})
	for name, tc := range map[string]struct {
		m    *model
		rec  fakeRecord
		want bool
	}{
		"plain":           {plain, fakeRecord{"y": 7.0, "x": 1.0}, true},
		"listed_number":   {listed, fakeRecord{"y": 2.0, "x": 1.0}, true},
		"unlisted_number": {listed, fakeRecord{"y": 0.0, "x": 1.0}, false},
		"fraction":        {listed, fakeRecord{"y": 2.5, "x": 1.0}, false},
		"listed_label":    {labels, fakeRecord{"y": "mid", "x": 1.0}, true},
		"unlisted_label":  {labels, fakeRecord{"y": "n/a", "x": 1.0}, false},
		"cluster_present": {cluster, fakeRecord{"y": 1.0, "x": 1.0, "g": "a"}, true},
		"cluster_numeric": {cluster, fakeRecord{"y": 1.0, "x": 1.0, "g": 3.0}, true},
		"cluster_null":    {cluster, fakeRecord{"y": 1.0, "x": 1.0}, false},
		"no_cluster_rule": {plain, fakeRecord{"y": 1.0, "x": 1.0}, true},
	} {
		if got := tc.m.AdmitRow(tc.rec, pmodel.AdmitToken{}); got != tc.want {
			t.Errorf("%s: AdmitRow = %v, want %v", name, got, tc.want)
		}
	}
}
