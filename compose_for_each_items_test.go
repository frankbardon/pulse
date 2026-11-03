package pulse

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
)

// compose_for_each_items_test.go pins the aggregation and matrix item
// fan-out (library-gaps E7-S1, G4) at the Compose facade: a `base`
// aggregation carrying a for_each expands in every slot after the merge,
// and result() reads an expanded aggregation label — on predict and both
// orchestrators.

// forEachBaseBody is a two-slot batch whose base fans one aggregation
// out over two fields; forEachBaseTwin is the same batch with the
// fan-out written by hand in each slot.
const forEachBaseBody = `{
  "base": {
    "cohort": {"filename": "opt_price.pulse"},
    "aggregations": [{"type": "AGG_MAX", "field": "{{f}}", "label": "top_{{f}}",
                      "for_each": {"axes": [{"name": "f", "values": ["price", "units"]}]}}]
  },
  "requests": [
    {"label": "a", "aggregations": [{"type": "AGG_COUNT", "field": "week", "label": "n"}]},
    {"label": "b", "filterers": [{"type": "FILTER_RANGE", "field": "week", "values": ["10", "39"]}]}
  ]
}`

const forEachBaseTwin = `{
  "requests": [
    {"label": "a", "cohort": {"filename": "opt_price.pulse"}, "aggregations": [
      {"type": "AGG_MAX", "field": "price", "label": "top_price"},
      {"type": "AGG_MAX", "field": "units", "label": "top_units"},
      {"type": "AGG_COUNT", "field": "week", "label": "n"}]},
    {"label": "b", "cohort": {"filename": "opt_price.pulse"},
     "filterers": [{"type": "FILTER_RANGE", "field": "week", "values": ["10", "39"]}],
     "aggregations": [
      {"type": "AGG_MAX", "field": "price", "label": "top_price"},
      {"type": "AGG_MAX", "field": "units", "label": "top_units"}]}
  ]
}`

// TestComposeForEachItems_BaseAggregationExpandsPerSlot: a base
// aggregation's for_each expands in each slot after the merge — the
// responses are the hand-expanded twin's on both
// orchestrators (the normalized batch shows the merged slots), and
// predict judges the batch valid.
func TestComposeForEachItems_BaseAggregationExpandsPerSlot(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{EchoRequest: true})
	for name, run := range composeRuns(p) {
		got, err := run(decodeComposedJSON(t, forEachBaseBody))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := run(decodeComposedJSON(t, forEachBaseTwin))
		if err != nil {
			t.Fatalf("%s twin: %v", name, err)
		}
		if g, w := mustJSON(t, "for_each", got.Responses), mustJSON(t, "for_each", want.Responses); g != w {
			t.Fatalf("%s: responses differ from the twin\ngot:  %s\nwant: %s", name, g, w)
		}
		// The normalized batch is the merged one: like a window's, the
		// aggregation for_each expands per slot as the slot runs, so the
		// batch shows the base item first in each slot, as written.
		if nr := got.NormalizedRequest; nr == nil || nr.Base != nil || len(nr.Requests) != 2 ||
			nr.Requests[0].Aggregations[0].ForEach == nil || nr.Requests[1].Aggregations[0].ForEach == nil {
			t.Fatalf("%s: normalized request %s, want the merged slots", name, mustJSON(t, "for_each", nr))
		}
		for i, r := range got.Responses {
			if _, ok := r.Data[0]["top_units"]; !ok {
				t.Fatalf("%s: slot %d lacks the expanded aggregation: %v", name, i, r.Data[0])
			}
		}
	}
	env, err := p.PredictCompose(context.Background(), decodeComposedJSON(t, forEachBaseBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused: %+v", env.Errors)
	}
	if res := env.Data.(*ComposePredictResult); !res.Valid || len(res.Slots) != 2 {
		t.Fatalf("predict valid=%v slots=%d", res.Valid, len(res.Slots))
	}
}

// TestComposeForEachItems_ResultReadsExpandedAggregation: an optimize
// bound's result() names a label a for_each aggregation adds — the
// result() catalog holds the expanded labels, on predict and both
// orchestrators.
func TestComposeForEachItems_ResultReadsExpandedAggregation(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	upper := `result("weeks", "top_price") - 5`
	c := decodeOpt(t, optfixture.PriceModel, func(c *ComposedRequest) {
		c.Requests[0].Aggregations = append(c.Requests[0].Aggregations, &types.Aggregation{
			Type: types.AGG_MAX, Field: "{{f}}", Label: "top_{{f}}",
			ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "f", Values: []any{"units", "price"}}}},
		})
		c.Optimize[0].Variables[0].Upper = types.BoundExpr(upper)
	})
	want := []descriptor.UnresolvedScalar{{Path: "optimize[0].variables[0].upper", Expression: upper, Reads: []string{"result"}}}
	if got := predictUnresolved(t, p, c); !reflect.DeepEqual(got, want) {
		t.Fatalf("predict unresolved_scalars = %+v, want %+v", got, want)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// The top price is 15, so the bound is 10, below the optimum 17.5.
		if v := oneOptimum(t, name, resp).Variables[0]; math.Abs(v.Value-10) > 1e-9 || v.Active != types.OptimizeActiveUpper {
			t.Errorf("%s: price %+v, want 10 on its upper bound", name, v)
		}
	}
}
