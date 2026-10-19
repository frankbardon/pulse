package pulse_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// prevStateTransitions is one subject's (t1, t2) plan pair; nil is a
// null state. Subject 6's null first state reads as a null previous
// state in the long form and a null plan_t1 in the wide form — both
// drop from the row axis.
var prevStateTransitions = [][2]any{
	{"free", "paid"}, {"free", "free"}, {"paid", "pro"}, {"pro", "free"},
	{"paid", "paid"}, {nil, "paid"}, {"pro", "pro"}, {"free", "paid"},
}

// prevStateLongAndWide writes the transitions as a long panel
// (panel.pulse: id, seen, plan — two rows per subject) and as its wide
// twin (wide.pulse: id, plan_t1, plan_t2 — one row per subject). The
// wide cohort's dictionary is built in a different first-seen order
// (t2 values first), so a match cannot ride a shared dictionary layout.
func prevStateLongAndWide(t *testing.T, opts pulse.Options) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	var long [][3]any
	for i, tr := range prevStateTransitions {
		id := uint64(i + 1)
		long = append(long, [3]any{id, int32(1), tr[0]}, [3]any{id, int32(2), tr[1]})
	}
	panelCohort(t, p, "panel.pulse", long)

	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, Description: "Customer identifier of the wide row"},
		{Name: "plan_t2", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan at the second wave"},
		{Name: "plan_t1", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan at the first wave"},
	}}
	bld, err := p.NewCohortBuilder(context.Background(), "wide.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, tr := range prevStateTransitions {
		if err := bld.Append(pulse.CohortRow{uint64(i + 1), tr[1], tr[0]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bld.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fs
}

// prevStateAttr is the long panel's previous-plan slot.
func prevStateAttr() []*types.Attribute {
	return []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "id", OrderField: "seen", Label: "prev"}}
}

// transitionCrosstab is from × to over a cohort, counted on the to
// column.
func transitionCrosstab(cohort, from, to string, attrs []*types.Attribute) *types.Request {
	return &types.Request{
		Cohort:     &types.Cohort{Filename: cohort},
		Attributes: attrs,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: from}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: to}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: to, Label: "n"},
		},
	}
}

// TestPreviousState_CrosstabMatchesWideForm: the long panel's
// previous-state × state crosstab equals the wide plan_t1 × plan_t2
// crosstab cell for cell and label for label — the output column
// resolves through the state field's dictionary, so its keys are state
// names, never dictionary indices, and a null previous state drops from
// the axis instead of becoming a bucket.
func TestPreviousState_CrosstabMatchesWideForm(t *testing.T) {
	p, _ := prevStateLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	long, err := p.Process(ctx, transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr()))
	if err != nil {
		t.Fatal(err)
	}
	wide, err := p.Process(ctx, transitionCrosstab("wide.pulse", "plan_t1", "plan_t2", nil))
	if err != nil {
		t.Fatal(err)
	}
	lm, wm := long.Crosstab.Matrix, wide.Crosstab.Matrix
	want := []types.AxisKey{{"free"}, {"paid"}, {"pro"}}
	if !reflect.DeepEqual(lm.RowKeys, want) || !reflect.DeepEqual(lm.ColumnKeys, want) {
		t.Fatalf("long axes rows %v cols %v, want state names %v on both", lm.RowKeys, lm.ColumnKeys, want)
	}
	if !reflect.DeepEqual(lm.RowKeys, wm.RowKeys) || !reflect.DeepEqual(lm.ColumnKeys, wm.ColumnKeys) {
		t.Fatalf("axes differ: long %v x %v, wide %v x %v", lm.RowKeys, lm.ColumnKeys, wm.RowKeys, wm.ColumnKeys)
	}
	if !reflect.DeepEqual(lm.Cells, wm.Cells) {
		t.Fatalf("cells differ:\nlong %v\nwide %v", lm.Cells, wm.Cells)
	}
}

// TestPreviousState_GroupCategoryKeysAndInclude: GROUP_CATEGORY on the
// output column keys by state name, Group.Include filters and orders by
// those names, the grouper's dict_size is the state field's, and the
// null previous states count in the grouper's n_null.
func TestPreviousState_GroupCategoryKeysAndInclude(t *testing.T) {
	p, _ := prevStateLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	req := func(include []string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "panel.pulse"},
			Attributes:   prevStateAttr(),
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "prev", Include: include}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}},
		}
	}
	resp, err := p.Process(ctx, req(nil))
	if err != nil {
		t.Fatal(err)
	}
	got := map[any]any{}
	for _, row := range resp.Data {
		got[row["prev"]] = row["n"]
	}
	if want := map[any]any{"free": 3.0, "paid": 2.0, "pro": 2.0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped by prev %v, want %v", got, want)
	}
	gc := resp.Components.Groupers[0]
	if gc.NNull != 9 {
		t.Errorf("grouper n_null %d, want 9 (8 first records + 1 null previous state)", gc.NNull)
	}
	if ds, _ := gc.Operator["dict_size"].(int); ds != 3 {
		t.Errorf("grouper dict_size %v, want the state field's 3", gc.Operator["dict_size"])
	}

	resp, err = p.Process(ctx, req([]string{"pro", "free"}))
	if err != nil {
		t.Fatal(err)
	}
	var keys []any
	for _, row := range resp.Data {
		keys = append(keys, row["prev"])
	}
	if want := []any{"pro", "free"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("Include [pro free] keys %v, want %v", keys, want)
	}
}

