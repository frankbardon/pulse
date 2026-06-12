package processing

import (
	"math"
	"reflect"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the Facet-side population resolver
// (processing/overlay_facet_pop.go).
//
// E5-S1 scope: structural foundation for the Facet overlay kinds that
// consume the OverlayRef.Population arm in S2 / S3 / S4 / S5. The
// resolver maps a FacetResult + field name to a per-kind population
// view (categorical fast path via dict counts, numeric streaming path
// via Welford summary / percentiles / histogram); unknown fields fail
// with the canonical PULSE_OVERLAY_REF_UNKNOWN code carrying the
// `{field, available_fields}` Details map shape.

// newFacetResultDiscrete builds a synthetic FacetResult carrying one
// discrete field for the categorical-path tests. Mirrors the shape
// FacetSchema's categoricalAccumulator.finalize emits.
func newFacetResultDiscrete(field string, values []types.FacetValueCount, distinct int64, totalRecords, filteredRecords, nullCount int64) *types.FacetResult {
	return &types.FacetResult{
		Fields: map[string]*types.FacetField{
			field: {
				Kind:      "discrete",
				TypeName:  "categorical_u16",
				NullCount: nullCount,
				Discrete: &types.FacetDiscrete{
					Values:        values,
					DistinctCount: distinct,
				},
			},
		},
		TotalRecords:    totalRecords,
		FilteredRecords: filteredRecords,
	}
}

// newFacetResultNumeric builds a synthetic FacetResult carrying one
// numeric field for the streaming-path tests. Mirrors the shape
// FacetSchema's numericAccumulator.finalize emits.
func newFacetResultNumeric(field string, num *types.FacetNumeric, totalRecords, filteredRecords, nullCount int64) *types.FacetResult {
	return &types.FacetResult{
		Fields: map[string]*types.FacetField{
			field: {
				Kind:      "numeric",
				TypeName:  "f64",
				NullCount: nullCount,
				Numeric:   num,
			},
		},
		TotalRecords:    totalRecords,
		FilteredRecords: filteredRecords,
	}
}

// TestFacetPopulationResolver_CategoricalReadsDictCountsDirectly pins the
// categorical fast path: the resolver returns a view whose
// DiscreteCounts() is the exact slice the FacetResult carries (no copy,
// no re-sort, no recomputation). Per-value counts + frequencies match
// the host payload byte-for-byte.
func TestFacetPopulationResolver_CategoricalReadsDictCountsDirectly(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 50},
		{Value: "green", Count: 30},
		{Value: "blue", Count: 20},
	}
	result := newFacetResultDiscrete("color", values, 3, 120, 100, 0)

	view, err := ResolveFacetPopulation(result, "color")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if view == nil {
		t.Fatal("ResolveFacetPopulation view = nil; want non-nil")
	}
	if !view.IsDiscrete() {
		t.Errorf("view.IsDiscrete() = false; want true")
	}
	if view.IsNumeric() {
		t.Errorf("view.IsNumeric() = true; want false")
	}
	if got := view.Kind(); got != "discrete" {
		t.Errorf("view.Kind() = %q; want %q", got, "discrete")
	}
	if got := view.Field(); got != "color" {
		t.Errorf("view.Field() = %q; want %q", got, "color")
	}

	gotCounts, ok := view.DiscreteCounts()
	if !ok {
		t.Fatal("DiscreteCounts ok = false; want true")
	}
	if !reflect.DeepEqual(gotCounts, values) {
		t.Errorf("DiscreteCounts = %v; want %v", gotCounts, values)
	}

	// Per-value count lookups must match the host payload exactly — no
	// recomputation, just a slice walk.
	cases := []struct {
		value     string
		wantCount int64
	}{
		{"red", 50},
		{"green", 30},
		{"blue", 20},
	}
	for _, tc := range cases {
		got, ok := view.DiscreteCount(tc.value)
		if !ok {
			t.Errorf("DiscreteCount(%q) ok = false; want true", tc.value)
			continue
		}
		if got != tc.wantCount {
			t.Errorf("DiscreteCount(%q) = %d; want %d", tc.value, got, tc.wantCount)
		}
	}
}

