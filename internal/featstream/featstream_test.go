package featstream

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestStreamable pins the spec-level verdict: a global-pass pre-pass
// reading a column an earlier feature derives routes buffered; stored
// columns, row-local operators and level readers stream.
func TestStreamable(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "s", Type: encoding.FieldTypeU8},
	}}
	f := func(typ types.FeatureType, field, params string) *types.Feature {
		return &types.Feature{Type: typ, Field: field, Params: json.RawMessage(params)}
	}
	cases := []struct {
		name string
		feat *types.Feature
		want bool
	}{
		{"te plain", f(types.FEAT_TARGET_ENCODE, "g", `{"target":"y"}`), true},
		{"te fit_on stored", f(types.FEAT_TARGET_ENCODE, "g", `{"target":"y","fit_on":"s == 0"}`), true},
		{"te fit_on derived", f(types.FEAT_TARGET_ENCODE, "g", `{"target":"y","fit_on":"split == 0"}`), false},
		{"te target derived", f(types.FEAT_TARGET_ENCODE, "g", `{"target":"ly"}`), false},
		{"te malformed left to the refusal", f(types.FEAT_TARGET_ENCODE, "g", `{"target":5}`), true},
		{"bucketize quantiles stored", f(types.FEAT_BUCKETIZE, "y", `{"quantiles":4}`), true},
		{"bucketize quantiles derived", f(types.FEAT_BUCKETIZE, "ly", `{"quantiles":4}`), false},
		{"bucketize boundaries derived", f(types.FEAT_BUCKETIZE, "ly", `{"boundaries":[1,2]}`), true},
		{"log derived", f(types.FEAT_LOG, "ly", ``), true},
		{"frequency derived", f(types.FEAT_FREQUENCY_ENCODE, "ly", ``), true},
		{"nil", nil, true},
	}
	for _, c := range cases {
		got, reason := Streamable(c.feat, schema)
		if got != c.want || (got == (reason != "")) {
			t.Errorf("%s: Streamable = %v %q, want %v", c.name, got, reason, c.want)
		}
	}
}
