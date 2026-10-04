package types

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// finiteSentinel is a finite float no fixture uses, so the slow path's
// output can be compared with json.Marshal's by substitution.
const finiteSentinel = 7.25e37 // representable as float32 too

type finiteInner struct {
	A float64 `json:"a"`
	B []any   `json:"b,omitempty"`
}

type finiteValueMarshaler struct{ X float64 }

func (v finiteValueMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"value-receiver"`), nil }

type finitePtrMarshaler struct{ X float64 }

func (v *finitePtrMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"ptr-receiver"`), nil }

type finiteEmbedded struct {
	E float64 `json:"e"`
}

type finiteFixture struct {
	finiteEmbedded
	Name     string   `json:"name"`
	HTML     string   `json:"html"`
	F64      float64  `json:"f64"`
	F32      float32  `json:"f32"`
	OmitF    float64  `json:"omit_f,omitempty"`
	OmitZero float64  `json:"omit_zero,omitzero"`
	Ptr      *float64 `json:"ptr,omitempty"`
	NilPtr   *float64 `json:"nil_ptr,omitempty"`
	Skipped  float64  `json:"-"`
	Untagged float64
	Any      any                  `json:"any"`
	Map      map[string]any       `json:"map"`
	IntKeys  map[int]float64      `json:"int_keys"`
	Floats   []float64            `json:"floats"`
	Pair     [2]float64           `json:"pair"`
	Inner    finiteInner          `json:"inner"`
	InnerPtr *finiteInner         `json:"inner_ptr"`
	Value    finiteValueMarshaler `json:"value"`
	PtrM     finitePtrMarshaler   `json:"ptr_m"`
	Empty    []string             `json:"empty,omitempty"`
	unexp    float64
}

// buildFiniteFixture fills every float slot with f.
func buildFiniteFixture(f float64) *finiteFixture {
	p := f
	return &finiteFixture{
		finiteEmbedded: finiteEmbedded{E: f},
		Name:           "n", HTML: "<a & b>",
		F64: f, F32: float32(f), OmitF: f, OmitZero: f, Ptr: &p,
		Skipped: f, Untagged: f,
		Any:      []any{1, "x", f, map[string]any{"k": f}},
		Map:      map[string]any{"z": f, "a": 1.5, "m": []float64{f, 2}},
		IntKeys:  map[int]float64{10: f, 2: 3},
		Floats:   []float64{1, f},
		Pair:     [2]float64{f, 4},
		Inner:    finiteInner{A: f, B: []any{f}},
		InnerPtr: &finiteInner{A: f},
		Value:    finiteValueMarshaler{X: f},
		PtrM:     finitePtrMarshaler{X: f},
		unexp:    f,
	}
}

// TestMarshalFinite_FastPathIsJSONMarshal: with no non-finite float the
// bytes are json.Marshal's exactly.
func TestMarshalFinite_FastPathIsJSONMarshal(t *testing.T) {
	v := buildFiniteFixture(1.5)
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalFinite(v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("finite value drifted from json.Marshal\n got  %s\n want %s", got, want)
	}
}

// TestMarshalFinite_SlowPathMatchesJSONMarshal: a value holding NaN /
// ±Inf encodes exactly as json.Marshal encodes the same value with a
// finite sentinel in those slots, the sentinel replaced by null — same
// key order, same omitempty / omitzero / "-" / embedding /
// marshaller-receiver / HTML-escape behaviour.
func TestMarshalFinite_SlowPathMatchesJSONMarshal(t *testing.T) {
	ref, err := json.Marshal(buildFiniteFixture(finiteSentinel))
	if err != nil {
		t.Fatal(err)
	}
	sentinel, _ := json.Marshal(finiteSentinel)
	if f32, _ := json.Marshal(float32(finiteSentinel)); !bytes.Equal(f32, sentinel) {
		t.Fatalf("sentinel encodes differently as float32 (%s vs %s)", f32, sentinel)
	}
	want := strings.ReplaceAll(string(ref), string(sentinel), "null")
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := json.Marshal(buildFiniteFixture(bad)); err == nil {
			t.Fatalf("json.Marshal accepted %v; the fixture is not exercising the slow path", bad)
		}
		got, err := MarshalFinite(buildFiniteFixture(bad))
		if err != nil {
			t.Fatalf("MarshalFinite(%v): %v", bad, err)
		}
		if string(got) != want {
			t.Errorf("MarshalFinite(%v)\n got  %s\n want %s", bad, got, want)
		}
		if !json.Valid(got) {
			t.Errorf("MarshalFinite(%v) produced invalid JSON: %s", bad, got)
		}
	}
}

