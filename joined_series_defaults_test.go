package pulse_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
)

// A joined chain stage 0 or Compose slot authored with an untyped
// aggregation over a JOINED-ONLY field (r_y exists only on the right
// side of the self-join) is labelled by its smart default AGG_SUM_r_y —
// a default only the post-join schema decides. The Request the overlay
// path reads must carry that Type, so a series overlay scoring it —
// named by params.field or as aggregations[0] — equals the explicitly
// typed twin.

// jsJoin self-joins the multi-grouper cohort on id, prefixing every
// right-side field "r_".
func jsJoin(f *mgFixture) []*types.JoinSpec {
	return []*types.JoinSpec{{Right: f.cohort, On: []types.OnPair{{LeftField: "id", RightField: "id"}}, As: "r_"}}
}

// jsRY is the joined-only aggregation: untyped (typed=false) or the
// explicit twin AGG_SUM, labelled as the default would label it.
func jsRY(typed bool) *types.Aggregation {
	if typed {
		return &types.Aggregation{Type: types.AGG_SUM, Field: "r_y", Label: "AGG_SUM_r_y"}
	}
	return &types.Aggregation{Field: "r_y"}
}

// TestChainSeriesOverlay_JoinedStageDefaultedLabel: the chain arm.
// Stage 0 joins and carries the untyped r_y aggregation, alone (absent
// field scores aggregations[0]) or behind zz_n (field names its default
// label). Stage 1 re-groups stage 0 by region, reading AGG_SUM_r_y.
func TestChainSeriesOverlay_JoinedStageDefaultedLabel(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ChainKinds() {
		for _, named := range []bool{false, true} {
			name := string(kind) + "/aggregations[0]"
			if named {
				name = string(kind) + "/field"
			}
			t.Run(name, func(t *testing.T) {
				run := func(typed bool) string {
					field := ""
					s0 := []*types.Aggregation{jsRY(typed)}
					s1 := []*types.Aggregation{{Type: types.AGG_SUM, Field: "AGG_SUM_r_y", Label: "AGG_SUM_r_y"}}
					if named {
						field = "AGG_SUM_r_y"
						s0 = append([]*types.Aggregation{csZ()}, s0...)
						s1 = append([]*types.Aggregation{chStage1("zz_n")}, s1...)
					}
					req := chChain(f, kind, field, "zz_n")
					req.Stages[0].Request.Aggregations = s0
					req.Stages[0].Request.Joins = jsJoin(f)
					req.Stages[1].Request.Aggregations = s1
					if env := chPredict(t, f.p, req); len(env.Errors) > 0 {
						for _, e := range env.Errors {
							t.Errorf("predict-chain: %s %s %v", e.Code, e.Message, e.Details)
						}
						t.FailNow()
					}
					resp, err := f.p.ProcessChain(context.Background(), req)
					if err != nil {
						t.Fatalf("process chain: %v", err)
					}
					return chRender(t, resp)
				}
				if got, want := run(false), run(true); got != want {
					t.Errorf("untyped r_y scored %s, want the typed twin %s", got, want)
				}
			})
		}
	}
}

// TestComposeSeriesOverlay_JoinedSlotDefaultedLabel: the Compose arm,
// every slot joined. The runtime already scored it (the join arm's
// defaults reach the slot's aggregations); predict-compose now resolves
// the label against the joined schema instead of deferring.
func TestComposeSeriesOverlay_JoinedSlotDefaultedLabel(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ComposeKinds() {
		for _, named := range []bool{false, true} {
			name := string(kind) + "/aggregations[0]"
			if named {
				name = string(kind) + "/field"
			}
			t.Run(name, func(t *testing.T) {
				run := func(typed bool) string {
					field := ""
					aggs := []func() *types.Aggregation{func() *types.Aggregation { return jsRY(typed) }}
					if named {
						field = "AGG_SUM_r_y"
						aggs = append([]func() *types.Aggregation{csZ}, aggs...)
					}
					req := csCompose(f, csOverlay(kind, field), csRegion, aggs...)
					for _, r := range req.Requests {
						r.Joins = jsJoin(f)
					}
					if env := csPredict(t, f.p, req); len(env.Errors) > 0 {
						t.Fatalf("predict-compose: %+v", env.Errors)
					}
					resp, err := f.p.Compose(context.Background(), req)
					if err != nil {
						t.Fatalf("compose: %v", err)
					}
					return csRender(t, resp)
				}
				if got, want := run(false), run(true); got != want {
					t.Errorf("untyped r_y scored %s, want the typed twin %s", got, want)
				}
			})
		}
	}
}

