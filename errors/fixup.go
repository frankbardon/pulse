package errors

// FixupAction enumerates the mechanical repair categories a caller can
// apply to an offending request. Action values are stable wire constants:
// downstream consumers (predict-suggestions, unified-ask-tool, manifest)
// dispatch on them.
type FixupAction string

const (
	// FixupReplaceField swaps the offending field reference for a
	// different field of the right type. The replacement candidate is
	// typically named in the Hint or surfaced by the caller's schema
	// walk; this package only declares the action class.
	FixupReplaceField FixupAction = "REPLACE_FIELD"

	// FixupReplaceOperator swaps the offending operator (aggregator,
	// attribute, filterer, grouper, window, feature, test) for a
	// compatible one. The Hint identifies one or more valid swaps.
	FixupReplaceOperator FixupAction = "REPLACE_OPERATOR"

	// FixupRemoveParam removes an incompatible request parameter (a
	// frame on a non-frame window, a success value on the wrong test,
	// etc.) so the request validates.
	FixupRemoveParam FixupAction = "REMOVE_PARAM"

	// FixupSetDefault supplies a missing required parameter with the
	// canonical default (alpha=0.05, etc.).
	FixupSetDefault FixupAction = "SET_DEFAULT"

	// FixupRequiresReschema signals that the offending request cannot
	// be repaired by editing the request alone — the .pulse cohort
	// itself needs different schema, filtering, or row counts.
	FixupRequiresReschema FixupAction = "REQUIRES_RESCHEMA"
)

// Fixup is a machine-actionable hint for repairing a request that
// triggered an error. Templates live in the per-code metadata table and
// are materialised via Code.Fixup(req) before being shipped to a caller.
type Fixup struct {
	// Action is the repair category.
	Action FixupAction `json:"action"`

	// Path is the request path the fix applies to. First-cut templates
	// use either an empty path (the caller resolves it) or a static
	// wildcard path like ["Aggregations", "*", "Type"] that describes
	// where the fix conceptually lives. Concrete index resolution is
	// predict-suggestions' job.
	Path []string `json:"path,omitempty"`

	// Hint is the prose remediation. Single sentence, imperative,
	// actionable — describes a concrete next move.
	Hint string `json:"hint"`

	// Examples lists ranked example fixes (e.g. specific operator
	// names, replacement values). Populated sparingly; most codes
	// carry only a Hint.
	Examples []any `json:"examples,omitempty"`
}

// Metadata bundles per-code documentation that lives alongside the flat
// Code constant. Today it carries the canonical human Message and a
// Fixups template list. Future fields (severity, retryable, etc.) attach
// here.
type Metadata struct {
	// Message is the canonical human-readable description used when no
	// caller-supplied message is provided. Mirrors the curated text in
	// descriptor.errorMetaTable but is owned by the errors package.
	Message string

	// Fixups is the template list. Empty when FixupNotApplicable is
	// true; otherwise carries at least one Fixup.
	Fixups []Fixup

	// FixupNotApplicable is true for codes that have no mechanical
	// fix: internal-invariant violations, corrupted files, and similar
	// "bug, not user error" signals. The flag is the honest signal —
	// callers should not invent suggestions for these codes.
	FixupNotApplicable bool
}

// Metadata returns the per-code metadata entry. The second return value
// is false if the code has no entry — TestCodesHaveFixups enforces that
// every entry in allCodes has metadata, so production code can ignore
// the bool except in introspection paths.
func MetadataFor(c Code) (Metadata, bool) {
	m, ok := codeMetadata[c]
	return m, ok
}

// Fixup returns the materialised fixup templates for this code. The
// req argument is reserved for future path-resolution work; today the
// templates are returned as-is so callers see the abstract paths from
// the metadata table. Returns nil when the code is tagged
// FixupNotApplicable.
//
// The signature accepts any so the errors package can avoid a circular
// dependency on types/. Predict-suggestions, which lives above both
// packages, will introduce a typed wrapper that walks the request to
// resolve concrete indices.
func (c Code) Fixup(req any) []Fixup {
	m, ok := codeMetadata[c]
	if !ok {
		return nil
	}
	if m.FixupNotApplicable {
		return nil
	}
	out := make([]Fixup, len(m.Fixups))
	copy(out, m.Fixups)
	return out
}
