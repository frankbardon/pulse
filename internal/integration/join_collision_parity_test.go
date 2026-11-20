package integration_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestJoinFieldCollision_RefusedLikeRuntime: a join whose right fields
// collide with the left ones (no `as` prefix) is refused by predict, the
// Compose slot validator and the chain stage-0 validator with the
// runtime's own PULSE_JOIN_FIELD_COLLISION — the one rule
// (internal/encoding.JoinedSchema). The Compose slot validator used to
// swallow the collision (it judged the slot schema-less) while the
// runtime refused the slot.
func TestJoinFieldCollision_RefusedLikeRuntime(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	mk := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}},
			Joins:        []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}}},
		}
	}
	t.Run("process and predict", func(t *testing.T) {
		_, rerr := p.Process(ctx, mk())
		requireCode(t, rerr, errors.PULSE_JOIN_FIELD_COLLISION)
		sameEntry(t, predictEnvelope(t, p, fs, cohort, mk()), rerr)
	})
	t.Run("compose slot", func(t *testing.T) {
		compose := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{
				{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()},
				mk(),
			}}
		}
		_, rerr := p.Compose(ctx, compose())
		if ce := requireCode(t, rerr, errors.PULSE_JOIN_FIELD_COLLISION); ce.Details["request"] != 1 {
			t.Fatalf("details = %v, want request=1", ce.Details)
		}
		sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), rerr)
		env, perr := p.PredictCompose(ctx, compose())
		if perr != nil {
			t.Fatal(perr)
		}
		sameEntry(t, env, rerr)
	})
	t.Run("chain stage 0", func(t *testing.T) {
		chain := func() *types.ChainRequest {
			stage := mk()
			stage.Cohort = nil
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{{Request: stage}}}
		}
		_, rerr := p.ProcessChain(ctx, chain())
		requireCode(t, rerr, errors.PULSE_JOIN_FIELD_COLLISION)
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), chain(), opts), rerr)
	})
}
