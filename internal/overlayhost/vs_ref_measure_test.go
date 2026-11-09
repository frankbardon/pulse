package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestVsRefMeasureRead pins the Compose two-sample series allowlist
// (U66 #444): the mean built-ins, the moment carriers, the element
// rule and the extension rule.
func TestVsRefMeasureRead(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		measure             types.AggregationType
		element, extMoments bool
		want                VsRefRead
	}{
		{"", false, false, VsRefReadUnjudged},
		{types.AGG_AVERAGE, false, false, VsRefReadMean},
		{types.AGG_WEIGHTED_MEAN, false, false, VsRefReadMean},
		{types.AGG_WELFORD, false, false, VsRefReadMoments},
		{types.AGG_VARIANCE, false, false, VsRefReadMoments},
		{types.AGG_STDDEV, false, false, VsRefReadMoments},
		{types.AGG_SUM, false, false, VsRefReadRefused},
		{types.AGG_COUNT, false, false, VsRefReadRefused},
		{types.AGG_MEDIAN, false, false, VsRefReadRefused},
		{types.AGG_MODE, false, false, VsRefReadRefused},
		{types.AGG_SUM, false, true, VsRefReadRefused}, // a built-in is judged by its type
		{types.AGG_VEC_MEAN, true, false, VsRefReadMean},
		{types.AGG_VEC_SUM, true, false, VsRefReadRefused},
		{types.AGG_AVERAGE, true, false, VsRefReadRefused},
		{"AGG_ACME_X", false, false, VsRefReadRefused},
		{"AGG_ACME_X", false, true, VsRefReadMoments},
	} {
		if got := VsRefMeasureRead(c.measure, c.element, c.extMoments); got != c.want {
			t.Errorf("VsRefMeasureRead(%s, element=%v, ext=%v) = %s, want %s", c.measure, c.element, c.extMoments, got, c.want)
		}
	}
}

// TestComposeVsRefMeasureRefusal: the refusal order (reference, then
// targets, then the mixed-read check), the kinds it judges, and the
// extension moments read off the instance's AggregationKinds.
func TestComposeVsRefMeasureRefusal(t *testing.T) {
	t.Parallel()

	slot := func(aggs ...types.AggregationType) *types.Request {
		r := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}}
		for _, a := range aggs {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: a, Field: "x"})
		}
		return r
	}
	labels := []string{"r", "t", "u"}
	index := func(label string) int {
		for j, l := range labels {
			if l == label {
				return j
			}
		}
		return -1
	}
	kinds := vectors.AggregationKinds{Extension: func(name string) vectors.ExtensionAggregation {
		return vectors.ExtensionAggregation{Registered: true, CarriesMoments: name == "AGG_ACME_M"}
	}}
	cols := func(reqs []*types.Request) func(int) seriesfield.Columns {
		return func(j int) seriesfield.Columns { return vectors.AggregationColumns(reqs[j], nil, kinds) }
	}
	type want struct {
		code   errors.Code
		slot   string
		reason string
	}
	for _, c := range []struct {
		name    string
		kind    types.OverlayKind
		targets []string
		field   string
		reqs    []*types.Request
		want    *want
	}{
		{"means run", types.OverlayKindTVsRef, []string{"t"}, "", []*types.Request{slot(types.AGG_AVERAGE), slot(types.AGG_WEIGHTED_MEAN), slot(types.AGG_SUM)}, nil},
		{"moments run", types.OverlayKindZVsRef, []string{"t"}, "", []*types.Request{slot(types.AGG_VARIANCE), slot("AGG_ACME_M"), nil}, nil},
		{"reference first", types.OverlayKindTVsRef, []string{"t"}, "", []*types.Request{slot(types.AGG_SUM), slot(types.AGG_COUNT), nil},
			&want{errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, "r", "measure_not_mean"}},
		{"second target", types.OverlayKindTVsRef, []string{"t", "u"}, "", []*types.Request{slot(types.AGG_AVERAGE), slot(types.AGG_AVERAGE), slot(types.AGG_MAX)},
			&want{errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, "u", "measure_not_mean"}},
		{"extension without moments", types.OverlayKindTVsRef, []string{"t"}, "", []*types.Request{slot("AGG_ACME_X"), slot("AGG_ACME_X"), nil},
			&want{errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, "r", "measure_not_mean"}},
		{"field selects", types.OverlayKindTVsRef, []string{"t"}, "AGG_SUM_x", []*types.Request{slot(types.AGG_AVERAGE, types.AGG_SUM), slot(types.AGG_AVERAGE, types.AGG_SUM), nil},
			&want{errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, "r", "measure_not_mean"}},
		{"mixed reads", types.OverlayKindTVsRef, []string{"t"}, "", []*types.Request{slot(types.AGG_WELFORD), slot(types.AGG_AVERAGE), nil},
			&want{errors.PULSE_OVERLAY_SCHEMA_DIVERGENT, "", "mixed_measure_read"}},
		{"untyped not judged", types.OverlayKindTVsRef, []string{"t"}, "", []*types.Request{slot(""), slot(types.AGG_AVERAGE), nil}, nil},
		{"crosstab not judged", types.OverlayKindTVsRef, []string{"t"}, "", []*types.Request{{Crosstab: &types.CrosstabSpec{Cell: &types.Aggregation{Type: types.AGG_SUM}}}, slot(types.AGG_AVERAGE), nil}, nil},
		{"no target not judged", types.OverlayKindTVsRef, nil, "", []*types.Request{slot(types.AGG_SUM), slot(types.AGG_SUM), nil}, nil},
		{"other kind not judged", types.OverlayKindIndexVsRef, []string{"t"}, "", []*types.Request{slot(types.AGG_SUM), slot(types.AGG_SUM), nil}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.ComposeOverlaySpec{Kind: c.kind, Reference: "r", Targets: c.targets}
			if c.field != "" {
				spec.Params = map[string]any{"field": c.field}
			}
			ce := ComposeVsRefMeasureRefusal(spec, c.kind, 3, c.reqs, index, cols(c.reqs), kinds)
			if c.want == nil {
				if ce != nil {
					t.Fatalf("unexpected refusal: %v", ce)
				}
				return
			}
			if ce == nil || ce.Code != c.want.code || ce.Details["reason"] != c.want.reason || ce.Details["overlay_index"] != 3 {
				t.Fatalf("got %v %v, want %s reason %s", ce, ce.Details, c.want.code, c.want.reason)
			}
			if c.want.slot != "" && ce.Details["slot"] != c.want.slot {
				t.Fatalf("slot = %v, want %s", ce.Details["slot"], c.want.slot)
			}
		})
	}
}
