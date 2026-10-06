package descriptor

import (
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func returnCode(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestResolveReturn_Paths: the resolver accepts every path the payload
// schema publishes (deep keys, [*], map-key globs, open maps, the
// per-group aggregator components, untagged Go-named keys) and refuses a
// path the instance's Response does not carry with
// PULSE_RETURN_PATH_UNKNOWN — a hidden feature's path exactly like a
// nonexistent one — and bad syntax / preset / precision with
// PULSE_RETURN_INVALID.
func TestResolveReturn_Paths(t *testing.T) {
	hideMatrices := scopedOnly(featProcess)
	for _, c := range []struct {
		name string
		ret  types.Return
		inst *InstanceSnapshot
		want errors.Code
		at   string
	}{
		{name: "top-level", ret: types.Return{Include: []string{"data", "tests"}}},
		{name: "deep array", ret: types.Return{Include: []string{"tests[*].p_value", "matrices[*].primary.values[*][*]"}}},
		{name: "per-group components", ret: types.Return{Include: []string{"components.aggregations[*].groups[*].operator.mean", "components.aggregations[*].groups[*].n"}}},
		{name: "open map any key", ret: types.Return{Include: []string{"tests[*].details.anything_at_all.deeper", "components.groupers[*].operator.buckets"}}},
		{name: "map-key glob", ret: types.Return{Include: []string{"tests[*].details.effect_*", "data[*].*"}}},
		{name: "untagged go name", ret: types.Return{Include: []string{"overlays[*].warnings[*].Code"}}},
		{name: "exclude resolves too", ret: types.Return{Preset: types.ReturnPresetFull, Exclude: []string{"components.run"}}},
		{name: "presets valid", ret: types.Return{Preset: types.ReturnPresetMinimal}},
		{name: "precision bounds", ret: types.Return{Precision: 17}},

		{name: "unknown key", ret: types.Return{Include: []string{"tests[*].pvalue"}}, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "pvalue"},
		{name: "unknown top", ret: types.Return{Exclude: []string{"nope"}}, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "nope"},
		{name: "index on object", ret: types.Return{Include: []string{"metadata[*]"}}, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "[*]"},
		{name: "key on array", ret: types.Return{Include: []string{"tests.p_value"}}, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "p_value"},
		{name: "past a leaf", ret: types.Return{Include: []string{"metadata.total_rows.x"}}, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "x"},
		{name: "glob on struct key", ret: types.Return{Include: []string{"comp*"}}, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "comp*"},
		{name: "hidden matrices", ret: types.Return{Include: []string{"matrices"}}, inst: hideMatrices, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "matrices"},
		{name: "hidden nested component", ret: types.Return{Include: []string{"components.matrices"}}, inst: hideMatrices, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "matrices"},
		{name: "hidden p_adjusted", ret: types.Return{Exclude: []string{"tests[*].p_adjusted"}}, inst: hideMatrices, want: errors.PULSE_RETURN_PATH_UNKNOWN, at: "p_adjusted"},

		{name: "bad preset", ret: types.Return{Preset: "everything"}, want: errors.PULSE_RETURN_INVALID},
		{name: "precision high", ret: types.Return{Precision: 18}, want: errors.PULSE_RETURN_INVALID},
		{name: "precision negative", ret: types.Return{Precision: -1}, want: errors.PULSE_RETURN_INVALID},
		{name: "malformed", ret: types.Return{Include: []string{"data[0]"}}, want: errors.PULSE_RETURN_INVALID},
	} {
		t.Run(c.name, func(t *testing.T) {
			ret := c.ret
			plan, err := ResolveReturn(&types.Request{Return: &ret}, c.inst)
			if got := returnCode(err); got != c.want {
				t.Fatalf("code = %q (%v), want %q", got, err, c.want)
			}
			if c.want == "" && plan == nil {
				t.Fatal("no plan for a valid block")
			}
			if c.at != "" {
				var ce *errors.CodedError
				stderrors.As(err, &ce)
				if ce.Details["at"] != c.at {
					t.Errorf("details.at = %v, want %q", ce.Details["at"], c.at)
				}
			}
		})
	}
	// The same paths resolve on the unscoped instance.
	if _, err := ResolveReturn(&types.Request{Return: &types.Return{Include: []string{"matrices", "tests[*].p_adjusted"}}}, nil); err != nil {
		t.Errorf("visible paths refused on the unscoped instance: %v", err)
	}
	if plan, err := ResolveReturn(&types.Request{}, nil); plan != nil || err != nil {
		t.Errorf("no block must resolve to no plan, got %v, %v", plan, err)
	}
}

// TestResolveReturn_Resolution: preset → include → exclude, exclude
// wins; include-only is an empty-base allowlist; exclude / precision
// alone shape the full response; warnings are kept unless excluded.
func TestResolveReturn_Resolution(t *testing.T) {
	resolve := func(ret types.Return) *descriptor.ReturnPlan {
		t.Helper()
		plan, err := ResolveReturn(&types.Request{Return: &ret}, nil)
		if err != nil {
			t.Fatalf("ResolveReturn(%+v): %v", ret, err)
		}
		return returnPlanDescriptor(plan)
	}
	allow := resolve(types.Return{Include: []string{"data[*].region", "tests[*].p_value"}})
	if allow.Preset != "custom" || allow.Identity {
		t.Errorf("allowlist = %+v", allow)
	}
	if !slices.Equal(allow.Include, []string{"data[*].region", "tests[*].p_value", "warnings"}) {
		t.Errorf("allowlist include = %v (empty base + paths + top-level warnings)", allow.Include)
	}
	if !slices.Contains(allow.Keep, "tests[*].warnings") {
		t.Errorf("nested warnings not retained: keep = %v", allow.Keep)
	}

	full := resolve(types.Return{Preset: types.ReturnPresetFull})
	if !full.Identity || full.Preset != "full" || len(full.Keep) != 0 || len(full.Exclude) != 0 {
		t.Errorf("full = %+v", full)
	}
	if !slices.Contains(full.Include, "components") || !slices.Contains(full.Include, "matrices") {
		t.Errorf("full must expand to every visible top-level key: %v", full.Include)
	}
	scoped, _ := ResolveReturn(&types.Request{Return: &types.Return{Preset: types.ReturnPresetFull}}, scopedOnly(featProcess))
	if slices.Contains(returnPlanDescriptor(scoped).Include, "matrices") || !scoped.Identity() {
		t.Errorf("full under a profile hiding matrices = %v", returnPlanDescriptor(scoped).Include)
	}

	carve := resolve(types.Return{Preset: types.ReturnPresetFull, Include: []string{"components"}, Exclude: []string{"components", "tests[*].details"}})
	if slices.Contains(carve.Include, "components") || carve.Identity || carve.Preset != "full" {
		t.Errorf("exclude must win over an include: %+v", carve)
	}
	if !slices.Equal(carve.Exclude, []string{"tests[*].details"}) {
		t.Errorf("exclude = %v (components dropped from include, so only the carve remains)", carve.Exclude)
	}

	excludeOnly := resolve(types.Return{Exclude: []string{"components"}})
	if excludeOnly.Preset != "custom" || !slices.Contains(excludeOnly.Include, "data") {
		t.Errorf("exclude-only must shape the full response: %+v", excludeOnly)
	}
	precisionOnly := resolve(types.Return{Precision: 4})
	if precisionOnly.Preset != "full" || precisionOnly.Identity || precisionOnly.Precision != 4 {
		t.Errorf("precision-only = %+v", precisionOnly)
	}

	noWarnings := resolve(types.Return{Include: []string{"data"}, Exclude: []string{"warnings"}})
	// The exclude removed the retained top-level slot, after which it
	// touches nothing selected and drops from the canonical plan.
	if slices.Contains(noWarnings.Include, "warnings") || len(noWarnings.Exclude) != 0 {
		t.Errorf("explicitly excluded warnings = %+v", noWarnings)
	}
}

// TestResolveReturn_DigestStable: equivalent spellings — order,
// duplicates, covered paths, an explicit top-level list versus the full
// preset, implicit versus explicit top-level warnings — share a digest;
// a different selection does not.
func TestResolveReturn_DigestStable(t *testing.T) {
	digest := func(ret types.Return) string {
		t.Helper()
		plan, err := ResolveReturn(&types.Request{Return: &ret}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return plan.Digest
	}
	a := digest(types.Return{Include: []string{"tests[*].p_value", "data[*].region"}})
	for _, eq := range []types.Return{
		{Include: []string{"data[*].region", "tests[*].p_value", "data[*].region"}},
		{Include: []string{"data[*].region", "tests[*].p_value", "warnings"}},
		{Include: []string{"data[*].region", "tests[*].p_value"}, Exclude: []string{"metadata"}},
	} {
		if got := digest(eq); got != a {
			t.Errorf("%+v digest %s, want %s", eq, got, a)
		}
	}
	full := digest(types.Return{Preset: types.ReturnPresetFull})
	if got := digest(types.Return{Include: returnVisibleKeys(returnRoot, nil)}); got != full {
		t.Errorf("explicit top-level list digest %s, want full's %s", got, full)
	}
	if got := digest(types.Return{}); got != full {
		t.Errorf("empty block digest %s, want full's %s", got, full)
	}
	if digest(types.Return{Include: []string{"data"}}) == a {
		t.Error("different selections share a digest")
	}
}

func returnColumnsSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "N", "S")},
		{Name: "revenue", Type: encoding.FieldTypeF64},
	}}
}

