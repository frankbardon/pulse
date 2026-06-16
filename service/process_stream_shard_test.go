package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestShardArchiveProcessStream(t *testing.T) {
	t.Run("SingleFileRegression", testShardArchiveProcessStream_SingleFileRegression)
	t.Run("MultiShardRowCount", testShardArchiveProcessStream_MultiShardRowCount)
	t.Run("MultiShardOnlineAccumulation", testShardArchiveProcessStream_MultiShardOnlineAccumulation)
	t.Run("MultiShardRowOrder", testShardArchiveProcessStream_MultiShardRowOrder)
	t.Run("MultiShardFilterApplies", testShardArchiveProcessStream_MultiShardFilterApplies)
	t.Run("MultiShardMetadata", testShardArchiveProcessStream_MultiShardMetadata)
}

// testShardArchiveProcessStream_SingleFileRegression confirms that the
// single-file ProcessStream path is byte-for-byte unchanged. Regression
// guard against the newScanIter dispatcher introducing single-file
// fallthrough drift.
func testShardArchiveProcessStream_SingleFileRegression(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "single.pulse", schema, testRecords())
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "single.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	rows := drain(t, iter)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if got, _ := rows[0]["total"].(float64); got != 150 {
		t.Errorf("total = %v, want 150", rows[0]["total"])
	}
	if got, _ := rows[0]["n"].(float64); got != 5 {
		t.Errorf("n = %v, want 5", rows[0]["n"])
	}
}

// testShardArchiveProcessStream_MultiShardRowCount confirms the streamed
// row count when grouping reflects the union of every shard's records.
// 12 rows split 4/4/4 across three shards, grouped by id (each unique)
// produces 12 result rows.
func testShardArchiveProcessStream_MultiShardRowCount(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "id"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "score_sum"},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	rows := drain(t, iter)
	if len(rows) != 12 {
		t.Fatalf("streamed rows = %d, want 12 (one per id across union)", len(rows))
	}

	// Every per-id score_sum should equal that row's original score
	// (one record per id). Sum of all per-group sums = 780 = full
	// union total — proves no rows were dropped or double-counted.
	var total float64
	for _, r := range rows {
		v, _ := r["score_sum"].(float64)
		total += v
	}
	if total != 780 {
		t.Errorf("sum of grouped score_sum = %v, want 780", total)
	}
}

// testShardArchiveProcessStream_MultiShardOnlineAccumulation confirms
// that an online aggregator (AGG_SUM, AGG_COUNT, AGG_MIN, AGG_MAX) folds
// records across all shards. The streamed result equals the
// concatenated single-file result byte-for-byte for associative+
// commutative ops.
func testShardArchiveProcessStream_MultiShardOnlineAccumulation(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				{Type: types.AGG_SUM, Field: "score", Label: "total"},
				{Type: types.AGG_MIN, Field: "score", Label: "lo"},
				{Type: types.AGG_MAX, Field: "score", Label: "hi"},
			},
		}
	}

	streamA := drainProcessStream(t, svc, req("arch.pulse"))
	streamB := drainProcessStream(t, svc, req("arch.pulse.concat"))

	if len(streamA) != 1 || len(streamB) != 1 {
		t.Fatalf("rows: archive=%d concat=%d, want 1 each", len(streamA), len(streamB))
	}

	for _, label := range []string{"n", "total", "lo", "hi"} {
		a, _ := streamA[0][label].(float64)
		b, _ := streamB[0][label].(float64)
		if a != b {
			t.Errorf("%s: archive=%v concat=%v (must be byte-equal)", label, a, b)
		}
	}
	if got, _ := streamA[0]["n"].(float64); got != 12 {
		t.Errorf("n = %v, want 12", got)
	}
	if got, _ := streamA[0]["total"].(float64); got != 780 {
		t.Errorf("total = %v, want 780", got)
	}
	if got, _ := streamA[0]["lo"].(float64); got != 10 {
		t.Errorf("lo = %v, want 10", got)
	}
	if got, _ := streamA[0]["hi"].(float64); got != 120 {
		t.Errorf("hi = %v, want 120", got)
	}
}

