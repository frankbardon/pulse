package pulse

import (
	"context"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The output column `type` cast (types.MaterializeOutputColumn.Type):
// the numeric / bool / temporal half of the cast matrix, exact or
// failing, with on_cast_error and predict's static refusals and risks.

func intPtr(i int) *int { return &i }

// TestMaterializeCast_LegalPairsWriteExactValues: every legal pair the
// source offers, over source and derived columns, writes the exact cast
// of each stored value under the declared type — and the predicted
// schema is the written one.
func TestMaterializeCast_LegalPairsWriteExactValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "id", As: "id8", Type: "u8"},
		{From: "week", As: "week_f", Type: "f64"},
		{From: "flag", As: "flag_n", Type: "u4"},
		{From: "day", As: "day_ts", Type: "datetime"},
		{From: "price", As: "price_f", Type: "f32"},
		{From: "price", As: "price4", Type: "decimal128", Precision: intPtr(12), Scale: intPtr(4)},
		{From: "tags", As: "tags16", Type: "set_u16"},
		{From: "id", As: "id_dec", Type: "decimal128"},
		{From: "week", As: "week_day", Type: "date"},
		{From: "LOG_spend", As: "log32", Type: "f32"},
		{From: "rn", As: "rn16", Type: "u16"},
		{From: "region"}, // no cast
	}})
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", res.Warnings)
	}
	schema, rows := readAll(t, p, "out.pulse")
	_, env := predictMat(t, p, req)
	assertPredictedSchema(t, env.Columns, schema)
	_, src := readAll(t, p, "src.pulse")
	wantTypes := []encoding.FieldType{encoding.FieldTypeU8, encoding.FieldTypeF64, encoding.FieldTypeU4, encoding.FieldTypeDateTime,
		encoding.FieldTypeF32, encoding.FieldTypeDecimal128, encoding.FieldTypeSetU16, encoding.FieldTypeDecimal128,
		encoding.FieldTypeDate, encoding.FieldTypeF32, encoding.FieldTypeU16, encoding.FieldTypeCategoricalU8}
	for i, f := range schema.Fields {
		if f.Type != wantTypes[i] {
			t.Errorf("%s type = %s, want %s", f.Name, f.Type, wantTypes[i])
		}
	}
	if f := schema.Fields[5]; f.Precision != 12 || f.Scale != 4 {
		t.Errorf("price4 shape = (%d,%d), want (12,4)", f.Precision, f.Scale)
	}
	if f := schema.Fields[7]; f.Precision != 38 || f.Scale != 0 {
		t.Errorf("id_dec shape = (%d,%d), want the default (38,0)", f.Precision, f.Scale)
	}
	if d := schema.Fields[6].Dictionary; d == nil || strings.Join(d.Values(), ",") != "a,b,c" {
		t.Errorf("tags16 dictionary = %v, want the source's a,b,c", d)
	}
	for r, row := range rows {
		s := src[r]
		mant := func(v any) *big.Int { return v.(encoding.Decimal128).Mantissa() }
		var dayTS any
		if s[6] != nil {
			dayTS = int64(s[6].(int32)) * 86400
		}
		checks := []struct {
			col  int
			ok   bool
			what string
		}{
			{0, row[0] == s[0], "id8"},
			{1, row[1] == float64(s[2].(uint64)), "week_f"},
			{2, row[2] == map[bool]uint64{true: 1, false: 0}[s[5].(bool)], "flag_n"},
			{3, row[3] == dayTS, "day_ts"},
			{4, row[4] == float32(s[7].(encoding.Decimal128).Float64(2)), "price_f"},
			{5, mant(row[5]).Cmp(new(big.Int).Mul(mant(s[7]), big.NewInt(100))) == 0, "price4"},
			{7, mant(row[7]).Cmp(new(big.Int).SetUint64(s[0].(uint64))) == 0, "id_dec"},
			{8, row[8] == int32(s[2].(uint64)), "week_day"},
			{10, row[10] == uint64(r+1), "rn16"},
			{11, row[11] == s[1], "region"},
		}
		for _, c := range checks {
			if !c.ok {
				t.Errorf("row %d %s = %#v", r, c.what, row[c.col])
			}
		}
		if (row[6] == nil) != (s[8] == nil) || (row[6] != nil && strings.Join(row[6].([]string), ",") != strings.Join(s[8].([]string), ",")) {
			t.Errorf("row %d tags16 = %v, want %v", r, row[6], s[8])
		}
		switch spend := s[3]; {
		case spend == nil:
			if row[9] != nil {
				t.Errorf("row %d log32 = %v, want null", r, row[9])
			}
		default:
			if want := float32(math.Log1p(spend.(float64))); row[9] != want {
				t.Errorf("row %d log32 = %v, want %v", r, row[9], want)
			}
		}
	}
}

