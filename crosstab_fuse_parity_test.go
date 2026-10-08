package pulse_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension names the fusion parity corpus registers on top of the
// shared parity suites: copies of a fusable registration with the one
// fact that changes the answer stripped.
const (
	aggFuseOpaque   types.AggregationType = "AGG_XT_OPAQUE"         // AGG_XT_SUM without FieldInputs
	grpFuseNoStream types.GroupType       = "GROUP_PARITY_NOSTREAM" // keyable value, Streamable=false
	grpFuseOpaque   types.GroupType       = "GROUP_PARITY_OPAQUE"   // GROUP_PARITY_CATEGORY without FieldInputs
	fltFuseOpaque   types.FiltererType    = "FILTER_PARITY_OPAQUE"  // FILTER_PARITY_INCLUDE without FieldInputs
	attrFuseOpaque  types.AttributeType   = "ATTR_PARITY_OPAQUE"    // ATTR_PARITY_SET_POPCOUNT without FieldInputs
	fuseRangeTable                        = "fuse_fiscal"
)

// fusionParityExtensions registers extension operators of every
// category the fusion rule reads, with and without FieldInputs, plus a
// named range table for GROUP_DATE_RANGES `table:`.
func fusionParityExtensions() pulse.Extensions {
	probe := &parityProbe{}
	ext := crosstabCellExtensions(probe)
	grp := grouperParitySuite().register(probe)
	flt := filtererParitySuite().register(probe)
	attr := attributeParitySuite().register(probe)

	opaqueAgg := ext.Aggregators[0]
	opaqueAgg.Name, opaqueAgg.FieldInputs = aggFuseOpaque, nil
	ext.Aggregators = append(ext.Aggregators, opaqueAgg)

	ext.Groupers = grp.Groupers
	noStream := grp.Groupers[0]
	noStream.Name, noStream.Streamable, noStream.Mergeable = grpFuseNoStream, false, false
	noStream.ComponentSchema, noStream.ComponentsFunc = grp.Groupers[0].ComponentSchema, grp.Groupers[0].ComponentsFunc
	opaqueGrp := grp.Groupers[0]
	opaqueGrp.Name, opaqueGrp.FieldInputs = grpFuseOpaque, nil
	ext.Groupers = append(ext.Groupers, noStream, opaqueGrp)

	ext.Filterers = flt.Filterers
	opaqueFlt := flt.Filterers[0]
	opaqueFlt.Name, opaqueFlt.FieldInputs = fltFuseOpaque, nil
	ext.Filterers = append(ext.Filterers, opaqueFlt)

	ext.Attributes = attr.Attributes
	opaqueAttr := attr.Attributes[1]
	opaqueAttr.Name, opaqueAttr.FieldInputs = attrFuseOpaque, nil
	ext.Attributes = append(ext.Attributes, opaqueAttr)

	start := "2024-01-01"
	ext.RangeTables = map[string]pulse.RangeTable{fuseRangeTable: {Ranges: []pulse.DateRangeSpec{{Label: "fy", Start: &start}}}}
	return ext
}

type fusionCase struct {
	name string
	req  *types.Request
	// constructFails: a keyable grouper whose factory refuses its params.
	// Only the runtime arm can know that; it declines, the buffered path
	// then refuses the request with the factory's coded error, and the
	// no-execute arm (which constructs nothing) answers on the static
	// table. The one sanctioned divergence.
	constructFails bool
}

func fusionXtab(rows, cols []*types.Group, cell *types.Aggregation) *types.Request {
	return &types.Request{Crosstab: &types.CrosstabSpec{Rows: rows, Columns: cols, Cell: cell}}
}

