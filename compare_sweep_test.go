package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// compare_sweep_test.go pins a nested-model comparison reading a
// sweep-bound fit (`compare` over `fits[].from_sweep`, U69 #643) —
// predict ≡ runtime: a comparison every sweep slot refuses is the same
// refusal on both; one only some slots refuse is valid at predict with
// those slots under `unresolved`, and the run judges the winner — a
// matching winner computes the request-bound twin's comparison to the
// byte, a non-matching one fails the comparison alone (Strict: the
// batch). The figures against R: internal/service
// compose_compare_sweep_oracle_test.go.

// cmpSweepBatch is a ranked three-slot sweep over rows id ∈ [0, hi]
// (hi 119, 59, 89) whose body holds "small" (y ~ x1) and "big"
// (y ~ x1 + x2), ranked by big's n_obs in order (asc: hi 59 wins,
// slot 1; desc: hi 119, slot 0), with the batch fits and comparison
// given.
func cmpSweepBatch(order types.SweepRankOrder, fits []types.FitSpec, compare ...types.FitComparisonSpec) *ComposedRequest {
	return &ComposedRequest{
		Fits:    fits,
		Compare: compare,
		Requests: []*Request{{Label: "rows", Cohort: &types.Cohort{Filename: marginsCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}}}},
		Sweep: &types.SweepSpec{
			Axes:  []types.SweepAxis{{Name: "hi", Values: []any{json.Number("119"), json.Number("59"), json.Number("89")}}},
			Label: "hi_{{hi}}",
			Request: json.RawMessage(`{"cohort":{"filename":"` + marginsCohort + `"},
				"filterers":[{"type":"FILTER_RANGE","field":"id","values":["0","{{hi}}"]}],
				"regressions":[{"type":"REG_OLS","name":"small","target":"y","predictors":["x1"]},
					{"type":"REG_OLS","name":"big","target":"y","predictors":["x1","x2"]}]}`),
			Rank: &types.SweepRank{By: "regressions.big.n_obs", Order: order},
		},
	}
}

// cmpUpTo is a request-bound OLS fit over rows id ∈ [0, hi].
func cmpUpTo(hi string, preds ...string) *Request {
	r := cmpFit(marginsCohort, cmpOLS(preds...))
	r.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", hi}}}
	return r
}

func fromSweep(name, regression string) types.FitSpec {
	return types.FitSpec{Name: name, FromSweep: &types.FitFromSweep{Regression: regression}}
}

var cmpSweepSpec = types.FitComparisonSpec{Name: "adds_x2", Restricted: "m0", Full: "m1"}

func predictCompare(t *testing.T, p *Pulse, c *ComposedRequest) []descriptor.ComparisonSummary {
	t.Helper()
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("predict refused: %+v", env.Errors)
	}
	return env.Data.(*descx.ComposeValidationResult).Compare
}

