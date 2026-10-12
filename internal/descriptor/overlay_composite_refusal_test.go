package descriptor

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_SeriesKindsMultiGrouperPartitioned: every SERIES
// overlay that reads the host as an ordered series (lag-1 prior,
// rolling window, positional baseline, sibling) and the three _TOTAL
// kinds are ACCEPTED over two groupers — it runs once per partition, every key but the last by
// default — and with an explicit leading-prefix partition_by or [].
// A non-prefix or unknown partition_by is refused
// PULSE_OVERLAY_PARTITION_INVALID with the Details the runtime raises
// (internal/seriespartition.Depth on both sides). Runtime twin:
// processing.TestOverlaySeries_PartitionInvalidRefused.
func TestValidateOverlay_SeriesKindsMultiGrouperPartitioned(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)
	window := json.RawMessage(`{"window": 2}`)
	specs := []types.OverlaySpec{
		{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "ib", Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "db", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "is", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"}}},
		{Name: "ds", Kind: types.OverlayKindDeltaVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"}}},
		{Name: "it", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup},
		{Name: "st", Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup},
		{Name: "zt", Kind: types.OverlayKindZScoreVsTotal, Scope: types.OverlayScopeGroup},
	}
	region := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "region"} }
	segment := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "segment"} }
	withPartition := func(spec types.OverlaySpec, pb any) types.OverlaySpec {
		m := map[string]any{}
		if len(spec.Params) > 0 {
			_ = json.Unmarshal(spec.Params, &m)
		}
		m["partition_by"] = pb
		spec.Params, _ = json.Marshal(m)
		return spec
	}
	predict := func(groups []*types.Group, spec types.OverlaySpec) []descriptorError {
		req := &types.Request{
			Groups:       groups,
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
			Overlays:     []types.OverlaySpec{spec},
		}
		var out []descriptorError
		for _, e := range predictFromBytes(data, req, nil).Errors {
			out = append(out, descriptorError{e.Code, e.Details})
		}
		return out
	}
	for _, spec := range specs {
		t.Run(string(spec.Kind), func(t *testing.T) {
			for name, c := range map[string]struct {
				groups []*types.Group
				spec   types.OverlaySpec
			}{
				"one grouper":             {[]*types.Group{region()}, spec},
				"outer region":            {[]*types.Group{region(), segment()}, spec},
				"inner region":            {[]*types.Group{segment(), region()}, spec},
				"partition_by [region]":   {[]*types.Group{region(), segment()}, withPartition(spec, []string{"region"})},
				"partition_by []":         {[]*types.Group{region(), segment()}, withPartition(spec, []string{})},
				"one grouper, []":         {[]*types.Group{region()}, withPartition(spec, []string{})},
				"partition_by null":       {[]*types.Group{region(), segment()}, withPartition(spec, nil)},
				"full key as a partition": {[]*types.Group{region(), segment()}, withPartition(spec, []string{"region", "segment"})},
			} {
				if errs := predict(c.groups, c.spec); len(errs) > 0 {
					t.Errorf("%s: predict errors %+v, want accepted", name, errs)
				}
			}
			for name, c := range map[string]struct {
				pb     any
				reason string
				field  string
			}{
				"not a prefix":  {[]string{"segment"}, "not_prefix", "segment"},
				"unknown field": {[]string{"warehouse"}, "unknown_field", "warehouse"},
				"not a list":    {"region", "not_string_list", ""},
			} {
				errs := predict([]*types.Group{region(), segment()}, withPartition(spec, c.pb))
				if len(errs) != 1 || errs[0].code != string(errors.PULSE_OVERLAY_PARTITION_INVALID) {
					t.Fatalf("%s: predict errors %+v, want one PULSE_OVERLAY_PARTITION_INVALID", name, errs)
				}
				want := map[string]any{"index": 0, "kind": string(spec.Kind), "partition_by": c.pb,
					"groups": []string{"region", "segment"}, "reason": c.reason}
				if pb, ok := c.pb.([]string); ok {
					want["partition_by"] = pb
				}
				if c.field != "" {
					want["field"] = c.field
				}
				if !reflect.DeepEqual(errs[0].details, want) {
					t.Errorf("%s: details %#v, want %#v", name, errs[0].details, want)
				}
			}
		})
	}
}

type descriptorError struct {
	code    string
	details map[string]any
}
