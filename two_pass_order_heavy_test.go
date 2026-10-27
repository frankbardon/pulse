//go:build heavy

package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestTwoPassOrder_Layers covers row-local → two-pass → row-local →
// two-pass chains (including an extension two-pass in the middle and a
// filter), on every arm including the >threshold single file.
func TestTwoPassOrder_Layers(t *testing.T) {
	t.Parallel()

	cases := map[string]*types.Request{
		"repro_formula_zscore": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "qty * 3 + 2", Label: "x"},
				{Type: types.ATTR_ZSCORE, Field: "x", Label: "zx"},
			},
			Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "zx", Label: "max_zx"}},
		},
		"rl_tp_rl_tp": {
			Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "qty", Values: []string{"1", "11"}}},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "qty * qty + 1", Label: "u"},
				{Type: types.ATTR_ZSCORE, Field: "u", Label: "z1"},
				{Type: types.ATTR_FORMULA, Expression: "z1 * z1 + qty", Label: "w"},
				{Type: "ATTR_TPO_CENTER", Field: "w", Label: "c"},
				{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "pc"},
				{Type: types.ATTR_FORMULA, Expression: "c * pc", Label: "v"},
				{Type: types.ATTR_NORMALIZED, Field: "v", Label: "out"},
			},
			Aggregations: append(tpoAggs("out"),
				&types.Aggregation{Type: types.AGG_SUM, Field: "z1", Label: "sum_z1"},
				&types.Aggregation{Type: types.AGG_MAX, Field: "c", Label: "max_c"}),
		},
		"opaque_extension_layer": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_ZSCORE, Field: "qty", Label: "z"},
				{Type: types.ATTR_FORMULA, Expression: "z + qty", Label: "w"},
				{Type: "ATTR_TPO_OPAQUE", Field: "qty", Params: []byte(`{"src":"w"}`), Label: "out"},
			},
			Aggregations: tpoAggs("out"),
		},
	}
	for _, arm := range tpoArms(t, true) {
		t.Run(arm.name, func(t *testing.T) {
			p, path := arm.open(t)
			for name, req := range cases {
				t.Run(name, func(t *testing.T) { runTPOParity(t, p, path, req) })
			}
		})
	}
}
