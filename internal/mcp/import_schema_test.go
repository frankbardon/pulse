package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// newSchemaImportPulse seeds sources whose same-typed columns a
// POSITIONAL schema would silently swap: `a` and `b` are both u16-range
// integers, so only by-name matching keeps 100 under b.
func newSchemaImportPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	seed := map[string]string{
		"ab.csv":      "a,b,amount\n1,100,10.5\n2,200,20.25\n",
		"late.ndjson": "{\"a\":1,\"b\":100}\n{\"a\":2,\"b\":200,\"c\":7}\n",
		"dupe.csv":    "a,a,b\n1,2,3\n",
		"floats.csv":  "x\n0.1\n0.2\n",
	}
	for name, body := range seed {
		if err := afero.WriteFile(afs, name, []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, afs
}

// invokeImportOut calls pulse_import through the catalog's Invoke (the
// strict decode the transport runs) and returns the typed result.
func invokeImportOut(t *testing.T, p *pulse.Pulse, args string) (ImportOut, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolImport {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return ImportOut{}, err
		}
		res, ok := out.(ImportOut)
		if !ok {
			t.Fatalf("Invoke returned %T, want ImportOut", out)
		}
		return res, nil
	}
	t.Fatalf("pulse_import not in the catalog")
	return ImportOut{}, nil
}

// readCohort returns the cohort's schema and every row, cell-stringified.
func readCohort(t *testing.T, p *pulse.Pulse, path string) (*encoding.Schema, [][]string) {
	t.Helper()
	c, err := p.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatalf("Reader: %v", err)
	}
	defer r.Close()
	var rows [][]string
	for i := int64(0); i < r.Len(); i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			t.Fatalf("RecordAt(%d): %v", i, err)
		}
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = fmt.Sprint(v)
		}
		rows = append(rows, cells)
	}
	return r.Schema(), rows
}

func fieldNames(s *encoding.Schema) []string {
	out := make([]string, len(s.Fields))
	for i := range s.Fields {
		out[i] = s.Fields[i].Name
	}
	return out
}

