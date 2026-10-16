package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

var (
	regionNames  = map[string]string{"north": "North", "south": "South", "east": "East"}
	segmentNames = map[string]string{"retail": "Retail", "wholesale": "Wholesale"}
)

// labelledCrosstabService is the region × segment crosstab cohort with
// the two label tables registered.
func labelledCrosstabService(t *testing.T, fused bool) *Service {
	t.Helper()
	cfg := setupTestFS(t, "xt.pulse", weightedCrosstabSchema(), weightedCrosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(!fused)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"region_names":  {Rows: regionNames},
		"segment_names": {Rows: segmentNames},
	})
	return svc
}

// pairwiseCrosstabRequest is a matrix crosstab carrying a row-scope and
// a column-scope OVERLAY_PAIRWISE_PROP_Z layer.
func pairwiseCrosstabRequest(labels []*types.LabelBinding) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "xt.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "value", Label: "avg"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: []types.OverlaySpec{
			// The mean cell names its proportion reading (an omitted
			// p_source over it is refused, survey-tables E4-S6).
			{Name: "pz_row", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow, Params: json.RawMessage(`{"p_source":"cell_value_pct"}`)},
			{Name: "pz_col", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn, Params: json.RawMessage(`{"p_source":"cell_value_pct"}`)},
		},
		Labels: labels,
	}
}

func relabelKeys(t *testing.T, keys []types.AxisKey, table map[string]string) []types.AxisKey {
	t.Helper()
	out := make([]types.AxisKey, len(keys))
	for i, k := range keys {
		out[i] = make(types.AxisKey, len(k))
		for j, v := range k {
			s, ok := v.(string)
			if !ok || table[s] == "" {
				t.Fatalf("key %v: element %v has no label in the test table", k, v)
			}
			out[i][j] = table[s]
		}
	}
	return out
}

func warningsWithCode(ws []*types.ResponseWarning, code errors.Code) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	for _, w := range ws {
		if w != nil && w.Code == string(code) {
			out = append(out, w)
		}
	}
	return out
}

// TestCrosstab_LabelsReachAxesAndPairKeys: a replace-mode binding on
// each crosstab axis field relabels the matrix row / column keys and
// every pairwise overlay's PAIR keys and echoed host axis, on both
// crosstab arms — while every cell, margin and p-value stays exactly
// the unlabelled run's (only key text moves, never a coordinate).
func TestCrosstab_LabelsReachAxesAndPairKeys(t *testing.T) {
	for _, fused := range []bool{true, false} {
		name := map[bool]string{true: "fused", false: "buffered"}[fused]
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			base, err := labelledCrosstabService(t, fused).Process(ctx, pairwiseCrosstabRequest(nil))
			if err != nil {
				t.Fatalf("unlabelled Process: %v", err)
			}
			got, err := labelledCrosstabService(t, fused).Process(ctx, pairwiseCrosstabRequest([]*types.LabelBinding{
				{Field: "region", Table: "region_names", Mode: types.LabelModeReplace},
				{Field: "segment", Table: "segment_names"},
			}))
			if err != nil {
				t.Fatalf("labelled Process: %v", err)
			}
			bm, gm := base.Crosstab.Matrix, got.Crosstab.Matrix
			if want := relabelKeys(t, bm.RowKeys, regionNames); !reflect.DeepEqual(gm.RowKeys, want) {
				t.Errorf("row keys = %v, want %v", gm.RowKeys, want)
			}
			if want := relabelKeys(t, bm.ColumnKeys, segmentNames); !reflect.DeepEqual(gm.ColumnKeys, want) {
				t.Errorf("column keys = %v, want %v", gm.ColumnKeys, want)
			}
			if !reflect.DeepEqual(gm.Cells, bm.Cells) || !reflect.DeepEqual(gm.RowMargins, bm.RowMargins) ||
				!reflect.DeepEqual(gm.ColumnMargins, bm.ColumnMargins) || !reflect.DeepEqual(gm.GrandTotal, bm.GrandTotal) {
				t.Error("labels moved a cell or margin figure")
			}
			if len(got.Overlays) != 2 || len(base.Overlays) != 2 {
				t.Fatalf("overlays: got %d, base %d, want 2", len(got.Overlays), len(base.Overlays))
			}
			// Row scope: rows = region pairs, columns = the host segments.
			bl, gl := base.Overlays[0].Payload.Matrix, got.Overlays[0].Payload.Matrix
			if len(bl.RowKeys) == 0 {
				t.Fatal("row-scope layer has no pairs — the pair-key check would be vacuous")
			}
			if want := relabelKeys(t, bl.RowKeys, regionNames); !reflect.DeepEqual(gl.RowKeys, want) {
				t.Errorf("row-scope pair keys = %v, want %v", gl.RowKeys, want)
			}
			if !reflect.DeepEqual(gl.ColumnKeys, gm.ColumnKeys) {
				t.Errorf("row-scope layer columns = %v, want the host's %v", gl.ColumnKeys, gm.ColumnKeys)
			}
			if !reflect.DeepEqual(gl.Cells, bl.Cells) {
				t.Error("labels moved a row-scope p-value")
			}
			// Column scope: columns = segment pairs, rows = the host regions.
			bc, gc := base.Overlays[1].Payload.Matrix, got.Overlays[1].Payload.Matrix
			if want := relabelKeys(t, bc.ColumnKeys, segmentNames); !reflect.DeepEqual(gc.ColumnKeys, want) {
				t.Errorf("column-scope pair keys = %v, want %v", gc.ColumnKeys, want)
			}
			if !reflect.DeepEqual(gc.RowKeys, gm.RowKeys) {
				t.Errorf("column-scope layer rows = %v, want the host's %v", gc.RowKeys, gm.RowKeys)
			}
			if !reflect.DeepEqual(gc.Cells, bc.Cells) {
				t.Error("labels moved a column-scope p-value")
			}
			if w := warningsWithCode(got.Warnings, errors.PULSE_LABEL_NOT_APPLIED); len(w) != 0 {
				t.Errorf("unexpected PULSE_LABEL_NOT_APPLIED: %+v", w[0])
			}
			if w := warningsWithCode(got.Warnings, errors.PULSE_LABEL_LOOKUP_MISS); len(w) != 0 {
				t.Errorf("unexpected PULSE_LABEL_LOOKUP_MISS (a value translated twice?): %+v", w[0])
			}
		})
	}
}