// TestFacetPopulationResolver_CategoricalFrequency pins the per-value
// frequency math: DiscreteFrequency(value) returns count / TotalRecords.
// The denominator is the host's FilteredRecords slot (the post-filter
// record total), matching the same denominator the per-value counts
// were folded against.
func TestFacetPopulationResolver_CategoricalFrequency(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 50},
		{Value: "green", Count: 30},
		{Value: "blue", Count: 20},
	}
	// FilteredRecords = 100; per-value frequencies = count / 100.
	result := newFacetResultDiscrete("color", values, 3, 200, 100, 0)

	view, err := ResolveFacetPopulation(result, "color")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if got := view.TotalRecords(); got != 100 {
		t.Errorf("TotalRecords = %d; want 100 (FilteredRecords slot)", got)
	}

	cases := []struct {
		value    string
		wantFreq float64
	}{
		{"red", 0.50},
		{"green", 0.30},
		{"blue", 0.20},
	}
	for _, tc := range cases {
		got, ok := view.DiscreteFrequency(tc.value)
		if !ok {
			t.Errorf("DiscreteFrequency(%q) ok = false; want true", tc.value)
			continue
		}
		if math.Abs(got-tc.wantFreq) > 1e-12 {
			t.Errorf("DiscreteFrequency(%q) = %v; want %v", tc.value, got, tc.wantFreq)
		}
	}
}

// TestFacetPopulationResolver_CategoricalMissingValue pins the
// "value-not-in-population" branch: the resolver returns (0, false) for
// values that did not appear in the host's discrete payload. Handlers
// surface kind-specific zero / NaN / warning shapes; the resolver's job
// is to faithfully report absence.
func TestFacetPopulationResolver_CategoricalMissingValue(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 50},
		{Value: "green", Count: 30},
	}
	result := newFacetResultDiscrete("color", values, 2, 80, 80, 0)

	view, err := ResolveFacetPopulation(result, "color")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if v, ok := view.DiscreteCount("purple"); ok || v != 0 {
		t.Errorf("DiscreteCount(missing) = (%d, %v); want (0, false)", v, ok)
	}
	if f, ok := view.DiscreteFrequency("purple"); ok || f != 0 {
		t.Errorf("DiscreteFrequency(missing) = (%v, %v); want (0, false)", f, ok)
	}
}

// TestFacetPopulationResolver_CategoricalZeroTotalRecords pins the
// degenerate-empty-population arm: when FilteredRecords is zero the
// denominator is undefined and DiscreteFrequency returns (0, false) for
// every value. DiscreteCount still resolves because counts are zero
// across the board but the slice is well-formed.
func TestFacetPopulationResolver_CategoricalZeroTotalRecords(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 0},
	}
	result := newFacetResultDiscrete("color", values, 0, 0, 0, 0)

	view, err := ResolveFacetPopulation(result, "color")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if got := view.TotalRecords(); got != 0 {
		t.Errorf("TotalRecords = %d; want 0", got)
	}
	if f, ok := view.DiscreteFrequency("red"); ok || f != 0 {
		t.Errorf("DiscreteFrequency(zero-total) = (%v, %v); want (0, false)", f, ok)
	}
}

// TestFacetPopulationResolver_NumericReadsWelfordDirectly pins the
// numeric streaming path: the resolver returns a view whose
// NumericSummary() is the exact *FacetNumeric the FacetResult carries
// (no recomputation, no second pass over records). Welford mean / stddev
// / count / sum / min / max round-trip byte-for-byte.
func TestFacetPopulationResolver_NumericReadsWelfordDirectly(t *testing.T) {
	num := &types.FacetNumeric{
		Min:    1.0,
		Max:    99.0,
		Mean:   50.0,
		StdDev: 28.5,
		Sum:    50000.0,
		Count:  1000,
	}
	result := newFacetResultNumeric("revenue", num, 1200, 1000, 0)

	view, err := ResolveFacetPopulation(result, "revenue")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if !view.IsNumeric() {
		t.Errorf("view.IsNumeric() = false; want true")
	}
	if view.IsDiscrete() {
		t.Errorf("view.IsDiscrete() = true; want false")
	}
	if got := view.Kind(); got != "numeric" {
		t.Errorf("view.Kind() = %q; want %q", got, "numeric")
	}

	got, ok := view.NumericSummary()
	if !ok {
		t.Fatal("NumericSummary ok = false; want true")
	}
	// Pointer identity check — the resolver returns the same pointer the
	// host carries, no copy. Byte identity beats DeepEqual here because
	// any unintentional copy / mutation surfaces immediately.
	if got != num {
		t.Errorf("NumericSummary pointer = %p; want %p (same struct)", got, num)
	}
	if got.Mean != 50.0 || got.StdDev != 28.5 {
		t.Errorf("Welford summary (mean=%v stddev=%v); want (50, 28.5)", got.Mean, got.StdDev)
	}
}

