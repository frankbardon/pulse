package pulse

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The categorical half of the output column `type` cast: categorical ↔
// categorical (the dictionary kept; a narrowing that does not fit
// refused statically), categorical → numeric (labels parsed) and
// numeric → categorical (values stringified into a first-seen
// dictionary).

// catSource writes cat.pulse: wide256 / wide257 (categorical_u16 whose
// dictionaries hold 256 / 257 labels), code (categorical_u8 labels
// "1", "2", "3.5", "n/a", nullable), n (u16, 300 distinct values) and
// x (f64), over 300 rows.
func catSource(t *testing.T, p *Pulse) {
	t.Helper()
	dict := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			if _, err := d.Add("L" + strconv.Itoa(i)); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	codes := encoding.NewDictionary()
	for _, l := range []string{"1", "2", "3.5", "n/a"} {
		if _, err := codes.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	b, err := p.NewCohortBuilder(context.Background(), "cat.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "wide256", Type: encoding.FieldTypeCategoricalU16, Description: "Category with 256 dictionary labels", Dictionary: dict(256)},
		{Name: "wide257", Type: encoding.FieldTypeCategoricalU16, Description: "Category with 257 dictionary labels", Dictionary: dict(257)},
		{Name: "code", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Response code stored as a label", Dictionary: codes},
		{Name: "n", Type: encoding.FieldTypeU16, Description: "Distinct counter per record"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Measured value per record"},
	}}, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	xs := []float64{3, 2.5, 3, 1e6, math.Copysign(0, -1)}
	for i := 0; i < 300; i++ {
		var code any = []string{"2", "1"}[i%2]
		switch i {
		case 2:
			code = "3.5"
		case 3:
			code = nil
		case 5:
			code = "n/a"
		}
		row := CohortRow{"L" + strconv.Itoa(i%256), "L" + strconv.Itoa(i%257), code, uint64(1000 + i), xs[i%len(xs)]}
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func catReq(cols ...types.MaterializeOutputColumn) *Request {
	return &Request{Cohort: &types.Cohort{Filename: "cat.pulse"}, Output: &types.MaterializeOutput{Columns: cols}}
}

// TestMaterializeCastCategorical_WidenAndNarrow: a categorical_u16 whose
// dictionary holds exactly 256 labels narrows to categorical_u8 with its
// whole dictionary (order, codes, every label) and every cell unchanged;
// a widen keeps them too; 257 labels — one over — are refused before
// anything is read, by the run and by predict with the same code and
// details. Predict reports the written schema, dictionary included.
func TestMaterializeCastCategorical_WidenAndNarrow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	catSource(t, p)
	req := catReq(
		types.MaterializeOutputColumn{From: "wide256", As: "narrow", Type: "categorical_u8"},
		types.MaterializeOutputColumn{From: "code", As: "wide", Type: "categorical_u32"},
	)
	res, err := p.Materialize(ctx, req, "good.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
	schema, rows := readAll(t, p, "good.pulse")
	_, src := readAll(t, p, "cat.pulse")
	srcSchema, _ := readAll(t, p, "cat.pulse")
	if schema.Fields[0].Type != encoding.FieldTypeCategoricalU8 || schema.Fields[1].Type != encoding.FieldTypeCategoricalU32 {
		t.Fatalf("types %s / %s", schema.Fields[0].Type, schema.Fields[1].Type)
	}
	if got, want := schema.Fields[0].Dictionary.Values(), srcSchema.Fields[0].Dictionary.Values(); !slices.Equal(got, want) {
		t.Errorf("narrowed dictionary differs from the source's (%d vs %d labels)", len(got), len(want))
	}
	if got, want := schema.Fields[1].Dictionary.Values(), srcSchema.Fields[2].Dictionary.Values(); !slices.Equal(got, want) {
		t.Errorf("widened dictionary = %v, want %v", got, want)
	}
	for i, r := range rows {
		if r[0] != src[i][0] || r[1] != src[i][2] {
			t.Fatalf("row %d = %v, want %v / %v", i, r, src[i][0], src[i][2])
		}
	}
	_, pres := predictMat(t, p, req)
	for i, f := range schema.Fields {
		// Predict truncates a dictionary to its first 100 values; the
		// total is the written one.
		c := pres.Columns[i]
		if c.Type != f.Type.String() || c.Dictionary == nil || c.Dictionary.TotalEntries != f.Dictionary.Count() ||
			!slices.Equal(c.Dictionary.Values, f.Dictionary.Values()[:len(c.Dictionary.Values)]) {
			t.Errorf("predicted column %s = %s %+v, written %s with %d labels", f.Name, c.Type, c.Dictionary, f.Type, f.Dictionary.Count())
		}
	}
	if len(pres.Risks) != 0 {
		t.Errorf("risks %+v, want none (the dictionary is known and fits)", pres.Risks)
	}

	over := catReq(types.MaterializeOutputColumn{From: "wide257", Type: "categorical_u8"})
	_, err = p.Materialize(ctx, over, "over.pulse", MaterializeOptions{})
	ce := assertCode(t, err, errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED)
	if ce.Details["from_type"] != "categorical_u16" || ce.Details["to_type"] != "categorical_u8" ||
		!strings.Contains(ce.Details["reason"].(string), "257 labels") {
		t.Errorf("details %v", ce.Details)
	}
	assertAbsent(t, fsys, "over.pulse")
	env, _ := predictMat(t, p, over)
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED) || env.Errors[0].Message != ce.Message {
		t.Fatalf("predict errors %+v, want the run's refusal", env.Errors)
	}
	for k, v := range ce.Details {
		if env.Errors[0].Details[k] != v {
			t.Errorf("predict details.%s = %v, run %v", k, env.Errors[0].Details[k], v)
		}
	}
}

// TestMaterializeCastCategorical_ToNumeric: labels parse to the target —
// integers and decimals ("3.5" to f64) — and a label that is not a
// number ("n/a") or does not fit ("3.5" to u8) fails the run with
// CAST_FAILED (the label verbatim as details.value), or under
// on_cast_error: "null" is nulled and counted; predict lists each risk.
func TestMaterializeCastCategorical_ToNumeric(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	catSource(t, p)

	nulling := catReq(types.MaterializeOutputColumn{From: "code", As: "score", Type: "f64", OnCastError: "null"})
	res, err := p.Materialize(ctx, nulling, "good.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, rows := readAll(t, p, "good.pulse")
	for i, want := range []any{2.0, 1.0, 3.5, nil, 2.0, nil, 2.0} {
		if rows[i][0] != want {
			t.Errorf("row %d score = %#v, want %#v", i, rows[i][0], want)
		}
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != errors.PULSE_MATERIALIZE_CAST_NULLED ||
		res.Warnings[0].Details["count"] != int64(1) || res.Warnings[0].Details["first_row"] != int64(5) {
		t.Errorf("warnings %v, want one CAST_NULLED (count 1, first_row 5)", res.Warnings)
	}
	_, pres := predictMat(t, p, nulling)
	if len(pres.Risks) != 1 || pres.Risks[0].Code != string(errors.PULSE_MATERIALIZE_CAST_NULLED) ||
		!strings.HasPrefix(pres.Risks[0].Check, "categorical_u8 → f64: label is a number within f64 — ") {
		t.Errorf("risks %+v", pres.Risks)
	}

	for _, c := range []struct {
		col    types.MaterializeOutputColumn
		row    int64
		value  string
		reason string
		check  string
	}{
		{types.MaterializeOutputColumn{From: "code", As: "c8", Type: "u8"}, 2, "3.5", "fractional", "categorical_u8 → u8: label is an integral number, 0 ≤ v ≤ 255"},
		{types.MaterializeOutputColumn{From: "code", As: "cd", Type: "decimal128", Precision: intPtr(4), Scale: intPtr(1)}, 5, "n/a", "unparseable", "categorical_u8 → decimal128(4,1): label is a number, at most 1 fractional digits, |v| < 1e3"},
	} {
		req := catReq(c.col)
		_, err := p.Materialize(ctx, req, "bad.pulse", MaterializeOptions{})
		ce := assertCode(t, err, errors.PULSE_MATERIALIZE_CAST_FAILED)
		for k, v := range map[string]any{"column": c.col.As, "row": c.row, "value": c.value, "reason": c.reason} {
			if ce.Details[k] != v {
				t.Errorf("%s details.%s = %#v, want %#v", c.col.As, k, ce.Details[k], v)
			}
		}
		assertAbsent(t, fsys, "bad.pulse")
		_, pres := predictMat(t, p, req)
		if len(pres.Risks) != 1 || pres.Risks[0].Code != string(errors.PULSE_MATERIALIZE_CAST_FAILED) || !strings.HasPrefix(pres.Risks[0].Check, c.check+" — ") {
			t.Errorf("%s risks %+v, want %q", c.col.As, pres.Risks, c.check)
		}
	}
}

// TestMaterializeCastCategorical_FromNumeric: values stringify into a
// dictionary built in first-seen output order — an integral float
// without ".0", -0 as "0", 1e6 in plain digits — and the first distinct
// value past the rung (the 257th into categorical_u8) fails the run
// with overflow, or under on_cast_error: "null" is nulled and counted.
func TestMaterializeCastCategorical_FromNumeric(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	catSource(t, p)

	req := catReq(
		types.MaterializeOutputColumn{From: "x", As: "xc", Type: "categorical_u8"},
		types.MaterializeOutputColumn{From: "n", As: "nc", Type: "categorical_u16"},
	)
	res, err := p.Materialize(ctx, req, "good.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
	schema, rows := readAll(t, p, "good.pulse")
	if got, want := schema.Fields[0].Dictionary.Values(), []string{"3", "2.5", "1000000", "0"}; !slices.Equal(got, want) {
		t.Errorf("xc dictionary = %v, want first-seen %v", got, want)
	}
	if d := schema.Fields[1].Dictionary.Values(); len(d) != 300 || d[0] != "1000" || d[299] != "1299" {
		t.Errorf("nc dictionary = %d labels %v…, want 1000..1299 in order", len(d), d[:2])
	}
	for i, want := range []string{"3", "2.5", "3", "1000000", "0"} {
		if rows[i][0] != want || rows[i][1] != strconv.Itoa(1000+i) {
			t.Errorf("row %d = %v, want %s / %d", i, rows[i], want, 1000+i)
		}
	}
	_, pres := predictMat(t, p, req)
	if len(pres.Risks) != 1 || pres.Risks[0].Column != "xc" ||
		!strings.HasPrefix(pres.Risks[0].Check, "f64 → categorical_u8: finite, at most 256 distinct values — ") {
		t.Errorf("risks %+v, want only xc's (u16 fits categorical_u16)", pres.Risks)
	}

	over := catReq(types.MaterializeOutputColumn{From: "n", As: "n8", Type: "categorical_u8"})
	_, err = p.Materialize(ctx, over, "over.pulse", MaterializeOptions{})
	ce := assertCode(t, err, errors.PULSE_MATERIALIZE_CAST_FAILED)
	if ce.Details["row"] != int64(256) || ce.Details["value"] != "1256" || ce.Details["reason"] != "overflow" {
		t.Errorf("details %v, want row 256 value 1256 overflow", ce.Details)
	}
	assertAbsent(t, fsys, "over.pulse")
	_, pres = predictMat(t, p, over)
	if len(pres.Risks) != 1 || !strings.HasPrefix(pres.Risks[0].Check, "u16 → categorical_u8: at most 256 distinct values — ") {
		t.Errorf("risks %+v", pres.Risks)
	}

	over.Output.Columns[0].Nullable, over.Output.Columns[0].OnCastError = boolPtr(true), "null"
	res, err = p.Materialize(ctx, over, "nulled.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schema, rows = readAll(t, p, "nulled.pulse")
	if n := schema.Fields[0].Dictionary.Count(); n != 256 {
		t.Errorf("dictionary holds %d labels, want 256", n)
	}
	if rows[255][0] != "1255" || rows[256][0] != nil || rows[299][0] != nil {
		t.Errorf("rows 255 / 256 / 299 = %v / %v / %v", rows[255][0], rows[256][0], rows[299][0])
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Details["count"] != int64(44) || res.Warnings[0].Details["first_row"] != int64(256) {
		t.Errorf("warnings %v, want one CAST_NULLED count 44 first_row 256", res.Warnings)
	}
}
