package processing

import (
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// welfordSchema builds a single-field schema with a float64 column for
// the AGG_WELFORD happy-path tests.
func welfordSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "value", Type: encoding.FieldTypeF64},
		},
	}
}

// welfordRecord builds a Record carrying a single float64 value.
func welfordRecord(v float64) *Record {
	return NewRecordWithNulls(welfordSchema(), map[string]float64{"value": v}, nil)
}

// controlWelford pumps the same samples through a private welfordBucket
// to give the test an in-process numerical reference at the same float64
// precision the aggregator uses.
func controlWelford(samples []float64) (mean, variance float64, n uint64) {
	var b welfordBucket
	for _, v := range samples {
		b.add(v)
	}
	return b.mean, b.sampleVariance(), uint64(b.n)
}

// TestAggregatorWelford_BufferedMatchesWelfordBucket pumps 100 random
// samples through AGG_WELFORD's buffered path and asserts at
// math.Float64bits granularity against a parallel welfordBucket
// computation. This locks the aggregator to the same recurrence
// TEST_WELCH consumes per the S1 design doc.
func TestAggregatorWelford_BufferedMatchesWelfordBucket(t *testing.T) {
	rng := rand.New(rand.NewSource(0xDEADBEEF))
	samples := make([]float64, 100)
	records := make([]*Record, 100)
	for i := range samples {
		samples[i] = rng.NormFloat64()*7.0 + 42.0
		records[i] = welfordRecord(samples[i])
	}

	spec := &types.Aggregation{Type: types.AGG_WELFORD, Field: "value"}
	agg, err := newWelfordAggregator(spec, welfordSchema())
	if err != nil {
		t.Fatalf("newWelfordAggregator: %v", err)
	}

	scalar, err := agg.Aggregate(records, "value")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	wantMean, wantVar, wantN := controlWelford(samples)

	if math.Float64bits(scalar) != math.Float64bits(wantMean) {
		t.Errorf("Value scalar = %v, want %v (bit-identical to welfordBucket mean)", scalar, wantMean)
	}

	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	triple, ok := rich.(WelfordTriple)
	if !ok {
		t.Fatalf("Rich() returned %T, want WelfordTriple", rich)
	}
	if math.Float64bits(triple.Mean) != math.Float64bits(wantMean) {
		t.Errorf("Rich.Mean = %v, want %v (bit-identical)", triple.Mean, wantMean)
	}
	if math.Float64bits(triple.Variance) != math.Float64bits(wantVar) {
		t.Errorf("Rich.Variance = %v, want %v (bit-identical sample variance)", triple.Variance, wantVar)
	}
	if triple.N != wantN {
		t.Errorf("Rich.N = %d, want %d", triple.N, wantN)
	}
}

// TestAggregatorWelford_StreamingMatchesBuffered drives UpdateRow row
// by row, calls Finalize, and asserts the resulting (mean, variance, n)
// triple is bit-identical to the buffered Aggregate path. Locks
// OnlineAggregator parity with the buffered path.
func TestAggregatorWelford_StreamingMatchesBuffered(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))
	samples := make([]float64, 100)
	records := make([]*Record, 100)
	for i := range samples {
		samples[i] = rng.NormFloat64()*3.0 - 1.5
		records[i] = welfordRecord(samples[i])
	}
	spec := &types.Aggregation{Type: types.AGG_WELFORD, Field: "value"}

	bufAgg, _ := newWelfordAggregator(spec, welfordSchema())
	bufScalar, _ := bufAgg.Aggregate(records, "value")
	bufRich, _ := bufAgg.(RichAggregator).Rich()

	streamAgg, _ := newWelfordAggregator(spec, welfordSchema())
	stream := streamAgg.(*welfordAggregator)
	for _, r := range records {
		if err := stream.UpdateRow(r, "value"); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	streamScalar, err := stream.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	streamRich, _ := stream.Rich()

	if math.Float64bits(bufScalar) != math.Float64bits(streamScalar) {
		t.Errorf("scalar buffered=%v streamed=%v differ at bit level", bufScalar, streamScalar)
	}
	bufT := bufRich.(WelfordTriple)
	stT := streamRich.(WelfordTriple)
	if math.Float64bits(bufT.Mean) != math.Float64bits(stT.Mean) ||
		math.Float64bits(bufT.Variance) != math.Float64bits(stT.Variance) ||
		bufT.N != stT.N {
		t.Errorf("rich buffered=%+v streamed=%+v differ", bufT, stT)
	}
}

// TestAggregatorWelford_EmptyInputReturnsNaNAndNil locks the no-rows
// contract: scalar is NaN (so callers can distinguish "no rows" from
// "rows averaging zero") and Rich() returns (nil, nil) so the
// orchestrator falls through to the scalar path.
func TestAggregatorWelford_EmptyInputReturnsNaNAndNil(t *testing.T) {
	spec := &types.Aggregation{Type: types.AGG_WELFORD, Field: "value"}
	agg, err := newWelfordAggregator(spec, welfordSchema())
	if err != nil {
		t.Fatalf("newWelfordAggregator: %v", err)
	}
	scalar, err := agg.Aggregate(nil, "value")
	if err != nil {
		t.Fatalf("Aggregate(nil): %v", err)
	}
	if !math.IsNaN(scalar) {
		t.Errorf("Value() with N=0 = %v, want NaN", scalar)
	}
	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich(): %v", err)
	}
	if rich != nil {
		t.Errorf("Rich() with N=0 = %v, want nil", rich)
	}
}