// TestFacetPopulationResolver_NumericPercentiles pins the percentile
// path: when FacetRequest.NumericPercentiles populated the percentile
// map, the resolver surfaces it via NumericPercentiles() — exact map
// pointer, no recomputation. KS_VS_POP reads this surface to build the
// population CDF baseline.
func TestFacetPopulationResolver_NumericPercentiles(t *testing.T) {
	percentiles := map[string]float64{
		"p50": 50.0,
		"p90": 90.0,
		"p99": 99.0,
	}
	num := &types.FacetNumeric{
		Min:         1.0,
		Max:         99.0,
		Mean:        50.0,
		StdDev:      28.5,
		Sum:         50000.0,
		Count:       1000,
		Percentiles: percentiles,
	}
	result := newFacetResultNumeric("revenue", num, 1200, 1000, 0)

	view, err := ResolveFacetPopulation(result, "revenue")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	got, ok := view.NumericPercentiles()
	if !ok {
		t.Fatal("NumericPercentiles ok = false; want true")
	}
	if !reflect.DeepEqual(got, percentiles) {
		t.Errorf("NumericPercentiles = %v; want %v", got, percentiles)
	}
}

// TestFacetPopulationResolver_NumericPercentilesAbsent pins the
// "no percentiles requested" branch: when the FacetRequest did not ask
// for percentiles, the resolver reports (nil, false) so handlers can
// decide whether to fall back to the histogram or surface a
// "missing population CDF" warning.
func TestFacetPopulationResolver_NumericPercentilesAbsent(t *testing.T) {
	num := &types.FacetNumeric{
		Min:    1.0,
		Max:    99.0,
		Mean:   50.0,
		StdDev: 28.5,
		Sum:    50000.0,
		Count:  1000,
	}
	result := newFacetResultNumeric("revenue", num, 1200, 1000, 0)

	view, err := ResolveFacetPopulation(result, "revenue")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if got, ok := view.NumericPercentiles(); ok || got != nil {
		t.Errorf("NumericPercentiles(absent) = (%v, %v); want (nil, false)", got, ok)
	}
}

// TestFacetPopulationResolver_NumericHistogram pins the histogram path:
// when FacetRequest.IncludeHistogram populated the histogram, the
// resolver surfaces it via NumericHistogram() — exact pointer, no
// recomputation. Handlers can use the histogram as a coarse CDF
// approximation when percentiles are unavailable.
func TestFacetPopulationResolver_NumericHistogram(t *testing.T) {
	hist := &types.FacetHistogram{
		Min:  0.0,
		Max:  100.0,
		Bins: []int64{10, 20, 30, 40, 50},
	}
	num := &types.FacetNumeric{
		Min:       1.0,
		Max:       99.0,
		Mean:      50.0,
		StdDev:    28.5,
		Sum:       50000.0,
		Count:     1000,
		Histogram: hist,
	}
	result := newFacetResultNumeric("revenue", num, 1200, 1000, 0)

	view, err := ResolveFacetPopulation(result, "revenue")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	got, ok := view.NumericHistogram()
	if !ok {
		t.Fatal("NumericHistogram ok = false; want true")
	}
	// Pointer identity — the resolver does not copy.
	if got != hist {
		t.Errorf("NumericHistogram pointer = %p; want %p", got, hist)
	}
}

