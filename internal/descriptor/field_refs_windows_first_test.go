package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// field_refs_windows_first_test.go: the walk under the windows-first
// classification (mmm-followups E4-S1, vectors.WindowsFirst) — an
// aggregation, grouper or crosstab axis reading a window or post-window
// label is admitted (it was SERVICE_VALIDATION "unknown field"), the
// windows are judged against the record columns ahead of the
// aggregations, and predict reports the request buffered.

func sumField(field, label string) *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: field, Label: label}
}

// TestWindowsFirst_WalkAdmitsWindowLabels: each output slot reading a
// window label, and an aggregation over a post-window formula label
// (directly and through a chain), walks clean.
func TestWindowsFirst_WalkAdmitsWindowLabels(t *testing.T) {
	win := []*types.Window{lagWindow("lag_n")}
	cases := map[string]*types.Request{
		"aggregation": {Windows: win, Aggregations: []*types.Aggregation{sumField("lag_n", "s")}},
		"aggregation column param": {Windows: win, Aggregations: []*types.Aggregation{{Type: types.AGG_RATIO, Field: "n", Label: "r",
			Params: []byte(`{"numerator_field":"lag_n","denominator_field":"m"}`)}}},
		"grouper": {Windows: win, Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "lag_n", Interval: 5}},
			Aggregations: []*types.Aggregation{sumField("n", "s")}},
		"crosstab axis and cell": {Windows: win, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "lag_n", Interval: 5}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    sumField("lag_n", "c"),
		}},
		"post-window chain by group": {Windows: win,
			Attributes: []*types.Attribute{
				postFormula("sat", "lag_n / (lag_n + 3)"),
				postFormula("contrib", "sat * 0.5"),
			},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{sumField("contrib", "total_contrib"), sumField("n", "n_sum")}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if got := FieldRefRefusals(req, fieldRefSchema(), nil); len(got) != 0 {
				t.Fatalf("refused: %v", got)
			}
		})
	}
}

// TestWindowsFirst_WindowReadingAggregate: a window reading an
// aggregation's output label in a windows-first request is
// PULSE_WINDOW_INVALID windows_first_reads_aggregate (the aggregations
// run after the windows there); on the historical order the same read
// is a window over the aggregated rows, admitted as before.
func TestWindowsFirst_WindowReadingAggregate(t *testing.T) {
	req := &types.Request{
		Windows: []*types.Window{
			lagWindow("lag_n"),
			{Type: types.WIN_LAG, Field: "s_n", Label: "lag_s", OrderBy: []types.OrderKey{{Field: "m"}}},
		},
		Aggregations: []*types.Aggregation{sumField("lag_n", "s_lag"), sumField("n", "s_n")},
	}
	got := FieldRefRefusals(req, fieldRefSchema(), nil)
	if len(got) != 1 || got[0].Code != errors.PULSE_WINDOW_INVALID || got[0].Details["reason"] != "windows_first_reads_aggregate" ||
		got[0].Details["field"] != "s_n" || got[0].Details["window_index"] != 1 {
		t.Fatalf("refusals %v, want windows_first_reads_aggregate on s_n", got)
	}
	historical := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Aggregations: []*types.Aggregation{sumField("n", "s_n")},
		Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "s_n", Label: "lag_s", OrderBy: []types.OrderKey{{Field: "cat"}}}},
	}
	if got := FieldRefRefusals(historical, fieldRefSchema(), nil); len(got) != 0 {
		t.Fatalf("window over aggregated rows refused: %v", got)
	}
}

// TestWindowsFirst_StillRefused: in a windows-first request a weight
// naming a window label stays refused (windows run after every weight
// is resolved), as does a tier-1 test reading one and a sort key over
// the aggregated output rows naming one; a non-row-local
// attribute over a window label is PULSE_ATTRIBUTE_WINDOW_ORDER with the
// record-row why.
func TestWindowsFirst_StillRefused(t *testing.T) {
	base := func() *types.Request {
		return &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Aggregations: []*types.Aggregation{sumField("lag_n", "s")}}
	}
	weighted := base()
	weighted.Weight = &types.WeightSpec{Field: "lag_n"}
	got := FieldRefRefusals(weighted, fieldRefSchema(), nil)
	if len(got) != 1 || got[0].Code != errors.SERVICE_VALIDATION || got[0].Details["window"] != "lag_n" {
		t.Fatalf("window-label weight: %v", got)
	}
	tested := base()
	tested.Tests = []*types.Test{{Type: types.TEST_T, Field: "lag_n"}}
	if got := FieldRefRefusals(tested, fieldRefSchema(), nil); len(got) != 1 || got[0].Details["field"] != "lag_n" {
		t.Fatalf("tier-1 test over a window label: %v", got)
	}
	sorted := base()
	sorted.Sort = []types.OrderKey{{Field: "lag_n"}}
	if got := FieldRefRefusals(sorted, fieldRefSchema(), nil); len(got) != 1 || got[0].Details["sort_index"] != 0 {
		t.Fatalf("sort over a window label beside the aggregated rows: %v", got)
	}
	z := base()
	z.Attributes = []*types.Attribute{postFormula("sat", "lag_n * 2"), {Type: types.ATTR_ZSCORE, Field: "lag_n", Label: "z"}}
	z.Aggregations = []*types.Aggregation{sumField("sat", "s")}
	got = FieldRefRefusals(z, fieldRefSchema(), nil)
	if len(got) != 1 || got[0].Code != errors.PULSE_ATTRIBUTE_WINDOW_ORDER || !strings.Contains(got[0].Message, "only a row-local") {
		t.Fatalf("non-row-local window reader: %v", got)
	}
}

// TestWindowsFirst_PredictStreamableMatchesRuntime: a windows-first
// request (ungrouped, grouped, crosstab) is buffered with the
// windows-first reason, on predict and processing.canStream alike; the
// historical order keeps its reason.
func TestWindowsFirst_PredictStreamableMatchesRuntime(t *testing.T) {
	schema := fieldRefSchema()
	win := []*types.Window{lagWindow("lag_n")}
	cases := []struct {
		name   string
		req    *types.Request
		reason string
	}{
		{"ungrouped", &types.Request{Windows: win, Aggregations: []*types.Aggregation{sumField("lag_n", "s")}}, "windows-first"},
		{"grouped", &types.Request{Windows: win, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{sumField("lag_n", "s")}}, "windows-first"},
		{"crosstab", &types.Request{Windows: win, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "m", Interval: 5}},
			Cell:    sumField("lag_n", "c"),
		}}, "windows-first"},
		{"historical order", &types.Request{Windows: win, Aggregations: []*types.Aggregation{sumField("n", "s")}}, "post-aggregate rows"},
	}
	data := buildTestPulseFile(t, schema)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := predictFromBytes(data, c.req, nil).Data.(*descriptor.PredictResult)
			if runtime := processing.CanStreamRequest(c.req, schema); res.Streamable || runtime {
				t.Fatalf("predict.Streamable=%v runtime=%v, want both buffered", res.Streamable, runtime)
			}
			if !strings.Contains(strings.Join(res.StreamableReasons, "|"), c.reason) {
				t.Fatalf("reasons %v lack %q", res.StreamableReasons, c.reason)
			}
		})
	}
}
