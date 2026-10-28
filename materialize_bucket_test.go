package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The output column `bucket` (types.MaterializeBucket): a numeric or
// date-family column written as a categorical of range labels, the ranges
// inline or a registered range table, compiled by the shared
// range-compilation pass (numeric half-open, dates inclusive).

func f64p(f float64) *float64 { return &f }
func strp(s string) *string   { return &s }

// epochDay is y-m-d as epoch days.
func epochDay(y int, m time.Month, d int) int32 {
	return int32(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// bucketTables are the registered tables the inline ranges mirror.
func bucketTables() map[string]RangeTable {
	return map[string]RangeTable{
		"spend_band": {NumericRanges: []NumericRangeSpec{
			{Label: "high", Start: f64p(20)},
			{Label: "low", End: f64p(10)},
			{Label: "mid", Start: f64p(10), End: f64p(20)},
		}},
		"fy_quarter": {Ranges: []DateRangeSpec{
			{Label: "Q2", Start: strp("2024-04-01"), End: strp("2024-06-30")},
			{Label: "Q1", Start: strp("2024-01-01"), End: strp("2024-03-31")},
		}},
	}
}

// raw renders a bound as its JSON.
func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// spendRanges / quarterRanges are bucketTables' ranges inline, in the
// same declared order.
func spendRanges() []types.MaterializeBucketRange {
	return []types.MaterializeBucketRange{
		{Label: "high", Start: raw(20)},
		{Label: "low", End: raw(10)},
		{Label: "mid", Start: raw(10), End: raw(20)},
	}
}

func quarterRanges() []types.MaterializeBucketRange {
	return []types.MaterializeBucketRange{
		{Label: "Q2", Start: raw("2024-04-01"), End: raw("2024-06-30")},
		{Label: "Q1", Start: raw("2024-01-01"), End: raw("2024-03-31")},
	}
}

// bucketSource writes bucket.pulse: spend (f64, nullable), big (u64),
// price (decimal128(10,2)), day (date) and ts (datetime), one row per
// boundary probe.
func bucketSource(t *testing.T, p *Pulse) {
	t.Helper()
	dec := func(s string) encoding.Decimal128 {
		d, _, err := encoding.ParseDecimal128(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	b, err := p.NewCohortBuilder(context.Background(), "bucket.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true, Description: "Spend of the customer in dollars"},
		{Name: "big", Type: encoding.FieldTypeU64, Description: "Large identifier of the record"},
		{Name: "price", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Unit price in dollars"},
		{Name: "day", Type: encoding.FieldTypeDate, Description: "Calendar day of the order"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Instant of the order in epoch seconds"},
	}}, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const two53 = uint64(1) << 53
	mar31 := int64(epochDay(2024, time.March, 31)) * 86400
	rows := []CohortRow{
		{9.999, two53 + 3, dec("9.99"), epochDay(2024, time.January, 1), mar31 + 23*3600 + 1800},
		{10.0, two53 + 4, dec("10.00"), epochDay(2024, time.March, 31), mar31 + 12*3600},
		{19.99, uint64(0), dec("19.99"), epochDay(2024, time.April, 1), mar31 + 86400},
		{20.0, two53 + 5, dec("20.00"), epochDay(2024, time.June, 30), mar31 - 1},
		{nil, uint64(7), dec("-1.00"), epochDay(2024, time.June, 30), mar31},
	}
	for _, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func bucketReq(tz string, cols ...types.MaterializeOutputColumn) *Request {
	return &Request{Cohort: &types.Cohort{Filename: "bucket.pulse"}, TimeZone: tz, Output: &types.MaterializeOutput{Columns: cols}}
}

// column returns column col of rows.
func column(rows []CohortRow, col int) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r[col]
	}
	return out
}

// TestMaterializeBucket_InlineAndTableIdentical: inline ranges and the
// registered table holding the same ranges write identical columns — a
// categorical_u8 whose dictionary is the labels in DECLARED order —
// with RangeTables' boundary rules at the edges: a numeric range is
// half-open (10 → mid, 20 → high, 9.999 → low), compared exactly (u64
// 2^53+3 sits below a 2^53+4 bound though its float64 rounds onto it; a
// decimal 19.99 is mid), a date range inclusive on both ends (03-31 → Q1,
// 04-01 → Q2, 06-30 → Q2). A null stays null. Predict reports the same
// schema and no risk for gapless numeric ranges.
func TestMaterializeBucket_InlineAndTableIdentical(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{Extensions: Extensions{RangeTables: bucketTables()}})
	bucketSource(t, p)
	edge := []types.MaterializeBucketRange{{Label: "below", End: raw(float64(9007199254740996))}, {Label: "above", Start: raw(float64(9007199254740996))}}
	req := bucketReq("",
		types.MaterializeOutputColumn{From: "spend", As: "spend_inline", Bucket: &types.MaterializeBucket{Ranges: spendRanges()}},
		types.MaterializeOutputColumn{From: "spend", As: "spend_table", Bucket: &types.MaterializeBucket{RangeTable: "spend_band"}},
		types.MaterializeOutputColumn{From: "day", As: "q_inline", Bucket: &types.MaterializeBucket{Ranges: quarterRanges()}},
		types.MaterializeOutputColumn{From: "day", As: "q_table", Bucket: &types.MaterializeBucket{RangeTable: "fy_quarter"}},
		types.MaterializeOutputColumn{From: "big", Bucket: &types.MaterializeBucket{Ranges: edge}},
		types.MaterializeOutputColumn{From: "price", Bucket: &types.MaterializeBucket{RangeTable: "spend_band"}},
	)
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
	schema, rows := readAll(t, p, "out.pulse")
	for i, want := range [][]string{{"high", "low", "mid"}, {"high", "low", "mid"}, {"Q2", "Q1"}, {"Q2", "Q1"}, {"below", "above"}, {"high", "low", "mid"}} {
		f := schema.Fields[i]
		if f.Type != encoding.FieldTypeCategoricalU8 || !slices.Equal(f.Dictionary.Values(), want) {
			t.Errorf("%s = %s %v, want categorical_u8 %v", f.Name, f.Type, f.Dictionary.Values(), want)
		}
	}
	if !schema.Fields[0].Nullable || schema.Fields[2].Nullable {
		t.Errorf("nullability not inherited: spend %v, day %v", schema.Fields[0].Nullable, schema.Fields[2].Nullable)
	}
	for i, want := range [][]any{
		{"low", "mid", "mid", "high", nil},
		{"low", "mid", "mid", "high", nil},
		{"Q1", "Q1", "Q2", "Q2", "Q2"},
		{"Q1", "Q1", "Q2", "Q2", "Q2"},
		{"below", "above", "below", "above", "below"},
		{"low", "mid", "mid", "high", "low"},
	} {
		if got := column(rows, i); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", schema.Fields[i].Name, got, want)
		}
	}

	_, pres := predictMat(t, p, req)
	if !pres.Valid {
		t.Fatal("predict refused")
	}
	for i, c := range pres.Columns {
		if c.Type != schema.Fields[i].Type.String() || c.Dictionary == nil || !slices.Equal(c.Dictionary.Values, schema.Fields[i].Dictionary.Values()) {
			t.Errorf("predict column %s = %s %v, run wrote %s %v", c.Name, c.Type, c.Dictionary, schema.Fields[i].Type, schema.Fields[i].Dictionary.Values())
		}
	}
	// spend (f64: a NaN lies in no range) and the date quarters (gaps)
	// carry a risk; big and price (gapless, exact) do not.
	var risky []string
	for _, r := range pres.Risks {
		risky = append(risky, r.Column)
		if r.Code != string(errors.PULSE_MATERIALIZE_CAST_FAILED) {
			t.Errorf("risk %+v, want CAST_FAILED", r)
		}
	}
	if want := []string{"spend_inline", "spend_table", "q_inline", "q_table"}; !slices.Equal(risky, want) {
		t.Errorf("risky columns %v, want %v", risky, want)
	}
}

// TestMaterializeBucket_DateTimeByLocalDay: a datetime is bucketed by
// its local day in the request time_zone (else Options.DefaultTimeZone,
// else UTC) — 2024-03-31 23:30 UTC is April 1st in Berlin.
func TestMaterializeBucket_DateTimeByLocalDay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts Options
		tz   string
		want []any
	}{
		{"utc", Options{}, "", []any{"Q1", "Q1", "Q2", "Q1", "Q1"}},
		{"request time_zone", Options{}, "Europe/Berlin", []any{"Q2", "Q1", "Q2", "Q1", "Q1"}},
		{"default time zone", Options{DefaultTimeZone: "Europe/Berlin"}, "", []any{"Q2", "Q1", "Q2", "Q1", "Q1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, _ := matPulse(t, c.opts)
			bucketSource(t, p)
			req := bucketReq(c.tz, types.MaterializeOutputColumn{From: "ts", Bucket: &types.MaterializeBucket{Ranges: quarterRanges()}})
			if _, err := p.Materialize(context.Background(), req, "out.pulse", MaterializeOptions{}); err != nil {
				t.Fatal(err)
			}
			_, rows := readAll(t, p, "out.pulse")
			if got := column(rows, 0); !slices.Equal(got, c.want) {
				t.Errorf("ts buckets %v, want %v", got, c.want)
			}
		})
	}
}

