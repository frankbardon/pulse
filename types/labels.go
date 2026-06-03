package types

// LabelMode selects how a LabelBinding affects output rendering.
//
// Replace: the binding's field is rewritten to the label string in
// every output surface (row values, aggregation map keys, facet
// counts, exported cells). When two source values map to the same
// label the output disambiguates with the source value appended
// in parentheses (e.g. "United States (US)") and emits a
// PULSE_LABEL_COLLISION warning.
//
// Augment: the binding's field is left untouched; a sibling string
// field named "<field>_label" is added to the output schema and
// populated with the label. Collision with an existing schema field
// name surfaces PULSE_LABEL_FIELD_COLLISION at validation time.
//
// Labels are output-only: filter expressions, formula attributes,
// sort keys, and group keys continue to operate on the raw
// categorical value. Labels never influence what records pass
// through the pipeline.
type LabelMode string

const (
	LabelModeReplace LabelMode = "replace"
	LabelModeAugment LabelMode = "augment"
)

// LabelBinding requests that an output surface render the named
// categorical field through the named label table.
//
// Field must reference a categorical_u8 / categorical_u16 /
// categorical_u32 schema field. Table must reference a label table
// registered on pulse.Options.Extensions.LabelTables. Mode picks
// between "replace" (rewrite the field's value) and "augment" (emit
// a sibling "<field>_label" field).
//
// Duplicate bindings for the same field within a single request
// produce PULSE_LABEL_DUPLICATE_BINDING.
type LabelBinding struct {
	Field string    `json:"field"`
	Table string    `json:"table"`
	Mode  LabelMode `json:"mode,omitempty"`
}

// LabelModeOrDefault returns the binding's mode falling back to
// LabelModeReplace when empty. Callers may use this when an empty
// JSON value should mean "the simplest behaviour."
func (b *LabelBinding) LabelModeOrDefault() LabelMode {
	if b == nil || b.Mode == "" {
		return LabelModeReplace
	}
	return b.Mode
}

// AugmentFieldName is the fixed sibling-column name used when Mode
// is LabelModeAugment. The suffix is not overridable; collisions
// surface as PULSE_LABEL_FIELD_COLLISION at validation time.
func (b *LabelBinding) AugmentFieldName() string {
	if b == nil {
		return ""
	}
	return b.Field + "_label"
}

// SampleRequest is the input shape for pulse.SampleWithRequest. It
// carries the cohort path, the row cap, and the per-request label
// bindings that the no-struct Sample entry point cannot accept. The
// shape is intentionally tiny — the legacy Sample(ctx, path, n)
// signature stays for back-compat and is implemented as a thin
// wrapper that builds a SampleRequest with no labels.
type SampleRequest struct {
	// Cohort selects the .pulse file. Same shape as Request.Cohort.
	Cohort *Cohort `json:"cohort,omitempty"`
	// N is the maximum row count returned. Zero or negative returns
	// no rows.
	N int `json:"n"`
	// Labels rewrites or augments categorical row values using
	// embedder-registered label tables. See LabelBinding.
	Labels []*LabelBinding `json:"labels,omitempty"`
}
