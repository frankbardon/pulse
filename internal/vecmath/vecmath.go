// Package vecmath holds the stateless vector and scalar maths kernels
// behind the expression-language vector built-ins (vsum, vmean, dot,
// cosine, argmax, vzscore, vnormalize, sqrt, ...). The expr closures in
// internal/processing only adapt arguments; every numeric rule lives
// here so later units (reference-vector lookups) reuse one
// implementation.
//
// It is a LEAF: standard library only, so internal/processing,
// internal/descriptor and any future caller import it without a cycle.
// TestVecmathImportBoundary enforces the ceiling.
//
// # Edge semantics (pinned)
//
//   - Reductions (Sum, Mean, Min, Max, SD, Norm) skip NaN. An empty or
//     all-NaN input gives NaN — Sum included, so "unanswered" never
//     masquerades as 0. SD (ddof = 1) needs at least two valid values.
//     Count is the number of non-NaN values.
//   - Dot, Dist and Cosine are pairwise-complete: index i is dropped
//     when either side is NaN. No complete pair gives NaN; a zero-norm
//     Cosine gives NaN. Unequal lengths return a *LengthMismatchError
//     (errors.Is ErrLengthMismatch), never NaN.
//   - ArgMax / ArgMin return a 0-based index; the first index wins a
//     tie; an empty or all-NaN input returns ok == false.
//   - Center, ZScore and Normalize return a NEW slice (the input is
//     never mutated, because callers bind reused per-worker buffers),
//     keep NaN in place, and give all-NaN when the mean is undefined,
//     the sd is zero or undefined, or the norm is zero or undefined.
//   - Infinities are ordinary IEEE values: they are not skipped.
//
// # FMA-free arithmetic
//
// Every product that feeds an addition is written float64(a*b): the Go
// specification forbids fusing an explicitly converted product into a
// fused multiply-add, so arm64 and amd64 give identical bits. Never
// remove one of those conversions, and never reorder a loop: the
// left-to-right summation order is part of the contract.
package vecmath

import (
	"errors"
	"fmt"
	"math"
)

// ErrLengthMismatch is the sentinel every *LengthMismatchError matches
// under errors.Is.
var ErrLengthMismatch = errors.New("vecmath: vector length mismatch")

// ErrInvalidNormalizeMode is the sentinel every *InvalidNormalizeModeError
// matches under errors.Is.
var ErrInvalidNormalizeMode = errors.New("vecmath: invalid normalize mode")

// LengthMismatchError reports two vectors of unequal length handed to a
// pairwise kernel (Dot, Dist, Cosine).
type LengthMismatchError struct {
	Left, Right int
}

func (e *LengthMismatchError) Error() string {
	return fmt.Sprintf("vecmath: vector length mismatch: %d vs %d", e.Left, e.Right)
}

// Is makes errors.Is(err, ErrLengthMismatch) true.
func (e *LengthMismatchError) Is(target error) bool { return target == ErrLengthMismatch }

// InvalidNormalizeModeError reports a Normalize mode other than NormL1
// or NormL2.
type InvalidNormalizeModeError struct {
	Mode string
}

func (e *InvalidNormalizeModeError) Error() string {
	return fmt.Sprintf("vecmath: invalid normalize mode %q (want %q or %q)", e.Mode, NormL1, NormL2)
}

// Is makes errors.Is(err, ErrInvalidNormalizeMode) true.
func (e *InvalidNormalizeModeError) Is(target error) bool { return target == ErrInvalidNormalizeMode }

// Normalize modes.
const (
	NormL1 = "l1"
	NormL2 = "l2"
)

// Count returns the number of non-NaN values in v.
func Count(v []float64) int {
	n := 0
	for _, x := range v {
		if !math.IsNaN(x) {
			n++
		}
	}
	return n
}

// Sum returns the NaN-skipping sum of v; NaN when v has no valid value.
func Sum(v []float64) float64 {
	s, n := sumCount(v)
	if n == 0 {
		return math.NaN()
	}
	return s
}

// Mean returns the NaN-skipping arithmetic mean of v; NaN when v has no
// valid value.
func Mean(v []float64) float64 {
	s, n := sumCount(v)
	if n == 0 {
		return math.NaN()
	}
	return s / float64(n)
}

// Min returns the NaN-skipping minimum of v; NaN when v has no valid
// value.
func Min(v []float64) float64 {
	i, ok := ArgMin(v)
	if !ok {
		return math.NaN()
	}
	return v[i]
}

// Max returns the NaN-skipping maximum of v; NaN when v has no valid
// value.
func Max(v []float64) float64 {
	i, ok := ArgMax(v)
	if !ok {
		return math.NaN()
	}
	return v[i]
}

// SD returns the NaN-skipping sample standard deviation (ddof = 1) of v,
// computed two-pass about the mean; NaN with fewer than two valid
// values.
func SD(v []float64) float64 {
	s, n := sumCount(v)
	if n < 2 {
		return math.NaN()
	}
	mean := s / float64(n)
	ss := 0.0
	for _, x := range v {
		if math.IsNaN(x) {
			continue
		}
		d := x - mean
		ss += float64(d * d)
	}
	return math.Sqrt(ss / float64(n-1))
}

