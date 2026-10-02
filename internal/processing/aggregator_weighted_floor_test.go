package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// AGG_WEIGHTED_MEAN universal floor {n, n_null} contract.
//
// The floor is a property of the VALUE field only: n counts rows whose
// `Field` is present, n_null counts rows whose `Field` is null. A row
// with a null or zero weight is skipped by the weighted recurrence (it
// adds nothing to sum_weights) but its present value still counts in
// n. The weight-valid count is therefore NOT n — callers size weighted
// statistics from sum_weights / n_eff.
//
// Every execution path probes the floor through FieldPresent(r, Field)
// (buffered aggregateWithComponents, streaming processStreaming, buffered
// crosstab runCellAggregation, fused crosstab UpdateRow), so the rule is
// uniform; this file pins it on all four so a single path drifting is
// caught.
//
// Fixture (value, weight):
//
//	(10, 1)    counted, weighted
//	(20, 2)    counted, weighted
//	(30, 0)    counted, weight==0 skipped by the recurrence
//	(40, null) counted, null weight skipped by the recurrence
//	(null, 1)  n_null
//
// → n = 4, n_null = 1, sum_weights = 3, weighted_mean = 50/3.
const (
	wfWantN          = 4
	wfWantNNull      = 1
	wfWantSumWeights = 3.0
)

func wfRows(mk func(value, weight float64, nulls map[string]bool) *Record) []*Record {
	return []*Record{
		mk(10, 1, nil),
		mk(20, 2, nil),
		mk(30, 0, nil),
		mk(40, 0, map[string]bool{"weight": true}),
		mk(0, 1, map[string]bool{"value": true}),
	}
}

func wfAggregation() *types.Aggregation {
	return &types.Aggregation{
		Type:   types.AGG_WEIGHTED_MEAN,
		Field:  "value",
		Label:  "wm",
		Params: json.RawMessage(`{"weight_field":"weight"}`),
	}
}

func wfAssertFloor(t *testing.T, path string, n, nNull int, op map[string]any) {
	t.Helper()
	if n != wfWantN {
		t.Errorf("%s: floor n = %d, want %d (value-present rows, null/zero-weight rows INCLUDED)", path, n, wfWantN)
	}
	if nNull != wfWantNNull {
		t.Errorf("%s: floor n_null = %d, want %d (null VALUE rows only)", path, nNull, wfWantNNull)
	}
	sw, ok := op["sum_weights"].(float64)
	if !ok {
		t.Fatalf("%s: sum_weights missing or not float64: %v", path, op["sum_weights"])
	}
	if sw != wfWantSumWeights {
		t.Errorf("%s: sum_weights = %v, want %v (null/zero-weight rows EXCLUDED)", path, sw, wfWantSumWeights)
	}
}

func TestWeightedMean_FloorCountsSkippedWeightRows_Process(t *testing.T) {
	schema := cohortSchema()
	recs := wfRows(func(v, w float64, nulls map[string]bool) *Record {
		return cohortRec(v, w, 0, 0, nulls)
	})
	req := func() *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{wfAggregation()}}
	}

	t.Run("buffered", func(t *testing.T) {
		resp, err := NewProcessor(schema).processRecords(context.Background(), req(), recs)
		if err != nil {
			t.Fatalf("processRecords: %v", err)
		}
		if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
			t.Fatalf("components shape wrong: %+v", resp.Components)
		}
		e := resp.Components.Aggregations[0]
		wfAssertFloor(t, "buffered", e.N, e.NNull, e.Operator)
	})

	t.Run("streaming", func(t *testing.T) {
		p := NewProcessor(schema)
		resp, err := p.Process(context.Background(), req(), NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if p.LastPath() != PathStreaming {
			t.Fatalf("expected streaming path, got %v", p.LastPath())
		}
		if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
			t.Fatalf("components shape wrong: %+v", resp.Components)
		}
		e := resp.Components.Aggregations[0]
		wfAssertFloor(t, "streaming", e.N, e.NNull, e.Operator)
	})
}

func TestWeightedMean_FloorCountsSkippedWeightRows_Crosstab(t *testing.T) {
	schema := crosstabWeightedOverlaySchema(t)
	// Every row lands in cell (r0, c0) so the cell floor, the row and
	// column margins and the grand total all see the same five rows.
	recs := wfRows(func(v, w float64, nulls map[string]bool) *Record {
		return NewRecordWithNulls(schema, map[string]float64{
			"row": 0, "col": 0, "value": v, "weight": w,
		}, nulls)
	})

	req := crosstabWeightedOverlayBaseRequest()
	if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
		t.Fatalf("fixture must be fusable, gate said: %s", reason)
	}

	paths := []struct {
		name string
		run  func() (*types.Response, error)
	}{
		{"buffered", func() (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, crosstabWeightedOverlayBaseRequest(), recs, false)
		}},
		{"fused", func() (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, crosstabWeightedOverlayBaseRequest(), recs, false)
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			resp, err := p.run()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if resp.Components == nil || resp.Components.Crosstab == nil {
				t.Fatalf("no crosstab components block")
			}
			cc := resp.Components.Crosstab
			if len(cc.CellComponents) == 0 || len(cc.CellComponents[0]) == 0 || cc.CellComponents[0][0] == nil {
				t.Fatalf("missing CellComponents[0][0]: %+v", cc.CellComponents)
			}
			check := func(where string, m map[string]any) {
				t.Helper()
				n, _ := m["n"].(int)
				nNull, _ := m["n_null"].(int)
				wfAssertFloor(t, p.name+" "+where, n, nNull, m)
			}
			check("cell", cc.CellComponents[0][0])
			if cc.GrandTotalComponents == nil {
				t.Fatalf("missing GrandTotalComponents")
			}
			check("grand", cc.GrandTotalComponents)
		})
	}
}
