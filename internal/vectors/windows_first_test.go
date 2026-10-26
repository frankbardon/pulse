package vectors

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// windows_first_test.go pins WindowsFirst, the one windows-first
// classification (mmm-followups E4-S1), and the sites extended through
// it: WindowsOverRecords, PostWindowAttributes and the
// PULSE_ATTRIBUTE_WINDOW_ORDER text.

func sumOf(field string) *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: field, Label: "s_" + field}
}

func catOf(field string) *types.Group {
	return &types.Group{Type: types.GROUP_CATEGORY, Field: field}
}

// TestWindowsFirst_Classification: the mode is on iff an aggregation
// (field or column param), grouper or crosstab axis / cell / margin
// aggregation reads a window label or a post-window attribute label
// (transitively); never in the record-row shape; never through a
// non-row-local reader, a shadowed label or an unbound model.
func TestWindowsFirst_Classification(t *testing.T) {
	cat := model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Terms: []string{"x", "lag"}, Kind: types.FitKindRegression}}
	lag := []*types.Window{lagOf("x", "lag")}
	ct := func(mut func(*types.CrosstabSpec)) *types.CrosstabSpec {
		s := &types.CrosstabSpec{Rows: []*types.Group{catOf("t")}, Columns: []*types.Group{catOf("x")}}
		mut(s)
		return s
	}
	cases := []struct {
		name string
		req  *types.Request
		cat  model.Catalog
		want bool
	}{
		{"no window", &types.Request{Aggregations: []*types.Aggregation{sumOf("x")}}, nil, false},
		{"record-row shape", &types.Request{Windows: lag, Attributes: []*types.Attribute{formula("f", "lag * 2")}}, nil, false},
		{"aggregation over a field (historical order)", &types.Request{Windows: lag, Aggregations: []*types.Aggregation{sumOf("x")}}, nil, false},
		{"aggregation over a window label", &types.Request{Windows: lag, Aggregations: []*types.Aggregation{sumOf("lag")}}, nil, true},
		{"aggregation column param", &types.Request{Windows: lag, Aggregations: []*types.Aggregation{{Type: types.AGG_RATIO, Label: "r",
			Params: json.RawMessage(`{"numerator_field":"x","denominator_field":"lag"}`)}}}, nil, true},
		{"grouper over a window label", &types.Request{Windows: lag, Groups: []*types.Group{catOf("lag")}, Aggregations: []*types.Aggregation{sumOf("x")}}, nil, true},
		{"crosstab row axis", &types.Request{Windows: lag, Crosstab: ct(func(s *types.CrosstabSpec) { s.Rows = []*types.Group{catOf("lag")} })}, nil, true},
		{"crosstab cell", &types.Request{Windows: lag, Crosstab: ct(func(s *types.CrosstabSpec) { s.Cell = sumOf("lag") })}, nil, true},
		{"crosstab margin", &types.Request{Windows: lag, Crosstab: ct(func(s *types.CrosstabSpec) { s.MarginAggregations = []*types.Aggregation{sumOf("lag")} })}, nil, true},
		{"crosstab over fields", &types.Request{Windows: lag, Crosstab: ct(func(s *types.CrosstabSpec) { s.Cell = sumOf("x") })}, nil, false},
		{"post-window formula label", &types.Request{Windows: lag, Attributes: []*types.Attribute{formula("sat", "lag / (lag + 3)")},
			Aggregations: []*types.Aggregation{sumOf("sat")}}, nil, true},
		{"post-window chain", &types.Request{Windows: lag, Attributes: []*types.Attribute{formula("sat", "lag * 2"), formula("contrib", "sat * 0.5")},
			Groups: []*types.Group{catOf("t")}, Aggregations: []*types.Aggregation{sumOf("contrib")}}, nil, true},
		{"pre-window attribute label", &types.Request{Windows: lag, Attributes: []*types.Attribute{formula("dbl", "x * 2")},
			Aggregations: []*types.Aggregation{sumOf("dbl")}}, nil, false},
		{"non-row-local reader", &types.Request{Windows: lag, Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "lag", Label: "z"}},
			Aggregations: []*types.Aggregation{sumOf("z")}}, nil, false},
		{"total() reader", &types.Request{Windows: lag, Attributes: []*types.Attribute{formula("share", "lag / total(lag)")},
			Aggregations: []*types.Aggregation{sumOf("share")}}, nil, false},
		{"bound model score", &types.Request{Windows: lag, Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
			Aggregations: []*types.Aggregation{sumOf("p")}}, cat, true},
		{"unbound model score", &types.Request{Windows: lag, Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
			Aggregations: []*types.Aggregation{sumOf("p")}}, nil, false},
		{"window label shadowing a schema field", &types.Request{Windows: []*types.Window{lagOf("t", "x")}, Aggregations: []*types.Aggregation{sumOf("x")}}, nil, false},
		{"window label naming a vector", &types.Request{Vectors: []types.VectorSpec{{Name: "lag", Fields: []string{"t", "x"}}}, Windows: lag,
			Aggregations: []*types.Aggregation{sumOf("lag")}}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WindowsFirst(c.req, postWindowSchema(), nil, c.cat); got != c.want {
				t.Fatalf("WindowsFirst = %v, want %v", got, c.want)
			}
			if got := PostWindowAttributes(c.req, postWindowSchema(), nil, c.cat).WindowsFirst; got != c.want {
				t.Fatalf("PostWindow.WindowsFirst = %v, want %v", got, c.want)
			}
		})
	}
}

