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

	terminal := chunkComponents(buffered, mergeability, nil, true)
	if terminal != buffered {
		t.Fatalf("terminal projection should return buffered pointer; got %p want %p",
			terminal, buffered)
	}

	mid := chunkComponents(buffered, mergeability, nil, false)
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
	if got := chunkComponents(nil, mergeability, nil, false); got != nil {
		t.Fatalf("non-terminal nil buffered → %+v, want nil", got)
	}
	if got := chunkComponents(nil, mergeability, nil, true); got != nil {
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

// TestGrpMergeabilityVector_AlignsWithCapabilityTable pins the
// grouper-axis lookup against the descriptor capability table.
// GROUP_CATEGORY is Mergeable, GROUP_QUANTILE is None — the vector
// must reflect both classes in slot order. Mirrors the aggregator-axis
// gate and exercises the new GroupMergeability helper.
func TestGrpMergeabilityVector_AlignsWithCapabilityTable(t *testing.T) {
	req := &Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
			{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4},
		},
	}
	got := grpMergeabilityVector(req)
	want := []descriptor.ComponentsMergeability{
		descriptor.Mergeable,
		descriptor.None,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grpMergeabilityVector = %v, want %v", got, want)
	}
}

// TestChunkComponents_NonTerminalRedactsNonMergeableGrouper pins the
// grouper-axis projection: a non-terminal chunk strips the Operator
// map from a GROUP_QUANTILE slot (Mergeability=None) while preserving
// the GROUP_CATEGORY slot's Operator (Mergeability=Mergeable) and the
// universal floor (Field / Label / TotalN / NNull) on every entry.
// Terminal projection passes the buffered shell through verbatim.
func TestChunkComponents_NonTerminalRedactsNonMergeableGrouper(t *testing.T) {
	buffered := &types.ResponseComponents{
		Groupers: []types.GrouperComponents{
			{
				Field:  "region",
				Label:  "region_group",
				TotalN: 10,
				NNull:  0,
				Operator: map[string]any{
					"dict_size": 3,
				},
			},
			{
				Field:  "score",
				Label:  "score_quartile",
				TotalN: 10,
				NNull:  0,
				Operator: map[string]any{
					"n_quantiles": 4,
					"method":      "linear",
					"edges":       []float64{2.5, 5.0, 7.5},
				},
			},
		},
	}
	grpMerge := []descriptor.ComponentsMergeability{
		descriptor.Mergeable,
		descriptor.None,
	}

	terminal := chunkComponents(buffered, nil, grpMerge, true)
	if terminal != buffered {
		t.Fatalf("terminal projection should return buffered pointer; got %p want %p",
			terminal, buffered)
	}

	mid := chunkComponents(buffered, nil, grpMerge, false)
	if mid == nil {
		t.Fatal("non-terminal projection nil")
	}
	if mid == buffered {
		t.Fatal("non-terminal projection should clone shell, not share pointer with buffered")
	}
	if len(mid.Groupers) != 2 {
		t.Fatalf("Groupers len = %d, want 2", len(mid.Groupers))
	}
	if mid.Groupers[0].Operator == nil {
		t.Fatal("mergeable GROUP_CATEGORY Operator stripped; want preserved")
	}
	if got := mid.Groupers[0].Operator["dict_size"]; got != 3 {
		t.Fatalf("mergeable GROUP_CATEGORY 'dict_size' = %v, want 3", got)
	}
	if mid.Groupers[1].Operator != nil {
		t.Fatalf("non-mergeable GROUP_QUANTILE Operator = %v, want nil",
			mid.Groupers[1].Operator)
	}
	// Universal floor preserved on the redacted slot.
	if mid.Groupers[1].TotalN != 10 || mid.Groupers[1].Field != "score" ||
		mid.Groupers[1].Label != "score_quartile" {
		t.Fatalf("universal floor stripped from redacted slot: %+v", mid.Groupers[1])
	}
	// Buffered original must remain untouched.
	if buffered.Groupers[1].Operator == nil {
		t.Fatal("buffered Components mutated; non-terminal projection should clone")
	}
}

