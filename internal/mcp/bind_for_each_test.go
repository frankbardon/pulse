package mcp

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// compileBound compiles one bound tool schema with a real draft 2020-12
// validator — what a validating MCP client does with an inputSchema.
func compileBound(t *testing.T, raw []byte) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unmarshal bound schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("bound.json", doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	sch, err := c.Compile("bound.json")
	if err != nil {
		t.Fatalf("compile bound schema: %v", err)
	}
	return sch
}

// validates reports whether instance (JSON) validates against sch.
func validates(t *testing.T, sch *jsonschema.Schema, instance string) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(instance))
	if err != nil {
		t.Fatalf("unmarshal instance %s: %v", instance, err)
	}
	return sch.Validate(inst)
}

// TestBind_ForEachPlaceholders: a bound schema lets an item carrying
// for_each spell its cohort-bound strings as axis placeholders while an
// item without for_each keeps the strict enums — checked with a real
// draft 2020-12 validator over pulse_process, a pulse_compose slot and a
// pulse_process_chain stage.
func TestBind_ForEachPlaceholders(t *testing.T) {
	t.Parallel()

	_, inst := instanceFor(t, nil, pulse.Extensions{})
	bound, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	const fe = `"for_each": {"axes": [{"name": "ch", "values": ["a", "b"]}]}`
	cases := []struct {
		name string
		body string // a request body fragment beside the cohort
		ok   bool
	}{
		{"window placeholders under for_each", `"windows": [{"type": "WIN_EWMA", "field": "{{ch}}_score", "label": "e_{{ch}}",
			"partition_by": ["{{ch}}"], "order_by": [{"field": "{{ ch }}_on"}], ` + fe + `}]`, true},
		{"attribute placeholders under for_each", `"attributes": [{"type": "ATTR_ZSCORE", "field": "{{ch}}", ` + fe + `}]`, true},
		{"feature placeholder under for_each", `"features": [{"type": "FEAT_LOG", "field": "{{ch}}_score", ` + fe + `}]`, true},
		{"operator type placeholder under for_each", `"features": [{"type": "{{ch}}", "field": "score", ` + fe + `}]`, true},
		{"aggregation placeholders under for_each", `"aggregations": [{"type": "AGG_SUM", "field": "{{ch}}_score", "label": "s_{{ch}}", ` + fe + `}]`, true},
		{"aggregation placeholder without for_each", `"aggregations": [{"type": "AGG_SUM", "field": "{{ch}}_score"}]`, false},
		{"matrix placeholders under for_each", `"matrices": [{"type": "MAT_COVARIANCE", "name": "m_{{ch}}", "fields": ["{{ch}}_score", "score"], ` + fe + `}]`, true},
		{"matrix type placeholder without for_each", `"matrices": [{"type": "{{ch}}", "fields": ["score"]}]`, false},
		{"plain window", `"windows": [{"type": "WIN_EWMA", "field": "score", "order_by": [{"field": "observed_on"}]}]`, true},
		{"placeholder without for_each", `"windows": [{"type": "WIN_EWMA", "field": "{{ch}}_score", "order_by": [{"field": "observed_on"}]}]`, false},
		{"placeholder partition key without for_each", `"windows": [{"type": "WIN_EWMA", "field": "score", "partition_by": ["{{ch}}"], "order_by": [{"field": "observed_on"}]}]`, false},
		{"placeholder order key without for_each", `"windows": [{"type": "WIN_EWMA", "field": "score", "order_by": [{"field": "{{ch}}"}]}]`, false},
		{"typo field without for_each", `"windows": [{"type": "WIN_EWMA", "field": "scroe", "order_by": [{"field": "observed_on"}]}]`, false},
		{"typo field under for_each", `"windows": [{"type": "WIN_EWMA", "field": "scroe", "order_by": [{"field": "observed_on"}], ` + fe + `}]`, false},
		// The attribute field binds per type (E2-S5): ATTR_ZSCORE reads a
		// quantity, so a categorical is refused; ATTR_PREVIOUS_STATE reads
		// a categorical state, so it is admitted.
		{"categorical attribute field without for_each", `"attributes": [{"type": "ATTR_ZSCORE", "field": "category"}]`, false},
		{"categorical attribute field under for_each", `"attributes": [{"type": "ATTR_ZSCORE", "field": "category", ` + fe + `}]`, false},
		{"categorical state field without for_each", `"attributes": [{"type": "ATTR_PREVIOUS_STATE", "field": "category", "subject_field": "category", "order_field": "observed_on"}]`, true},
		{"numeric state field without for_each", `"attributes": [{"type": "ATTR_PREVIOUS_STATE", "field": "score"}]`, false},
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

// TestBind_ForEachHiddenDropsPlaceholderBranch: with capability:for_each
// hidden the placeholder branch disappears with the key — the bound
// strings are the strict enums again and a placeholder is rejected even
// beside a (now unadvertised) for_each.
func TestBind_ForEachHiddenDropsPlaceholderBranch(t *testing.T) {
	t.Parallel()

	_, inst := instanceFor(t, &pulse.FeatureProfile{Features: []string{"capability:process", "WIN_EWMA", "FEAT_LOG"}}, pulse.Extensions{})
	bound, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	raw := bound[toolmeta.ToolProcess]
	if bytes.Contains(raw, []byte(`"for_each"`)) || bytes.Contains(raw, []byte(`"pattern"`)) {
		t.Fatalf("hidden for_each still binds the key or a placeholder pattern: %s", raw)
	}
	if got := enumOf(decodeBoundRequest(t, raw), "windows", "field"); len(got) == 0 {
		t.Fatal("windows.field lost its strict enum")
	}
	sch := compileBound(t, raw)
	if validates(t, sch, `{"cohort": {"filename": "c.pulse"}, "windows": [{"type": "WIN_EWMA", "field": "{{ch}}_score",
		"order_by": [{"field": "observed_on"}], "for_each": {"axes": [{"name": "ch", "values": ["a"]}]}}]}`) == nil {
		t.Fatal("a placeholder field validated on an instance that hides for_each")
	}
}

// TestBind_LiteralPlaceholderFieldName (#379): a cohort field literally
// named like an axis placeholder (`{{x}}`) is a real enum member, so an
// item WITHOUT for_each naming it validates; a placeholder that is not a
// member (`{{y}}`) is still refused there, and an item carrying for_each
// keeps accepting any placeholder.
func TestBind_LiteralPlaceholderFieldName(t *testing.T) {
	t.Parallel()

	schema := makeBindSchema()
	schema.Fields = append(schema.Fields, encoding.Field{Name: "{{x}}", Type: encoding.FieldTypeF64})
	_, inst := instanceFor(t, nil, pulse.Extensions{})
	bound, err := BindForInstance(schema, inst)
	if err != nil {
		t.Fatal(err)
	}
	const fe = `"for_each": {"axes": [{"name": "y", "values": ["score"]}]}`
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"literal member without for_each", `"windows": [{"type": "WIN_EWMA", "field": "{{x}}", "order_by": [{"field": "observed_on"}]}]`, true},
		{"literal member partition key without for_each", `"windows": [{"type": "WIN_EWMA", "field": "score", "partition_by": ["{{x}}"], "order_by": [{"field": "observed_on"}]}]`, true},
		{"literal member attribute without for_each", `"attributes": [{"type": "ATTR_ZSCORE", "field": "{{x}}"}]`, true},
		{"non-member placeholder without for_each", `"windows": [{"type": "WIN_EWMA", "field": "{{y}}", "order_by": [{"field": "observed_on"}]}]`, false},
		{"non-member placeholder attribute without for_each", `"attributes": [{"type": "ATTR_ZSCORE", "field": "{{y}}"}]`, false},
		{"placeholder under for_each", `"windows": [{"type": "WIN_EWMA", "field": "{{y}}", "order_by": [{"field": "observed_on"}], ` + fe + `}]`, true},
		{"literal member under for_each", `"attributes": [{"type": "ATTR_ZSCORE", "field": "{{x}}", ` + fe + `}]`, true},
	}
	sch := compileBound(t, bound[toolmeta.ToolProcess])
	for _, c := range cases {
		err := validates(t, sch, `{"cohort": {"filename": "c.pulse"}, `+c.body+`}`)
		if c.ok && err != nil {
			t.Errorf("%s: rejected, want accepted: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: accepted, want rejected", c.name)
		}
	}
}

