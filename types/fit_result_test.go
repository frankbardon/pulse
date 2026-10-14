package types

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

// TestFitResult_Wire: a regression arm marshals as the bare
// RegressionResult (byte-identical to the regression-only shape), a
// vector arm as {"vector": …} with undefined figures null, an empty
// entry as null — and each reads back into its own arm.
func TestFitResult_Wire(t *testing.T) {
	reg := &RegressionResult{Type: REG_OLS, Coefficients: map[string]float64{"x": 2, "(intercept)": math.NaN()}, NObs: 9}
	want, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(FitResult{RegressionResult: reg})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || bytes.Contains(got, []byte(`"vector"`)) {
		t.Fatalf("regression arm %s, want %s", got, want)
	}
	vec := &VectorFitResult{Operator: "MAT_COVARIANCE", Labels: []string{"a"}, Members: []string{"a"}, Basis: "unweighted",
		N: 1, SumWeights: 1, NStar: 1, Mean: []float64{math.NaN()},
		Covariance: &MatrixValues{Kind: MatrixKindSquareSymmetric, Encoding: MatrixEncodingFull, RowKeys: []string{"a"}, ColumnKeys: []string{"a"}, Values: [][]float64{{math.NaN()}}}}
	got, err = json.Marshal(map[string]*FitResult{"v": {Vector: vec}, "r": {RegressionResult: reg}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"v":{"vector":{"operator":"MAT_COVARIANCE"`)) || !bytes.Contains(got, []byte(`"mean":[null]`)) ||
		!bytes.Contains(got, []byte(`"values":[[null]]`)) {
		t.Fatalf("vector arm %s", got)
	}
	var back map[string]*FitResult
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if back["v"].Vector == nil || back["v"].RegressionResult != nil || back["r"].RegressionResult == nil || back["r"].Vector != nil || back["r"].NObs != 9 {
		t.Fatalf("read back %+v / %+v", back["v"], back["r"])
	}
	if b, _ := json.Marshal(FitResult{}); string(b) != "null" {
		t.Fatalf("empty entry %s", b)
	}
}
