package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_RETENTION carries no weight class: every rate is a ratio of
// host cells, so a weighted host's Σw table is read as is under either
// weight kind (.claude/reference/weighting.md, classification by
// family). Pinned here against the rep()-expanded oracle: integer
// frequency weights answer byte-identically to the physically
// duplicated rows run unweighted, kind probability answers like kind
// frequency (no Kish scaling), and the weighted layer differs from the
// unweighted one over the same rows (the weight reached it).

func retentionWeightSchema() *encoding.Schema {
	states := func() *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, s := range []string{"gold", "silver", "churned"} {
			if _, err := d.Add(s); err != nil {
				panic(err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "from", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: states()},
		{Name: "to", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: states()},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
	}}
}

// retentionWeightRows: (from, to, w) over gold = 0, silver = 1,
// churned = 2. The Σw table is gold {gold 4, silver 2, churned 2},
// silver {gold 3, silver 3, churned 1}, churned {churned 1}.
var retentionWeightRows = [][3]uint64{
	{0, 0, 3}, {0, 0, 1}, {0, 1, 2}, {0, 2, 0}, {0, 2, 2},
	{1, 1, 1}, {1, 0, 3}, {1, 2, 1}, {1, 1, 2},
	{2, 2, 1},
}

func retentionWeightLayer(t *testing.T, expand bool, w *types.WeightSpec) types.OverlayLayer {
	t.Helper()
	var recs [][]uint64
	for _, r := range retentionWeightRows {
		if !expand {
			recs = append(recs, []uint64{r[0], r[1], math.Float64bits(float64(r[2]))})
			continue
		}
		for k := uint64(0); k < r[2]; k++ {
			recs = append(recs, []uint64{r[0], r[1], math.Float64bits(1)})
		}
	}
	cfg := setupTestFS(t, "tiers.pulse", retentionWeightSchema(), recs)
	resp, err := New(cfg).Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: "tiers.pulse"},
		Weight: w,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "from"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "to"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "w", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
		},
		Overlays: []types.OverlaySpec{{Name: "keep", Kind: types.OverlayKindRetention, Scope: types.OverlayScopeRow,
			Params: json.RawMessage(`{"absorbing":["churned"]}`)}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("Response.Overlays = %d layers, want 1", len(resp.Overlays))
	}
	return resp.Overlays[0]
}

func TestRetention_FrequencyWeightEqualsExpandedRows(t *testing.T) {
	wire := func(l types.OverlayLayer) string {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	freq := retentionWeightLayer(t, false, &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	expanded := retentionWeightLayer(t, true, nil)
	if wire(freq) != wire(expanded) {
		t.Fatalf("frequency-weighted layer\n%s\nwant the expanded rows'\n%s", wire(freq), wire(expanded))
	}
	prob := retentionWeightLayer(t, false, &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	if wire(prob) != wire(freq) {
		t.Fatalf("probability-weighted layer\n%s\nwant the frequency layer (no Kish scaling)\n%s", wire(prob), wire(freq))
	}
	if raw := retentionWeightLayer(t, false, nil); wire(raw) == wire(freq) {
		t.Fatal("the weighted layer equals the unweighted one: the weight never reached it")
	}

	// Hand check on the Σw table: gold 4 / 8 stay, 2 / 8 switch, 2 / 8
	// churn; silver 3 / 7, 3 / 7, 1 / 7; churned is absorbing, so not
	// reported.
	es := freq.Payload.Series.Entries
	if len(es) != 2 {
		t.Fatalf("entries %+v", es)
	}
	for i, want := range [][4]float64{{0.5, 0.25, 0.25, 8}, {3.0 / 7, 3.0 / 7, 1.0 / 7, 7}} {
		s := es[i].Summary
		if *s.Statistic != want[0] || s.Parameters["switch"] != want[1] || s.Parameters["churn"] != want[2] || s.Parameters["from_total"] != want[3] {
			t.Errorf("entry %v: %v %v, want %v", es[i].Key, *s.Statistic, s.Parameters, want)
		}
	}
}
