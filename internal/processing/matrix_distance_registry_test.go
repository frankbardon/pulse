package processing

import (
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/metric"
)

// zeroSpreadRows is three buckets over x1, x2 and a constant x3: under
// standardize x3 has no z-score.
func zeroSpreadRows() []map[string]float64 {
	return []map[string]float64{
		{"x1": 1, "x2": 2, "x3": 5, "g": 1, "w": 1}, {"x1": 3, "x2": 1, "x3": 5, "g": 1, "w": 1},
		{"x1": 6, "x2": 8, "x3": 5, "g": 2, "w": 1}, {"x1": 7, "x2": 9, "x3": 5, "g": 2, "w": 1},
		{"x1": -2, "x2": 4, "x3": 5, "g": 3, "w": 1}, {"x1": -1, "x2": 6, "x3": 5, "g": 3, "w": 1},
	}
}

// TestMatrixDistance_ZeroSpreadMemberDroppedPairwise: a zero-spread
// member has no z-score under standardize — a MISSING coordinate, not a
// whole-pair null. The metrics read the remaining members pairwise
// (the registry's policy): the Scaled sums rescaled by p/p_observed
// (R stats::dist()), chebyshev and cosine over the complete pairs; the
// PULSE_MATRIX_ZERO_VARIANCE warning still names the member.
func TestMatrixDistance_ZeroSpreadMemberDroppedPairwise(t *testing.T) {
	t.Parallel()

	schema, _ := distanceSchema(3)
	spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "euclidean", "standardize": true}`)
	spec.Fields = []string{"x1", "x2", "x3"}
	res, _ := runDistance(t, schema, spec, zeroSpreadRows(), nil)
	if w := findDistanceWarning(res, errors.PULSE_MATRIX_ZERO_VARIANCE); w == nil || fmt.Sprint(w.Details["members"]) != "[x3]" {
		t.Fatalf("warnings %+v", res.Warnings)
	}
	z := res.Auxiliary["centroids_standardized"].Values
	for i := range z {
		if !math.IsNaN(z[i][2]) {
			t.Fatalf("centroid %d standardized x3 = %v, want NaN (no z-score)", i, z[i][2])
		}
	}
	for a := 0; a < 3; a++ {
		for b := 0; b < 3; b++ {
			d1, d2 := z[a][0]-z[b][0], z[a][1]-z[b][1]
			want := math.Sqrt((float64(d1*d1) + float64(d2*d2)) / (2.0 / 3.0))
			if a == b {
				want = 0
			}
			distNear(t, fmt.Sprintf("d(%d,%d)", a, b), res.Primary.Values[a][b], want)
			if math.IsNaN(res.Primary.Values[a][b]) {
				t.Errorf("d(%d,%d) is null; a partial pair must not be", a, b)
			}
		}
	}
	// Every accepted metric with a signed domain gives a defined value.
	for _, name := range []string{"sq_euclidean", "manhattan", "chebyshev", "cosine"} {
		spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "` + name + `", "standardize": true}`)
		spec.Fields = []string{"x1", "x2", "x3"}
		got, _ := runDistance(t, schema, spec, zeroSpreadRows(), nil)
		m, _ := metric.Lookup(name)
		want, _ := m.Eval(metric.Distance, z[0], z[1], nil)
		if got.Primary.Values[0][1] != want || math.IsNaN(want) {
			t.Errorf("%s d(1,2) = %v, registry %v", name, got.Primary.Values[0][1], want)
		}
	}
}

// TestMatrixDistance_MinValid: params.min_valid is the fewest complete
// member pairs a distance needs; below it every cell — the diagonal too
// — is null.
func TestMatrixDistance_MinValid(t *testing.T) {
	t.Parallel()

	schema, _ := distanceSchema(3)
	for _, c := range []struct {
		minValid string
		null     bool
	}{{"1", false}, {"2", false}, {"3", true}} {
		spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "standardize": true, "min_valid": ` + c.minValid + `}`)
		spec.Fields = []string{"x1", "x2", "x3"}
		res, _ := runDistance(t, schema, spec, zeroSpreadRows(), nil)
		for a, row := range res.Primary.Values {
			for b, v := range row {
				if math.IsNaN(v) != c.null {
					t.Errorf("min_valid %s: d(%d,%d) = %v, null want %v", c.minValid, a, b, v, c.null)
				}
			}
		}
	}
	// Without standardize every coordinate is defined: min_valid = p
	// changes nothing.
	spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "min_valid": 3}`)
	spec.Fields = []string{"x1", "x2", "x3"}
	res, _ := runDistance(t, schema, spec, zeroSpreadRows(), nil)
	distNear(t, "d(1,2)", res.Primary.Values[0][1], math.Hypot(4.5, 7))
}

