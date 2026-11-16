package gp

import (
	"fmt"
	"math"
)

// Dim is one dimension of the search space, in declared order.
type Dim struct {
	// Levels is 0 for a continuous dimension, read as a unit-interval
	// coordinate; ≥ 1 for a categorical dimension with Levels values,
	// encoded one-hot (Levels columns sharing one length scale).
	Levels int
}

// Space is a validated search space: its dimensions and the encoded
// column layout. Immutable once built.
type Space struct {
	dims []Dim
	// group maps each encoded column to its dimension (its length-scale
	// index); start is each dimension's first encoded column.
	group []int
	start []int
}

// NewSpace validates dims (at least one; Levels ≥ 0) and lays out the
// encoded columns: one per continuous dimension, Levels per categorical
// one, in declared order.
func NewSpace(dims []Dim) (*Space, error) {
	if len(dims) == 0 {
		return nil, fmt.Errorf("gp: a space needs at least one dimension")
	}
	s := &Space{dims: append([]Dim(nil), dims...), start: make([]int, len(dims))}
	for d, dim := range dims {
		if dim.Levels < 0 {
			return nil, fmt.Errorf("gp: dimension %d has negative Levels %d", d, dim.Levels)
		}
		s.start[d] = len(s.group)
		w := max(dim.Levels, 1)
		for range w {
			s.group = append(s.group, d)
		}
	}
	return s, nil
}

// Dims is the dimension count — the number of length scales.
func (s *Space) Dims() int { return len(s.dims) }

// Cols is the encoded column count.
func (s *Space) Cols() int { return len(s.group) }

// Encode maps one point onto its encoded row: a continuous dimension d
// contributes u[d] (finite; not clamped), a categorical one the one-hot
// row of index[d] in [0, Levels). Both slices are len(Dims); each is
// read only at the dimensions of its kind, and index may be nil for an
// all-continuous space, u for an all-categorical one.
func (s *Space) Encode(u []float64, index []int) ([]float64, error) {
	row := make([]float64, len(s.group))
	for d, dim := range s.dims {
		if dim.Levels == 0 {
			if d >= len(u) {
				return nil, fmt.Errorf("gp: continuous dimension %d has no coordinate (len(u) = %d)", d, len(u))
			}
			if math.IsNaN(u[d]) || math.IsInf(u[d], 0) {
				return nil, fmt.Errorf("gp: continuous dimension %d coordinate is not finite", d)
			}
			row[s.start[d]] = u[d]
			continue
		}
		if d >= len(index) {
			return nil, fmt.Errorf("gp: categorical dimension %d has no index (len(index) = %d)", d, len(index))
		}
		if index[d] < 0 || index[d] >= dim.Levels {
			return nil, fmt.Errorf("gp: categorical dimension %d index %d outside [0, %d)", d, index[d], dim.Levels)
		}
		row[s.start[d]+index[d]] = 1
	}
	return row, nil
}
