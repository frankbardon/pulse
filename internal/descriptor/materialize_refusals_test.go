package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/types"
)

// refusalSchema is the source every static-refusal row reads: numeric,
// decimal, categorical, set and date columns, all non-nullable but f.
func refusalSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU32, Description: "Count of the source"},
		{Name: "f", Type: encoding.FieldTypeF64, Nullable: true, Description: "Score of the source"},
		{Name: "p", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Price of the source"},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "a", "b"), Description: "Colour of the source"},
		{Name: "s", Type: encoding.FieldTypeSetU8, Dictionary: makeDictionary(t, "x"), Description: "Set of the source"},
		{Name: "d", Type: encoding.FieldTypeDate, Description: "Day of the source"},
	}}
}

// refusalInstance is bucketInstance plus the numeric table "dup", whose
// two ranges share a label: the shared range pass refuses it.
func refusalInstance() *InstanceSnapshot {
	one, two := 1.0, 2.0
	return NewInstanceSnapshot(&ExtensionsSnapshot{
		RangeTables: []descriptor.RangeTableMeta{
			{Name: "band", Kind: daterange.KindNumeric, RangeCount: 2},
			{Name: "q", Kind: daterange.KindDate, RangeCount: 2},
			{Name: "dup", Kind: daterange.KindNumeric, RangeCount: 2},
		},
		RangeTableSpecs: map[string]RangeTableSpecs{
			"band": bucketInstance().Extensions().RangeTableSpecs["band"],
			"q":    bucketInstance().Extensions().RangeTableSpecs["q"],
			"dup":  {Numeric: []numrange.Spec{{Label: "x", End: &one}, {Label: "x", Start: &two}}},
		},
	}, FeatureSet{})
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// TestMaterializeOutputLayout_StaticRefusals: every static refusal of
// the output block — shape, unknown names, cast keys, bucket sources —
// carries its code and the details.path (and reason / column) the run's
// pre-flight and predict both surface. One row per refusal arm.
func TestMaterializeOutputLayout_StaticRefusals(t *testing.T) {
	t.Parallel()

	i := func(v int) *int { return &v }
	cols := func(c ...types.MaterializeOutputColumn) *types.MaterializeOutput {
		return &types.MaterializeOutput{Columns: c}
	}
	rows := []struct {
		name    string
		out     *types.MaterializeOutput
		code    errors.Code
		details map[string]any
	}{
		{"columns and exclude", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "n"}}, Exclude: []string{"f"}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "columns_and_exclude", "path": "output"}},
		{"empty columns", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "no_columns", "path": "output.columns"}},
		{"shards max_records zero", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "n"}}, Shards: &types.MaterializeShards{}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "shards_max_records", "path": "output.shards.max_records", "max_records": 0}},
		{"from missing", cols(types.MaterializeOutputColumn{From: "n"}, types.MaterializeOutputColumn{As: "x"}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "from_missing", "path": "output.columns[1].from"}},
		{"exclude unknown", &types.MaterializeOutput{Exclude: []string{"n", "zz"}},
			errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN, map[string]any{"column": "zz", "path": "output.exclude[1]"}},
		{"exclude drops every column", &types.MaterializeOutput{Exclude: []string{"n", "f", "p", "c", "s", "d"}},
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "no_columns", "path": "output.exclude"}},
		{"from unknown", cols(types.MaterializeOutputColumn{From: "nn"}),
			errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN, map[string]any{"column": "nn", "path": "output.columns[0].from", "suggestion": "n"}},
		{"duplicate after rename", cols(types.MaterializeOutputColumn{From: "n"}, types.MaterializeOutputColumn{From: "f", As: "n"}),
			errors.PULSE_MATERIALIZE_OUTPUT_DUPLICATE, map[string]any{"column": "n", "path": "output.columns[1]", "first": "output.columns[0]"}},
		{"on_cast_error invalid", cols(types.MaterializeOutputColumn{From: "n", Type: "u8", OnCastError: "skip"}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "on_cast_error_invalid", "path": "output.columns[0].on_cast_error"}},
		{"on_cast_error without type", cols(types.MaterializeOutputColumn{From: "n", OnCastError: "fail"}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "on_cast_error_without_type", "path": "output.columns[0].on_cast_error"}},
		{"on_cast_error null on a non-nullable recode", cols(types.MaterializeOutputColumn{From: "c", OnCastError: "null", Recode: &types.MaterializeRecode{}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "on_cast_error_not_nullable", "path": "output.columns[0].on_cast_error"}},
		{"on_cast_error null on a non-nullable cast", cols(types.MaterializeOutputColumn{From: "n", Type: "u8", OnCastError: "null"}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "on_cast_error_not_nullable", "path": "output.columns[0].on_cast_error"}},
		{"precision without type", cols(types.MaterializeOutputColumn{From: "n", Precision: i(5)}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "decimal_shape", "path": "output.columns[0].precision"}},
		{"decimal precision out of range", cols(types.MaterializeOutputColumn{From: "n", Type: "decimal128", Precision: i(39)}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "decimal_shape", "path": "output.columns[0].precision"}},
		{"decimal scale past precision", cols(types.MaterializeOutputColumn{From: "p", Type: "decimal128", Scale: i(11)}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "decimal_shape", "path": "output.columns[0].precision"}},
		{"scale on a non-decimal type", cols(types.MaterializeOutputColumn{From: "n", Type: "u8", Scale: i(2)}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "decimal_shape", "path": "output.columns[0].precision"}},
		{"unknown type", cols(types.MaterializeOutputColumn{From: "n", Type: "u7"}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "unknown_type", "path": "output.columns[0].type"}},
		{"recode over a set", cols(types.MaterializeOutputColumn{From: "s", Recode: &types.MaterializeRecode{}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "recode_not_categorical", "path": "output.columns[0].recode"}},
		{"recode of an unknown label", cols(types.MaterializeOutputColumn{From: "c", As: "cc", Recode: &types.MaterializeRecode{Map: map[string]*string{"bb": nil}}}),
			errors.PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL, map[string]any{"column": "cc", "path": "output.columns[0].recode.map", "from": "c", "label": "bb", "suggestion": "b"}},
		{"recode to null on a non-nullable column", cols(types.MaterializeOutputColumn{From: "c", Recode: &types.MaterializeRecode{Map: map[string]*string{"a": nil}}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "recode_null_not_nullable", "path": "output.columns[0].recode.map", "label": "a"}},
		{"recode order names a label twice", cols(types.MaterializeOutputColumn{From: "c", Recode: &types.MaterializeRecode{Order: []string{"a", "a", "b"}}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "recode_order_duplicate", "path": "output.columns[0].recode.order", "label": "a"}},
		{"recode order names an unwritten label", cols(types.MaterializeOutputColumn{From: "c", Recode: &types.MaterializeRecode{Order: []string{"b", "z"}}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "recode_order_unknown", "path": "output.columns[0].recode.order", "label": "z"}},
		{"recode order omits a written label", cols(types.MaterializeOutputColumn{From: "c", Recode: &types.MaterializeRecode{Order: []string{"b"}}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "recode_order_missing", "path": "output.columns[0].recode.order", "label": "a"}},
		{"illegal cast pair", cols(types.MaterializeOutputColumn{From: "s", As: "sd", Type: "date"}),
			errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED, map[string]any{"column": "sd", "path": "output.columns[0].type", "from": "s", "from_type": "set_u8", "to_type": "date"}},
		{"bucket over a categorical", cols(types.MaterializeOutputColumn{From: "c", Bucket: &types.MaterializeBucket{RangeTable: "band"}}),
			errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "bucket_unsupported_type", "path": "output.columns[0].bucket"}},
		{"bucket with ranges and range_table", cols(types.MaterializeOutputColumn{From: "n", Bucket: &types.MaterializeBucket{RangeTable: "band",
			Ranges: []types.MaterializeBucketRange{{Label: "all"}}}}),
			errors.PULSE_RANGE_SOURCE_AMBIGUOUS, map[string]any{"column": "n", "path": "output.columns[0].bucket"}},
		{"bucket with neither", cols(types.MaterializeOutputColumn{From: "n", As: "nb", Bucket: &types.MaterializeBucket{RangeTable: "  "}}),
			errors.PULSE_RANGE_SOURCE_AMBIGUOUS, map[string]any{"column": "nb", "path": "output.columns[0].bucket"}},
		{"numeric table on a date column", cols(types.MaterializeOutputColumn{From: "d", Bucket: &types.MaterializeBucket{RangeTable: "band"}}),
			errors.PULSE_RANGE_TABLE_KIND_MISMATCH, map[string]any{"column": "d", "path": "output.columns[0].bucket.range_table", "table": "band", "want": "date", "got": "numeric"}},
		{"table the shared pass refuses", cols(types.MaterializeOutputColumn{From: "n", Bucket: &types.MaterializeBucket{RangeTable: "dup"}}),
			errors.PULSE_RANGE_DUPLICATE_LABEL, map[string]any{"column": "n", "path": "output.columns[0].bucket.range_table"}},
		{"string bound on a numeric column", cols(types.MaterializeOutputColumn{From: "n", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "lo", End: raw("5")}, {Label: "hi", Start: raw(`"5"`)}}}}),
			errors.PULSE_RANGE_INVALID, map[string]any{"column": "n", "path": "output.columns[0].bucket.ranges[1].start", "label": "hi"}},
		{"number bound on a date column", cols(types.MaterializeOutputColumn{From: "d", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "early", Start: raw("null"), End: raw("20240101")}}}}),
			errors.PULSE_RANGE_INVALID, map[string]any{"column": "d", "path": "output.columns[0].bucket.ranges[0].end", "label": "early"}},
		{"overlapping inline numeric ranges", cols(types.MaterializeOutputColumn{From: "n", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "a", Start: raw("0"), End: raw("10")}, {Label: "b", Start: raw("5"), End: raw("15")}}}}),
			errors.PULSE_RANGE_OVERLAP, map[string]any{"column": "n", "path": "output.columns[0].bucket.ranges"}},
		{"overlapping inline date ranges", cols(types.MaterializeOutputColumn{From: "d", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "a", Start: raw(`"2024-01-01"`), End: raw(`"2024-02-01"`)}, {Label: "b", Start: raw(`"2024-01-15"`), End: raw(`"2024-03-01"`)}}}}),
			errors.PULSE_RANGE_OVERLAP, map[string]any{"column": "d", "path": "output.columns[0].bucket.ranges"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			req := &types.Request{Output: row.out}
			layout, ce := MaterializeOutputLayout(req, refusalSchema(t), nil, refusalInstance())
			if ce == nil {
				t.Fatalf("layout %v resolved, want %s", layoutNames(layout), row.code)
			}
			if ce.Code != row.code {
				t.Fatalf("code %s (%s), want %s", ce.Code, ce.Message, row.code)
			}
			for k, want := range row.details {
				if got := ce.Details[k]; got != want {
					t.Errorf("details[%q] = %v, want %v (all: %v)", k, got, want, ce.Details)
				}
			}
		})
	}
}

