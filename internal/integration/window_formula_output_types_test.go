package integration_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// window_formula_output_types_test.go pins WIN_FORMULA's output-row
// typing (E6-S4): over the grouped or aggregated output rows each
// built-in group key and aggregation label is declared as the cell the
// engine writes (winformula.OutputColumns) — a key is a label string, a
// set union a label list, a set frequency a map, AGG_WELFORD its moment
// triple, a decimal figure a float64 — on predict's walk and the
// engine's factory alike, so arithmetic on a non-number is refused
// before any row, and a number reads as before.

const wfOutRecords = 36

// wfOutCohort writes out.pulse: a number x, a categorical region, a set
// tags and a decimal amt (scale 2).
func wfOutCohort(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Balance outstanding on the account"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region the account is booked in"},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Description: "Product lines the account holds today"},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Description: "Fee charged on the account this month"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "out.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range wfOutRecords {
		tags := []string{"card"}
		if i%2 == 0 {
			tags = append(tags, "loan")
		}
		amt := encoding.NewDecimal128FromInt(int64(125 * (i%4 + 1))) // 1.25 .. 5.00
		if err := b.Append(pulse.CohortRow{float64(i%9) + 0.5, wfRegions[i%3], tags, amt}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// wfOutAggs are the aggregation slots every grouped case carries.
func wfOutAggs() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		{Type: types.AGG_SET_UNION, Field: "tags", Label: "u"},
		{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "fr"},
		{Type: types.AGG_WELFORD, Field: "x", Label: "w"},
		{Type: types.AGG_SUM, Field: "amt", Label: "fees"},
		{Type: types.AGG_CONSTANT, Label: "tier", Params: []byte(`{"type": "categorical_u8", "value": "gold"}`)},
	}
}

// TestWinFormula_NonNumberOutputCellRefusedAtPredict: arithmetic on a
// group key or a non-number aggregation over the output rows — one
// grouper, two groupers, the single aggregated row — is the predict
// envelope's first error, and Process and ProcessStream raise exactly it
// (code, message, details), before any row. A crosstab runs its windows
// over the record rows only (windows-first); there the key column is a
// record column, typed the same way.
func TestWinFormula_NonNumberOutputCellRefusedAtPredict(t *testing.T) {
	t.Parallel()
	p, fsys := wfOutCohort(t)
	ctx := context.Background()
	shapes := map[string]func(src string) *types.Request{
		"one_grouper": func(src string) *types.Request {
			return &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Aggregations: wfOutAggs(),
				Windows: winFormulaWindows(fmt.Sprintf(`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": %q}}]`, src))}
		},
		"two_groupers": func(src string) *types.Request {
			return &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_RANGE, Field: "x", Interval: 3}},
				Aggregations: wfOutAggs(),
				Windows:      winFormulaWindows(fmt.Sprintf(`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": %q}}]`, src))}
		},
		"aggregated_row": func(src string) *types.Request {
			return &types.Request{Aggregations: wfOutAggs(),
				Windows: winFormulaWindows(fmt.Sprintf(`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": %q}}]`, src))}
		},
	}
	cases := map[string][]string{
		"one_grouper":    {"region * 2", "n + region", "u * 2", "fr * n", "w * 2", "tier + 1", "region"},
		"two_groupers":   {"x * 2", "region + x"},
		"aggregated_row": {"u * n", "w - 1", "fr / 2", "tier * n"},
	}
	for shape, srcs := range cases {
		for _, src := range srcs {
			t.Run(shape+"/"+src, func(t *testing.T) {
				req := func() *types.Request {
					r := shapes[shape](src)
					r.Cohort = &types.Cohort{Filename: "out.pulse"}
					return r
				}
				env := predictEnvelope(t, p, fsys, "out.pulse", req())
				if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_FORMULA_COMPILE_ERROR) {
					t.Fatalf("predict errors = %+v, want PULSE_FORMULA_COMPILE_ERROR first", env.Errors)
				}
				_, perr := p.Process(ctx, req())
				sameEntry(t, env, perr)
				_, serr := p.ProcessStream(ctx, req())
				sameEntry(t, env, serr)
			})
		}
	}
	t.Run("crosstab_windows_first", func(t *testing.T) {
		req := func() *types.Request {
			return &types.Request{
				Cohort:  &types.Cohort{Filename: "out.pulse"},
				Windows: winFormulaWindows(`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": "region * 2"}}]`),
				Crosstab: &types.CrosstabSpec{
					Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "f"},
				},
			}
		}
		env := predictEnvelope(t, p, fsys, "out.pulse", req())
		if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_FORMULA_COMPILE_ERROR) {
			t.Fatalf("predict errors = %+v, want PULSE_FORMULA_COMPILE_ERROR first", env.Errors)
		}
		_, perr := p.Process(ctx, req())
		sameEntry(t, env, perr)
	})
}

