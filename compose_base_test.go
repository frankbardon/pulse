package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
)

// compose_base_test.go pins the Compose `base` (library-gaps E6-S1, G5a)
// at the facade: the base is merged into every explicit slot before the
// params inheritance and the sweep / banner expansion, so a batch with
// a base answers exactly as its hand-merged twin on both orchestrators,
// predict judges the merged slots, the echo shows them (and no base),
// result() reads an aggregation declared only in the base, base items
// fan out with for_each after the merge, and a collision or a hidden
// capability:compose_base is refused identically by predict and both
// orchestrators.

// baseBody is a batch over the optimize fixture cohort whose base
// declares the cohort, a params-reading formula, a for_each lag window
// and two aggregations; each slot adds its own aggregation over a base
// window, slot "b" also overrides a param and filters.
const baseBody = `{
  "params": {"k": 10, "shift": 1},
  "base": {
    "cohort": {"filename": "opt_price.pulse"},
    "params": {"k": 2},
    "attributes": [{"type": "ATTR_FORMULA", "label": "scaled", "expression": "units * params.k + params.shift"}],
    "windows": [{"type": "WIN_LAG", "field": "units", "label": "units_lag{{n}}", "order_by": [{"field": "week"}],
                 "params": {"offset": {"$var": "n"}}, "for_each": {"axes": [{"name": "n", "values": [1, 2]}]}}],
    "aggregations": [
      {"type": "AGG_SUM", "field": "scaled", "label": "total_scaled"},
      {"type": "AGG_COUNT", "field": "week", "label": "rows"}
    ]
  },
  "requests": [
    {"label": "a", "aggregations": [{"type": "AGG_AVERAGE", "field": "units_lag2", "label": "lag2_mean"}]},
    {"label": "b", "params": {"k": 3}, "filterers": [{"type": "FILTER_RANGE", "field": "week", "values": ["10", "39"]}],
     "aggregations": [{"type": "AGG_AVERAGE", "field": "units_lag1", "label": "lag1_mean"}]}
  ]
}`

// baseTwin is baseBody merged by hand: the base items first in every
// list, the slot's own after; params key-wise, the slot's key winning.
const baseTwin = `{
  "params": {"k": 10, "shift": 1},
  "requests": [
    {"label": "a", "cohort": {"filename": "opt_price.pulse"}, "params": {"k": 2},
     "attributes": [{"type": "ATTR_FORMULA", "label": "scaled", "expression": "units * params.k + params.shift"}],
     "windows": [{"type": "WIN_LAG", "field": "units", "label": "units_lag{{n}}", "order_by": [{"field": "week"}],
                  "params": {"offset": {"$var": "n"}}, "for_each": {"axes": [{"name": "n", "values": [1, 2]}]}}],
     "aggregations": [
       {"type": "AGG_SUM", "field": "scaled", "label": "total_scaled"},
       {"type": "AGG_COUNT", "field": "week", "label": "rows"},
       {"type": "AGG_AVERAGE", "field": "units_lag2", "label": "lag2_mean"}
     ]},
    {"label": "b", "cohort": {"filename": "opt_price.pulse"}, "params": {"k": 3},
     "attributes": [{"type": "ATTR_FORMULA", "label": "scaled", "expression": "units * params.k + params.shift"}],
     "windows": [{"type": "WIN_LAG", "field": "units", "label": "units_lag{{n}}", "order_by": [{"field": "week"}],
                  "params": {"offset": {"$var": "n"}}, "for_each": {"axes": [{"name": "n", "values": [1, 2]}]}}],
     "filterers": [{"type": "FILTER_RANGE", "field": "week", "values": ["10", "39"]}],
     "aggregations": [
       {"type": "AGG_SUM", "field": "scaled", "label": "total_scaled"},
       {"type": "AGG_COUNT", "field": "week", "label": "rows"},
       {"type": "AGG_AVERAGE", "field": "units_lag1", "label": "lag1_mean"}
     ]}
  ]
}`