func fusionParityCorpus() []fusionCase {
	region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	sum := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_SUM, Field: "qty"} }
	with := func(mut func(*types.Request)) *types.Request {
		r := fusionXtab(region, region, sum())
		mut(r)
		return r
	}
	var out []fusionCase
	add := func(name string, r *types.Request) { out = append(out, fusionCase{name: name, req: r}) }

	// Every built-in aggregator as the cell (numeric and decimal field)
	// and as an auxiliary margin aggregation.
	for _, at := range types.AllAggregationTypes() {
		add("cell/"+string(at), fusionXtab(region, region, &types.Aggregation{Type: at, Field: "score"}))
		add("cell_decimal/"+string(at), fusionXtab(region, region, &types.Aggregation{Type: at, Field: "amount"}))
		add("aux/"+string(at), with(func(r *types.Request) {
			r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: at, Field: "score", Label: "aux"}}
		}))
		add("aux_decimal/"+string(at), with(func(r *types.Request) {
			r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: at, Field: "amount", Label: "aux"}}
		}))
	}
	add("aux_malformed", with(func(r *types.Request) {
		r.Crosstab.MarginAggregations = []*types.Aggregation{nil, {Field: "score"}}
	}))

	// Every built-in grouper type on each axis.
	axisFix := map[types.GroupType]*types.Group{
		types.GROUP_CATEGORY:        {Type: types.GROUP_CATEGORY, Field: "region"},
		types.GROUP_DATE:            {Type: types.GROUP_DATE, Field: "day"},
		types.GROUP_DATE_RANGES:     {Type: types.GROUP_DATE_RANGES, Field: "day", Params: json.RawMessage(`{"ranges":[{"label":"h1","start":"2024-01-01","end":"2024-06-30"}]}`)},
		types.GROUP_QUANTILE:        {Type: types.GROUP_QUANTILE, Field: "score"},
		types.GROUP_RANGE:           {Type: types.GROUP_RANGE, Field: "qty", Interval: 5},
		types.GROUP_ROUNDED:         {Type: types.GROUP_ROUNDED, Field: "score"},
		types.GROUP_SET_VALUE:       {Type: types.GROUP_SET_VALUE, Field: "tags"},
		types.GROUP_SET_PER_ELEMENT: {Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"},
	}
	for _, gt := range types.AllGroupTypes() {
		g, ok := axisFix[gt]
		if !ok {
			// A new built-in grouper needs a fixture here.
			g = &types.Group{Type: gt, Field: "region"}
		}
		add("rows/"+string(gt), fusionXtab([]*types.Group{g}, region, sum()))
		add("cols/"+string(gt), fusionXtab(region, []*types.Group{g}, sum()))
	}
	add("date_on_datetime", fusionXtab([]*types.Group{{Type: types.GROUP_DATE, Field: "ts"}}, region, sum()))
	add("date_ranges_table", fusionXtab([]*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "day",
		Params: json.RawMessage(`{"table":"` + fuseRangeTable + `"}`)}}, region, sum()))
	add("date_ranges_unknown_table", fusionXtab([]*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "day",
		Params: json.RawMessage(`{"table":"no_such_table"}`)}}, region, sum()))
	add("unknown_grouper", fusionXtab([]*types.Group{{Type: "GROUP_NO_SUCH", Field: "region"}}, region, sum()))
	add("nil_grouper", fusionXtab([]*types.Group{nil}, region, sum()))
	out = append(out, fusionCase{name: "grouper_bad_params", constructFails: true,
		req: fusionXtab([]*types.Group{{Type: types.GROUP_DATE, Field: "day", Params: json.RawMessage(`{"component":"fortnight"}`)}}, region, sum())})

	// Request-level exclusions and non-exclusions.
	add("no_crosstab", &types.Request{Aggregations: []*types.Aggregation{sum()}})
	add("missing_cell", fusionXtab(region, region, nil))
	add("joins", with(func(r *types.Request) { r.Joins = []*types.JoinSpec{{}} }))
	add("features", with(func(r *types.Request) { r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "score"}} }))
	add("tests", with(func(r *types.Request) { r.Tests = []*types.Test{{Type: types.TEST_T}} }))
	add("post_tests", with(func(r *types.Request) { r.PostTests = []*types.Test{{Type: types.TEST_T}} }))
	add("overlays", with(func(r *types.Request) { r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ}} }))
	for _, at := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED,
		types.ATTR_REG_FITTED, types.ATTR_DATE_PART, types.ATTR_SET_POPCOUNT} {
		add("attr/"+string(at), with(func(r *types.Request) { r.Attributes = []*types.Attribute{{Type: at, Field: "score", Label: "a"}} }))
	}
	add("attr/ATTR_CODE_IN", with(func(r *types.Request) {
		r.Attributes = []*types.Attribute{{Type: types.ATTR_CODE_IN, Field: "region", Label: "a", Params: json.RawMessage(`{"codes":["north","west"]}`)}}
	}))
	add("attr/ATTR_CODE_IN_u32", with(func(r *types.Request) {
		r.Attributes = []*types.Attribute{{Type: types.ATTR_CODE_IN, Field: "qty", Label: "a", Params: json.RawMessage(`{"codes":[1, 2]}`)}}
	}))
	add("formula_expression", with(func(r *types.Request) {
		r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "score * 2", Label: "f"}}
	}))
	add("formula_empty", with(func(r *types.Request) { r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f"}} }))
	add("filter_expression", with(func(r *types.Request) {
		r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score > 1"}}
	}))
	add("filter_include", with(func(r *types.Request) {
		r.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}}
	}))

	// Weighted cells and auxiliaries (weighting E3-S1, every built-in
	// aggregator since E3-S2): a weight never moves the answer — a
	// weighted median / percentile stays buffered (non-mergeable, exactly
	// as unweighted), a weighted mergeable cell stays fusable, a weight
	// the resolver refuses or skips decides nothing, and a `weight: null`
	// base changes nothing.
	weighted := func(r *types.Request) *types.Request {
		r.Weight = &types.WeightSpec{Field: "qty"}
		return r
	}
	for _, at := range types.AllAggregationTypes() {
		add("weighted_cell/"+string(at), weighted(fusionXtab(region, region, &types.Aggregation{Type: at, Field: "score"})))
		add("weighted_aux/"+string(at), weighted(with(func(r *types.Request) {
			r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: at, Field: "score", Label: "aux"}}
		})))
	}
	add("weighted_null_base", weighted(with(func(r *types.Request) {
		r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "base", Weight: types.NullSlotWeight()}}
	})))

	// Extension operators, with and without FieldInputs.
	for _, at := range []types.AggregationType{aggXtSum, aggXtMean, aggXtUndecl, aggXtRecomp, aggFuseOpaque} {
		add("ext_cell/"+string(at), fusionXtab(region, region, &types.Aggregation{Type: at, Field: "qty"}))
		add("ext_cell_decimal/"+string(at), fusionXtab(region, region, &types.Aggregation{Type: at, Field: "amount"}))
		add("ext_aux/"+string(at), with(func(r *types.Request) {
			r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: at, Field: "amount", Label: "aux"}}
		}))
		add("ext_plain_agg/"+string(at), with(func(r *types.Request) { r.Aggregations = []*types.Aggregation{{Type: at, Field: "qty"}} }))
	}
	for _, gt := range []types.GroupType{grpParityCategory, grpParityPerElement, grpFuseNoStream, grpFuseOpaque} {
		field := "region"
		if gt == grpParityPerElement {
			field = "tags"
		}
		add("ext_axis/"+string(gt), fusionXtab([]*types.Group{{Type: gt, Field: field}}, region, sum()))
		add("ext_groups_slot/"+string(gt), with(func(r *types.Request) { r.Groups = []*types.Group{{Type: gt, Field: field}} }))
	}
	for _, ft := range []types.FiltererType{fltParityInclude, fltFuseOpaque} {
		add("ext_filter/"+string(ft), with(func(r *types.Request) {
			r.Filterers = []*types.Filterer{{Type: ft, Field: "region", Values: []string{"north"}}}
		}))
	}
	for _, at := range []types.AttributeType{attrParityZScore, attrParityPopcount, attrFuseOpaque} {
		add("ext_attr/"+string(at), with(func(r *types.Request) { r.Attributes = []*types.Attribute{{Type: at, Field: "tags", Label: "a"}} }))
	}
	return out
}

