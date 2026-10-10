package io

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// floatFixture is one ordinary-decimal column: values f32 holds in
// RANGE but rounds, so the old range-only rule inferred f32 for it.
func floatFixture() ([]string, [][]string) {
	return []string{"price"}, [][]string{{"0.1"}, {"123456.789012"}, {"42.5"}}
}

// TestInfer_FloatDefaultsF64: an all-float column infers f64 by default
// (empty and "f64" alike) and f32 only on the "f32" opt-in.
func TestInfer_FloatDefaultsF64(t *testing.T) {
	cols, rows := floatFixture()
	for width, want := range map[string]encoding.FieldType{
		"":            encoding.FieldTypeF64,
		FloatWidthF64: encoding.FieldTypeF64,
		FloatWidthF32: encoding.FieldTypeF32,
	} {
		res, err := InferSchemaWithOptions(newMockReader(cols, rows), InferOptions{FloatWidth: width})
		if err != nil {
			t.Fatalf("FloatWidth %q: %v", width, err)
		}
		if got := res.Schema.Fields[0].Type; got != want {
			t.Errorf("FloatWidth %q: price = %s, want %s", width, got, want)
		}
	}
	// The opt-in keeps the range rule: a value past f32's range is f64.
	res, err := InferSchemaWithOptions(newMockReader(cols, append(rows, []string{"1e300"})), InferOptions{FloatWidth: FloatWidthF32})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Errorf("f32 opt-in with 1e300: price = %s, want f64", got)
	}
}

// TestImport_FloatRoundTripsExactly: under the default, 0.1 and
// 123456.789012 import as f64 and decode to exactly the float64 the
// source text parses to.
func TestImport_FloatRoundTripsExactly(t *testing.T) {
	cols, rows := floatFixture()
	rep, _, fs, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Fatalf("price = %s, want f64", got)
	}
	_, vals, _ := decodeAll(t, fs, "out.pulse")
	for k, row := range rows {
		want, _ := strconv.ParseFloat(row[0], 64)
		if vals[k]["price"] != want {
			t.Errorf("row %d price = %v, want %v exactly", k, vals[k]["price"], want)
		}
	}
}

// moneySum imports a money column at width and returns the sum of the
// decoded values, plus the f64 reference sum of the source text.
func moneySum(t *testing.T, width string) (got, ref float64) {
	t.Helper()
	var rows [][]string
	for i := 0; i < 1000; i++ {
		cell := fmt.Sprintf("%d.%02d", 1000+i*37, i%100)
		v, _ := strconv.ParseFloat(cell, 64)
		ref += v
		rows = append(rows, []string{cell})
	}
	_, _, fs, err := runGroupImport(t, newMockReader([]string{"amount"}, rows), nil, func(j *ImportJob) { j.FloatWidth = width })
	if err != nil {
		t.Fatal(err)
	}
	_, vals, _ := decodeAll(t, fs, "out.pulse")
	for _, v := range vals {
		got += v["amount"]
	}
	return got, ref
}

// TestImport_MoneySumMatchesF64Reference: a money column's sum over the
// imported cohort is the f64 reference sum — bit for bit — under the
// default; the f32 opt-in drifts, which is why it is not the default.
func TestImport_MoneySumMatchesF64Reference(t *testing.T) {
	got, ref := moneySum(t, "")
	if got != ref {
		t.Errorf("default sum = %v, want the f64 reference %v", got, ref)
	}
	if f32, _ := moneySum(t, FloatWidthF32); f32 == ref {
		t.Errorf("f32 sum = %v equals the reference; the fixture no longer exercises f32 rounding", f32)
	}
}

func requireFloatWidthRefusal(t *testing.T, err error, value string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_FLOAT_WIDTH_INVALID {
		t.Fatalf("err = %v, want PULSE_IMPORT_FLOAT_WIDTH_INVALID", err)
	}
	if ce.Details["value"] != value {
		t.Errorf("details %v, want value %q", ce.Details, value)
	}
}

