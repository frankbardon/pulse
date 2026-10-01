package spss

// DERIVED-COLUMN FOLD-BACK: what an export DOES with the columns the import
// synthesised.
//
// # The question, and why a name cannot answer it
//
// An import emits columns the source dictionary never declared: a
// `<var>_missing` reason sibling (E4-S2) and a multiple-dichotomy `set_*`
// convenience column (E4-S4). Both are additive, so the cohort has strictly
// more columns than the `.sav` had variables, and an export folding it back
// has to answer one question per column: is this a variable the file
// declared, or something this reader made up?
//
// It is NOT answered by looking at the name. `income_missing` is a perfectly
// legal SPSS variable name and a survey that declares one is not
// hypothetical; a set column's name is whatever the set was called minus its
// '$', which pattern-matches against nothing at all. Answering by suffix
// would drop a real respondent column the first time a file used that name,
// and no reader would ever show that anything was missing.
//
// So the answer is READ, from the registry E4-S5 built: [Payload.Derived]
// names every synthesised column explicitly, and a column absent from it is
// a source variable by construction. This file consumes that registry and
// nothing else. There is no name matching anywhere in it.
//
// # Consumed, not emitted — and what "consumed" means per kind
//
// [DerivedFoldFor] gives each kind exactly one action, and the two differ
// because only one of the two columns is the sole home for what it holds:
//
//   - [DerivedFoldRestore] (`numeric_missing`). The analytic column beside
//     it is null at every missing position and the `.pulse` null bitmap is
//     one bit, so WHICH SPSS missing state each row was in exists nowhere
//     else in the cohort. The fold binds the sibling to the variable it
//     belongs to, and the data encoder then writes the recorded SPSS code
//     back into every null instead of the system-missing sentinel. The
//     mapping comes from [Derived.Reasons] and is never re-derived: E4-S2
//     was explicit that rebuilding it from the missing specification plus
//     the value labels is a guess that can silently disagree with the
//     import that made the column.
//
//   - [DerivedFoldDrop] (`multiple_dichotomy`). Every bit the column shows
//     is a second reading of a constituent that is still in the cohort under
//     its own name, so there is nothing to reconstruct. The fold checks that
//     the constituents really are being emitted and then lets the column go.
//
// Neither action emits an SPSS variable, which is why an emitted `.sav`
// carries exactly the source's own variables and no `_missing` or `set_*`
// artefacts.
//
// # Why the encoder needs no filter
//
// E5-S3's encoder is driven by [DictionaryPlan.Columns] alone, so a cohort
// field no column names is decoded (the record stride demands it) and then
// written nowhere. A derived column therefore cannot leak into the case
// stream by construction, and this pass never has to filter one out. What it
// does instead is decide what a derived column MEANS for the variable it
// belongs to — plan construction, not the case pass.
//
// # The check that is worth more than the fold
//
// [DictionaryPlan.UnboundFields] lists every cohort field no emitted
// variable is written from. On the sidecar path those should be EXACTLY the
// derived columns. A field that is unbound and NOT in the registry is a
// column about to leave the export silently, which is the one outcome this
// whole path exists to refuse — PULSE_SPSS_COLUMN_UNMAPPED. It is checked
// on the synthesised path too, where the registry is empty and every field
// is expected to bind, so a front-end that ever forgot a column would say so
// rather than quietly shipping a narrower file.

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// MissingCode is what one dictionary ID of a `<var>_missing` reason sibling
// means for the variable it belongs to: the SPSS value to write in place of
// the cohort's null.
//
// It is the projection of [DerivedReason] the data encoder indexes by ID,
// built once at plan time for the same reason [CategoryCode] is — a mapping
// resolved per case is a mapping that can be resolved differently per case.
type MissingCode struct {
	// Known reports that the ID has a recorded reason at all. A false entry
	// is an ID the registry never described, which the encoder refuses
	// rather than writing a value for.
	Known bool

	// Sysmis marks the reason standing for the system-missing sentinel
	// rather than for a user-missing code. Code is meaningless when it is
	// set.
	Sysmis bool

	// Code is the SPSS numeric value to write. It is the SOURCE's own
	// double, in the source's own units: for a `date` or `datetime`
	// variable that is raw SPSS seconds, not epoch days, because the import
	// never converted a missing code and so the export must not either.
	Code float64

	// Reason is the dictionary entry text the cohort stores, carried for
	// diagnostics only.
	Reason string
}

