package processing

import (
	"encoding/json"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeAttribute(t *testing.T, attrType types.AttributeType, field string, schema *encoding.Schema, expr string) AttributeComputer {
	t.Helper()
	factory, ok := attributeRegistry[attrType]
	if !ok {
		t.Fatalf("no attribute registered for %s", attrType)
	}
	attr, err := factory(&types.Attribute{Type: attrType, Field: field, Label: "result", Expression: expr}, schema)
	if err != nil {
		t.Fatalf("create attribute: %v", err)
	}
	return attr
}

func TestAttribute_ZScore_Basic(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_ZSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 8 {
		t.Fatalf("result length = %d, want 8", len(result))
	}
	// Mean=5, stddev=2. Z-score of 9 = (9-5)/2 = 2.0
	if !floatClose(result[7], 2.0, 0.01) {
		t.Errorf("zscore[7] = %f, want 2.0", result[7])
	}
	// Z-score of 2 = (2-5)/2 = -1.5
	if !floatClose(result[0], -1.5, 0.01) {
		t.Errorf("zscore[0] = %f, want -1.5", result[0])
	}
}

func TestAttribute_ZScore_Empty(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_ZSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("result length = %d, want 0", len(result))
	}
}

func TestAttribute_ZScore_SingleValue(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_ZSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{42})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("result length = %d, want 1", len(result))
	}
	// One value: the population sd is 0, so z = 0/0 — undefined, null
	// (U50 E7-S2), never 0.
	if !IsAttributeNull(result[0]) {
		t.Errorf("zscore = %f, want AttributeNull", result[0])
	}
}

func TestAttribute_TScore_Basic(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_TSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 8 {
		t.Fatalf("result length = %d, want 8", len(result))
	}
	// T-score = z*10 + 50. For value 9: z=2.0, t=70
	if !floatClose(result[7], 70.0, 0.1) {
		t.Errorf("tscore[7] = %f, want 70.0", result[7])
	}
	// For value 2: z=-1.5, t=35
	if !floatClose(result[0], 35.0, 0.1) {
		t.Errorf("tscore[0] = %f, want 35.0", result[0])
	}
}

func TestAttribute_Normalized_Basic(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_NORMALIZED, "score", schema, "")
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result length = %d, want 3", len(result))
	}
	// Min=10, Max=30. Normalized: (x-min)/(max-min)
	if !floatClose(result[0], 0.0, 0.001) {
		t.Errorf("normalized[0] = %f, want 0.0", result[0])
	}
	if !floatClose(result[1], 0.5, 0.001) {
		t.Errorf("normalized[1] = %f, want 0.5", result[1])
	}
	if !floatClose(result[2], 1.0, 0.001) {
		t.Errorf("normalized[2] = %f, want 1.0", result[2])
	}
}

func TestAttribute_Normalized_AllSame(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_NORMALIZED, "score", schema, "")
	records := makeRecords(schema, "score", []float64{5, 5, 5})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All same: max = min, so every (x-min)/(max-min) is 0/0 — null
	// (U50 E7-S2), never 0.
	for i, v := range result {
		if !IsAttributeNull(v) {
			t.Errorf("normalized[%d] = %f, want AttributeNull", i, v)
		}
	}
}

func TestAttribute_Formula_Basic(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_FORMULA, "score", schema, "score * 2")
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result length = %d, want 3", len(result))
	}
	if !floatClose(result[0], 20.0, 0.001) {
		t.Errorf("formula[0] = %f, want 20.0", result[0])
	}
	if !floatClose(result[2], 60.0, 0.001) {
		t.Errorf("formula[2] = %f, want 60.0", result[2])
	}
}

