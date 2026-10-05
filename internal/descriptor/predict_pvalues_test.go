package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// pvalueCohort: x numeric, g a 4-entry categorical, h a 3-entry
// categorical, b a packed_bool.
func pvalueCohort(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Measured score per respondent"},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Description: "Region of the respondent", Dictionary: makeDictionary(t, "n", "s", "e", "w")},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, Description: "Answer to the survey question", Dictionary: makeDictionary(t, "yes", "no", "maybe")},
		{Name: "b", Type: encoding.FieldTypePackedBool, Description: "Whether the respondent opted in"},
	}})
}

func pvTest(m *types.Multiplicity) *types.Test {
	return &types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g", Multiplicity: m}
}

func pvCrosstab(rows, cols string) *types.CrosstabSpec {
	axis := func(f string) []*types.Group {
		if f == "x" {
			return []*types.Group{{Type: types.GROUP_RANGE, Field: f, Interval: 10}}
		}
		return []*types.Group{{Type: types.GROUP_CATEGORY, Field: f}}
	}
	return &types.CrosstabSpec{Rows: axis(rows), Columns: axis(cols), Cell: &types.Aggregation{Type: types.AGG_COUNT, Label: "n"}}
}

func pvOverlay(kind types.OverlayKind, scope types.OverlayScope, m *types.Multiplicity) types.OverlaySpec {
	return types.OverlaySpec{Kind: kind, Scope: scope, Multiplicity: m}
}

// TestPredict_PValues counts the inferential p-values a request emits
// and how many no correction reaches.
func TestPredict_PValues(t *testing.T) {
	holm := mult(mHolm, "", 0)
	none := mult(mNone, "", 0)
	pv := func(total, uncorrected int, basis string) *descriptor.PValueCount {
		return &descriptor.PValueCount{Total: total, Uncorrected: uncorrected, Basis: basis, Threshold: descriptor.MultiplicityTriggerThreshold}
	}
	cases := []struct {
		name string
		req  *types.Request
		def  *types.Multiplicity
		want *descriptor.PValueCount
	}{
		{"no test, no overlay", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}, nil, nil},
		{"tests uncorrected", &types.Request{Tests: []*types.Test{pvTest(nil), pvTest(nil), pvTest(nil)}}, nil,
			pv(3, 3, descriptor.PValueBasisExact)},
		{"post-tests count, Tukey excluded", &types.Request{
			Tests:     []*types.Test{pvTest(nil)},
			PostTests: []*types.Test{{Type: types.TEST_TUKEY_HSD, Field: "s"}, {Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"}},
		}, nil, pv(2, 2, descriptor.PValueBasisExact)},
		{"request correction reaches every test", &types.Request{Multiplicity: holm, Tests: []*types.Test{pvTest(nil), pvTest(nil)}}, nil,
			pv(2, 0, descriptor.PValueBasisExact)},
		{"instance default reaches every test", &types.Request{Tests: []*types.Test{pvTest(nil), pvTest(nil)}}, holm,
			pv(2, 0, descriptor.PValueBasisExact)},
		{"a none slot stays uncorrected", &types.Request{Multiplicity: holm, Tests: []*types.Test{pvTest(none), pvTest(nil)}}, nil,
			pv(2, 1, descriptor.PValueBasisExact)},
		{"pairwise row scope from dictionaries", &types.Request{
			Crosstab: pvCrosstab("g", "h"),
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindPairwisePropZ, types.OverlayScopeRow, nil)},
		}, nil, pv(18, 18, descriptor.PValueBasisDictionary)}, // C(4,2) row pairs x 3 columns
		{"pairwise column scope from dictionaries", &types.Request{
			Crosstab: pvCrosstab("g", "h"),
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindPairwiseWelchT, types.OverlayScopeColumn, nil)},
		}, nil, pv(12, 12, descriptor.PValueBasisDictionary)}, // C(3,2) column pairs x 4 rows
		{"packed_bool axis has two buckets", &types.Request{
			Crosstab: pvCrosstab("b", "h"),
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindFisherExactCell, types.OverlayScopeCell, nil)},
		}, nil, pv(6, 6, descriptor.PValueBasisDictionary)},
		{"per-row, per-column and whole-table kinds", &types.Request{
			Crosstab: pvCrosstab("g", "h"),
			Overlays: []types.OverlaySpec{
				pvOverlay(types.OverlayKindChiSqRow, types.OverlayScopeRow, nil),
				pvOverlay(types.OverlayKindChiSqCol, types.OverlayScopeColumn, holm),
				pvOverlay(types.OverlayKindChiSqMatrix, types.OverlayScopeMatrix, nil),
			},
		}, nil, pv(4+3+1, 4+1, descriptor.PValueBasisDictionary)},
		{"whole-table kind alone is exact", &types.Request{
			Crosstab: pvCrosstab("x", "h"),
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindChiSqMatrix, types.OverlayScopeMatrix, nil)},
		}, nil, pv(1, 1, descriptor.PValueBasisExact)},
		{"numeric axis is a lower bound", &types.Request{
			Crosstab: pvCrosstab("x", "h"),
			Tests:    []*types.Test{pvTest(nil)},
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindPairwisePropZ, types.OverlayScopeRow, holm)},
		}, nil, pv(2, 1, descriptor.PValueBasisLowerBound)},
		{"descriptive overlay emits no p-value", &types.Request{
			Crosstab: pvCrosstab("g", "h"),
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindShareOfRow, types.OverlayScopeRow, nil)},
		}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := predictFromBytes(pvalueCohort(t), c.req, &PredictOptions{DefaultMultiplicity: c.def})
			got := env.Data.(*descriptor.PredictResult).PValues
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("PValues = %+v, want %+v (errors %v)", got, c.want, env.Errors)
			}
		})
	}
}