// foldDerived consumes the derived-column registry into the emitted plan.
//
// It runs after emission because it decides nothing the dictionary section
// declares: a derived column produces no record type 2, so folding it
// changes only the data-plan half of [ColumnPlan]. Running it here rather
// than over the intermediate model keeps [DictionaryPlan.UnboundFields] —
// which it audits — as the one statement of what bound and what did not.
func foldDerived(req DictionaryRequest, plan *DictionaryPlan) error {
	var (
		derived  []Derived
		sidecar  string
		cohort   string
		registry = map[int]*Derived{} // cohort field index -> entry
	)
	if req.Sidecar != nil {
		sidecar, cohort = req.Sidecar.Path, req.Sidecar.Cohort
		if req.Sidecar.Document != nil {
			derived = req.Sidecar.Document.Payload.Derived
		}
	}

	byName := schemaIndex(req.Schema)
	// bound maps a cohort field index to the first emitted variable written
	// from it, which is what a Sources reference has to land on.
	bound := make(map[int]*ColumnPlan, len(plan.Columns))
	for i := range plan.Columns {
		c := &plan.Columns[i]
		if c.Field >= 0 {
			if _, seen := bound[c.Field]; !seen {
				bound[c.Field] = c
			}
		}
	}

	for i := range derived {
		d := &derived[i]
		at, ok := byName[strings.ToLower(d.Name)]
		if !ok {
			return unfoldable(d, sidecar,
				"the cohort's schema has no field of that name, so the registry does not describe this cohort")
		}
		if _, dup := registry[at]; dup {
			return unfoldable(d, sidecar,
				"the registry names the cohort field "+strconv.Quote(d.Name)+
					" twice, and a column cannot be folded two ways")
		}
		// A registry entry naming a column the export is ALSO emitting is
		// the document disagreeing with itself about whether that column is
		// synthetic. It is reported as the collision it is: the derived name
		// and the real variable that claimed it.
		if col, clash := bound[at]; clash {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_DERIVED_NAME_COLLISION,
				"spss: the metadata sidecar records "+strconv.Quote(d.Name)+
					" as a derived column, but this export is also emitting it as the SPSS variable "+
					strconv.Quote(col.Name)+"; the document cannot both declare the column and disown it",
				map[string]any{
					errors.DetailSPSSDerived:      d.Name,
					errors.DetailSPSSCollidesWith: col.Name,
					errors.DetailSPSSSidecar:      sidecar,
				})
		}
		if _, known := d.Fold(); !known {
			return unfoldable(d, sidecar,
				"its kind "+strconv.Quote(d.Kind)+" is not one this build knows how to fold back; "+
					"the document was written by a newer import, and both available guesses — emitting the column, "+
					"or dropping it — are silent data faults")
		}
		if !d.Complete() {
			return unfoldable(d, sidecar,
				"its registry entry is missing what a "+strconv.Quote(d.Kind)+
					" fold needs, so folding it would mean re-deriving the mapping and hoping it matched the import that made the column")
		}
		registry[at] = d
	}

	// Every cohort field no variable is written from must be one the
	// registry accounts for. See the file comment: this is the check, and
	// the fold below is the consequence of it.
	for _, at := range plan.UnboundFields {
		if _, ok := registry[at]; ok {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_COLUMN_UNMAPPED,
			"spss: the cohort field "+strconv.Quote(req.Schema.Fields[at].Name)+
				" is written to no SPSS variable and is not one the metadata sidecar records as a derived column; "+
				"emitting the file would drop it silently",
			map[string]any{
				errors.DetailSPSSField:   req.Schema.Fields[at].Name,
				errors.DetailSPSSCohort:  cohort,
				errors.DetailSPSSSidecar: sidecar,
			})
	}

	for at, d := range registry {
		fold, _ := d.Fold()
		switch fold {
		case DerivedFoldRestore:
			if err := foldRestore(d, at, byName, bound, sidecar); err != nil {
				return err
			}
		case DerivedFoldDrop:
			if err := foldDrop(d, byName, bound, sidecar); err != nil {
				return err
			}
		}
	}
	return nil
}