// TestCompareSweep_EverySlotRefusesPredictMatchesRuntime: the swapped
// sweep-bound sides (not_nested in every slot) and a request-bound side
// whose rows no slot matches (request_mismatch in every slot) are
// refused by predict and by both runtimes with the same code, message
// and details, before anything runs.
func TestCompareSweep_EverySlotRefusesPredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	p := cmpPulse(t, Options{})
	for _, tc := range []struct {
		name   string
		fits   []types.FitSpec
		reason string
	}{
		{"swapped", []types.FitSpec{fromSweep("m0", "big"), fromSweep("m1", "small")}, "not_nested"},
		{"no slot's rows", []types.FitSpec{{Name: "m0", Request: cmpUpTo("30", "x1")}, fromSweep("m1", "big")}, "request_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cmpSweepBatch(types.SweepRankAsc, tc.fits, cmpSweepSpec)
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_COMPARISON_INVALID) || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict errors %+v", env.Errors)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, errors.PULSE_COMPARISON_INVALID)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestCompareSweep_UnresolvedWinnerDecides: a request-bound restricted
// fit over id ∈ [0, 59] against the sweep-bound "big" matches only slot
// 1's rows — predict accepts it and lists slots 0 and 2 under
// `unresolved` (request_mismatch); the run with slot 1 winning computes
// exactly the request-bound twin's comparison, and with slot 0 winning
// fails the comparison alone with PULSE_COMPARISON_INVALID naming the
// winner (a PULSE_SLOT_FAILED warning; Strict fails the batch).
func TestCompareSweep_UnresolvedWinnerDecides(t *testing.T) {
	t.Parallel()
	p := cmpPulse(t, Options{})
	fits := func() []types.FitSpec {
		return []types.FitSpec{{Name: "m0", Request: cmpUpTo("59", "x1")}, fromSweep("m1", "big")}
	}
	summary := predictCompare(t, p, cmpSweepBatch(types.SweepRankAsc, fits(), cmpSweepSpec))
	want := &descriptor.ComparisonUnresolved{Slots: 3, Refused: []descriptor.ComparisonSlotRefusal{
		{Slot: 0, Label: "hi_119", Reason: "request_mismatch"}, {Slot: 2, Label: "hi_89", Reason: "request_mismatch"}}}
	if len(summary) != 1 || !reflect.DeepEqual(summary[0].Unresolved, want) || summary[0].Test != types.ComparisonTestF {
		t.Fatalf("predict summary %+v (unresolved %+v)", summary, summary[0].Unresolved)
	}

	twin := cmpBatch(marginsCohort, nil, cmpSweepSpec)
	twin.Fits = []types.FitSpec{{Name: "m0", Request: cmpUpTo("59", "x1")}, {Name: "m1", Request: cmpUpTo("59", "x1", "x2")}}
	twinResp, err := p.Compose(context.Background(), twin)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := comparisonsJSON(t, twinResp)
	for name, run := range composeRuns(p) {
		resp, err := run(cmpSweepBatch(types.SweepRankAsc, fits(), cmpSweepSpec))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.Ranking[0].Slot != 1 {
			t.Fatalf("%s: vacuous, winner %+v", name, resp.Ranking[0])
		}
		if got := comparisonsJSON(t, resp); got != wantJSON {
			t.Fatalf("%s: comparisons\n got %s\nwant %s", name, got, wantJSON)
		}
		if summary[0].Test != resp.Comparisons[0].Test || summary[0].Distribution != resp.Comparisons[0].Distribution {
			t.Fatalf("%s: predict %+v, run %+v", name, summary[0], resp.Comparisons[0])
		}

		resp, err = run(cmpSweepBatch(types.SweepRankDesc, fits(), cmpSweepSpec))
		if err != nil {
			t.Fatalf("%s desc: %v", name, err)
		}
		got := resp.Comparisons[0]
		if resp.Ranking[0].Slot != 0 || got.Error == nil || got.Error.Code != string(errors.PULSE_COMPARISON_INVALID) ||
			got.Error.Details["reason"] != "request_mismatch" || got.Error.Details["sweep_slot"] != 0 || got.Error.Details["sweep_label"] != "hi_119" {
			t.Fatalf("%s desc: comparison %+v (error %+v)", name, got, got.Error)
		}
		if got.Statistic != 0 || len(got.Terms) != 0 {
			t.Fatalf("%s desc: a refused comparison carries figures: %+v", name, got)
		}
		failed := false
		for _, w := range resp.Warnings {
			failed = failed || (w.Code == string(errors.PULSE_SLOT_FAILED) && w.Details["slot_kind"] == "comparisons" && w.Details["code"] == string(errors.PULSE_COMPARISON_INVALID))
		}
		if !failed {
			t.Fatalf("%s desc: no PULSE_SLOT_FAILED warning: %+v", name, resp.Warnings)
		}
	}

	strict := cmpPulse(t, Options{Strict: true})
	for name, run := range composeRuns(strict) {
		_, err := run(cmpSweepBatch(types.SweepRankDesc, fits(), cmpSweepSpec))
		ce := requireCoded(t, err, errors.PULSE_COMPARISON_INVALID)
		if ce.Details["slot_kind"] != "comparisons" || ce.Details["sweep_slot"] != 0 {
			t.Fatalf("%s strict: %v", name, ce.Details)
		}
	}
}

// TestCompareSweep_BothSidesBoundMatchHandWritten: two sides bound from
// the same sweep compare in every slot (one winner binds both), so
// predict leaves `unresolved` absent, and the run's comparison is, to
// the byte, the request-bound pair over the winning slot's rows — under
// either winner, on both orchestrators.
func TestCompareSweep_BothSidesBoundMatchHandWritten(t *testing.T) {
	t.Parallel()
	p := cmpPulse(t, Options{})
	fits := func() []types.FitSpec { return []types.FitSpec{fromSweep("m0", "small"), fromSweep("m1", "big")} }
	summary := predictCompare(t, p, cmpSweepBatch(types.SweepRankAsc, fits(), cmpSweepSpec))
	if len(summary) != 1 || summary[0].Unresolved != nil || summary[0].Test != types.ComparisonTestF {
		t.Fatalf("predict summary %+v", summary)
	}
	for _, tc := range []struct {
		order types.SweepRankOrder
		hi    string
		slot  int
	}{{types.SweepRankAsc, "59", 1}, {types.SweepRankDesc, "119", 0}} {
		twin := cmpBatch(marginsCohort, nil, cmpSweepSpec)
		twin.Fits = []types.FitSpec{{Name: "m0", Request: cmpUpTo(tc.hi, "x1")}, {Name: "m1", Request: cmpUpTo(tc.hi, "x1", "x2")}}
		twinResp, err := p.Compose(context.Background(), twin)
		if err != nil {
			t.Fatal(err)
		}
		want := comparisonsJSON(t, twinResp)
		for name, run := range composeRuns(p) {
			resp, err := run(cmpSweepBatch(tc.order, fits(), cmpSweepSpec))
			if err != nil {
				t.Fatalf("%s %s: %v", name, tc.order, err)
			}
			if resp.Ranking[0].Slot != tc.slot {
				t.Fatalf("%s %s: winner %+v", name, tc.order, resp.Ranking[0])
			}
			if got := comparisonsJSON(t, resp); got != want {
				t.Fatalf("%s %s: comparisons\n got %s\nwant %s", name, tc.order, got, want)
			}
		}
	}
}