func TestFormulaAttribute_CategoricalStringAccess(t *testing.T) {
	t.Parallel()

	schema := categoricalSchema()
	attr := makeAttribute(t, types.ATTR_FORMULA, "brand", schema, `brand == "Apple"`)

	// Apple=0, Samsung=1, Google=2
	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 2, "score": 30})

	result, err := attr.Compute(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// brand == "Apple" => true(1), false(0), false(0)
	if result[0] != 1.0 {
		t.Errorf("formula[0] = %f, want 1.0 (Apple==Apple)", result[0])
	}
	if result[1] != 0.0 {
		t.Errorf("formula[1] = %f, want 0.0 (Samsung!=Apple)", result[1])
	}
}

func TestFormulaAttribute_CategoricalInOperator(t *testing.T) {
	t.Parallel()

	schema := categoricalSchema()
	attr := makeAttribute(t, types.ATTR_FORMULA, "brand", schema, `brand in ["Apple", "Samsung"]`)

	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 2, "score": 30})

	result, err := attr.Compute(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// brand in ["Apple", "Samsung"] => true(1), true(1), false(0)
	if result[0] != 1.0 {
		t.Errorf("formula[0] = %f, want 1.0", result[0])
	}
	if result[1] != 1.0 {
		t.Errorf("formula[1] = %f, want 1.0", result[1])
	}
	if result[2] != 0.0 {
		t.Errorf("formula[2] = %f, want 0.0", result[2])
	}
}

func TestAttribute_Percentile_Basic(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_PERCENTILE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{10, 30, 20, 40})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("result length = %d, want 4", len(result))
	}
	// Values sorted: 10, 20, 30, 40
	// Percentiles: 10=>25, 20=>50, 30=>75, 40=>100
	if !floatClose(result[0], 25.0, 0.1) {
		t.Errorf("percentile[0] (10) = %f, want ~25.0", result[0])
	}
	if !floatClose(result[2], 50.0, 0.1) {
		t.Errorf("percentile[2] (20) = %f, want ~50.0", result[2])
	}
}

// TestAttribute_RankRemoved verifies the registry no longer resolves ATTR_RANK.
// Migration: use WIN_RANK with empty PartitionBy and one ASC OrderBy on the
// same field. See skills/window-design.md.
func TestAttribute_RankRemoved(t *testing.T) {
	t.Parallel()

	if _, ok := attributeRegistry[types.AttributeType("ATTR_RANK")]; ok {
		t.Error("ATTR_RANK still registered; expected removal in favor of WIN_RANK")
	}
}

func dateSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "enrolled", Type: encoding.FieldTypeDate},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
}

func makeDatePartAttribute(t *testing.T, field string, schema *encoding.Schema, part string) AttributeComputer {
	t.Helper()
	factory, ok := attributeRegistry[types.ATTR_DATE_PART]
	if !ok {
		t.Fatalf("no attribute registered for ATTR_DATE_PART")
	}
	params := json.RawMessage(`{"part":"` + part + `"}`)
	attr, err := factory(&types.Attribute{
		Type:   types.ATTR_DATE_PART,
		Field:  field,
		Label:  "result",
		Params: params,
	}, schema)
	if err != nil {
		t.Fatalf("create date_part attribute: %v", err)
	}
	return attr
}

// 2024-03-15 = 19797 days since epoch
// 2023-12-01 = 19692 days since epoch
// 2025-01-31 = 20119 days since epoch

func TestAttribute_DatePart_Year(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "year")
	records := makeRecords(schema, "enrolled", []float64{19797, 19692, 20119})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result length = %d, want 3", len(result))
	}
	if result[0] != 2024 {
		t.Errorf("year[0] = %f, want 2024", result[0])
	}
	if result[1] != 2023 {
		t.Errorf("year[1] = %f, want 2023", result[1])
	}
	if result[2] != 2025 {
		t.Errorf("year[2] = %f, want 2025", result[2])
	}
}

func TestAttribute_DatePart_Month(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "month")
	records := makeRecords(schema, "enrolled", []float64{19797, 19692, 20119})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0] != 3 {
		t.Errorf("month[0] = %f, want 3", result[0])
	}
	if result[1] != 12 {
		t.Errorf("month[1] = %f, want 12", result[1])
	}
	if result[2] != 1 {
		t.Errorf("month[2] = %f, want 1", result[2])
	}
}

