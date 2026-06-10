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