// TestSeriesOverlay_DefaultedLabelUnknownParity: predict resolves an
// untyped, unlabelled aggregation's default label from the slot's or
// stage's schema — the joined one when it joins — instead of deferring
// to the runtime, so a params.field naming a label the default does
// NOT produce (AGG_COUNT_* where the default is AGG_SUM_*) is
// PULSE_OVERLAY_FIELD_UNKNOWN from predict and runtime alike, Details
// equal. Joined (r_y) and unjoined (y) for both arms.
func TestSeriesOverlay_DefaultedLabelUnknownParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, joined := range []bool{false, true} {
		fieldName := "y"
		if joined {
			fieldName = "r_y"
		}
		bad, avail := "AGG_COUNT_"+fieldName, []string{"AGG_SUM_" + fieldName}
		sub := map[bool]string{false: "unjoined", true: "joined"}[joined]
		t.Run("compose/"+sub, func(t *testing.T) {
			untyped := func() *types.Aggregation { return &types.Aggregation{Field: fieldName} }
			req := csCompose(f, csOverlay(types.OverlayKindIndexVsRef, bad), csRegion, untyped)
			if joined {
				for _, r := range req.Requests {
					r.Joins = jsJoin(f)
				}
			}
			// Predict first: the runtime resolves defaults in place on the
			// caller's aggregations, which would hand predict typed ones.
			env := csPredict(t, f.p, req)
			_, err := f.p.Compose(context.Background(), req)
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_FIELD_UNKNOWN)
			want := map[string]any{"index": 0, "kind": string(types.OverlayKindIndexVsRef), "field": bad, "available": avail, "slot": "F"}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details %#v, want %#v", ce.Details, want)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
				t.Fatalf("predict-compose: want PULSE_OVERLAY_FIELD_UNKNOWN first, got %+v", env.Errors)
			}
			if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
				t.Errorf("predict details %#v != runtime %#v", env.Errors[0].Details, ce.Details)
			}
		})
		t.Run("chain/"+sub, func(t *testing.T) {
			// Stage 1 carries the bad label, so the target resolves and
			// stage 0 — the defaulted, possibly joined one — is refused.
			req := chChain(f, types.OverlayKindIndexVsStage, bad, "zz_n")
			req.Stages[0].Request.Aggregations = []*types.Aggregation{{Field: fieldName}}
			if joined {
				req.Stages[0].Request.Joins = jsJoin(f)
			}
			req.Stages[1].Request.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: avail[0], Label: bad}}
			// Predict first: the runtime resolves defaults in place on the
			// caller's aggregations, which would hand predict typed ones.
			env := chPredict(t, f.p, req)
			_, err := f.p.ProcessChain(context.Background(), req)
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_FIELD_UNKNOWN)
			want := map[string]any{"index": 0, "kind": string(types.OverlayKindIndexVsStage), "field": bad, "available": avail, "which": "ref", "stage_index": 0}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details %#v, want %#v", ce.Details, want)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
				t.Fatalf("predict-chain: want PULSE_OVERLAY_FIELD_UNKNOWN first, got %+v", env.Errors)
			}
			if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
				t.Errorf("predict details %#v != runtime %#v", env.Errors[0].Details, ce.Details)
			}
		})
	}
}
