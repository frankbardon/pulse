package spss

// The derived-column REGISTRY: the closed vocabulary of column kinds this
// import synthesises, and what an export must DO with each of them.
//
// # Why a registry and not a naming convention
//
// An import emits columns the source dictionary never declared. Today two
// kinds of them — a `<var>_missing` reason sibling (E4-S2) and a
// multiple-dichotomy `set_*` convenience column (E4-S4) — and both are
// ADDITIVE, so a cohort has strictly more columns than the `.sav` had
// variables. An export folding that cohort back has to answer one question
// per column, correctly, every time: is this a variable the file declared,
// or something this reader made up?
//
// Getting it wrong is not a cosmetic fault in either direction. Treating a
// derived column as real emits a phantom SPSS variable that was never in the
// source; treating a real one as derived DROPS a respondent's data. Neither
// failure is visible in the output file — both produce a perfectly
// well-formed `.sav` that is quietly wrong.
//
// A name-pattern heuristic cannot answer it. "_missing" is a legal SPSS
// variable name suffix and a survey that genuinely declares `income_missing`
// is not hypothetical; a set column's name is whatever the set was called
// minus its '$', which pattern-matches against nothing at all. So the answer
// is recorded rather than inferred: [Payload.Derived] names every synthesised
// column explicitly, and a column absent from it is a source variable by
// construction.
//
// That is also why the slot is NOT `omitempty`. An import that derived
// nothing writes `"derived": []`, not nothing — the distinction between "this
// cohort has no derived columns" and "this document predates the registry, so
// I cannot tell" is the whole value of the registry, and an absent key
// collapses the two.
//
// # Kind is a CLOSED vocabulary
//
// [DerivedKinds] is the complete list, and [DerivedFoldFor] maps each value
// to the one thing an export does with it. Both are exhaustive by
// construction and gated by a test, so a kind added later cannot reach the
// sidecar without arriving here too.
//
// A consumer MUST reject an unrecognised kind rather than skip it. The
// vocabulary being closed is what makes that safe: a document written by a
// newer import carrying a kind this binary has never heard of describes a
// column whose fold-back is unknown, and guessing between "drop it" and
// "restore something from it" is a coin flip between the two silent failures
// above. [DerivedFoldFor] reports `false` precisely so that case is
// detectable instead of defaulted.
//
// # Every entry is self-sufficient
//
// The registry's contract is that an export never re-derives anything. It
// reads the entry and acts; it does not re-run the mapping the import ran,
// because a second derivation that disagreed with the first would produce a
// file that looks authoritative and is wrong. What "self-sufficient" costs
// differs per kind, and the difference is exactly whether the derived column
// is the ONLY home for what it holds:
//
//   - [DerivedKindNumericMissing] IS the only home. The analytic column it
//     sits beside is null at every missing position and the null bitmap is
//     one bit, so which SPSS state each row was in exists nowhere else in the
//     cohort. So the entry carries [Derived.Reasons] — the full ID <-> reason
//     <-> SPSS code <-> label dictionary — and the fold reads the sibling's
//     stored ID, finds the entry and writes back the code (or the
//     system-missing sentinel). Without it the fold would have to rebuild the
//     mapping from the missing specification plus the value labels and hope
//     it landed on the same answer this import did.
//
//   - [DerivedKindMultipleDichotomy] is NOT the only home. Every bit it shows
//     is a second reading of a constituent column that is still in the cohort
//     under its own name, so there is nothing to reconstruct: the fold drops
//     the column outright. [Derived.Sources] names the constituents in BIT
//     order and [Derived.SetName] is the key into
//     [Payload.MultipleResponseSets], where the counted value and the set
//     label live for re-emitting the record 7/7 definition.
//
// # The registry does not own the set definitions
//
// [Payload.MultipleResponseSets] is the write-back record for EVERY
// multiple-response set the file declared, whether or not it produced a
// derived column. A set that refused to derive
// (PULSE_SPSS_MR_SET_NOT_DERIVED) and every multiple-CATEGORY set have no
// registry entry at all and must still be written back. The registry answers
// "which cohort columns are synthetic", not "which sets existed" — conflating
// the two would drop a set definition from the exported file because its
// convenience column happened not to derive.

import "strconv"

