package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// runProcessForRunComponents drives svc.Process and returns the
// RunComponents pointer. Fails the test if Components or Run is nil.
func runProcessForRunComponents(t *testing.T, svc *Service, req *types.Request) *types.RunComponents {
	t.Helper()
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	if resp.Components.Run == nil {
		t.Fatalf("Response.Components.Run nil; want populated on every successful Process")
	}
	// Metadata-vs-Run parity invariant — locked here so any future
	// path that forgets to populate one side surfaces at this gate.
	if resp.Metadata == nil {
		t.Fatalf("Response.Metadata nil; want populated alongside Components.Run")
	}
	if resp.Metadata.TotalRows != resp.Components.Run.TotalRecords {
		t.Errorf("Metadata.TotalRows = %d, Components.Run.TotalRecords = %d (want equal)",
			resp.Metadata.TotalRows, resp.Components.Run.TotalRecords)
	}
	if resp.Metadata.FilteredRows != resp.Components.Run.FilteredRecords {
		t.Errorf("Metadata.FilteredRows = %d, Components.Run.FilteredRecords = %d (want equal)",
			resp.Metadata.FilteredRows, resp.Components.Run.FilteredRecords)
	}
	return resp.Components.Run
}

// TestService_Process_RunComponents_BaseFloor covers the single-file
// no-filter no-null floor case across the streaming (AGG_SUM) exit.
// Every counter sits at its baseline: TotalRecords == 5,
// FilteredRecords == 5, NullRecords == 0, ShardCount == 0,
// PartialCohortReason == "".
func TestService_Process_RunComponents_BaseFloor(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}
	run := runProcessForRunComponents(t, svc, req)
	if run.TotalRecords != 5 {
		t.Errorf("TotalRecords = %d, want 5", run.TotalRecords)
	}
	if run.FilteredRecords != 5 {
		t.Errorf("FilteredRecords = %d, want 5 (no filters)", run.FilteredRecords)
	}
	if run.NullRecords != 0 {
		t.Errorf("NullRecords = %d, want 0 (no nullable inputs)", run.NullRecords)
	}
	if run.ShardCount != 0 {
		t.Errorf("ShardCount = %d, want 0 (single-file cohort)", run.ShardCount)
	}
	if run.PartialCohortReason != "" {
		t.Errorf("PartialCohortReason = %q, want \"\" (full run)", run.PartialCohortReason)
	}
}

// TestService_Process_RunComponents_FilteredMatchesLastSlot covers the
// single-file FILTER_RANGE case. FilteredRecords MUST equal the last
// (here: only) filterer's NOut, locking the cross-slot invariant
// documented on RunComponents.
func TestService_Process_RunComponents_FilteredMatchesLastSlot(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
		// FILTER_RANGE on score in [10,40] admits records with score in
		// {10,20,30,40}; 4 of 5 admit, 1 (score=50) drops.
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"10", "40"}},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatalf("Components.Run not populated")
	}
	if got := resp.Components.Run.TotalRecords; got != 5 {
		t.Errorf("TotalRecords = %d, want 5", got)
	}
	if got := resp.Components.Run.FilteredRecords; got != 4 {
		t.Errorf("FilteredRecords = %d, want 4", got)
	}
	// Cross-slot invariant: last filterer's NOut == Run.FilteredRecords.
	if got := len(resp.Components.Filterers); got != 1 {
		t.Fatalf("Filterers slot count = %d, want 1", got)
	}
	last := resp.Components.Filterers[0]
	if int64(last.NOut) != resp.Components.Run.FilteredRecords {
		t.Errorf("last Filterers.NOut = %d, Run.FilteredRecords = %d (want equal)",
			last.NOut, resp.Components.Run.FilteredRecords)
	}
	if resp.Components.Run.ShardCount != 0 {
		t.Errorf("ShardCount = %d, want 0 (single-file)", resp.Components.Run.ShardCount)
	}
}

// TestService_Process_RunComponents_NullsOnPrimaryField writes a 5-row
// cohort whose primary (score) column is null on every row, then runs
// an AGG_SUM(score) request through the streaming exit. NullRecords
// MUST equal the post-filter null count (5 — every row's primary field
// is null after the no-op filter pass).
func TestService_Process_RunComponents_NullsOnPrimaryField(t *testing.T) {
	svc := allNullScoreCohort(t, "all_null_scores.pulse")
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "all_null_scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}
	run := runProcessForRunComponents(t, svc, req)
	if run.TotalRecords != 5 {
		t.Errorf("TotalRecords = %d, want 5", run.TotalRecords)
	}
	if run.FilteredRecords != 5 {
		t.Errorf("FilteredRecords = %d, want 5 (no filters)", run.FilteredRecords)
	}
	if run.NullRecords != 5 {
		t.Errorf("NullRecords = %d, want 5 (every primary value is null)", run.NullRecords)
	}
}

