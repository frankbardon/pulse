package synth

import (
	"bytes"
	"io"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// SyntheticFieldName is the provenance column every AugmentFromProfile
// output cohort carries: false for rows copied from the source cohort,
// true for newly generated rows.
const SyntheticFieldName = "_synthetic"

// syntheticFieldDescription documents the tag column inline in the
// output schema so a downstream `pulse inspect` reader sees its meaning
// without consulting external docs.
const syntheticFieldDescription = "Row provenance: false for rows copied from the source cohort, true for newly generated rows."

// AugmentFromProfile is the tagged top-up entry point behind
// `synth from-profile`. It never opens sourcePath for write: it reads the
// source cohort, generates spec.RowCount brand-new rows, and writes a
// single combined cohort to output containing every source row (tagged
// _synthetic=false) followed by every generated row (tagged
// _synthetic=true). output must be a distinct path from sourcePath —
// synthesis never mutates the source in place.
//
// spec.RowCount is an explicit count of NEW rows to generate; it is never
// a "top up to N total" target. A 200-row source with spec.RowCount=500
// produces a 700-row output.
func AugmentFromProfile(fs afero.Fs, spec *Spec, sourcePath, output string, opts Options) (*Result, error) {
	if fs == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: fs is required")
	}
	if sourcePath == "" {
		return nil, errors.NewCodedError(errors.PULSE_SYNTH_SOURCE_REQUIRED,
			"synth from-profile requires a source cohort path")
	}
	if output == "" {
		return nil, errors.NewCodedError(errors.PULSE_SYNTH_OUTPUT_REQUIRED,
			"synth from-profile requires an output path")
	}
	if samePath(sourcePath, output) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_OUTPUT_COLLISION,
			"output path must be different from the source cohort path — the source is never opened for write",
			map[string]any{"path": output})
	}
	if err := validateSpec(spec); err != nil {
		return nil, err
	}

	data, err := afero.ReadFile(fs, sourcePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "reading source cohort")
	}
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}
	srcSchema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}
	if srcSchema.Field(SyntheticFieldName) != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_ALREADY_TAGGED,
			"source cohort already has a _synthetic field", map[string]any{"path": sourcePath})
	}

	genSchema, wfs, err := buildSchema(spec)
	if err != nil {
		return nil, err
	}

	mergedSchema, err := buildMergedSchema(srcSchema, genSchema)
	if err != nil {
		return nil, err
	}

	var recordsBuf bytes.Buffer

	// Real rows first, byte order preserved, each tagged false.
	if _, err := reencodeRecords(r, srcSchema, mergedSchema, false, &recordsBuf); err != nil {
		return nil, err
	}

	// Generate spec.RowCount brand-new rows against the original
	// (untagged) genSchema, then re-encode them into the merged schema
	// tagged true. spec.RowCount is never adjusted by the source's row
	// count — it is always an explicit count of new rows.
	rng := newRng(opts.Seed)
	var genBuf bytes.Buffer
	rowsGenerated, rowsRejected, warnings, err := generate(spec, genSchema, wfs, &genBuf, rng)
	if err != nil {
		return nil, err
	}
	if _, err := reencodeRecords(bytes.NewReader(genBuf.Bytes()), genSchema, mergedSchema, true, &recordsBuf); err != nil {
		return nil, err
	}

	var fileBuf bytes.Buffer
	if err := encoding.WriteHeader(&fileBuf); err != nil {
		return nil, err
	}
	if err := encoding.WriteSchema(&fileBuf, mergedSchema); err != nil {
		return nil, err
	}
	if _, err := fileBuf.Write(recordsBuf.Bytes()); err != nil {
		return nil, err
	}

	if err := afero.WriteFile(fs, output, fileBuf.Bytes(), 0644); err != nil {
		return nil, errors.WrapCodedError(err, errors.CLI_OUTPUT, "writing synth output")
	}

	return &Result{
		RowsGenerated: rowsGenerated,
		RowsRejected:  rowsRejected,
		OutputPath:    output,
		Warnings:      warnings,
	}, nil
}