// TestStreamability_CrosstabFusionPredictMatchesRuntime is the parity
// gate for the shared fusion rule (internal/crosstabfuse): the engine's
// processing.CanFuseCrosstab over the instance ExtensionRegistry and the
// no-execute descriptor.CrosstabFusion over the instance ExtensionsSnapshot
// must give the same answer and the same reasons for every request in a
// corpus spanning every built-in cell / auxiliary aggregator, decimal
// fields, every grouper type (QUANTILE, DATE, SET_PER_ELEMENT,
// DATE_RANGES with `table:`), joins, features, tests, two-pass
// attributes, formula / expression bails, extension operators with and
// without FieldInputs — on an unprofiled instance and on one whose
// feature profile hides most of the corpus.
func TestStreamability_CrosstabFusionPredictMatchesRuntime(t *testing.T) {
	schema := paritySchema(t)
	instances := []struct {
		name    string
		profile *pulse.FeatureProfile
	}{
		{"unprofiled", nil},
		{"profiled", &pulse.FeatureProfile{Features: []string{
			"capability:process", "AGG_SUM", "AGG_COUNT", "GROUP_CATEGORY", "GROUP_SET_PER_ELEMENT",
			string(aggXtSum), string(grpParityCategory), string(attrParityPopcount),
		}}},
	}
	corpus := fusionParityCorpus()
	for _, inst := range instances {
		t.Run(inst.name, func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: fusionParityExtensions(), FeatureProfile: inst.profile})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			svc := pulse.ServiceForTest(p)
			reg, snap := svc.Extensions(), svc.InstanceSnapshot()
			fused, sanctioned := 0, 0
			for _, c := range corpus {
				rtOK, rtReason := processing.CanFuseCrosstab(c.req, schema, reg)
				rtReasons := processing.CrosstabFuseReasons(c.req, schema, reg)
				pdOK, pdReasons := descx.CrosstabFusion(c.req, schema, snap.Extensions(), snap)
				if rtOK {
					fused++
				}
				if rtOK != (len(rtReasons) == 0) || (!rtOK && rtReason != rtReasons[0]) {
					t.Errorf("%s: CanFuseCrosstab=(%v,%q) disagrees with CrosstabFuseReasons=%q", c.name, rtOK, rtReason, rtReasons)
				}
				if c.constructFails && pdOK {
					// The sanctioned divergence; on an instance hiding the
					// grouper both arms decline alike and fall through.
					if rtOK {
						t.Errorf("%s: runtime fused a grouper whose factory refuses its params", c.name)
					}
					sanctioned++
					continue
				}
				if rtOK != pdOK || !reflect.DeepEqual(rtReasons, pdReasons) {
					t.Errorf("%s: runtime=(%v, %q) predict=(%v, %q)", c.name, rtOK, rtReasons, pdOK, pdReasons)
				}
			}
			if inst.profile == nil && sanctioned != 1 {
				t.Errorf("sanctioned construction divergence hit %d times on the unprofiled instance, want 1", sanctioned)
			}
			// A vacuous corpus (everything declined) would agree trivially.
			if fused < 5 || fused == len(corpus) {
				t.Errorf("fused %d of %d corpus requests; the corpus must exercise both answers", fused, len(corpus))
			}
		})
	}
}

