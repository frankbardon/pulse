//go:build heavy

package processing

import (
	"math"
	"testing"
)

// TestSEMScore_BoundMatchesInlineAcrossBlocks: past one merge block the
// inline fit folds MAT_SEM's blocked merge, so a bound fit still scores
// to the inline fit's bit — 18 copies of the brand rows (21,600, six
// blocks) with the second block's q1 null: a block that adds no mass
// still holds its place in the merge tree, as in MAT_SEM's listwise fold
// (dropping it reshapes the tree and moves the bits). Ungrouped and the
// joint two-wave fit, both methods.
func TestSEMScore_BoundMatchesInlineAcrossBlocks(t *testing.T) {
	schema, base := brandSEMFixture(t, nil, nil)
	const copies = 18
	mk := func() []*Record {
		var out []*Record
		for range copies {
			for _, r := range base() {
				i := len(out)
				vals, nulls := map[string]float64{}, map[string]bool{}
				for _, f := range schema.Fields {
					v, ok := r.NumericValue(f.Name)
					if !ok || (f.Name == "q1" && i >= 4096 && i < 2*4096) {
						nulls[f.Name] = true
						continue
					}
					vals[f.Name] = v
				}
				out = append(out, NewRecordWithNulls(schema, vals, nulls))
			}
		}
		return out
	}
	desc := brandModel(t, "brand_ms")
	for _, c := range []struct {
		name  string
		knobs map[string]any
	}{
		{"covariance", map[string]any{}},
		{"groups", map[string]any{"group": "wave", "group_equal": []string{"loadings", "intercepts"}}},
	} {
		knobs := map[string]any{"model": desc}
		for k, v := range c.knobs {
			knobs[k] = v
		}
		sf := captureSEMFit(t, schema, mk, "brand", knobs)
		set := semSetOf(t, sf)
		for _, method := range []string{"regression", "bartlett"} {
			t.Run(c.name+"/"+method, func(t *testing.T) {
				inline := map[string]any{"latent": "value", "method": method}
				for k, v := range knobs {
					inline[k] = v
				}
				want, _, _ := runFittedArms(t, schema, mk, "score", nil, semScoreSlot(t, inline))
				got, _, _ := runFittedArms(t, schema, mk, "score", set, semScoreSlot(t, map[string]any{"fit": "brand", "latent": "value", "method": method}))
				if !sameBits(got, want) {
					t.Fatalf("bound scores differ from the inline fit's\n got %v\nwant %v", got[:3], want[:3])
				}
				if math.IsNaN(got[0]) || !math.IsNaN(got[4096]) {
					t.Fatalf("vacuous: row 0 scored %v, null-q1 row 4096 scored %v", got[0], got[4096])
				}
			})
		}
	}
}