// TestFacetPopulationResolver_NumericHistogramAbsent pins the
// "no histogram requested" branch: when the FacetRequest did not ask
// for a histogram, the resolver reports (nil, false).
func TestFacetPopulationResolver_NumericHistogramAbsent(t *testing.T) {
	num := &types.FacetNumeric{
		Min:    1.0,
		Max:    99.0,
		Mean:   50.0,
		StdDev: 28.5,
		Sum:    50000.0,
		Count:  1000,
	}
	result := newFacetResultNumeric("revenue", num, 1200, 1000, 0)

	view, err := ResolveFacetPopulation(result, "revenue")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation err = %v; want nil", err)
	}
	if got, ok := view.NumericHistogram(); ok || got != nil {
		t.Errorf("NumericHistogram(absent) = (%v, %v); want (nil, false)", got, ok)
	}
}

// TestFacetPopulationResolver_UnknownFieldRejected pins the
// PULSE_OVERLAY_REF_UNKNOWN rejection arm — the canonical "field absent
// from FacetRequest.Fields" failure mode (the acceptance criterion the
// story flags explicitly). Details carry the offending field name and
// the sorted list of legitimate alternates.
func TestFacetPopulationResolver_UnknownFieldRejected(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 50},
	}
	result := newFacetResultDiscrete("color", values, 1, 50, 50, 0)
	// Add a second field so available_fields surface a multi-entry list.
	result.Fields["shape"] = &types.FacetField{
		Kind:     "discrete",
		TypeName: "categorical_u8",
	}

	view, err := ResolveFacetPopulation(result, "size")
	if err == nil {
		t.Fatal("ResolveFacetPopulation err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	if view != nil {
		t.Errorf("ResolveFacetPopulation view = %v; want nil on rejection", view)
	}
	assertFacetPopulationUnknownError(t, err, "size", []string{"color", "shape"})
}

// TestFacetPopulationResolver_NilFacetResultRejected pins the defensive
// nil-result branch: a missing host FacetResult fires the same coded
// error with an empty available_fields slice. The Details map shape is
// identical; only the available_fields list is empty.
func TestFacetPopulationResolver_NilFacetResultRejected(t *testing.T) {
	view, err := ResolveFacetPopulation(nil, "color")
	if err == nil {
		t.Fatal("ResolveFacetPopulation(nil) err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	if view != nil {
		t.Errorf("ResolveFacetPopulation(nil) view = %v; want nil on rejection", view)
	}
	assertFacetPopulationUnknownError(t, err, "color", []string{})
}

// TestFacetPopulationResolver_EmptyFieldRejected pins the defensive
// empty-string-field branch: an empty reference field fires the coded
// error with `field: ""` Details (the message renders the empty string
// as `""` so the failure mode is unambiguous).
func TestFacetPopulationResolver_EmptyFieldRejected(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 50},
	}
	result := newFacetResultDiscrete("color", values, 1, 50, 50, 0)

	view, err := ResolveFacetPopulation(result, "")
	if err == nil {
		t.Fatal("ResolveFacetPopulation(emptyField) err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	if view != nil {
		t.Errorf("ResolveFacetPopulation(emptyField) view = %v; want nil", view)
	}
	assertFacetPopulationUnknownError(t, err, "", []string{"color"})
}