// TestCrosstab_AugmentOnMatrixAxisWarns: an augment-mode binding has no
// sibling column on a matrix axis key, so the keys stay raw and the run
// says so with PULSE_LABEL_NOT_APPLIED instead of accepting and ignoring
// the binding.
func TestCrosstab_AugmentOnMatrixAxisWarns(t *testing.T) {
	svc := labelledCrosstabService(t, true)
	got, err := svc.Process(context.Background(), pairwiseCrosstabRequest([]*types.LabelBinding{
		{Field: "region", Table: "region_names", Mode: types.LabelModeAugment},
	}))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, k := range got.Crosstab.Matrix.RowKeys {
		if _, raw := regionNames[k[0].(string)]; !raw {
			t.Errorf("augment rewrote a row key: %v", k)
		}
	}
	w := warningsWithCode(got.Warnings, errors.PULSE_LABEL_NOT_APPLIED)
	if len(w) != 1 {
		t.Fatalf("want one PULSE_LABEL_NOT_APPLIED, got %+v", got.Warnings)
	}
	if w[0].Details["field"] != "region" || w[0].Details["reason"] != labelReasonAugmentAxis || w[0].Details["index"] != 0 {
		t.Errorf("details = %+v", w[0].Details)
	}
}

// TestProcess_LabelNotApplied: a caller-authored binding on a field the
// response carries nowhere warns PULSE_LABEL_NOT_APPLIED; a binding that
// reaches a column does not, and neither does an instance default
// (AutoLabels), which is offered to every request whose schema carries
// the field.
func TestProcess_LabelNotApplied(t *testing.T) {
	binding := &types.LabelBinding{Field: "country", Table: "country_names", Mode: types.LabelModeReplace}
	cases := []struct {
		name    string
		grouped bool
		labels  []*types.LabelBinding
		auto    []*types.LabelBinding
		want    int
	}{
		{"grouped_binding_reaches", true, []*types.LabelBinding{binding}, nil, 0},
		{"ungrouped_binding_reaches_nothing", false, []*types.LabelBinding{binding}, nil, 1},
		{"ungrouped_auto_default_silent", false, nil, []*types.LabelBinding{binding}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, path := setupGroupableCohort(t)
			attachLabelExtensions(t, svc, map[string]processing.LabelTable{
				"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada", "USA": "USofA"}},
			})
			svc.SetAutoLabels(tc.auto)
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: path},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
			}
			for _, b := range tc.labels {
				cp := *b
				req.Labels = append(req.Labels, &cp)
			}
			if tc.grouped {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}}
			}
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			w := warningsWithCode(resp.Warnings, errors.PULSE_LABEL_NOT_APPLIED)
			if len(w) != tc.want {
				t.Fatalf("PULSE_LABEL_NOT_APPLIED count = %d, want %d (%+v)", len(w), tc.want, resp.Warnings)
			}
			if tc.want == 1 && (w[0].Details["field"] != "country" || w[0].Details["reason"] != labelReasonNoSurface) {
				t.Errorf("details = %+v", w[0].Details)
			}
		})
	}
}