// TestBind_OptimizeForEachMarkers: with capability:for_each bound, a
// pulse_compose optimize block takes a `for_each` and a whole
// `{"$var": "axis"}` value in any of its scalars (a bound, `starts`, a
// constraint expression) — checked with a real validator; hiding the
// capability drops both.
func TestBind_OptimizeForEachMarkers(t *testing.T) {
	t.Parallel()

	block := `{"requests": [{"cohort": {"filename": "c.pulse"}}], "optimize": [{"name": "o_{{v}}", "direction": "minimize",
		"variables": [{"name": "x", "lower": 0, "upper": {"$var": "v"}}], "objective": "x", "starts": {"$var": "s"},
		"constraints": [{"expr": {"$var": "c"}, "type": "ineq"}],
		"for_each": {"mode": "zip", "axes": [{"name": "v", "values": [1, 2]}, {"name": "s", "values": [1, 2]}, {"name": "c", "values": ["1 - x", "2 - x"]}]}}]}`
	_, inst := instanceFor(t, nil, pulse.Extensions{})
	bound, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := validates(t, compileBound(t, bound[toolmeta.ToolCompose]), block); err != nil {
		t.Fatalf("a fanned-out optimize block was rejected: %v", err)
	}

	_, hidden := instanceFor(t, &pulse.FeatureProfile{Features: []string{"capability:compose", "capability:compose_optimize", "capability:params", "AGG_COUNT"}}, pulse.Extensions{})
	bound, err = BindForInstance(makeBindSchema(), hidden)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bound[toolmeta.ToolCompose], []byte(`"$var"`)) || bytes.Contains(bound[toolmeta.ToolCompose], []byte(`"for_each"`)) {
		t.Fatal("hidden capability:for_each still binds the optimize for_each or a $var marker")
	}
	if validates(t, compileBound(t, bound[toolmeta.ToolCompose]), block) == nil {
		t.Fatal("a marker block validated on an instance that hides for_each")
	}
}

