package linalg_test

import (
	stderrors "errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// symBits reports whether a and b hold the same bits (NaN equal NaN).
func symBitsEqual(a, b *linalg.Sym) bool {
	if a.N() != b.N() {
		return false
	}
	for i := 0; i < a.N(); i++ {
		for j := i; j < a.N(); j++ {
			x, y := a.At(i, j), b.At(i, j)
			if math.Float64bits(x) != math.Float64bits(y) && !(math.IsNaN(x) && math.IsNaN(y)) {
				return false
			}
		}
	}
	return true
}

func vecBitsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float64bits(a[i]) != math.Float64bits(b[i]) && !(math.IsNaN(a[i]) && math.IsNaN(b[i])) {
			return false
		}
	}
	return true
}

// TestCoMomentListwiseStateRoundTrip: a captured state rebuilds an
// accumulator whose every output — counts, sums, Mean, Cov(0/1), Corr,
// NEff, PairM2 — and every later Add / Merge is bit-identical to the
// original's; the captured state is a copy (later folds never move it).
func TestCoMomentListwiseStateRoundTrip(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewPCG(5, 11))
	rows := randomRows(r, 400, 4)
	more := randomRows(r, 50, 4)
	for _, tc := range []struct {
		name string
		rows []wrow
	}{{"weighted", rows}, {"empty", nil}, {"single", []wrow{{[]float64{1, 2, 3, 4}, 2}}}} {
		t.Run(tc.name, func(t *testing.T) {
			orig := feed(t, 4, linalg.Listwise, tc.rows)
			st, err := orig.ListwiseState()
			if err != nil {
				t.Fatal(err)
			}
			back, err := linalg.NewCoMomentFromListwise(st)
			if err != nil {
				t.Fatal(err)
			}
			same := func(stage string, a, b *linalg.CoMoment) {
				t.Helper()
				if a.N() != b.N() || a.NWeightInvalid() != b.NWeightInvalid() || a.P() != b.P() || a.Mode() != b.Mode() ||
					a.PairNMass(0, 0) != b.PairNMass(0, 0) ||
					math.Float64bits(a.W()) != math.Float64bits(b.W()) || math.Float64bits(a.PairW2(0, 0)) != math.Float64bits(b.PairW2(0, 0)) ||
					math.Float64bits(a.NEff()) != math.Float64bits(b.NEff()) {
					t.Fatalf("%s: counts or sums differ", stage)
				}
				if !vecBitsEqual(a.Mean().Slice(), b.Mean().Slice()) || !symBitsEqual(a.Cov(0), b.Cov(0)) ||
					!symBitsEqual(a.Cov(1), b.Cov(1)) || !symBitsEqual(a.Corr(), b.Corr()) {
					t.Fatalf("%s: moments differ", stage)
				}
			}
			same("rebuilt", orig, back)
			for _, row := range more {
				orig.Add(row.x, row.w)
				back.Add(row.x, row.w)
			}
			same("after Add", orig, back)
			other := feed(t, 4, linalg.Listwise, more)
			if err := orig.Merge(other); err != nil {
				t.Fatal(err)
			}
			if err := back.Merge(other); err != nil {
				t.Fatal(err)
			}
			same("after Merge", orig, back)
			again, _ := linalg.NewCoMomentFromListwise(st)
			if len(tc.rows) > 0 && symBitsEqual(again.Cov(0), orig.Cov(0)) {
				t.Fatal("the captured state moved with the original's later folds")
			}
		})
	}
}

// TestCoMomentListwiseStateRefusals: a pairwise accumulator has no
// single state; a malformed state is refused — every refusal
// PULSE_MATRIX_SHAPE_MISMATCH.
func TestCoMomentListwiseStateRefusals(t *testing.T) {
	t.Parallel()

	code := func(err error) errors.Code {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			return ce.Code
		}
		return ""
	}
	if _, err := mustCoMoment(t, 2, linalg.Pairwise).ListwiseState(); code(err) != errors.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatalf("pairwise state: %v", err)
	}
	m2, _ := linalg.NewSymFromRows([][]float64{{1, 0}, {0, 1}})
	good := linalg.ListwiseState{N: 3, NMass: 3, W: 3, W2: 3, Mean: []float64{0, 0}, M2: m2}
	if _, err := linalg.NewCoMomentFromListwise(good); err != nil {
		t.Fatalf("good state: %v", err)
	}
	for name, mut := range map[string]func(*linalg.ListwiseState){
		"nil m2":       func(s *linalg.ListwiseState) { s.M2 = nil },
		"mean length":  func(s *linalg.ListwiseState) { s.Mean = []float64{0} },
		"negative n":   func(s *linalg.ListwiseState) { s.N = -1 },
		"nmass over n": func(s *linalg.ListwiseState) { s.NMass = 4 },
		"nan w":        func(s *linalg.ListwiseState) { s.W = math.NaN() },
		"negative w2":  func(s *linalg.ListwiseState) { s.W2 = -1 },
		"inf w":        func(s *linalg.ListwiseState) { s.W = math.Inf(1) },
	} {
		s := good
		mut(&s)
		if _, err := linalg.NewCoMomentFromListwise(s); code(err) != errors.PULSE_MATRIX_SHAPE_MISMATCH {
			t.Errorf("%s: %v, want PULSE_MATRIX_SHAPE_MISMATCH", name, err)
		}
	}
}
