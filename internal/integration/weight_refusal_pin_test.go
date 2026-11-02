package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// TestWeight_UnweightedOnlyTestsRefusalPinned (U64 E2-S2, #617): the
// two built-in row tests with no weighted form, TEST_ANOVA_RM and
// TEST_SHAPIRO_WILK, are PERMANENTLY refused under a weight, never run
// unweighted, so "How Pulse is validated" can state the refusal rather
// than a missing golden. Each is refused PULSE_WEIGHT_UNSUPPORTED with its own stated
// reason (details.reason = weighting.RefusalReason, carried in the
// message), at run time and by predict with the same entry, for both
// weight kinds and every weight source (instance default, request,
// slot); `weight: null` on the slot still runs it unweighted.
func TestWeight_UnweightedOnlyTestsRefusalPinned(t *testing.T) {
	t.Parallel()

	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	// The operator's acceptance request on field x, its test slot
	// carrying w and the request reqW.
	req := func(op string, w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates[op], "$F", "x")), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		r.Tests[0].Weight = w
		return &r
	}
	for _, op := range []string{string(types.TEST_ANOVA_RM), string(types.TEST_SHAPIRO_WILK)} {
		reason := weighting.RefusalReason(op)
		if reason == "" {
			t.Fatalf("%s: no permanent refusal reason", op)
		}
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			spec := &types.WeightSpec{Field: "y", Kind: kind}
			plain := weightPulse(t, fs, nil)
			sources := []struct {
				name string
				p    *pulse.Pulse
				w    types.SlotWeight
				reqW *types.WeightSpec
			}{
				{"default", weightPulse(t, fs, spec), types.SlotWeight{}, nil},
				{"request", plain, types.SlotWeight{}, spec},
				{"slot", plain, types.SlotWeightOf(*spec), nil},
			}
			for _, src := range sources {
				where := op + "/" + string(kind) + "/" + src.name
				_, rerr := src.p.Process(ctx, req(op, src.w, src.reqW))
				ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
				if ce.Details["operator"] != op || ce.Details["field"] != "y" || ce.Details["reason"] != reason {
					t.Errorf("%s: details = %v, want operator %s, field y, reason %q", where, ce.Details, op, reason)
				}
				if !strings.Contains(ce.Message, "cannot be weighted: "+reason) {
					t.Errorf("%s: message %q does not state the reason", where, ce.Message)
				}
				sameEntry(t, predictEnvelope(t, src.p, fs, cohort, req(op, src.w, src.reqW)), rerr)
			}
			// Opting the slot out runs the test unweighted, on both sides.
			withDefault := weightPulse(t, fs, spec)
			optOut := req(op, types.NullSlotWeight(), nil)
			if _, err := withDefault.Process(ctx, optOut); err != nil {
				t.Errorf("%s/%s: opted-out slot refused: %v", op, kind, err)
			}
			if env := predictEnvelope(t, withDefault, fs, cohort, req(op, types.NullSlotWeight(), nil)); len(env.Errors) != 0 {
				t.Errorf("%s/%s: predict refused the opted-out slot: %+v", op, kind, env.Errors)
			}
		}
	}
}
