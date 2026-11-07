package integration_test

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestOverlayKindUnknown_EveryProcessShapeMatchesPredict: a
// Request.Overlays spec of a never-registered kind is refused
// PULSE_OVERLAY_KIND_UNKNOWN by Process, ProcessStream and predict alike
// on every Process shape — ungrouped (scalar), matrix-only, grouped
// (host "series") and crosstab — with one message and details. The
// ungrouped and matrix-only shapes run no overlay dispatcher, so the
// runtime once ignored the kind there while predict refused it; the
// host pre-pass now raises overlayhost.RequestKindUnknownRefusal.
func TestOverlayKindUnknown_EveryProcessShapeMatchesPredict(t *testing.T) {
	t.Parallel()

	const kind = types.OverlayKind("OVERLAY_NEVER_REGISTERED")
	p, fs, cohort := acceptanceCohort(t)
	for _, c := range []struct {
		name  string
		shape func(*types.Request)
		host  string
	}{
		{"ungrouped", func(r *types.Request) {}, ""},
		{"matrix_only", func(r *types.Request) {
			r.Aggregations = nil
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"x", "y"}, Name: "m"}}
		}, ""},
		{"grouped", func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		}, "series"},
		{"crosstab", func(r *types.Request) {
			r.Aggregations = nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
			}
		}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			mk := func() *types.Request {
				r := &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
					Overlays:     []types.OverlaySpec{{Kind: kind}},
				}
				c.shape(r)
				return r
			}
			ce := assertPredictMatchesRefusal(t, p, fs, cohort, mk)
			want := map[string]any{"index": 0, "kind": string(kind)}
			if c.host != "" {
				want["host"] = c.host
			}
			if ce.Code != errors.PULSE_OVERLAY_KIND_UNKNOWN || ce.Message != "overlay kind is not in the catalog: "+string(kind) ||
				!reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime %s %q %#v, want PULSE_OVERLAY_KIND_UNKNOWN details %#v", ce.Code, ce.Message, ce.Details, want)
			}
		})
	}
}