// TestChunkComponents_AllMergeableGroupersSharesSlice pins the
// allocation-bounded optimisation in chunkComponents: when no grouper
// slot is None-mergeable, the non-terminal projection shares the
// Groupers slice by reference with the buffered original rather than
// cloning. Mid-stream consumers see the same backing array; only the
// shell wrapper is fresh.
func TestChunkComponents_AllMergeableGroupersSharesSlice(t *testing.T) {
	buffered := &types.ResponseComponents{
		Groupers: []types.GrouperComponents{
			{
				Field:    "region",
				TotalN:   5,
				Operator: map[string]any{"dict_size": 2},
			},
		},
	}
	grpMerge := []descriptor.ComponentsMergeability{descriptor.Mergeable}

	mid := chunkComponents(buffered, nil, grpMerge, false)
	if mid == nil {
		t.Fatal("non-terminal projection nil")
	}
	if mid == buffered {
		t.Fatal("non-terminal projection should clone shell wrapper, not share pointer")
	}
	if len(mid.Groupers) != 1 {
		t.Fatalf("Groupers len = %d, want 1", len(mid.Groupers))
	}
	if mid.Groupers[0].Operator == nil {
		t.Fatal("mergeable Operator stripped; want preserved")
	}
}

// TestProcessStreamResult_MixedMergeabilityComponents pins the
// end-to-end mixed-mergeability streaming contract: a Request carrying
// AGG_AVERAGE (Mergeable) + AGG_MEDIAN (None) emits a terminal chunk
// whose Components carries both slots' Operator maps populated and
// matches the buffered Process Response.Components byte-for-byte.
// Ungrouped Process emits a single result row → single terminal chunk;
// the non-terminal-redaction path for the same shape is unit-tested in
// TestChunkComponents_NonTerminalRedactsNonMergeable.
func TestProcessStreamResult_MixedMergeabilityComponents(t *testing.T) {
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
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
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
		t.Fatalf("got %d chunks, want 1 (ungrouped Process)", len(chunks))
	}

	// Terminal chunk: both Operators populated.
	terminal := chunks[0].Components
	if terminal == nil || len(terminal.Aggregations) != 2 {
		t.Fatalf("terminal: missing Components.Aggregations; got %+v", terminal)
	}
	if terminal.Aggregations[0].Label != "avg_score" {
		t.Fatalf("terminal slot 0 Label = %q, want avg_score", terminal.Aggregations[0].Label)
	}
	if terminal.Aggregations[0].Operator == nil {
		t.Fatal("terminal: AGG_AVERAGE Operator nil; want populated")
	}
	if terminal.Aggregations[1].Label != "median_score" {
		t.Fatalf("terminal slot 1 Label = %q, want median_score", terminal.Aggregations[1].Label)
	}
	if terminal.Aggregations[1].Operator == nil {
		t.Fatal("terminal: AGG_MEDIAN Operator nil; want populated on terminal")
	}
	if _, ok := terminal.Aggregations[1].Operator["median"]; !ok {
		t.Fatalf("terminal AGG_MEDIAN Operator missing 'median': %v",
			terminal.Aggregations[1].Operator)
	}

	// Streaming-vs-buffered terminal parity: terminal chunk's
	// Components is byte-equal to buffered Process's Components.
	buffered, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !reflect.DeepEqual(terminal, buffered.Components) {
		t.Fatalf("terminal Components != buffered:\nstream:   %+v\nbuffered: %+v",
			terminal, buffered.Components)
	}
}

// TestChunkComponents_MixedMergeabilityAggregations is the unit-level
// sibling of TestProcessStreamResult_MixedMergeabilityComponents.
// Drives chunkComponents directly with a mixed mergeability vector
// (AGG_AVERAGE Mergeable, AGG_MEDIAN None) and asserts the
// non-terminal projection keeps the AVERAGE Operator populated while
// stripping the MEDIAN Operator. Captures the same per-chunk contract
// the orchestrator enforces, decoupled from the streaming machinery
// that buffers all result rows into a single terminal chunk for
// ungrouped Process.
func TestChunkComponents_MixedMergeabilityAggregations(t *testing.T) {
	buffered := &types.ResponseComponents{
		Aggregations: []types.AggregationComponents{
			{
				Label:    "avg_score",
				N:        6,
				NNull:    0,
				Operator: map[string]any{"sum": 210.0, "mean": 35.0},
			},
			{
				Label: "median_score",
				N:     6,
				NNull: 0,
				Operator: map[string]any{
					"median":        35.0,
					"position_low":  2,
					"position_high": 3,
				},
			},
		},
	}
	aggMerge := []descriptor.ComponentsMergeability{
		descriptor.Mergeable,
		descriptor.None,
	}

	mid := chunkComponents(buffered, aggMerge, nil, false)
	if mid == nil || len(mid.Aggregations) != 2 {
		t.Fatalf("non-terminal Components missing Aggregations; got %+v", mid)
	}
	// AVERAGE: Operator preserved with running state.
	if mid.Aggregations[0].Operator == nil {
		t.Fatal("mergeable AGG_AVERAGE Operator nil; want populated mid-stream")
	}
	if got := mid.Aggregations[0].Operator["sum"]; got != 210.0 {
		t.Fatalf("AGG_AVERAGE 'sum' mid-stream = %v, want 210.0", got)
	}
	// MEDIAN: Operator stripped; universal floor preserved.
	if mid.Aggregations[1].Operator != nil {
		t.Fatalf("non-mergeable AGG_MEDIAN Operator = %v, want nil",
			mid.Aggregations[1].Operator)
	}
	if mid.Aggregations[1].N != 6 || mid.Aggregations[1].Label != "median_score" {
		t.Fatalf("AGG_MEDIAN universal floor stripped: %+v", mid.Aggregations[1])
	}

	// Terminal: pointer-identity passthrough.
	terminal := chunkComponents(buffered, aggMerge, nil, true)
	if terminal != buffered {
		t.Fatal("terminal projection should return buffered pointer")
	}
}

