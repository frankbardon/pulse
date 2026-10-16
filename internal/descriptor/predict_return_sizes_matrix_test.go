package descriptor

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// predict_return_sizes_matrix_test.go pins the size model's
// matrices[*].auxiliary: every key a matrix operator emits under its
// params, at the runtime's shape, and the `return` rules over it.

func auxSizesSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "N", "S", "E", "W")},
		{Name: "x1", Type: encoding.FieldTypeF64},
		{Name: "x2", Type: encoding.FieldTypeF64},
		{Name: "x3", Type: encoding.FieldTypeF64},
		{Name: "x4", Type: encoding.FieldTypeF64},
	}}
}

func auxSizesCohort(t *testing.T, n int) []byte {
	t.Helper()
	s := auxSizesSchema(t)
	return append(buildTestPulseFile(t, s), make([]byte, n*s.RecordByteSize())...)
}

func auxSpec(typ types.MatrixType, fields []string, params string) types.MatrixSpec {
	s := types.MatrixSpec{Name: "m", Type: typ, Fields: fields}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

type auxShape struct {
	name       string
	rows, cols int64
}

// TestMatrixAuxiliaryNodes_PerOperator: each operator's auxiliary keys
// (every one declared in its OutputKeys.Auxiliary) and shapes as its
// runtime emits them (matrix_finalize.go,
// matrix_inference.go, matrix_partial.go, matrix_pca.go,
// matrix_factor.go, matrix_collinearity.go, matrix_distance.go,
// matrix_sem.go).
func TestMatrixAuxiliaryNodes_PerOperator(t *testing.T) {
	x3 := []string{"x1", "x2", "x3"}
	x4 := []string{"x1", "x2", "x3", "x4"}
	between := `"between": {"type": "GROUP_CATEGORY", "field": "region"}`
	cases := []struct {
		name string
		spec types.MatrixSpec
		want []auxShape
	}{
		{"covariance_listwise", auxSpec(types.MAT_COVARIANCE, x3, ""), nil},
		{"covariance_pairwise", auxSpec(types.MAT_COVARIANCE, x3, `{"missing": "pairwise"}`), []auxShape{{"n", 3, 3}}},
		{"correlation_off", auxSpec(types.MAT_CORRELATION, x3, ""), nil},
		{"correlation_inference", auxSpec(types.MAT_CORRELATION, x3, `{"inference": true}`),
			[]auxShape{{"p", 3, 3}, {"ci_lower", 3, 3}, {"ci_upper", 3, 3}}},
		{"correlation_pairwise_inference", auxSpec(types.MAT_CORRELATION, x3, `{"inference": true, "missing": "pairwise"}`),
			[]auxShape{{"n", 3, 3}, {"p", 3, 3}, {"ci_lower", 3, 3}, {"ci_upper", 3, 3}}},
		{"kendall_inference", auxSpec(types.MAT_CORRELATION, x3, `{"inference": true, "method": "kendall"}`),
			[]auxShape{{"p", 3, 3}, {"ci_lower", 3, 3}, {"ci_upper", 3, 3}}},
		// A control list drops x4 from the output axis: 3 x 3.
		{"partial_controls_inference", auxSpec(types.MAT_PARTIAL_CORRELATION, x4, `{"inference": true, "control": ["x4"], "missing": "pairwise"}`),
			[]auxShape{{"n", 3, 3}, {"p", 3, 3}, {"ci_lower", 3, 3}, {"ci_upper", 3, 3}}},
		{"reliability_pairwise", auxSpec(types.MAT_RELIABILITY, x3, `{"missing": "pairwise"}`), []auxShape{{"n", 3, 3}}},
		{"pca_fixed", auxSpec(types.MAT_PCA, x4, `{"components": 2}`), []auxShape{{"eigenvectors", 4, 2}}},
		{"pca_kaiser_varimax", auxSpec(types.MAT_PCA, x4, `{"rotation": "varimax"}`),
			[]auxShape{{"eigenvectors", 4, 4}, {"unrotated", 4, 4}}},
		{"factor_none", auxSpec(types.MAT_FACTOR, x4, `{"factors": 1}`), nil},
		{"factor_varimax", auxSpec(types.MAT_FACTOR, x4, `{"factors": 1, "rotation": "varimax"}`), []auxShape{{"unrotated", 4, 1}}},
		{"factor_promax_pairwise", auxSpec(types.MAT_FACTOR, x4, `{"factors": 1, "rotation": "promax", "missing": "pairwise"}`),
			[]auxShape{{"n", 4, 4}, {"unrotated", 4, 1}, {"factor_correlation", 1, 1}}},
		{"collinearity_uncentered", auxSpec(types.MAT_COLLINEARITY, x3, ""), []auxShape{{"variance_decomposition", 4, 4}}},
		{"collinearity_centered", auxSpec(types.MAT_COLLINEARITY, x3, `{"center": true}`), []auxShape{{"variance_decomposition", 3, 3}}},
		{"distance", auxSpec(types.MAT_DISTANCE, x3, `{`+between+`}`), []auxShape{{"centroids", 4, 3}}},
		// One factor over three members: three loadings (x1's fixed),
		// three residual variances and the factor variance.
		{"sem_default", auxSpec(types.MAT_SEM, x3, ""), []auxShape{{"estimates", 7, 5}, {"residual_covariance", 3, 3}}},
		{"sem_model", auxSpec(types.MAT_SEM, x4, `{"model": "f =~ x1 + x2 + x3\nx4 ~ f"}`), []auxShape{{"estimates", 9, 5}, {"residual_covariance", 4, 4}}},
		{"distance_standardized_mahalanobis", auxSpec(types.MAT_DISTANCE, x3, `{`+between+`, "standardize": true, "metric": "mahalanobis"}`),
			[]auxShape{{"centroids", 4, 3}, {"centroids_standardized", 4, 3}, {"pooled_covariance", 3, 3}}},
	}
	schema := auxSizesSchema(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Matrices: []types.MatrixSpec{c.spec}}
			result := &descriptor.PredictResult{}
			plans := predictMatrices(result, req, schema, nil, nil, -1, 0, -1)
			if len(plans) != 1 || len(result.Matrices) != 1 {
				t.Fatalf("resolve: %d plans, %d results", len(plans), len(result.Matrices))
			}
			declared := map[string]bool{}
			for _, meta := range matrixCapabilities() {
				if meta.Name == string(c.spec.Type) {
					for _, k := range meta.OutputKeys.Auxiliary {
						declared[k] = true
					}
				}
			}
			var got []auxShape
			for _, n := range matrixAuxiliaryNodes(plans[0], result.Matrices[0], false) {
				if !declared[n.seg.Name] {
					t.Errorf("%s sizes auxiliary.%s, which the operator does not declare", c.spec.Type, n.seg.Name)
				}
				got = append(got, auxShape{n.seg.Name, n.kids[2].kids[0].count, n.kids[3].kids[0].count})
				if v := n.kids[4].kids[0].count; v != n.kids[2].kids[0].count*n.kids[3].kids[0].count {
					t.Errorf("%s: %d values for a %d x %d matrix", n.seg.Name, v, n.kids[2].kids[0].count, n.kids[3].kids[0].count)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("auxiliary = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPredict_ReturnSizes_MatrixAuxiliary: params.inference grows the
// matrices section by exactly its three p x p auxiliaries (plus the
// auxiliary key); a `return` that drops matrices[*].auxiliary (exclude,
// preset standard) shapes them away, and an include of one key keeps
// exactly that key.
func TestPredict_ReturnSizes_MatrixAuxiliary(t *testing.T) {
	data := auxSizesCohort(t, 50)
	fields := []string{"x1", "x2", "x3"}
	run := func(params string, ret *types.Return) descriptor.ResponseSectionSize {
		t.Helper()
		rp := predictSizes(t, data, &types.Request{Matrices: []types.MatrixSpec{auxSpec(types.MAT_CORRELATION, fields, params)}, Return: ret})
		s, ok := sizeOf(rp, "matrices")
		if !ok {
			t.Fatalf("matrices not sized for params %s", params)
		}
		return s
	}
	square := matrixValuesNode("p", 3, 3).full()
	auxKey := len64("auxiliary") + sizeKeyOverhead + sizeObject
	off := run("", nil)
	on := run(`{"inference": true}`, nil)
	if want := off.FullBytes + auxKey + 3*square + len64("ci_lower") - len64("p") + len64("ci_upper") - len64("p"); on.FullBytes != want {
		t.Errorf("inference full = %d, want %d (off %d + 3 auxiliaries)", on.FullBytes, want, off.FullBytes)
	}
	if on.ShapedBytes != on.FullBytes {
		t.Errorf("no return block: shaped %d != full %d", on.ShapedBytes, on.FullBytes)
	}
	// Pairwise adds the n auxiliary too.
	pw := run(`{"inference": true, "missing": "pairwise"}`, nil)
	if want := on.FullBytes + matrixValuesNode("n", 3, 3).full(); pw.FullBytes != want {
		t.Errorf("pairwise inference full = %d, want %d", pw.FullBytes, want)
	}

	excl := run(`{"inference": true}`, &types.Return{Exclude: []string{"matrices[*].auxiliary"}})
	if excl.ShapedBytes != off.FullBytes || excl.FullBytes != on.FullBytes {
		t.Errorf("exclude auxiliary: %+v, want shaped %d full %d", excl, off.FullBytes, on.FullBytes)
	}
	// standard (E4-G1) keeps the inference auxiliaries a spec asked
	// for, and only those: an inference spec keeps its three squares, a
	// spec without inference loses its auxiliary entirely.
	std := run(`{"inference": true}`, &types.Return{Preset: types.ReturnPresetStandard})
	stdOff := run("", &types.Return{Preset: types.ReturnPresetStandard})
	inferenceAux := auxKey + 3*square + len64("ci_lower") - len64("p") + len64("ci_upper") - len64("p")
	if want := stdOff.ShapedBytes + inferenceAux; std.ShapedBytes != want {
		t.Errorf("standard keeps inference auxiliaries: shaped %d, want %d", std.ShapedBytes, want)
	}
	// The pairwise n rides beside p (a companion) under standard; a
	// pairwise spec without inference still drops it.
	pwOff := run(`{"missing": "pairwise"}`, &types.Return{Preset: types.ReturnPresetStandard})
	if pwOff.ShapedBytes != stdOff.ShapedBytes {
		t.Errorf("standard pairwise without inference: shaped %d, want %d (no auxiliary)", pwOff.ShapedBytes, stdOff.ShapedBytes)
	}
	pwStd := run(`{"inference": true, "missing": "pairwise"}`, &types.Return{Preset: types.ReturnPresetStandard})
	if want := std.ShapedBytes + matrixValuesNode("n", 3, 3).full(); pwStd.ShapedBytes != want {
		t.Errorf("standard pairwise inference: shaped %d, want %d (n beside p)", pwStd.ShapedBytes, want)
	}
	// Exclude wins over the preset: p gone takes its companion n too.
	pwNoP := run(`{"inference": true, "missing": "pairwise"}`, &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"matrices[*].auxiliary.p"}})
	if want := std.ShapedBytes - square; pwNoP.ShapedBytes != want {
		t.Errorf("standard pairwise inference minus p: shaped %d, want %d", pwNoP.ShapedBytes, want)
	}
	pwNoN := run(`{"inference": true, "missing": "pairwise"}`, &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{"matrices[*].auxiliary.n"}})
	if pwNoN.ShapedBytes != std.ShapedBytes {
		t.Errorf("standard pairwise inference minus n: shaped %d, want %d", pwNoN.ShapedBytes, std.ShapedBytes)
	}
	// minimal is unchanged: no auxiliary at all.
	minOn := run(`{"inference": true}`, &types.Return{Preset: types.ReturnPresetMinimal})
	minOff := run("", &types.Return{Preset: types.ReturnPresetMinimal})
	if minOn.ShapedBytes != minOff.ShapedBytes {
		t.Errorf("minimal drops auxiliary: shaped %d, want %d", minOn.ShapedBytes, minOff.ShapedBytes)
	}
	inc := run(`{"inference": true}`, &types.Return{Include: []string{"matrices[*].auxiliary.p"}})
	if want := auxKey + square; inc.ShapedBytes != want+sizeObject+len64("matrices")+sizeKeyOverhead+sizeObject {
		t.Errorf("include auxiliary.p alone: shaped %d, want the auxiliary.p square", inc.ShapedBytes)
	}
}

// TestPredict_ReturnSizes_MatrixMultiplicity (U28 E3-S4): a spec a
// multiplicity family claims grows by its two p x p twins
// (p_adjusted, significant_adjusted) and the multiplicity echo; a
// block that resolves to none, or an inherited one on a spec without
// inference, adds nothing.
func TestPredict_ReturnSizes_MatrixMultiplicity(t *testing.T) {
	data := auxSizesCohort(t, 50)
	fields := []string{"x1", "x2", "x3"}
	run := func(params string, own, req *types.Multiplicity) int64 {
		t.Helper()
		spec := auxSpec(types.MAT_CORRELATION, fields, params)
		spec.Multiplicity = own
		rp := predictSizes(t, data, &types.Request{Matrices: []types.MatrixSpec{spec}, Multiplicity: req})
		s, ok := sizeOf(rp, "matrices")
		if !ok {
			t.Fatalf("matrices not sized")
		}
		return s.FullBytes
	}
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	none := &types.Multiplicity{Method: types.MultiplicityMethodNone}
	inference := run(`{"inference": true}`, nil, nil)
	twins := matrixValuesNode("p_adjusted", 3, 3).full() + matrixValuesNode("significant_adjusted", 3, 3).full() +
		leaf("multiplicity", sizeHeader).full()
	if got := run("", holm, nil); got != inference+twins {
		t.Errorf("own block: %d, want %d (inference %d + twins and echo %d)", got, inference+twins, inference, twins)
	}
	if got := run(`{"inference": true}`, nil, holm); got != inference+twins {
		t.Errorf("inherited block on an inferential spec: %d, want %d", got, inference+twins)
	}
	if got := run("", none, nil); got != inference {
		t.Errorf("own none: %d, want the implied inference alone %d", got, inference)
	}
	if got, off := run("", nil, holm), run("", nil, nil); got != off {
		t.Errorf("inherited block on a spec without inference: %d, want %d", got, off)
	}
}