// TestMaterializeBucket_OutOfRangeFailsOrNulls: a value outside every
// range fails the run with PULSE_MATERIALIZE_CAST_FAILED (reason
// no_range, the value rendered) and writes nothing — or, under
// on_cast_error: "null" on a nullable column, is written null and
// counted in one PULSE_MATERIALIZE_CAST_NULLED warning. Predict codes
// the risk by the policy.
func TestMaterializeBucket_OutOfRangeFailsOrNulls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	bucketSource(t, p)
	q1 := []types.MaterializeBucketRange{{Label: "Q1", Start: raw("2024-01-01"), End: raw("2024-03-31")}}

	fail := bucketReq("", types.MaterializeOutputColumn{From: "day", As: "q", Bucket: &types.MaterializeBucket{Ranges: q1}})
	_, err := p.Materialize(ctx, fail, "out.pulse", MaterializeOptions{})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATERIALIZE_CAST_FAILED {
		t.Fatalf("err %v, want CAST_FAILED", err)
	}
	if d := ce.Details; d["column"] != "q" || d["row"] != int64(2) || d["value"] != "2024-04-01" || d["reason"] != "no_range" || d["to_type"] != "categorical_u8" {
		t.Errorf("details %v", d)
	}
	assertAbsent(t, fsys, "out.pulse")
	if _, pres := predictMat(t, p, fail); len(pres.Risks) != 1 || pres.Risks[0].Code != string(errors.PULSE_MATERIALIZE_CAST_FAILED) || pres.Risks[0].Path != "output.columns[0].bucket" {
		t.Errorf("risks %+v, want one CAST_FAILED on .bucket", pres.Risks)
	}

	null := bucketReq("", types.MaterializeOutputColumn{From: "day", As: "q", Nullable: boolPtr(true), OnCastError: "null", Bucket: &types.MaterializeBucket{Ranges: q1}})
	res, err := p.Materialize(ctx, null, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, rows := readAll(t, p, "out.pulse")
	if got := column(rows, 0); !slices.Equal(got, []any{"Q1", "Q1", nil, nil, nil}) {
		t.Errorf("q = %v", got)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != errors.PULSE_MATERIALIZE_CAST_NULLED {
		t.Fatalf("warnings %v", res.Warnings)
	}
	if d := res.Warnings[0].Details; d["count"] != int64(3) || d["first_row"] != int64(2) || d["from_type"] != "date" || d["to_type"] != "categorical_u8" {
		t.Errorf("warning details %v", d)
	}
	if _, pres := predictMat(t, p, null); len(pres.Risks) != 1 || pres.Risks[0].Code != string(errors.PULSE_MATERIALIZE_CAST_NULLED) {
		t.Errorf("risks %+v, want one CAST_NULLED", pres.Risks)
	}
}

// TestMaterializeBucket_StaticRefusalsMatchPredict: an unbucketable
// source, both or neither range source, an unknown table (a hidden
// capability:range_tables included), a table of the other kind, a bound
// of the wrong JSON kind, and every shared-pass refusal are raised
// before anything is read — by the run and by predict, with the same
// code, message and details (column + path).
func TestMaterializeBucket_StaticRefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, fsys := matPulse(t, Options{Extensions: Extensions{RangeTables: bucketTables()}})
	bucketSource(t, p)
	cases := []struct {
		name string
		col  types.MaterializeOutputColumn
		code errors.Code
		path string
	}{
		{"neither source", types.MaterializeOutputColumn{From: "spend", Bucket: &types.MaterializeBucket{}},
			errors.PULSE_RANGE_SOURCE_AMBIGUOUS, "output.columns[0].bucket"},
		{"both sources", types.MaterializeOutputColumn{From: "spend", Bucket: &types.MaterializeBucket{Ranges: spendRanges(), RangeTable: "spend_band"}},
			errors.PULSE_RANGE_SOURCE_AMBIGUOUS, "output.columns[0].bucket"},
		{"unknown table", types.MaterializeOutputColumn{From: "spend", Bucket: &types.MaterializeBucket{RangeTable: "nope"}},
			errors.PULSE_RANGE_TABLE_UNKNOWN, "output.columns[0].bucket.range_table"},
		{"kind mismatch", types.MaterializeOutputColumn{From: "day", Bucket: &types.MaterializeBucket{RangeTable: "spend_band"}},
			errors.PULSE_RANGE_TABLE_KIND_MISMATCH, "output.columns[0].bucket.range_table"},
		{"string bound on numeric", types.MaterializeOutputColumn{From: "big", Bucket: &types.MaterializeBucket{Ranges: quarterRanges()}},
			errors.PULSE_RANGE_INVALID, "output.columns[0].bucket.ranges[0].start"},
		{"number bound on date", types.MaterializeOutputColumn{From: "ts", Bucket: &types.MaterializeBucket{Ranges: spendRanges()}},
			errors.PULSE_RANGE_INVALID, "output.columns[0].bucket.ranges[0].start"},
		{"overlap", types.MaterializeOutputColumn{From: "spend", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "a", End: raw(10)}, {Label: "b", Start: raw(9)}}}},
			errors.PULSE_RANGE_OVERLAP, "output.columns[0].bucket.ranges"},
		{"start not below end", types.MaterializeOutputColumn{From: "spend", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "a", Start: raw(10), End: raw(10)}}}},
			errors.PULSE_RANGE_INVALID, "output.columns[0].bucket.ranges"},
		{"duplicate label", types.MaterializeOutputColumn{From: "day", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "a", End: raw("2024-01-01")}, {Label: "a", Start: raw("2024-02-01")}}}},
			errors.PULSE_RANGE_DUPLICATE_LABEL, "output.columns[0].bucket.ranges"},
		{"bucket then an illegal type", types.MaterializeOutputColumn{From: "spend", As: "x", Type: "packed_bool", Bucket: &types.MaterializeBucket{Ranges: spendRanges()}},
			errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED, "output.columns[0].type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := map[string]any{"path": c.path}
			if c.code != errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED {
				want["column"] = c.col.From
			}
			assertRecodeRefusal(t, p, fsys, bucketReq("", c.col), c.code, want)
		})
	}

	t.Run("not numeric or date", func(t *testing.T) {
		recodeSource(t, p)
		req := recodeReq(types.MaterializeOutputColumn{From: "answer", Bucket: &types.MaterializeBucket{Ranges: spendRanges()}})
		assertRecodeRefusal(t, p, fsys, req, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "bucket_unsupported_type", "path": "output.columns[0].bucket"})
	})

	t.Run("hidden range tables", func(t *testing.T) {
		hp, hfs := matPulse(t, Options{
			Extensions:     Extensions{RangeTables: bucketTables()},
			FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "capability:materialize", "capability:materialize_output"}},
		})
		bucketSource(t, hp)
		req := bucketReq("", types.MaterializeOutputColumn{From: "spend", Bucket: &types.MaterializeBucket{RangeTable: "spend_band"}})
		assertRecodeRefusal(t, hp, hfs, req, errors.PULSE_RANGE_TABLE_UNKNOWN, map[string]any{"table": "spend_band"})
	})
}
