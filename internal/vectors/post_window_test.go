package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// post_window_test.go pins PostWindowAttributes, the one post-window
// classification the walk and the engine read (transformed-predictors
// E2-S1).

func postWindowSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeF64},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
}

func lagOf(field, label string) *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: field, Label: label, OrderBy: []types.OrderKey{{Field: "t"}}}
}

func formula(label, expr string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Expression: expr}
}

// TestWindowPredictor_PostWindowClassification: direct and transitive
// reads move; a pre-window label read beside one does not move its
// producer; ineligible and out-of-shape readers are dependent but stay
// (the refusal's to judge); a forward read is not a dependency; the
// window reads list only what the moved slots read.
func TestWindowPredictor_PostWindowClassification(t *testing.T) {
	t.Parallel()

	cat := model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Terms: []string{"x", "lag2"}, Kind: types.FitKindRegression}}
	req := &types.Request{
		Windows: []*types.Window{lagOf("x", "lag"), lagOf("x", "lag2"), lagOf("x", "unread")},
		Attributes: []*types.Attribute{
			formula("fwd", "sat + 1"),                             // 0: forward read — not a dependency
			formula("sat", "hill(lag, 3, 2)"),                     // 1: direct
			formula("pre", "x * 2"),                               // 2: stays
			formula("chain", "sat * pre"),                         // 3: through sat
			{Type: types.ATTR_ZSCORE, Field: "chain", Label: "z"}, // 4: dependent, ineligible
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}, // 5: model term lag2
			formula("share", "chain / total(chain)"),                                           // 6: total() is not row-local
		},
	}
	pw := PostWindowAttributes(req, postWindowSchema(), nil, cat)
	if want := []bool{false, true, false, true, false, true, false}; !reflect.DeepEqual(pw.Post, want) {
		t.Errorf("Post = %v, want %v", pw.Post, want)
	}
	if want := []string{"", "lag", "", "sat", "chain", "lag2", "chain"}; !reflect.DeepEqual(pw.Dep, want) {
		t.Errorf("Dep = %q, want %q", pw.Dep, want)
	}
	if want := []string{"lag", "lag2"}; !reflect.DeepEqual(pw.WindowReads, want) {
		t.Errorf("WindowReads = %v, want %v", pw.WindowReads, want)
	}
	if want := []string{"sat", "chain", "p"}; !reflect.DeepEqual(pw.Labels, want) {
		t.Errorf("Labels = %v, want %v", pw.Labels, want)
	}
	if ce := pw.Refusal(req); ce == nil || ce.Details["attribute"] != "ATTR_ZSCORE" || ce.Details["field"] != "chain" {
		t.Errorf("Refusal = %v, want the ATTR_ZSCORE reading chain", ce)
	}

	// Without the model bound, ATTR_REG_PREDICT reads nothing knowable.
	if pw := PostWindowAttributes(req, postWindowSchema(), nil, nil); pw.IsPost(5) || pw.DepOf(5) != "" {
		t.Errorf("unbound model classified: %+v", pw)
	}
	// A route hiding ATTR_FORMULA makes it ineligible (still dependent).
	hide := func(at types.AttributeType) types.AttributeType {
		if at == types.ATTR_FORMULA {
			return ""
		}
		return at
	}
	if pw := PostWindowAttributes(req, postWindowSchema(), hide, cat); pw.IsPost(1) || pw.DepOf(1) != "lag" {
		t.Errorf("hidden formula moved: %+v", pw)
	}
}

// TestWindowPredictor_PostWindowShapeAndShadow: outside the record-row
// shape nothing moves (every reader is dependent); a window label equal
// to a schema field is no window-derived name.
func TestWindowPredictor_PostWindowShapeAndShadow(t *testing.T) {
	t.Parallel()

	grouped := &types.Request{
		Windows:      []*types.Window{lagOf("x", "lag")},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "x"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
		Attributes:   []*types.Attribute{formula("f", "lag * 2")},
	}
	pw := PostWindowAttributes(grouped, postWindowSchema(), nil, nil)
	if pw.Any() || pw.DepOf(0) != "lag" || pw.Refusal(grouped) == nil {
		t.Errorf("grouped plan %+v, want no move and a dependent slot", pw)
	}
	shadow := &types.Request{Windows: []*types.Window{lagOf("x", "x")}, Attributes: []*types.Attribute{formula("f", "x * 2")}}
	if pw := PostWindowAttributes(shadow, postWindowSchema(), nil, nil); pw.Any() || pw.DepOf(0) != "" {
		t.Errorf("schema-field window label classified: %+v", pw)
	}
	// A window label equal to a vector name: the expression reads the
	// vector, and the collision is PULSE_VECTOR_NAME_COLLISION's.
	vec := &types.Request{Vectors: []types.VectorSpec{{Name: "lag", Fields: []string{"t", "x"}}},
		Windows: []*types.Window{lagOf("x", "lag")}, Attributes: []*types.Attribute{formula("f", "vmean(lag)")}}
	if pw := PostWindowAttributes(vec, postWindowSchema(), nil, nil); pw.Any() || pw.DepOf(0) != "" {
		t.Errorf("vector-named window label classified: %+v", pw)
	}
}

// TestWindowPredictor_PostWindowZeroPlanAllocationFree: a request with
// no window or no attribute classifies to the zero plan without
// allocating — the historical path's cost.
func TestWindowPredictor_PostWindowZeroPlanAllocationFree(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	s := postWindowSchema()
	noWindow := &types.Request{Attributes: []*types.Attribute{formula("f", "x * 2")}}
	noAttr := &types.Request{Windows: []*types.Window{lagOf("x", "lag")}}
	for _, req := range []*types.Request{noWindow, noAttr} {
		if n := testing.AllocsPerRun(100, func() { _ = PostWindowAttributes(req, s, nil, nil) }); n != 0 {
			t.Errorf("%d allocations, want 0", int(n))
		}
		if pw := PostWindowAttributes(req, s, nil, nil); !reflect.DeepEqual(pw, PostWindow{}) {
			t.Errorf("plan %+v, want zero", pw)
		}
	}
}

// TestWindowPredictor_PostWindowPerSlotReads: each moved slot records
// the window labels it reads directly, in read order without repeats —
// empty when it reads only an earlier post-window label — and
// WindowDerivedLabels lists every window label but a schema-field or
// vector-named one, in declared order.
func TestWindowPredictor_PostWindowPerSlotReads(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "vec", Fields: []string{"t", "x"}}},
		Windows: []*types.Window{lagOf("x", "lag"), lagOf("x", "x"), lagOf("t", "lag_t"), lagOf("x", "vec")},
		Attributes: []*types.Attribute{
			formula("a", "lag_t + lag + lag_t"),
			formula("b", "a * 2"),
			formula("c", "lag + b"),
		},
	}
	pw := PostWindowAttributes(req, postWindowSchema(), nil, nil)
	want := [][]string{{"lag_t", "lag"}, {}, {"lag"}}
	if !reflect.DeepEqual(pw.PostReads, want) || !reflect.DeepEqual(pw.WindowReads, []string{"lag_t", "lag"}) {
		t.Fatalf("per-slot reads %v (union %v), want %v", pw.PostReads, pw.WindowReads, want)
	}
	if got := WindowDerivedLabels(req, postWindowSchema()); !reflect.DeepEqual(got, []string{"lag", "lag_t"}) {
		t.Fatalf("window-derived labels %v, want [lag lag_t]", got)
	}
	if got := WindowDerivedLabels(&types.Request{}, postWindowSchema()); got != nil {
		t.Fatalf("no window: %v", got)
	}
}
