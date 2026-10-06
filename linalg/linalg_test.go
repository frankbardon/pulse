package linalg_test

import (
	stderrors "errors"
	"math"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

func codeOf(t *testing.T, err error) perr.Code {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	return ce.Code
}

func mustSym(t *testing.T, rows [][]float64) *linalg.Sym {
	t.Helper()
	s, err := linalg.NewSymFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	spd3 = [][]float64{
		{4, 12, -16},
		{12, 37, -43},
		{-16, -43, 98},
	}
	// Exactly rank 1: second row is twice the first.
	rankDeficient = [][]float64{
		{1, 2},
		{2, 4},
	}
	indefinite = [][]float64{
		{1, 2},
		{2, 1},
	}
)

func TestMatrixConstructors(t *testing.T) {
	cases := []struct {
		name       string
		rows, cols int
		data       []float64
		wantCode   perr.Code
	}{
		{name: "ok", rows: 2, cols: 3, data: []float64{1, 2, 3, 4, 5, 6}},
		{name: "nil data allocates zeros", rows: 2, cols: 2},
		{name: "empty", rows: 0, cols: 0, data: []float64{}},
		{name: "short data", rows: 2, cols: 2, data: []float64{1, 2, 3}, wantCode: perr.PULSE_MATRIX_SHAPE_MISMATCH},
		{name: "negative dim", rows: -1, cols: 2, wantCode: perr.PULSE_MATRIX_SHAPE_MISMATCH},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := linalg.NewMatrix(tc.rows, tc.cols, tc.data)
			if tc.wantCode != "" {
				if err == nil {
					t.Fatal("expected an error")
				}
				if got := codeOf(t, err); got != tc.wantCode {
					t.Fatalf("code %s, want %s", got, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Rows() != tc.rows || m.Cols() != tc.cols {
				t.Fatalf("shape %dx%d, want %dx%d", m.Rows(), m.Cols(), tc.rows, tc.cols)
			}
			for i, v := range m.RowMajor() {
				want := 0.0
				if tc.data != nil {
					want = tc.data[i]
				}
				if v != want {
					t.Fatalf("element %d = %v, want %v", i, v, want)
				}
			}
		})
	}

	data := []float64{1, 2, 3, 4, 5, 6}
	m, _ := linalg.NewMatrix(2, 3, data)
	data[0] = 99
	if m.At(0, 0) != 1 {
		t.Fatal("NewMatrix must copy its data")
	}
	m.Set(1, 2, 7)
	if got := m.ToRows(); got[1][2] != 7 || got[0][1] != 2 {
		t.Fatalf("ToRows = %v", got)
	}

	if _, err := linalg.NewMatrixFromRows([][]float64{{1, 2}, {3}}); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("ragged rows must be a shape mismatch")
	}
	e, err := linalg.NewMatrixFromRows(nil)
	if err != nil || e.Rows() != 0 || e.Cols() != 0 {
		t.Fatalf("empty rows: %v %v", e, err)
	}
	r, err := linalg.NewMatrixFromRows([][]float64{{1, 2}, {3, 4}, {5, 6}})
	if err != nil || r.Rows() != 3 || r.Cols() != 2 || r.At(2, 1) != 6 {
		t.Fatalf("NewMatrixFromRows: %v %v", r, err)
	}
}

func TestMatrixAtPanicsOutOfRange(t *testing.T) {
	m, _ := linalg.NewMatrix(2, 2, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("At out of range must panic")
		}
	}()
	m.At(2, 0)
}

func TestSymConstructors(t *testing.T) {
	s, err := linalg.NewSym(3, []float64{1, 2, 3, 4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]float64{{1, 2, 3}, {2, 4, 5}, {3, 5, 6}}
	for i := range want {
		for j := range want[i] {
			if s.At(i, j) != want[i][j] {
				t.Fatalf("At(%d,%d) = %v, want %v", i, j, s.At(i, j), want[i][j])
			}
		}
	}
	s.Set(2, 0, 9)
	if s.At(0, 2) != 9 || s.ToRows()[2][0] != 9 {
		t.Fatal("Set(i,j) must set (j,i)")
	}
	if s.N() != 3 {
		t.Fatalf("N = %d", s.N())
	}

	cases := []struct {
		name string
		fn   func() error
	}{
		{"packed length", func() error { _, err := linalg.NewSym(3, []float64{1, 2}); return err }},
		{"negative order", func() error { _, err := linalg.NewSym(-1, nil); return err }},
		{"non-square rows", func() error { _, err := linalg.NewSymFromRows([][]float64{{1, 2, 3}, {4, 5, 6}}); return err }},
		{"ragged rows", func() error { _, err := linalg.NewSymFromRows([][]float64{{1, 2}, {3}}); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if err == nil || codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
				t.Fatalf("want PULSE_MATRIX_SHAPE_MISMATCH, got %v", err)
			}
		})
	}

	// NewSymFromRows reads the LOWER triangle only.
	l := mustSym(t, [][]float64{{1, 100}, {2, 3}})
	if l.At(0, 1) != 2 {
		t.Fatalf("NewSymFromRows must read the lower triangle, got %v", l.At(0, 1))
	}
	z, err := linalg.NewSym(2, nil)
	if err != nil || z.At(1, 1) != 0 {
		t.Fatal("NewSym(n, nil) must allocate zeros")
	}
}

