package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// orderedKeys extracts the group-field value from each Data row in
// payload order.
func orderedKeys(data []map[string]any, groupField string) []string {
	out := make([]string, 0, len(data))
	for _, row := range data {
		k, _ := row[groupField].(string)
		out = append(out, k)
	}
	return out
}

// TestGroupedInclude_CategoryPayloadFollowsIncludeOrder: a plain (non-
// crosstab) GROUP_CATEGORY request with include ["Google","Apple"] must
// emit Response.Data rows Google then Apple — include order, NOT the
// alphabetical sort.Strings default (which would give Apple, Google).
func TestGroupedInclude_CategoryPayloadFollowsIncludeOrder(t *testing.T) {
	schema := categoricalSchema() // Apple=0, Samsung=1, Google=2
	records := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0, "score": 1}), // Apple
		NewRecord(schema, map[string]float64{"brand": 2, "score": 2}), // Google
		NewRecord(schema, map[string]float64{"brand": 0, "score": 3}), // Apple
		NewRecord(schema, map[string]float64{"brand": 1, "score": 4}), // Samsung — dropped
	}
	req := &types.Request{
		Groups: []*types.Group{{
			Type:    types.GROUP_CATEGORY,
			Field:   "brand",
			Include: []string{"Google", "Apple"},
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	}

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered processRecords: %v", err)
	}
	wantOrder := []string{"Google", "Apple"}
	if got := orderedKeys(bufResp.Data, "brand"); !sliceEqual(got, wantOrder) {
		t.Errorf("buffered Data order = %v, want %v", got, wantOrder)
	}

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming Process: %v", err)
	}
	if stream.LastPath() != PathStreaming {
		t.Fatalf("expected streaming path, got %s", stream.LastPath())
	}
	if got := orderedKeys(streamResp.Data, "brand"); !sliceEqual(got, wantOrder) {
		t.Errorf("streaming Data order = %v, want %v", got, wantOrder)
	}
}

// TestGroupedInclude_StreamingMatchesBufferedOrder: for the same include
// request, the streaming payload row order must equal the buffered order.
func TestGroupedInclude_StreamingMatchesBufferedOrder(t *testing.T) {
	schema := categoricalSchema()
	records := []*Record{
		NewRecord(schema, map[string]float64{"brand": 1, "score": 1}), // Samsung
		NewRecord(schema, map[string]float64{"brand": 2, "score": 2}), // Google
		NewRecord(schema, map[string]float64{"brand": 0, "score": 3}), // Apple
	}
	req := &types.Request{
		Groups: []*types.Group{{
			Type:    types.GROUP_CATEGORY,
			Field:   "brand",
			Include: []string{"Samsung", "Google", "Apple"},
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	}

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}

	bufOrder := orderedKeys(bufResp.Data, "brand")
	streamOrder := orderedKeys(streamResp.Data, "brand")
	want := []string{"Samsung", "Google", "Apple"}
	if !sliceEqual(bufOrder, want) {
		t.Errorf("buffered order = %v, want %v", bufOrder, want)
	}
	if !sliceEqual(streamOrder, bufOrder) {
		t.Errorf("streaming order %v != buffered order %v", streamOrder, bufOrder)
	}
}

// TestGroupedInclude_ExplicitSortOverridesIncludeOrder: include order is
// the DEFAULT — an explicit req.Sort still wins.
func TestGroupedInclude_ExplicitSortOverridesIncludeOrder(t *testing.T) {
	schema := categoricalSchema()
	records := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0, "score": 5}), // Apple
		NewRecord(schema, map[string]float64{"brand": 2, "score": 1}), // Google
	}
	req := &types.Request{
		Groups: []*types.Group{{
			Type:    types.GROUP_CATEGORY,
			Field:   "brand",
			Include: []string{"Google", "Apple"}, // include order = Google, Apple
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "n"}},
		// Explicit descending sort on the aggregate: Apple(5) before Google(1).
		// Include order would be Google, Apple — so a passing test proves the
		// explicit Sort overrides include order.
		Sort: []types.OrderKey{{Field: "n", Desc: true}},
	}
	wantOrder := []string{"Apple", "Google"} // by n desc: Apple(5), Google(1)

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	if got := orderedKeys(bufResp.Data, "brand"); !sliceEqual(got, wantOrder) {
		t.Errorf("buffered Sort override order = %v, want %v", got, wantOrder)
	}

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}
	if got := orderedKeys(streamResp.Data, "brand"); !sliceEqual(got, wantOrder) {
		t.Errorf("streaming Sort override order = %v, want %v", got, wantOrder)
	}
}

// TestGroupedInclude_SetValuePayloadFollowsIncludeOrder covers
// GROUP_SET_VALUE (composite keys).
func TestGroupedInclude_SetValuePayloadFollowsIncludeOrder(t *testing.T) {
	schema := makeSetTestSchema(t) // VISA=0, MC=1, AMEX=2, DISC=3
	records := []*Record{
		makeSetRecord(schema, 0b0100), // AMEX
		makeSetRecord(schema, 0b0011), // MC|VISA
		makeSetRecord(schema, 0b0011), // MC|VISA
	}
	// Include composite keys out of alphabetical order.
	req := &types.Request{
		Groups: []*types.Group{{
			Type:    types.GROUP_SET_VALUE,
			Field:   "tags",
			Include: []string{"MC|VISA", "AMEX"},
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "tags", Label: "n"}},
	}
	wantOrder := []string{"MC|VISA", "AMEX"}

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	if got := orderedKeys(bufResp.Data, "tags"); !sliceEqual(got, wantOrder) {
		t.Errorf("buffered set-value order = %v, want %v", got, wantOrder)
	}

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}
	if got := orderedKeys(streamResp.Data, "tags"); !sliceEqual(got, wantOrder) {
		t.Errorf("streaming set-value order = %v, want %v", got, wantOrder)
	}
}

// TestGroupedInclude_SetPerElementPayloadFollowsIncludeOrder covers
// GROUP_SET_PER_ELEMENT (row fans into multiple element buckets;
// MultiKeyStreamingGrouper streaming path).
func TestGroupedInclude_SetPerElementPayloadFollowsIncludeOrder(t *testing.T) {
	schema := makeSetTestSchema(t) // VISA=0, MC=1, AMEX=2, DISC=3
	records := []*Record{
		makeSetRecord(schema, 0b0111), // VISA + MC + AMEX
		makeSetRecord(schema, 0b0100), // AMEX
		makeSetRecord(schema, 0b0001), // VISA
	}
	// Include element labels out of alphabetical order.
	req := &types.Request{
		Groups: []*types.Group{{
			Type:    types.GROUP_SET_PER_ELEMENT,
			Field:   "tags",
			Include: []string{"VISA", "AMEX"},
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "tags", Label: "n"}},
	}
	wantOrder := []string{"VISA", "AMEX"}

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	if got := orderedKeys(bufResp.Data, "tags"); !sliceEqual(got, wantOrder) {
		t.Errorf("buffered set-per-element order = %v, want %v", got, wantOrder)
	}

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}
	if stream.LastPath() != PathStreaming {
		t.Fatalf("expected streaming path, got %s", stream.LastPath())
	}
	if got := orderedKeys(streamResp.Data, "tags"); !sliceEqual(got, wantOrder) {
		t.Errorf("streaming set-per-element order = %v, want %v", got, wantOrder)
	}
}
