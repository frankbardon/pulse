package vectors

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
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

// TestWindowsFirst_WindowsOverRecords: the record-row shape and every
// windows-first request run their windows over the records; the
// historical order does not. The decision reads the site's catalog: a
// request windows-first only through a bound model's terms runs them
// over the records for its computed columns, regression reads and
// vector members exactly as for its attributes (mmm-followups E4-S6);
// without the catalog that binds the model it is the historical order
// everywhere.
func TestWindowsFirst_WindowsOverRecords(t *testing.T) {
	cat := model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Terms: []string{"lag"}, Kind: types.FitKindRegression}}
	lag := []*types.Window{lagOf("x", "lag")}
	s := postWindowSchema()
	if !WindowsOverRecords(&types.Request{Windows: lag}, s, nil) {
		t.Error("record-row shape: windows not over the records")
	}
	if WindowsOverRecords(&types.Request{Windows: lag, Aggregations: []*types.Aggregation{sumOf("x")}}, s, cat) {
		t.Error("historical order: windows over the records")
	}
	wf := &types.Request{Windows: lag, Aggregations: []*types.Aggregation{sumOf("lag")}}
	if !WindowsOverRecords(wf, s, nil) {
		t.Error("windows-first: windows not over the records")
	}
	if comp := ComputedColumns(wf, s, nil); comp["lag"].Slot != "window" {
		t.Errorf("windows-first: the window label is no computed record column (%v)", comp)
	}
	scored := &types.Request{Windows: lag,
		Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
		Aggregations: []*types.Aggregation{sumOf("p")},
		Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"lag"}}},
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"x", "lag"}}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Vector: "v"}},
	}
	if !WindowsOverRecords(scored, s, cat) {
		t.Error("catalog-triggered windows-first: windows not over the records")
	}
	if pw := PostWindowAttributes(scored, s, nil, cat); !pw.WindowsFirst || !pw.IsPost(0) || pw.Refusal(scored) != nil {
		t.Errorf("catalog-triggered windows-first: attribute plan %+v", pw)
	}
	if comp := ComputedColumns(scored, s, cat); comp["lag"].Slot != "window" {
		t.Errorf("catalog-triggered: the window label is no computed record column (%v)", comp)
	}
	if got := RegressionWindowReads(scored, s, cat); !reflect.DeepEqual(got, []string{"lag"}) {
		t.Errorf("catalog-triggered regression window reads %v, want [lag]", got)
	}
	if got := MatrixComputedMembers(scored, s, cat); !reflect.DeepEqual(got, []string{"lag"}) {
		t.Errorf("catalog-triggered matrix computed members %v, want [lag]", got)
	}
	if r, err := ResolveRequest(scored, s, cat); err != nil || !reflect.DeepEqual(r[0].Computed, []string{"lag"}) {
		t.Errorf("catalog-triggered ResolveRequest %+v, %v; want v resolved with computed member lag", r, err)
	}
	// Without the binding catalog the model reads nothing: the
	// historical order, the window label no record column.
	if WindowsOverRecords(scored, s, nil) || RegressionWindowReads(scored, s, nil) != nil {
		t.Error("no catalog: windows over the records")
	}
	if _, err := ResolveRequest(scored, s, nil); err == nil {
		t.Error("no catalog: vector v resolved its window member")
	}
	if WindowsOverRecords(nil, s, cat) || WindowsOverRecords(&types.Request{}, s, cat) {
		t.Error("no window: windows over the records")
	}
}

// TestCrosstabWindowRefusal: a crosstab whose windows do not run
// windows-first is refused at its first window (crosstab_window_unread);
// a windows-first crosstab and every other shape are not.
func TestCrosstabWindowRefusal(t *testing.T) {
	ct := &types.CrosstabSpec{Rows: []*types.Group{catOf("t")}, Cell: sumOf("x")}
	req := &types.Request{Windows: []*types.Window{nil, lagOf("x", "lag")}, Crosstab: ct}
	ce := CrosstabWindowRefusal(req, false)
	if ce == nil || ce.Code != errors.PULSE_WINDOW_INVALID || ce.Details["window_index"] != 1 || ce.Details["reason"] != "crosstab_window_unread" {
		t.Fatalf("refusal %v, want PULSE_WINDOW_INVALID at window 1", ce)
	}
	if CrosstabWindowRefusal(req, true) != nil {
		t.Error("windows-first crosstab refused")
	}
	if CrosstabWindowRefusal(&types.Request{Crosstab: ct}, false) != nil {
		t.Error("crosstab without windows refused")
	}
	if CrosstabWindowRefusal(&types.Request{Windows: req.Windows, Aggregations: []*types.Aggregation{sumOf("x")}}, false) != nil {
		t.Error("non-crosstab request refused")
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

// TestWindowsFirstReads: the window labels the windows-first engine
// copies onto the records — every aggregation (field and column param),
// grouper and crosstab axis / cell / margin read of a window output, in
// first-read order, once each; never a schema field, a post-window
// attribute label, or anything in the record-row shape.
func TestWindowsFirstReads(t *testing.T) {
	wins := []*types.Window{lagOf("x", "lag"), lagOf("t", "lag_t"), lagOf("x", "x")}
	cases := []struct {
		name string
		req  *types.Request
		want []string
	}{
		{"record-row shape", &types.Request{Windows: wins}, nil},
		{"aggregations then column params", &types.Request{Windows: wins, Attributes: []*types.Attribute{formula("sat", "lag * 2")},
			Aggregations: []*types.Aggregation{sumOf("x"), sumOf("sat"), {Type: types.AGG_RATIO, Field: "lag_t", Label: "r",
				Params: json.RawMessage(`{"numerator_field":"lag","denominator_field":"lag_t"}`)}, sumOf("lag")}}, []string{"lag_t", "lag"}},
		{"groupers", &types.Request{Windows: wins, Groups: []*types.Group{catOf("lag_t")}, Aggregations: []*types.Aggregation{sumOf("lag")}},
			[]string{"lag", "lag_t"}},
		{"crosstab", &types.Request{Windows: wins, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{catOf("t")}, Columns: []*types.Group{catOf("lag_t")},
			Cell: sumOf("lag"), MarginAggregations: []*types.Aggregation{sumOf("lag_t")}}}, []string{"lag_t", "lag"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WindowsFirstReads(c.req, postWindowSchema()); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("WindowsFirstReads = %v, want %v", got, c.want)
			}
		})
	}
}