// TestCodedOf_FreshDetails: the shared range pass's refusal is
// re-raised as a fresh error whose details are a copy — adding column /
// path to it never writes into the pass's own error — and an uncoded
// pass error becomes PULSE_RANGE_INVALID.
func TestCodedOf_FreshDetails(t *testing.T) {
	t.Parallel()

	_, err := numrange.Compile(refusalInstance().Extensions().RangeTableSpecs["dup"].Numeric)
	pass, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("numrange.Compile(dup) = %v, want a coded error", err)
	}
	got := codedOf(pass)
	if got == pass || got.Code != pass.Code || got.Message != pass.Message || got.Details["label"] != "x" {
		t.Fatalf("codedOf = %+v, want a fresh copy of %+v", got, pass)
	}
	got.Details["column"] = "n"
	if _, leaked := pass.Details["column"]; leaked {
		t.Errorf("details aliased: the pass's error gained column")
	}

	plain := codedOf(stderrors.New("bad range"))
	if plain.Code != errors.PULSE_RANGE_INVALID {
		t.Errorf("uncoded pass error = %s, want PULSE_RANGE_INVALID", plain.Code)
	}
}

// TestBucketCategorical_Rungs: a bucket's written type is the narrowest
// categorical rung holding its label count.
func TestBucketCategorical_Rungs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		n    int
		want encoding.FieldType
	}{
		{1, encoding.FieldTypeCategoricalU8},
		{int(encoding.FieldTypeCategoricalU8.MaxCategoricalEntries()), encoding.FieldTypeCategoricalU8},
		{int(encoding.FieldTypeCategoricalU8.MaxCategoricalEntries()) + 1, encoding.FieldTypeCategoricalU16},
		{int(encoding.FieldTypeCategoricalU16.MaxCategoricalEntries()), encoding.FieldTypeCategoricalU16},
		{int(encoding.FieldTypeCategoricalU16.MaxCategoricalEntries()) + 1, encoding.FieldTypeCategoricalU32},
	} {
		if got := bucketCategorical(tc.n); got != tc.want {
			t.Errorf("bucketCategorical(%d) = %s, want %s", tc.n, got, tc.want)
		}
	}
}