// Norm returns the NaN-skipping L2 norm of v; NaN when v has no valid
// value.
func Norm(v []float64) float64 {
	ss, n := 0.0, 0
	for _, x := range v {
		if math.IsNaN(x) {
			continue
		}
		ss += float64(x * x)
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(ss)
}

// Dot returns the pairwise-complete dot product of a and b; NaN when no
// index has both sides valid.
func Dot(a, b []float64) (float64, error) {
	if err := sameLen(a, b); err != nil {
		return 0, err
	}
	s, n := 0.0, 0
	for i := range a {
		if math.IsNaN(a[i]) || math.IsNaN(b[i]) {
			continue
		}
		s += float64(a[i] * b[i])
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	return s, nil
}

// Dist returns the pairwise-complete Euclidean distance between a and
// b; NaN when no index has both sides valid.
func Dist(a, b []float64) (float64, error) {
	if err := sameLen(a, b); err != nil {
		return 0, err
	}
	ss, n := 0.0, 0
	for i := range a {
		if math.IsNaN(a[i]) || math.IsNaN(b[i]) {
			continue
		}
		d := a[i] - b[i]
		ss += float64(d * d)
		n++
	}
	if n == 0 {
		return math.NaN(), nil
	}
	return math.Sqrt(ss), nil
}

// Cosine returns the pairwise-complete cosine similarity of a and b:
// the dot product and both norms are taken over the same complete
// pairs. NaN when no complete pair exists or either norm is zero.
func Cosine(a, b []float64) (float64, error) {
	if err := sameLen(a, b); err != nil {
		return 0, err
	}
	dot, aa, bb, n := 0.0, 0.0, 0.0, 0
	for i := range a {
		if math.IsNaN(a[i]) || math.IsNaN(b[i]) {
			continue
		}
		dot += float64(a[i] * b[i])
		aa += float64(a[i] * a[i])
		bb += float64(b[i] * b[i])
		n++
	}
	if n == 0 || aa == 0 || bb == 0 {
		return math.NaN(), nil
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb)), nil
}

// ArgMax returns the 0-based index of the largest non-NaN value in v;
// the first index wins a tie. ok is false when v has no valid value.
func ArgMax(v []float64) (idx int, ok bool) {
	idx = -1
	for i, x := range v {
		if math.IsNaN(x) {
			continue
		}
		if idx < 0 || x > v[idx] {
			idx = i
		}
	}
	return idx, idx >= 0
}

// ArgMin returns the 0-based index of the smallest non-NaN value in v;
// the first index wins a tie. ok is false when v has no valid value.
func ArgMin(v []float64) (idx int, ok bool) {
	idx = -1
	for i, x := range v {
		if math.IsNaN(x) {
			continue
		}
		if idx < 0 || x < v[idx] {
			idx = i
		}
	}
	return idx, idx >= 0
}

// Center returns a new slice holding v minus its NaN-skipping mean, NaN
// kept in place; all-NaN when the mean is undefined.
func Center(v []float64) []float64 {
	return affine(v, Mean(v), 1)
}

// ZScore returns a new slice holding (v - mean) / sd (ddof = 1), NaN
// kept in place; all-NaN when the sd is zero or undefined.
func ZScore(v []float64) []float64 {
	sd := SD(v)
	if sd == 0 {
		return affine(v, math.NaN(), 1)
	}
	return affine(v, Mean(v), sd)
}

// Normalize returns a new slice holding v divided by its NaN-skipping
// L1 (sum of absolute values) or L2 norm, NaN kept in place; all-NaN
// when the norm is zero or undefined. Any mode other than NormL1 or
// NormL2 returns an *InvalidNormalizeModeError.
func Normalize(v []float64, mode string) ([]float64, error) {
	var norm float64
	switch mode {
	case NormL1:
		norm = l1(v)
	case NormL2:
		norm = Norm(v)
	default:
		return nil, &InvalidNormalizeModeError{Mode: mode}
	}
	if norm == 0 {
		norm = math.NaN()
	}
	return affine(v, 0, norm), nil
}

// Sqrt is the IEEE square root (negative → NaN).
func Sqrt(x float64) float64 { return math.Sqrt(x) }

// Log is the IEEE natural logarithm (0 → -Inf, negative → NaN).
func Log(x float64) float64 { return math.Log(x) }

// Exp is the IEEE natural exponential (overflow → +Inf).
func Exp(x float64) float64 { return math.Exp(x) }

// Pow is the IEEE power x**y (math.Pow special cases apply).
func Pow(x, y float64) float64 { return math.Pow(x, y) }

// sumCount is the NaN-skipping left-to-right sum and valid count.
func sumCount(v []float64) (float64, int) {
	s, n := 0.0, 0
	for _, x := range v {
		if math.IsNaN(x) {
			continue
		}
		s += x
		n++
	}
	return s, n
}

// l1 is the NaN-skipping L1 norm. An empty or all-NaN v gives 0, which
// Normalize's zero-norm rule already maps to all-NaN.
func l1(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		if math.IsNaN(x) {
			continue
		}
		s += math.Abs(x)
	}
	return s
}

// affine returns a new slice holding (x - shift) / scale for every x;
// NaN stays NaN, and a NaN shift or scale yields all-NaN.
func affine(v []float64, shift, scale float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = (x - shift) / scale
	}
	return out
}

func sameLen(a, b []float64) error {
	if len(a) != len(b) {
		return &LengthMismatchError{Left: len(a), Right: len(b)}
	}
	return nil
}