// TestMaterializeCast_BadValueFailsTheRun: a value the cast cannot
// represent — a fraction to an integer, a value past the range, a value
// other than 0 / 1 to packed_bool — fails the run with
// PULSE_MATERIALIZE_CAST_FAILED naming the column, the output row, the
// value and the reason; nothing is written. Predict lists the check.
func TestMaterializeCast_BadValueFailsTheRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		col    types.MaterializeOutputColumn
		row    int64
		value  string
		reason string
		check  string
	}{
		{"fractional", types.MaterializeOutputColumn{From: "spend", As: "s8", Type: "u8"}, 0, "0.5", "fractional", "f64 → u8: integral, 0 ≤ v ≤ 255"},
		{"overflow", types.MaterializeOutputColumn{From: "day", As: "d8", Type: "u8"}, 0, "19000", "overflow", "date → u8: 0 ≤ v ≤ 255"},
		{"not boolean", types.MaterializeOutputColumn{From: "id", As: "b", Type: "packed_bool"}, 1, "2", "not_boolean", "u32 → packed_bool: v is 0 or 1"},
		{"decimal scale", types.MaterializeOutputColumn{From: "price", As: "p1", Type: "decimal128", Scale: intPtr(1), Precision: intPtr(10)}, 1, "20.25", "fractional", "decimal128(10,2) → decimal128(10,1): at most 1 fractional digits"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, fsys := matPulse(t, Options{})
			buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
			req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"},
				Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "id"}, c.col}}}
			_, err := p.Materialize(context.Background(), req, "out.pulse", MaterializeOptions{})
			ce := assertCode(t, err, errors.PULSE_MATERIALIZE_CAST_FAILED)
			for k, v := range map[string]any{"column": c.col.As, "row": c.row, "value": c.value, "reason": c.reason} {
				if ce.Details[k] != v {
					t.Errorf("details.%s = %#v, want %#v (%v)", k, ce.Details[k], v, ce.Details)
				}
			}
			assertAbsent(t, fsys, "out.pulse")
			_, res := predictMat(t, p, req)
			if len(res.Risks) != 1 || res.Risks[0].Code != string(errors.PULSE_MATERIALIZE_CAST_FAILED) ||
				!strings.HasPrefix(res.Risks[0].Check, c.check+" — ") || res.Risks[0].Path != "output.columns[1].type" {
				t.Errorf("risks = %+v, want one CAST_FAILED %q", res.Risks, c.check)
			}
		})
	}
}

// TestMaterializeCast_OnCastErrorNullNullsAndCounts: on_cast_error:
// "null" writes a null in place of each value the cast refuses and warns
// once with the count and the first row; predict codes its risk
// PULSE_MATERIALIZE_CAST_NULLED.
func TestMaterializeCast_OnCastErrorNullNullsAndCounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "level", As: "lvl", Type: "packed_bool", Nullable: boolPtr(true), OnCastError: "null"},
	}}}
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, rows := readAll(t, p, "out.pulse")
	for r, row := range rows {
		var want any
		switch r {
		case 0:
			want = false
		case 1:
			want = true
		}
		if row[0] != want {
			t.Errorf("row %d = %v, want %v", r, row[0], want)
		}
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != errors.PULSE_MATERIALIZE_CAST_NULLED {
		t.Fatalf("warnings = %v, want one PULSE_MATERIALIZE_CAST_NULLED", res.Warnings)
	}
	d := res.Warnings[0].Details
	if d["column"] != "lvl" || d["count"] != int64(10) || d["first_row"] != int64(2) || d["from_type"] != "u4" || d["to_type"] != "packed_bool" {
		t.Errorf("warning details = %v", d)
	}
	_, pres := predictMat(t, p, req)
	if len(pres.Risks) != 1 || pres.Risks[0].Code != string(errors.PULSE_MATERIALIZE_CAST_NULLED) {
		t.Errorf("risks = %+v, want one CAST_NULLED", pres.Risks)
	}
}

