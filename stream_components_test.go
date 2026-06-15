package pulse

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestProcessStreamResult_AggSumPerChunkComponents pins the per-chunk
// mergeable contract on an ungrouped AGG_SUM stream: the single
// emitted chunk carries the Components.Aggregations entry with the
// "sum" operator key populated, and that Components is byte-equal to
// the buffered Process call's Response.Components.
func TestProcessStreamResult_AggSumPerChunkComponents(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse",
		[]string{"score"},
		[][]string{
			{"10"}, {"20"}, {"30"}, {"40"}, {"50"}, {"60"},
		})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Cohort: &types.Cohort{Filename: "rows.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}

	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var chunks []StreamChunk[Row]
	for chunk := range res.Chunks {
		chunks = append(chunks, chunk)
	}
	term := <-res.Done
	if term.Status != StreamCompleted {
		t.Fatalf("status = %v, want completed (err=%v)", term.Status, term.Error)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (ungrouped AGG_SUM)", len(chunks))
	}
	c := chunks[0].Components
	if c == nil {
		t.Fatal("Components nil on terminal chunk for mergeable AGG_SUM")
	}
	if len(c.Aggregations) != 1 {
		t.Fatalf("Aggregations len = %d, want 1", len(c.Aggregations))
	}
	op := c.Aggregations[0].Operator
	if op == nil {
		t.Fatal("AGG_SUM slot Operator nil; want populated for mergeable")
	}
	if _, ok := op["sum"]; !ok {
		t.Fatalf("Operator missing 'sum'; got %v", op)
	}

	buffered, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !reflect.DeepEqual(c, buffered.Components) {
		t.Fatalf("terminal Components != buffered Components\nstream:   %+v\nbuffered: %+v",
			c, buffered.Components)
	}
}

// TestProcessStreamResult_AggVariancePerChunkComponents pins the
// mergeable contract for AGG_VARIANCE (Welford-family). The Operator
// map carries Welford state on the terminal chunk and matches the
// buffered Process Components byte-for-byte.
func TestProcessStreamResult_AggVariancePerChunkComponents(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse",
		[]string{"score"},
		[][]string{
			{"10"}, {"20"}, {"30"}, {"40"}, {"50"}, {"60"},
		})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Cohort: &types.Cohort{Filename: "rows.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_VARIANCE, Field: "score", Label: "var_score"},
		},
	}

	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var chunks []StreamChunk[Row]
	for chunk := range res.Chunks {
		chunks = append(chunks, chunk)
	}
	term := <-res.Done
	if term.Status != StreamCompleted {
		t.Fatalf("status = %v, want completed (err=%v)", term.Status, term.Error)
	}
	c := chunks[len(chunks)-1].Components
	if c == nil || len(c.Aggregations) != 1 {
		t.Fatalf("terminal Components missing Aggregations entry; got %+v", c)
	}
	if c.Aggregations[0].Operator == nil {
		t.Fatal("Variance Operator nil on terminal chunk; want Welford state")
	}

	buffered, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !reflect.DeepEqual(c, buffered.Components) {
		t.Fatalf("terminal Components != buffered:\nstream:   %+v\nbuffered: %+v",
			c, buffered.Components)
	}
}

// TestProcessStreamResult_AggMedianTerminalCarriesOperator pins the
// terminal-only contract for AGG_MEDIAN (non-mergeable). On the
// ungrouped single-chunk stream the lone chunk IS the terminal, so it
// carries the full Operator map with the median / position_low /
// position_high keys. Mirrors the buffered Process Components.
func TestProcessStreamResult_AggMedianTerminalCarriesOperator(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse",
		[]string{"score"},
		[][]string{
			{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
		})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Cohort: &types.Cohort{Filename: "rows.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "score", Label: "median_score"},
		},
	}

	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var chunks []StreamChunk[Row]
	for chunk := range res.Chunks {
		chunks = append(chunks, chunk)
	}
	term := <-res.Done
	if term.Status != StreamCompleted {
		t.Fatalf("status = %v, want completed (err=%v)", term.Status, term.Error)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (ungrouped AGG_MEDIAN)", len(chunks))
	}
	c := chunks[0].Components
	if c == nil || len(c.Aggregations) != 1 {
		t.Fatalf("terminal Components missing Aggregations entry; got %+v", c)
	}
	op := c.Aggregations[0].Operator
	if op == nil {
		t.Fatal("Terminal chunk Operator nil for AGG_MEDIAN; want full map")
	}
	if _, ok := op["median"]; !ok {
		t.Fatalf("Terminal Operator missing 'median' key: %v", op)
	}

	buffered, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !reflect.DeepEqual(c, buffered.Components) {
		t.Fatalf("terminal Components != buffered:\nstream:   %+v\nbuffered: %+v",
			c, buffered.Components)
	}
}

