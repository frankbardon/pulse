package pulse

import "testing"

// TestProbeParams_Placeholders pins the synthesized probe params: a
// placeholder per declared JSONType for every Required param and the
// first member of every ExactlyOneOf group, optional params absent, and
// nil when nothing needs a value.
func TestProbeParams_Placeholders(t *testing.T) {
	fields := []string{"probe_x1", "probe_x2"}
	cases := []struct {
		name   string
		params []ParamMeta
		fields []string
		want   string
	}{
		{"no params", nil, fields, ""},
		{"optional only", []ParamMeta{{Name: "a", JSONType: "number"}, {Name: "b", JSONType: "string", Default: "x"}}, fields, ""},
		{"every type with fields", []ParamMeta{
			{Name: "n", JSONType: "number", Required: true},
			{Name: "b", JSONType: "boolean", Required: true},
			{Name: "s", JSONType: "string", Required: true},
			{Name: "a", JSONType: "array", Required: true},
			{Name: "o", JSONType: "object", Required: true},
			{Name: "opt", JSONType: "number"},
		}, fields, `{"a":["probe_x1","probe_x2"],"b":false,"n":1,"o":{},"s":"probe_x1"}`},
		{"every type without fields", []ParamMeta{
			{Name: "s", JSONType: "string", Required: true},
			{Name: "a", JSONType: "array", Required: true},
		}, nil, `{"a":[],"s":""}`},
		{"one-of group gets its first member", []ParamMeta{
			{Name: "span", JSONType: "number", ExactlyOneOf: "rate"},
			{Name: "halflife", JSONType: "number", ExactlyOneOf: "rate"},
			{Name: "mode", JSONType: "string", ExactlyOneOf: "how"},
			{Name: "fn", JSONType: "object", ExactlyOneOf: "how"},
		}, fields, `{"mode":"probe_x1","span":1}`},
		{"an Example replaces the placeholder", []ParamMeta{
			{Name: "kind", JSONType: "string", Required: true, Example: "b"},
			{Name: "span", JSONType: "number", ExactlyOneOf: "rate", Example: 7},
			{Name: "halflife", JSONType: "number", ExactlyOneOf: "rate", Example: 9},
			{Name: "opt", JSONType: "number", Example: 3},
			{Name: "n", JSONType: "number", Required: true},
		}, fields, `{"kind":"b","n":1,"span":7}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(probeParams(tc.params, tc.fields)); got != tc.want {
				t.Fatalf("probeParams = %s, want %s", got, tc.want)
			}
		})
	}
}