// TestMaterializeCast_StaticRefusalsMatchPredict: an illegal pair, an
// unknown type, a bad decimal shape and every on_cast_error misuse are
// refused before anything is read — by the run and by predict, with the
// same code and details; nothing is written.
func TestMaterializeCast_StaticRefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	type col = types.MaterializeOutputColumn
	cases := []struct {
		name    string
		col     col
		code    errors.Code
		details map[string]any
	}{
		{"categorical to number", col{From: "region", Type: "u8"}, errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED,
			map[string]any{"column": "region", "path": "output.columns[0].type", "from": "region", "from_type": "categorical_u8", "to_type": "u8"}},
		{"number to categorical", col{From: "id", Type: "categorical_u16"}, errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED,
			map[string]any{"from_type": "u32", "to_type": "categorical_u16"}},
		{"set to a scalar", col{From: "tags", As: "t", Type: "u8"}, errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED,
			map[string]any{"column": "t", "from": "tags", "from_type": "set_u8", "to_type": "u8"}},
		{"bool to date", col{From: "flag", Type: "date"}, errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED,
			map[string]any{"from_type": "packed_bool", "to_type": "date"}},
		{"derived to set", col{From: "LOG_spend", Type: "set_u8"}, errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED,
			map[string]any{"from_type": "f64", "to_type": "set_u8"}},
		{"unknown type", col{From: "id", Type: "int32"}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "unknown_type", "path": "output.columns[0].type"}},
		{"precision out of range", col{From: "id", Type: "decimal128", Precision: intPtr(39)}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "decimal_shape", "path": "output.columns[0].precision"}},
		{"scale past precision", col{From: "price", Type: "decimal128", Scale: intPtr(11)}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "decimal_shape"}},
		{"shape on a non-decimal", col{From: "id", Type: "u64", Scale: intPtr(2)}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "decimal_shape"}},
		{"shape without a type", col{From: "price", Precision: intPtr(12)}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "decimal_shape"}},
		{"on_cast_error unknown", col{From: "spend", Type: "u8", OnCastError: "skip"}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "on_cast_error_invalid", "path": "output.columns[0].on_cast_error"}},
		{"on_cast_error without a type", col{From: "spend", OnCastError: "null"}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "on_cast_error_without_type"}},
		{"on_cast_error null on a non-nullable source", col{From: "id", Type: "u8", OnCastError: "null"}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "on_cast_error_not_nullable", "path": "output.columns[0].on_cast_error"}},
		{"on_cast_error null under nullable false", col{From: "spend", Type: "u8", Nullable: boolPtr(false), OnCastError: "null"}, errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			map[string]any{"reason": "on_cast_error_not_nullable"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{c.col}})
			_, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
			ce := assertCode(t, err, c.code)
			for k, v := range c.details {
				if ce.Details[k] != v {
					t.Errorf("details.%s = %v, want %v (details %v)", k, ce.Details[k], v, ce.Details)
				}
			}
			assertAbsent(t, fsys, "out.pulse")
			env, _ := predictMat(t, p, req)
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("predict errors = %+v, want %s first", env.Errors, c.code)
			}
			for k, v := range ce.Details {
				if env.Errors[0].Details[k] != v {
					t.Errorf("predict details.%s = %v, run %v", k, env.Errors[0].Details[k], v)
				}
			}
			if env.Errors[0].Message != ce.Message {
				t.Errorf("predict message %q, run %q", env.Errors[0].Message, ce.Message)
			}
		})
	}
}