// TestAggregatorWelford_SingleRowVarianceIsZero asserts the N<2 edge:
// mean equals the lone observation, variance is zero (mirrors
// welfordBucket.sampleVariance behaviour), N = 1.
func TestAggregatorWelford_SingleRowVarianceIsZero(t *testing.T) {
	spec := &types.Aggregation{Type: types.AGG_WELFORD, Field: "value"}
	agg, _ := newWelfordAggregator(spec, welfordSchema())
	_, err := agg.Aggregate([]*Record{welfordRecord(17.0)}, "value")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	rich, _ := agg.(RichAggregator).Rich()
	triple := rich.(WelfordTriple)
	if triple.Mean != 17.0 {
		t.Errorf("Rich.Mean = %v, want 17.0", triple.Mean)
	}
	if triple.Variance != 0 {
		t.Errorf("Rich.Variance = %v, want 0 (welfordBucket zero-on-N<2 contract)", triple.Variance)
	}
	if triple.N != 1 {
		t.Errorf("Rich.N = %d, want 1", triple.N)
	}
}

// TestAggregatorWelford_NullValuesSkipped folds three real values and
// two nulls; the resulting triple must match a parallel welfordBucket
// pumped with only the three non-null samples. Locks the
// "non-null rows contribute" denominator semantics.
func TestAggregatorWelford_NullValuesSkipped(t *testing.T) {
	schema := welfordSchema()
	mkNull := func() *Record {
		return NewRecordWithNulls(schema, map[string]float64{"value": 0}, map[string]bool{"value": true})
	}
	records := []*Record{
		welfordRecord(1.0),
		mkNull(),
		welfordRecord(2.0),
		mkNull(),
		welfordRecord(3.0),
	}

	spec := &types.Aggregation{Type: types.AGG_WELFORD, Field: "value"}
	agg, _ := newWelfordAggregator(spec, schema)
	_, _ = agg.Aggregate(records, "value")
	rich, _ := agg.(RichAggregator).Rich()
	got := rich.(WelfordTriple)

	wantMean, wantVar, wantN := controlWelford([]float64{1.0, 2.0, 3.0})
	if math.Float64bits(got.Mean) != math.Float64bits(wantMean) ||
		math.Float64bits(got.Variance) != math.Float64bits(wantVar) ||
		got.N != wantN {
		t.Errorf("rich after null skip = %+v, want {Mean:%v Variance:%v N:%d}", got, wantMean, wantVar, wantN)
	}
}

// TestAggregatorWelford_FactoryRejectsNonNumericFields confirms the
// field-type gate. The aggregator accepts only the strict scalar
// numeric family (u8/u16/u32/u64/f32/f64) per the S1 design doc;
// every other type — categorical, set, date, decimal128, packed_bool —
// must surface PROCESSING_CONFIG at factory time so the per-record
// hot path never has to branch on type.
func TestAggregatorWelford_FactoryRejectsNonNumericFields(t *testing.T) {
	dict := encoding.NewDictionary()
	if _, err := dict.Add("A"); err != nil {
		t.Fatalf("dict.Add: %v", err)
	}
	cases := []struct {
		name      string
		fieldType encoding.FieldType
	}{
		{"categorical_u8", encoding.FieldTypeCategoricalU8},
		{"set_u8", encoding.FieldTypeSetU8},
		{"date", encoding.FieldTypeDate},
		{"decimal128", encoding.FieldTypeDecimal128},
		{"packed_bool", encoding.FieldTypePackedBool},
		{"u4", encoding.FieldTypeU4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := encoding.Field{Name: "x", Type: tc.fieldType}
			if tc.fieldType.HasDictionary() {
				field.Dictionary = dict
			}
			schema := &encoding.Schema{Fields: []encoding.Field{field}}
			_, err := newWelfordAggregator(&types.Aggregation{Type: types.AGG_WELFORD, Field: "x"}, schema)
			if err == nil {
				t.Fatalf("factory accepted field of type %s, want PROCESSING_CONFIG", tc.fieldType)
			}
			ce, ok := err.(*errors.CodedError)
			if !ok {
				t.Fatalf("error %v is %T, want *errors.CodedError", err, err)
			}
			if ce.Code != errors.PROCESSING_CONFIG {
				t.Errorf("error code = %s, want PROCESSING_CONFIG", ce.Code)
			}
		})
	}
}

// TestAggregatorWelford_FactoryAcceptsAllNumericFields confirms the
// happy-path tier: every member of the strict scalar numeric family
// passes the factory gate.
func TestAggregatorWelford_FactoryAcceptsAllNumericFields(t *testing.T) {
	cases := []encoding.FieldType{
		encoding.FieldTypeU8,
		encoding.FieldTypeU16,
		encoding.FieldTypeU32,
		encoding.FieldTypeU64,
		encoding.FieldTypeF32,
		encoding.FieldTypeF64,
	}
	for _, ft := range cases {
		t.Run(ft.String(), func(t *testing.T) {
			schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: ft}}}
			_, err := newWelfordAggregator(&types.Aggregation{Type: types.AGG_WELFORD, Field: "x"}, schema)
			if err != nil {
				t.Fatalf("factory rejected %s: %v", ft, err)
			}
		})
	}
}

// TestAggregatorWelford_FactoryRequiresField asserts the empty-Field
// path fires PROCESSING_CONFIG (matches every other registered
// aggregator's required-field contract).
func TestAggregatorWelford_FactoryRequiresField(t *testing.T) {
	_, err := newWelfordAggregator(&types.Aggregation{Type: types.AGG_WELFORD}, welfordSchema())
	if err == nil {
		t.Fatal("factory accepted empty Field, want PROCESSING_CONFIG")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("error %v is %T, want *errors.CodedError", err, err)
	}
	if ce.Code != errors.PROCESSING_CONFIG {
		t.Errorf("error code = %s, want PROCESSING_CONFIG", ce.Code)
	}
}
