package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

func TestExpandMatrixValues(t *testing.T) {
	for _, c := range []struct {
		name string
		in   *types.MatrixValues
		want [][]float64
	}{
		{"full copied", &types.MatrixValues{Kind: types.MatrixKindSquareSymmetric, Encoding: types.MatrixEncodingFull,
			RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"}, Values: [][]float64{{1, 2}, {2, 1}}},
			[][]float64{{1, 2}, {2, 1}}},
		{"upper mirrored", &types.MatrixValues{Kind: types.MatrixKindSquareSymmetric, Encoding: types.MatrixEncodingUpper,
			RowKeys: []string{"a", "b", "c"}, ColumnKeys: []string{"a", "b", "c"}, Values: [][]float64{{1, 2, 3}, {4, 5}, {6}}},
			[][]float64{{1, 2, 3}, {2, 4, 5}, {3, 5, 6}}},
		{"rectangular", &types.MatrixValues{Kind: types.MatrixKindRectangular, Encoding: types.MatrixEncodingFull,
			RowKeys: []string{"a", "b"}, ColumnKeys: []string{"PC1"}, Values: [][]float64{{0.5}, {-0.5}}},
			[][]float64{{0.5}, {-0.5}}},
		{"short row", &types.MatrixValues{Kind: types.MatrixKindSquareSymmetric, Encoding: types.MatrixEncodingFull,
			RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"}, Values: [][]float64{{1}}},
			[][]float64{{1, nan}, {nan, nan}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ExpandMatrixValues(c.in)
			if !sameGrid(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
	if ExpandMatrixValues(nil) != nil {
		t.Error("nil matrix expanded")
	}
}

func sameGrid(a, b [][]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] && !(math.IsNaN(a[i][j]) && math.IsNaN(b[i][j])) {
				return false
			}
		}
	}
	return true
}

// gridOf reads a MATRIX payload's cell values back as a float grid,
// with its row and column keys.
func gridOf(t *testing.T, m *types.MatrixPayload) ([][]float64, []string, []string) {
	t.Helper()
	var rows, cols []string
	for _, k := range m.RowKeys {
		rows = append(rows, k[0].(string))
	}
	for _, k := range m.ColumnKeys {
		cols = append(cols, k[0].(string))
	}
	out := make([][]float64, len(m.Cells))
	for i, r := range m.Cells {
		for _, c := range r {
			if !c.Present {
				t.Fatalf("absent cell in %v", m.Cells)
			}
			out[i] = append(out[i], c.Value.(float64))
		}
	}
	return out, rows, cols
}

func mgsResult(name string, enc types.MatrixEncoding, keys []string, values [][]float64) types.MatrixResult {
	return types.MatrixResult{Name: name, Type: types.MAT_CORRELATION, Primary: &types.MatrixValues{
		Kind: types.MatrixKindSquareSymmetric, Encoding: enc, RowKeys: keys, ColumnKeys: keys, Values: values}}
}

// TestMatrixGroupSummary_Cells: the union of the buckets' keys in
// first-seen order; per cell the unweighted mean, min, max, range and
// count over the buckets with a finite cell; a NaN cell or a missing
// key contributes nothing; a cell no bucket fed is null with n 0; an
// upper bucket reads as its mirrored full matrix; another spec's
// result is not read.
func TestMatrixGroupSummary_Cells(t *testing.T) {
	results := []types.MatrixResult{
		mgsResult("r", types.MatrixEncodingFull, []string{"a", "b"}, [][]float64{{1, 0.2}, {0.2, 1}}),
		mgsResult("other", types.MatrixEncodingFull, []string{"a", "b"}, [][]float64{{1, 9}, {9, 1}}),
		mgsResult("r", types.MatrixEncodingUpper, []string{"b", "c"}, [][]float64{{1, -0.4}, {1}}),
		mgsResult("r", types.MatrixEncodingFull, []string{"a", "b"}, [][]float64{{1, nan}, {nan, 1}}),
		mgsResult("r", types.MatrixEncodingFull, []string{"a", "b"}, [][]float64{{1, 0.6}, {0.6, 1}}),
	}
	spec := &types.OverlaySpec{Kind: types.OverlayKindMatrixGroupSummary, Scope: types.OverlayScopeMatrix,
		Ref: types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: "r"}}}
	layer, ws, err := applyMatrixGroupSummary(spec, newMatrixResultHostView("r", results))
	if err != nil || ws != nil {
		t.Fatalf("err %v, warnings %v", err, ws)
	}
	if layer.Name != "group_summary_r" || layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer %+v", layer)
	}
	if cerr := overlayhost.PartsRefusal(&layer, 0); cerr != nil {
		t.Fatal(cerr)
	}
	keys := []string{"a", "b", "c"}
	for _, c := range []struct {
		part string
		want [][]float64
	}{
		{"", [][]float64{{1, 0.4, nan}, {0.4, 1, -0.4}, {nan, -0.4, 1}}},
		{overlayhost.GroupSummaryPartMin, [][]float64{{1, 0.2, nan}, {0.2, 1, -0.4}, {nan, -0.4, 1}}},
		{overlayhost.GroupSummaryPartMax, [][]float64{{1, 0.6, nan}, {0.6, 1, -0.4}, {nan, -0.4, 1}}},
		{overlayhost.GroupSummaryPartRange, [][]float64{{0, 0.6 - 0.2, nan}, {0.6 - 0.2, 0, 0}, {nan, 0, 0}}},
		{overlayhost.GroupSummaryPartNGroups, [][]float64{{3, 2, 0}, {2, 4, 1}, {0, 1, 1}}},
	} {
		m := layer.Payload.Matrix
		if c.part != "" {
			p := layer.Payload.Parts[c.part]
			if p == nil || p.Shape != types.OverlayShapeMatrix {
				t.Fatalf("part %q = %+v", c.part, p)
			}
			m = p.Matrix
		}
		got, rows, cols := gridOf(t, m)
		if !reflect.DeepEqual(rows, keys) || !reflect.DeepEqual(cols, keys) {
			t.Fatalf("%q keys %v x %v, want %v", c.part, rows, cols, keys)
		}
		// The mean of 0.2 and 0.6 is 0.4 only within an ulp.
		for i := range got {
			for j := range got[i] {
				g, w := got[i][j], c.want[i][j]
				if math.IsNaN(g) != math.IsNaN(w) || (!math.IsNaN(w) && math.Abs(g-w) > 1e-15) {
					t.Errorf("%q [%d][%d] = %v, want %v", c.part, i, j, g, w)
				}
			}
		}
	}
	if len(layer.Payload.Parts) != 4 {
		t.Errorf("parts %v", layer.Payload.Parts)
	}
	b, err := json.Marshal(layer)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if v := back["payload"].(map[string]any)["matrix"].(map[string]any)["cells"].([]any)[0].([]any)[2].(map[string]any)["value"]; v != nil {
		t.Errorf("no-bucket cell marshals %v, want null", v)
	}
}

