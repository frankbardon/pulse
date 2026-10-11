package metric_test

import (
	stderrors "errors"
	"math"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/metric"
)

func mustLookup(t *testing.T, name string) metric.Metric {
	t.Helper()
	m, ok := metric.Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) failed", name)
	}
	return m
}

// TestEval_NaNPolicy pins the one pairwise-complete policy: zero
// overlap is NaN with no error, a complete pair set is never rescaled,
// infinities are ordinary values.
func TestEval_NaNPolicy(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	sd := &metric.Stats{SD: []float64{1, 1}}
	masses := &metric.Stats{ColumnMasses: []float64{0.5, 0.5}}
	inv, _ := linalg.NewSymFromRows([][]float64{{1, 0}, {0, 1}})
	cov := &metric.Stats{InverseCovariance: inv}
	cases := []struct {
		name string
		m    string
		f    metric.Form
		a, b []float64
		s    *metric.Stats
		want float64 // NaN means "want NaN"
	}{
		{"zero overlap euclidean", "euclidean", dist, []float64{nan, 1}, []float64{2, nan}, nil, nan},
		{"zero overlap manhattan", "manhattan", dist, []float64{nan}, []float64{2}, nil, nan},
		{"zero overlap chebyshev", "chebyshev", dist, []float64{nan}, []float64{nan}, nil, nan},
		{"zero overlap sq", "sq_euclidean", dist, []float64{nan}, []float64{nan}, nil, nan},
		{"zero overlap cosine", "cosine", sim, []float64{nan}, []float64{1}, nil, nan},
		{"zero overlap correlation", "correlation", sim, []float64{nan}, []float64{1}, nil, nan},
		{"zero overlap hellinger", "hellinger", dist, []float64{nan}, []float64{1}, nil, nan},
		{"zero overlap aitchison", "aitchison", dist, []float64{nan}, []float64{1}, nil, nan},
		{"zero overlap jaccard", "jaccard", sim, []float64{nan}, []float64{1}, nil, nan},
		{"zero overlap hamming", "hamming", dist, []float64{nan}, []float64{1}, nil, nan},
		{"zero overlap std", "std_euclidean", dist, []float64{nan, nan}, []float64{1, 1}, sd, nan},
		{"zero overlap chi", "chi_square", dist, []float64{nan, nan}, []float64{1, 1}, masses, nan},
		{"empty vectors", "euclidean", dist, []float64{}, []float64{}, nil, nan},
		{"mahalanobis partial is NaN", "mahalanobis", dist, []float64{1, nan}, []float64{2, 3}, cov, nan},
		{"mahalanobis complete", "mahalanobis", dist, []float64{0, 0}, []float64{3, 4}, cov, 5},
		{"mahalanobis empty", "mahalanobis", dist, []float64{}, []float64{}, &metric.Stats{InverseCovariance: mustSym(t, nil)}, nan},
		{"std partial rescaled", "std_euclidean", dist, []float64{0, nan}, []float64{2, 9}, &metric.Stats{SD: []float64{2, 1}}, math.Sqrt(2)},
		{"chebyshev not rescaled", "chebyshev", dist, []float64{0, nan, 1}, []float64{3, 9, 1}, nil, 3},
		{"complete never rescaled", "sq_euclidean", dist, []float64{0, 0}, []float64{1, 2}, nil, 5},
		{"inf is ordinary", "manhattan", dist, []float64{inf, 0}, []float64{0, 0}, nil, inf},
		{"cosine zero norm", "cosine", sim, []float64{0, 0}, []float64{1, 2}, nil, nan},
		{"correlation zero variance", "correlation", sim, []float64{3, 3, 3}, []float64{1, 2, 3}, nil, nan},
		{"hellinger zero total", "hellinger", dist, []float64{0, 0}, []float64{1, 2}, nil, nan},
		{"hellinger identical", "hellinger", dist, []float64{1, 3}, []float64{2, 6}, nil, 0},
		{"hellinger disjoint", "hellinger", dist, []float64{1, 0}, []float64{0, 1}, nil, 1},
		{"aitchison scale invariant", "aitchison", dist, []float64{1, 2, 3}, []float64{10, 20, 30}, nil, 0},
		{"chi zero total", "chi_square", dist, []float64{0, 0}, []float64{1, 1}, masses, nan},
		{"jaccard two empties", "jaccard", sim, []float64{0, 0}, []float64{0, 0}, nil, nan},
		{"dice", "dice", sim, []float64{1, 1, 0}, []float64{1, 0, 1}, nil, 0.5},
		{"overlap", "overlap", sim, []float64{1, 1, 0}, []float64{1, 0, 0}, nil, 1},
		{"overlap empty side", "overlap", sim, []float64{0, 0}, []float64{1, 0}, nil, nan},
		{"hamming complete count", "hamming", dist, []float64{1, 0, 1}, []float64{0, 0, 0}, nil, 2},
	}
	for _, tc := range cases {
		got, err := mustLookup(t, tc.m).Eval(tc.f, tc.a, tc.b, tc.s)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if math.IsNaN(tc.want) {
			if !math.IsNaN(got) {
				t.Errorf("%s: got %v, want NaN", tc.name, got)
			}
			continue
		}
		if !near(got, tc.want, 1e-15) {
			t.Errorf("%s: got %.17g, want %.17g", tc.name, got, tc.want)
		}
	}
}