// TestStreamability_CrosstabFusionIgnoresDisableOption pins that the
// no-execute answer is eligibility only: Options.DisableCrosstabFusion is
// a dispatch-time override and changes neither arm's rule.
func TestStreamability_CrosstabFusionIgnoresDisableOption(t *testing.T) {
	schema := paritySchema(t)
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	svc := pulse.ServiceForTest(p)
	req := fusionXtab([]*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		[]*types.Group{{Type: types.GROUP_DATE, Field: "day"}}, &types.Aggregation{Type: types.AGG_SUM, Field: "qty"})
	snap := svc.InstanceSnapshot()
	if ok, why := descx.CrosstabFusion(req, schema, snap.Extensions(), snap); !ok {
		t.Errorf("CrosstabFusion with DisableCrosstabFusion = false (%q), want true", why)
	}
	if ok, why := processing.CanFuseCrosstab(req, schema, svc.Extensions()); !ok {
		t.Errorf("CanFuseCrosstab with DisableCrosstabFusion = false (%q), want true", why)
	}
}

// TestPredict_CrosstabFusableMatchesRuntime drives the fusion parity
// corpus end to end through the facade — p.Predict over a cohort on
// disk and p.PredictBytes over its bytes — and requires
// PredictResult.CrosstabFusable / CrosstabFusionReasons to equal the
// decision service.processCrosstab makes: Options.DisableCrosstabFusion
// first, then processing.CanFuseCrosstab on the defaults-resolved
// request over the cohort schema. Nil exactly when the request has no
// crosstab or the instance refuses one of its slots before dispatch.
func TestPredict_CrosstabFusableMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	schema := paritySchema(t)
	profiled := &pulse.FeatureProfile{Features: []string{
		"capability:process", "capability:crosstab", "AGG_SUM", "AGG_COUNT", "GROUP_CATEGORY", "GROUP_SET_PER_ELEMENT",
		string(aggXtSum), string(grpParityCategory), string(attrParityPopcount),
	}}
	for _, inst := range []struct {
		name    string
		profile *pulse.FeatureProfile
		disable bool
	}{
		{"unprofiled", nil, false},
		{"unprofiled_disabled", nil, true},
		{"profiled", profiled, false},
		{"profiled_disabled", profiled, true},
	} {
		t.Run(inst.name, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			writeParityCohort(t, fsys, "parity.pulse", schema, 0, 0)
			data, err := afero.ReadFile(fsys, "parity.pulse")
			if err != nil {
				t.Fatal(err)
			}
			p, err := pulse.New(pulse.Options{FS: fsys, Extensions: fusionParityExtensions(),
				FeatureProfile: inst.profile, DisableCrosstabFusion: inst.disable})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			svc := pulse.ServiceForTest(p)
			reg, snap := svc.Extensions(), svc.InstanceSnapshot()
			compared, fused := 0, 0
			for _, c := range fusionParityCorpus() {
				req := *c.req
				req.Cohort = &types.Cohort{Filename: "parity.pulse"}

				// The runtime decision, as service.processCrosstab makes it.
				var rtOK bool
				var rtReasons []string
				refused := descx.SlotRefusal(&req, snap) != nil
				if req.Crosstab != nil && !refused {
					resolved := cloneFusionRequest(t, &req)
					descx.ResolveDefaults(resolved, schema, snap)
					if svc.CrosstabFusionDisabled() {
						rtReasons = []string{descx.CrosstabFusionDisabledReason}
					} else {
						rtOK, _ = processing.CanFuseCrosstab(resolved, schema, reg)
						rtReasons = processing.CrosstabFuseReasons(resolved, schema, reg)
					}
				}

				res, err := p.Predict(ctx, &req)
				if err != nil {
					t.Fatalf("%s: Predict: %v", c.name, err)
				}
				env, err := p.PredictBytes(ctx, data, &req)
				if err != nil {
					t.Fatalf("%s: PredictBytes: %v", c.name, err)
				}
				byBytes := env.Data.(*descriptor.PredictResult)
				for arm, got := range map[string]*descriptor.PredictResult{"Predict": res, "PredictBytes": byBytes} {
					if req.Crosstab == nil || refused {
						if got.CrosstabFusable != nil || got.CrosstabFusionReasons != nil {
							t.Errorf("%s/%s: CrosstabFusable=%v reasons=%q, want nil (no crosstab dispatch)",
								c.name, arm, got.CrosstabFusable, got.CrosstabFusionReasons)
						}
						continue
					}
					if got.CrosstabFusable == nil {
						t.Errorf("%s/%s: CrosstabFusable nil on a crosstab request", c.name, arm)
						continue
					}
					if c.constructFails && *got.CrosstabFusable && !rtOK {
						continue // the sanctioned construction divergence (see fusionCase)
					}
					if *got.CrosstabFusable != rtOK || !reflect.DeepEqual(got.CrosstabFusionReasons, rtReasons) {
						t.Errorf("%s/%s: predict=(%v, %q) runtime=(%v, %q)",
							c.name, arm, *got.CrosstabFusable, got.CrosstabFusionReasons, rtOK, rtReasons)
					}
				}
				if req.Crosstab != nil && !refused {
					compared++
					if rtOK {
						fused++
					}
				}
			}
			t.Logf("compared %d crosstab requests, %d fused", compared, fused)
			if compared < 50 {
				t.Errorf("compared %d crosstab requests, want most of the corpus", compared)
			}
			if inst.disable && fused != 0 {
				t.Errorf("DisableCrosstabFusion instance fused %d requests", fused)
			}
			if !inst.disable && fused < 5 {
				t.Errorf("fused %d requests; the corpus must exercise both answers", fused)
			}
		})
	}
}

// cloneFusionRequest deep-copies r through its wire form, so resolving
// defaults on the copy cannot leak into the request predict sees.
func cloneFusionRequest(t *testing.T, r *types.Request) *types.Request {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out types.Request
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}
