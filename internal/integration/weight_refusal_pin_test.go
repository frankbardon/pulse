package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestWeight_UnweightedOnlyTestsRefusalPinned (U64 E2-S2, #617): the
// built-in tests with no weighted form — the row tests TEST_ANOVA_RM and
// TEST_SHAPIRO_WILK, and the post-test-only TEST_TREND and
// TEST_TUKEY_HSD — are PERMANENTLY refused under a weight, never run
// unweighted, so "How Pulse is validated" can state the refusal rather
// than a missing golden. Each is refused PULSE_WEIGHT_UNSUPPORTED with
// its stated reason (details.reason, carried in the message: the row
// test's own descx.OperatorWeightRefusal, a post-test's shared
// descx.PostTestWeightRefusal), at run time and by predict with the same
// entry, for both weight kinds and every weight source (instance
// default, request, slot); `weight: null` on the slot still runs it
// unweighted.
func TestWeight_UnweightedOnlyTestsRefusalPinned(t *testing.T) {
	t.Parallel()

	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	postOnly := map[string]bool{string(types.TEST_TREND): true, string(types.TEST_TUKEY_HSD): true}
	// The operator's acceptance request on field x (a post-test:
	// inferentialRefusalRequests' grouped AVERAGE / COUNT result), its
	// test slot carrying w and the request reqW.
	req := func(op string, w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		if postOnly[op] {
			r := inferentialRefusalRequests(t, cohort, w)["post/"+op]
			r.Weight = reqW
			return r
		}
		var r types.Request
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates[op], "$F", "x")), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		r.Tests[0].Weight = w
		return &r
	}
	for _, op := range []string{string(types.TEST_ANOVA_RM), string(types.TEST_SHAPIRO_WILK), string(types.TEST_TREND), string(types.TEST_TUKEY_HSD)} {
		reason, ok := descx.OperatorWeightRefusal(op)
		if postOnly[op] {
			reason, ok = descx.PostTestWeightRefusal(), true
		}
		if !ok || reason == "" {
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
			// A post-test's opt-out run (Tukey over every subject group,
			// seconds) is the heavy tier's TestWeight_InferentialRefusalsMatchPredict
			// null/post arm; predict is checked here.
			withDefault := weightPulse(t, fs, spec)
			optOut := req(op, types.NullSlotWeight(), nil)
			if !postOnly[op] {
				if _, err := withDefault.Process(ctx, optOut); err != nil && !isDataOutcome(err) {
					t.Errorf("%s/%s: opted-out slot refused: %v", op, kind, err)
				}
			}
			if env := predictEnvelope(t, withDefault, fs, cohort, req(op, types.NullSlotWeight(), nil)); len(env.Errors) != 0 {
				t.Errorf("%s/%s: predict refused the opted-out slot: %+v", op, kind, env.Errors)
			}
		}
	}
}

// isDataOutcome reports an opted-out run's data-dependent refusal (the
// acceptance cohort's outcomes), which is not a weight refusal.
func isDataOutcome(err error) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && acceptanceDataOutcomes[ce.Code]
}
