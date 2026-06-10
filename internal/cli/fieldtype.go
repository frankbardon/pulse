package cli

import "github.com/frankbardon/pulse/encoding"

// parseFieldType converts a string type name to an encoding.FieldType.
// Delegates to encoding.ParseFieldType so the canonical type-name table
// lives in one place. Unknown names fall back to f64.
func parseFieldType(name string) encoding.FieldType {
	if ft, ok := encoding.ParseFieldType(name); ok {
		return ft
	}
	return encoding.FieldTypeF64 // fallback
}
