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

// LookupKey is one ordered key column/value pair within a composite
// point lookup — see LookupRequest.Keys. A composite key is an ORDERED
// TUPLE: [{region, "east"}, {date, "2024-01-01"}] and
// [{date, "2024-01-01"}, {region, "east"}] name the same two logical
// values but resolve against two different sidecar indices (built via
// two different Service.BuildIndex keyFields orderings) and produce two
// different on-wire composite keys — key column order is significant
// end to end, from build through probe.
type LookupKey struct {
	// Field is the schema field name.
	Field string `json:"field"`

	// Value is the literal key value to look up, as text — the same
	// string-literal convention Filterer.Values uses for include/
	// exclude filters.
	Value string `json:"value"`
}

// LookupRequest is the input to Pulse.Lookup — a point lookup against a
// cohort's sidecar index (built via Service.BuildIndex / the eventual
// `pulse index build` CLI leaf), keyed on an ordered tuple of one or
// more columns.
//
// Two ways to supply the key, in precedence order:
//
//  1. Keys — the ordered set of key column/value pairs (LookupKey),
//     for a composite (multi-column) key or a single-key lookup spelled
//     out as a 1-element slice. Order matters: it must match the
//     sidecar index's key-spec order that Service.BuildIndex wrote.
//  2. Field/Value — the single-key convenience path (E1-S4's original
//     shape, kept for ergonomics and wire back-compatibility). Used
//     only when Keys is empty; ignored when Keys is non-empty.
//
// At least one of Keys or Field must resolve to a non-empty ordered key
// tuple, or service.Lookup rejects the request with SERVICE_VALIDATION.
// Supplying a key-component count that does not match the resolved
// sidecar index's key-spec is a PROCESSING_CONFIG error.
type LookupRequest struct {
	// Cohort selects the .pulse file. Same shape as Request.Cohort —
	// slot 1, matching every other request type's convention.
	Cohort *Cohort `json:"cohort,omitempty"`

	// Field is the schema field name the sidecar index was built
	// against — the single-key convenience path. Ignored when Keys is
	// non-empty. Required when Keys is empty.
	Field string `json:"field,omitempty"`

	// Value is the literal key value to look up, as text, paired with
	// Field — the single-key convenience path. Ignored when Keys is
	// non-empty. Resolved to on-wire bytes via
	// processing.ResolveLookupKeyBytes (or ResolveCompositeLookupKeyBytes
	// for the Keys path) before probing the sidecar index's hash
	// buckets. Required when Keys is empty.
	Value string `json:"value,omitempty"`

	// Keys is the ordered set of key column/value pairs for a
	// composite (multi-column) point lookup — see LookupKey. When
	// non-empty, takes precedence over Field/Value. Order matters — it
	// must match the sidecar index's key-spec order that
	// Service.BuildIndex wrote to build the composite key.
	Keys []LookupKey `json:"keys,omitempty"`

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

// KeyComponents resolves LookupRequest's key into the ordered tuple of
// LookupKey components service.Lookup probes with: Keys verbatim when
// non-empty, else the single-key convenience path — a 1-element slice
// built from Field/Value (only when Field is non-empty). Returns nil
// when neither slot carries a usable key, which service.Lookup treats
// as a SERVICE_VALIDATION error.
func (r *LookupRequest) KeyComponents() []LookupKey {
	if r == nil {
		return nil
	}
	if len(r.Keys) > 0 {
		return r.Keys
	}
	if r.Field == "" {
		return nil
	}
	return []LookupKey{{Field: r.Field, Value: r.Value}}
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
