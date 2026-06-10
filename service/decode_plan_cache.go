package service

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
)

// retainedFromFilter walks the schema once and returns the names of
// fields the keep filter accepts, in stable sorted order. Used by the
// scanning iterators to derive the retained set fed to
// Schema.BuildDecodePlan. Returns an empty slice when keep rejects
// every field — BuildDecodePlan then emits a single SkipBytes for the
// entire record stride.
//
// A nil keep is treated as "every field retained", matching the
// FieldFilter convention used throughout the encoding package.
func retainedFromFilter(schema *encoding.Schema, keep encoding.FieldFilter) []string {
	if schema == nil {
		return nil
	}
	if keep == nil {
		out := make([]string, len(schema.Fields))
		for i := range schema.Fields {
			out[i] = schema.Fields[i].Name
		}
		sort.Strings(out)
		return out
	}
	out := make([]string, 0, len(schema.Fields))
	for i := range schema.Fields {
		name := schema.Fields[i].Name
		if keep(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// joinRetainedKey turns a sorted retained-set slice into a single
// string usable as the cache key alongside schema-pointer identity.
// Uses a delimiter that cannot legally appear in a field name
// (newline) so distinct retained sets never collide.
func joinRetainedKey(retained []string) string {
	if len(retained) == 0 {
		return ""
	}
	return strings.Join(retained, "\n")
}
