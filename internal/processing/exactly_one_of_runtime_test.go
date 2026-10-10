package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
)

// exactlyOneOfRuntimeSamples is a valid value per grouped param for the
// runtime half of the exactly_one_of parity gate.
var exactlyOneOfRuntimeSamples = map[string]map[string]any{
	string(types.WIN_EWMA):       {"alpha": 0.3, "decay": 0.7},
	string(types.FEAT_BUCKETIZE): {"boundaries": []float64{0, 4, 8}, "quantiles": 4},
}

// runWithParams drives one window or feature operator's real runtime
// path with the given params and returns its error.
func runWithParams(t *testing.T, name, category string, params map[string]any) error {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	switch category {
	case "window":
		w := &types.Window{
			Type: types.WindowType(name), Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  raw,
		}
		return window.Apply(context.Background(), emitsWindowRows(), []*types.Window{w})
	case "feature":
		s := numericSchema()
		recs := emitsNumericRecords(s)
		view := make([]feature.Record, len(recs))
		for i, r := range recs {
			view[i] = r
		}
		f := &types.Feature{Type: types.FeatureType(name), Field: "score", Label: "out", Params: raw}
		return feature.Apply(view, []*types.Feature{f}, s)
	}
	t.Fatalf("%s: no runtime arm for category %q; add one", name, category)
	return nil
}

// TestExactlyOneOfRefusedAtRuntime is the runtime half of descriptor's
// TestManifestExactlyOneOfComplete: for every manifest exactly_one_of
// group, the runtime refuses no member and two members, and accepts
// each member alone — the rule the manifest declares is the rule the
// engine enforces.
func TestExactlyOneOfRefusedAtRuntime(t *testing.T) {
	grouped := 0
	for name, op := range manifestOperatorEntries() {
		members := map[string][]string{}
		var order []string
		for _, p := range op.Params {
			if p.ExactlyOneOf == "" {
				continue
			}
			if _, ok := members[p.ExactlyOneOf]; !ok {
				order = append(order, p.ExactlyOneOf)
			}
			members[p.ExactlyOneOf] = append(members[p.ExactlyOneOf], p.Name)
		}
		if len(order) == 0 {
			continue
		}
		grouped++
		samples := exactlyOneOfRuntimeSamples[name]
		if err := runWithParams(t, name, op.Category, nil); err == nil {
			t.Errorf("%s: no params, yet the runtime accepts", name)
		}
		for _, g := range order {
			ms := members[g]
			for _, m := range ms {
				v, ok := samples[m]
				if !ok {
					t.Fatalf("%s: no exactlyOneOfRuntimeSamples value for %q", name, m)
				}
				if err := runWithParams(t, name, op.Category, map[string]any{m: v}); err != nil {
					t.Errorf("%s: %q alone is refused: %v", name, m, err)
				}
			}
			if len(ms) >= 2 {
				if err := runWithParams(t, name, op.Category, map[string]any{ms[0]: samples[ms[0]], ms[1]: samples[ms[1]]}); err == nil {
					t.Errorf("%s: %q with %q is accepted", name, ms[0], ms[1])
				}
			}
		}
	}
	if grouped < len(exactlyOneOfRuntimeSamples) {
		t.Errorf("only %d operators declare a group; the samples list %d", grouped, len(exactlyOneOfRuntimeSamples))
	}
}
