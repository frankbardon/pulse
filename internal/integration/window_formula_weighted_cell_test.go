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

// An AGG_SET_FREQUENCY cell is a per-label map whose values are integer
// counts unweighted and Σw floats under a weight (E6-S2): a WIN_FORMULA
// over the output rows types it by the slot's RESOLVED weight — its own
// `weight`, the request weight, then Options.DefaultWeight
// (weighting.Stamp) — on predict's walk and the engine's factory alike
// (winformula.OutputColumns), so an integer-only use of a weighted cell
// is refused before any row instead of failing the run.

// wfWeightedCohort writes wfw.pulse (region, a tags set, a frequency
// weight wt) and returns an unweighted instance and one whose
// Options.DefaultWeight is wt, both over it.
func wfWeightedCohort(t *testing.T) (plain, weighted *pulse.Pulse, fsys afero.Fs) {
	t.Helper()
	fsys = afero.NewMemMapFs()
	plain, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	weighted, err = pulse.New(pulse.Options{FS: fsys, DefaultWeight: &types.WeightSpec{Field: "wt", Kind: types.WeightKindFrequency}})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region the account is booked in"},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Description: "Product lines the account holds today"},
		{Name: "wt", Type: encoding.FieldTypeU8, Description: "Frequency weight of the account row"},
	}}
	b, err := plain.NewCohortBuilder(context.Background(), "wfw.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		tags := []string{"card"}
		if i%2 == 0 {
			tags = append(tags, "loan")
		}
		if err := b.Append(pulse.CohortRow{[]string{"north", "south"}[i%2], tags, uint64(i%3 + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return plain, weighted, fsys
}

// wfWeightedReq groups by region, counts tags as fr (under slotWeight)
// and runs one WIN_FORMULA over src.
func wfWeightedReq(src string, slotWeight types.SlotWeight, reqWeight *types.WeightSpec) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "wfw.pulse"},
		Weight:       reqWeight,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "fr", Weight: slotWeight}},
		Windows: []*types.Window{{Type: types.WIN_FORMULA, Label: "f",
			Params: []byte(fmt.Sprintf(`{"expression": %q}`, src))}},
	}
}

// TestWinFormula_SetFrequencyCellFollowsResolvedWeight: an integer-only
// use of the cell (a range bound) runs on the unweighted map and is one
// PULSE_FORMULA_COMPILE_ERROR — predict's first error, raised exactly
// by Process and ProcessStream before any row — under a slot, request
// or instance default weight; `weight: null` on the slot keeps the
// integer map under a default; a float read of the weighted map runs
// on both sides.
func TestWinFormula_SetFrequencyCellFollowsResolvedWeight(t *testing.T) {
	t.Parallel()
	plain, weighted, fsys := wfWeightedCohort(t)
	ctx := context.Background()
	const intOnly = `len(1..fr["card"]) * 1.0`
	wt := &types.WeightSpec{Field: "wt", Kind: types.WeightKindFrequency}

	refused := map[string]struct {
		p   *pulse.Pulse
		req func() *types.Request
	}{
		"slot_weight": {plain, func() *types.Request { return wfWeightedReq(intOnly, types.SlotWeightOf(*wt), nil) }},
		// A request weight beside a WIN_FORMULA is refused later anyway
		// (PROCESSING_CONFIG: a window has no weighted form); the typed
		// compile refusal is the first error on both sides.
		"request_weight": {plain, func() *types.Request { return wfWeightedReq(intOnly, types.SlotWeight{}, wt) }},
		"default_weight": {weighted, func() *types.Request { return wfWeightedReq(intOnly, types.SlotWeight{}, nil) }},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := predictEnvelope(t, c.p, fsys, "wfw.pulse", c.req())
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_FORMULA_COMPILE_ERROR) {
				t.Fatalf("predict errors = %+v, want PULSE_FORMULA_COMPILE_ERROR first", env.Errors)
			}
			_, perr := c.p.Process(ctx, c.req())
			sameEntry(t, env, perr)
			_, serr := c.p.ProcessStream(ctx, c.req())
			sameEntry(t, env, serr)
		})
	}

	accepted := map[string]struct {
		p    *pulse.Pulse
		req  func() *types.Request
		want float64 // f on the north row
	}{
		"unweighted":         {plain, func() *types.Request { return wfWeightedReq(intOnly, types.SlotWeight{}, nil) }, 6},
		"null_under_default": {weighted, func() *types.Request { return wfWeightedReq(intOnly, types.NullSlotWeight(), nil) }, 6},
		// north holds rows 0, 2, …, 10: weights 1, 3, 2, 1, 3, 2.
		"weighted_float_read": {weighted, func() *types.Request { return wfWeightedReq(`fr["card"] * 1.0`, types.SlotWeight{}, nil) }, 12},
	}
	for name, c := range accepted {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if env := predictEnvelope(t, c.p, fsys, "wfw.pulse", c.req()); len(env.Errors) != 0 {
				t.Fatalf("predict refused: %s %s", env.Errors[0].Code, env.Errors[0].Message)
			}
			resp, err := c.p.Process(ctx, c.req())
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			stream := wfStreamRows(ctx, t, c.p, c.req())
			if len(resp.Data) != 2 || len(stream) != 2 {
				t.Fatalf("rows: Process %d, ProcessStream %d, want 2", len(resp.Data), len(stream))
			}
			for i, row := range resp.Data {
				if row["region"] == "north" {
					if got := cellNumber(t, row, "f"); got != c.want {
						t.Errorf("north f = %v, want %v", got, c.want)
					}
				}
				if stream[i]["f"] != row["f"] {
					t.Errorf("row %d: ProcessStream f %v, Process %v", i, stream[i]["f"], row["f"])
				}
			}
		})
	}
}
