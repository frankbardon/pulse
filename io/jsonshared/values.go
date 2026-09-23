// Package jsonshared holds value coercion helpers shared by the ndjson and jsonarray packages.
package jsonshared

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SetArrayDelimiter is the delimiter ValueToString uses when joining
// the elements of a JSON array cell into a string. Mirrors the default
// delimiter used by the import-side set inference probe.
const SetArrayDelimiter = "|"

// ValueToString converts a JSON value to its string representation for the tabular interface.
// Arrays of scalars are pipe-joined so downstream set-type inference
// can detect them by the standard delimited-cell heuristic; arrays
// containing objects fall back to the verbose Go-format representation
// so the inference path will not misclassify them as set fields.
func ValueToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case json.Number:
		return val.String()
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []any:
		if len(val) == 0 {
			// A present array with no elements is an EMPTY SELECTION,
			// not a null — the JSON-native spelling of the state
			// io.EmptySetCell carries in flat text. Joining zero parts
			// would yield "", which the shared import path reads as a
			// null token before any dictionary is consulted, so a
			// respondent who ticked none of the boxes would come back
			// indistinguishable from one who never saw the question.
			// The bare delimiter splits to zero tokens: mask 0.
			return SetArrayDelimiter
		}
		parts := make([]string, 0, len(val))
		for _, el := range val {
			switch el.(type) {
			case map[string]any, []any:
				return fmt.Sprintf("%v", v)
			}
			parts = append(parts, ValueToString(el))
		}
		return strings.Join(parts, SetArrayDelimiter)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// CoerceValue attempts to convert string values to native JSON types
// (number, boolean, null) for cleaner JSON output.
//
// The empty string becomes JSON null. That is correct for the TEXT
// path — io.ConvertJob copies source cells, where "" is the null token
// io/import.go's isNullToken recognises — and wrong for the export
// path, where a cohort's categorical dictionary can hold "" as a
// genuine value and the null cell has its own spelling (an untyped
// nil). CoerceValueExplicitNull is that second convention; a writer
// picks between them from io.NullAwareWriter, never from the value.
func CoerceValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	if s == "" {
		return nil
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	switch s {
	case "true":
		return true
	case "false":
		return false
	}

	return s
}

// CoerceValueExplicitNull is CoerceValue for callers that mark the null
// cell themselves: an untyped nil is the ONLY null, and "" is an empty
// JSON string rather than JSON null.
//
// io.ExportJob.Run is that caller — it spells a bitmap-null cell nil
// and calls io.NullAwareWriter.SetExplicitNulls, because a `.pulse`
// categorical distinguishes "answered with a blank" from "did not
// answer" and JSON can carry both. Every other coercion (number,
// boolean, passthrough) is identical to CoerceValue, so a non-empty
// cell's JSON type does not move.
func CoerceValueExplicitNull(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok && s == "" {
		return s
	}
	return CoerceValue(v)
}