// TestPreviousState_LabelsTranslateLikeTheSourceField: a req.Labels
// binding on the output column validates (it is categorical through the
// inherited dictionary) on Process and predict alike and translates the
// axis as the same binding translates the state field's.
func TestPreviousState_LabelsTranslateLikeTheSourceField(t *testing.T) {
	p, fs := prevStateLongAndWide(t, pulse.Options{Extensions: pulse.Extensions{LabelTables: map[string]pulse.LabelTable{
		"plans": {Rows: map[string]string{"free": "Free tier", "paid": "Paid tier", "pro": "Pro tier"}},
	}}})
	req := transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr())
	req.Labels = []*types.LabelBinding{{Field: "prev", Table: "plans"}, {Field: "plan", Table: "plans"}}
	if env := predictEnvelope(t, p, fs, "panel.pulse", req); len(env.Errors) != 0 {
		t.Fatalf("predict refused the binding: %+v", env.Errors)
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Crosstab.Matrix
	want := []types.AxisKey{{"Free tier"}, {"Paid tier"}, {"Pro tier"}}
	if !reflect.DeepEqual(m.RowKeys, want) || !reflect.DeepEqual(m.ColumnKeys, want) {
		t.Fatalf("labelled axes rows %v cols %v, want %v on both", m.RowKeys, m.ColumnKeys, want)
	}
}

// TestPreviousState_PredictTypesOutputCategorical: predict reads the
// output column's axis bound off the inherited dictionary — a per-row
// chi-square over previous-state × state predicts the wide twin's
// p-value count on the dictionary basis — and the runtime's row axis
// carries that many states (one per-row test each).
func TestPreviousState_PredictTypesOutputCategorical(t *testing.T) {
	p, fs := prevStateLongAndWide(t, pulse.Options{})
	withChiSq := func(r *types.Request) *types.Request {
		r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow}}
		return r
	}
	pvalues := func(cohort string, r *types.Request) *descriptor.PValueCount {
		env := predictEnvelope(t, p, fs, cohort, r)
		if len(env.Errors) != 0 {
			t.Fatalf("%s: predict errors %+v", cohort, env.Errors)
		}
		res, ok := env.Data.(*descriptor.PredictResult)
		if !ok || res.PValues == nil {
			t.Fatalf("%s: predict data %T lacks p_values", cohort, env.Data)
		}
		return res.PValues
	}
	long := pvalues("panel.pulse", withChiSq(transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr())))
	wide := pvalues("wide.pulse", withChiSq(transitionCrosstab("wide.pulse", "plan_t1", "plan_t2", nil)))
	if *long != *wide || long.Basis != descriptor.PValueBasisDictionary || long.Total != 3 {
		t.Fatalf("predicted p-values long %+v, wide %+v; want both 3 on the dictionary basis", long, wide)
	}
	resp, err := p.Process(context.Background(), withChiSq(transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr())))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 1 || len(resp.Crosstab.Matrix.RowKeys) != long.Total {
		t.Fatalf("runtime rows %v, overlays %d; want %d rows and one overlay", resp.Crosstab.Matrix.RowKeys, len(resp.Overlays), long.Total)
	}
}

// TestPreviousState_MarkovOverTransitionCrosstab: OVERLAY_MARKOV runs
// over the square previous-state × state host (both axes carry the same
// state names, so the key sets match).
func TestPreviousState_MarkovOverTransitionCrosstab(t *testing.T) {
	p, _ := prevStateLongAndWide(t, pulse.Options{})
	req := transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr())
	req.Overlays = []types.OverlaySpec{markovOverlay("")}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Kind != types.OverlayKindMarkov {
		t.Fatalf("overlays %+v", resp.Overlays)
	}
}

// TestPreviousState_CorrespondenceDimensionsMatchRuntime: predict
// bounds a correspondence overlay's dimensions by the previous-state
// axis's inherited dictionary, so a too-large dimensions param over the
// 3 x 3 transition table is refused by predict exactly as the runtime
// refuses it (code, message, details).
func TestPreviousState_CorrespondenceDimensionsMatchRuntime(t *testing.T) {
	p, fs := prevStateLongAndWide(t, pulse.Options{})
	req := transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr())
	req.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix,
		Params: json.RawMessage(`{"dimensions":3}`)}}
	_, err := p.Process(context.Background(), req)
	requireCode(t, err, errors.PULSE_OVERLAY_PARAM_MISSING)
	sameEntry(t, predictEnvelope(t, p, fs, "panel.pulse", req), err)
}

// TestPreviousState_SeriesBaselineBoundMatchesRuntime: a series overlay
// grouped by the previous-state column is bounded by its inherited
// dictionary (3 states), so a baseline position past the end is refused
// by predict as the runtime refuses it — the FORMULA baseline_position
// with the runtime's code, message and details; the baseline-index ref
// with the runtime's code (its predict message differs from the
// runtime's for every GROUP_CATEGORY host, the state field's too).
func TestPreviousState_SeriesBaselineBoundMatchesRuntime(t *testing.T) {
	p, fs := prevStateLongAndWide(t, pulse.Options{})
	for _, c := range []struct {
		name  string
		spec  types.OverlaySpec
		exact bool
	}{
		{"formula", types.OverlaySpec{Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
			Params: json.RawMessage(`{"formula": "value / baseline", "baseline_position": 3}`)}, true},
		{"index_vs_baseline", types.OverlaySpec{Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: 3}}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "panel.pulse"},
				Attributes:   prevStateAttr(),
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "prev"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}},
				Overlays:     []types.OverlaySpec{c.spec},
			}
			_, err := p.Process(context.Background(), req)
			ce := requireCode(t, err, errors.PULSE_OVERLAY_REF_UNKNOWN)
			env := predictEnvelope(t, p, fs, "panel.pulse", req)
			if c.exact {
				sameEntry(t, env, err)
				return
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) {
				t.Fatalf("predict errors %+v, want %s", env.Errors, ce.Code)
			}
		})
	}
}