// TestProcessStreamResult_QuantileGrouperPerChunkComponents pins the
// GROUP_QUANTILE per-chunk suppression contract: every non-terminal
// chunk's Components.Groupers entry for the quantile slot carries the
// universal floor (Field / TotalN / NNull) but a nil Operator map. The
// terminal chunk emits the full Operator (n_quantiles / method /
// edges / buckets) and matches the buffered Process result.
func TestProcessStreamResult_QuantileGrouperPerChunkComponents(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse",
		[]string{"score"},
		[][]string{
			{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
			{"60"}, {"70"}, {"80"}, {"90"}, {"100"},
			{"110"}, {"120"},
		})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Cohort: &types.Cohort{Filename: "rows.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
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
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want >=2 (quartile partition over 12 rows)", len(chunks))
	}

	// Non-terminal chunks: grouper Operator nil; universal floor preserved.
	for i := 0; i < len(chunks)-1; i++ {
		c := chunks[i].Components
		if c == nil || len(c.Groupers) != 1 {
			t.Fatalf("chunk %d: missing Components.Groupers; got %+v", i, c)
		}
		if c.Groupers[0].Field != "score" {
			t.Fatalf("chunk %d: Groupers[0].Field = %q, want score", i, c.Groupers[0].Field)
		}
		if c.Groupers[0].Operator != nil {
			t.Fatalf("chunk %d: GROUP_QUANTILE Operator = %v, want nil",
				i, c.Groupers[0].Operator)
		}
	}

	// Terminal chunk: Operator populated with quantile keys.
	terminal := chunks[len(chunks)-1].Components
	if terminal == nil || len(terminal.Groupers) != 1 {
		t.Fatalf("terminal: missing Components.Groupers; got %+v", terminal)
	}
	op := terminal.Groupers[0].Operator
	if op == nil {
		t.Fatal("terminal: GROUP_QUANTILE Operator nil; want populated")
	}
	if _, ok := op["n_quantiles"]; !ok {
		t.Fatalf("terminal Operator missing 'n_quantiles': %v", op)
	}

	// Streaming-vs-buffered terminal parity.
	buffered, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !reflect.DeepEqual(terminal, buffered.Components) {
		t.Fatalf("terminal Components != buffered:\nstream:   %+v\nbuffered: %+v",
			terminal, buffered.Components)
	}
}

// TestPredict_QuantileGroupBufferedComponents pins the predict surface
// for GROUP_QUANTILE: the per-slot GroupPredict carries
// BufferedComponents=true, mirroring the descriptor capability table's
// ComponentSchema.Mergeability=None declaration. This is the predict
// hint streaming consumers branch on to budget the per-chunk
// suppression behaviour without consulting the manifest at runtime.
func TestPredict_QuantileGroupBufferedComponents(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse",
		[]string{"score"},
		[][]string{
			{"10"}, {"20"}, {"30"}, {"40"},
		})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Cohort: &types.Cohort{Filename: "rows.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "score"},
			{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score"},
		},
	}
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if len(pr.Groups) != 2 {
		t.Fatalf("Predict.Groups len = %d, want 2", len(pr.Groups))
	}
	if pr.Groups[0].BufferedComponents {
		t.Errorf("GROUP_CATEGORY BufferedComponents = true; want false (mergeable)")
	}
	if !pr.Groups[1].BufferedComponents {
		t.Errorf("GROUP_QUANTILE BufferedComponents = false; want true (None)")
	}
	if pr.Groups[1].ComponentSchema.Mergeability != descriptor.None {
		t.Errorf("GROUP_QUANTILE ComponentSchema.Mergeability = %q, want %q",
			pr.Groups[1].ComponentSchema.Mergeability, descriptor.None)
	}
}
