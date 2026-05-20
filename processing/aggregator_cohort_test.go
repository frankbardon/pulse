package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func cohortSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "value", Type: encoding.FieldTypeF64},
			{Name: "weight", Type: encoding.FieldTypeF64},
			{Name: "num", Type: encoding.FieldTypeF64},
			{Name: "den", Type: encoding.FieldTypeF64},
		},
	}
}

func cohortRec(value, weight, num, den float64, nulls map[string]bool) *Record {
	s := cohortSchema()
	vals := map[string]float64{
		"value":  value,
		"weight": weight,
		"num":    num,
		"den":    den,
	}
	if nulls == nil {
		nulls = map[string]bool{}
	}
	return NewRecordWithNulls(s, vals, nulls)
}

func TestAggregator_WeightedMean_Streaming(t *testing.T) {
	spec := &types.Aggregation{
		Type:   types.AGG_WEIGHTED_MEAN,
		Field:  "value",
		Params: json.RawMessage(`{"weight_field":"weight"}`),
	}
	agg, err := newWeightedMeanAggregator(spec, cohortSchema())
	if err != nil {
		t.Fatal(err)
	}
	wm := agg.(*weightedMeanAggregator)

	// values 10, 20, 30 with weights 1, 1, 2 → (10+20+60)/4 = 22.5
	records := []*Record{
		cohortRec(10, 1, 0, 0, nil),
		cohortRec(20, 1, 0, 0, nil),
		cohortRec(30, 2, 0, 0, nil),
	}
	for _, r := range records {
		if err := wm.UpdateRow(r, "value"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := wm.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-22.5) > 1e-9 {
		t.Errorf("weighted_mean = %v, want 22.5", got)
	}
}

func TestAggregator_WeightedMean_BufferedMatchesStreaming(t *testing.T) {
	spec := &types.Aggregation{
		Type:   types.AGG_WEIGHTED_MEAN,
		Field:  "value",
		Params: json.RawMessage(`{"weight_field":"weight"}`),
	}
	agg, _ := newWeightedMeanAggregator(spec, cohortSchema())
	records := []*Record{
		cohortRec(1.5, 0.5, 0, 0, nil),
		cohortRec(4.0, 2.0, 0, 0, nil),
		cohortRec(7.0, 1.5, 0, 0, nil),
	}
	buffered, err := agg.Aggregate(records, "value")
	if err != nil {
		t.Fatal(err)
	}

	agg2, _ := newWeightedMeanAggregator(spec, cohortSchema())
	stream := agg2.(*weightedMeanAggregator)
	for _, r := range records {
		stream.UpdateRow(r, "value")
	}
	streamed, _ := stream.Finalize()
	if math.Abs(buffered-streamed) > 1e-9 {
		t.Errorf("buffered=%v streamed=%v differ", buffered, streamed)
	}
}

func TestAggregator_WeightedMean_MergeMatchesSerial(t *testing.T) {
	spec := &types.Aggregation{
		Type:   types.AGG_WEIGHTED_MEAN,
		Field:  "value",
		Params: json.RawMessage(`{"weight_field":"weight"}`),
	}
	all := []*Record{
		cohortRec(1, 1, 0, 0, nil),
		cohortRec(2, 2, 0, 0, nil),
		cohortRec(3, 3, 0, 0, nil),
		cohortRec(10, 1, 0, 0, nil),
		cohortRec(11, 1, 0, 0, nil),
	}

	serial, _ := newWeightedMeanAggregator(spec, nil)
	s := serial.(*weightedMeanAggregator)
	for _, r := range all {
		s.UpdateRow(r, "value")
	}
	serialOut, _ := s.Finalize()

	left, _ := newWeightedMeanAggregator(spec, nil)
	right, _ := newWeightedMeanAggregator(spec, nil)
	for _, r := range all[:3] {
		left.(*weightedMeanAggregator).UpdateRow(r, "value")
	}
	for _, r := range all[3:] {
		right.(*weightedMeanAggregator).UpdateRow(r, "value")
	}
	if err := left.(*weightedMeanAggregator).MergeOnline(right.(*weightedMeanAggregator)); err != nil {
		t.Fatal(err)
	}
	mergedOut, _ := left.(*weightedMeanAggregator).Finalize()
	if math.Abs(serialOut-mergedOut) > 1e-9 {
		t.Errorf("serial=%v merged=%v", serialOut, mergedOut)
	}
}

func TestAggregator_WeightedMean_MissingWeightFieldErrors(t *testing.T) {
	_, err := newWeightedMeanAggregator(&types.Aggregation{Type: types.AGG_WEIGHTED_MEAN, Field: "value"}, nil)
	if err == nil || !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Fatalf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

func TestAggregator_Ratio_Streaming(t *testing.T) {
	spec := &types.Aggregation{
		Type:   types.AGG_RATIO,
		Field:  "value", // ignored
		Params: json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
	}
	agg, err := newRatioAggregator(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := agg.(*ratioAggregator)
	records := []*Record{
		cohortRec(0, 0, 10, 2, nil),
		cohortRec(0, 0, 30, 8, nil),
	}
	for _, rec := range records {
		r.UpdateRow(rec, "value")
	}
	got, _ := r.Finalize()
	if math.Abs(got-4.0) > 1e-9 {
		t.Errorf("ratio = %v, want 4.0 (40/10)", got)
	}
}

func TestAggregator_Ratio_NaNWhenDenZero(t *testing.T) {
	spec := &types.Aggregation{
		Type:   types.AGG_RATIO,
		Field:  "value",
		Params: json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
	}
	agg, _ := newRatioAggregator(spec, nil)
	r := agg.(*ratioAggregator)
	r.UpdateRow(cohortRec(0, 0, 5, 0, nil), "value")
	got, _ := r.Finalize()
	if !math.IsNaN(got) {
		t.Errorf("expected NaN on zero denominator, got %v", got)
	}
}

func TestAggregator_CI_NormalBracketsMean(t *testing.T) {
	specLow := &types.Aggregation{
		Type:   types.AGG_CI_LOWER,
		Field:  "value",
		Params: json.RawMessage(`{"confidence":0.95,"method":"normal"}`),
	}
	specUp := &types.Aggregation{
		Type:   types.AGG_CI_UPPER,
		Field:  "value",
		Params: json.RawMessage(`{"confidence":0.95,"method":"normal"}`),
	}
	low, err := newCIAggregator(ciLower)(specLow, nil)
	if err != nil {
		t.Fatal(err)
	}
	up, err := newCIAggregator(ciUpper)(specUp, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Symmetric sample around 10 with stddev > 0
	values := []float64{8, 9, 10, 11, 12}
	for _, v := range values {
		low.(*ciAggregator).UpdateRow(cohortRec(v, 0, 0, 0, nil), "value")
		up.(*ciAggregator).UpdateRow(cohortRec(v, 0, 0, 0, nil), "value")
	}
	lo, _ := low.(*ciAggregator).Finalize()
	hi, _ := up.(*ciAggregator).Finalize()
	if !(lo < 10 && 10 < hi) {
		t.Errorf("expected lower=%v < 10 < upper=%v", lo, hi)
	}
	if hi-10 != 10-lo {
		// CI is symmetric around mean for normal method
		if math.Abs((hi-10)-(10-lo)) > 1e-9 {
			t.Errorf("CI not symmetric: lo=%v hi=%v", lo, hi)
		}
	}
}

func TestAggregator_CI_BootstrapReserved(t *testing.T) {
	_, err := newCIAggregator(ciLower)(&types.Aggregation{
		Type:   types.AGG_CI_LOWER,
		Field:  "value",
		Params: json.RawMessage(`{"method":"bootstrap"}`),
	}, nil)
	if err == nil || !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Fatalf("expected PROCESSING_CONFIG for bootstrap method, got: %v", err)
	}
}

func TestAggregator_CI_InvalidConfidence(t *testing.T) {
	_, err := newCIAggregator(ciLower)(&types.Aggregation{
		Type:   types.AGG_CI_LOWER,
		Field:  "value",
		Params: json.RawMessage(`{"confidence":1.5}`),
	}, nil)
	if err == nil || !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Fatalf("expected PROCESSING_CONFIG for confidence>=1, got: %v", err)
	}
}

func TestAggregator_CI_NaNUnderTwoSamples(t *testing.T) {
	low, _ := newCIAggregator(ciLower)(&types.Aggregation{
		Type:   types.AGG_CI_LOWER,
		Field:  "value",
		Params: json.RawMessage(`{"confidence":0.95}`),
	}, nil)
	low.(*ciAggregator).UpdateRow(cohortRec(5, 0, 0, 0, nil), "value")
	got, _ := low.(*ciAggregator).Finalize()
	if !math.IsNaN(got) {
		t.Errorf("expected NaN for n<2, got %v", got)
	}
}

func TestNormalQuantile_KnownPoints(t *testing.T) {
	cases := []struct {
		p, want, tol float64
	}{
		{0.5, 0.0, 1e-9},
		{0.975, 1.959964, 1e-3},
		{0.995, 2.575829, 1e-3},
		{0.025, -1.959964, 1e-3},
	}
	for _, c := range cases {
		got := normalQuantile(c.p)
		if math.Abs(got-c.want) > c.tol {
			t.Errorf("normalQuantile(%v) = %v, want %v ± %v", c.p, got, c.want, c.tol)
		}
	}
}