func TestAttribute_DatePart_Day(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "day")
	records := makeRecords(schema, "enrolled", []float64{19797, 19692, 20119})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0] != 15 {
		t.Errorf("day[0] = %f, want 15", result[0])
	}
	if result[1] != 1 {
		t.Errorf("day[1] = %f, want 1", result[1])
	}
	if result[2] != 31 {
		t.Errorf("day[2] = %f, want 31", result[2])
	}
}

func TestAttribute_DatePart_YearMonth(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "year_month")
	records := makeRecords(schema, "enrolled", []float64{19797, 19692, 20119})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2024-03 => 202403, 2023-12 => 202312, 2025-01 => 202501
	if result[0] != 202403 {
		t.Errorf("year_month[0] = %f, want 202403", result[0])
	}
	if result[1] != 202312 {
		t.Errorf("year_month[1] = %f, want 202312", result[1])
	}
	if result[2] != 202501 {
		t.Errorf("year_month[2] = %f, want 202501", result[2])
	}
}

func TestAttribute_DatePart_YearMonthDay(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "year_month_day")
	records := makeRecords(schema, "enrolled", []float64{19797, 19692, 20119})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2024-03-15 => 20240315, 2023-12-01 => 20231201, 2025-01-31 => 20250131
	if result[0] != 20240315 {
		t.Errorf("year_month_day[0] = %f, want 20240315", result[0])
	}
	if result[1] != 20231201 {
		t.Errorf("year_month_day[1] = %f, want 20231201", result[1])
	}
	if result[2] != 20250131 {
		t.Errorf("year_month_day[2] = %f, want 20250131", result[2])
	}
}

func TestAttribute_DatePart_MonthDay(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "month_day")
	records := makeRecords(schema, "enrolled", []float64{19797, 19692, 20119})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 03-15 => 315, 12-01 => 1201, 01-31 => 131
	if result[0] != 315 {
		t.Errorf("month_day[0] = %f, want 315", result[0])
	}
	if result[1] != 1201 {
		t.Errorf("month_day[1] = %f, want 1201", result[1])
	}
	if result[2] != 131 {
		t.Errorf("month_day[2] = %f, want 131", result[2])
	}
}

func TestAttribute_DatePart_Empty(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "year")
	records := makeRecords(schema, "enrolled", []float64{})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("result length = %d, want 0", len(result))
	}
}

func TestAttribute_DatePart_NullReturnsNull(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	attr := makeDatePartAttribute(t, "enrolled", schema, "year")
	records := makeRecordsWithNulls(schema, "enrolled", []float64{19797, 0}, []int{1})

	result, err := attr.Compute(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0] != 2024 {
		t.Errorf("year[0] = %f, want 2024", result[0])
	}
	if !IsAttributeNull(result[1]) {
		t.Errorf("year[1] = %f, want AttributeNull (a null date has no year)", result[1])
	}
}

func TestAttribute_DatePart_NonDateFieldErrors(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	factory := attributeRegistry[types.ATTR_DATE_PART]
	params := json.RawMessage(`{"part":"year"}`)
	_, err := factory(&types.Attribute{
		Type:   types.ATTR_DATE_PART,
		Field:  "score",
		Label:  "result",
		Params: params,
	}, schema)
	if err == nil {
		t.Fatal("expected error for non-date field, got nil")
	}
}

func TestAttribute_DatePart_MissingPartErrors(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	factory := attributeRegistry[types.ATTR_DATE_PART]
	params := json.RawMessage(`{}`)
	_, err := factory(&types.Attribute{
		Type:   types.ATTR_DATE_PART,
		Field:  "enrolled",
		Label:  "result",
		Params: params,
	}, schema)
	if err == nil {
		t.Fatal("expected error for missing part, got nil")
	}
}