// TestWindowsFirst_WindowsOverRecords: the catalog-free view — the
// record-row shape and every windows-first request run their windows
// over the records; the historical order does not; a request
// windows-first only through a bound model's terms does not here (it
// does for its attributes, which take the catalog).
func TestWindowsFirst_WindowsOverRecords(t *testing.T) {
	cat := model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Terms: []string{"lag"}, Kind: types.FitKindRegression}}
	lag := []*types.Window{lagOf("x", "lag")}
	s := postWindowSchema()
	if !WindowsOverRecords(&types.Request{Windows: lag}, s) {
		t.Error("record-row shape: windows not over the records")
	}
	if WindowsOverRecords(&types.Request{Windows: lag, Aggregations: []*types.Aggregation{sumOf("x")}}, s) {
		t.Error("historical order: windows over the records")
	}
	wf := &types.Request{Windows: lag, Aggregations: []*types.Aggregation{sumOf("lag")}}
	if !WindowsOverRecords(wf, s) {
		t.Error("windows-first: windows not over the records")
	}
	if comp := ComputedColumns(wf, s); comp["lag"].Slot != "window" {
		t.Errorf("windows-first: the window label is no computed record column (%v)", comp)
	}
	scored := &types.Request{Windows: lag,
		Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
		Aggregations: []*types.Aggregation{sumOf("p")}}
	if WindowsOverRecords(scored, s) {
		t.Error("catalog-only windows-first: the catalog-free view moved")
	}
	if pw := PostWindowAttributes(scored, s, nil, cat); !pw.WindowsFirst || !pw.IsPost(0) || pw.Refusal(scored) != nil {
		t.Errorf("catalog-only windows-first: attribute plan %+v", pw)
	}
	if WindowsOverRecords(nil, s) || WindowsOverRecords(&types.Request{}, s) {
		t.Error("no window: windows over the records")
	}
}

// TestWindowsFirst_PostWindowPlan: in a windows-first request the
// row-local window readers move after the windows (they ran
// PULSE_ATTRIBUTE_WINDOW_ORDER before), a non-row-local one is still
// refused with the record-row why, and a request with no attribute
// still carries the mode.
func TestWindowsFirst_PostWindowPlan(t *testing.T) {
	req := &types.Request{
		Windows: []*types.Window{lagOf("x", "lag")},
		Attributes: []*types.Attribute{
			formula("sat", "lag * 2"),
			{Type: types.ATTR_ZSCORE, Field: "lag", Label: "z"},
		},
		Groups:       []*types.Group{catOf("t")},
		Aggregations: []*types.Aggregation{sumOf("sat")},
	}
	pw := PostWindowAttributes(req, postWindowSchema(), nil, nil)
	if !pw.WindowsFirst || !pw.IsPost(0) || pw.IsPost(1) || !reflect.DeepEqual(pw.WindowReads, []string{"lag"}) || !reflect.DeepEqual(pw.Labels, []string{"sat"}) {
		t.Fatalf("plan %+v", pw)
	}
	ce := pw.Refusal(req)
	if ce == nil || ce.Details["attribute"] != "ATTR_ZSCORE" || !strings.Contains(ce.Message, "only a row-local") {
		t.Fatalf("refusal %v, want the ATTR_ZSCORE with the record-row why", ce)
	}
	noAttr := &types.Request{Windows: req.Windows, Aggregations: []*types.Aggregation{sumOf("lag")}}
	if pw := PostWindowAttributes(noAttr, postWindowSchema(), nil, nil); !reflect.DeepEqual(pw, PostWindow{WindowsFirst: true}) {
		t.Fatalf("no-attribute plan %+v, want only the mode", pw)
	}
}

// TestWindowsFirst_WindowOrderTextNamesRoutes: the refusal names the
// windows-first route and materialize-first, with the historical why on
// the historical order.
func TestWindowsFirst_WindowOrderTextNamesRoutes(t *testing.T) {
	req := &types.Request{Windows: []*types.Window{lagOf("x", "lag")}, Aggregations: []*types.Aggregation{sumOf("x")}}
	ce := AttributeWindowOrderRefusal(req, false, &types.Attribute{Type: types.ATTR_ZSCORE}, "lag")
	for _, want := range []string{"grouped or aggregated output rows", "aggregate, group or cross-tabulate that label", "materialize the window first"} {
		if !strings.Contains(ce.Message, want) {
			t.Errorf("message %q lacks %q", ce.Message, want)
		}
	}
	if ce := AttributeWindowOrderRefusal(req, true, &types.Attribute{Type: types.ATTR_ZSCORE}, "lag"); !strings.Contains(ce.Message, "only a row-local") {
		t.Errorf("windows-first message %q", ce.Message)
	}
}

// TestAggregationColumnReads: Field, then each set built-in column
// param in key order; malformed params read Field alone.
func TestAggregationColumnReads(t *testing.T) {
	cases := []struct {
		agg  *types.Aggregation
		want []string
	}{
		{nil, nil},
		{sumOf("x"), []string{"x"}},
		{&types.Aggregation{Type: types.AGG_RATIO, Params: json.RawMessage(`{"denominator_field":"d","numerator_field":"n"}`)}, []string{"n", "d"}},
		{&types.Aggregation{Type: types.AGG_WEIGHTED_MEAN, Field: "x", Params: json.RawMessage(`{"weight_field":"w"}`)}, []string{"x", "w"}},
		{&types.Aggregation{Type: types.AGG_RATIO, Field: "x", Params: json.RawMessage(`[1]`)}, []string{"x"}},
		{&types.Aggregation{Type: types.AGG_SUM, Field: "x", Params: json.RawMessage(`{"numerator_field":"n"}`)}, []string{"x"}},
	}
	for i, c := range cases {
		if got := AggregationColumnReads(c.agg); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
	}
}
