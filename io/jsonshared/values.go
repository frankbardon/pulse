// Package jsonshared holds value coercion helpers shared by the ndjson and jsonarray packages.
package jsonshared

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ValueToString converts a JSON value to its string representation for the tabular interface.
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
