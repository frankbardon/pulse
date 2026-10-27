package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// TestWeight_FormulaTotalMatchesPredict: an ATTR_FORMULA that calls
// total() is frequency-only (survey-tables E1-S3, #398). Under a
// frequency weight from every source predict reports the slot applied
// and the runtime total is Σw·x (the weighted AGG_SUM of x); under a
// probability weight predict and runtime raise the same
// PULSE_WEIGHT_UNSUPPORTED (code, message, details); `weight: null`
// runs it unweighted, byte-identical to a plain instance; a row-local
// formula stays skipped under either kind.
func TestWeight_FormulaTotalMatchesPredict(t *testing.T) {
	t.Parallel()

	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	prob := &types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	freq := &types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	mk := func(expr string, attrW, sumW types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		return &types.Request{
			Cohort:     &types.Cohort{Filename: cohort},
			Weight:     reqW,
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "tx", Expression: expr, Weight: attrW}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_MAX, Field: "tx", Label: "tx", Weight: types.NullSlotWeight()},
				{Type: types.AGG_SUM, Field: "x", Label: "sx", Weight: sumW},
			},
		}
	}
	for _, spec := range []*types.WeightSpec{freq, prob} {
		for name, src := range map[string]struct {
			def  *types.WeightSpec
			reqW *types.WeightSpec
			w    types.SlotWeight
		}{
			"default": {def: spec},
			"request": {reqW: spec},
			"slot":    {w: types.SlotWeightOf(*spec)},
		} {
			p := weightPulse(t, fs, src.def)
			t.Run(string(spec.Kind)+"/"+name, func(t *testing.T) {
				req := func() *types.Request { return mk("total(x) + 0 * x", src.w, src.w, src.reqW) }
				if spec.Kind == types.WeightKindProbability {
					_, rerr := p.Process(ctx, req())
					ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
					if ce.Details["slot"] != "attributes[0]" || ce.Details["function"] != "total" ||
						ce.Details["reason"] != weighting.FormulaTotalKindReason || ce.Details["kind"] != "probability" {
						t.Fatalf("refusal details = %v", ce.Details)
					}
					if !strings.Contains(ce.Message, "ATTR_FORMULA calling total()") {
						t.Fatalf("refusal message = %q", ce.Message)
					}
					sameEntry(t, predictEnvelope(t, p, fs, cohort, req()), rerr)
				} else {
					pr, err := p.Predict(ctx, req())
					if err != nil {
						t.Fatal(err)
					}
					if st := slotStatus(pr.Weights, "attributes[0]"); st != descriptor.WeightStatusApplied+"/frequency" {
						t.Fatalf("predict status %q, want applied/frequency", st)
					}
					resp, err := p.Process(ctx, req())
					if err != nil {
						t.Fatalf("runtime refused: %v", err)
					}
					got, want := resp.Data[0]["tx"].(float64), resp.Data[0]["sx"].(float64)
					if math.Abs(got-want) > 1e-12*math.Abs(want) {
						t.Fatalf("weighted total %v, want Σw·x %v", got, want)
					}
				}
				// A row-local formula reads no weight under either kind.
				local := mk("x * 2", src.w, types.NullSlotWeight(), src.reqW)
				local.Aggregations = local.Aggregations[:1]
				pr, err := p.Predict(ctx, local)
				if err != nil {
					t.Fatal(err)
				}
				if st := slotStatus(pr.Weights, "attributes[0]"); !strings.HasPrefix(st, descriptor.WeightStatusSkippedNotWeightAware) {
					t.Fatalf("row-local formula status %q, want skipped", st)
				}
				if _, err := p.Process(ctx, local); err != nil {
					t.Fatalf("row-local formula refused: %v", err)
				}
			})
		}
	}
	// weight: null opts the total out: byte-identical to a plain instance.
	plain := weightPulse(t, fs, nil)
	opted := weightPulse(t, fs, prob)
	optedReq := mk("x / total(x)", types.NullSlotWeight(), types.NullSlotWeight(), nil)
	a, err := opted.Process(ctx, optedReq)
	if err != nil {
		t.Fatalf("opted-out total refused: %v", err)
	}
	b, err := plain.Process(ctx, mk("x / total(x)", types.SlotWeight{}, types.SlotWeight{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a.Data)
	jb, _ := json.Marshal(b.Data)
	if string(ja) != string(jb) {
		t.Fatalf("opted out %s, plain %s", ja, jb)
	}
}

func slotStatus(ws []descriptor.ResolvedWeight, slot string) string {
	for _, w := range ws {
		if w.Slot == slot {
			return w.Status + "/" + w.Kind
		}
	}
	return ""
}