func TestSymAtPanicsOutOfRange(t *testing.T) {
	s, _ := linalg.NewSym(2, nil)
	defer func() {
		if recover() == nil {
			t.Fatal("At out of range must panic")
		}
	}()
	s.At(0, 2)
}

func TestVec(t *testing.T) {
	src := []float64{1, 2, 3}
	v := linalg.NewVec(src)
	src[0] = 9
	if v.At(0) != 1 || v.Len() != 3 {
		t.Fatal("NewVec must copy")
	}
	v.Set(2, 5)
	if got := v.Slice(); got[2] != 5 {
		t.Fatalf("Slice = %v", got)
	}
}

func TestCholesky(t *testing.T) {
	cases := []struct {
		name     string
		in       *linalg.Sym
		want     [][]float64
		wantCode perr.Code
	}{
		{name: "spd", in: mustSym(t, spd3), want: [][]float64{{2, 0, 0}, {6, 1, 0}, {-8, 5, 3}}},
		{name: "empty", in: mustSym(t, nil), want: [][]float64{}},
		{name: "rank deficient", in: mustSym(t, rankDeficient), wantCode: perr.PULSE_MATRIX_SINGULAR},
		{name: "indefinite", in: mustSym(t, indefinite), wantCode: perr.PULSE_MATRIX_SINGULAR},
		{name: "nil", in: nil, wantCode: perr.PULSE_MATRIX_SHAPE_MISMATCH},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			L, err := linalg.Cholesky(tc.in)
			if tc.wantCode != "" {
				if err == nil {
					t.Fatal("expected an error")
				}
				if got := codeOf(t, err); got != tc.wantCode {
					t.Fatalf("code %s, want %s", got, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if L.Rows() != len(tc.want) {
				t.Fatalf("order %d, want %d", L.Rows(), len(tc.want))
			}
			for i := range tc.want {
				for j := range tc.want[i] {
					if L.At(i, j) != tc.want[i][j] {
						t.Fatalf("L[%d][%d] = %v, want %v", i, j, L.At(i, j), tc.want[i][j])
					}
				}
			}
		})
	}
}

func TestCholeskySingularDetailsPivot(t *testing.T) {
	_, err := linalg.Cholesky(mustSym(t, rankDeficient))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Details["pivot"] != 1 {
		t.Fatalf("want pivot 1 in details, got %v", err)
	}
}

func TestDefaultRidgeSchedule(t *testing.T) {
	s := linalg.DefaultRidgeSchedule()
	want := []float64{1e-6, 1e-5, 1e-4, 1e-3, 1e-2, 1e-1, 1, 10}
	if len(s) != 8 {
		t.Fatalf("len %d, want 8", len(s))
	}
	for i := range want {
		if s[i] != math.Pow(10, float64(i-6)) || s[i] != want[i] {
			t.Fatalf("schedule[%d] = %v, want %v", i, s[i], want[i])
		}
	}
	s[0] = 42
	if linalg.DefaultRidgeSchedule()[0] != 1e-6 {
		t.Fatal("DefaultRidgeSchedule must return a fresh copy")
	}
}

func TestCholeskyRidge(t *testing.T) {
	cases := []struct {
		name      string
		in        *linalg.Sym
		schedule  linalg.RidgeSchedule
		wantRidge float64
		wantCode  perr.Code
	}{
		{name: "spd needs no ridge", in: mustSym(t, spd3), wantRidge: 0},
		{name: "rank deficient ridged once", in: mustSym(t, rankDeficient), wantRidge: 1e-6},
		{name: "custom schedule", in: mustSym(t, rankDeficient), schedule: linalg.RidgeSchedule{0.5, 0.25}, wantRidge: 0.5},
		{name: "empty", in: mustSym(t, nil), wantRidge: 0},
		{name: "exhausted", in: mustSym(t, [][]float64{{-100}}), schedule: linalg.RidgeSchedule{1, 2},
			wantRidge: 3, wantCode: perr.PULSE_MATRIX_SINGULAR},
		{name: "nil", in: nil, wantCode: perr.PULSE_MATRIX_SHAPE_MISMATCH},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var before [][]float64
			if tc.in != nil {
				before = tc.in.ToRows()
			}
			L, ridge, err := linalg.CholeskyRidge(tc.in, tc.schedule)
			if ridge != tc.wantRidge {
				t.Fatalf("ridge %v, want %v", ridge, tc.wantRidge)
			}
			if tc.wantCode != "" {
				if err == nil || codeOf(t, err) != tc.wantCode {
					t.Fatalf("want %s, got %v", tc.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if L.Rows() != tc.in.N() {
				t.Fatalf("factor order %d, want %d", L.Rows(), tc.in.N())
			}
			after := tc.in.ToRows()
			for i := range before {
				for j := range before[i] {
					if before[i][j] != after[i][j] {
						t.Fatal("CholeskyRidge must not modify its input")
					}
				}
			}
		})
	}
}

func TestSolveSPD(t *testing.T) {
	// spd3 · [1, 2, 3] = [-20, -43, 192]
	s := mustSym(t, spd3)
	x, err := linalg.SolveSPD(s, linalg.NewVec([]float64{-20, -43, 192}))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{1, 2, 3} {
		if math.Abs(x.At(i)-want) > 1e-12 {
			t.Fatalf("x[%d] = %v, want %v", i, x.At(i), want)
		}
	}

	cases := []struct {
		name     string
		s        *linalg.Sym
		b        *linalg.Vec
		wantCode perr.Code
	}{
		{"length mismatch", s, linalg.NewVec([]float64{1, 2}), perr.PULSE_MATRIX_SHAPE_MISMATCH},
		{"nil matrix", nil, linalg.NewVec(nil), perr.PULSE_MATRIX_SHAPE_MISMATCH},
		{"nil vector", s, nil, perr.PULSE_MATRIX_SHAPE_MISMATCH},
		{"rank deficient", mustSym(t, rankDeficient), linalg.NewVec([]float64{1, 2}), perr.PULSE_MATRIX_SINGULAR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := linalg.SolveSPD(tc.s, tc.b)
			if err == nil || codeOf(t, err) != tc.wantCode {
				t.Fatalf("want %s, got %v", tc.wantCode, err)
			}
		})
	}

	e, err := linalg.SolveSPD(mustSym(t, nil), linalg.NewVec(nil))
	if err != nil || e.Len() != 0 {
		t.Fatalf("empty solve: %v %v", e, err)
	}
}

func TestInverseSPD(t *testing.T) {
	s := mustSym(t, spd3)
	inv, err := linalg.InverseSPD(s)
	if err != nil {
		t.Fatal(err)
	}
	full := s.ToRows()
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			sum := 0.0
			for k := 0; k < 3; k++ {
				sum += full[i][k] * inv.At(k, j)
			}
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(sum-want) > 1e-9 {
				t.Fatalf("(S·S⁻¹)[%d][%d] = %v, want %v", i, j, sum, want)
			}
		}
	}

	for _, tc := range []struct {
		name     string
		in       *linalg.Sym
		wantCode perr.Code
	}{
		{"nil", nil, perr.PULSE_MATRIX_SHAPE_MISMATCH},
		{"rank deficient", mustSym(t, rankDeficient), perr.PULSE_MATRIX_SINGULAR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := linalg.InverseSPD(tc.in)
			if err == nil || codeOf(t, err) != tc.wantCode {
				t.Fatalf("want %s, got %v", tc.wantCode, err)
			}
		})
	}

	e, err := linalg.InverseSPD(mustSym(t, nil))
	if err != nil || e.N() != 0 {
		t.Fatalf("empty inverse: %v %v", e, err)
	}
}

func TestRankTolerance(t *testing.T) {
	if linalg.Epsilon != math.Nextafter(1, 2)-1 {
		t.Fatalf("Epsilon = %v, want float64 machine epsilon", linalg.Epsilon)
	}
	cases := []struct {
		rows, cols int
		sigma      float64
		want       float64
	}{
		{3, 5, 2, 5 * linalg.Epsilon * 2},
		{7, 2, 1, 7 * linalg.Epsilon},
		{0, 0, 10, 0},
	}
	for _, tc := range cases {
		if got := linalg.RankTolerance(tc.rows, tc.cols, tc.sigma); got != tc.want {
			t.Fatalf("RankTolerance(%d,%d,%v) = %v, want %v", tc.rows, tc.cols, tc.sigma, got, tc.want)
		}
	}
}