// TestDerivedField_Inherits: a derived column is written as a nullable
// f64 with its description, unless it inherits its state field's type
// and dictionary (ATTR_PREVIOUS_STATE).
func TestDerivedField_Inherits(t *testing.T) {
	t.Parallel()

	plain := derivedField(MaterializeColumn{Name: "z", Description: "z score", Type: encoding.FieldTypeU8})
	if plain.Type != encoding.FieldTypeF64 || !plain.Nullable || plain.Description != "z score" || plain.Dictionary != nil {
		t.Errorf("plain derived field = %+v, want a nullable f64", plain)
	}
	dict := makeDictionary(t, "on", "off")
	inh := derivedField(MaterializeColumn{Name: "prev", Inherits: "state", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict})
	if inh.Type != encoding.FieldTypeCategoricalU8 || inh.Dictionary != dict || !inh.Nullable {
		t.Errorf("inheriting derived field = %+v, want the state field's categorical_u8 and dictionary", inh)
	}
}

// TestMaterializeRunRefusals_Details: the run-time refusals and warning
// the sink raises carry their code and the documented details keys
// (column, row / count / first_row, the cast's types, value, reason;
// archive, max_records, limit for the shard limit).
func TestMaterializeRunRefusals_Details(t *testing.T) {
	t.Parallel()

	from := encoding.Field{Name: "x", Type: encoding.FieldTypeF64}
	to := encoding.Field{Name: "x8", Type: encoding.FieldTypeU8}
	for _, tc := range []struct {
		name    string
		ce      *errors.CodedError
		code    errors.Code
		details map[string]any
	}{
		{"output null", MaterializeOutputNull("x8", 7),
			errors.PULSE_MATERIALIZE_OUTPUT_NULL, map[string]any{"column": "x8", "row": int64(7)}},
		{"cast failed", MaterializeCastFailed("x8", 3, from, to, "256.5", "out of range"),
			errors.PULSE_MATERIALIZE_CAST_FAILED, map[string]any{"column": "x8", "row": int64(3), "from_type": "f64", "to_type": "u8", "value": "256.5", "reason": "out of range"}},
		{"cast nulled", MaterializeCastNulled("x8", 4, 2, from, to),
			errors.PULSE_MATERIALIZE_CAST_NULLED, map[string]any{"column": "x8", "count": int64(4), "first_row": int64(2), "from_type": "f64", "to_type": "u8"}},
		{"shard limit", MaterializeShardLimit("out.pulse", 10, 655350),
			errors.PULSE_SHARD_COUNT_LIMIT, map[string]any{"archive": "out.pulse", "max_records": 10, "limit": 65535, "row": int64(655350)}},
	} {
		if tc.ce.Code != tc.code {
			t.Errorf("%s: code %s, want %s", tc.name, tc.ce.Code, tc.code)
		}
		if len(tc.ce.Details) != len(tc.details) {
			t.Errorf("%s: details %v, want exactly %v", tc.name, tc.ce.Details, tc.details)
		}
		for k, want := range tc.details {
			if got := tc.ce.Details[k]; got != want {
				t.Errorf("%s: details[%q] = %v (%T), want %v", tc.name, k, got, got, want)
			}
		}
		if tc.ce.Message == "" {
			t.Errorf("%s: empty message", tc.name)
		}
	}
	if m := MaterializeShardLimit("out.pulse", 10, 655350).Message; m != "output row 655350 would open shard "+strconv.Itoa(65536)+
		" of out.pulse; an archive holds at most 65535 shards — raise output.shards.max_records; nothing was written" {
		t.Errorf("shard limit message %q", m)
	}
}
