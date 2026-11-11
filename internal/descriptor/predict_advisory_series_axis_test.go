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
	t.Parallel()

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
	t.Parallel()

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

// saCompose is a Compose request over the series-axis cohort: slots a
// and b grouped by groups (b the host, index 1) and the given
// Compose-host overlays.
func saCompose(groups []*types.Group, specs ...types.ComposeOverlaySpec) *types.ComposedRequest {
	slot := func(label string) *types.Request {
		r := saRequest(groups)
		r.Label = label
		r.Cohort = &types.Cohort{Filename: "sa.pulse"}
		return r
	}
	return &types.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b")}, Overlays: specs}
}

func saComposeFormula(params map[string]any) types.ComposeOverlaySpec {
	if params == nil {
		params = map[string]any{}
	}
	params["formula"] = "prior"
	return types.ComposeOverlaySpec{Name: "prior", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
		Reference: "a", Targets: []string{"b"}, Params: params}
}

func saComposeResult(t *testing.T, req *types.ComposedRequest, opts *PredictOptions) (*descriptor.Envelope, *ComposeValidationResult) {
	t.Helper()
	if opts == nil {
		opts = &PredictOptions{}
	}
	opts.SchemaLoader = composeSchemaLoader(t, map[string][]byte{"sa.pulse": seriesAxisCohort(t)})
	env := ValidateComposeWithOptions(req, opts)
	return env, env.Data.(*ComposeValidationResult)
}

// TestComposePredict_AdvisorySeriesAxisNotDate (U66 E8-S2): a Compose
// OVERLAY_FORMULA — the ordered kind that runs per partition of its host
// slot, the first target — draws the Process advisory, same rule and
// message shape, when that slot meets the Process condition. details
// add request (the host slot's index) and target (its label). An
// explicit partition_by, a date last axis, a hidden kind and a kind the
// Compose host does not partition never fire, and a quiet request
// carries no advisory at all.
func TestComposePredict_AdvisorySeriesAxisNotDate(t *testing.T) {
	t.Parallel()

	day := saGroup(types.GROUP_DATE, "day")
	wh := saGroup(types.GROUP_CATEGORY, "warehouse")
	region := saGroup(types.GROUP_CATEGORY, "region")
	fired := map[string]any{
		"slot": "overlays[0]", "operator": "OVERLAY_FORMULA", "series_axis": "warehouse",
		"grouper": "GROUP_CATEGORY", "groups": []string{"day", "warehouse"}, "date_axis": "day",
		"request": 1, "target": "b",
	}
	notPartitioned := types.ComposeOverlaySpec{Name: "dr", Kind: types.OverlayKindDeltaVsRef, Scope: types.OverlayScopeGroup,
		Reference: "a", Targets: []string{"b"}}
	cases := []struct {
		name string
		req  *types.ComposedRequest
		inst *InstanceSnapshot
		want []map[string]any
	}{
		{"[day, warehouse]", saCompose([]*types.Group{day, wh}, saComposeFormula(nil)), nil, []map[string]any{fired}},
		{"partition_by null is implicit", saCompose([]*types.Group{day, wh}, saComposeFormula(map[string]any{"partition_by": nil})), nil, []map[string]any{fired}},
		{"[region, warehouse] no date grouper", saCompose([]*types.Group{region, wh}, notPartitioned, saComposeFormula(nil)), nil, []map[string]any{{
			"slot": "overlays[1]", "operator": "OVERLAY_FORMULA", "series_axis": "warehouse",
			"grouper": "GROUP_CATEGORY", "groups": []string{"region", "warehouse"}, "request": 1, "target": "b",
		}}},
		{"[warehouse, day]", saCompose([]*types.Group{wh, day}, saComposeFormula(nil)), nil, nil},
		{"one grouper", saCompose([]*types.Group{wh}, saComposeFormula(nil)), nil, nil},
		{"explicit partition_by", saCompose([]*types.Group{day, wh}, saComposeFormula(map[string]any{"partition_by": []string{"day"}})), nil, nil},
		{"partition_by []", saCompose([]*types.Group{day, wh}, saComposeFormula(map[string]any{"partition_by": []string{}})), nil, nil},
		{"a kind the Compose host does not partition", saCompose([]*types.Group{day, wh}, notPartitioned), nil, nil},
		{"an ordered Process kind on the Compose host", saCompose([]*types.Group{day, wh}, types.ComposeOverlaySpec{Name: "dp",
			Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}}), nil, nil},
		{"hidden kind", saCompose([]*types.Group{day, wh}, saComposeFormula(nil)), scopedExcept("OVERLAY_FORMULA"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, res := saComposeResult(t, c.req, &PredictOptions{Instance: c.inst})
			if len(c.want) == 0 {
				if len(res.Advisories) != 0 {
					t.Fatalf("advisories = %+v, want none", res.Advisories)
				}
				return
			}
			if len(res.Advisories) != len(c.want) {
				t.Fatalf("advisories = %+v, want %d", res.Advisories, len(c.want))
			}
			for i, w := range c.want {
				a := res.Advisories[i]
				if a.Code != string(errors.PULSE_ADVISORY_SERIES_AXIS_NOT_DATE) {
					t.Fatalf("advisory %d code %s", i, a.Code)
				}
				if !reflect.DeepEqual(a.Details, w) {
					t.Errorf("advisory %d details = %#v, want %#v", i, a.Details, w)
				}
				for _, s := range []string{w["slot"].(string), "OVERLAY_FORMULA", "host slot b", "warehouse", "params.partition_by"} {
					if !strings.Contains(a.Message, s) {
						t.Errorf("advisory %d message %q does not name %s", i, a.Message, s)
					}
				}
				if hits := LintGuidanceText(a.Message); len(hits) > 0 {
					t.Errorf("advisory %d lint hits %+v in %q", i, hits, a.Message)
				}
			}
		})
	}
}