// TestWinFormula_OutputCellReads: typed output cells still read where
// the expression does no arithmetic on a non-number — a key comparison,
// a label list's length, a map lookup, the Welford mean — and a decimal
// aggregate (and a decimal record column) reads as its float64 value;
// predict accepts what the run computes, Process and ProcessStream
// alike.
func TestWinFormula_OutputCellReads(t *testing.T) {
	t.Parallel()
	p, fsys := wfOutCohort(t)
	ctx := context.Background()
	grouped := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "out.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: wfOutAggs(),
			Windows: winFormulaWindows(`[
				{"type": "WIN_FORMULA", "label": "north_n", "params": {"expression": "region == \"north\" ? n : 0"}},
				{"type": "WIN_FORMULA", "label": "lines", "params": {"expression": "len(u) * 1.0"}},
				{"type": "WIN_FORMULA", "label": "loans", "params": {"expression": "fr[\"loan\"] / n"}},
				{"type": "WIN_FORMULA", "label": "mean2", "params": {"expression": "w.Mean * 2"}},
				{"type": "WIN_FORMULA", "label": "fees2", "params": {"expression": "fees * 2"}},
				{"type": "WIN_FORMULA", "label": "gold", "params": {"expression": "tier == \"gold\" ? 1 : 0"}}
			]`),
		}
	}
	if env := predictEnvelope(t, p, fsys, "out.pulse", grouped()); len(env.Errors) != 0 {
		t.Fatalf("predict refused: %s %s %v", env.Errors[0].Code, env.Errors[0].Message, env.Errors[0].Details)
	}
	resp, err := p.Process(ctx, grouped())
	if err != nil {
		t.Fatal(err)
	}
	stream := wfStreamRows(ctx, t, p, grouped())
	if len(resp.Data) != len(wfRegions) || len(stream) != len(resp.Data) {
		t.Fatalf("rows: Process %d, ProcessStream %d, want %d", len(resp.Data), len(stream), len(wfRegions))
	}
	for i, row := range resp.Data {
		region, _ := row["region"].(string)
		n := cellNumber(t, row, "n")
		wantNorth := 0.0
		if region == "north" {
			wantNorth = n
		}
		checks := map[string]float64{"north_n": wantNorth, "lines": 2, "loans": 0.5, "gold": 1}
		for k, want := range checks {
			if got := cellNumber(t, row, k); got != want {
				t.Errorf("region %s: %s = %v, want %v", region, k, got, want)
			}
		}
		if got := cellNumber(t, row, "mean2"); math.IsNaN(got) || got <= 0 {
			t.Errorf("region %s: mean2 = %v, want twice a positive mean", region, got)
		}
		// Each region holds 12 rows, three of each fee 1.25..5.00.
		if got := cellNumber(t, row, "fees2"); got != 2*3*(1.25+2.5+3.75+5) {
			t.Errorf("region %s: fees2 = %v, want %v", region, got, 2*3*(1.25+2.5+3.75+5))
		}
		for _, k := range []string{"north_n", "lines", "loans", "mean2", "fees2", "gold"} {
			if stream[i][k] != row[k] {
				t.Errorf("region %s: %s ProcessStream %v, Process %v", region, k, stream[i][k], row[k])
			}
		}
	}

	// A decimal record column reads as its float64 value over the
	// record rows too.
	records := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "out.pulse"},
			Windows: winFormulaWindows(`[{"type": "WIN_FORMULA", "label": "amt2", "params": {"expression": "amt * 2"}}]`)}
	}
	if env := predictEnvelope(t, p, fsys, "out.pulse", records()); len(env.Errors) != 0 {
		t.Fatalf("predict refused: %s %s %v", env.Errors[0].Code, env.Errors[0].Message, env.Errors[0].Details)
	}
	rr, err := p.Process(ctx, records())
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rr.Data {
		if got, want := cellNumber(t, row, "amt2"), 2*1.25*float64(i%4+1); got != want {
			t.Errorf("row %d: amt2 = %v, want %v", i, got, want)
		}
	}
}

// wfStreamRows drains ProcessStream over req.
func wfStreamRows(ctx context.Context, t *testing.T, p *pulse.Pulse, req *types.Request) []map[string]any {
	t.Helper()
	it, err := p.ProcessStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = it.Close() }()
	var out []map[string]any
	for {
		row, ok, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return out
		}
		out = append(out, row)
	}
}