// TestPredict_PValuesFollowInstance: a hidden capability:multiplicity
// omits the count; a hidden test type or overlay kind is not counted.
func TestPredict_PValuesFollowInstance(t *testing.T) {
	req := func() *types.Request {
		return &types.Request{
			Crosstab: pvCrosstab("g", "h"),
			Tests:    []*types.Test{pvTest(nil), {Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"}},
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindChiSqMatrix, types.OverlayScopeMatrix, nil)},
		}
	}
	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want *descriptor.PValueCount
	}{
		{"unscoped", nil, &descriptor.PValueCount{Total: 3, Uncorrected: 3, Basis: descriptor.PValueBasisExact, Threshold: 10}},
		{"multiplicity hidden", scopedExcept(featMultiplicity), nil},
		{"a test type hidden", scopedExcept("TEST_ANOVA_F"), &descriptor.PValueCount{Total: 2, Uncorrected: 2, Basis: descriptor.PValueBasisExact, Threshold: 10}},
		{"the overlay kind hidden", scopedExcept(string(types.OverlayKindChiSqMatrix)), &descriptor.PValueCount{Total: 2, Uncorrected: 2, Basis: descriptor.PValueBasisExact, Threshold: 10}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := predictFromBytes(pvalueCohort(t), req(), &PredictOptions{Instance: c.inst})
			if got := env.Data.(*descriptor.PredictResult).PValues; !reflect.DeepEqual(got, c.want) {
				t.Errorf("PValues = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestPredict_PValuesOmittedOnRefusal: a refused multiplicity block
// reports no count (the request does not run).
func TestPredict_PValuesOmittedOnRefusal(t *testing.T) {
	req := &types.Request{Tests: []*types.Test{pvTest(mult("sidak", "", 0))}}
	env := predictFromBytes(pvalueCohort(t), req, nil)
	if len(env.Errors) == 0 {
		t.Fatal("unknown method not refused")
	}
	if got := env.Data.(*descriptor.PredictResult).PValues; got != nil {
		t.Errorf("PValues = %+v on a refused request", got)
	}
}

func TestMultiplicityTriggerThreshold(t *testing.T) {
	if descriptor.MultiplicityTriggerThreshold != 10 {
		t.Errorf("threshold = %d, want 10", descriptor.MultiplicityTriggerThreshold)
	}
}