// TestMaterializeCast_U64AboveTwoTo53StaysExact: a source u64 past 2^53
// cast to decimal128, and a decimal128 holding one cast to u64, are the
// exact stored integers — never a float64 echo (2^53+1 would round to
// 2^53).
func TestMaterializeCast_U64AboveTwoTo53StaysExact(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	b, err := p.NewCohortBuilder(ctx, "big.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU64, Description: "Large unsigned counter value"},
		{Name: "d", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 0, Description: "Large exact decimal counter"},
	}}, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vals := []uint64{1<<53 + 1, math.MaxUint64, 1<<63 + 7}
	for _, v := range vals {
		if err := b.Append(CohortRow{v, bigDecimal(t, new(big.Int).SetUint64(v).String())}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	req := &Request{Cohort: &types.Cohort{Filename: "big.pulse"}, Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "n", As: "n_dec", Type: "decimal128"},
		{From: "d", As: "d_u64", Type: "u64"},
		{From: "n", As: "n_u64", Type: "u64"},
	}}}
	if _, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, rows := readAll(t, p, "out.pulse")
	for i, v := range vals {
		if m := rows[i][0].(encoding.Decimal128).Mantissa(); m.Cmp(new(big.Int).SetUint64(v)) != 0 {
			t.Errorf("row %d n_dec = %s, want %d", i, m, v)
		}
		if rows[i][1] != v || rows[i][2] != v {
			t.Errorf("row %d d_u64 / n_u64 = %v / %v, want %d", i, rows[i][1], rows[i][2], v)
		}
	}
}

// TestMaterializeCast_DateTimeZoneAcrossDST: datetime → date truncates
// in the request time_zone (else Options.DefaultTimeZone, else UTC) and
// date → datetime is local midnight there — across Europe/Berlin's
// 2024-03-31 spring-forward.
func TestMaterializeCast_DateTimeZoneAcrossDST(t *testing.T) {
	t.Parallel()

	const (
		mar31 = 19813
		apr1  = 19814
	)
	mid31 := int64(mar31)*86400 - 3600 // local midnight, CET (+1)
	mid01 := int64(apr1)*86400 - 7200  // local midnight, CEST (+2)
	secs := []int64{mid31 - 1, mid31, mid31 + 3*3600, mid01 - 1, mid01}
	wantDays := []int32{mar31 - 1, mar31, mar31, mar31, apr1}
	cases := []struct {
		name string
		opts Options
		tz   string
		zone bool
	}{
		{"request time_zone", Options{}, "Europe/Berlin", true},
		{"default time zone", Options{DefaultTimeZone: "Europe/Berlin"}, "", true},
		{"utc", Options{}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			p, _ := matPulse(t, c.opts)
			b, err := p.NewCohortBuilder(ctx, "ts.pulse", encoding.Schema{Fields: []encoding.Field{
				{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Event instant in epoch seconds"},
				{Name: "d", Type: encoding.FieldTypeDate, Description: "Calendar day of the event"},
			}}, CohortBuilderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for i, s := range secs {
				if err := b.Append(CohortRow{s, int32(mar31 + i%2)}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.Close(); err != nil {
				t.Fatal(err)
			}
			req := &Request{Cohort: &types.Cohort{Filename: "ts.pulse"}, TimeZone: c.tz,
				Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
					{From: "ts", As: "day", Type: "date"},
					{From: "d", As: "midnight", Type: "datetime"},
				}}}
			if _, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{}); err != nil {
				t.Fatal(err)
			}
			_, rows := readAll(t, p, "out.pulse")
			for i, row := range rows {
				day := int32(math.Floor(float64(secs[i]) / 86400))
				mid := int64(mar31+i%2) * 86400
				if c.zone {
					day = wantDays[i]
					mid = map[int]int64{0: mid31, 1: mid01}[i%2]
				}
				if row[0] != day || row[1] != mid {
					t.Errorf("row %d = %v / %v, want %d / %d", i, row[0], row[1], day, mid)
				}
			}
		})
	}
}