// TestBind_OptimizeVarMarkerPayloadParity: the pulse_compose input
// schema and the instance payload schema admit the `{"$var"}` marker
// at the same optimize-block properties — every scalar of the block
// and of its variables, constraints and models, never for_each or an
// array — and both admit none on an instance hiding
// capability:for_each.
func TestBind_OptimizeVarMarkerPayloadParity(t *testing.T) {
	t.Parallel()

	hidden := &pulse.FeatureProfile{Features: []string{"capability:compose", "capability:compose_optimize", "capability:params", "AGG_COUNT"}}
	for _, fp := range []*pulse.FeatureProfile{nil, hidden} {
		_, inst := instanceFor(t, fp, pulse.Extensions{})
		raw, err := descx.PayloadSchemaForInstance(inst)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Defs map[string]map[string]any `json:"$defs"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		bound, err := BindForInstance(makeBindSchema(), inst)
		if err != nil {
			t.Fatal(err)
		}
		items := schemaAt(decodeBoundRequest(t, bound[toolmeta.ToolCompose]), "properties.optimize.items")
		var want, got []string
		markerPaths(payload.Defs["OptimizeSpec"], "", func(n map[string]any) bool {
			return n["$ref"] == "#/$defs/VarMarker"
		}, &want)
		markerPaths(items, "", func(n map[string]any) bool {
			p, _ := n["properties"].(map[string]any)
			return len(p) == 1 && p["$var"] != nil
		}, &got)
		sort.Strings(want)
		sort.Strings(got)
		if !slices.Equal(want, got) {
			t.Errorf("profile %v: payload marker paths %v, bound %v", fp != nil, want, got)
		}
		if fp == nil && len(want) < 10 {
			t.Errorf("vacuous: only %v admit the marker", want)
		}
		if fp != nil && len(want)+len(got) != 0 {
			t.Errorf("hidden for_each still admits the marker at %v / %v", want, got)
		}
	}
}

// markerPaths appends the path of every property of node (recursing
// into array items) whose anyOf has a branch isMarker matches.
func markerPaths(node map[string]any, prefix string, isMarker func(map[string]any) bool, out *[]string) {
	props, _ := node["properties"].(map[string]any)
	for k, v := range props {
		p, _ := v.(map[string]any)
		if it, _ := p["items"].(map[string]any); it != nil {
			markerPaths(it, prefix+k+"[].", isMarker, out)
		}
		branches, _ := p["anyOf"].([]any)
		for _, b := range branches {
			if m, _ := b.(map[string]any); m != nil && isMarker(m) {
				*out = append(*out, prefix+k)
			}
		}
	}
}
