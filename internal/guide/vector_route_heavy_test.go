//go:build heavy

package guide

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestRecommendBound_NativeVectorsOnlyInVectorSlots is the
// FieldKindVector routing proof: over a cohort carrying native vector
// fields, a draft names a native ONLY as a vector slot's "vector" value
// (never field, fields, split_by, a param …), some intent does draft
// one, and an operator with no vector slot drafts exactly what it
// drafts without the natives — the vector fields cost the scalar
// recommendations nothing.
func TestRecommendBound_NativeVectorsOnlyInVectorSlots(t *testing.T) {
	b := vectorBound(t, vectorFixture(t))
	drafted := 0
	for _, in := range descx.Intents() {
		res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: in.ID, Limit: 1000}, b)
		if err != nil {
			t.Fatalf("RecommendBound(%s): %v", in.ID, err)
		}
		scalar := func(res *descriptor.RecommendResult) []string {
			var out []string
			for _, r := range res.Recommendations {
				if !hasVectorSlot(t, r.Operator, in) {
					out = append(out, string(r.Request))
				}
			}
			slices.Sort(out)
			return out
		}
		if got, want := scalar(res), scalar(boundAll(t, nil, in.ID)); !slices.Equal(got, want) {
			t.Errorf("%s: scalar-slot drafts change with native vectors present\n got %v\nwant %v", in.ID, got, want)
		}
		for _, r := range res.Recommendations {
			for _, u := range draftNatives(t, r) {
				if u[0] != "vector" {
					t.Errorf("%s %s: native %s bound under %q: %s", in.ID, r.Operator, u[1], u[0], r.Request)
				}
				drafted++
			}
		}
	}
	if drafted == 0 {
		t.Fatal("no intent drafted a native vector")
	}
}
