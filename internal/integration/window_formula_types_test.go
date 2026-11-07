package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// window_formula_types_test.go pins WIN_FORMULA's record-column typing
// (E6-S3): over the record rows a non-number schema field — a
// categorical label, a set's labels — is declared as the value the row
// carries (winformula.Columns), on predict's walk and the engine's
// factory alike, so arithmetic on it is refused before any row.

const wfTypedRecords = 40

var wfRegions = []string{"north", "south", "east"}

// wfTypedCohort writes typed.pulse: a number x, a categorical region and
// a set tags.
func wfTypedCohort(t *testing.T) (*pulse.Pulse, afero.Fs) {
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
	}}
	b, err := p.NewCohortBuilder(context.Background(), "typed.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range wfTypedRecords {
		tags := []string{"card"}
		if i%2 == 0 {
			tags = append(tags, "loan")
		}
		if err := b.Append(pulse.CohortRow{float64(i%9) + 0.5, wfRegions[i%3], tags}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// TestWinFormula_NonNumberRecordColumnRefusedAtPredict: arithmetic on a
// categorical or set column over the record rows — windows alone, and
// windows-first beside an aggregation reading the formula — is the
// predict envelope's first error, and Process, ProcessStream and (over
// the records alone) Materialize raise exactly it (code, message,
// details), before any row.
func TestWinFormula_NonNumberRecordColumnRefusedAtPredict(t *testing.T) {
	t.Parallel()
	p, fsys := wfTypedCohort(t)
	ctx := context.Background()
	for i, src := range []string{"region * 2", "x + region", "tags * x", "len(region) + region"} {
		for _, first := range []bool{false, true} {
			req := func() *types.Request {
				r := &types.Request{
					Cohort:  &types.Cohort{Filename: "typed.pulse"},
					Windows: winFormulaWindows(fmt.Sprintf(`[{"type": "WIN_FORMULA", "label": "f", "params": {"expression": %q}}]`, src)),
				}
				if first {
					r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "sum_f"}}
				}
				return r
			}
			t.Run(fmt.Sprintf("%s/windows_first=%v", src, first), func(t *testing.T) {
				env := predictEnvelope(t, p, fsys, "typed.pulse", req())
				if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_FORMULA_COMPILE_ERROR) {
					t.Fatalf("predict errors = %+v, want PULSE_FORMULA_COMPILE_ERROR first", env.Errors)
				}
				_, perr := p.Process(ctx, req())
				sameEntry(t, env, perr)
				_, serr := p.ProcessStream(ctx, req())
				sameEntry(t, env, serr)
				if !first { // Materialize keeps no aggregation slot
					_, merr := p.Materialize(ctx, req(), fmt.Sprintf("wf_typed%d.pulse", i), pulse.MaterializeOptions{})
					sameEntry(t, env, merr)
				}
			})
		}
	}
}

// TestWinFormula_NonNumberRecordColumnReads: a typed categorical or set
// column still reads where the expression does not do arithmetic on it
// — a label comparison, a membership test, a length — and predict
// accepts what the run computes.
func TestWinFormula_NonNumberRecordColumnReads(t *testing.T) {
	t.Parallel()
	p, fsys := wfTypedCohort(t)
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "typed.pulse"},
			Windows: winFormulaWindows(`[
				{"type": "WIN_FORMULA", "label": "north_x", "params": {"expression": "region == \"north\" ? x : 0"}},
				{"type": "WIN_FORMULA", "label": "loan", "params": {"expression": "\"loan\" in tags ? 1 : 0"}},
				{"type": "WIN_FORMULA", "label": "rlen", "params": {"expression": "len(region) * x"}}
			]`),
		}
	}
	if env := predictEnvelope(t, p, fsys, "typed.pulse", req()); len(env.Errors) != 0 {
		t.Fatalf("predict refused: %+v", env.Errors)
	}
	resp, err := p.Process(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != wfTypedRecords {
		t.Fatalf("%d rows, want %d", len(resp.Data), wfTypedRecords)
	}
	for _, row := range resp.Data {
		x := cellNumber(t, row, "x")
		region, _ := row["region"].(string)
		wantNorth, wantLen := 0.0, float64(len(region))*x
		if region == "north" {
			wantNorth = x
		}
		if got := cellNumber(t, row, "north_x"); got != wantNorth {
			t.Errorf("region %s x %v: north_x = %v, want %v", region, x, got, wantNorth)
		}
		if got := cellNumber(t, row, "rlen"); got != wantLen {
			t.Errorf("region %s x %v: rlen = %v, want %v", region, x, got, wantLen)
		}
	}
}
