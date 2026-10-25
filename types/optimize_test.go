package types

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/returnplan"
)

// TestOptimize_ComposedOptimizeFreeByteIdentity: a ComposedRequest /
// ComposedResponse without an optimize block marshals and hashes exactly
// as the shape before the slot existed; a set block moves both.
func TestOptimize_ComposedOptimizeFreeByteIdentity(t *testing.T) {
	type preOptimizeComposedRequest struct {
		Requests []*Request `json:"requests"`
		Fits     []FitSpec  `json:"fits,omitempty"`
		Params   Params     `json:"params,omitempty"`
	}
	reqs := []*Request{{Label: "a", Cohort: &Cohort{Filename: "a.pulse"}, Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}}}}
	cur := &ComposedRequest{Requests: reqs, Params: Params{"k": 1}}
	legacy := &preOptimizeComposedRequest{Requests: reqs, Params: Params{"k": 1}}
	got, _ := json.Marshal(cur)
	want, _ := json.Marshal(legacy)
	if string(got) != string(want) {
		t.Fatalf("wire diverged:\n got %s\nwant %s", got, want)
	}
	if h, w := cur.Hash(), CanonicalHash("composed", legacy); h != w {
		t.Fatalf("Hash diverged: got %s want %s", h, w)
	}
	opt := *cur
	opt.Optimize = []OptimizeSpec{{Name: "o", Objective: "x", Direction: OptimizeMinimize,
		Variables: []OptimizeVariable{{Name: "x", Lower: BoundValue(0), Upper: BoundValue(1)}}}}
	if opt.Hash() == cur.Hash() {
		t.Fatal("an optimize block does not reach the hash")
	}
	if b, _ := json.Marshal(&opt); !strings.Contains(string(b), `"optimize":[{`) {
		t.Fatalf("optimize absent from the wire: %s", b)
	}

	b, err := json.Marshal(&ComposedResponse{Responses: []*Response{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"responses":[]}` {
		t.Fatalf("optimize-free response: %s", b)
	}
}

// TestOptimize_BoundWire: a bound is a JSON number or "params.<name>";
// it round-trips, an absent start is omitted, and any other shape is a
// decode error.
func TestOptimize_BoundWire(t *testing.T) {
	var v OptimizeVariable
	if err := json.Unmarshal([]byte(`{"name":"tv","lower":"params.tv_lo","upper":250.5}`), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Lower.IsParam() || v.Lower.Param() != "tv_lo" || v.Upper.IsParam() || v.Upper.Value() != 250.5 || !v.Start.IsZero() {
		t.Fatalf("decoded %+v", v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"name":"tv","lower":"params.tv_lo","upper":250.5}` {
		t.Fatalf("re-encoded %s", b)
	}
	v.Start = BoundValue(0)
	if b, _ = json.Marshal(v); !strings.Contains(string(b), `"start":0`) {
		t.Fatalf("a zero start is dropped: %s", b)
	}
	for _, bad := range []string{`"tv_lo"`, `"params."`, `true`, `[1]`, `{}`} {
		var bnd OptimizeBound
		if err := json.Unmarshal([]byte(bad), &bnd); err == nil {
			t.Errorf("%s decoded as %+v", bad, bnd)
		}
	}
	var bnd OptimizeBound
	if err := json.Unmarshal([]byte(`null`), &bnd); err != nil || !bnd.IsZero() {
		t.Fatalf("null: %+v %v", bnd, err)
	}
}

// TestOptimize_ResultWire: a result writes its figures with a non-finite
// one as null; a failed block writes {name, direction, error} only; a
// starts count is the default when unset.
func TestOptimize_ResultWire(t *testing.T) {
	r := OptimizeResult{
		Name: "o", Direction: OptimizeMaximize, Objective: math.NaN(),
		Variables: []OptimizeVariableResult{{Name: "x", Value: 1, Marginal: 0.5, Active: OptimizeActiveUpper}},
		Converged: true, Status: "Optimization terminated successfully", Starts: 8, StartsAgreeing: 8, RNGSeed: 7,
		Constraints: []OptimizeConstraintResult{{Expr: "1 - x", Type: OptimizeConstraintIneq, Value: 0, Multiplier: -0.5}},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"objective":null`) || !strings.Contains(string(b), `"active":"upper"`) {
		t.Fatalf("result %s", b)
	}
	r.Error = &SlotError{Code: "PULSE_OPTIMIZE_INFEASIBLE", Message: "m"}
	b, _ = json.Marshal(r)
	if string(b) != `{"name":"o","direction":"maximize","error":{"code":"PULSE_OPTIMIZE_INFEASIBLE","message":"m"}}` {
		t.Fatalf("failed block %s", b)
	}
	var s OptimizeSpec
	if s.EffectiveStarts() != OptimizeDefaultStarts {
		t.Fatal("default starts")
	}
	n := 3
	s.Starts = &n
	if s.EffectiveStarts() != 3 {
		t.Fatal("set starts")
	}
}

// TestOptimize_PinnedOutsideComposeReturn: a Compose-level `return`
// selecting only the overlays at precision 1 still writes the
// optimizations, whole and unrounded — like `fits`.
func TestOptimize_PinnedOutsideComposeReturn(t *testing.T) {
	r := ComposedResponse{
		Responses: []*Response{},
		Optimizations: []OptimizeResult{{Name: "o", Direction: OptimizeMinimize, Objective: 1.23456789,
			Variables: []OptimizeVariableResult{{Name: "x", Value: 0.987654321, Marginal: 1e-9}}, Starts: 8, StartsAgreeing: 7, RNGSeed: 3}},
	}
	var roots []string
	for _, f := range jsonFields(reflect.ValueOf(ComposedResponse{})) {
		roots = append(roots, f.name)
	}
	r.plan = returnplan.New("minimal", []returnplan.Path{{Segments: []returnplan.Segment{returnplan.Key("overlays")}}}, nil, nil, 1, roots)
	if r.plan.Identity() || r.plan.SelectsAll {
		t.Fatal("the plan is the identity; the test would not exercise the planned encoder")
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"objective":1.23456789`, `"value":0.987654321`, `"starts_agreeing":7`, `"rng_seed":3`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("shaped wire lacks %s: %s", want, b)
		}
	}
}
