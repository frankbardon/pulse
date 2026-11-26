package io

import (
	"strings"

	"github.com/frankbardon/pulse/encoding"
)

// Source-declared column pins (DeclaredTypeReader).
//
// Arrow and Parquet declare some columns' widths — unsigned integers,
// floats, booleans, dates, timestamps, decimal128 — and leave the rest
// (signed integers, strings, dictionaries, lists) to the data. A pinned
// column skips the type heuristics in InferSchemaWithOptions and is never
// widenable (widenableFields), so a value it cannot hold is an ordinary
// per-row PULSE_IMPORT_ROW_ERROR. Unlike a ColumnTypeOverrides column it
// is not forced: the row pass never turns a pin's failure into the fatal
// PULSE_IMPORT_OVERRIDE_INVALID. Nullability, dictionaries and every
// unpinned column stay with inference, byte-for-byte as before.
//
// Pins apply only to an inference-originated schema (Schema == nil, or a
// KeepPulseAt re-import's InferredSchema), and a user override beats a pin
// for its column.

// declaredPins returns src's pinned columns, or nil when src declares
// nothing. A column_type_overrides entry still wins for its column:
// InferSchemaWithOptions consults the override before the pin, and an
// overridden column is never widenable either way. An error reading the
// source's declaration fails the import: the row pass would hit the same
// fault.
func declaredPins(src Reader) (map[string]encoding.Field, error) {
	dr, ok := src.(DeclaredTypeReader)
	if !ok {
		return nil, nil
	}
	return dr.DeclaredColumnTypes()
}

// sampleHasNull reports whether any sampled cell is a null token — the
// nullability a pinned column takes, exactly as inference derives it.
func sampleHasNull(values []string) bool {
	for _, v := range values {
		if isNullToken(strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// declaredPins resolves the job's pins: none unless the schema is
// inference-originated, read from the unfolded source otherwise.
func (j *ImportJob) declaredPins(inferred bool) (map[string]encoding.Field, error) {
	if !inferred {
		return nil, nil
	}
	return declaredPins(j.rawSource())
}
