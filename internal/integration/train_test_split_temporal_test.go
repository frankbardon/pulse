package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestTrainTestSplit_TemporalEndToEnd runs a temporal split through
// Process on a stored date column the rest of the request never names
// (so projected decode must keep it): rows i = 0..19 fall on day
// 19000 + i/2 (pairs share a day), rows 3 and 10 have no day. The 18
// placeable rows cut at round(18·0.6) = 11 and 11 + round(18·0.2) = 15;
// both cuts land mid-pair and snap to the earlier day edge (10 and 14),
// so train is every day up to 19005 (rows 0-2, 4-9, 11), val days 19006-
// 19007 (rows 12-15), test days 19008-19009 (rows 16-19), and rows 3 and
// 10 are in no partition. Identical across decode worker counts.
func TestTrainTestSplit_TemporalEndToEnd(t *testing.T) {
	t.Parallel()

	for _, workers := range []int{1, 4} {
		fsys := afero.NewMemMapFs()
		p, err := pulse.New(pulse.Options{FS: fsys, DecodeWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		schema := encoding.Schema{Fields: []encoding.Field{
			{Name: "day", Type: encoding.FieldTypeDate, Nullable: true, Description: "Calendar day the order was placed"},
			{Name: "amt", Type: encoding.FieldTypeF64, Description: "Row index stored as the order amount"},
		}}
		b, err := p.NewCohortBuilder(context.Background(), "orders.pulse", schema, pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range 20 {
			var day any = int32(19000 + i/2)
			if i == 3 || i == 10 {
				day = nil
			}
			if err := b.Append(pulse.CohortRow{day, float64(i)}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}

		for _, c := range []struct {
			split      string
			n, sumRows float64
		}{
			{"0", 10, 53},
			{"1", 4, 54},
			{"2", 4, 70},
		} {
			var req types.Request
			body := `{
				"features": [{"type": "FEAT_TRAIN_TEST_SPLIT", "params": {"ratios": [0.6, 0.2, 0.2], "mode": "temporal", "order_by": "day"}}],
				"filterers": [{"type": "FILTER_EXPRESSION", "expression": "split == ` + c.split + `"}],
				"aggregations": [
					{"type": "AGG_COUNT", "field": "amt", "label": "n"},
					{"type": "AGG_SUM", "field": "amt", "label": "rows"}
				]
			}`
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatal(err)
			}
			req.Cohort = &types.Cohort{Filename: "orders.pulse"}
			resp, err := p.Process(context.Background(), &req)
			if err != nil {
				t.Fatalf("workers %d split %s: %v", workers, c.split, err)
			}
			if len(resp.Data) != 1 {
				t.Fatalf("workers %d split %s: %d rows", workers, c.split, len(resp.Data))
			}
			row := resp.Data[0]
			if row["n"] != c.n || row["rows"] != c.sumRows {
				t.Errorf("workers %d split %s: n=%v sum=%v, want n=%v sum=%v", workers, c.split, row["n"], row["rows"], c.n, c.sumRows)
			}
		}
	}
}
