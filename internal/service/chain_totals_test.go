package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// chainTotalsPrior is a previous chain stage: grouped by g and h
// (forwarded as categorical keys), summing x into sx and counting into
// n. Its input schema and its rows (non-dyadic, mixed-magnitude sx,
// every 13th one undefined — NaN, read as null).
func chainTotalsPrior(rows int) (*types.Request, *encoding.Schema, []map[string]any) {
	prior := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "h"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "sx"},
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		},
	}
	in := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
	data := make([]map[string]any, rows)
	for i := range data {
		sx := float64(i%97)/3 + float64(i%7)*1e3 + 0.1
		if i%13 == 5 {
			sx = math.NaN()
		}
		data[i] = map[string]any{"g": fmt.Sprintf("g%d", i%5), "h": fmt.Sprintf("h%d", i), "sx": sx, "n": float64(1 + i%4)}
	}
	return prior, in, data
}

// chainTotalsStages are total()-bearing later-stage requests over the
// prior stage's rows: ungrouped, grouped (filtered, a frequency-weighted
// total, a grouper over the share's label) and three pre-pass layers
// grouped.
func chainTotalsStages() map[string]*types.Request {
	f := func(label, expr string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
	}
	nw := types.SlotWeightOf(types.WeightSpec{Field: "n", Kind: types.WeightKindFrequency})
	wsh := f("wsh", "(sx ?? 0) * 1000 / total(sx)")
	wsh.Weight = nw
	t1 := f("t1", "d == nil ? nil : d / total(d)")
	t1.Weight = nw
	return map[string]*types.Request{
		"ungrouped": {
			Attributes: []*types.Attribute{f("sh", "sx == nil ? nil : sx / total(sx)")},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "sh", Label: "s"},
				{Type: types.AGG_MAX, Field: "sh", Label: "m"},
				{Type: types.AGG_COUNT, Field: "sh", Label: "c"},
			},
		},
		"grouped": {
			Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "n > 1"}},
			Attributes: []*types.Attribute{f("sh", "sx == nil ? nil : sx / total(sx)"), wsh},
			Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_ROUNDED, Field: "wsh", Interval: 0.25}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "sh", Label: "s"},
				{Type: types.AGG_AVERAGE, Field: "wsh", Label: "a"},
				{Type: types.AGG_COUNT, Field: "sh", Label: "c"},
			},
		},
		"multilayer_grouped": {
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "n > 1"}},
			Attributes: []*types.Attribute{
				f("t0", "sx == nil ? nil : sx / total(sx)"),
				f("d", "t0 == nil ? nil : t0 * 1000 - n / 7"),
				t1,
				f("t2", "(t1 ?? 0) * 1000 + total((t1 ?? 0) - n / 11)"),
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "t0", Label: "s0"},
				{Type: types.AGG_SUM, Field: "t1", Label: "s1"},
				{Type: types.AGG_SUM, Field: "t2", Label: "s2"},
				{Type: types.AGG_AVERAGE, Field: "t2", Label: "a2"},
			},
		},
	}
}

// chainTotalsRows is the prior stage's rows over a fresh synthesised
// schema (each iterator owns its dictionaries).
func chainTotalsRows(t *testing.T, svc *Service, prior *types.Request, in *encoding.Schema, data []map[string]any) (*encoding.Schema, *processing.ChainRowIterator) {
	t.Helper()
	schema, err := processing.ChainOutputSchema(prior, in, svc.extensions.AggregationKinds())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := processing.NewChainRowIterator(data, schema)
	if err != nil {
		t.Fatal(err)
	}
	return schema, rows
}

// TestChainStageTotals_MatchesSerialProcessor (U74 E3-S3, #494): a
// later ProcessChain stage carrying total() takes the two-phase runtime
// (chainStageTwoPhase) and answers the serial processor's whole
// response, byte for byte — ungrouped (the serial arm streams two-pass),
// grouped and three-layer grouped (the serial arm buffers every row).
func TestChainStageTotals_MatchesSerialProcessor(t *testing.T) {
	t.Parallel()

	cfg, err := fs.New(fs.WithFs(afero.NewMemMapFs()))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	prior, in, data := chainTotalsPrior(600)
	ctx := context.Background()
	for name, stage := range chainTotalsStages() {
		t.Run(name, func(t *testing.T) {
			schema, rows := chainTotalsRows(t, svc, prior, in, data)
			if !chainStageTwoPhase(stage, rows) {
				t.Fatal("stage does not take the two-phase runtime")
			}
			got, err := svc.runChainStageTotals(ctx, stage, nil, schema, rows)
			if err != nil {
				t.Fatalf("two-phase: %v", err)
			}
			schema, rows = chainTotalsRows(t, svc, prior, in, data)
			want, err := svc.runChainStage(ctx, stage, nil, schema, rows)
			if err != nil {
				t.Fatalf("serial: %v", err)
			}
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) {
				t.Fatalf("two-phase stage differs from the serial processor:\n got %s\nwant %s", gb, wb)
			}
			if (name == "grouped" && len(got.Data) <= 5) || (name == "multilayer_grouped" && len(got.Data) != 5) {
				t.Fatalf("%d rows: the grouped stage does not split", len(got.Data))
			}
			if got.Components == nil || got.Components.Run == nil || got.Components.Run.TotalRecords != int64(len(data)) {
				t.Fatalf("run components = %+v, want total_records %d", got.Components, len(data))
			}
		})
	}
}

// TestChainStageTwoPhase_Admission: only a later stage carrying a
// total() formula over a non-empty input takes the two-phase runtime;
// every other stage keeps the serial processor, byte-identical.
func TestChainStageTwoPhase_Admission(t *testing.T) {
	t.Parallel()

	cfg, err := fs.New(fs.WithFs(afero.NewMemMapFs()))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	prior, in, data := chainTotalsPrior(10)
	_, rows := chainTotalsRows(t, svc, prior, in, data)
	_, empty := chainTotalsRows(t, svc, prior, in, nil)
	total := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "sx / total(sx)", Label: "sh"}}}
	plain := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "sx * 2", Label: "d"}}}
	for _, tc := range []struct {
		name string
		req  *types.Request
		rows *processing.ChainRowIterator
		want bool
	}{
		{"total", total, rows, true},
		{"plain formula", plain, rows, false},
		{"empty input", total, empty, false},
		{"nil request", nil, rows, false},
	} {
		if got := chainStageTwoPhase(tc.req, tc.rows); got != tc.want {
			t.Errorf("%s: chainStageTwoPhase = %v, want %v", tc.name, got, tc.want)
		}
	}
}