// TestMatrixGroupSummary_NoResults: a spec with no result (an empty
// grouped run) emits an empty matrix, not a fault.
func TestMatrixGroupSummary_NoResults(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindMatrixGroupSummary, Scope: types.OverlayScopeMatrix,
		Ref: types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: "r"}}}
	layer, _, err := applyMatrixGroupSummary(spec, newMatrixResultHostView("r", nil))
	if err != nil || layer.Payload.Matrix == nil || len(layer.Payload.Matrix.Cells) != 0 {
		t.Fatalf("layer %+v err %v", layer, err)
	}
	if _, err := json.Marshal(layer); err != nil {
		t.Fatal(err)
	}
}

// TestApplyMatrixResultOverlays_IndexAligned: the pass writes each
// MATRIX_RESULT layer at its spec's index, leaves every other layer as
// the earlier hook wrote it, allocates aligned layers when none exist,
// and is a no-op (resp untouched) without a MATRIX_RESULT spec or under
// a ComputePlan that skips it.
func TestApplyMatrixResultOverlays_IndexAligned(t *testing.T) {
	mr := types.OverlaySpec{Kind: types.OverlayKindMatrixGroupSummary, Scope: types.OverlayScopeMatrix,
		Ref: types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: "r"}}}
	share := types.OverlaySpec{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup}
	req := func(specs ...types.OverlaySpec) *types.Request {
		return &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}},
			Matrices:     []types.MatrixSpec{{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"x", "y"}}},
			Overlays:     specs,
		}
	}
	results := []types.MatrixResult{mgsResult("r", types.MatrixEncodingFull, []string{"x", "y"}, [][]float64{{1, 0.5}, {0.5, 1}})}

	resp := &types.Response{Matrices: results, Overlays: []types.OverlayLayer{{Name: "series"}, {}}}
	if err := ApplyMatrixResultOverlays(req(share, mr), resp, nil, FullComputePlan()); err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 2 || resp.Overlays[0].Name != "series" || resp.Overlays[1].Kind != types.OverlayKindMatrixGroupSummary {
		t.Fatalf("overlays %+v", resp.Overlays)
	}

	resp = &types.Response{Matrices: results}
	if err := ApplyMatrixResultOverlays(req(mr), resp, nil, FullComputePlan()); err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
		t.Fatalf("overlays %+v", resp.Overlays)
	}

	for name, c := range map[string]struct {
		req     *types.Request
		compute ComputePlan
	}{
		"no MATRIX_RESULT spec": {req(share), FullComputePlan()},
		"skipped by the plan":   {req(mr), FullComputePlan().WithOverlayLayers([]bool{false})},
	} {
		resp := &types.Response{Matrices: results}
		if err := ApplyMatrixResultOverlays(c.req, resp, nil, c.compute); err != nil || resp.Overlays != nil {
			t.Errorf("%s: overlays %+v err %v", name, resp.Overlays, err)
		}
	}

	// A spec without Ref.Matrix is the shared MatrixResultRefusal, same
	// error as predict's.
	bad := req(mr)
	bad.Overlays[0].Ref = types.OverlayRef{}
	err := ApplyMatrixResultOverlays(bad, &types.Response{Matrices: results}, nil, FullComputePlan())
	if cerr := overlayhost.MatrixResultRefusal(bad, &bad.Overlays[0], types.OverlayKindMatrixGroupSummary, 0); err == nil || cerr == nil || err.Error() != cerr.Error() {
		t.Fatalf("err %v, want %v", err, cerr)
	}
}
