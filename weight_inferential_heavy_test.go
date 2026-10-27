//go:build heavy

package pulse_test

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestWeight_InferentialRefusalsMatchPredict: every refused inferential
// surface — each refused built-in TEST_* (tests and post-tests), REG_*
// (plain and with a resample / selection modifier), ATTR_PERCENTILE and
// the refused Inferential overlays — is refused PULSE_WEIGHT_UNSUPPORTED at runtime
// and predict with the same code, message and details, under the
// instance default, a request weight and a slot weight alike; a
// permanent refusal states its reason (details.reason), a pending one
// says it has no weighted form yet, and none names the roadmap. The
// slot's `weight: null` opts out on both sides (an instance with
// Options.DefaultWeight still runs TEST_T).
func TestWeight_InferentialRefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	plain := weightPulse(t, fs, nil)
	withDefault := weightPulse(t, fs, def)

	type source struct {
		p    *pulse.Pulse
		w    types.SlotWeight
		reqW *types.WeightSpec
	}
	sources := map[string]source{
		"default": {withDefault, types.SlotWeight{}, nil},
		"request": {plain, types.SlotWeight{}, def},
		"slot":    {plain, types.SlotWeightField("y"), nil},
	}
	for sname, src := range sources {
		reqs := inferentialRefusalRequests(t, cohort, src.w)
		keys := sortedKeys(reqs)
		for _, key := range keys {
			t.Run(sname+"/"+key, func(t *testing.T) {
				mk := func() *types.Request {
					r := inferentialRefusalRequests(t, cohort, src.w)[key]
					r.Weight = src.reqW
					return r
				}
				_, rerr := src.p.Process(ctx, mk())
				ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
				if ce.Details["field"] != "y" || ce.Details["slot"] == nil || ce.Details["operator"] == nil {
					t.Fatalf("details = %v", ce.Details)
				}
				reason, _ := ce.Details["reason"].(string)
				switch {
				case permanentRefusalKeys[key] && (reason == "" || !strings.Contains(ce.Message, "cannot be weighted: "+reason)):
					t.Fatalf("permanent refusal without its reason: %s %v", ce.Message, ce.Details)
				case !permanentRefusalKeys[key] && (reason != "" || !strings.Contains(ce.Message, "has no weighted form yet")):
					t.Fatalf("pending refusal: %s %v", ce.Message, ce.Details)
				case strings.Contains(ce.Message, "U12") || strings.Contains(ce.Message, "not implemented"):
					t.Fatalf("refusal names the roadmap: %s", ce.Message)
				}
				sameEntry(t, predictEnvelope(t, src.p, fs, cohort, mk()), rerr)
			})
		}
	}

	for _, key := range sortedKeys(inferentialRefusalRequests(t, cohort, types.NullSlotWeight())) {
		t.Run("null/"+key, func(t *testing.T) {
			mk := func() *types.Request {
				r := inferentialRefusalRequests(t, cohort, types.NullSlotWeight())[key]
				r.Weight = def
				return r
			}
			_, rerr := withDefault.Process(ctx, mk())
			var ce *errors.CodedError
			if rerr != nil && !(stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code]) {
				t.Fatalf("opted-out slot refused: %v", rerr)
			}
			if env := predictEnvelope(t, withDefault, fs, cohort, mk()); len(env.Errors) != 0 {
				t.Fatalf("predict refused the opted-out slot: %+v", env.Errors)
			}
		})
	}
}
