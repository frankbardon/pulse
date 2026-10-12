package descriptor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// seriesAxisCohort carries a date, a datetime and two categorical
// columns for the series-axis advisory.
func seriesAxisCohort(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Units shipped per order line"},
		{Name: "day", Type: encoding.FieldTypeDate, Description: "Day the order shipped"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Moment the order was placed"},
		{Name: "warehouse", Type: encoding.FieldTypeCategoricalU8, Description: "Warehouse that shipped the order", Dictionary: makeDictionary(t, "north", "south", "east")},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region of the customer", Dictionary: makeDictionary(t, "a", "b")},
	}})
}

func seriesAxisAdvisoriesFor(t *testing.T, req *types.Request, opts *PredictOptions) []descriptor.Advisory {
	t.Helper()
	var out []descriptor.Advisory
	for _, a := range predictFromBytes(seriesAxisCohort(t), req, opts).Data.(*descriptor.PredictResult).Advisories {
		if a.Code == string(errors.PULSE_ADVISORY_SERIES_AXIS_NOT_DATE) {
			out = append(out, a)
		}
	}
	return out
}

func saGroup(gt types.GroupType, field string) *types.Group {
	return &types.Group{Type: gt, Field: field}
}

func saRequest(groups []*types.Group, specs ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Groups:       groups,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}},
		Overlays:     specs,
	}
}

func saPrior() types.OverlaySpec {
	return types.OverlaySpec{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup}
}

func saWithPartition(spec types.OverlaySpec, pb any) types.OverlaySpec {
	spec.Params, _ = json.Marshal(map[string]any{"partition_by": pb})
	return spec
}

// TestPredict_AdvisorySeriesAxisNotDate: an ordered series overlay over
// two or more groupers with no partition_by fires once per overlay when
// the last grouper (the implicit series axis) is not a date grouper —
// [day, warehouse] fires naming date_axis day, [warehouse, day] is
// quiet. A date or datetime last axis, one grouper, an explicit
// partition_by (even []), a _TOTAL kind, a crosstab and a hidden kind
// never fire.
func TestPredict_AdvisorySeriesAxisNotDate(t *testing.T) {
	day := saGroup(types.GROUP_DATE, "day")
	wh := saGroup(types.GROUP_CATEGORY, "warehouse")
	region := saGroup(types.GROUP_CATEGORY, "region")
	ts := saGroup(types.GROUP_DATE, "ts")
	share := types.OverlaySpec{Name: "st", Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup}
	rolling := types.OverlaySpec{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: json.RawMessage(`{"window": 2}`)}
	cases := []struct {
		name string
		req  *types.Request
		inst *InstanceSnapshot
		want []map[string]any
	}{
		{"[day, warehouse]", saRequest([]*types.Group{day, wh}, saPrior()), nil, []map[string]any{{
			"slot": "overlays[0]", "operator": "OVERLAY_DELTA_VS_PRIOR", "series_axis": "warehouse",
			"grouper": "GROUP_CATEGORY", "groups": []string{"day", "warehouse"}, "date_axis": "day",
		}}},
		{"[warehouse, day]", saRequest([]*types.Group{wh, day}, saPrior()), nil, nil},
		{"[region, warehouse] no date grouper, two ordered overlays and a total", saRequest([]*types.Group{region, wh}, saPrior(), share, rolling), nil,
			[]map[string]any{
				{"slot": "overlays[0]", "operator": "OVERLAY_DELTA_VS_PRIOR", "series_axis": "warehouse",
					"grouper": "GROUP_CATEGORY", "groups": []string{"region", "warehouse"}},
				{"slot": "overlays[2]", "operator": "OVERLAY_ZSCORE_VS_ROLLING", "series_axis": "warehouse",
					"grouper": "GROUP_CATEGORY", "groups": []string{"region", "warehouse"}},
			}},
		{"[region, ts] datetime axis", saRequest([]*types.Group{region, ts}, saPrior()), nil, nil},
		{"[region, GROUP_ROUNDED over a date column]", saRequest([]*types.Group{region, saGroup(types.GROUP_ROUNDED, "day")}, saPrior()), nil, nil},
		{"[region, warehouse, day]", saRequest([]*types.Group{region, wh, day}, saPrior()), nil, nil},
		{"one grouper", saRequest([]*types.Group{wh}, saPrior()), nil, nil},
		{"explicit partition_by", saRequest([]*types.Group{day, wh}, saWithPartition(saPrior(), []string{"day"})), nil, nil},
		{"partition_by []", saRequest([]*types.Group{day, wh}, saWithPartition(saPrior(), []string{})), nil, nil},
		{"partition_by null is implicit", saRequest([]*types.Group{day, wh}, saWithPartition(saPrior(), nil)), nil, []map[string]any{{
			"slot": "overlays[0]", "operator": "OVERLAY_DELTA_VS_PRIOR", "series_axis": "warehouse",
			"grouper": "GROUP_CATEGORY", "groups": []string{"day", "warehouse"}, "date_axis": "day",
		}}},
		{"_TOTAL kind", saRequest([]*types.Group{day, wh}, share), nil, nil},
		{"hidden kind", saRequest([]*types.Group{day, wh}, saPrior()), scopedExcept("OVERLAY_DELTA_VS_PRIOR"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := seriesAxisAdvisoriesFor(t, c.req, &PredictOptions{Instance: c.inst})
			if len(got) != len(c.want) {
				t.Fatalf("advisories = %+v, want %d", got, len(c.want))
			}
			for i, w := range c.want {
				if !reflect.DeepEqual(got[i].Details, w) {
					t.Errorf("advisory %d details = %#v, want %#v", i, got[i].Details, w)
				}
				for _, s := range []string{w["slot"].(string), w["operator"].(string), "warehouse"} {
					if !strings.Contains(got[i].Message, s) {
						t.Errorf("advisory %d message %q does not name %s", i, got[i].Message, s)
					}
				}
				if hits := LintGuidanceText(got[i].Message); len(hits) > 0 {
					t.Errorf("advisory %d lint hits %+v in %q", i, hits, got[i].Message)
				}
			}
		})
	}
	crosstab := saRequest(nil, saPrior())
	crosstab.Crosstab = &types.CrosstabSpec{Rows: []*types.Group{day}, Columns: []*types.Group{wh}}
	crosstab.Groups = []*types.Group{day, wh}
	if got := seriesAxisAdvisoriesFor(t, crosstab, nil); len(got) != 0 {
		t.Errorf("crosstab host: advisories = %+v, want none", got)
	}
}

// TestPredict_AdvisorySeriesAxisNeverEscalated: strict predict keeps the
// advisory in its slot (valid, no error, no warning) and suppressing the
// code drops it.
func TestPredict_AdvisorySeriesAxisNeverEscalated(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_SERIES_AXIS_NOT_DATE)
	req := saRequest([]*types.Group{saGroup(types.GROUP_DATE, "day"), saGroup(types.GROUP_CATEGORY, "warehouse")}, saPrior())
	env := predictFromBytes(seriesAxisCohort(t), req, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v, want a clean valid result", res.Valid, env.Errors, env.Warnings)
	}
	suppressed := &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})}
	if got := seriesAxisAdvisoriesFor(t, req, suppressed); len(got) != 0 {
		t.Errorf("suppressed: advisories = %+v, want none", got)
	}
}
