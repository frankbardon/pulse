package descriptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// kmPredictFile is a cohort of n records over two f64 members and a
// categorical (16-byte stride + 1).
func kmPredictFile(t *testing.T, n int) []byte {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 16, Dictionary: makeDictionary(t, "N", "S")},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, n*schema.RecordByteSize()))
	return buf.Bytes()
}

func kmPredictReq(params string) *types.Request {
	return &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"a", "b"}, Params: json.RawMessage(params)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}},
		Sort:         []types.OrderKey{{Field: "GROUP_KMEANS_a"}},
	}
}

// TestPredict_GroupKMeans: a GROUP_KMEANS request predicts valid and
// buffered, its key column visible to sort, its point store reported on
// the slot (n·p·8 + k·p·8) and folded into the memory estimate, and its
// k graded against MaxGroups; a shape refusal reaches predict with the
// runtime's code.
func TestPredict_GroupKMeans(t *testing.T) {
	t.Parallel()

	const n = 50
	file := kmPredictFile(t, n)
	env := Predict(bytes.NewReader(file), kmPredictReq(`{"k":4}`), nil)
	res := env.Data.(*descriptor.PredictResult)
	if !res.Valid || len(env.Errors) != 0 {
		t.Fatalf("invalid: %+v", env.Errors)
	}
	if res.Streamable || !strings.Contains(strings.Join(res.StreamableReasons, " "), "group GROUP_KMEANS requires the buffered path") {
		t.Fatalf("streamable %v reasons %v, want buffered", res.Streamable, res.StreamableReasons)
	}
	if len(res.Groups) != 1 || res.Groups[0].RowBufferBytes == nil {
		t.Fatalf("groups %+v, want a row_buffer_bytes", res.Groups)
	}
	if got, want := *res.Groups[0].RowBufferBytes, int64(n*2*8+4*2*8); got != want {
		t.Fatalf("row_buffer_bytes %d, want %d", got, want)
	}
	if !res.Groups[0].BufferedComponents {
		t.Fatal("GROUP_KMEANS components must be buffered (mergeability none)")
	}

	// The memory estimate carries the term.
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64}, {Name: "b", Type: encoding.FieldTypeF64},
	}}
	with, ok := EstimateMemory(kmPredictReq(`{"k":4}`), schema, nil, LimitInputs{Records: 1000, JoinRightRows: -1})
	plain := &types.Request{Groups: []*types.Group{{Type: types.GROUP_QUANTILE, Field: "a", Interval: 4}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}}
	without, ok2 := EstimateMemory(plain, schema, nil, LimitInputs{Records: 1000, JoinRightRows: -1})
	if !ok || !ok2 || with-without != limits.KMeansBufferBytes(1000, 2, 4) {
		t.Fatalf("memory with %d without %d: difference %d, want %d", with, without, with-without, limits.KMeansBufferBytes(1000, 2, 4))
	}

	// MaxGroups grades params.k.
	l := limits.Defaults()
	l.MaxGroups = 3
	env = Predict(bytes.NewReader(file), kmPredictReq(`{"k":4}`), &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithLimits(l)})
	res = env.Data.(*descriptor.PredictResult)
	if len(res.LimitFindings) != 1 || res.LimitFindings[0].Limit != "max_groups" || res.LimitFindings[0].Estimated != 4 {
		t.Fatalf("findings %+v, want max_groups estimated 4", res.LimitFindings)
	}

	// Shape refusals match the runtime's field-reference refusal.
	for _, c := range []struct {
		name   string
		mut    func(*types.Request)
		reason string
	}{
		{"field set", func(r *types.Request) { r.Groups[0].Field, r.Sort = "a", nil }, "field_on_vector_group"},
		{"no k", func(r *types.Request) { r.Groups[0].Params = nil }, "k_required"},
	} {
		req := kmPredictReq(`{"k":4}`)
		c.mut(req)
		env := Predict(bytes.NewReader(file), req, nil)
		if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.SERVICE_VALIDATION) || env.Errors[0].Details["reason"] != c.reason {
			t.Fatalf("%s: errors %+v, want SERVICE_VALIDATION %s", c.name, env.Errors, c.reason)
		}
	}

	// A probability weight is refused permanently with a reason; a
	// frequency weight is applied.
	req := kmPredictReq(`{"k":2}`)
	req.Weight = &types.WeightSpec{Field: "a", Kind: types.WeightKindProbability}
	env = Predict(bytes.NewReader(file), req, nil)
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_WEIGHT_UNSUPPORTED) || env.Errors[0].Details["reason"] == nil {
		t.Fatalf("probability weight: errors %+v, want PULSE_WEIGHT_UNSUPPORTED with a reason", env.Errors)
	}
}