func TestAttribute_DatePart_InvalidPartErrors(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	factory := attributeRegistry[types.ATTR_DATE_PART]
	params := json.RawMessage(`{"part":"quarter"}`)
	_, err := factory(&types.Attribute{
		Type:   types.ATTR_DATE_PART,
		Field:  "enrolled",
		Label:  "result",
		Params: params,
	}, schema)
	if err == nil {
		t.Fatal("expected error for invalid part, got nil")
	}
}

func TestAttribute_DatePart_NilParamsErrors(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	factory := attributeRegistry[types.ATTR_DATE_PART]
	_, err := factory(&types.Attribute{
		Type:  types.ATTR_DATE_PART,
		Field: "enrolled",
		Label: "result",
	}, schema)
	if err == nil {
		t.Fatal("expected error for nil params, got nil")
	}
}

// Suppress unused import warning
var _ = math.Abs

// TestAttribute_NullAndDegenerateInputs pins what the per-row attributes
// emit for a missing input and for a constant field. The guidance (the
// ATTR_* Purposes and value Interpretations in internal/descriptor), the
// atomic skills and the manifest notes state these values. A missing
// input scores null (AttributeNull) on every score — never the 0 (50 for
// the T-score) it read before U50 E6-S1 — and is left out of the
// pre-pass; a field with no spread (sd = 0, max = min) scores null on
// every row for ATTR_ZSCORE / ATTR_TSCORE / ATTR_NORMALIZED — 0/0 is
// undefined, never the 0 / 50 / 0 it read before U50 E7-S2. A missing set is null for
// ATTR_SET_POPCOUNT (an empty selection stays 0) and 0 for the
// membership flag ATTR_SET_HAS (a null set holds no label).
func TestAttribute_NullAndDegenerateInputs(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	cases := []struct {
		typ       types.AttributeType
		constNull bool
	}{
		{types.ATTR_ZSCORE, true},
		{types.ATTR_TSCORE, true},
		{types.ATTR_NORMALIZED, true},
		{types.ATTR_PERCENTILE, false}, // constant case pinned by the ties test
	}
	for _, c := range cases {
		t.Run(string(c.typ), func(t *testing.T) {
			attr := makeAttribute(t, c.typ, "score", schema, "")
			recs := makeRecordsWithNulls(schema, "score", []float64{10, 20, 0, 40}, []int{2})
			got, err := attr.Compute(recs, "score")
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if !IsAttributeNull(got[2]) {
				t.Errorf("null row = %v, want AttributeNull", got[2])
			}
			// The null row is out of the pre-pass: the other rows score as
			// over {10, 20, 40} alone.
			want := map[types.AttributeType][]float64{
				types.ATTR_NORMALIZED: {0, 1.0 / 3, 0, 1},
				types.ATTR_PERCENTILE: {100.0 / 3, 200.0 / 3, 0, 100},
			}[c.typ]
			for i, w := range want {
				if i != 2 && math.Abs(got[i]-w) > 1e-12 {
					t.Errorf("row %d = %v, want %v (the null row reached the pre-pass)", i, got[i], w)
				}
			}
			if !c.constNull {
				return
			}
			attr = makeAttribute(t, c.typ, "score", schema, "")
			got, err = attr.Compute(makeRecords(schema, "score", []float64{5, 5, 5}), "score")
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			for i, v := range got {
				if !IsAttributeNull(v) {
					t.Errorf("constant field row %d = %v, want AttributeNull", i, v)
				}
			}
		})
	}

	setSchema := makeSetTestSchema(t)
	missing := NewRecordWithWide(setSchema, map[string]float64{}, map[string]bool{"tags": true}, nil)
	pop, err := newSetPopcountAttribute(&types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags"}, setSchema)
	if err != nil {
		t.Fatal(err)
	}
	has, err := newSetHasAttribute(&types.Attribute{Type: types.ATTR_SET_HAS, Field: "tags", Params: []byte(`{"label":"AMEX"}`)}, setSchema)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pop.Compute([]*Record{missing}, "tags")
	if err != nil {
		t.Fatalf("ATTR_SET_POPCOUNT Compute: %v", err)
	}
	if !IsAttributeNull(got[0]) {
		t.Errorf("ATTR_SET_POPCOUNT null set row = %v, want AttributeNull", got[0])
	}
	got, err = has.Compute([]*Record{missing}, "tags")
	if err != nil {
		t.Fatalf("ATTR_SET_HAS Compute: %v", err)
	}
	if got[0] != 0 {
		t.Errorf("ATTR_SET_HAS null set row = %v, want 0", got[0])
	}
}

// TestAttribute_Percentile_TiesTakeDistinctRanks pins ATTR_PERCENTILE's
// definition as the guidance states it: rank / n * 100 over the rows with
// a value, the smallest reading 100 / n and the largest 100, and tied
// values NOT sharing a percentile — each tied row takes its own rank.
// When ties are made to share a rank, update the ATTR_PERCENTILE
// Interpretation and op-attr-percentile.md with this test.
func TestAttribute_Percentile_TiesTakeDistinctRanks(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_PERCENTILE, "score", schema, "")
	got, err := attr.Compute(makeRecords(schema, "score", []float64{5, 5, 5, 5}), "score")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	sorted := append([]float64(nil), got...)
	sort.Float64s(sorted)
	want := []float64{25, 50, 75, 100}
	for i := range want {
		if !floatClose(sorted[i], want[i], 1e-9) {
			t.Fatalf("tied percentiles = %v, want the distinct set %v", got, want)
		}
	}
}

