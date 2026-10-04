package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The weight tests reuse zoneCohort: `n` is an unsigned integer (a
// valid weight), `cat` categorical and `d` a calendar date (both
// refused as weights).

func weightPulse(t *testing.T, fs afero.Fs, def *types.WeightSpec) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, DefaultWeight: def})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func weightedSum(cohort string, w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Weight:       reqW,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total", Weight: w}},
	}
}

// TestWeight_NewValidatesDefault: Options.DefaultWeight is shape-checked
// at pulse.New — no field, or an unknown kind, is PROCESSING_CONFIG.
func TestWeight_NewValidatesDefault(t *testing.T) {
	for _, def := range []*types.WeightSpec{{}, {Field: "n", Kind: "replicate"}} {
		_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), DefaultWeight: def})
		ce := requireCode(t, err, errors.PROCESSING_CONFIG)
		if ce.Details["slot"] != "Options.DefaultWeight" {
			t.Fatalf("details = %v", ce.Details)
		}
	}
	for _, def := range []*types.WeightSpec{{Field: "n"}, {Field: "n", Kind: types.WeightKindFrequency}} {
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), DefaultWeight: def}); err != nil {
			t.Fatalf("valid default refused: %v", err)
		}
	}
}

// TestWeight_PredictMatchesRuntime: every weight refusal is raised by
// the runtime and predict with the same code, message and details —
// a mistyped slot or request weight, an unknown kind, an unknown field,
// and a joined (prefixed) field resolved through the joined schema.
func TestWeight_PredictMatchesRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := weightPulse(t, fs, nil)
	ctx := context.Background()
	join := func(r *types.Request) *types.Request {
		r.Joins = []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}}
		return r
	}
	refused := map[string]func() *types.Request{
		"slot categorical": func() *types.Request { return weightedSum(cohort, types.SlotWeightField("cat"), nil) },
		"request date":     func() *types.Request { return weightedSum(cohort, types.SlotWeight{}, &types.WeightSpec{Field: "d"}) },
		"slot bad kind": func() *types.Request {
			return weightedSum(cohort, types.SlotWeightOf(types.WeightSpec{Field: "n", Kind: "x"}), nil)
		},
		"unknown field":      func() *types.Request { return weightedSum(cohort, types.SlotWeightField("nope"), nil) },
		"joined categorical": func() *types.Request { return join(weightedSum(cohort, types.SlotWeightField("r_cat"), nil)) },
	}
	for name, mk := range refused {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.Process(ctx, mk())
			if rerr == nil {
				t.Fatal("runtime accepted a refused weight")
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			sameEntry(t, env, rerr)
		})
	}

	accepted := map[string]func() *types.Request{
		"slot":    func() *types.Request { return weightedSum(cohort, types.SlotWeightField("n"), nil) },
		"request": func() *types.Request { return weightedSum(cohort, types.SlotWeight{}, &types.WeightSpec{Field: "n"}) },
		"null": func() *types.Request {
			return weightedSum(cohort, types.NullSlotWeight(), &types.WeightSpec{Field: "n"})
		},
		"joined": func() *types.Request { return join(weightedSum(cohort, types.SlotWeightField("r_n"), nil)) },
		"joined rq": func() *types.Request {
			return join(weightedSum(cohort, types.SlotWeight{}, &types.WeightSpec{Field: "r_n"}))
		},
	}
	for name, mk := range accepted {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Process(ctx, mk()); err != nil {
				t.Fatalf("runtime: %v", err)
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			if len(env.Errors) != 0 {
				t.Fatalf("predict refused what runtime accepts: %+v", env.Errors)
			}
			if w := env.Data.(*descriptor.PredictResult).Weights; len(w) != 1 {
				t.Fatalf("Weights = %+v, want one slot", w)
			}
		})
	}
}

// TestWeight_PredictReportsResolution: Pulse.Predict carries the
// instance default through to PredictResult.Weights, and a request with
// no weight anywhere omits the key from its JSON.
func TestWeight_PredictReportsResolution(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()

	plain := weightPulse(t, fs, nil)
	pr, err := plain.Predict(ctx, weightedSum(cohort, types.SlotWeight{}, nil))
	if err != nil || !pr.Valid {
		t.Fatalf("predict: %v %+v", err, pr)
	}
	b, _ := json.Marshal(pr)
	if bytes.Contains(b, []byte(`"weights"`)) {
		t.Fatalf("unweighted predict carries weights: %s", b)
	}

	p := weightPulse(t, fs, &types.WeightSpec{Field: "n", Kind: types.WeightKindFrequency})
	req := weightedSum(cohort, types.SlotWeight{}, nil)
	req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "base", Weight: types.NullSlotWeight()})
	pr, err = p.Predict(ctx, req)
	if err != nil || !pr.Valid {
		t.Fatalf("predict: %v %+v", err, pr)
	}
	want := []descriptor.ResolvedWeight{
		{Slot: "aggregations[0]", Operator: "AGG_SUM", Field: "n", Kind: "frequency", Status: pr.Weights[0].Status, Source: "options"},
		{Slot: "aggregations[1]", Operator: "AGG_COUNT", Status: "opted_out", Source: "slot"},
	}
	if !reflect.DeepEqual(pr.Weights, want) {
		t.Fatalf("Weights = %+v\nwant %+v", pr.Weights, want)
	}
	if _, err := p.Process(ctx, req); err != nil {
		t.Fatalf("runtime: %v", err)
	}
}

// TestWeight_ValidatorsMatchRuntime: Compose slots and chain stages
// each resolve their own weights; the validators refuse what the
// runtime refuses, located by details.request / details.stage.
func TestWeight_ValidatorsMatchRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := weightPulse(t, fs, nil)
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("compose", func(t *testing.T) {
		mk := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{
				weightedSum(cohort, types.SlotWeightField("n"), nil),
				weightedSum(cohort, types.SlotWeightField("cat"), nil),
			}}
		}
		_, rerr := p.Compose(ctx, mk())
		ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
		if !reflect.DeepEqual(ce.Details["request"], 1) {
			t.Fatalf("details = %v", ce.Details)
		}
		sameEntry(t, descx.ValidateComposeWithOptions(mk(), opts), rerr)
	})
	t.Run("chain stage 1", func(t *testing.T) {
		mk := func() *types.ChainRequest {
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{
					{Request: &types.Request{
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
						Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					}},
					{Request: &types.Request{
						Weight:       &types.WeightSpec{Field: "total", Kind: "bogus"},
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
					}},
				},
			}
		}
		_, rerr := p.ProcessChain(ctx, mk())
		ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
		if !reflect.DeepEqual(ce.Details["stage"], 1) {
			t.Fatalf("details = %v", ce.Details)
		}
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
}
