package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// flowsRow is one long-panel record: subject (nil = null key), hour
// offset of the observation instant (nil = null order) and state (nil
// = a gap), plus the (from, to) pair the wide twin carries for it —
// derived here BY HAND from the attribute's rules, never by running it.
type flowsRow struct {
	subject, hour, state any
	from, to             any
}

// flowsPanel is the flows long panel, ordered by a datetime: "trial"
// is a first-only state (only ever a subject's first record), "churned"
// a last-only one, subject 3 has a differing tie at hour 1 (both tied
// rows read free; the hour-2 row is ambiguous, so null), subject 4 an
// identical tie (the row after it reads paid), subject 5 a gap (a null
// state is no predecessor), subject 9 a null-order record and one
// record has a null subject. Listed in a shuffled order so neither
// subject nor time sorts the file.
var flowsPanel = []flowsRow{
	{3, 2, "free", nil, "free"},
	{1, 3, "churned", "paid", "churned"},
	{6, 0, "paid", nil, "paid"},
	{2, 1, "paid", "trial", "paid"},
	{4, 1, "paid", "free", "paid"},
	{9, nil, "paid", nil, "paid"},
	{5, 1, nil, "free", nil},
	{7, 0, "trial", nil, "trial"},
	{1, 0, "trial", nil, "trial"},
	{11, 2, "free", "paid", "free"},
	{3, 1, "free", "free", "free"},
	{6, 3, "churned", "paid", "churned"},
	{8, 1, "paid", "paid", "paid"},
	{4, 2, "churned", "paid", "churned"},
	{2, 0, "trial", nil, "trial"},
	{nil, 0, "free", nil, "free"},
	{5, 2, "paid", nil, "paid"},
	{3, 1, "paid", "free", "paid"},
	{7, 1, "free", "trial", "free"},
	{1, 1, "free", "trial", "free"},
	{9, 0, "free", nil, "free"},
	{4, 0, "free", nil, "free"},
	{6, 1, "free", "paid", "free"},
	{11, 0, "paid", nil, "paid"},
	{2, 2, "paid", "paid", "paid"},
	{8, 0, "paid", nil, "paid"},
	{3, 0, "free", nil, "free"},
	{1, 2, "paid", "free", "paid"},
	{5, 0, "free", nil, "free"},
	{4, 1, "paid", "free", "paid"},
	{7, 2, "free", "free", "free"},
	{6, 2, "paid", "free", "paid"},
	{9, 1, "paid", "free", "paid"},
	{11, 3, "paid", "free", "paid"},
}

// flowsLongAndWide writes flowsPanel as a long panel (flows_long.pulse:
// id, customer, at, plan) and as its wide twin (flows_wide.pulse: id,
// to_plan, from_plan — one row per long record, its by-hand pair). The
// wide file is written in reverse with to_plan first, so its dictionary
// order differs from the long one's.
func flowsLongAndWide(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC).Unix()
	write := func(name string, schema encoding.Schema, rows []pulse.CohortRow) {
		bld, err := p.NewCohortBuilder(context.Background(), name, schema, pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range rows {
			if err := bld.Append(r); err != nil {
				t.Fatalf("%s row %d: %v", name, i, err)
			}
		}
		if _, err := bld.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var long, wide []pulse.CohortRow
	for i, r := range flowsPanel {
		var subject, at any
		if r.subject != nil {
			subject = uint64(r.subject.(int))
		}
		if r.hour != nil {
			at = base + int64(r.hour.(int))*3600
		}
		long = append(long, pulse.CohortRow{uint64(i + 1), subject, at, r.state})
	}
	for i := len(flowsPanel) - 1; i >= 0; i-- {
		wide = append(wide, pulse.CohortRow{uint64(i + 1), flowsPanel[i].to, flowsPanel[i].from})
	}
	write("flows_long.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Panel row identifier"},
		{Name: "customer", Type: encoding.FieldTypeU64, Nullable: true, Description: "Customer the panel row belongs to"},
		{Name: "at", Type: encoding.FieldTypeDateTime, Nullable: true, Description: "Instant the plan was observed"},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan observed then"},
	}}, long)
	write("flows_wide.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Panel row identifier of the move"},
		{Name: "to_plan", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan after the move"},
		{Name: "from_plan", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan before the move"},
	}}, wide)
	return p, fs
}