// TestPulseImport_SchemaMatchesByName: a schema written in a different
// order than the source reads each field from the column of its NAME,
// and the array order is the cohort's field order.
func TestPulseImport_SchemaMatchesByName(t *testing.T) {
	t.Parallel()

	p, _ := newSchemaImportPulse(t)
	out, err := invokeImportOut(t, p, `{"source":"ab.csv","schema":[
		{"name":"b","type":"u16"},
		{"name":"amount","type":"f64","nullable":true,"description":"Order amount in dollars"},
		{"name":"a","type":"u16"}]}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	schema, rows := readCohort(t, p, out.Path)
	if got := fmt.Sprint(fieldNames(schema)); got != "[b amount a]" {
		t.Fatalf("fields = %s, want [b amount a]", got)
	}
	if !schema.Fields[1].Nullable || schema.Fields[1].Description != "Order amount in dollars" {
		t.Errorf("amount = %+v, want nullable with its description", schema.Fields[1])
	}
	if schema.Fields[0].Type != encoding.FieldTypeU16 {
		t.Errorf("b type = %v, want u16", schema.Fields[0].Type)
	}
	want := [][]string{{"100", "10.5", "1"}, {"200", "20.25", "2"}}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("rows = %v, want %v (by name, not position)", rows, want)
	}
}

// TestPulseImport_SchemaRefusalsKeepTheirCode: every by-name, type-name
// and float-width refusal reaches the agent as the error's OWN code —
// never a placeholder — and no cohort is written.
func TestPulseImport_SchemaRefusalsKeepTheirCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args string
		want perr.Code
	}{
		{"field missing", `{"source":"ab.csv","handle":"x","schema":[{"name":"a","type":"u16"},{"name":"b","type":"u16"},{"name":"amount","type":"f64"},{"name":"nope","type":"u8"}]}`, perr.PULSE_IMPORT_SCHEMA_FIELD_MISSING},
		{"column unmapped", `{"source":"ab.csv","handle":"x","schema":[{"name":"a","type":"u16"},{"name":"b","type":"u16"}]}`, perr.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED},
		{"duplicate field", `{"source":"ab.csv","handle":"x","schema":[{"name":"a","type":"u16"},{"name":"a","type":"u16"},{"name":"b","type":"u16"},{"name":"amount","type":"f64"}]}`, perr.PULSE_IMPORT_SCHEMA_DUPLICATE},
		{"duplicate header", `{"source":"dupe.csv","handle":"x","schema":[{"name":"a","type":"u8"},{"name":"b","type":"u8"}]}`, perr.PULSE_IMPORT_SCHEMA_DUPLICATE},
		{"late key", `{"source":"late.ndjson","handle":"x","schema":[{"name":"a","type":"u16"},{"name":"b","type":"u16"}]}`, perr.PULSE_IMPORT_SCHEMA_KEY_LATE},
		{"type unknown", `{"source":"ab.csv","handle":"x","schema":[{"name":"a","type":"u16"},{"name":"b","type":"u16"},{"name":"amount","type":"float"}]}`, perr.PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN},
		{"float invalid", `{"source":"ab.csv","handle":"x","float":"f16"}`, perr.PULSE_IMPORT_FLOAT_WIDTH_INVALID},
		{"unknown entry key", `{"source":"ab.csv","handle":"x","schema":[{"name":"a","type":"u16","nullabel":true},{"name":"b","type":"u16"},{"name":"amount","type":"f64"}]}`, perr.PULSE_IMPORT_SCHEMA_INVALID},
		{"not an array", `{"source":"ab.csv","handle":"x","schema":{"name":"a","type":"u16"}}`, perr.PULSE_IMPORT_SCHEMA_INVALID},
		{"empty array", `{"source":"ab.csv","handle":"x","schema":[]}`, perr.PULSE_IMPORT_SCHEMA_INVALID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, afs := newSchemaImportPulse(t)
			_, err := invokeImportOut(t, p, tc.args)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if ok, _ := afero.Exists(afs, "imports/x.pulse"); ok {
				t.Errorf("a refused import still wrote a cohort")
			}
		})
	}
}

// TestPulseImport_SchemaRefusedOnPulsePassthrough: a .pulse source is
// used as-is, so a schema for it would be silently ignored — refused.
func TestPulseImport_SchemaRefusedOnPulsePassthrough(t *testing.T) {
	t.Parallel()

	p, _ := newSchemaImportPulse(t)
	out, err := invokeImportOut(t, p, `{"source":"ab.csv","handle":"src"}`)
	if err != nil {
		t.Fatalf("seed import: %v", err)
	}
	_, err = invokeImportOut(t, p, `{"source":"`+out.Path+`","schema":[{"name":"a","type":"u16"}]}`)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_SCHEMA_INVALID || ce.Details["reason"] != "passthrough" {
		t.Fatalf("err = %v, want PULSE_IMPORT_SCHEMA_INVALID reason passthrough", err)
	}
}

// TestPulseImport_IgnoreExtraDropsUnnamedColumns: ignore_extra turns the
// unmapped-column and late-key refusals into drops.
func TestPulseImport_IgnoreExtraDropsUnnamedColumns(t *testing.T) {
	t.Parallel()

	p, _ := newSchemaImportPulse(t)
	out, err := invokeImportOut(t, p, `{"source":"ab.csv","ignore_extra":true,"schema":[{"name":"b","type":"u16"},{"name":"a","type":"u16"}]}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	schema, rows := readCohort(t, p, out.Path)
	if got := fmt.Sprint(fieldNames(schema), rows); got != "[b a] [[100 1] [200 2]]" {
		t.Errorf("cohort = %s, want [b a] [[100 1] [200 2]]", got)
	}

	out, err = invokeImportOut(t, p, `{"source":"late.ndjson","ignore_extra":true,"schema":[{"name":"a","type":"u16"},{"name":"b","type":"u16"}]}`)
	if err != nil {
		t.Fatalf("invoke late.ndjson: %v", err)
	}
	if out.RowsImported != 2 {
		t.Errorf("late.ndjson RowsImported = %d, want 2", out.RowsImported)
	}
}

// TestPulseImport_FloatWidth: inferred floats are f64 by default and f32
// only on request.
func TestPulseImport_FloatWidth(t *testing.T) {
	t.Parallel()

	p, _ := newSchemaImportPulse(t)
	for _, tc := range []struct {
		args string
		want encoding.FieldType
	}{
		{`{"source":"floats.csv","handle":"d"}`, encoding.FieldTypeF64},
		{`{"source":"floats.csv","handle":"w","float":"f64"}`, encoding.FieldTypeF64},
		{`{"source":"floats.csv","handle":"n","float":"f32"}`, encoding.FieldTypeF32},
	} {
		out, err := invokeImportOut(t, p, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.args, err)
		}
		schema, _ := readCohort(t, p, out.Path)
		if got := schema.Fields[0].Type; got != tc.want {
			t.Errorf("%s: x type = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestPulseImport_NoSchemaUnchanged: an absent or null schema infers
// exactly as before the slot existed.
func TestPulseImport_NoSchemaUnchanged(t *testing.T) {
	t.Parallel()

	p, _ := newSchemaImportPulse(t)
	base, err := invokeImportOut(t, p, `{"source":"ab.csv","handle":"base"}`)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	null, err := invokeImportOut(t, p, `{"source":"ab.csv","handle":"null","schema":null,"ignore_extra":true}`)
	if err != nil {
		t.Fatalf("null schema: %v", err)
	}
	bs, brows := readCohort(t, p, base.Path)
	ns, nrows := readCohort(t, p, null.Path)
	if fmt.Sprint(bs.Fields) != fmt.Sprint(ns.Fields) || fmt.Sprint(brows) != fmt.Sprint(nrows) {
		t.Errorf("null-schema import differs from inference:\n base %v %v\n null %v %v", bs.Fields, brows, ns.Fields, nrows)
	}
	if got := fmt.Sprint(fieldNames(bs)); got != "[a b amount]" {
		t.Errorf("inferred fields = %s, want source order [a b amount]", got)
	}
}
