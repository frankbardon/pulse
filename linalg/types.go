package linalg

import (
	"strconv"

	perr "github.com/frankbardon/pulse/errors"
)

// Matrix is a dense rows×cols matrix of float64 in row-major order.
// The zero value is an empty 0×0 matrix.
type Matrix struct {
	rows, cols int
	data       []float64
}

// NewMatrix returns a rows×cols matrix over a COPY of data, which must
// hold exactly rows*cols values in row-major order. A nil data allocates
// a zero matrix. Negative dimensions or a length mismatch are
// PULSE_MATRIX_SHAPE_MISMATCH.
func NewMatrix(rows, cols int, data []float64) (*Matrix, error) {
	if rows < 0 || cols < 0 {
		return nil, shapeError("matrix dimensions must be non-negative",
			map[string]any{"rows": rows, "cols": cols})
	}
	m := &Matrix{rows: rows, cols: cols, data: make([]float64, rows*cols)}
	if data == nil {
		return m, nil
	}
	if len(data) != rows*cols {
		return nil, shapeError("matrix data length does not equal rows*cols",
			map[string]any{"rows": rows, "cols": cols, "len": len(data)})
	}
	copy(m.data, data)
	return m, nil
}

// NewMatrixFromRows returns a matrix over a copy of a rectangular
// [][]float64. A ragged input is PULSE_MATRIX_SHAPE_MISMATCH; an empty
// input is a 0×0 matrix.
func NewMatrixFromRows(rows [][]float64) (*Matrix, error) {
	if len(rows) == 0 {
		return &Matrix{}, nil
	}
	cols := len(rows[0])
	m := &Matrix{rows: len(rows), cols: cols, data: make([]float64, len(rows)*cols)}
	for i, r := range rows {
		if len(r) != cols {
			return nil, shapeError("ragged rows: every row must have the same length",
				map[string]any{"row": i, "len": len(r), "cols": cols})
		}
		copy(m.data[i*cols:], r)
	}
	return m, nil
}

// Rows returns the row count.
func (m *Matrix) Rows() int { return m.rows }

// Cols returns the column count.
func (m *Matrix) Cols() int { return m.cols }

// At returns element (i, j). It panics when (i, j) is out of range.
func (m *Matrix) At(i, j int) float64 {
	m.check(i, j)
	return m.data[i*m.cols+j]
}

// Set assigns element (i, j). It panics when (i, j) is out of range.
func (m *Matrix) Set(i, j int, v float64) {
	m.check(i, j)
	m.data[i*m.cols+j] = v
}

// RowMajor returns a copy of the elements in row-major order.
func (m *Matrix) RowMajor() []float64 {
	out := make([]float64, len(m.data))
	copy(out, m.data)
	return out
}

// ToRows returns a copy of the matrix as a [][]float64.
func (m *Matrix) ToRows() [][]float64 {
	out := make([][]float64, m.rows)
	for i := range out {
		out[i] = make([]float64, m.cols)
		copy(out[i], m.data[i*m.cols:(i+1)*m.cols])
	}
	return out
}

func (m *Matrix) check(i, j int) {
	if i < 0 || i >= m.rows || j < 0 || j >= m.cols {
		panic("linalg: Matrix index (" + strconv.Itoa(i) + ", " + strconv.Itoa(j) +
			") out of range for " + strconv.Itoa(m.rows) + "x" + strconv.Itoa(m.cols))
	}
}

// Sym is an n×n symmetric matrix stored as its packed upper triangle in
// row order: (0,0) (0,1) … (0,n-1) (1,1) … (n-1,n-1). At(i, j) and
// At(j, i) read the same element. The zero value is an empty 0×0 matrix.
type Sym struct {
	n    int
	data []float64
}

