package linalg_test

import (
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// gram returns XᵀX for the columns of x (rows of observations).
func gram(x [][]float64) [][]float64 {
	p := len(x[0])
	out := make([][]float64, p)
	for i := range out {
		out[i] = make([]float64, p)
		for j := range out[i] {
			for _, row := range x {
				out[i][j] += row[i] * row[j]
			}
		}
	}
	return out
}

// singularDetailCase is one PULSE_MATRIX_SINGULAR diagnostics fixture.
type singularDetailCase struct {
	name      string
	rows      [][]float64
	rank      int
	condInf   bool
	dependent []int // nil: no dependent_indices key
	noDiag    bool  // no diagnostics at all (non-finite input)
}

func singularDetailCases() []singularDetailCase {
	// Columns a, b, c = a + b, d independent: the dependency is {a, b, c}.
	obs := [][]float64{
		{1, 2, 3, 0.5}, {2, 1, 3, -1}, {0, 1, 1, 2}, {3, 0, 3, 1}, {1, 1, 2, 0}, {2, 3, 5, -2},
	}
	return []singularDetailCase{
		{name: "collinear battery", rows: gram(obs), rank: 3, condInf: true, dependent: []int{0, 1, 2}},
		{name: "duplicated column", rows: [][]float64{{1, 2}, {2, 4}}, rank: 1, condInf: true, dependent: []int{0, 1}},
		{name: "zero member", rows: [][]float64{{2, 0, 1}, {0, 0, 0}, {1, 0, 2}}, rank: 2, condInf: true, dependent: []int{1}},
		{name: "full-rank indefinite", rows: [][]float64{{1, 3}, {3, 1}}, rank: 2},
		{name: "non-finite", rows: [][]float64{{-1, math.Inf(1)}, {math.Inf(1), 1}}, noDiag: true},
	}
}

func checkSingularDetails(t *testing.T, c singularDetailCase, err error) {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MATRIX_SINGULAR {
		t.Fatalf("want PULSE_MATRIX_SINGULAR, got %v", err)
	}
	if c.noDiag {
		for _, k := range []string{"rank", "condition_number", "dependent_indices"} {
			if _, ok := ce.Details[k]; ok {
				t.Errorf("non-finite input carries %q: %v", k, ce.Details)
			}
		}
		return
	}
	if ce.Details["rank"] != c.rank {
		t.Errorf("rank = %v, want %d", ce.Details["rank"], c.rank)
	}
	cond, ok := ce.Details["condition_number"].(float64)
	if !ok {
		t.Fatalf("condition_number missing: %v", ce.Details)
	}
	if c.condInf != math.IsInf(cond, 1) || math.IsNaN(cond) || cond < 1 {
		t.Errorf("condition_number = %v, want +Inf %v", cond, c.condInf)
	}
	got, has := ce.Details["dependent_indices"]
	if c.dependent == nil {
		if has {
			t.Errorf("dependent_indices = %v, want none", got)
		}
		return
	}
	if !reflect.DeepEqual(got, c.dependent) {
		t.Errorf("dependent_indices = %v, want %v", got, c.dependent)
	}
}

// TestCholesky_SingularDiagnostics: a reference Cholesky failure keeps
// its pivot and adds rank, condition_number and — for a rank-deficient
// matrix — the dependent axis indices; a non-finite matrix gets none.
func TestCholesky_SingularDiagnostics(t *testing.T) {
	for _, c := range singularDetailCases() {
		t.Run(c.name, func(t *testing.T) {
			_, err := linalg.Cholesky(mustSym(t, c.rows))
			checkSingularDetails(t, c, err)
			var ce *perr.CodedError
			stderrors.As(err, &ce)
			if _, ok := ce.Details["pivot"].(int); !ok {
				t.Errorf("pivot missing: %v", ce.Details)
			}
		})
	}
}

// TestFactorSPD_SingularDiagnostics: the gonum-backed factor carries the
// same diagnostics beside its reason and n.
func TestFactorSPD_SingularDiagnostics(t *testing.T) {
	for _, c := range singularDetailCases() {
		t.Run(c.name, func(t *testing.T) {
			_, err := linalg.FactorSPD(mustSym(t, c.rows))
			checkSingularDetails(t, c, err)
			var ce *perr.CodedError
			stderrors.As(err, &ce)
			if ce.Details["reason"] != "not_positive_definite" || ce.Details["n"] != len(c.rows) {
				t.Errorf("reason / n lost: %v", ce.Details)
			}
		})
	}
}
