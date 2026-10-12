package metric_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/metric"
)

// metricOracle holds R's answers, printed by
// scripts/reference/gen_metric.R (Rscript, base stats only). The
// partial_* and binary_* cases are stats::dist() over vectors with
// missing elements: the pairwise-complete p/p_observed rescaling.
var metricOracle = map[string]float64{
	"partial_euclidean":    6.5828058860438334,
	"partial_sq_euclidean": 43.333333333333336,
	"partial_manhattan":    13.333333333333334,
	"partial_chebyshev":    4,
	"complete_euclidean":   6.2449979983983983,
	"complete_manhattan":   11,
	"complete_chebyshev":   5,
	"binary_hamming":       2.7999999999999998,
	"binary_jaccard":       0.5,
	"partial_correlation":  0.96153016922427947,
	"partial_cosine":       0.98550023161074096,
	"trap_cosine":          0.9882352941176471,
	"trap_correlation":     -1,
	"aitchison":            1.0653017912468179,
	"hellinger":            0.3318162917087773,
	"mahalanobis":          1.7320508075688772,
	"std_euclidean":        2.2912878474779199,
	"chi_square":           0.90810394657097371,
}

// TestMetricsMatchROracle compares every kernel with R within 1e-14
// relative (R's C may contract a multiply-add; Pulse never does).
func TestMetricsMatchROracle(t *testing.T) {
	nan := math.NaN()
	x := []float64{1, nan, 3, 4, 7}
	y := []float64{2, 5, nan, 1, 3}
	xc := []float64{1.5, 2, -3, 4}
	yc := []float64{0.5, -1, 2, 2}
	u := []float64{1, 0, 1, nan, 1, 0, 1}
	v := []float64{1, 1, 0, 0, nan, 0, 1}
	xr := []float64{2, 4, nan, 5, 9, 1}
	yr := []float64{1, 3, 8, nan, 7, 2}
	inv, err := linalg.NewSymFromRows([][]float64{{4, 2}, {2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	inv, err = linalg.InverseSPD(inv)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		oracle, metric string
		form           metric.Form
		a, b           []float64
		stats          *metric.Stats
	}{
		{"partial_euclidean", "euclidean", dist, x, y, nil},
		{"partial_sq_euclidean", "sq_euclidean", dist, x, y, nil},
		{"partial_manhattan", "manhattan", dist, x, y, nil},
		{"partial_chebyshev", "chebyshev", dist, x, y, nil},
		{"complete_euclidean", "euclidean", dist, xc, yc, nil},
		{"complete_manhattan", "manhattan", dist, xc, yc, nil},
		{"complete_chebyshev", "chebyshev", dist, xc, yc, nil},
		{"binary_hamming", "hamming", dist, u, v, nil},
		{"binary_jaccard", "jaccard", sim, u, v, nil},
		{"partial_correlation", "correlation", sim, xr, yr, nil},
		{"partial_cosine", "cosine", sim, xr, yr, nil},
		{"trap_cosine", "cosine", sim, []float64{6, 7, 6, 7}, []float64{7, 6, 7, 6}, nil},
		{"trap_correlation", "correlation", sim, []float64{6, 7, 6, 7}, []float64{7, 6, 7, 6}, nil},
		{"aitchison", "aitchison", dist, []float64{0.2, 0.3, 0.5}, []float64{0.1, 0.6, 0.3}, nil},
		{"hellinger", "hellinger", dist, []float64{1, 2, 3, 4}, []float64{4, 3, 2, 1}, nil},
		{"mahalanobis", "mahalanobis", dist, []float64{1, 2}, []float64{3, 1}, &metric.Stats{InverseCovariance: inv}},
		{"std_euclidean", "std_euclidean", dist, []float64{1, 2, 3}, []float64{2, 1, 7}, &metric.Stats{SD: []float64{2, 0.5, 4}}},
		{"chi_square", "chi_square", dist, []float64{10, 20, 30}, []float64{5, 30, 5}, &metric.Stats{ColumnMasses: []float64{0.2, 0.5, 0.3}}},
	}
	for _, tc := range cases {
		m, err := metric.Resolve(tc.metric, tc.form)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.Eval(tc.form, tc.a, tc.b, tc.stats)
		if err != nil {
			t.Errorf("%s: %v", tc.oracle, err)
			continue
		}
		want := metricOracle[tc.oracle]
		if !near(got, want, 1e-14) {
			t.Errorf("%s: %s = %.17g, R %.17g", tc.oracle, tc.metric, got, want)
		}
	}
}

func near(got, want, rel float64) bool {
	if got == want {
		return true
	}
	return math.Abs(got-want) <= rel*math.Max(1, math.Abs(want))
}