// TestMatrixDistance_MetricDomain: hellinger and aitchison read
// non-negative centroids. A negative coordinate (or an aitchison zero
// part) nulls every distance touching that centroid — the diagonal
// included — and raises one PULSE_MATRIX_METRIC_DOMAIN warning naming
// the metric, the refused pairs, their buckets and the reasons; the
// other distances are the registry's.
func TestMatrixDistance_MetricDomain(t *testing.T) {
	t.Parallel()

	schema, _ := distanceSchema(2)
	rows, nulls := distanceRows() // bucket 3's centroid is (-1.5, 5)
	for _, c := range []struct {
		metric, reason string
	}{{"hellinger", metric.ReasonNegative}, {"aitchison", metric.ReasonNegative}} {
		res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "`+c.metric+`"}`), rows, nulls)
		w := findDistanceWarning(res, errors.PULSE_MATRIX_METRIC_DOMAIN)
		if w == nil || w.Details["metric"] != c.metric || w.Details["pairs"] != 3 ||
			fmt.Sprint(w.Details["buckets"]) != "[1 2 3]" || fmt.Sprint(w.Details["reasons"]) != "["+c.reason+"]" {
			t.Fatalf("%s warnings %+v", c.metric, res.Warnings)
		}
		v := res.Primary.Values
		if !math.IsNaN(v[0][2]) || !math.IsNaN(v[1][2]) || !math.IsNaN(v[2][2]) || v[0][0] != 0 || math.IsNaN(v[0][1]) {
			t.Errorf("%s primary %v: bucket 3 null, the rest defined", c.metric, v)
		}
		m, _ := metric.Lookup(c.metric)
		want, _ := m.Eval(metric.Distance, []float64{2, 1.5}, []float64{6.5, 8.5}, nil)
		distNear(t, c.metric+" d(1,2)", v[0][1], want)
	}
	zero := []map[string]float64{
		{"x1": 0, "x2": 2, "g": 1, "w": 1}, {"x1": 0, "x2": 4, "g": 1, "w": 1},
		{"x1": 1, "x2": 1, "g": 2, "w": 1}, {"x1": 3, "x2": 2, "g": 3, "w": 1},
	}
	res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "aitchison"}`), zero, nil)
	if w := findDistanceWarning(res, errors.PULSE_MATRIX_METRIC_DOMAIN); w == nil || fmt.Sprint(w.Details["reasons"]) != "[zero]" {
		t.Fatalf("aitchison zero part warnings %+v", res.Warnings)
	}
	if !math.IsNaN(res.Primary.Values[0][1]) || math.IsNaN(res.Primary.Values[1][2]) {
		t.Errorf("aitchison primary %v", res.Primary.Values)
	}
	// hellinger reads a zero part fine (it is a valid share).
	if res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "hellinger"}`), zero, nil); len(res.Warnings) != 0 || math.IsNaN(res.Primary.Values[0][1]) {
		t.Errorf("hellinger over a zero part %+v", res)
	}
}

// TestMatrixDistance_MahalanobisIsRegistry: mahalanobis keeps its
// pooled-covariance path and its value is the registry's quadratic
// form over the raw centroids, bit for bit.
func TestMatrixDistance_MahalanobisIsRegistry(t *testing.T) {
	t.Parallel()

	schema, _ := distanceSchema(2)
	rows, nulls := distanceRows()
	rows = append(rows, map[string]float64{"x1": 2, "x2": 3, "g": 1, "w": 1}, map[string]float64{"x1": 5, "x2": 9, "g": 2, "w": 1})
	nulls = append(nulls, nil, nil)
	res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "mahalanobis", "standardize": true}`), rows, nulls)
	pc := res.Auxiliary["pooled_covariance"]
	if pc == nil || len(res.Warnings) != 0 {
		t.Fatalf("result %+v", res)
	}
	cents := res.Auxiliary["centroids"].Values
	inv := pooledInverseFromValues(t, pc.Values)
	m, _ := metric.Lookup("mahalanobis")
	want, err := m.Eval(metric.Distance, cents[0], cents[2], &metric.Stats{InverseCovariance: inv})
	if err != nil || res.Primary.Values[0][2] != want {
		t.Errorf("d(1,3) = %v, registry %v (%v)", res.Primary.Values[0][2], want, err)
	}
}

// pooledInverseFromValues inverts a rendered full covariance through
// the reference SPD inverse the engine uses.
func pooledInverseFromValues(t *testing.T, rows [][]float64) *linalg.Sym {
	t.Helper()
	s, err := linalg.NewSym(len(rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		for j := i; j < len(rows); j++ {
			s.Set(i, j, rows[i][j])
		}
	}
	inv, err := linalg.InverseSPD(s)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// TestStandardizeCentroid: a member with zero, undefined or infinite
// spread has no z-score (NaN) even when a rounding residual leaves the
// centroid off the mean — never ±Inf, which the registry would read as
// an ordinary value.
func TestStandardizeCentroid(t *testing.T) {
	t.Parallel()

	c := []float64{3, 0.30000000000000004, 1, 2}
	mean := []float64{1, 0.3, 1, 2}
	sd := []float64{2, 0, math.NaN(), math.Inf(1)}
	z := standardizeCentroid(c, mean, sd)
	if z[0] != 1 {
		t.Errorf("z[0] = %v, want 1", z[0])
	}
	for j := 1; j < 4; j++ {
		if !math.IsNaN(z[j]) {
			t.Errorf("z[%d] = %v, want NaN (no spread)", j, z[j])
		}
	}
}