// flowsRequests is the long-panel request and its wide twin: the same
// from × to crosstab (counted on id, margins on) and overlays.
func flowsRequests(overlays []types.OverlaySpec) (long, wide *types.Request) {
	mk := func(cohort, from, to string, attrs []*types.Attribute) *types.Request {
		return &types.Request{
			Cohort:     &types.Cohort{Filename: cohort},
			Attributes: attrs,
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: from}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: to}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "n"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Overlays: overlays,
		}
	}
	attrs := []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "customer", OrderField: "at", Label: "prev_plan"}}
	return mk("flows_long.pulse", "prev_plan", "plan", attrs), mk("flows_wide.pulse", "from_plan", "to_plan", nil)
}

// flowsOverlay is an overlay spec with optional JSON params.
func flowsOverlay(name string, kind types.OverlayKind, scope types.OverlayScope, params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: name, Kind: kind, Scope: scope}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// flowsMatrix is resp's crosstab matrix as JSON, minus the axis
// headers (they name each form's own fields).
func flowsMatrix(t *testing.T, resp *types.Response) string {
	t.Helper()
	return flowsNoHeaders(t, resp.Crosstab.Matrix)
}

// flowsNoHeaders is v as JSON with every matrix's row_header /
// column_header dropped at any depth — the one place the long and wide
// forms may differ (each names its own axis fields).
func flowsNoHeaders(t *testing.T, v any) string {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(flowsJSON(t, v)), &doc); err != nil {
		t.Fatal(err)
	}
	var strip func(any)
	strip = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			delete(x, "row_header")
			delete(x, "column_header")
			for _, c := range x {
				strip(c)
			}
		case []any:
			for _, c := range x {
				strip(c)
			}
		}
	}
	strip(doc)
	return flowsJSON(t, doc)
}

