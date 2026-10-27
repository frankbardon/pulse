package extend_test

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

func cell(r, c int) float64 { return float64(10*r + c) }

// TestMatrixInputSquare pins the square renderer: the full encoding
// writes every cell, the upper encoding starts row r at column r, an
// empty Encoding means full, labels ride along only when present, and
// the result never aliases the input's member or label slices.
func TestMatrixInputSquare(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       extend.MatrixInput
		encoding types.MatrixEncoding
		values   [][]float64
		labels   []string
	}{
		{
			name:     "default_full",
			in:       extend.MatrixInput{Members: []string{"a", "b", "c"}},
			encoding: types.MatrixEncodingFull,
			values:   [][]float64{{0, 1, 2}, {10, 11, 12}, {20, 21, 22}},
		},
		{
			name:     "upper",
			in:       extend.MatrixInput{Members: []string{"a", "b", "c"}, Encoding: types.MatrixEncodingUpper},
			encoding: types.MatrixEncodingUpper,
			values:   [][]float64{{0, 1, 2}, {11, 12}, {22}},
		},
		{
			name:     "labels",
			in:       extend.MatrixInput{Members: []string{"a", "b"}, Labels: []string{"A", "B"}, Encoding: types.MatrixEncodingFull},
			encoding: types.MatrixEncodingFull,
			values:   [][]float64{{0, 1}, {10, 11}},
			labels:   []string{"A", "B"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Square(cell)
			if got.Kind != types.MatrixKindSquareSymmetric || got.Encoding != tc.encoding {
				t.Fatalf("kind/encoding = %s/%s, want %s/%s", got.Kind, got.Encoding, types.MatrixKindSquareSymmetric, tc.encoding)
			}
			if !reflect.DeepEqual(got.RowKeys, tc.in.Members) || !reflect.DeepEqual(got.ColumnKeys, tc.in.Members) {
				t.Fatalf("keys = %v/%v, want %v", got.RowKeys, got.ColumnKeys, tc.in.Members)
			}
			if !reflect.DeepEqual(got.Values, tc.values) {
				t.Fatalf("values = %v, want %v", got.Values, tc.values)
			}
			if !reflect.DeepEqual(got.Labels, tc.labels) {
				t.Fatalf("labels = %#v, want %#v", got.Labels, tc.labels)
			}
			got.RowKeys[0], got.ColumnKeys[0] = "x", "y"
			if len(got.Labels) > 0 {
				got.Labels[0] = "z"
			}
			if tc.in.Members[0] != "a" || (len(tc.in.Labels) > 0 && tc.in.Labels[0] != "A") {
				t.Fatalf("Square aliases the input: members %v labels %v", tc.in.Members, tc.in.Labels)
			}
		})
	}
}

// TestMatrixInputRect pins the rectangular renderer: always full (even
// when the input asks for upper), one row per member, the given column
// keys copied.
func TestMatrixInputRect(t *testing.T) {
	t.Parallel()

	in := extend.MatrixInput{Members: []string{"a", "b"}, Labels: []string{"A", "B"}, Encoding: types.MatrixEncodingUpper}
	cols := []string{"F1", "F2", "F3"}
	got := in.Rect(cols, cell)
	want := &types.MatrixValues{
		Kind:       types.MatrixKindRectangular,
		Encoding:   types.MatrixEncodingFull,
		RowKeys:    []string{"a", "b"},
		ColumnKeys: []string{"F1", "F2", "F3"},
		Labels:     []string{"A", "B"},
		Values:     [][]float64{{0, 1, 2}, {10, 11, 12}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rect = %+v, want %+v", got, want)
	}
	got.ColumnKeys[0] = "x"
	if cols[0] != "F1" {
		t.Fatal("Rect aliases the column keys")
	}
}

// TestMatrixOperatorContract compiles the contract an embedder writes
// against: a factory returning a finalize-only operator that reads the
// co-moment (PairM2 included) and fills every MatrixOutput part.
func TestMatrixOperatorContract(t *testing.T) {
	t.Parallel()

	var factory extend.MatrixFactory = func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
		return scatterOp{}, nil
	}
	op, err := factory(&types.MatrixSpec{Type: "MAT_ACME_SCATTER"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cm, err := linalg.NewCoMoment(2, linalg.Listwise)
	if err != nil {
		t.Fatal(err)
	}
	cm.Add([]float64{1, 2}, 1)
	cm.Add([]float64{2, 4}, 1)
	cm.Add([]float64{3, 9}, 1)
	out, err := op.Finalize(&extend.MatrixInput{CoMoment: cm, Members: []string{"x", "y"}, WantScalars: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]float64{{2, 7}, {7, 26}}; !reflect.DeepEqual(out.Primary.Values, want) {
		t.Fatalf("primary = %v, want %v", out.Primary.Values, want)
	}
	if out.Scalars["trace"] != 28 || out.Components["p"] != 2 {
		t.Fatalf("scalars %v components %v", out.Scalars, out.Components)
	}
}

// scatterOp is the scatter (raw co-moment) matrix: PairM2 per pair.
type scatterOp struct{}

func (scatterOp) Finalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	cm := in.CoMoment
	out := extend.MatrixOutput{
		Primary:    in.Square(cm.PairM2),
		Components: map[string]any{"p": cm.P()},
	}
	if in.WantScalars {
		tr := 0.0
		for i := 0; i < cm.P(); i++ {
			tr += cm.PairM2(i, i)
		}
		out.Scalars = map[string]float64{"trace": tr}
	}
	return out, nil
}