// TestProcess_LabelsReachGroupKeys: a grouped run's Components group
// keys repeat the Data rows' key, so a replace-mode binding relabels
// them too — the Data row and its components entry keep naming the same
// bucket.
func TestProcess_LabelsReachGroupKeys(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada", "USA": "USofA"}},
	})
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
		Labels:       []*types.LabelBinding{{Field: "country", Table: "country_names", Mode: types.LabelModeReplace}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
		t.Fatalf("components = %+v", resp.Components)
	}
	groups := resp.Components.Aggregations[0].Groups
	if len(groups) != len(resp.Data) || len(groups) == 0 {
		t.Fatalf("%d component groups for %d Data rows", len(groups), len(resp.Data))
	}
	for i, g := range groups {
		if !reflect.DeepEqual(g.GroupKey, types.AxisKey{resp.Data[i]["country"]}) {
			t.Errorf("groups[%d].group_key = %v, Data row key %v", i, g.GroupKey, resp.Data[i]["country"])
		}
	}
}

// TestFacet_LabelNotApplied: a facet binding on a field the facet did
// not tally warns instead of vanishing.
func TestFacet_LabelNotApplied(t *testing.T) {
	svc := labelledCrosstabService(t, true)
	got, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "xt.pulse"},
		Fields: []string{"region"},
		Labels: []*types.LabelBinding{
			{Field: "region", Table: "region_names"},
			{Field: "segment", Table: "segment_names"},
		},
	})
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	w := warningsWithCode(got.Warnings, errors.PULSE_LABEL_NOT_APPLIED)
	if len(w) != 1 || w[0].Details["field"] != "segment" || w[0].Details["index"] != 1 {
		t.Fatalf("want one PULSE_LABEL_NOT_APPLIED on segment, got %+v", got.Warnings)
	}
}

// chainAutoLabelRequest is a two-stage chain over the groupable cohort:
// stage 0 sums amount per country, stage 1 re-sums the forwarded totals
// per (forwarded) country key.
func chainAutoLabelRequest(path string) *types.ChainRequest {
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: path},
		Stages: []*types.ChainStage{
			{Name: "per_country", Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
			}},
			{Name: "rollup", Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "t2"}},
			}},
		},
	}
}