// flowsJSON marshals v for comparison.
func flowsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPreviousState_FlowsCrosstabMatchesWideTwin: over a datetime-
// ordered long panel with ties, a gap, a null key, a null order and
// first-only / last-only states, the previous-state × state crosstab
// equals the wide twin's from × to crosstab cell for cell, label for
// label, margins included, and both equal the by-hand table.
func TestPreviousState_FlowsCrosstabMatchesWideTwin(t *testing.T) {
	p, _ := flowsLongAndWide(t)
	ctx := context.Background()
	lreq, wreq := flowsRequests(nil)
	long, err := p.Process(ctx, lreq)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := p.Process(ctx, wreq)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flowsMatrix(t, long), flowsMatrix(t, wide); got != want {
		t.Fatalf("matrices differ:\nlong %s\nwide %s", got, want)
	}
	m := long.Crosstab.Matrix
	if want := []types.AxisKey{{"free"}, {"paid"}, {"trial"}}; !reflect.DeepEqual(m.RowKeys, want) {
		t.Errorf("row keys %v, want %v (churned last-only: never a row)", m.RowKeys, want)
	}
	// The first-only trial stays a (move-less) column: the column axis
	// keys every record, its null-previous first records included.
	if want := []types.AxisKey{{"churned"}, {"free"}, {"paid"}, {"trial"}}; !reflect.DeepEqual(m.ColumnKeys, want) {
		t.Errorf("column keys %v, want %v", m.ColumnKeys, want)
	}
	// By hand from flowsPanel's pairs: free → {churned 0, free 2,
	// paid 7, trial 0}, paid → {3, 2, 2, 0}, trial → {0, 2, 1, 0}.
	want := [][]float64{{0, 2, 7, 0}, {3, 2, 2, 0}, {0, 2, 1, 0}}
	got := make([][]float64, len(m.Cells))
	for i, row := range m.Cells {
		got[i] = make([]float64, len(row))
		for j, c := range row {
			if c.Present {
				if err := json.Unmarshal([]byte(flowsJSON(t, c.Value)), &got[i][j]); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cells %v, want the by-hand %v", got, want)
	}
}

// TestPreviousState_FlowsOverlaysMatchWideTwin: OVERLAY_MARKOV,
// OVERLAY_SYMMETRY and OVERLAY_RETENTION over the long panel's
// previous-state × state crosstab — no pre-pivot — answer exactly as
// over the wide twin, payloads, summaries and warnings alike (only the
// matrices' axis headers name different fields). The
// last-only state is a column-only key: Markov runs with it in
// params.absorbing (the unit row is synthesized) and, without it,
// refuses PULSE_OVERLAY_MATRIX_NOT_SQUARE alike on both forms.
func TestPreviousState_FlowsOverlaysMatchWideTwin(t *testing.T) {
	p, _ := flowsLongAndWide(t)
	ctx := context.Background()
	markov := func(params string) types.OverlaySpec {
		return flowsOverlay("flow", types.OverlayKindMarkov, types.OverlayScopeMatrix, params)
	}
	symmetry := flowsOverlay("shift", types.OverlayKindSymmetry, types.OverlayScopeMatrix, "")
	retention := flowsOverlay("retention", types.OverlayKindRetention, types.OverlayScopeRow, `{"absorbing":["churned"]}`)

	t.Run("markov_symmetry_retention", func(t *testing.T) {
		overlays := []types.OverlaySpec{markov(`{"steps":[1,2],"absorbing":["churned"],"initial":"margin"}`), symmetry, retention}
		lreq, wreq := flowsRequests(overlays)
		long, err := p.Process(ctx, lreq)
		if err != nil {
			t.Fatal(err)
		}
		wide, err := p.Process(ctx, wreq)
		if err != nil {
			t.Fatal(err)
		}
		if len(long.Overlays) != len(overlays) {
			t.Fatalf("%d layers for %d specs", len(long.Overlays), len(overlays))
		}
		for i := range long.Overlays {
			if !overlayLayerHasFigure(&long.Overlays[i]) {
				t.Errorf("layer %q carries no figure: %+v", long.Overlays[i].Name, long.Overlays[i].Warnings)
			}
		}
		if got, want := flowsNoHeaders(t, long.Overlays), flowsNoHeaders(t, wide.Overlays); got != want {
			t.Errorf("overlays differ:\nlong %s\nwide %s", got, want)
		}
	})

	t.Run("markov_column_only_state_not_absorbing", func(t *testing.T) {
		lreq, wreq := flowsRequests([]types.OverlaySpec{markov("")})
		_, lerr := p.Process(ctx, lreq)
		_, werr := p.Process(ctx, wreq)
		var lce, wce *errors.CodedError
		if !stderrors.As(lerr, &lce) || !stderrors.As(werr, &wce) {
			t.Fatalf("want coded refusals, got long %v wide %v", lerr, werr)
		}
		if lce.Code != errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE {
			t.Errorf("long code %s, want PULSE_OVERLAY_MATRIX_NOT_SQUARE", lce.Code)
		}
		if got, want := flowsJSON(t, []any{lce.Code, lce.Message, lce.Details}), flowsJSON(t, []any{wce.Code, wce.Message, wce.Details}); got != want {
			t.Errorf("refusals differ:\nlong %s\nwide %s", got, want)
		}
	})
}

// TestExamples_PreviousStateFlowsMatchWideTwin: the long-panel flows
// example (overlays/55) answers exactly as the same crosstab and
// overlays over plan_history's wide twin (plan_history_wide: one row
// per move, plan_before × plan_after) — cells, labels, margins and
// every overlay figure — so the example's numbers are the wide form's,
// with no pre-pivot.
func TestExamples_PreviousStateFlowsMatchWideTwin(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, p, fs, tmp)
	body, err := os.ReadFile(filepath.Join("internal", "examples", "overlays", "55_crosstab_previous_state_flows.json"))
	if err != nil {
		t.Fatal(err)
	}
	var long, wide pulse.Request
	if err := json.Unmarshal(body, &long); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &wide); err != nil {
		t.Fatal(err)
	}
	long.Cohort.DataDir = tmp
	wide.Cohort = &types.Cohort{Filename: "plan_history_wide.pulse", DataDir: tmp}
	wide.Attributes = nil
	wide.Crosstab.Rows[0].Field = "plan_before"
	wide.Crosstab.Columns[0].Field = "plan_after"

	ctx := context.Background()
	lr, err := p.Process(ctx, &long)
	if err != nil {
		t.Fatal(err)
	}
	wr, err := p.Process(ctx, &wide)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flowsMatrix(t, lr), flowsMatrix(t, wr); got != want {
		t.Errorf("crosstabs differ:\nlong %s\nwide %s", got, want)
	}
	if len(lr.Overlays) != 3 {
		t.Fatalf("%d overlay layers, want markov, symmetry and retention", len(lr.Overlays))
	}
	if got, want := flowsNoHeaders(t, lr.Overlays), flowsNoHeaders(t, wr.Overlays); got != want {
		t.Errorf("overlays differ:\nlong %s\nwide %s", got, want)
	}
	m := lr.Crosstab.Matrix
	if want := []types.AxisKey{{"basic"}, {"plus"}, {"pro"}}; !reflect.DeepEqual(m.RowKeys, want) || !reflect.DeepEqual(m.ColumnKeys, want) {
		t.Errorf("axes %v x %v, want plan names %v on both", m.RowKeys, m.ColumnKeys, want)
	}
}