// TestFacetPopulationResolver_NilFieldEntryRejected pins the
// "present key, nil entry" defensive branch: a FacetResult whose Fields
// map carries a nil *FacetField under the requested key is
// structurally equivalent to the missing-key case.
func TestFacetPopulationResolver_NilFieldEntryRejected(t *testing.T) {
	result := &types.FacetResult{
		Fields: map[string]*types.FacetField{
			"color": nil,
		},
	}

	view, err := ResolveFacetPopulation(result, "color")
	if err == nil {
		t.Fatal("ResolveFacetPopulation(nilEntry) err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	if view != nil {
		t.Errorf("ResolveFacetPopulation(nilEntry) view = %v; want nil", view)
	}
	assertFacetPopulationUnknownError(t, err, "color", []string{"color"})
}

// TestFacetPopulationResolver_Deterministic pins the pure-function
// contract: same FacetResult + same field name produces a byte-equal
// view + byte-equal error shape on every invocation. Guards against
// future drift where a caller might fold state into the resolver.
func TestFacetPopulationResolver_Deterministic(t *testing.T) {
	values := []types.FacetValueCount{
		{Value: "red", Count: 50},
		{Value: "green", Count: 30},
	}
	result := newFacetResultDiscrete("color", values, 2, 80, 80, 0)

	firstView, err := ResolveFacetPopulation(result, "color")
	if err != nil {
		t.Fatalf("first call err = %v", err)
	}
	for i := 0; i < 8; i++ {
		view, err := ResolveFacetPopulation(result, "color")
		if err != nil {
			t.Fatalf("repeat call %d err = %v", i, err)
		}
		if view.Field() != firstView.Field() || view.Kind() != firstView.Kind() {
			t.Fatalf("repeat call %d view diverged; first=(%q, %q) got=(%q, %q)",
				i, firstView.Field(), firstView.Kind(), view.Field(), view.Kind())
		}
		if view.TotalRecords() != firstView.TotalRecords() {
			t.Fatalf("repeat call %d total records diverged; first=%d got=%d",
				i, firstView.TotalRecords(), view.TotalRecords())
		}
	}
}

// TestFacetPopulationResolver_NilSafeQueries pins the nil-receiver
// safety contract: every accessor on a nil *FacetPopulationView returns
// a sensible empty value rather than panicking. This guards against the
// "I got nil + err, then forgot to check err first" callsite pattern.
func TestFacetPopulationResolver_NilSafeQueries(t *testing.T) {
	var view *FacetPopulationView
	if got := view.Field(); got != "" {
		t.Errorf("nil.Field() = %q; want empty", got)
	}
	if got := view.Kind(); got != "" {
		t.Errorf("nil.Kind() = %q; want empty", got)
	}
	if view.IsDiscrete() {
		t.Errorf("nil.IsDiscrete() = true; want false")
	}
	if view.IsNumeric() {
		t.Errorf("nil.IsNumeric() = true; want false")
	}
	if view.FacetField() != nil {
		t.Errorf("nil.FacetField() = non-nil; want nil")
	}
	if view.TotalRecords() != 0 {
		t.Errorf("nil.TotalRecords() = non-zero; want 0")
	}
	if _, ok := view.DiscreteCounts(); ok {
		t.Errorf("nil.DiscreteCounts() ok = true; want false")
	}
	if _, ok := view.NumericSummary(); ok {
		t.Errorf("nil.NumericSummary() ok = true; want false")
	}
}

// assertFacetPopulationUnknownError asserts the err carries Details with
// the canonical `PULSE_OVERLAY_REF_UNKNOWN` code, the expected field
// name, and the expected sorted available_fields slice. Defense-in-depth
// helper for every rejection-arm test.
func assertFacetPopulationUnknownError(t *testing.T, err error, wantField string, wantAvailable []string) {
	t.Helper()
	coded, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err = %T; want *errors.CodedError. err = %v", err, err)
	}
	if coded.Details == nil {
		t.Fatalf("coded.Details is nil; want map carrying code + field + available_fields")
	}
	codeRaw, ok := coded.Details["code"].(string)
	if !ok {
		t.Fatalf("coded.Details[code] = %v (%T); want string", coded.Details["code"], coded.Details["code"])
	}
	if codeRaw != string(pulseerrors.PULSE_OVERLAY_REF_UNKNOWN) {
		t.Fatalf("coded.Details[code] = %q; want %q",
			codeRaw, pulseerrors.PULSE_OVERLAY_REF_UNKNOWN)
	}
	gotField, ok := coded.Details["field"].(string)
	if !ok {
		t.Fatalf("coded.Details[field] = %v (%T); want string", coded.Details["field"], coded.Details["field"])
	}
	if gotField != wantField {
		t.Errorf("coded.Details[field] = %q; want %q", gotField, wantField)
	}
	gotAvailable, ok := coded.Details["available_fields"].([]string)
	if !ok {
		t.Fatalf("coded.Details[available_fields] = %v (%T); want []string",
			coded.Details["available_fields"], coded.Details["available_fields"])
	}
	if !reflect.DeepEqual(gotAvailable, wantAvailable) {
		t.Errorf("coded.Details[available_fields] = %v; want %v", gotAvailable, wantAvailable)
	}
	// Sanity: message references the offending field so a callsite reader
	// recognises the failure mode without parsing the details map.
	if !strings.Contains(coded.Message, "population reference field") {
		t.Errorf("coded.Message = %q; want substring %q",
			coded.Message, "population reference field")
	}
}
