package io

import "github.com/frankbardon/pulse/types"

// LabelResolver is the io-package projection of the processing-layer
// label resolver. ExportJob / ConvertJob consume the interface so io
// stays free of the processing dependency.
//
// processing.LabelResolver satisfies this interface as-is; the
// pulse-package facade builds the resolver from the user-facing
// LabelBinding slice + LabelTable registry and hands the value to the
// job via ExportJob.LabelResolver.
type LabelResolver interface {
	// Has reports whether the resolver carries any binding for the
	// named field.
	Has(field string) bool
	// Mode returns the binding mode for the named field. Empty when
	// no binding exists.
	Mode(field string) types.LabelMode
	// Apply translates a raw value through the binding for the named
	// field. See processing.LabelResolver.Apply for semantics.
	Apply(field, raw string) (out string, sibling string, ok bool)
	// FieldsWithAugment returns the field names that should emit a
	// "<field>_label" sibling column.
	FieldsWithAugment() []string
	// Warnings flushes the per-binding collision and miss summaries
	// once the job finishes.
	Warnings() []LabelWarning
}

// LabelWarning is the io-package projection of one resolver
// diagnostic record. ExportReport surfaces these for the caller; the
// CLI / MCP envelope wrapper promotes them to envelope warnings.
type LabelWarning struct {
	Code    string
	Message string
	Details map[string]any
}