// testShardArchiveProcessStream_MultiShardRowOrder confirms that when
// the request produces one output row per input record (here via a
// WIN_LAG window operator), the streamed row order equals the
// underlying iterator's record order: shard1 records first (in shard1's
// record order), then shard2's, etc. Window operators force the
// buffered path per CLAUDE.md, so this also exercises the union
// materialization contract from §5.2.
func testShardArchiveProcessStream_MultiShardRowOrder(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "score",
				Label:   "prev_score",
				OrderBy: []types.OrderKey{{Field: "id"}},
				Params:  json.RawMessage(`{"offset": 1}`),
			},
		},
	}

	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	rows := drain(t, iter)
	if len(rows) != 12 {
		t.Fatalf("streamed rows = %d, want 12", len(rows))
	}

	// The streamed rows come from records ordered shard1 (ids 1..4),
	// shard2 (ids 5..8), shard3 (ids 9..12). Window Apply does not
	// reorder rows in place — see processing/window/lag_test.go — so
	// the result preserves iteration order.
	for i, r := range rows {
		gotID, _ := r["id"].(float64)
		wantID := float64(concat[i][0])
		if gotID != wantID {
			t.Errorf("row[%d] id = %v, want %v (shard insertion order)", i, gotID, wantID)
		}
		gotScore, _ := r["score"].(float64)
		wantScore := math.Float64frombits(concat[i][1])
		if gotScore != wantScore {
			t.Errorf("row[%d] score = %v, want %v", i, gotScore, wantScore)
		}
	}
}

// testShardArchiveProcessStream_MultiShardFilterApplies confirms that
// FILTER_RANGE applied to a multi-shard cohort filters per-row across
// the entire union, with the streamed count matching the single-file
// concat. The filter selects scores in [40,90] inclusive → 6 rows
// (40, 50, 60, 70, 80, 90) drawn from shards 1, 2, and 3.
func testShardArchiveProcessStream_MultiShardFilterApplies(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "score", Values: []string{"40", "90"}},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				{Type: types.AGG_SUM, Field: "score", Label: "total"},
			},
		}
	}

	streamA := drainProcessStream(t, svc, req("arch.pulse"))
	streamB := drainProcessStream(t, svc, req("arch.pulse.concat"))

	if len(streamA) != 1 || len(streamB) != 1 {
		t.Fatalf("rows: archive=%d concat=%d", len(streamA), len(streamB))
	}
	nA, _ := streamA[0]["n"].(float64)
	nB, _ := streamB[0]["n"].(float64)
	if nA != nB {
		t.Errorf("filter count: archive=%v concat=%v", nA, nB)
	}
	if nA != 6 {
		t.Errorf("filter count = %v, want 6 (scores 40..90 across all shards)", nA)
	}
	tA, _ := streamA[0]["total"].(float64)
	tB, _ := streamB[0]["total"].(float64)
	if tA != tB {
		t.Errorf("filter sum: archive=%v concat=%v", tA, tB)
	}
	if tA != 390 { // 40+50+60+70+80+90
		t.Errorf("filter sum = %v, want 390", tA)
	}
}

// testShardArchiveProcessStream_MultiShardMetadata confirms post-drain
// metadata reports the union row count and the original archive path.
func testShardArchiveProcessStream_MultiShardMetadata(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()

	_ = drain(t, iter)
	meta := iter.Metadata()
	if meta == nil {
		t.Fatal("metadata is nil after drain")
	}
	if meta.TotalRows != 12 {
		t.Errorf("TotalRows = %d, want 12 (union across shards)", meta.TotalRows)
	}
	if meta.CohortFile != "arch.pulse" {
		t.Errorf("CohortFile = %q, want arch.pulse", meta.CohortFile)
	}
}

// drainProcessStream is a small helper that opens a ProcessStream
// iterator, drains every row, and returns the slice. Closes the
// iterator before returning. Test failures surface via t.Fatalf so
// individual subtests stay terse.
func drainProcessStream(t *testing.T, svc *Service, req *types.Request) []Row {
	t.Helper()
	iter, err := svc.ProcessStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	defer iter.Close()
	return drain(t, iter)
}
