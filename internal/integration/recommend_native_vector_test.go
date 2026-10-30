package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// recommendNeedValues fills the needs a native-vector draft can carry,
// so the draft runs as written once the caller supplies them. A need
// missing here fails the test: a new one must be given a value.
var recommendNeedValues = map[string]any{
	"k":         3,
	"component": 1,
	"factor":    1,
	"mu":        []float64{5, 5, 5, 5},
	"reference": []float64{5, 5, 5, 5},
	"metric":    "euclidean", // ATTR_PROFILE_SIMILARITY takes similarityMetric
	"between":   map[string]any{"type": "GROUP_CATEGORY", "field": "grp"},
}

// similarityMetric is the metric need's value on the similarity-direction
// comparison attribute.
const similarityMetric = "correlation"

// fillNeeds replaces each "<param>" placeholder in v (a string, or a
// one-element ["<param>"] list) with recommendNeedValues[param] (the
// similarity metric on ATTR_PROFILE_SIMILARITY, operator op).
func fillNeeds(t *testing.T, op string, v any) any {
	t.Helper()
	ph := func(s string) (any, bool) {
		if !strings.HasPrefix(s, "<") || !strings.HasSuffix(s, ">") {
			return nil, false
		}
		name := strings.Trim(s, "<>")
		if name == "metric" && op == "ATTR_PROFILE_SIMILARITY" {
			return similarityMetric, true
		}
		val, ok := recommendNeedValues[name]
		if !ok {
			t.Fatalf("no test value for need %s", s)
		}
		return val, true
	}
	switch x := v.(type) {
	case string:
		if val, ok := ph(x); ok {
			return val
		}
	case []any:
		if len(x) == 1 {
			if s, ok := x[0].(string); ok {
				if val, ok := ph(s); ok {
					return val
				}
			}
		}
		for i := range x {
			x[i] = fillNeeds(t, op, x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = fillNeeds(t, op, x[k])
		}
	}
	return v
}

// TestRecommend_NativeVectorDraftsRun: over a cohort with native vector
// fields (writeVparCohorts: emb, kind scale; emb2, kind measure), every
// recommendation that names a native in a vector slot — for every
// intent, f32 and f64 — runs end to end: as written when fully bound,
// and with its needs filled otherwise. A hinted native drafts only
// vector slots, and those run too.
func TestRecommend_NativeVectorDraftsRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, ft := range []encoding.FieldType{encoding.FieldTypeVecF64, encoding.FieldTypeVecF32} {
		t.Run(ft.String(), func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
			if err != nil {
				t.Fatal(err)
			}
			writeVparCohorts(t, p, ft, false)
			ran := map[string]bool{}
			for _, in := range pulse.Intents() {
				if !in.Analytic {
					continue
				}
				for _, hints := range [][]string{nil, {"emb2"}} {
					res, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: in.ID, Cohort: &types.Cohort{Filename: "native.pulse"}, Fields: hints, Limit: 1000})
					if err != nil {
						if hints != nil {
							continue // an intent with no vector slot refuses the hint
						}
						t.Fatalf("%s: Recommend: %v", in.ID, err)
					}
					for _, r := range res.Recommendations {
						var v map[string]any
						if err := json.Unmarshal(r.Request, &v); err != nil {
							t.Fatal(err)
						}
						native := strings.Contains(string(r.Request), `"vector":"emb"`) || strings.Contains(string(r.Request), `"vector":"emb2"`)
						if hints != nil && !strings.Contains(string(r.Request), `"vector":"emb2"`) {
							t.Errorf("%s %s: hinted draft does not bind emb2: %s", in.ID, r.Operator, r.Request)
						}
						if !native {
							continue
						}
						raw, err := json.Marshal(fillNeeds(t, r.Operator, v))
						if err != nil {
							t.Fatal(err)
						}
						var req types.Request
						if err := json.Unmarshal(raw, &req); err != nil {
							t.Fatalf("%s %s: filled draft does not decode: %v\n%s", in.ID, r.Operator, err, raw)
						}
						if _, err := p.Process(ctx, &req); err != nil {
							t.Errorf("%s %s: recommended native draft does not run: %v\n%s", in.ID, r.Operator, err, raw)
						}
						ran[r.Operator] = true
					}
				}
			}
			// Every operator family with a vector slot is exercised.
			for _, op := range []string{"AGG_VEC_MEAN", "MAT_CORRELATION", "MAT_RELIABILITY", "TEST_MANOVA", "ATTR_SCALE_SCORE", "ATTR_VEC_DISTANCE", "GROUP_KMEANS", "MAT_DISTANCE"} {
				if !ran[op] {
					t.Errorf("%s: no native draft ran; ran %v", op, slices.Sorted(func(yield func(string) bool) {
						for k := range ran {
							if !yield(k) {
								return
							}
						}
					}))
				}
			}
		})
	}
}
