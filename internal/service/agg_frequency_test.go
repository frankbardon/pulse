package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// AGG_FREQUENCY across the merge paths: a shard archive on the
// per-shard parallel reducer and the serial shard path must equal one
// single-file cohort holding the same rows, for a categorical label and
// a numeric value, grouped; and ProcessChain admits it as a scalar
// stage. Matches sit in more than one shard per group, so a merge that
// kept one partial changes the numbers.

func frequencyShardFixture() (*encoding.Schema, []struct {
	Name    string
	Records [][]uint64
}) {
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "grp", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: dict},
		{Name: "v", Type: encoding.FieldTypeU8, ByteOffset: 2, CsvColumnIdx: 2},
	}}
	// red = 0, green = 1, blue = 2.
	// grp 1: red x3 (one per shard), v=7 x2 (shards a, c).
	// grp 2: red x1, v=7 x0.
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{"a.pulse", [][]uint64{{1, 0, 7}, {1, 1, 5}, {2, 2, 1}}},
		{"b.pulse", [][]uint64{{1, 0, 5}, {2, 0, 2}, {2, 1, 2}}},
		{"c.pulse", [][]uint64{{1, 0, 7}, {1, 2, 5}, {2, 1, 1}}},
	}
	return schema, shards
}

func frequencyRequest(file string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: file},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grp"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_FREQUENCY, Field: "color", Label: "reds", Params: json.RawMessage(`{"value":"red"}`)},
			{Type: types.AGG_FREQUENCY, Field: "v", Label: "sevens", Params: json.RawMessage(`{"value":7}`)},
		},
		Sort: []types.OrderKey{{Field: "grp"}},
	}
}

func TestAggFrequency_ShardArchiveEqualsSingleFile(t *testing.T) {
	schema, shards := frequencyShardFixture()
	var flat [][]uint64
	for _, sh := range shards {
		flat = append(flat, sh.Records...)
	}
	cfg := setupTestFS(t, "flat.pulse", schema, flat)
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", buildShardArchive(t, schema, shards), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	serial, err := New(cfg).Process(ctx, frequencyRequest("flat.pulse"))
	if err != nil {
		t.Fatalf("single-file Process: %v", err)
	}
	want := map[string][2]float64{"1": {3, 2}, "2": {1, 0}}
	if len(serial.Data) != len(want) {
		t.Fatalf("single-file rows = %v", serial.Data)
	}
	for _, row := range serial.Data {
		w := want[fmt.Sprint(row["grp"])]
		if row["reds"] != w[0] || row["sevens"] != w[1] {
			t.Errorf("single-file grp %v: reds=%v sevens=%v, want %v", row["grp"], row["reds"], row["sevens"], w)
		}
	}
	for _, workers := range []int{3, 1} {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		resp, err := svc.Process(ctx, frequencyRequest("arch.pulse"))
		if err != nil {
			t.Fatalf("workers=%d: archive Process: %v", workers, err)
		}
		if !reflect.DeepEqual(resp.Data, serial.Data) {
			t.Errorf("workers=%d: archive = %v, single file = %v", workers, resp.Data, serial.Data)
		}
	}
}

func TestProcessChain_FrequencyAggregatorAccepted(t *testing.T) {
	schema, shards := frequencyShardFixture()
	cfg := setupTestFS(t, "unused.pulse", schema, nil)
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", buildShardArchive(t, schema, shards), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{3, 1} {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		resp, err := svc.ProcessChain(context.Background(), &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "arch.pulse"},
			Stages: []*types.ChainStage{
				{Name: "counts", Request: frequencyRequest("")},
				{Name: "total", Request: &types.Request{
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "reds", Label: "total"}},
				}},
			},
		})
		if err != nil {
			t.Fatalf("workers=%d: ProcessChain: %v", workers, err)
		}
		if got := resp.Final.Data[0]["total"]; got != 4.0 {
			t.Errorf("workers=%d: total reds = %v, want 4 (3 + 1)", workers, got)
		}
	}
}
