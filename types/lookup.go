package types

// LookupMultiplicity names how a LookupRequest should behave when the
// sidecar index's matched bucket entry carries more than one row-id
// (a duplicate key value in the source cohort). The field is defined
// now — and carried on the wire — so the eventual multiplicity engine
// (E2-S2) is purely additive: no LookupRequest shape churn, no wire
// break, just a handler that finally branches on this value.
//
// v1 (E1-S4) does NOT branch on Multiplicity at all: service.Lookup
// always takes IndexEntry.RowIDs[0], regardless of the declared mode
// or how many row-ids the matched entry actually carries. Treat this
// field as reserved/stubbed until E2-S2 ships.
type LookupMultiplicity string

const (
	// LookupMultiplicityAssertUnique is the conceptual default (see
	// LookupRequest.Multiplicity doc) — the eventual E2-S2 behavior
	// will fail the lookup when a matched key resolves to more than
	// one row-id. Not enforced in v1.
	LookupMultiplicityAssertUnique LookupMultiplicity = "assert_unique"

	// LookupMultiplicityFirst will, once E2-S2 lands, explicitly opt
	// into "take the first row-id" without the assert-unique failure
	// mode. This is v1's actual (unconditional) runtime behavior for
	// every Multiplicity value, including the zero value.
	LookupMultiplicityFirst LookupMultiplicity = "first"

	// LookupMultiplicityAll will, once E2-S2 lands, return every
	// matched row-id's record in LookupResult.Rows instead of a single
	// entry.
	LookupMultiplicityAll LookupMultiplicity = "all"
)

// LookupRequest is the input to Pulse.Lookup — a single-key point
// lookup against a cohort's sidecar index (built via
// Service.BuildIndex / the eventual `pulse index build` CLI leaf).
//
// v1 (E1-S4) scope: exactly one key field/value pair (Field/Value),
// resolved against a single-column sidecar index built for Field.
// Composite (multi-column) key lookups are a later story — Field/Value
// stay singular here rather than becoming slices so that story's shape
// change is additive (new slot) rather than a breaking rename.
type LookupRequest struct {
	// Cohort selects the .pulse file. Same shape as Request.Cohort —
	// slot 1, matching every other request type's convention.
	Cohort *Cohort `json:"cohort,omitempty"`

	// Field is the schema field name the sidecar index was built
	// against (Service.BuildIndex's keyFields[0]). Required.
	Field string `json:"field"`

	// Value is the literal key value to look up, as text — the same
	// string-literal convention Filterer.Values uses for include/
	// exclude filters. Resolved to on-wire bytes via
	// processing.ResolveLookupKeyBytes before probing the sidecar
	// index's hash buckets. Required.
	Value string `json:"value"`

	// ReturnColumns lists the field names to project into
	// LookupResult.Rows. Empty means "every schema field" (the full
	// decoded record), matching Sample's no-projection default.
	ReturnColumns []string `json:"return_columns,omitempty"`

	// Multiplicity is reserved for E2-S2's duplicate-key handling
	// modes (see LookupMultiplicity). Carried on the wire today but
	// not honored by service.Lookup — every lookup behaves as
	// LookupMultiplicityFirst regardless of this value.
	Multiplicity LookupMultiplicity `json:"multiplicity,omitempty"`
}

// LookupResult is the response shape returned by Pulse.Lookup.
type LookupResult struct {
	// Rows carries the matched row(s), each resolved via
	// Record.AllValues() and projected down to LookupRequest.ReturnColumns
	// (or every schema field when ReturnColumns is empty). A slice
	// (rather than a single map) so E2-S2's LookupMultiplicityAll mode
	// is additive — v1 always populates exactly one entry on a hit.
	Rows []map[string]any `json:"rows"`

	// Warnings carries per-lookup diagnostics. Empty in v1 — reserved
	// for the eventual multiplicity/staleness diagnostics E2 adds.
	Warnings []string `json:"warnings,omitempty"`
}