// TestChain_AutoLabelsEveryStage (#460): the instance's default bindings
// reach every ProcessChain stage, not only stage 0.
//
//   - augment: stage 0 keeps the raw key and adds country_label; the
//     forwarded raw key gets its sibling on stage 1 too (it used not to).
//   - replace (pinned): stage 0 already rewrote the key the next stage
//     reads, so stage 1 is NOT handed the default again — its rows carry
//     the same labels and totals as without the injection, with no
//     PULSE_LABEL_LOOKUP_MISS from translating a label a second time.
func TestChain_AutoLabelsEveryStage(t *testing.T) {
	table := map[string]string{"US": "United States", "CA": "Canada", "USA": "USofA"}
	wantTotals := map[string]float64{"US": 50, "CA": 70, "USA": 90}
	for _, mode := range []types.LabelMode{types.LabelModeAugment, types.LabelModeReplace} {
		t.Run(string(mode), func(t *testing.T) {
			svc, path := setupGroupableCohort(t)
			attachLabelExtensions(t, svc, map[string]processing.LabelTable{"country_names": {Rows: table}})
			svc.SetAutoLabels([]*types.LabelBinding{{Field: "country", Table: "country_names", Mode: mode}})
			req := chainAutoLabelRequest(path)
			resp, err := svc.ProcessChain(context.Background(), req)
			if err != nil {
				t.Fatalf("ProcessChain: %v", err)
			}
			final := resp.Final
			if len(final.Data) != 3 {
				t.Fatalf("stage-1 rows = %d, want 3: %+v", len(final.Data), final.Data)
			}
			for _, row := range final.Data {
				key, _ := row["country"].(string)
				switch mode {
				case types.LabelModeAugment:
					if _, raw := table[key]; !raw {
						t.Errorf("augment rewrote the forwarded key %q", key)
					}
					if row["country_label"] != table[key] {
						t.Errorf("stage-1 row %q: country_label = %v, want %q", key, row["country_label"], table[key])
					}
					if row["t2"] != wantTotals[key] {
						t.Errorf("stage-1 %q total = %v, want %v", key, row["t2"], wantTotals[key])
					}
				case types.LabelModeReplace:
					raw := ""
					for r, l := range table {
						if l == key {
							raw = r
						}
					}
					if raw == "" {
						t.Errorf("stage-1 key %q is not a label", key)
					}
					if row["t2"] != wantTotals[raw] {
						t.Errorf("stage-1 %q total = %v, want %v", key, row["t2"], wantTotals[raw])
					}
				}
			}
			if w := warningsWithCode(final.Warnings, errors.PULSE_LABEL_LOOKUP_MISS); len(w) != 0 {
				t.Errorf("stage 1 re-translated a label: %+v", w[0])
			}
			injected := 0
			for _, b := range req.Stages[1].Request.Labels {
				if b != nil && b.Field == "country" {
					injected++
				}
			}
			wantInjected := map[types.LabelMode]int{types.LabelModeAugment: 1, types.LabelModeReplace: 0}[mode]
			if injected != wantInjected {
				t.Errorf("stage-1 injected %d country default(s), want %d", injected, wantInjected)
			}
		})
	}
}

// TestCompose_OverlayAcrossLabelledAndUnlabelledSlot pins what a
// Compose overlay does when one slot's replace binding relabels its
// crosstab axis and the other slot's does not: the two slots' keys
// differ ("North" vs "north"), so the fold refuses loud with
// PULSE_OVERLAY_KEY_SET_DIVERGENT naming both key spellings — never an
// empty or partial layer. Binding both slots the same way folds clean.
func TestCompose_OverlayAcrossLabelledAndUnlabelledSlot(t *testing.T) {
	bind := []*types.LabelBinding{{Field: "region", Table: "region_names", Mode: types.LabelModeReplace}}
	slot := func(label string, labels []*types.LabelBinding) *types.Request {
		r := pairwiseCrosstabRequest(labels)
		r.Overlays = nil
		r.Crosstab.Margins = types.CrosstabMargins{}
		r.Label = label
		return r
	}
	compose := func(bLabels []*types.LabelBinding) (*types.ComposedResponse, error) {
		return labelledCrosstabService(t, false).Compose(context.Background(), &types.ComposedRequest{
			Requests: []*types.Request{slot("a", bind), slot("b", bLabels)},
			Overlays: []types.ComposeOverlaySpec{{
				Name: "d", Kind: types.OverlayKindDeltaVsRef, Scope: types.OverlayScopeCell,
				Reference: "a", Targets: []string{"b"},
			}},
		})
	}

	_, err := compose(nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_KEY_SET_DIVERGENT {
		t.Fatalf("labelled vs unlabelled slot: err = %v, want PULSE_OVERLAY_KEY_SET_DIVERGENT", err)
	}
	missing, _ := ce.Details["missing"].([]string)
	extra, _ := ce.Details["extra"].([]string)
	if !anyContains(missing, "North") || !anyContains(extra, "north") {
		t.Errorf("details missing=%v extra=%v, want the labelled keys missing and the raw keys extra", missing, extra)
	}

	resp, err := compose(bind)
	if err != nil {
		t.Fatalf("both slots labelled: %v", err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil || len(resp.Overlays[0].Payload.Matrix.Cells) == 0 {
		t.Fatalf("both slots labelled: overlay layer empty: %+v", resp.Overlays)
	}
}

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
