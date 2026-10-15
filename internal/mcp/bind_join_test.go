package mcp

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// makeJoinRightSchema is a join target: a key, a numeric and a
// categorical field, so the widening reaches every bucket a joined
// request names.
func makeJoinRightSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	dict.Add("north")
	dict.Add("south")
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "category_key", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "spend", Type: encoding.FieldTypeF64},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		},
	}
}

// joinedBody wraps a request fragment for every request-carrying bound
// tool: pulse_process, pulse_predict, a pulse_compose slot and a
// pulse_process_chain stage 0.
var joinedBodyWrap = map[string]func(string) string{
	toolmeta.ToolProcess: func(b string) string { return `{"cohort": {"filename": "c.pulse"}, ` + b + `}` },
	toolmeta.ToolPredict: func(b string) string { return `{"cohort": {"filename": "c.pulse"}, ` + b + `}` },
	toolmeta.ToolCompose: func(b string) string { return `{"requests": [{"cohort": {"filename": "c.pulse"}, ` + b + `}]}` },
	toolmeta.ToolProcessChain: func(b string) string {
		return `{"stages": [{"request": {"cohort": {"filename": "c.pulse"}, ` + b + `}}]}`
	},
}

// TestBind_JoinTargetWidensFieldEnums (#260): a schema bound with its
// join target accepts the joined (`as`-prefixed) right-side names in
// every field slot and the right cohort's own names as right_field, while
// left_field stays left-only and an unknown name is still refused. Bound
// without the target (BindForInstance) the joined name is refused — the
// pre-fix behaviour.
func TestBind_JoinTargetWidensFieldEnums(t *testing.T) {
	_, inst := instanceFor(t, nil, pulse.Extensions{})
	join := func(as, rightField string) string {
		return fmt.Sprintf(`"joins": [{"right": "r.pulse", "as": %q, "on": [{"left_field": "category", "right_field": %q}]}]`, as, rightField)
	}
	j := join("r_", "category_key")
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"joined aggregation field", j + `, "aggregations": [{"type": "AGG_SUM", "field": "r_spend"}]`, true},
		{"joined group field", j + `, "aggregations": [{"type": "AGG_COUNT", "field": "score"}], "groups": [{"type": "GROUP_CATEGORY", "field": "r_region"}]`, true},
		{"joined filterer field", j + `, "aggregations": [{"type": "AGG_COUNT", "field": "score"}], "filterers": [{"type": "FILTER_INCLUDE", "field": "r_region", "values": ["north"]}]`, true},
		{"joined numeric attribute field", j + `, "attributes": [{"type": "ATTR_ZSCORE", "field": "r_spend"}]`, true},
		{"left field still bound", j + `, "aggregations": [{"type": "AGG_SUM", "field": "score"}]`, true},
		{"unknown field refused", j + `, "aggregations": [{"type": "AGG_SUM", "field": "r_nope"}]`, false},
		{"bare right name under a prefix refused", j + `, "aggregations": [{"type": "AGG_SUM", "field": "spend"}]`, false},
		{"joined categorical not numeric", j + `, "attributes": [{"type": "ATTR_ZSCORE", "field": "r_region"}]`, false},
		{"unknown right_field refused", join("r_", "nope"), false},
		{"left_field stays left-only", `"joins": [{"right": "r.pulse", "on": [{"left_field": "r_spend", "right_field": "spend"}]}]`, false},
	}
	widened, err := BindForInstanceWithJoins(makeBindSchema(), []JoinTarget{{Schema: makeJoinRightSchema(), As: "r_"}}, inst)
	if err != nil {
		t.Fatal(err)
	}
	for tool, w := range joinedBodyWrap {
		sch := compileBound(t, widened[tool])
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

	// No prefix: the joined names are the right cohort's own.
	bare, err := BindForInstanceWithJoins(makeBindSchema(), []JoinTarget{{Schema: makeJoinRightSchema()}}, inst)
	if err != nil {
		t.Fatal(err)
	}
	sch := compileBound(t, bare[toolmeta.ToolProcess])
	if err := validates(t, sch, `{"cohort": {"filename": "c.pulse"}, "joins": [{"right": "r.pulse", "on": [{"left_field": "category", "right_field": "category_key"}]}], "aggregations": [{"type": "AGG_SUM", "field": "spend"}]}`); err != nil {
		t.Errorf("unprefixed joined field rejected: %v", err)
	}

	// Left-only binding still refuses the joined name.
	left, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	sch = compileBound(t, left[toolmeta.ToolProcess])
	if validates(t, sch, joinedBodyWrap[toolmeta.ToolProcess](j+`, "aggregations": [{"type": "AGG_SUM", "field": "r_spend"}]`)) == nil {
		t.Error("left-only binding accepted a joined field name")
	}

	// Sample / facet stay left-cohort tools.
	for _, tool := range []string{toolmeta.ToolSample, toolmeta.ToolFacet, toolmeta.ToolFacetSchema} {
		if bytes.Contains(widened[tool], []byte(`"r_spend"`)) {
			t.Errorf("%s widened with a joined name: %s", tool, widened[tool])
		}
	}
}

// TestBind_JoinTargetIgnoredWhenJoinsHidden: an instance hiding
// capability:joins never advertises a joined name, whatever target the
// bind was handed.
func TestBind_JoinTargetIgnoredWhenJoinsHidden(t *testing.T) {
	_, inst := instanceFor(t, &pulse.FeatureProfile{Features: []string{"capability:process", "AGG_SUM"}}, pulse.Extensions{})
	bound, err := BindForInstanceWithJoins(makeBindSchema(), []JoinTarget{{Schema: makeJoinRightSchema(), As: "r_"}}, inst)
	if err != nil {
		t.Fatal(err)
	}
	if got := enumOf(decodeBoundRequest(t, bound[toolmeta.ToolProcess]), "aggregations", "field"); slices.Contains(got, "r_spend") || len(got) == 0 {
		t.Fatalf("aggregations.field = %v, want the left-only enum", got)
	}
}
