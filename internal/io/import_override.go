package io

import (
	stderrors "errors"
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// ColumnTypeOverrides contract (E4-S13).
//
// An override is applied EXACTLY or the import is refused with
// PULSE_IMPORT_OVERRIDE_INVALID. It is never narrowed (inference does
// not run for the column), never widened (import_widen.go skips it),
// never ignored, and a value it cannot hold never becomes a skipped
// row. Concretely:
//
//   - Presence in the map decides whether a column is forced, never the
//     value: FieldTypeU8 is the iota zero, so `override != 0` read an
//     override to u8 as absent.
//   - Every key must name a header column exactly (checkOverrideColumns).
//   - Every present value — the inference sample (applyTypeOverride,
//     so Predict refuses too) and every later row (rowConverter.forced)
//     — must convert at the forced type.
//   - An override needs an inferred schema to override: a job with an
//     explicit Schema or an authoritative SchemaAwareReader schema is
//     refused (refuseOverridesWithoutInference).
//
// The rule lives in this one file and is shared by every text adapter
// (csv, tsv, ndjson, jsonarray, excel, arrow, parquet all reach the
// inference pass through the same Reader contract); SPSS is the
// authoritative-schema arm.

// forcedColumn is the inference result for one overridden column.
type forcedColumn struct {
	Type      encoding.FieldType
	Nullable  bool
	Delim     string
	Precision uint8
	Scale     uint8
}

// overrideRefusal builds the PULSE_IMPORT_OVERRIDE_INVALID error for a
// present value the forced type cannot hold. row is the 1-based source
// data row.
func overrideRefusal(column string, ft encoding.FieldType, value string, row int, cause error) *errors.CodedError {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_OVERRIDE_INVALID,
		fmt.Sprintf("column_type_overrides: column %q forced to %s cannot hold value %q at row %d: %s",
			column, ft, value, row, reason),
		map[string]any{"column": column, "type": ft.String(), "value": value, "row": row, "reason": reason})
}

// isOverrideRefusal reports whether err is the fatal override refusal
// (as opposed to an ordinary per-row error that skips the row).
func isOverrideRefusal(err error) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && ce.Code == errors.PULSE_IMPORT_OVERRIDE_INVALID
}

// checkOverrideColumns refuses an override key the header does not
// carry. Names match exactly — case and surrounding whitespace
// included — because the header name IS the field name.
func checkOverrideColumns(columns []string, overrides map[string]encoding.FieldType) error {
	if len(overrides) == 0 {
		return nil
	}
	known := make(map[string]bool, len(columns))
	for _, c := range columns {
		known[c] = true
	}
	var unknown []string
	for name := range overrides {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	name := unknown[0]
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_OVERRIDE_INVALID,
		fmt.Sprintf("column_type_overrides names column %q, which the source header does not carry", name),
		map[string]any{"column": name, "type": overrides[name].String(), "columns": append([]string(nil), columns...)})
}

// refuseOverridesWithoutInference refuses ColumnTypeOverrides on a job
// whose schema is not inferred: an explicit Schema (the caller already
// declared every type) or an authoritative source schema (a source
// dictionary, not a guess — re-typing it would discard the source's
// category IDs / mask bit positions). Before, both silently ignored the
// override.
func refuseOverridesWithoutInference(overrides map[string]encoding.FieldType, explicit, authoritative bool) error {
	if len(overrides) == 0 || (!explicit && !authoritative) {
		return nil
	}
	reason := "an explicit Schema was supplied; declare the column's type there instead"
	if authoritative {
		reason = "the source carries an authoritative schema (SchemaAwareReader, e.g. SPSS); supply an explicit Schema to re-type a column"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_OVERRIDE_INVALID,
		"column_type_overrides cannot apply: "+reason,
		map[string]any{"reason": reason})
}

// applyTypeOverride bypasses inference for a column. Nullability is
// still derived from the sampled values (null tokens flip the flag).
// For set fields the delimiter probes the same priority list and falls
// back to DefaultSetDelimiter when no probe fires. A decimal128 override
// takes the maximum precision and the largest scale the sample shows.
// Every non-null sampled value is converted at the forced type; the
// first one that does not convert refuses the import.
func applyTypeOverride(colName string, values []string, override encoding.FieldType,
) (forcedColumn, []InferenceWarning, error) {
	out := forcedColumn{Type: override}
	type sampled struct {
		raw string
		row int
	}
	nonNull := make([]sampled, 0, len(values))
	for i, v := range values {
		trimmed := strings.TrimSpace(v)
		if isNullToken(trimmed) {
			out.Nullable = true
			continue
		}
		nonNull = append(nonNull, sampled{trimmed, i + 1})
	}
	if override.IsSet() {
		raws := make([]string, len(nonNull))
		for i, s := range nonNull {
			raws[i] = s.raw
		}
		out.Delim = pickSetDelimiter(raws)
		if out.Delim == "" {
			out.Delim = DefaultSetDelimiter
		}
	}
	if override == encoding.FieldTypeDecimal128 {
		out.Precision = encoding.MaxDecimalPrecision
		for _, s := range nonNull {
			_, sc, err := encoding.ParseDecimal128(s.raw)
			if err != nil {
				return out, nil, overrideRefusal(colName, override, s.raw, s.row, err)
			}
			if sc > out.Scale {
				out.Scale = sc
			}
		}
	}

	// Probe every sampled value through the row pass's own converter, on
	// a scratch dictionary, so the sample verdict is the row verdict.
	probe := encoding.Field{Name: colName, Type: override, Precision: out.Precision, Scale: out.Scale}
	var dict *encoding.Dictionary
	if override.HasDictionary() {
		dict = encoding.NewDictionary()
	}
	for _, s := range nonNull {
		if err := convertForced(s.raw, probe, dict, out.Delim); err != nil {
			return out, nil, overrideRefusal(colName, override, s.raw, s.row, err)
		}
	}
	return out, []InferenceWarning{{
		Column:  colName,
		Message: fmt.Sprintf("column %q forced to %s via column_type_overrides", colName, override),
	}}, nil
}

// convertForced converts one present cell at a forced field's type,
// discarding the value. A decimal with more fractional digits than the
// column's scale is refused rather than rounded: the override promises
// the value lands exactly.
func convertForced(raw string, f encoding.Field, dict *encoding.Dictionary, delim string) error {
	if f.Type == encoding.FieldTypeDecimal128 {
		_, sc, err := encoding.ParseDecimal128(raw)
		if err != nil {
			return err
		}
		if sc > f.Scale {
			return fmt.Errorf("value has %d fractional digits; the forced decimal128 column holds %d (the largest scale in the inference sample)", sc, f.Scale)
		}
	}
	if isWideFieldType(f.Type) {
		_, err := convertValueWide(raw, f, dict, delim)
		return err
	}
	_, err := convertValue(raw, f.Type, dict, delim)
	return err
}
