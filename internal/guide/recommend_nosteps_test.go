package guide

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestRecommend_DraftsNoMaterializeSteps: no recommendation of any
// intent, unbound or bound, carries steps. A model reads a label its
// own request derives, so the old materialise plan was never needed;
// Recommendation.Steps stays only as a deprecated, always-absent slot.
func TestRecommend_DraftsNoMaterializeSteps(t *testing.T) {
	t.Parallel()

	for _, in := range descx.Intents() {
		for name, res := range map[string]*descriptor.RecommendResult{
			"unbound": all(t, nil, in.ID),
			"bound":   boundAll(t, nil, in.ID),
		} {
			for _, r := range res.Recommendations {
				raw, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte(`"steps"`)) {
					t.Errorf("%s %s: %s carries steps: %s", in.ID, name, r.Operator, raw)
				}
			}
		}
	}
}