// TestComposeBase_MatchesHandMergedTwin: on both orchestrators the base
// batch's responses and normalized request are byte-identical to its
// hand-merged twin's — the base items run in every slot, the base
// for_each window expands per slot, params resolve slot > base >
// top-level — and the echo carries no base. Predict's echo agrees.
func TestComposeBase_MatchesHandMergedTwin(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{EchoRequest: true})
	for name, run := range composeRuns(p) {
		got, err := run(decodeComposedJSON(t, baseBody))
		if err != nil {
			t.Fatalf("%s base: %v", name, err)
		}
		want, err := run(decodeComposedJSON(t, baseTwin))
		if err != nil {
			t.Fatalf("%s twin: %v", name, err)
		}
		if g, w := mustJSON(t, "base", got.Responses), mustJSON(t, "base", want.Responses); g != w {
			t.Fatalf("%s: responses differ from the twin\nbase: %s\ntwin: %s", name, g, w)
		}
		if got.NormalizedRequest == nil || got.NormalizedRequest.Base != nil {
			t.Fatalf("%s: normalized request %+v, want merged slots and no base", name, got.NormalizedRequest)
		}
		if g, w := mustJSON(t, "base", got.NormalizedRequest), mustJSON(t, "base", want.NormalizedRequest); g != w {
			t.Fatalf("%s: normalized request differs from the twin\nbase: %s\ntwin: %s", name, g, w)
		}
		// The figures themselves: k = 2 on slot a (base over top-level),
		// k = 3 on slot b (slot over base); shift = 1 from the top level.
		a, b := got.Responses[0].Data[0], got.Responses[1].Data[0]
		if n, _ := toFloat(a["rows"]); n != float64(optfixture.Weeks) {
			t.Errorf("%s: a.rows = %v", name, a["rows"])
		}
		if n, _ := toFloat(b["rows"]); n != 30 {
			t.Errorf("%s: b.rows = %v, want the 30 filtered weeks", name, b["rows"])
		}
		if _, ok := a["lag2_mean"]; !ok {
			t.Errorf("%s: slot a lacks its own aggregation over the base window: %v", name, a)
		}
	}

	env, err := p.PredictCompose(context.Background(), decodeComposedJSON(t, baseBody))
	if err != nil {
		t.Fatal(err)
	}
	twin, err := p.PredictCompose(context.Background(), decodeComposedJSON(t, baseTwin))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) > 0 || len(twin.Errors) > 0 {
		t.Fatalf("predict errors: base %+v twin %+v", env.Errors, twin.Errors)
	}
	if g, w := mustJSON(t, "base", env.Request), mustJSON(t, "base", twin.Request); g != w || env.Request == nil {
		t.Fatalf("predict echo differs from the twin\nbase: %s\ntwin: %s", g, w)
	}
	// The result's `request` keeps the batch as written; everything it
	// judged — every slot verdict — is the twin's.
	judged := func(env *descriptor.Envelope) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(mustJSON(t, "base", env.Data)), &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "request")
		return mustJSON(t, "base", m)
	}
	if g, w := judged(env), judged(twin); g != w {
		t.Fatalf("predict result differs from the twin\nbase: %s\ntwin: %s", g, w)
	}
}

// TestComposeBase_ResultReadsBaseAggregation: an optimize bound's
// result() reads an aggregation declared only in the base — the result
// catalog is built over the merged slots, on predict and both
// orchestrators.
func TestComposeBase_ResultReadsBaseAggregation(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	upper := `result("weeks", "top_price") - 5`
	c := decodeOpt(t, optfixture.PriceModel, func(c *ComposedRequest) {
		c.Base = &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "price", Label: "top_price"}}}
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

// TestComposeBase_RefusalParity: a base-vs-slot label collision, a
// labelled base and a hidden capability:compose_base are refused with
// the same code, message and details by predict and both orchestrators.
func TestComposeBase_RefusalParity(t *testing.T) {
	t.Parallel()

	all, err := InitFeatureProfile("")
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, f := range all.Features {
		if f != "capability:compose_base" {
			keep = append(keep, f)
		}
	}
	all.Features = keep

	cases := []struct {
		name   string
		opts   Options
		edit   func(*ComposedRequest)
		code   perr.Code
		reason string
	}{
		{"label collision", Options{}, func(c *ComposedRequest) {
			c.Requests[1].Aggregations = []*types.Aggregation{{Type: types.AGG_MAX, Field: "price", Label: "rows"}}
		}, perr.PULSE_COMPOSE_BASE_COLLISION, "label_collision"},
		{"base label", Options{}, func(c *ComposedRequest) { c.Base.Label = "shared" }, perr.PULSE_COMPOSE_BASE_COLLISION, "base_label"},
		{"hidden capability", Options{FeatureProfile: all}, nil, perr.PULSE_REQUEST_UNKNOWN_FIELD, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := optPulse(t, tc.opts)
			c := decodeComposedJSON(t, baseBody)
			if tc.edit != nil {
				tc.edit(c)
			}
			env, err := p.PredictCompose(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("predict errors %+v, want %s", env.Errors, tc.code)
			}
			if tc.reason != "" && env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict details %v, want reason %s", env.Errors[0].Details, tc.reason)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(c)
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}