// DerivedFold is what an export does with a derived column: the single
// mechanical action its [Derived.Kind] prescribes.
//
// It is a typed value rather than a string comparison at each call site so an
// unhandled kind is a compiler-visible switch arm instead of a silently
// skipped column.
type DerivedFold int

const (
	// DerivedFoldUnknown is the zero value and is never a legal fold. It is
	// what [DerivedFoldFor] returns alongside `false` for a kind this binary
	// does not recognise — a column whose fold-back is unknown, which an
	// export must refuse rather than guess at.
	DerivedFoldUnknown DerivedFold = iota

	// DerivedFoldDrop: emit no SPSS variable for this column and take
	// nothing from it. Everything it held is already carried by the columns
	// named in [Derived.Sources], which are real variables the export writes
	// normally.
	DerivedFoldDrop

	// DerivedFoldRestore: emit no SPSS variable for this column either, but
	// CONSUME it — its per-row value decides what the single variable named
	// in [Derived.Sources] writes wherever that variable is null. The mapping
	// from stored value to SPSS state is [Derived.Reasons] and is not to be
	// re-derived.
	DerivedFoldRestore
)

// String renders the fold action for diagnostics.
func (f DerivedFold) String() string {
	switch f {
	case DerivedFoldDrop:
		return "drop"
	case DerivedFoldRestore:
		return "restore"
	case DerivedFoldUnknown:
		return "unknown"
	default:
		return "DerivedFold(" + strconv.Itoa(int(f)) + ")"
	}
}

// derivedRegistry is the registry proper: the closed kind vocabulary paired
// with the fold action each value prescribes. Every value this package writes
// to [Derived.Kind] appears here, which
// TestDerivedRegistry_VocabularyIsClosed enforces against the package source.
//
// A slice and not a map, for two reasons. It is the SINGLE source of both the
// vocabulary and the fold mapping, so the two cannot drift into disagreeing
// about which kinds exist; and it is ordered, where map iteration is
// randomised and would make any diagnostic built from it un-diffable. Linear
// lookup over a vocabulary this size is not a cost worth a second structure.
var derivedRegistry = []struct {
	kind string
	fold DerivedFold
}{
	{DerivedKindNumericMissing, DerivedFoldRestore},
	{DerivedKindMultipleDichotomy, DerivedFoldDrop},
}

// DerivedKinds returns the complete, closed vocabulary of [Derived.Kind]
// values in a stable order. The returned slice is freshly allocated.
func DerivedKinds() []string {
	out := make([]string, 0, len(derivedRegistry))
	for _, e := range derivedRegistry {
		out = append(out, e.kind)
	}
	return out
}

// DerivedFoldFor reports the fold action a derived-column kind prescribes.
//
// The second result is false for a kind this binary does not recognise —
// which is a document written by a newer import, not a corrupt one. A caller
// MUST treat that as a refusal: the column's fold-back is genuinely unknown,
// and both available guesses (emit it as a variable, or drop it) are silent
// data faults.
func DerivedFoldFor(kind string) (DerivedFold, bool) {
	for _, e := range derivedRegistry {
		if e.kind == kind {
			return e.fold, true
		}
	}
	return DerivedFoldUnknown, false
}

// Fold reports this entry's fold action, or false when its Kind is outside
// the vocabulary this binary knows.
func (d Derived) Fold() (DerivedFold, bool) { return DerivedFoldFor(d.Kind) }

// Complete reports whether the entry carries everything its fold needs, so a
// consumer can reject an under-populated registry entry BEFORE it has written
// half an output file rather than discovering the gap mid-fold.
//
// It is a shape check over the document, not a check against a cohort: it
// says the entry is self-sufficient, not that the columns it names exist.
func (d Derived) Complete() bool {
	fold, ok := d.Fold()
	if !ok || d.Name == "" || d.Position < 0 {
		return false
	}
	switch fold {
	case DerivedFoldRestore:
		// One source to restore INTO, and the reason dictionary that says
		// what to write. Both are the whole point of the kind.
		return len(d.Sources) == 1 && len(d.Reasons) > 0
	case DerivedFoldDrop:
		// Nothing is reconstructed, so the bar is only that the entry says
		// what the column was: its constituents in bit order and the set
		// definition it keys into.
		return len(d.Sources) > 0 && d.SetName != ""
	}
	return false
}