// TestComposePredict_AdvisorySeriesAxisSharesTheProcessRule: the
// Compose advisory is the Process one over the host slot — the same
// message once the host is named, the same details once request and
// target are dropped.
func TestComposePredict_AdvisorySeriesAxisSharesTheProcessRule(t *testing.T) {
	t.Parallel()

	groups := []*types.Group{saGroup(types.GROUP_DATE, "day"), saGroup(types.GROUP_CATEGORY, "warehouse")}
	process := seriesAxisAdvisoriesFor(t, saRequest(groups, types.OverlaySpec{Name: "f", Kind: types.OverlayKindFormula,
		Scope: types.OverlayScopeGroup, Params: json.RawMessage(`{"formula": "prior"}`)}), nil)
	_, res := saComposeResult(t, saCompose(groups, saComposeFormula(nil)), nil)
	if len(process) != 1 || len(res.Advisories) != 1 {
		t.Fatalf("vacuous: process %+v, compose %+v", process, res.Advisories)
	}
	c := res.Advisories[0]
	if want := strings.Replace(process[0].Message, "overlays[0]", "overlays[0] (host slot b)", 1); c.Message != want {
		t.Errorf("compose message %q, want %q", c.Message, want)
	}
	details := map[string]any{}
	for k, v := range c.Details {
		if k != "request" && k != "target" {
			details[k] = v
		}
	}
	if !reflect.DeepEqual(details, process[0].Details) {
		t.Errorf("compose details %#v, want the process details %#v", details, process[0].Details)
	}
}

// TestComposePredict_AdvisorySeriesAxisNeverEscalated: strict Compose
// predict keeps the advisory in its slot — valid, no error, no warning —
// and suppressing the code drops it.
func TestComposePredict_AdvisorySeriesAxisNeverEscalated(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_SERIES_AXIS_NOT_DATE)
	req := saCompose([]*types.Group{saGroup(types.GROUP_DATE, "day"), saGroup(types.GROUP_CATEGORY, "warehouse")}, saComposeFormula(nil))
	env, res := saComposeResult(t, req, &PredictOptions{Strict: true})
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v, want a clean valid result", res.Valid, env.Errors, env.Warnings)
	}
	suppressed := &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})}
	if _, res := saComposeResult(t, req, suppressed); len(res.Advisories) != 0 {
		t.Errorf("suppressed: advisories = %+v, want none", res.Advisories)
	}
}