// TestService_Process_RunComponents_BufferedPath drives the buffered
// processRecords exit by adding a windowed request (windows force the
// buffered path). NullRecords here resolves via the per-aggregator
// floor cache and must match the streaming-path semantics.
func TestService_Process_RunComponents_BufferedPath(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
		Windows: []*types.Window{
			{Type: types.WIN_RANK, Field: "score", Label: "rk"},
		},
	}
	run := runProcessForRunComponents(t, svc, req)
	if run.TotalRecords != 5 {
		t.Errorf("TotalRecords = %d, want 5", run.TotalRecords)
	}
	if run.FilteredRecords != 5 {
		t.Errorf("FilteredRecords = %d, want 5", run.FilteredRecords)
	}
	if run.NullRecords != 0 {
		t.Errorf("NullRecords = %d, want 0", run.NullRecords)
	}
}

// TestService_Process_RunComponents_GroupedStreaming drives the
// streaming-grouped exit (GROUP_RANGE on score). The grouped path
// tracks NullRecords inline against the resolved primary field; with
// no nullable score values the counter stays at 0.
func TestService_Process_RunComponents_GroupedStreaming(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_RANGE, Field: "score", Interval: 20},
		},
	}
	run := runProcessForRunComponents(t, svc, req)
	if run.TotalRecords != 5 {
		t.Errorf("TotalRecords = %d, want 5", run.TotalRecords)
	}
	if run.FilteredRecords != 5 {
		t.Errorf("FilteredRecords = %d, want 5", run.FilteredRecords)
	}
	if run.NullRecords != 0 {
		t.Errorf("NullRecords = %d, want 0", run.NullRecords)
	}
}

// TestService_Process_RunComponents_ShardArchive drives the
// shard-archive parallel reducer (processShardArchiveParallel →
// finalizeMergedPartial). Builds an in-memory two-shard archive,
// asserts Run.ShardCount equals the merged shard count and
// Run.TotalRecords / FilteredRecords sum across shards.
func TestService_Process_RunComponents_ShardArchive(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	shardA := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
	}
	shardB := [][]uint64{
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
	}

	archive := buildShardArchive(t, schema, []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "shard_a.pulse", Records: shardA},
		{Name: "shard_b.pulse", Records: shardB},
	})
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "shards.pulse", archive, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)
	// Force shard-archive parallel reducer (workers != 1, mergeable
	// request). processShardArchiveParallel populates Run.ShardCount
	// from the merged partial; the serial path bypasses this and
	// would leave ShardCount at 0 — that branch is locked separately
	// by buildShardArchiveForRunComponentsTest's parallelism mode.
	svc.SetShardWorkers(2)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "shards.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatalf("Components.Run not populated")
	}
	run := resp.Components.Run
	if run.TotalRecords != 5 {
		t.Errorf("TotalRecords = %d, want 5 (3 + 2 across shards)", run.TotalRecords)
	}
	if run.FilteredRecords != 5 {
		t.Errorf("FilteredRecords = %d, want 5", run.FilteredRecords)
	}
	if run.ShardCount != 2 {
		t.Errorf("ShardCount = %d, want 2", run.ShardCount)
	}
	if resp.Metadata.TotalRows != run.TotalRecords {
		t.Errorf("Metadata-vs-Run parity broken on shard path: %d vs %d",
			resp.Metadata.TotalRows, run.TotalRecords)
	}
}

// TestService_Process_RunComponents_RoundTripJSON locks the public
// types-level round-trip contract over a populated RunComponents
// instance constructed by Process. Mirrors types.TestResponse_
// ComponentsRoundTripJSON but uses the actual orchestrator output as
// the source of truth — drift between the static round-trip lock and
// the runtime emission surfaces here.
func TestService_Process_RunComponents_RoundTripJSON(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatalf("Components.Run not populated")
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got types.Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Components == nil || got.Components.Run == nil {
		t.Fatalf("Run slot lost after round-trip")
	}
	want := resp.Components.Run
	if got.Components.Run.TotalRecords != want.TotalRecords {
		t.Errorf("TotalRecords drift: got %d, want %d",
			got.Components.Run.TotalRecords, want.TotalRecords)
	}
	if got.Components.Run.FilteredRecords != want.FilteredRecords {
		t.Errorf("FilteredRecords drift: got %d, want %d",
			got.Components.Run.FilteredRecords, want.FilteredRecords)
	}
	if got.Components.Run.NullRecords != want.NullRecords {
		t.Errorf("NullRecords drift: got %d, want %d",
			got.Components.Run.NullRecords, want.NullRecords)
	}
	if got.Components.Run.ShardCount != want.ShardCount {
		t.Errorf("ShardCount drift: got %d, want %d",
			got.Components.Run.ShardCount, want.ShardCount)
	}
}
