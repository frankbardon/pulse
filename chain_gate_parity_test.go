package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestChainGate_ValidatorMatchesRuntime: ProcessChain and
// ValidateChain run the one shared chain gate (internal/mergegate), so
// every stage either side refuses is refused by the other with the
// same code, message and details — including the two checks the
// validator used to skip (a non-streamable filterer and a built-in
// aggregator over a decimal128 field) and the extension half (an
// extension aggregator merges on its DECLARED flag and reads decimals
// itself).
func TestChainGate_ValidatorMatchesRuntime(t *testing.T) {
	fs := afero.NewMemMapFs()
	const cohort = "gate.pulse"
	writeParityCohort(t, fs, cohort, paritySchema(t), 0, 40)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	probe := &parityProbe{}
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			paritySumRegistration(probe, "AGG_GATE_SUM", true),
			paritySumRegistration(probe, "AGG_GATE_SOLO", false),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	opts := &descx.PredictOptions{Extensions: facadebridge.ExtensionsSnapshot(p)}
	ctx := context.Background()

	sum := func(field string) []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: "total"}}
	}
	region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	chain := func(stages ...*types.Request) func() *types.ChainRequest {
		return func() *types.ChainRequest {
			req := &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}}
			for i, s := range stages {
				clone := *s
				req.Stages = append(req.Stages, &types.ChainStage{Name: "s" + string(rune('0'+i)), Request: &clone})
			}
			return req
		}
	}
	ok0 := &types.Request{Aggregations: sum("qty"), Groups: region}

	cases := []struct {
		name   string
		mk     func() *types.ChainRequest
		reason string // substring of the refusal message; "" = accepted
		stage  int
	}{
		{"mergeable", chain(ok0, &types.Request{Aggregations: sum("total")}), "", 0},
		{"no aggregator", chain(&types.Request{Groups: region}), "no aggregator", 0},
		{"non-mergeable aggregator", chain(&types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "qty"}}}), "aggregator AGG_MEDIAN is not mergeable", 0},
		{"non-scalar on a later stage", chain(ok0, &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_FREQUENCY, Field: "region"}}, Groups: region}), "non-scalar", 1},
		{"two-pass attribute", chain(&types.Request{Aggregations: sum("qty"), Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "qty", Label: "z"}}}), "attribute ATTR_ZSCORE is not row-local", 0},
		{"non-mergeable grouper", chain(&types.Request{Aggregations: sum("qty"), Groups: []*types.Group{{Type: types.GROUP_QUANTILE, Field: "qty"}}}), "grouper GROUP_QUANTILE is not mergeable", 0},
		{"windows", chain(&types.Request{Aggregations: sum("qty"), Windows: []*types.Window{{Type: types.WIN_LAG, Field: "qty"}}}), "windows, features", 0},
		{"non-streamable filterer", chain(&types.Request{Aggregations: sum("qty"), Filterers: []*types.Filterer{{Type: "FILTER_NOT_REGISTERED", Field: "qty"}}}), "filterer FILTER_NOT_REGISTERED is not streamable", 0},
		{"built-in over decimal128", chain(&types.Request{Aggregations: sum("amount"), Groups: region}), `decimal128 field "amount"`, 0},
		{"mergeable extension over decimal128", chain(&types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_GATE_SUM", Field: "amount", Label: "total"}}, Groups: region}), "", 0},
		{"non-mergeable extension", chain(&types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_GATE_SOLO", Field: "qty"}}}), "aggregator AGG_GATE_SOLO is not mergeable", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rerr := p.ProcessChain(ctx, tc.mk())
			env := descx.ValidateChainWithOptions(bytes.NewReader(data), tc.mk(), opts)
			if tc.reason == "" {
				if rerr != nil {
					t.Fatalf("runtime refused: %v", rerr)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("validator refused what runtime accepts: %+v", env.Errors)
				}
				return
			}
			ce := requireCode(t, rerr, errors.PULSE_CHAIN_NOT_MERGEABLE)
			if !strings.Contains(ce.Message, tc.reason) {
				t.Fatalf("runtime message %q lacks %q", ce.Message, tc.reason)
			}
			if ce.Details["stage_index"] != tc.stage {
				t.Fatalf("runtime details = %v, want stage_index %d", ce.Details, tc.stage)
			}
			sameEntry(t, env, rerr)
		})
	}
}

// TestChainStageJoin_RefusedBothSides: a chain stage after stage 0
// carrying Joins is refused — it used to be dropped silently — by the
// runtime and the validator alike, PULSE_CHAIN_STAGE_JOIN {count,
// stage, stage_name}, before that stage's defaults, zones and gate.
func TestChainStageJoin_RefusedBothSides(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	mk := func(laterStage *types.Request) func() *types.ChainRequest {
		return func() *types.ChainRequest {
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{
					{Name: "s0", Request: &types.Request{
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
						Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					}},
					{Name: "s1", Request: laterStage},
				},
			}
		}
	}
	join := []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "cat", RightField: "cat"}}, As: "r_"}}
	for _, tc := range []struct {
		name  string
		stage *types.Request
	}{
		{"mergeable stage", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total"}},
			Joins:        join,
		}},
		// The join refusal precedes the gate: a stage that would also
		// fail the gate still reports the join first, on both sides.
		{"gate-failing stage", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "total"}},
			Joins:        join,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := mk(tc.stage)
			_, rerr := p.ProcessChain(ctx, req())
			ce := requireCode(t, rerr, errors.PULSE_CHAIN_STAGE_JOIN)
			if ce.Details["stage"] != 1 || ce.Details["stage_name"] != "s1" || ce.Details["count"] != 1 {
				t.Fatalf("runtime details = %v, want stage 1 / s1 / count 1", ce.Details)
			}
			sameEntry(t, descx.ValidateChain(bytes.NewReader(data), req()), rerr)
		})
	}
}

// TestJoinTooMany_LocatedInsideComposeAndChain: the join-count refusal
// is a located refusal — inside Compose (serial and FailFast parallel)
// it carries details.request, inside a chain details.stage, exactly as
// a zone refusal does, and the validators add the same location.
func TestJoinTooMany_LocatedInsideComposeAndChain(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	compose := func() *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{
			{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()},
			twoJoins(cohort),
		}}
	}
	want := func(t *testing.T, err error, key string, idx int) {
		t.Helper()
		ce := requireCode(t, err, errors.PULSE_JOIN_TOO_MANY)
		if ce.Details[key] != idx || ce.Details["count"] != 2 {
			t.Fatalf("details = %v, want %s=%d and count=2", ce.Details, key, idx)
		}
	}
	t.Run("compose serial", func(t *testing.T) {
		_, rerr := p.Compose(ctx, compose())
		want(t, rerr, "request", 1)
		sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), rerr)
	})
	t.Run("compose parallel fail-fast", func(t *testing.T) {
		_, rerr := p.ComposeParallel(ctx, compose(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		want(t, rerr, "request", 1)
		sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), rerr)
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
		want(t, rerr, "stage", 0)
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
	t.Run("single process stays unlocated", func(t *testing.T) {
		_, rerr := p.Process(ctx, twoJoins(cohort))
		ce := requireCode(t, rerr, errors.PULSE_JOIN_TOO_MANY)
		b, _ := json.Marshal(ce.Details)
		if string(b) != `{"count":2}` {
			t.Fatalf("details = %s, want only count", b)
		}
	})
}
