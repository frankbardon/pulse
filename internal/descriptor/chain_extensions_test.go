package descriptor

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestValidateChainWithExtensions_MergeableOperators pins the predict
// half of the chain gate against an ExtensionsSnapshot: an extension
// aggregator or grouper is admitted on its DECLARED Mergeable flag, a
// row_local extension attribute as a row-local operator — the same
// answers the runtime gate (processing.CanChainRequestWithExtensions)
// gives — while an undeclared one, a two_pass attribute, and every
// extension name under a nil snapshot stay refused with
// PULSE_CHAIN_NOT_MERGEABLE.
func TestValidateChainWithExtensions_MergeableOperators(t *testing.T) {
	snap := &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{
			{Name: "AGG_ACME_SUM", Streamable: true, Mergeable: true},
			{Name: "AGG_ACME_SERIAL", Streamable: true},
		},
		Groupers: []descriptor.OperatorMeta{
			{Name: "GROUP_ACME_CAT", Streamable: true, Mergeable: true},
			{Name: "GROUP_ACME_SERIAL", Streamable: true},
		},
		Attributes: []descriptor.OperatorMeta{
			{Name: "ATTR_ACME_ROW", Streamable: true, Mode: "row_local"},
			{Name: "ATTR_ACME_TWO", Streamable: true, Mode: "two_pass"},
		},
	}
	data := buildSimplePulseBytes(t)
	stage := func(agg types.AggregationType, grp types.GroupType, attr types.AttributeType) *types.ChainRequest {
		req := &types.Request{Aggregations: []*types.Aggregation{{Type: agg, Field: "x", Label: "v"}}}
		if grp != "" {
			req.Groups = []*types.Group{{Type: grp, Field: "x"}}
		}
		if attr != "" {
			req.Attributes = []*types.Attribute{{Type: attr, Field: "x", Label: "d"}}
		}
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "x.pulse"},
			Stages: []*types.ChainStage{{Name: "s0", Request: req}},
		}
	}
	cases := []struct {
		name string
		req  *types.ChainRequest
		want bool
	}{
		{"ext_mergeable_aggregator", stage("AGG_ACME_SUM", "", ""), true},
		{"ext_undeclared_aggregator", stage("AGG_ACME_SERIAL", "", ""), false},
		{"ext_mergeable_grouper", stage(types.AGG_SUM, "GROUP_ACME_CAT", ""), true},
		{"ext_undeclared_grouper", stage(types.AGG_SUM, "GROUP_ACME_SERIAL", ""), false},
		{"ext_row_local_attribute", stage(types.AGG_SUM, "", "ATTR_ACME_ROW"), true},
		{"ext_two_pass_attribute", stage(types.AGG_SUM, "", "ATTR_ACME_TWO"), false},
		{"builtin", stage(types.AGG_SUM, types.GROUP_CATEGORY, types.ATTR_FORMULA), true},
	}
	notMergeable := func(env *descriptor.Envelope) bool {
		for _, e := range env.Errors {
			if e.Code == "PULSE_CHAIN_NOT_MERGEABLE" {
				return true
			}
		}
		return false
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := ValidateChainWithExtensions(bytes.NewReader(data), c.req, snap)
			if got := !notMergeable(env); got != c.want {
				t.Errorf("with snapshot: admitted = %v, want %v (errors %+v)", got, c.want, env.Errors)
			}
			// Without a snapshot only built-in names pass: the nil
			// case is exactly ValidateChain.
			nilEnv := ValidateChainWithExtensions(bytes.NewReader(data), c.req, nil)
			plain := ValidateChain(bytes.NewReader(data), c.req)
			if notMergeable(nilEnv) != notMergeable(plain) {
				t.Errorf("nil snapshot disagrees with ValidateChain: %+v vs %+v", nilEnv.Errors, plain.Errors)
			}
			if c.name != "builtin" && !notMergeable(plain) {
				t.Errorf("ValidateChain (no snapshot) admitted an extension operator")
			}
		})
	}
}
