package feature

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFEAT_POLY_Numerical exercises the canonical fixture: Field=x,
// Degree=3, input [1, 2, 3, 4] expands to {x_poly_2: [1, 4, 9, 16],
// x_poly_3: [1, 8, 27, 64]}. Asserts exact equality — the operator is
// integer powers of a known input.
func TestFEAT_POLY_Numerical(t *testing.T) {
	records := makeRecords([]float64{1, 2, 3, 4}, nil, "x")
	c, err := newPoly(&types.Feature{
		Type:   types.FEAT_POLY,
		Field:  "x",
		Params: json.RawMessage(`{"degree":3}`),
	}, nil)
	if err != nil {
		t.Fatalf("newPoly: %v", err)
	}
	out, err := c.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	want2 := []float64{1, 4, 9, 16}
	want3 := []float64{1, 8, 27, 64}
	got2, ok := out["x_poly_2"]
	if !ok {
		t.Fatalf("expected x_poly_2 in outputs, got %v", keysSorted(out))
	}
	got3, ok := out["x_poly_3"]
	if !ok {
		t.Fatalf("expected x_poly_3 in outputs, got %v", keysSorted(out))
	}
	for i := range want2 {
		if got2.Values[i] != want2[i] {
			t.Errorf("x_poly_2[%d] = %f, want %f", i, got2.Values[i], want2[i])
		}
		if got3.Values[i] != want3[i] {
			t.Errorf("x_poly_3[%d] = %f, want %f", i, got3.Values[i], want3[i])
		}
	}
	if _, exists := out["x_poly_1"]; exists {
		t.Error("FEAT_POLY must not emit a degree-1 column; the original x is reachable unchanged")
	}
	if _, exists := out["x_poly_4"]; exists {
		t.Error("FEAT_POLY with Degree=3 must not emit higher-order columns")
	}
}

// TestFEAT_POLY_LabelOverride verifies the Label field replaces the
// default <field>_poly prefix so callers can clean up the predictor
// list when composing with REG_OLS.
func TestFEAT_POLY_LabelOverride(t *testing.T) {
	records := makeRecords([]float64{2}, nil, "x")
	c, _ := newPoly(&types.Feature{
		Type:   types.FEAT_POLY,
		Field:  "x",
		Label:  "x",
		Params: json.RawMessage(`{"degree":2}`),
	}, nil)
	out, _ := c.Compute(records, "x")
	if _, ok := out["x_2"]; !ok {
		t.Errorf("Label=\"x\" should yield x_2; got columns %v", keysSorted(out))
	}
	if _, ok := out["x_poly_2"]; ok {
		t.Errorf("Label override must drop the default _poly infix; got x_poly_2")
	}
}

// TestFEAT_POLY_DegreeValidation walks the rejection boundary: degree 0,
// 1, and 11 all surface PROCESSING_CONFIG. Degree 2 and 10 are accepted.
func TestFEAT_POLY_DegreeValidation(t *testing.T) {
	cases := []struct {
		name    string
		params  string
		wantErr bool
	}{
		{"degree zero", `{"degree":0}`, true},
		{"degree one (use linear term)", `{"degree":1}`, true},
		{"degree negative", `{"degree":-2}`, true},
		{"degree two — boundary OK", `{"degree":2}`, false},
		{"degree ten — boundary OK", `{"degree":10}`, false},
		{"degree eleven — capped", `{"degree":11}`, true},
		{"degree huge — capped", `{"degree":100}`, true},
		{"missing degree", `{}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := newPoly(&types.Feature{
				Type:   types.FEAT_POLY,
				Field:  "x",
				Params: json.RawMessage(c.params),
			}, nil)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if err == nil {
				return
			}
			coded, ok := err.(*errors.CodedError)
			if !ok {
				t.Fatalf("expected CodedError, got %T: %v", err, err)
			}
			if coded.Code != errors.PROCESSING_CONFIG {
				t.Errorf("Code = %s, want PROCESSING_CONFIG", coded.Code)
			}
		})
	}
}

// TestFEAT_POLY_FieldValidation rejects an empty field with
// PROCESSING_CONFIG, matching the diagnostic FEAT_LOG / FEAT_SQRT use in
// the same scenario. A non-numeric field at the schema level is
// rejected upstream by predict; at the per-row level, non-numeric
// records null out via Record.NumericValue (covered by NullPropagation
// below).
func TestFEAT_POLY_FieldValidation(t *testing.T) {
	_, err := newPoly(&types.Feature{
		Type:   types.FEAT_POLY,
		Params: json.RawMessage(`{"degree":2}`),
	}, nil)
	if err == nil {
		t.Fatal("expected error for missing field")
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected CodedError, got %T: %v", err, err)
	}
	if coded.Code != errors.PROCESSING_CONFIG {
		t.Errorf("Code = %s, want PROCESSING_CONFIG", coded.Code)
	}
	if !strings.Contains(coded.Message, "field") {
		t.Errorf("error message should mention 'field', got %q", coded.Message)
	}
}

// TestFEAT_POLY_NullPropagation verifies a null input row produces null
// on every derived column. Mirrors FEAT_LOG / FEAT_SQRT semantics.
func TestFEAT_POLY_NullPropagation(t *testing.T) {
	records := makeRecords([]float64{2, 0, 3}, []bool{false, true, false}, "x")
	c, _ := newPoly(&types.Feature{
		Type:   types.FEAT_POLY,
		Field:  "x",
		Params: json.RawMessage(`{"degree":3}`),
	}, nil)
	out, _ := c.Compute(records, "x")
	got2 := out["x_poly_2"]
	got3 := out["x_poly_3"]
	if got2.Nulls[0] || got2.Nulls[2] {
		t.Errorf("expected non-null at indices 0,2 for x_poly_2; got %v", got2.Nulls)
	}
	if !got2.Nulls[1] {
		t.Error("expected null at index 1 (null input) for x_poly_2")
	}
	if !got3.Nulls[1] {
		t.Error("expected null at index 1 (null input) for x_poly_3")
	}
}

// TestFEAT_POLY_StreamingParity runs the same fixture through buffered
// Compute and the StreamingComputer PrePass/Finalize/EmitRow loop and
// asserts identical outputs to the parity contract every per-row FEAT_*
// honours.
func TestFEAT_POLY_StreamingParity(t *testing.T) {
	records := makeRecords(
		[]float64{0, 1, -2, 3.5, 7, 0},
		[]bool{false, false, false, false, false, true},
		"x",
	)
	feat := &types.Feature{
		Type:   types.FEAT_POLY,
		Field:  "x",
		Params: json.RawMessage(`{"degree":4}`),
	}

	buffered, err := newPoly(feat, nil)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newPoly(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "x")
	assertOutputsEqual(t, "POLY", wantOut, gotOut)
}

// TestFEAT_POLY_RegisteredViaInit confirms the operator is wired into
// the package's init() registry so processing.Apply can dispatch it.
func TestFEAT_POLY_RegisteredViaInit(t *testing.T) {
	if _, ok := Lookup(types.FEAT_POLY); !ok {
		t.Fatal("expected FEAT_POLY to be registered via init()")
	}
}

func keysSorted(m map[string]Output) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Tests above use a small set of keys; alpha order is fine for messages.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
