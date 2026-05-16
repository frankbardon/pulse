package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestShardArchivePredict — non-skippable gate. Predict on a shard
// archive reports the cumulative record total and Shards listing, and
// streamability is inherited from the request against the canonical
// schema (unchanged from single-file behavior).
func TestShardArchivePredict(t *testing.T) {
	schema := twoShardInspectSchema(t)
	shards := []struct {
		Name    string
		NRecord int
	}{
		{Name: "p1.pulse", NRecord: 4},
		{Name: "p2.pulse", NRecord: 6},
	}
	data := buildShardArchiveBytes(t, schema, shards)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatal("Data is not *PredictResult")
	}
	if !result.Valid {
		t.Errorf("Valid = false, want true")
	}

	// SchemaInfo came from canonical _schema.pulse.
	if result.SchemaInfo == nil || result.SchemaInfo.FieldCount != 2 {
		t.Fatalf("SchemaInfo wrong: %+v", result.SchemaInfo)
	}

	// Cumulative record total.
	if result.RecordCount != 10 {
		t.Errorf("RecordCount = %d, want 10", result.RecordCount)
	}
	// Shards in central-directory order.
	if len(result.Shards) != 2 {
		t.Fatalf("Shards len = %d, want 2", len(result.Shards))
	}
	if result.Shards[0].Filename != "p1.pulse" || result.Shards[0].RecordCount != 4 {
		t.Errorf("Shards[0] = %+v", result.Shards[0])
	}
	if result.Shards[1].Filename != "p2.pulse" || result.Shards[1].RecordCount != 6 {
		t.Errorf("Shards[1] = %+v", result.Shards[1])
	}

	// AGG_SUM on a numeric field is streamable (online aggregator).
	if !result.Streamable {
		t.Errorf("Streamable = false, want true; reasons=%v", result.StreamableReasons)
	}
}

// TestShardArchivePredict_BufferedRequestBuffered confirms streamability
// inheritance: AGG_PERCENTILE buffers regardless of the cohort shape.
func TestShardArchivePredict_BufferedRequestBuffered(t *testing.T) {
	schema := twoShardInspectSchema(t)
	data := buildShardArchiveBytes(t, schema, []struct {
		Name    string
		NRecord int
	}{
		{Name: "x.pulse", NRecord: 5},
	})

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_PERCENTILE, Field: "score", Label: "p50"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Errorf("AGG_PERCENTILE marked Streamable; want buffered. reasons=%v",
			result.StreamableReasons)
	}
}

// TestShardArchivePredict_SingleFileUnchanged confirms the single-file
// path keeps Shards empty (non-nil) and RecordCount = 0.
func TestShardArchivePredict_SingleFileUnchanged(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Test score for the participant"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	result := env.Data.(*PredictResult)
	if result.Shards == nil {
		t.Fatal("Shards nil; want empty slice")
	}
	if len(result.Shards) != 0 {
		t.Errorf("Shards len = %d, want 0", len(result.Shards))
	}
	if result.RecordCount != 0 {
		t.Errorf("RecordCount = %d, want 0", result.RecordCount)
	}
}
