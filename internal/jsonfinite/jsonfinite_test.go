package jsonfinite

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

type inner struct {
	X float64 `json:"x"`
}

type Embedded struct {
	E float64 `json:"e"`
}

type sample struct {
	Embedded
	F       float64            `json:"f"`
	F32     float32            `json:"f32"`
	P       *float64           `json:"p"`
	S       []float64          `json:"s"`
	A       [2]float64         `json:"a"`
	M       map[string]float64 `json:"m"`
	In      inner              `json:"in"`
	Ins     []*inner           `json:"ins"`
	MapIns  map[string]inner   `json:"map_ins"`
	Any     any                `json:"any"`
	Untag   float64
	Skipped float64          `json:"-"`
	W       types.SlotWeight `json:"w"`
}

// A null in every kind of float slot becomes NaN; pointer and any
// slots keep their nil; a self-decoding type is left alone.
func TestUnmarshal_NullFloatIsNaN(t *testing.T) {
	raw := `{"e":null,"f":null,"f32":null,"p":null,"s":[1,null],"a":[null,2],"m":{"k":null,"j":3},
		"in":{"x":null},"ins":[{"x":null},null],"map_ins":{"q":{"x":null}},"any":null,"untag":null,"w":null}`
	var s sample
	if err := Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]float64{
		"e": s.E, "f": s.F, "f32": float64(s.F32), "s[1]": s.S[1], "a[0]": s.A[0], "m.k": s.M["k"],
		"in.x": s.In.X, "ins[0].x": s.Ins[0].X, "map_ins.q.x": s.MapIns["q"].X, "untag": s.Untag,
	} {
		if !math.IsNaN(v) {
			t.Errorf("%s = %v, want NaN", name, v)
		}
	}
	if s.S[0] != 1 || s.A[1] != 2 || s.M["j"] != 3 {
		t.Errorf("defined figures moved: %+v", s)
	}
	if s.P != nil || s.Any != nil || s.Ins[1] != nil {
		t.Errorf("pointer / any slots = %v %v %v, want nil", s.P, s.Any, s.Ins[1])
	}
	if !s.W.IsNull() {
		t.Errorf("slot weight null decoded as %+v, want its own opt-out", s.W)
	}
}

// Input with no null decodes exactly as encoding/json decodes it.
func TestUnmarshal_NoNullIsJSONUnmarshal(t *testing.T) {
	raw := []byte(`{"f":1.5,"s":[1,2],"m":{"k":4},"in":{"x":2},"ins":[{"x":1}],"any":{"z":1}}`)
	var a, b sample
	if err := Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Unmarshal = %+v, json.Unmarshal = %+v", a, b)
	}
	if err := Unmarshal([]byte(`{"f":"x"}`), &a); err == nil {
		t.Error("a type error is not reported")
	}
}

// The round trip a reader of a result depends on: an undefined test
// statistic, p-value and regression figure written as null by the wire
// rule read back as NaN — never as a real 0.
func TestUnmarshal_ResultRoundTrip(t *testing.T) {
	resp := &types.Response{
		Tests: []*types.TestResult{{Type: types.TEST_T, Statistic: math.NaN(), PValue: math.NaN(), Alpha: 0.05}},
		Regressions: []*types.RegressionResult{{Type: types.REG_OLS, R2: math.NaN(),
			Coefficients: map[string]float64{"x": 1}, PValues: map[string]float64{"x": math.NaN()}}},
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var plain, got types.Response
	if err := json.Unmarshal(raw, &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Tests[0].Statistic != 0 {
		t.Fatalf("fixture drift: encoding/json read the null statistic as %v", plain.Tests[0].Statistic)
	}
	if err := Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	tr, rr := got.Tests[0], got.Regressions[0]
	if !math.IsNaN(tr.Statistic) || !math.IsNaN(tr.PValue) || !math.IsNaN(rr.R2) || !math.IsNaN(rr.PValues["x"]) {
		t.Errorf("undefined figures read back as %v / %v / %v / %v", tr.Statistic, tr.PValue, rr.R2, rr.PValues["x"])
	}
	if tr.Alpha != 0.05 || rr.Coefficients["x"] != 1 {
		t.Errorf("defined figures moved: alpha %v, coefficient %v", tr.Alpha, rr.Coefficients["x"])
	}
}