// foldRestore binds a `<var>_missing` sibling to the variable it belongs to.
//
// The sibling column itself emits nothing. What it does is change what the
// SOURCE variable writes wherever the cohort holds a null: the recorded SPSS
// code rather than the system-missing sentinel. That is the whole of the
// user-missing round trip, and it is why the sibling's own column index
// travels onto the plan.
func foldRestore(d *Derived, at int, byName map[string]int, bound map[int]*ColumnPlan, sidecar string) error {
	// Complete() has already established there is exactly one source.
	src, ok := byName[strings.ToLower(d.Sources[0])]
	if !ok {
		return unfoldable(d, sidecar, "it restores into "+strconv.Quote(d.Sources[0])+
			", which this cohort's schema has no field for")
	}
	col, emitted := bound[src]
	if !emitted {
		return unfoldable(d, sidecar, "it restores into "+strconv.Quote(d.Sources[0])+
			", which no emitted variable is written from; consuming the reason column would discard what it holds")
	}
	switch col.Encoding {
	case EncodeNumeric, EncodeDateDays, EncodeDateTimeSeconds:
	default:
		// A reason sibling is generated only for the non-dictionary-bearing
		// numeric arm — a string variable and a value-labelled numeric are
		// both categorical, where the missing CODE is itself a dictionary
		// entry and nothing was moved out of the column. An entry pointing
		// at any other encoding describes a cohort this import could not
		// have produced.
		return unfoldable(d, sidecar, "it restores into "+strconv.Quote(d.Sources[0])+
			", which is emitted as "+col.Encoding.String()+
			"; a user-missing reason column belongs only to a plain numeric variable")
	}

	maxID := uint32(0)
	for _, r := range d.Reasons {
		if r.ID > maxID {
			maxID = r.ID
		}
	}
	codes := make([]MissingCode, maxID+1)
	for _, r := range d.Reasons {
		if codes[r.ID].Known {
			return unfoldable(d, sidecar, "two of its reasons claim dictionary ID "+
				strconv.FormatUint(uint64(r.ID), 10)+", so a stored ID would name two different SPSS states")
		}
		entry := MissingCode{Known: true, Sysmis: r.Sysmis, Reason: r.Reason}
		switch {
		case r.Sysmis:
		case r.Code != nil:
			entry.Code = float64(*r.Code)
		default:
			return unfoldable(d, sidecar, "its reason "+strconv.Quote(r.Reason)+
				" records neither the system-missing sentinel nor an SPSS code, so there is nothing to write back for it")
		}
		codes[r.ID] = entry
	}

	col.MissingField = at
	col.MissingCodes = codes
	return nil
}

// foldDrop lets a multiple-dichotomy `set_*` column go.
//
// Nothing is reconstructed from it, because nothing in it is only there: bit
// i of its mask is a second reading of Sources[i], which is a real source
// variable this export emits under its own name. What the fold owes is the
// check that this is actually true — a dropped column whose constituents are
// NOT being emitted is data leaving the file, not an artefact being consumed.
func foldDrop(d *Derived, byName map[string]int, bound map[int]*ColumnPlan, sidecar string) error {
	for _, name := range d.Sources {
		src, ok := byName[strings.ToLower(name)]
		if !ok {
			return unfoldable(d, sidecar, "it names the constituent "+strconv.Quote(name)+
				", which this cohort's schema has no field for")
		}
		if _, emitted := bound[src]; !emitted {
			return unfoldable(d, sidecar, "its constituent "+strconv.Quote(name)+
				" is written to no SPSS variable, so dropping the set column would drop the only copy of what it holds")
		}
	}
	return nil
}

// unfoldable reports a registry entry an export cannot act on.
func unfoldable(d *Derived, sidecar, why string) error {
	details := map[string]any{errors.DetailSPSSDerived: d.Name}
	if len(d.Sources) == 1 {
		details[errors.DetailSPSSVariable] = d.Sources[0]
	}
	if d.SetName != "" {
		details[errors.DetailSPSSSet] = d.SetName
	}
	if sidecar != "" {
		details[errors.DetailSPSSSidecar] = sidecar
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_DERIVED_UNFOLDABLE,
		"spss: the derived column "+strconv.Quote(d.Name)+" cannot be folded back: "+why, details)
}
