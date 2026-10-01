package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// E2-S4 — fan-out across a shard archive.
//
// A crosstab over a shard archive reaches the fused state through the
// shard iterator rather than the single-file scan, so the per-record
// fan-out has to survive a record stream that is stitched from several
// standalone shard payloads. Three-way assertion: archive-fused ==
// archive-buffered == single-file-buffered over the concatenation of
// the same rows. The third leg is what makes this more than a
// self-consistency check — the shard split must be invisible.
func TestCrosstabFused_SetFanOutOverShardArchive(t *testing.T) {
	schema := setFanoutSchema(t)
	shardA := [][]uint64{
		{0, 0b0111, math.Float64bits(10)}, // north, VISA+MC+AMEX
		{0, 0b0001, math.Float64bits(20)}, // north, VISA
	}
	shardB := [][]uint64{
		{1, 0b1100, math.Float64bits(30)}, // south, AMEX+DISC
		{1, 0b0000, math.Float64bits(40)}, // south, no selection
	}
	shardC := [][]uint64{
		{1, 0b1010, math.Float64bits(50)}, // south, MC+DISC
		{0, 0b1000, math.Float64bits(60)}, // north, DISC
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: shardA},
		{Name: "b.pulse", Records: shardB},
		{Name: "c.pulse", Records: shardC},
	}
	concat := append(append(append([][]uint64{}, shardA...), shardB...), shardC...)

	svcFused, cfg := setupShardArchive(t, "sets-arch.pulse", schema, shards, concat)
	ctx := context.Background()

	buildReq := func(file string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: file},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}

	if ok, reason := processing.CanFuseCrosstab(buildReq("sets-arch.pulse"), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected a sharded fan-out crosstab: %s", reason)
	}
	archiveFused, err := svcFused.Process(ctx, buildReq("sets-arch.pulse"))
	if err != nil {
		t.Fatalf("Process (archive, fused): %v", err)
	}

	svcBuf := New(cfg)
	svcBuf.SetDisableCrosstabFusion(true)
	archiveBuffered, err := svcBuf.Process(ctx, buildReq("sets-arch.pulse"))
	if err != nil {
		t.Fatalf("Process (archive, buffered): %v", err)
	}
	assertResponseSlotsEqual(t, archiveBuffered, archiveFused)

	// The shard split must be invisible: the same rows in one file must
	// produce the same matrix. Metadata carries the cohort filename and
	// is therefore excluded by construction here.
	singleBuffered, err := svcBuf.Process(ctx, buildReq("sets-arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process (single file, buffered): %v", err)
	}
	assertResponseSlotsEqual(t, singleBuffered, archiveFused)

	// Non-oracle: the fan must actually have fired across shards. VISA
	// appears only in shard a, DISC in shards b and c, and the north
	// DISC row is in shard c — so a per-shard state that failed to
	// merge would drop or split these buckets.
	m := archiveFused.Crosstab.Matrix
	if m == nil {
		t.Fatal("fused response missing Matrix payload")
	}
	byLabel := map[string]int{}
	for i, rk := range m.RowKeys {
		s, ok := rk[0].(string)
		if !ok {
			t.Fatalf("row key %v is not a string tuple", rk)
		}
		byLabel[s] = i
	}
	for _, want := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, ok := byLabel[want]; !ok {
			t.Fatalf("row key %q missing from %v", want, m.RowKeys)
		}
	}
	const north, south = 0, 1
	// DISC: north row 60 (shard c); south rows 30 (shard b) + 50 (shard c).
	if got := m.Cells[byLabel["DISC"]][north].Value; got != 60.0 {
		t.Errorf("(DISC, north) = %v, want 60", got)
	}
	if got := m.Cells[byLabel["DISC"]][south].Value; got != 80.0 {
		t.Errorf("(DISC, south) = %v, want 80", got)
	}
	// VISA: north rows 10 + 20, both in shard a; absent in the south.
	if got := m.Cells[byLabel["VISA"]][north].Value; got != 30.0 {
		t.Errorf("(VISA, north) = %v, want 30", got)
	}
	if cell := m.Cells[byLabel["VISA"]][south]; cell.Present {
		t.Errorf("(VISA, south) = %+v, want absent", cell)
	}
}
