package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rmpRequest is a single-grouper SERIES host carrying one rolling
// overlay with params.
func rmpRequest(t *testing.T, f *mgFixture, kind types.OverlayKind, params string) *types.Request {
	t.Helper()
	req := mgRequest(f, []*types.Group{mgRegion()}, psYSum())
	req.Overlays = []types.OverlaySpec{{Name: "r", Kind: kind, Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: json.RawMessage(params)}}
	return req
}

// TestRollingOverlays_MinPeriodsPredictParity (U50 E2-S3): an
// out-of-range or non-integer params.min_periods on either rolling kind
// is refused by predict and by Process (single file and shard archive)
// and Compose with the same code, message and details (bar predict's
// slot index); an in-range value is accepted by both.
func TestRollingOverlays_MinPeriodsPredictParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		kind   types.OverlayKind
		params string
	}{
		{"zscore below 2", types.OverlayKindZScoreVsRolling, `{"window": 3, "min_periods": 1}`},
		{"zscore above window", types.OverlayKindZScoreVsRolling, `{"window": 3, "min_periods": 4}`},
		{"zscore fraction", types.OverlayKindZScoreVsRolling, `{"window": 3, "min_periods": 2.5}`},
		{"index zero", types.OverlayKindIndexVsRollingMean, `{"window": 3, "min_periods": 0}`},
		{"index above window", types.OverlayKindIndexVsRollingMean, `{"window": 3, "min_periods": 4}`},
		{"index string", types.OverlayKindIndexVsRollingMean, `{"window": 3, "min_periods": "2"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := rmpRequest(t, f, c.kind, c.params)
			pe := firstPredictError(t, predictEnvelope(t, f.p, f.fs, f.cohort, req), nil)
			if pe == nil || pe.Code != string(perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) ||
				pe.Details["param"] != "min_periods" || pe.Details["index"] != 0 {
				t.Fatalf("predict %+v, want LEVEL_OUT_OF_RANGE on min_periods at index 0", pe)
			}
			want := map[string]any{}
			for k, v := range pe.Details {
				if k != "index" {
					want[k] = v
				}
			}
			wantJSON, _ := json.Marshal(want)
			shard := *req
			shard.Cohort = &types.Cohort{Filename: f.archive}
			_, procErr := f.p.Process(ctx, req)
			_, shardErr := f.shard.Process(ctx, &shard)
			_, composeErr := f.p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{req}})
			for arm, err := range map[string]error{"process": procErr, "process shards": shardErr, "compose": unlocated(t, composeErr)} {
				var ce *perr.CodedError
				if !stderrors.As(err, &ce) {
					t.Fatalf("%s: runtime did not refuse (err %v)", arm, err)
				}
				got := map[string]any{}
				for k, v := range ce.Details {
					if k != "index" {
						got[k] = v
					}
				}
				gotJSON, _ := json.Marshal(got)
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || string(gotJSON) != string(wantJSON) {
					t.Errorf("%s: runtime %s %q %s, predict %s %q %s", arm, ce.Code, ce.Message, gotJSON, pe.Code, pe.Message, wantJSON)
				}
			}
		})
	}

	// In range: predict and runtime both accept, and the explicit floor
	// answers earlier than the default (min_periods = window).
	for _, c := range []struct {
		kind  types.OverlayKind
		floor string
	}{
		{types.OverlayKindZScoreVsRolling, `{"window": 3, "min_periods": 2}`},
		{types.OverlayKindIndexVsRollingMean, `{"window": 3, "min_periods": 1}`},
	} {
		req := rmpRequest(t, f, c.kind, c.floor)
		if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) != 0 {
			t.Fatalf("%s predict refused %s: %+v", c.kind, c.floor, env.Errors[0])
		}
		early, err := f.p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		def, err := f.p.Process(ctx, rmpRequest(t, f, c.kind, `{"window": 3}`))
		if err != nil {
			t.Fatal(err)
		}
		es, ds := sfxStats(t, early, 0), sfxStats(t, def, 0)
		if len(ds) > 3 {
			t.Fatalf("%d rows; the default would answer past row 3", len(ds))
		}
		for i := range ds {
			if !math.IsNaN(ds[i]) {
				t.Errorf("%s default: row %d = %v before a full window", c.kind, i, ds[i])
			}
		}
		if math.IsNaN(es[len(es)-1]) {
			t.Errorf("%s %s: last row null, want a figure", c.kind, c.floor)
		}
	}
}