func mustSym(t *testing.T, rows [][]float64) *linalg.Sym {
	t.Helper()
	s, err := linalg.NewSymFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestEval_CosineDistanceClampedAndOpposite: cosine distance is 1 − cos
// in [0, 2]; the similarity stays a similarity.
func TestEval_CosineDistanceClampedAndOpposite(t *testing.T) {
	m := mustLookup(t, "cosine")
	cases := []struct {
		a, b      []float64
		sim, dist float64
	}{
		{[]float64{1, 2}, []float64{2, 4}, 1, 0},
		{[]float64{1, 0}, []float64{0, 1}, 0, 1},
		{[]float64{1, 2}, []float64{-1, -2}, -1, 2},
		// Rounding pushes cos above 1; the distance clamps to 0.
		{[]float64{0.1, 0.2, 0.3}, []float64{0.1, 0.2, 0.3}, 1, 0},
	}
	for _, tc := range cases {
		s, _ := m.Eval(metric.Similarity, tc.a, tc.b, nil)
		d, _ := m.Eval(metric.Distance, tc.a, tc.b, nil)
		if !near(s, tc.sim, 1e-15) {
			t.Errorf("cosine sim(%v, %v) = %.17g, want %v", tc.a, tc.b, s, tc.sim)
		}
		if d < 0 || d > 2 || !near(d, tc.dist, 1e-15) {
			t.Errorf("cosine dist(%v, %v) = %.17g, want %v in [0,2]", tc.a, tc.b, d, tc.dist)
		}
	}
	// Rounding makes this vector's self-cosine 1.0000000000000002: the
	// similarity is reported as computed, the distance clamps to 0.
	a := []float64{0.4377141871869802, 0.4246374970712657, 0.6868230728671094}
	if s, _ := m.Eval(metric.Similarity, a, a, nil); s <= 1 {
		t.Fatalf("fixture no longer rounds above 1 (cos = %.17g)", s)
	}
	if d, _ := m.Eval(metric.Distance, a, a, nil); d != 0 {
		t.Errorf("self distance = %.17g, want 0 (clamped)", d)
	}
	neg := []float64{-a[0], -a[1], -a[2]}
	if d, _ := m.Eval(metric.Distance, a, neg, nil); d != 2 {
		t.Errorf("opposite distance = %.17g, want 2 (clamped)", d)
	}
}

// TestEval_Errors covers every refusal path of Eval: wrong form, length
// mismatch, domain violations and fitted statistics.
func TestEval_Errors(t *testing.T) {
	nan := math.NaN()
	inv2 := mustSym(t, [][]float64{{1, 0}, {0, 1}})
	cases := []struct {
		name     string
		m        string
		f        metric.Form
		a, b     []float64
		s        *metric.Stats
		sentinel error
		reason   string
		index    int
	}{
		{"length mismatch", "euclidean", dist, []float64{1}, []float64{1, 2}, nil, metric.ErrLengthMismatch, "", 0},
		{"length mismatch sets", "jaccard", sim, []float64{1}, []float64{}, nil, metric.ErrLengthMismatch, "", 0},
		{"hellinger negative", "hellinger", dist, []float64{1, -1}, []float64{1, 1}, nil, metric.ErrDomain, metric.ReasonNegative, 1},
		{"aitchison negative b", "aitchison", dist, []float64{1, 1}, []float64{1, -2}, nil, metric.ErrDomain, metric.ReasonNegative, 1},
		{"aitchison zero", "aitchison", dist, []float64{1, 0, 2}, []float64{1, 1, 1}, nil, metric.ErrDomain, metric.ReasonZero, 1},
		{"aitchison zero b", "aitchison", dist, []float64{1, 1}, []float64{0, 1}, nil, metric.ErrDomain, metric.ReasonZero, 0},
		{"chi negative", "chi_square", dist, []float64{1, -1}, []float64{1, 1}, &metric.Stats{ColumnMasses: []float64{1, 1}}, metric.ErrDomain, metric.ReasonNegative, 1},
		{"set not binary", "hamming", dist, []float64{1, 2}, []float64{1, 0}, nil, metric.ErrDomain, metric.ReasonNotBinary, 1},
		{"set not binary b", "jaccard", sim, []float64{1, 0}, []float64{0.5, 0}, nil, metric.ErrDomain, metric.ReasonNotBinary, 0},
		{"std missing", "std_euclidean", dist, []float64{1}, []float64{2}, nil, metric.ErrStats, metric.ReasonMissing, -1},
		{"std nil sd", "std_euclidean", dist, []float64{1}, []float64{2}, &metric.Stats{}, metric.ErrStats, metric.ReasonMissing, -1},
		{"std length", "std_euclidean", dist, []float64{1}, []float64{2}, &metric.Stats{SD: []float64{1, 1}}, metric.ErrStats, metric.ReasonLength, -1},
		{"std zero sd", "std_euclidean", dist, []float64{1, 1}, []float64{2, 2}, &metric.Stats{SD: []float64{1, 0}}, metric.ErrStats, metric.ReasonInvalid, 1},
		{"std nan sd", "std_euclidean", dist, []float64{1}, []float64{2}, &metric.Stats{SD: []float64{nan}}, metric.ErrStats, metric.ReasonInvalid, 0},
		{"std inf sd", "std_euclidean", dist, []float64{1}, []float64{2}, &metric.Stats{SD: []float64{math.Inf(1)}}, metric.ErrStats, metric.ReasonInvalid, 0},
		{"chi missing", "chi_square", dist, []float64{1}, []float64{2}, nil, metric.ErrStats, metric.ReasonMissing, -1},
		{"chi negative mass", "chi_square", dist, []float64{1}, []float64{2}, &metric.Stats{ColumnMasses: []float64{-1}}, metric.ErrStats, metric.ReasonInvalid, 0},
		{"mahalanobis missing", "mahalanobis", dist, []float64{1, 2}, []float64{2, 1}, nil, metric.ErrStats, metric.ReasonMissing, -1},
		{"mahalanobis nil inv", "mahalanobis", dist, []float64{1, 2}, []float64{2, 1}, &metric.Stats{}, metric.ErrStats, metric.ReasonMissing, -1},
		{"mahalanobis order", "mahalanobis", dist, []float64{1, 2, 3}, []float64{2, 1, 3}, &metric.Stats{InverseCovariance: inv2}, metric.ErrStats, metric.ReasonLength, -1},
	}
	for _, tc := range cases {
		got, err := mustLookup(t, tc.m).Eval(tc.f, tc.a, tc.b, tc.s)
		if !math.IsNaN(got) {
			t.Errorf("%s: value %v beside an error, want NaN", tc.name, got)
		}
		if !stderrors.Is(err, tc.sentinel) {
			t.Errorf("%s: err %v, want %v", tc.name, err, tc.sentinel)
			continue
		}
		if err.Error() == "" {
			t.Errorf("%s: empty message", tc.name)
		}
		var de *metric.DomainError
		if stderrors.As(err, &de) && (de.Reason != tc.reason || de.Index != tc.index || de.Metric != tc.m) {
			t.Errorf("%s: %+v, want reason %s index %d", tc.name, de, tc.reason, tc.index)
		}
		var se *metric.StatsError
		if stderrors.As(err, &se) {
			if se.Reason != tc.reason || se.Index != tc.index || se.Metric != tc.m {
				t.Errorf("%s: %+v, want reason %s index %d", tc.name, se, tc.reason, tc.index)
			}
			if want := mustLookup(t, tc.m).Fit(); se.Stat != want {
				t.Errorf("%s: stat %s, want %s", tc.name, se.Stat, want)
			}
		}
		var le *metric.LengthMismatchError
		if stderrors.As(err, &le) && le.Left == le.Right {
			t.Errorf("%s: %+v", tc.name, le)
		}
	}
	// A missing element is dropped before the domain check.
	if v, err := mustLookup(t, "aitchison").Eval(dist, []float64{nan, 1, 2}, []float64{0, 2, 4}, nil); err != nil || !near(v, 0, 0) {
		t.Errorf("aitchison over complete pairs = %v, %v", v, err)
	}
	if v, err := mustLookup(t, "hamming").Eval(dist, []float64{nan, 1}, []float64{7, 0}, nil); err != nil || v != 2 {
		t.Errorf("hamming skips a missing non-binary element: %v, %v", v, err)
	}
}

// TestEval_WrongFormRefused: Eval in a form the metric lacks is
// PULSE_VECTOR_METRIC_UNKNOWN, never a converted value.
func TestEval_WrongFormRefused(t *testing.T) {
	for _, m := range metric.All() {
		for _, f := range []metric.Form{sim, dist} {
			if m.HasForm(f) {
				continue
			}
			v, err := m.Eval(f, []float64{1}, []float64{1}, nil)
			if !math.IsNaN(v) {
				t.Errorf("%s in %s returned %v", m.Name(), f, v)
			}
			if ce := codeOf(t, err); ce.Code != perr.PULSE_VECTOR_METRIC_UNKNOWN || ce.Details["reason"] != "no_form" {
				t.Errorf("%s in %s: %s %v", m.Name(), f, ce.Code, ce.Details["reason"])
			}
		}
	}
}

// TestEvalSets pins the count kernels and their refusals.
func TestEvalSets(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		m    string
		f    metric.Form
		c    metric.SetCounts
		want float64
	}{
		{"jaccard", sim, metric.SetCounts{A: 3, B: 2, Inter: 1}, 0.25},
		{"jaccard", sim, metric.SetCounts{}, nan},
		{"dice", sim, metric.SetCounts{A: 3, B: 2, Inter: 1}, 0.4},
		{"dice", sim, metric.SetCounts{}, nan},
		{"overlap", sim, metric.SetCounts{A: 3, B: 2, Inter: 1}, 0.5},
		{"overlap", sim, metric.SetCounts{A: 0, B: 2}, nan},
		{"hamming", dist, metric.SetCounts{A: 3, B: 2, Inter: 1}, 3},
		{"hamming", dist, metric.SetCounts{}, 0},
	}
	for _, tc := range cases {
		got, err := mustLookup(t, tc.m).EvalSets(tc.f, tc.c)
		if err != nil {
			t.Errorf("%s %+v: %v", tc.m, tc.c, err)
			continue
		}
		if math.IsNaN(tc.want) != math.IsNaN(got) || (!math.IsNaN(got) && got != tc.want) {
			t.Errorf("%s %+v = %v, want %v", tc.m, tc.c, got, tc.want)
		}
	}
	for _, bad := range []metric.SetCounts{{A: -1}, {B: -1}, {Inter: -1}, {A: 1, B: 3, Inter: 2}} {
		v, err := mustLookup(t, "jaccard").EvalSets(sim, bad)
		var de *metric.DomainError
		if !math.IsNaN(v) || !stderrors.As(err, &de) || de.Reason != metric.ReasonSetCounts || de.Index != -1 {
			t.Errorf("EvalSets(%+v) = %v, %v", bad, v, err)
		}
	}
	if _, err := mustLookup(t, "euclidean").EvalSets(dist, metric.SetCounts{}); codeOf(t, err).Details["reason"] != "not_accepted" {
		t.Errorf("non-set metric EvalSets: %v", err)
	}
	if _, err := mustLookup(t, "jaccard").EvalSets(dist, metric.SetCounts{}); codeOf(t, err).Details["reason"] != "no_form" {
		t.Errorf("jaccard distance EvalSets: %v", err)
	}
}

// TestObserved counts the complete pairs.
func TestObserved(t *testing.T) {
	nan := math.NaN()
	if n, err := metric.Observed([]float64{1, nan, 3, math.Inf(1)}, []float64{nan, 2, 3, 4}); err != nil || n != 2 {
		t.Errorf("Observed = %d, %v", n, err)
	}
	if _, err := metric.Observed([]float64{1}, nil); !stderrors.Is(err, metric.ErrLengthMismatch) {
		t.Errorf("Observed length mismatch: %v", err)
	}
}