// TestChunkComponents_NonTerminalRedactsNonMergeable unit-tests the
// stream.go projection helper directly. Buffered Components shell with
// AGG_SUM (mergeable) + AGG_MEDIAN (non-mergeable); the non-terminal
// projection keeps the AGG_SUM Operator but strips the AGG_MEDIAN
// Operator while preserving the universal floor. Terminal projection
// is a pointer-identity passthrough.
func TestChunkComponents_NonTerminalRedactsNonMergeable(t *testing.T) {
	buffered := &types.ResponseComponents{
		Aggregations: []types.AggregationComponents{
			{
				Label:    "sum_score",
				N:        5,
				NNull:    0,
				Operator: map[string]any{"sum": 150.0},
			},
			{
				Label: "median_score",
				N:     5,
				NNull: 0,
				Operator: map[string]any{
					"median":        30.0,
					"position_low":  2,
					"position_high": 2,
				},
			},
		},
	}
	mergeability := []descriptor.ComponentsMergeability{
		descriptor.Mergeable,
		descriptor.None,
	}

	terminal := chunkComponents(buffered, mergeability, true)
	if terminal != buffered {
		t.Fatalf("terminal projection should return buffered pointer; got %p want %p",
			terminal, buffered)
	}

	mid := chunkComponents(buffered, mergeability, false)
	if mid == nil {
		t.Fatal("non-terminal projection nil")
	}
	if mid == buffered {
		t.Fatal("non-terminal projection should clone shell, not share pointer with buffered")
	}
	if len(mid.Aggregations) != 2 {
		t.Fatalf("Aggregations len = %d, want 2", len(mid.Aggregations))
	}
	if mid.Aggregations[0].Operator == nil {
		t.Fatal("mergeable AGG_SUM Operator stripped; want preserved")
	}
	if got := mid.Aggregations[0].Operator["sum"]; got != 150.0 {
		t.Fatalf("mergeable AGG_SUM 'sum' = %v, want 150.0", got)
	}
	if mid.Aggregations[1].Operator != nil {
		t.Fatalf("non-mergeable AGG_MEDIAN Operator = %v, want nil",
			mid.Aggregations[1].Operator)
	}
	// Universal floor preserved on the redacted slot.
	if mid.Aggregations[1].N != 5 || mid.Aggregations[1].Label != "median_score" {
		t.Fatalf("universal floor stripped from redacted slot: %+v", mid.Aggregations[1])
	}
	// Buffered original must remain untouched (Operator still
	// present on the non-mergeable slot).
	if buffered.Aggregations[1].Operator == nil {
		t.Fatal("buffered Components mutated; non-terminal projection should clone")
	}
}

// TestChunkComponents_NilBufferedPassesThrough verifies the nil-input
// edge case: if the buffered Process attached no Components shell the
// per-chunk projection emits nil (not a synthetic empty shell). This
// preserves the omitempty wire shape — runs with no aggregations /
// groupers / filterers stream byte-identical to the pre-S3 baseline.
func TestChunkComponents_NilBufferedPassesThrough(t *testing.T) {
	mergeability := []descriptor.ComponentsMergeability{descriptor.Mergeable}
	if got := chunkComponents(nil, mergeability, false); got != nil {
		t.Fatalf("non-terminal nil buffered → %+v, want nil", got)
	}
	if got := chunkComponents(nil, mergeability, true); got != nil {
		t.Fatalf("terminal nil buffered → %+v, want nil", got)
	}
}

// TestAggMergeabilityVector_AlignsWithCapabilityTable pins the lookup
// helper against the descriptor capability table. AGG_SUM is
// Mergeable, AGG_FREQUENCY is Partial, AGG_MEDIAN is None — the
// vector must reflect those three classes in slot order.
func TestAggMergeabilityVector_AlignsWithCapabilityTable(t *testing.T) {
	req := &Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x"},
			{Type: types.AGG_FREQUENCY, Field: "y"},
			{Type: types.AGG_MEDIAN, Field: "z"},
		},
	}
	got := aggMergeabilityVector(req)
	want := []descriptor.ComponentsMergeability{
		descriptor.Mergeable,
		descriptor.Partial,
		descriptor.None,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggMergeabilityVector = %v, want %v", got, want)
	}
}
