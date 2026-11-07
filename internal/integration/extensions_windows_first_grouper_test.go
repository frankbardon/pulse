package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// extensions_windows_first_grouper_test.go (U67 E3-S2): an extension
// grouper whose FieldInputs hook names a window label keys the records
// by it exactly as GROUP_CATEGORY over the label does — the request
// runs windows-first, predict says so, and the runtime keys the
// windowed value off the records. Before, the classification saw only
// a grouper's Field: predict and the runtime both refused the read as
// an unknown field.

const groupWfxByOf types.GroupType = "GROUP_WFX_BY_OF"

// wfxByOf keys each row by the column params.of names (Field is the
// output key column); a null value joins no bucket.
type wfxByOf struct{ of string }

func (g *wfxByOf) Group(rows extend.Rows, _ string) (map[string][]int, error) {
	out := map[string][]int{}
	for i := 0; i < rows.Len(); i++ {
		if v, ok := rows.At(i).NumericValue(g.of); ok {
			k := strconv.FormatFloat(v, 'f', -1, 64)
			out[k] = append(out[k], i)
		}
	}
	return out, nil
}

func wfxByOfRegistration() pulse.GrouperRegistration {
	return pulse.GrouperRegistration{
		Name:        groupWfxByOf,
		Description: "Test-only grouper keying rows by the column params.of names.",
		Factory: func(spec *types.Group, _ *encoding.Schema) (extend.Grouper, error) {
			var p struct {
				Of string `json:"of"`
			}
			if len(spec.Params) > 0 {
				if err := json.Unmarshal(spec.Params, &p); err != nil {
					return nil, err
				}
			}
			return &wfxByOf{of: p.Of}, nil
		},
		Params: []pulse.ParamMeta{{Name: "of", JSONType: "string", Description: "Column to key by."}},
		FieldInputs: func(raw json.RawMessage) []string {
			var p struct {
				Of string `json:"of"`
			}
			if json.Unmarshal(raw, &p) != nil || p.Of == "" {
				return nil
			}
			return []string{p.Of}
		},
	}
}

// wfxKeyed is resp's grouped rows as sorted "key=s" pairs, the key read
// from column key, so the extension response compares to its twin's.
func wfxKeyed(t *testing.T, resp *pulse.Response, key string) []string {
	t.Helper()
	b, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("data %s: %v", b, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%v=%v", r[key], r["s"]))
	}
	sort.Strings(out)
	return out
}

// TestExtensions_GrouperFieldInputsWindowLabelRunsWindowsFirst:
// Predict and Process agree — the request is valid and windows-first,
// and the extension grouper's buckets equal GROUP_CATEGORY's over the
// window label.
func TestExtensions_GrouperFieldInputsWindowLabelRunsWindowsFirst(t *testing.T) {
	t.Parallel()

	p, _ := windowParityCohort(t, pulse.Extensions{Groupers: []pulse.GrouperRegistration{wfxByOfRegistration()}})
	ctx := context.Background()
	at := &types.Cohort{Filename: "series.pulse"}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "s"}}
	req := &pulse.Request{Cohort: at, Windows: wfxLag(), Aggregations: sum,
		Groups: []*types.Group{{Type: groupWfxByOf, Field: "g", Params: json.RawMessage(`{"of": "lag_x"}`)}}}
	res, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("predict refused: %+v", res)
	}
	if res.PostWindow == nil || !res.PostWindow.WindowsFirst {
		t.Fatalf("post_window %+v, want windows_first", res.PostWindow)
	}
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, &pulse.Request{Cohort: at, Windows: wfxLag(), Aggregations: sum,
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lag_x"}}})
	if err != nil {
		t.Fatalf("built-in twin: %v", err)
	}
	g, w := wfxKeyed(t, got, "g"), wfxKeyed(t, want, "lag_x")
	if fmt.Sprint(g) != fmt.Sprint(w) || len(g) == 0 {
		t.Fatalf("extension buckets %v, want the built-in twin's %v", g, w)
	}
}

// TestExtensions_GrouperFieldInputsWindowLabelCrosstabAxis: as a
// crosstab row axis the hook's window read is accepted by predict
// (windows-first) and by the runtime alike, and the axis carries the
// built-in twin's keys.
func TestExtensions_GrouperFieldInputsWindowLabelCrosstabAxis(t *testing.T) {
	t.Parallel()

	p, _ := windowParityCohort(t, pulse.Extensions{Groupers: []pulse.GrouperRegistration{wfxByOfRegistration()}})
	ctx := context.Background()
	at := &types.Cohort{Filename: "series.pulse"}
	ct := func(row *types.Group) *pulse.Request {
		return &pulse.Request{Cohort: at, Windows: wfxLag(), Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{row}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "y", Label: "s"}}}
	}
	req := ct(&types.Group{Type: groupWfxByOf, Field: "t", Params: json.RawMessage(`{"of": "lag_x"}`)})
	res, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.PostWindow == nil || !res.PostWindow.WindowsFirst {
		t.Fatalf("predict valid %v, post_window %+v; want valid, windows_first (errors %v)", res.Valid, res.PostWindow, res.Errors)
	}
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, ct(&types.Group{Type: types.GROUP_CATEGORY, Field: "lag_x"}))
	if err != nil {
		t.Fatalf("built-in twin: %v", err)
	}
	if got.Crosstab == nil || want.Crosstab == nil {
		t.Fatalf("crosstab payloads %v / %v", got.Crosstab, want.Crosstab)
	}
	g, _ := json.Marshal(got.Crosstab.Matrix.Cells)
	w, _ := json.Marshal(want.Crosstab.Matrix.Cells)
	if string(g) != string(w) {
		t.Fatalf("extension axis cells %s, want the built-in twin's %s", g, w)
	}
}
