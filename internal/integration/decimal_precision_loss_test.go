package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// decimal_precision_loss_test.go (#804): a decimal aggregate that falls
// back to f64 raises PULSE_DECIMAL_PRECISION_LOSS — once per slot,
// details {label, aggregation, field, cells} — on every host that
// carries an aggregate: ungrouped, grouped (a window reading the cell
// included), crosstab cells and margins. Field "wide" is decimal128(38,
// 20): its variance's scale 2·20 exceeds 38 digits, so AGG_VARIANCE /
// AGG_STDDEV take the f64 pass on every non-empty figure; "amt"
// (decimal128(18, 2)) never falls back. "big" holds 9e36-sized integers
// whose decimal running sum overflows, so its AGG_AVERAGE falls back.

const (
	plFile    = "pl.pulse"
	plArchive = "pl_archive.pulse"
)

func plSchema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), Description: "Region of the order"},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), Description: "Segment of the order"},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Description: "Amount charged on the order"},
		{Name: "wide", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 20, Description: "Exchange rate at twenty decimal places"},
		{Name: "big", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 0, Description: "Notional amount in the smallest unit"},
	}}
}

// plCohorts writes the single file and its shard archive twin (same rows,
// shards of 7) onto one filesystem.
func plCohorts(t *testing.T) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	big, _, err := encoding.ParseDecimal128("9000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		opts pulse.CohortBuilderOptions
	}{{plFile, pulse.CohortBuilderOptions{}}, {plArchive, pulse.CohortBuilderOptions{Shards: &pulse.ShardSplit{MaxRecords: 7}}}} {
		b, err := p.NewCohortBuilder(context.Background(), c.path, plSchema(), c.opts)
		if err != nil {
			t.Fatal(err)
		}
		regions, segments := []string{"n", "s", "e"}, []string{"a", "b"}
		for i := 0; i < 30; i++ {
			wide, sc, err := encoding.ParseDecimal128("1." + []string{"25", "5", "75", "125"}[i%4])
			if err != nil {
				t.Fatal(err)
			}
			if wide, err = wide.Rescale(sc, 20); err != nil {
				t.Fatal(err)
			}
			row := pulse.CohortRow{regions[i%3], segments[(i/3)%2], encoding.NewDecimal128FromInt(int64(100 + i*25)), wide, big}
			if err := b.Append(row); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

// lossWarnings is the response's PULSE_DECIMAL_PRECISION_LOSS details.
func lossWarnings(resp *types.Response) []map[string]any {
	var out []map[string]any
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_DECIMAL_PRECISION_LOSS) {
			out = append(out, w.Details)
		}
	}
	return out
}

func TestDecimalPrecisionLoss_EveryHost(t *testing.T) {
	t.Parallel()

	fs := plCohorts(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	cohort := &types.Cohort{Filename: plFile}
	agg := func(typ types.AggregationType, field, label string) *types.Aggregation {
		return &types.Aggregation{Type: typ, Field: field, Label: label}
	}
	for _, tc := range []struct {
		name string
		req  *types.Request
		want []map[string]any
	}{
		{"exact decimal stays quiet", &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{agg(types.AGG_VARIANCE, "amt", "v")}}, nil},
		{"ungrouped variance", &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{agg(types.AGG_VARIANCE, "wide", "v"), agg(types.AGG_SUM, "amt", "s")}},
			[]map[string]any{{"label": "v", "aggregation": "AGG_VARIANCE", "field": "wide", "cells": 1}}},
		{"ungrouped average overflow", &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{agg(types.AGG_AVERAGE, "big", "m")}},
			[]map[string]any{{"label": "m", "aggregation": "AGG_AVERAGE", "field": "big", "cells": 1}}},
		{"grouped stddev, one warning per slot", &types.Request{Cohort: cohort, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{agg(types.AGG_STDDEV, "wide", "sd"), agg(types.AGG_VARIANCE, "wide", "var")}},
			[]map[string]any{{"label": "sd", "aggregation": "AGG_STDDEV", "field": "wide", "cells": 3}, {"label": "var", "aggregation": "AGG_VARIANCE", "field": "wide", "cells": 3}}},
		{"grouped with a window reading the cell", &types.Request{Cohort: cohort, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{agg(types.AGG_STDDEV, "wide", "sd")},
			Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "sd", Label: "lag", OrderBy: []types.OrderKey{{Field: "g"}}}}},
			[]map[string]any{{"label": "sd", "aggregation": "AGG_STDDEV", "field": "wide", "cells": 3}}},
		{"crosstab cells and margins", &types.Request{Cohort: cohort, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
			Cell:    agg(types.AGG_VARIANCE, "wide", "v"),
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true}}},
			// 3 x 2 cells + 3 row margins + 2 column margins + the grand total
			[]map[string]any{{"label": "v", "aggregation": "AGG_VARIANCE", "field": "wide", "cells": 12}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := p.Process(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(lossWarnings(resp))
			want, _ := json.Marshal(tc.want)
			if string(got) != string(want) {
				t.Fatalf("PULSE_DECIMAL_PRECISION_LOSS details %s, want %s", got, want)
			}
			if len(tc.req.Windows) > 0 {
				lagged := 0
				for _, row := range resp.Data {
					// WIN_LAG copies the cell (the fallen-back figure); the
					// first row has no predecessor.
					if v, ok := row["lag"].(float64); row["lag"] != nil && (!ok || !math.IsNaN(v)) {
						lagged++
					}
				}
				if lagged != len(resp.Data)-1 {
					t.Fatalf("the window read %d fallen-back cells of %d rows: %v", lagged, len(resp.Data), resp.Data)
				}
			}
		})
	}
}

