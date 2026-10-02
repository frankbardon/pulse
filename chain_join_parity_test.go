package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestChainDefaults_ValidatorMatchesRuntime: ValidateChain judges each
// stage after the same smart-defaults pass the runtime applies (unless
// DisableDefaults), so a stage relying on a defaulted Type is accepted
// or refused by both sides alike — including the next stage's view of
// a defaulted aggregator's "<TYPE>_<field>" output label.
func TestChainDefaults_ValidatorMatchesRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	on := zonePulse(t, fs, "")
	off, err := pulse.New(pulse.Options{FS: fs, DisableDefaults: true})
	if err != nil {
		t.Fatal(err)
	}

	// n (numeric) → AGG_SUM, cat (categorical) → GROUP_CATEGORY: both
	// chain-mergeable. Stage 1's f64 column "total" → AGG_SUM, labelled
	// AGG_SUM_total, which stage 2 references.
	mergeable := func() *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{
				{Name: "s0", Request: &types.Request{
					Aggregations: []*types.Aggregation{{Field: "n", Label: "total"}},
					Groups:       []*types.Group{{Field: "cat"}},
				}},
				{Name: "s1", Request: &types.Request{
					Aggregations: []*types.Aggregation{{Field: "total"}},
					Groups:       []*types.Group{{Field: "cat"}},
				}},
				{Name: "s2", Request: &types.Request{
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "AGG_SUM_total", Label: "grand"}},
				}},
			},
		}
	}
	// cat (categorical) → AGG_FREQUENCY: non-scalar, refused by the gate.
	nonMergeable := func() *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{{Name: "s0", Request: &types.Request{
				Aggregations: []*types.Aggregation{{Field: "cat"}},
			}}},
		}
	}

	t.Run("mergeable after default", func(t *testing.T) {
		if _, err := on.ProcessChain(ctx, mergeable()); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		env := descx.ValidateChain(bytes.NewReader(data), mergeable())
		if len(env.Errors) != 0 {
			t.Fatalf("validator refused what runtime accepts: %+v", env.Errors)
		}
		res := env.Data.(*descx.ChainValidationResult)
		if res.Request.Stages[0].Request.Aggregations[0].Type != "" {
			t.Fatal("validator mutated the caller's request")
		}
	})
	t.Run("non-mergeable after default", func(t *testing.T) {
		_, rerr := on.ProcessChain(ctx, nonMergeable())
		requireCode(t, rerr, errors.PULSE_CHAIN_NOT_MERGEABLE)
		sameEntry(t, descx.ValidateChain(bytes.NewReader(data), nonMergeable()), rerr)
	})
	t.Run("defaults disabled", func(t *testing.T) {
		_, rerr := off.ProcessChain(ctx, mergeable())
		requireCode(t, rerr, errors.PULSE_CHAIN_NOT_MERGEABLE)
		env := descx.ValidateChainWithOptions(bytes.NewReader(data), mergeable(), &descx.PredictOptions{DisableDefaults: true})
		sameEntry(t, env, rerr)
	})
	t.Run("defaults disabled on a later stage", func(t *testing.T) {
		mk := func() *types.ChainRequest {
			req := mergeable()
			s0 := req.Stages[0].Request
			s0.Aggregations[0].Type = types.AGG_SUM
			s0.Groups[0].Type = types.GROUP_CATEGORY
			return req
		}
		_, rerr := off.ProcessChain(ctx, mk())
		ce := requireCode(t, rerr, errors.PULSE_CHAIN_NOT_MERGEABLE)
		if ce.Details["stage_index"] != 1 {
			t.Fatalf("runtime details = %v, want stage_index 1", ce.Details)
		}
		env := descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), &descx.PredictOptions{DisableDefaults: true})
		sameEntry(t, env, rerr)
	})
}

// twoJoins is a mergeable request carrying two JoinSpecs (both
// self-joins on n).
func twoJoins(cohort string) *types.Request {
	spec := func(as string) *types.JoinSpec {
		return &types.JoinSpec{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: as}
	}
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Joins:        []*types.JoinSpec{spec("a_"), spec("b_")},
	}
}

// TestJoinTooMany_PredictAndValidatorsMatchRuntime: more than one
// JoinSpec is refused by predict, the Compose slot validator and the
// chain validator exactly as the runtime refuses it — the one shared
// rule (descriptor.JoinCountRefusal), PULSE_JOIN_TOO_MANY {count}.
func TestJoinTooMany_PredictAndValidatorsMatchRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}

	t.Run("predict", func(t *testing.T) {
		_, rerr := p.Process(ctx, twoJoins(cohort))
		if ce := requireCode(t, rerr, errors.PULSE_JOIN_TOO_MANY); ce.Details["count"] != 2 {
			t.Fatalf("runtime details = %v, want count 2", ce.Details)
		}
		env := predictEnvelope(t, p, fs, cohort, twoJoins(cohort))
		sameEntry(t, env, rerr)
		if env.Data.(*descriptor.PredictResult).Valid {
			t.Fatal("predict Valid=true")
		}
	})
	t.Run("crosstab", func(t *testing.T) {
		// The rule runs before Process's crosstab dispatch, so a
		// crosstab with two JoinSpecs is refused under the join code
		// (it used to fall through to the fused path's
		// PROCESSING_INTERNAL), and predict says the same.
		mk := func() *types.Request {
			req := twoJoins(cohort)
			req.Groups, req.Aggregations = nil, nil
			req.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Params: json.RawMessage(`{"interval":10}`)}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
			}
			return req
		}
		_, rerr := p.Process(ctx, mk())
		requireCode(t, rerr, errors.PULSE_JOIN_TOO_MANY)
		sameEntry(t, predictEnvelope(t, p, fs, cohort, mk()), rerr)
	})
	t.Run("compose slot", func(t *testing.T) {
		mk := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{
				{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()},
				twoJoins(cohort),
			}}
		}
		_, rerr := p.Compose(ctx, mk())
		requireCode(t, rerr, errors.PULSE_JOIN_TOO_MANY)
		sameEntry(t, descx.ValidateComposeWithOptions(mk(), opts), rerr)
	})
	t.Run("chain stage 0", func(t *testing.T) {
		mk := func() *types.ChainRequest {
			stage := twoJoins(cohort)
			stage.Cohort = nil
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{{Request: stage}},
			}
		}
		_, rerr := p.ProcessChain(ctx, mk())
		requireCode(t, rerr, errors.PULSE_JOIN_TOO_MANY)
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
}
