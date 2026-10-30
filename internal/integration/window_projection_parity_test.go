package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// A windows-only request (no aggregation, no group, no crosstab) emits
// one record row per filtered record. Projection is output-transparent,
// so the default (projected) decode must return the same rows as
// DisableProjection — the FULL record plus the window columns, never
// only the fields the windows read (#201d). Pinned for every built-in
// window type, for several windows together, for windows beside a
// filter and an attribute, and over a shard archive read with
// ShardWorkers > 1.

func wpSchema() encoding.Schema {
	cat := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				panic(err)
			}
		}
		return d
	}
	fields := []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("north", "south")},
		{Name: "age", Type: encoding.FieldTypeU8},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "score", Type: encoding.FieldTypeF64},
	}
	for i := range fields {
		fields[i].Description = "Window projection parity column " + fields[i].Name + "."
	}
	return encoding.Schema{Fields: fields}
}

// wpRows are 12 rows; shards split them 0..3 / 3..3 (empty) / 3..12.
var wpShardBounds = []int{0, 3, 3, 12}

func wpRow(i int) pulse.CohortRow {
	var x any = float64(i*7%11) + 0.5
	if i == 4 {
		x = nil
	}
	region := "north"
	if i%3 == 0 {
		region = "south"
	}
	return pulse.CohortRow{uint64(i + 1), x, region, uint64(20 + i), i%2 == 0, float64(i) * 1.25}
}

func wpWrite(t *testing.T, p *pulse.Pulse, path string, from, to int) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, wpSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := from; i < to; i++ {
		if err := b.Append(wpRow(i)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func wpIntPtr(n int) *int { return &n }

// wpWindow is one valid window slot per built-in type.
func wpWindow(wt types.WindowType) *types.Window {
	w := &types.Window{Type: wt, Field: "x", Label: "w_" + string(wt), OrderBy: []types.OrderKey{{Field: "week"}}}
	switch wt {
	case types.WIN_ROW_NUMBER, types.WIN_RANK, types.WIN_DENSE_RANK:
		w.Field = ""
	case types.WIN_RUNNING_SUM, types.WIN_RUNNING_AVG:
		w.Frame = &types.FrameSpec{Mode: "rows", Following: wpIntPtr(0)}
	case types.WIN_MOVING_AVG:
		w.Frame = &types.FrameSpec{Mode: "rows", Preceding: wpIntPtr(2), Following: wpIntPtr(0)}
	case types.WIN_EWMA:
		w.Frame = &types.FrameSpec{Mode: "rows"}
		w.Params = json.RawMessage(`{"alpha":0.5}`)
	case types.WIN_PCT_CHANGE, types.WIN_DELTA:
		w.Params = json.RawMessage(`{"periods":1}`)
	case types.WIN_DISTRIBUTED_LAG:
		w.Params = json.RawMessage(`{"kernel":"weibull_pdf","shape":2,"scale":2,"max_lag":4}`)
	case types.WIN_LAG, types.WIN_LEAD:
		w.Params = json.RawMessage(`{"offset":1}`)
	case types.WIN_SHARE:
		w.Params = json.RawMessage(`{"cumulative":true}`)
	case types.WIN_LIFT:
		w.Params = json.RawMessage(`{"base":"score","cumulative":true}`)
	}
	return w
}

func TestWindowRecordRows_ProjectionTransparent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	engine := func(disable bool, shardWorkers int) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys, DisableProjection: disable, ShardWorkers: shardWorkers})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	writer := engine(false, 0)
	wpWrite(t, writer, "wp.pulse", 0, 12)
	var shards []string
	for i := 0; i+1 < len(wpShardBounds); i++ {
		path := fmt.Sprintf("wp_s%d.pulse", i)
		wpWrite(t, writer, path, wpShardBounds[i], wpShardBounds[i+1])
		shards = append(shards, path)
	}
	if _, err := writer.CreateShardArchive(ctx, "wp_archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	type shape struct {
		name string
		req  func() *types.Request
	}
	var shapes []shape
	for _, wt := range types.AllWindowTypes() {
		wt := wt
		shapes = append(shapes, shape{string(wt), func() *types.Request {
			return &types.Request{Windows: []*types.Window{wpWindow(wt)}}
		}})
	}
	shapes = append(shapes,
		shape{"multiple windows", func() *types.Request {
			lag := wpWindow(types.WIN_LAG)
			lag.PartitionBy = []string{"region"}
			return &types.Request{Windows: []*types.Window{wpWindow(types.WIN_ROW_NUMBER), lag, wpWindow(types.WIN_RUNNING_SUM)}}
		}},
		shape{"windows with filter and attribute", func() *types.Request {
			return &types.Request{
				Filterers:  []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}},
				Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "x", Label: "zx"}},
				Windows:    []*types.Window{wpWindow(types.WIN_RANK), wpWindow(types.WIN_MOVING_AVG)},
				Sort:       []types.OrderKey{{Field: "week", Desc: true}},
			}
		}},
	)

	arms := []struct {
		name         string
		cohort       string
		shardWorkers int
	}{
		{"single file", "wp.pulse", 0},
		{"shard archive", "wp_archive.pulse", 3},
	}
	for _, arm := range arms {
		projected, full := engine(false, arm.shardWorkers), engine(true, arm.shardWorkers)
		for _, sh := range shapes {
			t.Run(arm.name+"/"+sh.name, func(t *testing.T) {
				run := func(p *pulse.Pulse) []byte {
					req := sh.req()
					req.Cohort = &types.Cohort{Filename: arm.cohort}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					b, err := json.Marshal(resp.Data)
					if err != nil {
						t.Fatal(err)
					}
					return b
				}
				on, off := run(projected), run(full)
				if !bytes.Equal(on, off) {
					t.Fatalf("projection changed the record rows\nprojected: %s\nfull:      %s", on, off)
				}
				var rows []map[string]any
				if err := json.Unmarshal(on, &rows); err != nil {
					t.Fatal(err)
				}
				if len(rows) == 0 {
					t.Fatal("no rows")
				}
				for _, f := range wpSchema().Fields {
					if _, ok := rows[0][f.Name]; !ok {
						t.Fatalf("row 0 lacks schema field %q: %v", f.Name, rows[0])
					}
				}
			})
		}
	}
}