// TestMarshalFinite_PublicResultTypes: every result type that carries a
// float or an open slot marshals under the rule through plain
// json.Marshal — the embedder's spelling — with the undefined figure
// null in place and the key kept.
func TestMarshalFinite_PublicResultTypes(t *testing.T) {
	nan := math.NaN()
	inf := math.Inf(1)
	summary := OverlaySummary{Statistic: &nan, Parameters: map[string]float64{"df": inf}}
	matrix := &MatrixPayload{Cells: [][]MatrixCell{{{Value: nan, Present: true}}}, GrandTotal: MatrixCell{Value: inf, Present: true}}
	layer := OverlayLayer{Name: "l", Payload: OverlayPayload{Shape: "series", Series: &SeriesPayload{
		Entries: []SeriesEntry{{Key: AxisKey{1}, Summary: summary}}}}}
	aggComp := AggregationComponents{Label: "r", SumWeights: &nan, Operator: map[string]any{"ratio": nan}}
	xtComp := &CrosstabComponents{CellComponents: [][]map[string]any{{{"ratio": nan}}},
		GrandTotalAggregations: map[string]MarginAggregationFigure{"aux": {Value: nan, Present: true}}}
	test := &TestResult{Type: "TEST_T", Statistic: nan, DF: nan, PValue: inf}
	resp := &Response{
		Data:        []map[string]any{{"g": 0, "r": nan}},
		Tests:       []*TestResult{test},
		Regressions: []*RegressionResult{{R2: nan, Coefficients: map[string]float64{"x": inf}}},
		Crosstab:    &CrosstabResult{Shape: "matrix", Matrix: matrix},
		Overlays:    []OverlayLayer{layer},
		Components:  &ResponseComponents{Aggregations: []AggregationComponents{aggComp}, Crosstab: xtComp},
		Warnings:    []*ResponseWarning{{Code: "C", Details: map[string]any{"v": nan}}},
	}
	facet := &FacetField{Kind: "numeric", Numeric: &FacetNumeric{Mean: nan}}
	cases := map[string]struct {
		v     any
		nulls []string
	}{
		"Response":              {resp, []string{`"r":null`, `"statistic":null`, `"df":null`, `"p_value":null`, `"r2":null`, `"x":null`, `"value":null`, `"ratio":null`, `"sum_weights":null`, `"v":null`}},
		"ComposedResponse":      {&ComposedResponse{Responses: []*Response{resp}, Overlays: []OverlayLayer{layer}}, []string{`"r":null`, `"statistic":null`}},
		"ChainResponse":         {&ChainResponse{Stages: []*Response{resp}, Final: resp, Overlays: []*OverlayLayer{&layer}}, []string{`"r":null`, `"statistic":null`}},
		"FacetResult":           {&FacetResult{Fields: map[string]*FacetField{"x": facet}, Overlays: []OverlayLayer{layer}}, []string{`"mean":null`, `"statistic":null`}},
		"FacetField":            {facet, []string{`"mean":null`}},
		"OverlayLayer":          {layer, []string{`"statistic":null`, `"df":null`}},
		"OverlayPayload":        {OverlayPayload{Shape: "scalar", Scalar: &nan}, []string{`"scalar":null`}},
		"OverlaySummary":        {summary, []string{`"statistic":null`}},
		"CrosstabResult":        {resp.Crosstab, []string{`"value":null`}},
		"MatrixPayload":         {matrix, []string{`"value":null`}},
		"ResponseComponents":    {resp.Components, []string{`"ratio":null`}},
		"AggregationComponents": {aggComp, []string{`"ratio":null`, `"sum_weights":null`}},
		"GrouperComponents":     {GrouperComponents{Field: "g", Operator: map[string]any{"edge": inf}}, []string{`"edge":null`}},
		"CrosstabComponents":    {xtComp, []string{`"ratio":null`, `"value":null`}},
		"TestResult":            {test, []string{`"statistic":null`, `"df":null`, `"p_value":null`}},
		"RegressionResult":      {resp.Regressions[0], []string{`"r2":null`, `"x":null`}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if !json.Valid(body) {
				t.Fatalf("invalid JSON: %s", body)
			}
			for _, want := range tc.nulls {
				if !strings.Contains(string(body), want) {
					t.Errorf("missing %s in %s", want, body)
				}
			}
		})
	}
}

// TestMarshalFinite_Untyped covers the untyped fragments the CLI and MCP
// hand over directly (a streamed Row, a {"index","row"} wrapper).
func TestMarshalFinite_Untyped(t *testing.T) {
	row := map[string]any{"g": 1, "r": math.NaN(), "list": []any{math.Inf(-1), 2.5}}
	body, err := MarshalFinite(map[string]any{"index": 0, "row": row})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"index":0,"row":{"g":1,"list":[null,2.5],"r":null}}`; string(body) != want {
		t.Errorf("got %s, want %s", body, want)
	}
	if body, err := MarshalFinite(nil); err != nil || string(body) != "null" {
		t.Errorf("MarshalFinite(nil) = %s, %v", body, err)
	}
}
