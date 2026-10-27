package mcp

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestBind_ColumnInputItems: an aggregation or grouper item reads field,
// vector or fields — a strict draft 2020-12 client accepts a vector
// aggregation / grouper without `field` and still rejects an item that
// names no column at all; an attribute item needs only its type
// (ATTR_FORMULA's expression, a vector attribute's vector). Checked over
// pulse_process, a pulse_compose slot and a pulse_process_chain stage,
// plus the crosstab cell / margin / axis items.
func TestBind_ColumnInputItems(t *testing.T) {
	t.Parallel()

	_, inst := instanceFor(t, nil, pulse.Extensions{})
	bound, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"scalar aggregation", `"aggregations": [{"type": "AGG_SUM", "field": "score"}]`, true},
		{"vector aggregation", `"aggregations": [{"type": "AGG_VEC_MEAN", "vector": "battery", "params": {"expand": true}}]`, true},
		{"inline fields aggregation", `"aggregations": [{"type": "AGG_VEC_SUM", "fields": ["score"]}]`, true},
		{"aggregation without a column", `"aggregations": [{"type": "AGG_SUM"}]`, false},
		{"vector grouper", `"groups": [{"type": "GROUP_KMEANS", "vector": "battery"}]`, true},
		{"grouper without a column", `"groups": [{"type": "GROUP_CATEGORY"}]`, false},
		{"formula attribute", `"attributes": [{"type": "ATTR_FORMULA", "label": "x2", "expression": "score * 2"}]`, true},
		{"vector attribute", `"attributes": [{"type": "ATTR_SCALE_SCORE", "vector": "battery"}]`, true},
		{"crosstab vector cell", `"crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "category"}], "columns": [{"type": "GROUP_KMEANS", "fields": ["score"]}],
			"cell": {"type": "AGG_VEC_MEAN", "vector": "battery"}, "margin_aggregations": [{"type": "AGG_VEC_SUM", "fields": ["score"]}]}`, true},
		{"crosstab cell without a column", `"crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "category"}], "columns": [{"type": "GROUP_CATEGORY", "field": "category"}],
			"cell": {"type": "AGG_COUNT"}}`, false},
		{"crosstab axis without a column", `"crosstab": {"rows": [{"type": "GROUP_CATEGORY"}], "columns": [{"type": "GROUP_CATEGORY", "field": "category"}],
			"cell": {"type": "AGG_COUNT", "field": "score"}}`, false},
		{"crosstab margin without a column", `"crosstab": {"rows": [{"type": "GROUP_CATEGORY", "field": "category"}], "columns": [{"type": "GROUP_CATEGORY", "field": "category"}],
			"cell": {"type": "AGG_COUNT", "field": "score"}, "margin_aggregations": [{"type": "AGG_COUNT"}]}`, false},
	}
	wrap := map[string]func(string) string{
		toolmeta.ToolProcess: func(b string) string { return `{"cohort": {"filename": "c.pulse"}, ` + b + `}` },
		toolmeta.ToolCompose: func(b string) string { return `{"requests": [{"cohort": {"filename": "c.pulse"}, ` + b + `}]}` },
		toolmeta.ToolProcessChain: func(b string) string {
			return `{"stages": [{"request": {"cohort": {"filename": "c.pulse"}, ` + b + `}}]}`
		},
	}
	for tool, w := range wrap {
		sch := compileBound(t, bound[tool])
		for _, c := range cases {
			err := validates(t, sch, w(c.body))
			if c.ok && err != nil {
				t.Errorf("%s / %s: rejected, want accepted: %v", tool, c.name, err)
			}
			if !c.ok && err == nil {
				t.Errorf("%s / %s: accepted, want rejected", tool, c.name)
			}
		}
	}
}