// samePath reports whether a and b name the same file once cleaned —
// used only to refuse an output path that collides with the source, not
// to resolve symlinks or compare across filesystems.
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// buildMergedSchema lays out the combined output schema: every field of
// src, in order, followed by the appended _synthetic packed_bool field.
// gen (the schema buildSchema produced from the profile-derived spec)
// must line up with src field-for-field (name, type, and — for
// decimal128 — precision/scale); anything else means the profile was not
// captured from this exact source, or the source's shape has since
// changed, and PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH is returned rather
// than silently misinterpreting one side's bytes as the other's.
//
// Each field gets a fresh, empty Dictionary (categorical_*/set_*) —
// never src's or gen's own — because the merged output is a single
// unified cohort and both partitions' rows must resolve to IDs in one
// shared dictionary; reencodeRecords populates it as rows are copied in.
// A field is Nullable in the merged schema if either side declared it
// nullable.
func buildMergedSchema(src, gen *encoding.Schema) (*encoding.Schema, error) {
	if len(src.Fields) != len(gen.Fields) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH,
			"profile-derived spec field count does not match source cohort schema",
			map[string]any{"source_fields": len(src.Fields), "profile_fields": len(gen.Fields)})
	}

	fields := make([]encoding.Field, len(src.Fields)+1)
	byteOffset, bitCursor := 0, 8
	for i := range src.Fields {
		sf, gf := &src.Fields[i], &gen.Fields[i]
		if sf.Name != gf.Name || sf.Type != gf.Type {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH,
				"profile-derived field does not match source cohort field",
				map[string]any{
					"index":       i,
					"source_name": sf.Name, "source_type": sf.Type.String(),
					"profile_name": gf.Name, "profile_type": gf.Type.String(),
				})
		}
		if sf.Type.IsDecimal() && (sf.Precision != gf.Precision || sf.Scale != gf.Scale) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH,
				"profile-derived decimal field precision/scale does not match source cohort field",
				map[string]any{"name": sf.Name,
					"source_precision": sf.Precision, "source_scale": sf.Scale,
					"profile_precision": gf.Precision, "profile_scale": gf.Scale})
		}

		f := encoding.Field{
			Name:         sf.Name,
			Type:         sf.Type,
			Nullable:     sf.Nullable || gf.Nullable,
			Description:  sf.Description,
			CsvColumnIdx: i,
			Precision:    sf.Precision,
			Scale:        sf.Scale,
		}
		if sf.Type.HasDictionary() {
			f.Dictionary = encoding.NewDictionary()
		}
		f.ByteOffset, f.BitPosition = nextFieldLayout(sf.Type, &byteOffset, &bitCursor)
		fields[i] = f
	}

	tag := encoding.Field{
		Name:         SyntheticFieldName,
		Type:         encoding.FieldTypePackedBool,
		Description:  syntheticFieldDescription,
		CsvColumnIdx: len(src.Fields),
	}
	tag.ByteOffset, tag.BitPosition = nextFieldLayout(tag.Type, &byteOffset, &bitCursor)
	fields[len(src.Fields)] = tag

	return &encoding.Schema{Fields: fields}, nil
}

// reencodeRecords decodes every record from from's on-wire shape and
// re-encodes it into out under to's schema, appending the _synthetic tag
// (to's final field) with the given constant value. from's fields must
// be to's leading len(from.Fields) fields, in the same order — exactly
// what buildMergedSchema constructs. Returns the number of records
// copied.
func reencodeRecords(r io.Reader, from, to *encoding.Schema, synthetic bool, out *bytes.Buffer) (int, error) {
	rr := encoding.NewRecordReader(r, from)
	values := make(map[string]float64, len(from.Fields))
	nulls := make(map[string]bool, len(from.Fields))
	wide := make(map[string]any, len(from.Fields))
	bitmapSize := to.BitmapByteSize()
	tagField := &to.Fields[len(from.Fields)]

	count := 0
	for {
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}

		var bitmap []byte
		if bitmapSize > 0 {
			bitmap = make([]byte, bitmapSize)
		}
		for i := range from.Fields {
			sf, tf := &from.Fields[i], &to.Fields[i]
			val, isNull := decodedFieldValue(sf, values, nulls, wide)
			if err := writeFieldValueForField(out, tf, val, isNull); err != nil {
				return count, err
			}
			if tf.Nullable && isNull {
				encoding.BitmapSetNull(bitmap, i)
			}
		}
		if err := writeFieldValueForField(out, tagField, synthetic, false); err != nil {
			return count, err
		}
		if bitmapSize > 0 {
			if err := encoding.WriteBitmap(out, bitmap); err != nil {
				return count, err
			}
		}
		count++
	}
	return count, nil
}

// decodedFieldValue converts one field's decoded (values/nulls/wide)
// entry back into the same value shape writeFieldValueForField's sampler
// path already accepts (float64 for numeric/date, string for
// categorical, encoding.Decimal128 for decimal128) — so re-encoding a
// real row reuses exactly the same encode switch as generation.
func decodedFieldValue(f *encoding.Field, values map[string]float64, nulls map[string]bool, wide map[string]any) (val any, isNull bool) {
	if nulls[f.Name] {
		return nil, true
	}
	switch {
	case f.Type.IsCategorical():
		name := ""
		if f.Dictionary != nil {
			name = f.Dictionary.Resolve(uint32(values[f.Name]))
		}
		return name, false
	case f.Type == encoding.FieldTypeDecimal128:
		if d, ok := wide[f.Name].(encoding.Decimal128); ok {
			return d, false
		}
		return encoding.ZeroDecimal128(), false
	default:
		return values[f.Name], false
	}
}
