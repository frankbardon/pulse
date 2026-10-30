package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestPredictChain_ExtensionOperatorsMatchProcessChain (#194): the
// public PredictChain and ProcessChain answer the chain gate alike over
// every extension operator category it judges — a Mergeable and an
// undeclared extension aggregator, a Mergeable and an undeclared
// extension grouper, a row_local extension attribute and a two_pass /
// buffered one — on stage 0 and on a later stage. An admitted chain is
// admitted by both; a refused one is refused by both with the same
// code, message and details. PredictChain reaches the gate through the
// instance's ExtensionsSnapshot, so dropping that snapshot from the
// predict options refuses every extension name the runtime admits.
func TestPredictChain_ExtensionOperatorsMatchProcessChain(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	const cohort = "chain_ext.pulse"
	writeParityCohort(t, fsys, cohort, paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: mergeGateExtensions(&parityProbe{})})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	ctx := context.Background()

	agg := func(at types.AggregationType, field string) []*types.Aggregation {
		return []*types.Aggregation{{Type: at, Field: field, Label: "total"}}
	}
	byRegion := func(gt types.GroupType) []*types.Group { return []*types.Group{{Type: gt, Field: "region"}} }
	attr := func(at types.AttributeType) []*types.Attribute {
		return []*types.Attribute{{Type: at, Field: "tags", Label: "d"}}
	}
	// chain builds a fresh request per call: neither side may see the
	// other's normalisation.
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
	grouped := &types.Request{Aggregations: agg(types.AGG_SUM, "score"), Groups: byRegion(types.GROUP_CATEGORY)}

	cases := []struct {
		name  string
		mk    func() *types.ChainRequest
		admit bool
	}{
		{"mergeable aggregator", chain(&types.Request{Aggregations: agg(aggMrgSum, "score"), Groups: byRegion(types.GROUP_CATEGORY)}), true},
		{"mergeable aggregator over decimal128", chain(&types.Request{Aggregations: agg(aggMrgSum, "amount"), Groups: byRegion(types.GROUP_CATEGORY)}), true},
		{"undeclared aggregator", chain(&types.Request{Aggregations: agg(aggMrgSerial, "score")}), false},
		{"mergeable aggregator on a later stage", chain(grouped, &types.Request{Aggregations: agg(aggMrgSum, "total")}), true},
		{"undeclared aggregator on a later stage", chain(grouped, &types.Request{Aggregations: agg(aggMrgSerial, "total")}), false},
		{"mergeable grouper", chain(&types.Request{Aggregations: agg(types.AGG_SUM, "score"), Groups: byRegion(grpMrgMerge)}), true},
		{"mergeable fan-out grouper", chain(&types.Request{Aggregations: agg(types.AGG_SUM, "score"), Groups: []*types.Group{{Type: grpMrgFan, Field: "tags"}}}), true},
		{"undeclared grouper", chain(&types.Request{Aggregations: agg(types.AGG_SUM, "score"), Groups: byRegion(grpMrgCat)}), false},
		{"mergeable aggregator and grouper", chain(&types.Request{Aggregations: agg(aggMrgSum, "score"), Groups: byRegion(grpMrgMerge)}), true},
		{"row_local attribute", chain(&types.Request{Aggregations: agg(types.AGG_SUM, "d"), Attributes: attr(attrMrgRow)}), true},
		{"two_pass attribute", chain(&types.Request{Aggregations: agg(types.AGG_SUM, "d"), Attributes: attr(attrMrgTwo)}), false},
		{"buffered attribute", chain(&types.Request{Aggregations: agg(types.AGG_SUM, "d"), Attributes: attr(attrMrgBuf)}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rerr := p.ProcessChain(ctx, tc.mk())
			env, perr := p.PredictChain(ctx, tc.mk())
			if perr != nil {
				t.Fatalf("PredictChain: %v", perr)
			}
			if tc.admit {
				if rerr != nil {
					t.Fatalf("ProcessChain refused: %v", rerr)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("PredictChain refused what ProcessChain admits: %+v", env.Errors)
				}
				return
			}
			requireCode(t, rerr, errors.PULSE_CHAIN_NOT_MERGEABLE)
			sameEntry(t, env, rerr)
		})
	}
}