// TestReturnColumnRefusal: a known data column is judged against the
// columns the request produces; globs, joins and crosstabs are open.
func TestReturnColumnRefusal(t *testing.T) {
	schema := returnColumnsSchema(t)
	grouped := func(paths ...string) *types.Request {
		return &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "revenue"}, {Type: types.AGG_AVERAGE, Field: "revenue", Label: "avg"}},
			Labels:       []*types.LabelBinding{{Field: "region", Table: "regions"}},
			Return:       &types.Return{Include: paths},
		}
	}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"group field", grouped("data[*].region"), ""},
		{"default agg label", grouped("data[*].AGG_SUM_revenue"), ""},
		{"explicit agg label", grouped("data[*].avg"), ""},
		{"label sibling", grouped("data[*].region_label"), ""},
		{"glob", grouped("data[*].AGG_*"), ""},
		{"whole rows", grouped("data", "data[*]"), ""},
		{"source field not output when grouped", grouped("data[*].revenue"), errors.PULSE_RETURN_PATH_UNKNOWN},
		{"typo", grouped("data[*].AGG_SUM_revenu"), errors.PULSE_RETURN_PATH_UNKNOWN},
		{"exclude judged too", &types.Request{Return: &types.Return{Exclude: []string{"data[*].nope"}}}, errors.PULSE_RETURN_PATH_UNKNOWN},
		{"ungrouped schema field", &types.Request{Return: &types.Return{Include: []string{"data[*].revenue"}}}, ""},
		{"attribute label", &types.Request{
			Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "revenue"}},
			Return:     &types.Return{Include: []string{"data[*].ATTR_ZSCORE_revenue"}},
		}, ""},
		{"window label", &types.Request{
			Windows: []*types.Window{{Type: types.WIN_LAG, Field: "revenue", Label: "prev"}},
			Return:  &types.Return{Include: []string{"data[*].prev"}},
		}, ""},
		{"join is open", &types.Request{
			Joins:  []*types.JoinSpec{{}},
			Return: &types.Return{Include: []string{"data[*].anything"}},
		}, ""},
		{"crosstab is open", &types.Request{
			Crosstab: &types.CrosstabSpec{},
			Return:   &types.Return{Include: []string{"data[*].anything"}},
		}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := returnCode(ReturnColumnRefusal(c.req, schema, nil)); got != c.want {
				t.Errorf("code = %q, want %q", got, c.want)
			}
		})
	}
	err := ReturnColumnRefusal(grouped("data[*].AGG_SUM_revenu"), schema, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["suggestion"] != "AGG_SUM_revenue" {
		t.Errorf("suggestion = %v, want AGG_SUM_revenue", ce)
	}
}