// TestAttribute_Percentile_TiesRankInScanOrder pins ATTR_PERCENTILE's
// deterministic tie rule: tied rows take consecutive ranks in scan
// (record) order, the earlier row lower. The cohort is larger than the
// sort's insertion-sort cut-off and almost all ties, so an unstable sort
// visibly reorders tied rows; repeated runs must also be identical.
func TestAttribute_Percentile_TiesRankInScanOrder(t *testing.T) {
	t.Parallel()

	const rows = 300
	values := make([]float64, rows)
	var nulls []int
	for i := range values {
		values[i] = float64((i * 7) % 3)
		if i%11 == 5 {
			nulls = append(nulls, i)
		}
	}
	isNull := make(map[int]bool, len(nulls))
	for _, i := range nulls {
		isNull[i] = true
	}
	// Independent expectation: rank = (values below) + (equal values at
	// or before this row in scan order); n counts the non-null rows.
	n := 0
	for i := range values {
		if !isNull[i] {
			n++
		}
	}
	want := make([]float64, rows)
	for i, v := range values {
		if isNull[i] {
			want[i] = AttributeNull
			continue
		}
		rank := 0
		for j, u := range values {
			if isNull[j] {
				continue
			}
			if u < v || (u == v && j <= i) {
				rank++
			}
		}
		want[i] = float64(rank) / float64(n) * 100
	}

	schema := numericSchema()
	schema.Fields[0].Nullable = true
	attr := makeAttribute(t, types.ATTR_PERCENTILE, "score", schema, "")
	var first []float64
	for run := 0; run < 5; run++ {
		got, err := attr.Compute(makeRecordsWithNulls(schema, "score", values, nulls), "score")
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		for i := range want {
			if isNull[i] {
				if !IsAttributeNull(got[i]) {
					t.Fatalf("run %d row %d: got %v, want null", run, i, got[i])
				}
				continue
			}
			if got[i] != want[i] {
				t.Fatalf("run %d row %d (value %v): percentile %v, want %v (tied rows rank in scan order)", run, i, values[i], got[i], want[i])
			}
		}
		if run == 0 {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] && !(IsAttributeNull(got[i]) && IsAttributeNull(first[i])) {
				t.Fatalf("run %d row %d: %v differs from run 0's %v", run, i, got[i], first[i])
			}
		}
	}
}