// TestDecimalPrecisionLoss_WorkerInvariant: the warnings (and the whole
// answer) are identical on the single file and on its shard archive
// under every ShardWorkers count, grouped and crosstab alike. A decimal
// fold is not mergeable, so every archive cell runs buffered.
func TestDecimalPrecisionLoss_WorkerInvariant(t *testing.T) {
	t.Parallel()

	fs := plCohorts(t)
	cohorts := map[invariance.Layout]string{invariance.File: plFile, invariance.Archive: plArchive}
	group := invariance.Group{Name: "archive", Ref: invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.File, DecodeWorkers: 1, ShardWorkers: 1}}}
	for _, sw := range append([]int{1}, invariance.ArchiveShardWorkers...) {
		group.Cells = append(group.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.Archive, DecodeWorkers: 1, ShardWorkers: sw}, Arm: observe.ArmBuffered})
	}
	group.Cells = append(group.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.Archive, DecodeWorkers: 7, ShardWorkers: 3}, Arm: observe.ArmBuffered})
	for name, mk := range map[string]func(string) *types.Request{
		"grouped": func(c string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: c}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_STDDEV, Field: "wide", Label: "sd"}, {Type: types.AGG_AVERAGE, Field: "big", Label: "m"}}}
		},
		"crosstab": func(c string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: c}, Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
				Cell: &types.Aggregation{Type: types.AGG_VARIANCE, Field: "wide", Label: "v"}, Margins: types.CrosstabMargins{Rows: true, Grand: true}}}
		},
	} {
		invariance.Run(t, invariance.Family[string]{
			Name:   name,
			Groups: []invariance.Group{group},
			Run: func(t *testing.T, k invariance.Knobs) invariance.Result[string] {
				var mu sync.Mutex
				var arm observe.Arm
				p, err := pulse.New(pulse.Options{FS: fs, ShardWorkers: k.ShardWorkers, DecodeWorkers: k.DecodeWorkers,
					Hooks: &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
						mu.Lock()
						arm = r.Arm
						mu.Unlock()
					}}})
				if err != nil {
					t.Fatal(err)
				}
				resp, err := p.Process(context.Background(), mk(cohorts[k.Layout]))
				if err != nil {
					t.Fatalf("%s: %v", k, err)
				}
				body, err := types.MarshalFinite(invariance.LayoutFree(resp))
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				defer mu.Unlock()
				return invariance.Result[string]{Answer: string(body), Arm: arm}
			},
			Check: func(t *testing.T, _ invariance.Knobs, body string) {
				var resp types.Response
				if err := json.Unmarshal([]byte(body), &resp); err != nil {
					t.Fatal(err)
				}
				if len(lossWarnings(&resp)) == 0 {
					t.Fatalf("no PULSE_DECIMAL_PRECISION_LOSS in %s", body)
				}
			},
		})
	}
}