// TestPredict_ReturnPlan: predict reports the resolved plan; a refused
// block invalidates the request and reports no plan; no block, no key.
func TestPredict_ReturnPlan(t *testing.T) {
	data := buildTestPulseFile(t, returnColumnsSchema(t))
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Field: "revenue"}}, // defaulted to AGG_SUM
		Return:       &types.Return{Include: []string{"data[*].AGG_SUM_revenue", "data[*].region"}, Precision: 3},
	}
	env := predictFromBytes(data, req, nil)
	res := env.Data.(*descriptor.PredictResult)
	if !res.Valid || len(env.Errors) != 0 {
		t.Fatalf("valid = %v, errors = %v", res.Valid, env.Errors)
	}
	if res.Return == nil || res.Return.Precision != 3 || res.Return.Preset != "custom" ||
		!slices.Equal(res.Return.Include, []string{"data[*].AGG_SUM_revenue", "data[*].region", "warnings"}) {
		t.Errorf("return plan = %+v", res.Return)
	}

	for _, bad := range []*types.Return{
		{Include: []string{"data[*].revenue"}},
		{Include: []string{"tests[*].pvalue"}},
		{Preset: "nope"},
	} {
		req.Return = bad
		env := predictFromBytes(data, req, nil)
		res := env.Data.(*descriptor.PredictResult)
		if res.Valid || res.Return != nil || len(env.Errors) == 0 {
			t.Errorf("%+v: valid = %v, return = %+v, errors = %v", bad, res.Valid, res.Return, env.Errors)
		}
	}

	req.Return = nil
	if res := predictFromBytes(data, req, nil).Data.(*descriptor.PredictResult); res.Return != nil {
		t.Errorf("no block must report no plan, got %+v", res.Return)
	}
}