// TestImport_FloatWidthInvalidRefused: any width but f64 / f32 is a
// coded refusal on Run and Predict of both jobs and on inference itself,
// even when an explicit Schema means inference would never consult it.
func TestImport_FloatWidthInvalidRefused(t *testing.T) {
	cols, rows := floatFixture()
	for _, bad := range []string{"f16", "F64", "double"} {
		_, err := InferSchemaWithOptions(newMockReader(cols, rows), InferOptions{FloatWidth: bad})
		requireFloatWidthRefusal(t, err, bad)

		_, _, _, err = runGroupImport(t, newMockReader(cols, rows), nil, func(j *ImportJob) { j.FloatWidth = bad })
		requireFloatWidthRefusal(t, err, bad)

		_, err = predictJob(t, newMockReader(cols, rows), func(j *ImportJob) { j.FloatWidth = bad })
		requireFloatWidthRefusal(t, err, bad)

		explicit := &encoding.Schema{Fields: []encoding.Field{{Name: "price", Type: encoding.FieldTypeF64}}}
		_, _, _, err = runGroupImport(t, newMockReader(cols, rows), nil, func(j *ImportJob) { j.FloatWidth = bad; j.Schema = explicit })
		requireFloatWidthRefusal(t, err, bad)
		_, err = predictJob(t, newMockReader(cols, rows), func(j *ImportJob) { j.FloatWidth = bad; j.Schema = explicit })
		requireFloatWidthRefusal(t, err, bad)

		for _, schema := range []*encoding.Schema{nil, explicit} {
			cj := NewConvertJob(newMockReader(cols, rows), &collectWriter{})
			cj.FloatWidth, cj.Schema = bad, schema
			_, err = cj.Run(context.Background())
			requireFloatWidthRefusal(t, err, bad)
			cj = NewConvertJob(newMockReader(cols, rows), &collectWriter{})
			cj.FloatWidth, cj.Schema = bad, schema
			_, err = cj.Predict(context.Background())
			requireFloatWidthRefusal(t, err, bad)
		}
	}
}

// TestImportPredict_FloatWidthMatchesRun: import Predict (and convert
// Predict) infer the width Run writes, under the default and the opt-in.
func TestImportPredict_FloatWidthMatchesRun(t *testing.T) {
	cols, rows := floatFixture()
	for _, width := range []string{"", FloatWidthF32} {
		rep, _, _, err := runGroupImport(t, newMockReader(cols, rows), nil, func(j *ImportJob) { j.FloatWidth = width })
		if err != nil {
			t.Fatal(err)
		}
		pred, err := predictJob(t, newMockReader(cols, rows), func(j *ImportJob) { j.FloatWidth = width })
		if err != nil {
			t.Fatal(err)
		}
		cj := NewConvertJob(newMockReader(cols, rows), &collectWriter{})
		cj.FloatWidth = width
		cpred, err := cj.Predict(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		run, p, c := rep.Schema.Fields[0].Type, pred.Schema.Fields[0].Type, cpred.Schema.Fields[0].Type
		if p != run || c != run {
			t.Errorf("FloatWidth %q: run %s, import predict %s, convert predict %s", width, run, p, c)
		}
	}
}

// TestImport_FloatOverrideWinsOverDefault: a ColumnTypeOverrides entry
// (the managed-import force_type) still forces f32 under the f64
// default, and f64 under the f32 opt-in.
func TestImport_FloatOverrideWinsOverDefault(t *testing.T) {
	cols, rows := floatFixture()
	for width, forced := range map[string]encoding.FieldType{"": encoding.FieldTypeF32, FloatWidthF32: encoding.FieldTypeF64} {
		rep, _, _, err := runGroupImport(t, newMockReader(cols, rows), nil, func(j *ImportJob) {
			j.FloatWidth = width
			j.ColumnTypeOverrides = map[string]encoding.FieldType{"price": forced}
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := rep.Schema.Fields[0].Type; got != forced {
			t.Errorf("FloatWidth %q, force %s: price = %s", width, forced, got)
		}
	}
}