// NewSym returns an n×n symmetric matrix over a COPY of packed, the
// n(n+1)/2 upper-triangle elements in row order. A nil packed allocates
// a zero matrix. A negative n or a length mismatch is
// PULSE_MATRIX_SHAPE_MISMATCH.
func NewSym(n int, packed []float64) (*Sym, error) {
	if n < 0 {
		return nil, shapeError("symmetric matrix order must be non-negative", map[string]any{"n": n})
	}
	size := n * (n + 1) / 2
	s := &Sym{n: n, data: make([]float64, size)}
	if packed == nil {
		return s, nil
	}
	if len(packed) != size {
		return nil, shapeError("packed length does not equal n*(n+1)/2",
			map[string]any{"n": n, "len": len(packed), "want": size})
	}
	copy(s.data, packed)
	return s, nil
}

// NewSymFromRows returns the symmetric matrix whose LOWER triangle is
// read from a square [][]float64: element (i, j) for j ≤ i is rows[i][j],
// and the strict upper triangle of the input is ignored. Reading the
// lower triangle is deliberate — it is exactly what a row-wise Cholesky
// consumes. A non-square or ragged input is PULSE_MATRIX_SHAPE_MISMATCH;
// an empty input is a 0×0 matrix.
func NewSymFromRows(rows [][]float64) (*Sym, error) {
	n := len(rows)
	s := &Sym{n: n, data: make([]float64, n*(n+1)/2)}
	for i, r := range rows {
		if len(r) != n {
			return nil, shapeError("symmetric input must be square",
				map[string]any{"row": i, "len": len(r), "n": n})
		}
		for j := 0; j <= i; j++ {
			s.data[s.index(j, i)] = r[j]
		}
	}
	return s, nil
}

// N returns the order of the matrix.
func (s *Sym) N() int { return s.n }

// At returns element (i, j) == (j, i). It panics when out of range.
func (s *Sym) At(i, j int) float64 {
	s.check(i, j)
	return s.data[s.index(i, j)]
}

// Set assigns element (i, j) and therefore (j, i). It panics when out
// of range.
func (s *Sym) Set(i, j int, v float64) {
	s.check(i, j)
	s.data[s.index(i, j)] = v
}

// ToRows returns a copy of the full (both triangles) n×n matrix.
func (s *Sym) ToRows() [][]float64 {
	out := make([][]float64, s.n)
	for i := range out {
		out[i] = make([]float64, s.n)
		for j := range out[i] {
			out[i][j] = s.data[s.index(i, j)]
		}
	}
	return out
}

// index maps (i, j) to the packed upper-triangle offset.
func (s *Sym) index(i, j int) int {
	if i > j {
		i, j = j, i
	}
	return i*s.n - i*(i-1)/2 + (j - i)
}

func (s *Sym) check(i, j int) {
	if i < 0 || i >= s.n || j < 0 || j >= s.n {
		panic("linalg: Sym index (" + strconv.Itoa(i) + ", " + strconv.Itoa(j) +
			") out of range for order " + strconv.Itoa(s.n))
	}
}

func (s *Sym) clone() *Sym {
	c := &Sym{n: s.n, data: make([]float64, len(s.data))}
	copy(c.data, s.data)
	return c
}

// Vec is a dense float64 vector. The zero value is empty.
type Vec struct {
	data []float64
}

// NewVec returns a vector over a COPY of data.
func NewVec(data []float64) *Vec {
	v := &Vec{data: make([]float64, len(data))}
	copy(v.data, data)
	return v
}

// Len returns the vector length.
func (v *Vec) Len() int { return len(v.data) }

// At returns element i. It panics when out of range.
func (v *Vec) At(i int) float64 { return v.data[i] }

// Set assigns element i. It panics when out of range.
func (v *Vec) Set(i int, x float64) { v.data[i] = x }

// Slice returns a copy of the elements.
func (v *Vec) Slice() []float64 {
	out := make([]float64, len(v.data))
	copy(out, v.data)
	return out
}

func shapeError(msg string, details map[string]any) error {
	return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SHAPE_MISMATCH, "linalg: "+msg, details)
}

func nilOperand(name string) error {
	return shapeError("nil "+name+" operand", map[string]any{"operand": name})
}
