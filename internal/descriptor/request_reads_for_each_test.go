package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestRequestReads_ForEachItems pins the reads of a fanned-out item:
// every combination is read at the item's written path and merged, so
// an AGG_CONSTANT `expr` supplied whole through a `{"$var": "axis"}`
// marker reads each axis value's params, an `{{axis}}` field reads each
// value's column, and a fanned-out optimize block's bounds and
// objective read every combination's names.
func TestRequestReads_ForEachItems(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		root descriptor.ReadsRoot
		want []string
	}{
		{
			name: "AGG_CONSTANT expr through a $var axis marker",
			doc: `{"params":{"tv":1,"radio":2},"aggregations":[{"type":"AGG_CONSTANT","label":"k_{{ch}}",
				"params":{"type":"f64","expr":{"$var":"e"}},
				"for_each":{"mode":"zip","axes":[{"name":"ch","values":["tv","radio"]},{"name":"e","values":["params.tv * 2","params.radio + 1"]}]}}]}`,
			root: descriptor.ReadsRootRequest,
			want: []string{"aggregations[0]|params.expr| cols= params=tv,radio"},
		},
		{
			name: "axis field reads every value's column",
			doc: `{"aggregations":[{"type":"AGG_SUM","field":"{{ch}}_spend","label":"s_{{ch}}",
				"for_each":{"axes":[{"name":"ch","values":["tv","radio"]}]}}]}`,
			root: descriptor.ReadsRootRequest,
			want: []string{"aggregations[0]|field| cols=tv_spend,radio_spend params="},
		},
		{
			name: "optimize block fan-out",
			doc: `{"params":{"tv_hi":5,"radio_hi":7},"requests":[{"cohort":{"filename":"c.pulse"}}],
				"optimize":[{"name":"o_{{ch}}","direction":"maximize","objective":{"$var":"obj"},
				"variables":[{"name":"x","lower":0,"upper":{"$var":"hi"}}],
				"for_each":{"mode":"zip","axes":[{"name":"ch","values":["tv","radio"]},{"name":"hi","values":["params.tv_hi","params.radio_hi"]},{"name":"obj","values":["log(x + 1)","sqrt(x)"]}]}}]}`,
			root: descriptor.ReadsRootComposed,
			want: []string{
				"optimize[0]|objective| cols= params= vars=x",
				"optimize[0].variables[0]|upper| cols= params=tv_hi,radio_hi",
			},
		},
		{
			name: "malformed for_each reads as written",
			doc: `{"aggregations":[{"type":"AGG_SUM","field":"spend",
				"for_each":{"axes":[]}}]}`,
			root: descriptor.ReadsRootRequest,
			want: []string{"aggregations[0]|field| cols=spend params="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, got := readsLines(t, tc.doc, tc.root, nil)
			for _, w := range tc.want {
				if !slices.Contains(got, w) {
					t.Errorf("missing %q in\n%v", w, got)
				}
			}
			// One entry per path|key: the combinations merged.
			seen := map[string]bool{}
			for _, s := range got {
				k := s[:indexCols(s)]
				if seen[k] {
					t.Errorf("duplicate entry %q in\n%v", k, got)
				}
				seen[k] = true
			}
		})
	}
}

// indexCols is the length of a readsLine's path|key|request prefix.
func indexCols(s string) int {
	for i := range len(s) - 5 {
		if s[i:i+6] == " cols=" {
			return i
		}
	}
	return len(s)
}
