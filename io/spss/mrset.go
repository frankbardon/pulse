package spss

// Multiple-DICHOTOMY response sets, and the derived `set_*` convenience
// column each one gets — in ADDITION to its constituent variables, never
// instead of them.
//
// # Why this is the marquee mapping, and why it is additive anyway
//
// SPSS is the one common tabular format that DECLARES a multi-select
// question. Every other ingest path Pulse has must guess one: io/infer.go's
// probeSetClassification looks at delimited strings and votes with a 30%
// heuristic. Here the file states it — record 7/5, 7/7 or 7/19 says "$media
// is a multiple dichotomy over Q1A Q1B Q1C, counted value 1" — and Pulse's
// `set_*` types were built for exactly that shape.
//
// So the temptation is to COLLAPSE: replace the N indicator variables with
// one bitmask column and be done. That is lossy, and the loss is not a
// corner case. An MD constituent can itself be system-missing, and a bit is
// one bit:
//
//	Q1A=0  "shown the option, did not pick it"
//	Q1A=.  "never asked — skipped the battery, or filtered past it"
//
// Both are bit 0. Item non-response is not a rounding error in survey work;
// it drives weighting and it is reported. A bitmask cannot carry it, and
// nothing downstream could recover it, because the constituents would be
// gone.
//
// The resolution is the same one missing.go reached from the other
// direction: DERIVE, do not replace. Every constituent variable is imported
// as its own ordinary column — with its own null bitmap bit and, where it
// declares user-missing codes, its own `<var>_missing` reason sibling — and
// the set gets an EXTRA `set_*` column beside them. Lossless by
// construction. The constituents carry the fidelity; the derived column
// carries the ergonomics (FILTER_SET, GROUP_SET_PER_ELEMENT,
// AGG_SET_FREQUENCY over one field instead of N). The cost is schema width,
// which is the only currency this trade could have been paid in.
//
// The export half (E5-S5) drops the derived column and reconstructs the
// `.sav` from the constituents, which is possible precisely because they
// were kept.
//
// # Multiple CATEGORY sets get nothing here
//
// An MC set is N variables each holding a code from a shared value-label
// set. It is positional, it permits duplicates and it has no counted value —
// it is genuinely N categorical columns and not a set at all. E2-S3 modelled
// the two flavours as two Go TYPES so that consuming one requires a type
// switch and `countedValue` does not exist on the category arm; this file
// takes the *mrDichotomySet arm and ignores the other. E4-S5 owns MC.
//
// # The bitmask is built from the DECLARED counted value
//
// mrDichotomySet.countedValue is the text the record carried, verbatim. The
// wire form does not say whether it is a number or a string — that follows
// from the member variables' declared type — so it is interpreted per
// member: parsed as a double for a numeric constituent, compared as trimmed
// decoded text for a string one. Nothing is guessed and nothing is assumed
// about 0/1 coding. A counted value that will not parse against a numeric
// member does not derive a wrong column; it derives no column and warns.
//
// # The dictionary holds CONSTITUENT NAMES, not option labels
//
// Bit i of the mask maps to dictionary entry i, and the entry text is the
// constituent's PULSE FIELD NAME. Not its variable label.
//
// That follows mapping.go's rule for categorical dictionaries — the entry is
// the value, the label rides the sidecar — and it earns its keep three more
// ways here. Field names are unique in a cohort by construction, so the
// dictionary is injective with no collision handling; a variable label may
// be empty, duplicated across constituents, or contain the delimiter, and
// any of the three would need a fallback. And the entry text IS the name of
// the column holding that element's fidelity, so an analyst who finds a bit
// interesting can go straight to the constituent that can tell them whether
// a zero meant "no" or "not asked". That round trip is the entire argument
// for the additive design; naming the bits after the columns is what makes
// it one step instead of a sidecar lookup.
//
// Option labels are not lost: they are each constituent variable's own
// `variables[].label` in the sidecar, reachable by the same name.
//
// # The cell text, and the one thing it cannot say
//
// The derived column reaches the cohort through the SHARED import path, like
// every other cell this reader emits: io/import.go splits a set cell on
// io.DefaultSetDelimiter ("|") and turns each token into a dictionary ID.
// (ImportJob.SetDelimiters is inert under SchemaAwareReader — see the
// contract on pio.SchemaAwareReader — so "|" is not a preference, it is the
// only delimiter available.) So a row selecting Q1A and Q1C renders
// "Q1A|Q1C".
//
// Three row states, two of which the shared path can express directly:
//
//	at least one constituent counted   "Q1A|Q1C"   bits set
//	none counted, some present         "|"         EMPTY MASK, not null
//	every constituent missing          ""          NULL
//
// The middle row is the interesting one, and it is why setEmptySelection
// exists. "Answered the battery and picked nothing" is a real answer and
// CLAUDE.md's byte-layout invariants make it representable — an empty mask
// is a valid "no selection" distinct from null. But io/import.go reads an
// empty cell as a null token BEFORE it consults any dictionary, so "" cannot
// carry it. A cell of one bare delimiter can: it is not a null token, and
// splitSetTokens drops empty tokens, so it yields zero tokens and therefore
// a zero mask with no dictionary mutation. The bottom row — nothing known
// about any constituent — is the one that genuinely is null.
//
// That distinction is a convenience too, and the constituents remain
// authoritative: they separate "0" from sysmis per option, which the mask
// cannot do even with the empty-selection form.
//
// # When a set does not derive
//
// Refusals are WARNINGS (PULSE_SPSS_MR_SET_NOT_DERIVED), never errors, and
// that falls straight out of the additive design: the constituents are
// already in the cohort, so a set that does not derive costs ergonomics and
// nothing else. Failing the import to protect a convenience column would
// throw away data. The refusals:
//
//   - more than 64 constituents — a set_u64 has 64 bits, and there is no
//     wider set type to widen to (the acceptance criterion this file was
//     written against names this case explicitly);
//   - a member no record type 2 declares, or one named twice (one bit
//     cannot be two variables);
//   - a counted value that will not parse against a numeric member;
//   - a constituent whose Pulse field name contains "|" or IS a null
//     sentinel token — either would make the cell text ambiguous on the
//     shared import path. A constituent literally named "NA" is not
//     hypothetical, and a lone "NA" cell would import as null rather than
//     as that one bit.
//
// The derived NAME colliding with a real variable is the same shape and
// warns too — with PULSE_SPSS_DERIVED_NAME_COLLISION, reused from E4-S2
// because the situation is identical and the details are the same three
// names. Note that E4-S2 raises that code as a hard ERROR and this file
// raises it as a warning; the asymmetry is deliberate and is the additive
// design again. Suppressing a `<var>_missing` sibling loses the reason a
// value is missing, so E4-S2 must stop. Suppressing this column loses
// nothing.
//
// # Subtype 19 is uncorroborated — the standing risk
//
// E2-S3 flagged that record 7/19's extended 'E' grammar is PSPP-spec-only.
// It was re-checked for this story against the two independent R readers,
// and neither corroborates it: haven (ReadStat) and foreign::read.spss both
// read a fixture carrying 7/7 and 7/19 definitions cleanly and expose NO
// multiple-response set metadata whatsoever, from either subtype. So there
// is no second opinion to be had — what the cross-check does establish is
// only that a file carrying a 7/19 record as this package emits it is not
// structurally objectionable to ReadStat's extension walk.
//
// The residual risk is bounded by where subtype 19 can reach. A
// misinterpreted 'E' record can only produce a wrong counted value, a wrong
// member list or a parse failure — the first two derive a wrong CONVENIENCE
// column and the third derives none, and in every case the constituents are
// untouched and correct. That is the additive design paying for itself a
// third time.

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
)

// DerivedKindMultipleDichotomy is the sidecar [Derived] Kind of a
// multiple-dichotomy `set_*` convenience column.
//
// This package owns the derived-kind vocabulary and E4-S1 reserved the
// registry slot as additively extensible, so adding this value alongside
// [DerivedKindNumericMissing] leaves SidecarFormatVersion at 1. A consumer
// folding a cohort back to `.sav` matches on this string to know the column
// is synthesised, must NOT be emitted as an SPSS variable, and needs no
// reconstruction work at all — the constituents named in Derived.Sources are
// real columns that already carry every value the set was built from.
const DerivedKindMultipleDichotomy = "multiple_dichotomy"

// setElementDelimiter is the delimiter the derived cell joins selected
// element tokens with.
//
// It is io.DefaultSetDelimiter and not a choice. The shared import path
// splits a `set_*` cell on ImportJob.SetDelimiters[field], which is INERT
// when the reader is a pio.SchemaAwareReader — the authoritative-schema
// contract says so explicitly — leaving DefaultSetDelimiter as the only
// delimiter a cell this package emits will ever be split on.
const setElementDelimiter = pio.DefaultSetDelimiter

// setEmptySelection is the cell text for a row that answered the battery and
// selected nothing: a single bare delimiter.
//
// It renders an EMPTY MASK rather than a null, which is a state CLAUDE.md's
// byte-layout invariants define as valid and distinct. The mechanism is the
// shared import path's own two documented rules, not a trick: a lone "|" is
// not one of the null sentinel tokens io/import.go's isNullToken recognises,
// so the cell reaches convertValue; and splitSetTokens trims each part and
// drops the empty ones, so "|" yields zero tokens, mask 0, and no dictionary
// mutation.
//
// An empty string cannot serve, because it IS a null token and is consumed
// before any dictionary is consulted. That is why the two states need two
// spellings.
const setEmptySelection = setElementDelimiter

// maxSetElements is the widest `set_*` bitmask Pulse has, and therefore the
// most constituents a set can have and still derive a column.
const maxSetElements = 64

// mrSetElement is one bit of a derived set column.
type mrSetElement struct {
	// bit is the bit position, which is also the dictionary ID of the
	// entry naming this element.
	bit uint

	// col is the index into dictionary.vars / mapping.cols / dataPlan.cols
	// of the constituent variable this bit reads.
	col int

	// field is the constituent's Pulse field name, which is the dictionary
	// entry text at position bit. See the file comment for why the entry is
	// the name and not the variable label.
	field string

	// label is the constituent's SPSS variable label, "" when it declares
	// none. It is carried for diagnostics and for the sidecar; it is
	// deliberately NOT the dictionary entry.
	label string

	// numeric reports whether the constituent is a numeric variable, and
	// hence whether counted or countedText decides selection.
	numeric bool

	// counted is the set's counted value parsed as a double. Meaningful
	// only when numeric.
	counted float64

	// countedText is the set's counted value as the dictionary key a string
	// constituent's datum is compared against. Meaningful only when the
	// constituent is a string.
	countedText string
}

// mrSetColumn is one resolved derived multiple-dichotomy column.
type mrSetColumn struct {
	// name is the Pulse field name: the set name with its leading '$'
	// stripped. See stripSetSigil.
	name string

	// setName is the SPSS set name verbatim, '$' included. It is the
	// identity of the definition in the source file and in the sidecar's
	// multiple_response_sets block, so it is recorded rather than
	// reconstructed from name.
	setName string

	// label is the set label, "" when the definition carries none.
	label string

	// counted is the counted value exactly as the record carried it,
	// retained for diagnostics and for the description.
	counted string

	// fieldType is the resolved set width: the narrowest of set_u8 /
	// set_u16 / set_u32 / set_u64 that has a bit per constituent.
	fieldType encoding.FieldType

	// elements are the bits in order. Element i occupies bit i and
	// dictionary ID i.
	elements []mrSetElement

	// nullable is a FACT established by scanning every case, exactly as it
	// is for an ordinary column: true when at least one case had every
	// constituent missing and therefore rendered the empty cell that
	// imports as null. A row that answered and selected nothing is NOT
	// null — see setEmptySelection.
	nullable bool
}

// values returns the dictionary entry texts in ID order.
func (s *mrSetColumn) values() []string {
	out := make([]string, len(s.elements))
	for i, e := range s.elements {
		out[i] = e.field
	}
	return out
}

// sources returns the constituent Pulse field names in BIT order, which is
// what the sidecar records under Derived.Sources: bit i is Sources[i].
func (s *mrSetColumn) sources() []string { return s.values() }

// description is the derived column's Pulse field description.
//
// It leads with what the column IS rather than with the set label, for the
// same reason missingSiblingDescription does: a column described only
// "Media used" reads as a variable the file declared, and the one thing a
// reader must not believe about this column is that it is authoritative. The
// constituents are, and the description says so.
func (s *mrSetColumn) description() string {
	var b strings.Builder
	b.WriteString("Multiple-dichotomy set ")
	b.WriteString(strconv.Quote(s.setName))
	if s.label != "" {
		b.WriteString(" (")
		b.WriteString(s.label)
		b.WriteString(")")
	}
	b.WriteString(": one element per constituent variable, set where that variable holds the counted value ")
	b.WriteString(strconv.Quote(s.counted))
	b.WriteString(". Derived and additive — the ")
	b.WriteString(strconv.Itoa(len(s.elements)))
	b.WriteString(" constituent columns are all retained and remain authoritative, because a bit cannot tell \"not selected\" from \"not asked\"")
	return truncateDescription(b.String())
}

// truncateDescription clamps a generated description to the byte budget
// encoding.WriteDescription enforces.
//
// A generated description is assembled from file-supplied text (a set label
// is a counted string of arbitrary length), so the budget has to be applied
// here rather than assumed. It cuts on a rune boundary: a description
// truncated mid-rune would be invalid UTF-8 in the schema block.
func truncateDescription(s string) string {
	if len(s) <= encoding.MaxDescriptionBytes {
		return s
	}
	cut := encoding.MaxDescriptionBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// stripSetSigil turns an SPSS multiple-response set name into a Pulse field
// name by dropping its leading '$'.
//
// SPSS requires the sigil; Pulse has no use for it and one real cost. Pulse
// field names are permissive, so "$media" would encode fine — but expr-lang
// identifiers are not, so a field named "$media" is unreachable from
// ATTR_FORMULA and FILTER_EXPRESSION. This column exists to be convenient;
// shipping it with a name half the request surface cannot address would
// defeat the point.
//
// Only ONE leading '$' is dropped, and nothing else is rewritten. The full
// name including the sigil is retained on mrSetColumn.setName and in the
// sidecar's multiple_response_sets block, so the mapping back to the
// declaration is recorded rather than inferred.
func stripSetSigil(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(name, "$"))
}

// setTypeFor picks the narrowest set type with a bit per element, mirroring
// categoricalTypeFor. It reports false above 64, where there is no wider set
// type to widen to.
func setTypeFor(elements int) (encoding.FieldType, bool) {
	switch {
	case elements <= 0:
		return 0, false
	case elements <= int(encoding.FieldTypeSetU8.MaxSetEntries()):
		return encoding.FieldTypeSetU8, true
	case elements <= int(encoding.FieldTypeSetU16.MaxSetEntries()):
		return encoding.FieldTypeSetU16, true
	case elements <= int(encoding.FieldTypeSetU32.MaxSetEntries()):
		return encoding.FieldTypeSetU32, true
	case elements <= int(encoding.FieldTypeSetU64.MaxSetEntries()):
		return encoding.FieldTypeSetU64, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Planning the derived columns
// ---------------------------------------------------------------------------

// planMRSets resolves every multiple-DICHOTOMY definition in the dictionary
// into a derived column, or into a warning saying why it did not derive.
//
// It reads NO DATA, matching planOutputs, and for the same reason: the
// cohort's column layout has to be decidable from the dictionary alone so
// ReadHeader stays a dictionary-cheap call and cannot drift from what the
// schema declares. Only the nullable flag needs the data section, and that
// is filled in later by scanMRSetNulls.
//
// `owner` is the case-insensitive name index planOutputs maintains; a
// derived name colliding with anything already in it does not derive.
// Successful names are added, so two sets cannot claim one name either.
func planMRSets(d *dictionary, owner map[string]string) ([]*mrSetColumn, []*errors.CodedError) {
	if len(d.mrSets) == 0 {
		return nil, nil
	}

	byShortName := make(map[string]int, len(d.vars))
	for i, v := range d.vars {
		key := strings.ToUpper(v.name)
		if _, dup := byShortName[key]; !dup {
			byShortName[key] = i
		}
	}

	var out []*mrSetColumn
	var warnings []*errors.CodedError
	for _, def := range d.mrSets {
		// The type switch is the point of E2-S3's two-types modelling:
		// countedValue does not exist on the category arm, so this cannot
		// silently treat an MC set as a dichotomy. E4-S5 owns MC.
		set, ok := def.(*mrDichotomySet)
		if !ok {
			continue
		}
		col, warn := planMRSet(d, set, byShortName, owner)
		if warn != nil {
			warnings = append(warnings, warn)
			continue
		}
		owner[strings.ToUpper(col.name)] = col.name
		out = append(out, col)
	}
	return out, warnings
}

// planMRSet resolves one dichotomy definition, returning either a column or
// the single warning saying why there is none.
func planMRSet(d *dictionary, set *mrDichotomySet,
	byShortName map[string]int, owner map[string]string,
) (*mrSetColumn, *errors.CodedError) {
	members := set.vars
	if len(members) == 0 {
		// The parser already warned that the definition names no members;
		// repeating it here would double the diagnostic for one fault.
		return nil, nil
	}
	if len(members) > maxSetElements {
		return nil, mrSetNotDerived(set, len(members),
			"it names "+strconv.Itoa(len(members))+" constituent variables, more than the "+
				strconv.Itoa(maxSetElements)+" bits a set_u64 mask has, and there is no wider set type to widen to; "+
				"all "+strconv.Itoa(len(members))+" constituents are imported as ordinary columns")
	}

	name := stripSetSigil(set.name)
	if name == "" {
		return nil, mrSetNotDerived(set, len(members),
			"its name is empty once the leading '$' every SPSS set name carries is dropped, leaving no Pulse field name to give the derived column")
	}
	if existing, clash := owner[strings.ToUpper(name)]; clash {
		return nil, mrSetNameCollision(set, name, existing)
	}

	col := &mrSetColumn{
		name:    name,
		setName: set.name,
		label:   set.label,
		counted: set.countedValue,
	}

	counted := dictKey(set.countedValue)
	countedNum, countedNumOK := parseCountedValue(set.countedValue)

	seen := make(map[int]bool, len(members))
	for _, short := range members {
		at, found := byShortName[strings.ToUpper(short)]
		if !found {
			// The parser already warned that the member is undeclared;
			// this warning says what that costs — no derived column.
			ce := mrSetNotDerived(set, len(members),
				"it names the member variable "+strconv.Quote(short)+
					", which no record type 2 in this dictionary declares, so the set has no bit to assign it")
			ce.Details[errors.DetailSPSSVariable] = short
			return nil, ce
		}
		if seen[at] {
			ce := mrSetNotDerived(set, len(members),
				"it names the member variable "+strconv.Quote(short)+
					" more than once, and one bit cannot stand for one variable twice")
			ce.Details[errors.DetailSPSSVariable] = short
			return nil, ce
		}
		seen[at] = true

		v := d.vars[at]
		field := dictKey(v.fieldName())
		if field == "" || strings.Contains(field, setElementDelimiter) || rendersAsNull(field) {
			ce := mrSetNotDerived(set, len(members),
				"its constituent "+strconv.Quote(v.fieldName())+
					" cannot be a set dictionary entry: a derived cell joins the selected elements with "+
					strconv.Quote(setElementDelimiter)+
					" and the shared import path reads an empty cell or a null sentinel token as null, so this name would make the cell ambiguous")
			ce.Details[errors.DetailSPSSVariable] = v.fieldName()
			return nil, ce
		}

		e := mrSetElement{
			bit: uint(len(col.elements)), col: at, field: field,
			label: v.label, numeric: !v.isString(),
		}
		if e.numeric {
			if !countedNumOK {
				ce := mrSetNotDerived(set, len(members),
					"its counted value "+strconv.Quote(set.countedValue)+
						" is not a number, and its constituent "+strconv.Quote(v.fieldName())+
						" is a numeric variable whose data can never equal it")
				ce.Details[errors.DetailSPSSVariable] = v.fieldName()
				return nil, ce
			}
			e.counted = countedNum
		} else {
			if counted == "" {
				ce := mrSetNotDerived(set, len(members),
					"its counted value is empty, and its constituent "+strconv.Quote(v.fieldName())+
						" is a string variable whose blank data already reads as missing, so no value could ever count as selected")
				ce.Details[errors.DetailSPSSVariable] = v.fieldName()
				return nil, ce
			}
			e.countedText = counted
		}
		col.elements = append(col.elements, e)
	}

	ft, ok := setTypeFor(len(col.elements))
	if !ok {
		// Unreachable: the member count was bounded above and every member
		// contributed exactly one element. Kept so the resolution is total
		// rather than relying on that argument holding after an edit.
		return nil, mrSetNotDerived(set, len(col.elements),
			"no set type has a bit for each of its "+strconv.Itoa(len(col.elements))+" constituents")
	}
	col.fieldType = ft
	return col, nil
}

// parseCountedValue reads the declared counted value as a double.
//
// The record carries the counted value as TEXT and does not say whether it
// is a number — that follows from the member variables' declared type — so
// this is the numeric arm's interpretation of it and not a claim about the
// value. It trims first, because SPSS writers pad a counted value to the
// member's declared width.
func parseCountedValue(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// mrSetNotDerived builds the warning for a set that gets no derived column.
func mrSetNotDerived(set *mrDichotomySet, members int, why string) *errors.CodedError {
	msg := "spss: the multiple-dichotomy response set " + strconv.Quote(set.name) +
		" gets no derived set_* column because " + why +
		". Nothing was lost: the derived column is additive, so every constituent variable is imported as an ordinary column either way — what is unavailable is FILTER_SET / GROUP_SET_PER_ELEMENT over one field"
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_MR_SET_NOT_DERIVED, msg,
		map[string]any{
			errors.DetailSPSSSet:      set.name,
			errors.DetailSPSSDistinct: members,
			errors.DetailSPSSSubtype:  set.subtype,
		})
}

// mrSetNameCollision reuses E4-S2's collision code for a derived set column
// whose name a real variable already holds.
//
// E4-S2 raises the same code as a hard ERROR. The asymmetry is the additive
// design and not an inconsistency: suppressing a `<var>_missing` sibling
// loses the reason a value is missing, which lives nowhere else, so that
// path must stop; suppressing this column loses nothing at all, because
// every constituent is in the cohort regardless. Failing an otherwise-good
// import to protect a convenience column would be the wrong trade.
func mrSetNameCollision(set *mrDichotomySet, derived, existing string) *errors.CodedError {
	msg := "spss: the multiple-dichotomy response set " + strconv.Quote(set.name) +
		" would derive the convenience column " + strconv.Quote(derived) +
		" (its name with the leading '$' dropped, so it is addressable from ATTR_FORMULA), but this file already declares a variable named " +
		strconv.Quote(existing) + " and SPSS variable names are case-insensitive; the set derives no column. " +
		"Nothing was lost — every constituent is imported as an ordinary column either way — so unlike the " +
		strconv.Quote("<var>"+MissingSiblingSuffix) +
		" sibling collision this is a warning and not an error"
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_DERIVED_NAME_COLLISION, msg,
		map[string]any{
			errors.DetailSPSSSet:          set.name,
			errors.DetailSPSSDerived:      derived,
			errors.DetailSPSSCollidesWith: existing,
			errors.DetailSPSSSubtype:      set.subtype,
		})
}

// ---------------------------------------------------------------------------
// Rendering a cell
// ---------------------------------------------------------------------------

// renderMRSet renders one derived set cell from one case.
//
// It reads the CONSTITUENTS' bytes. The derived column has no storage of its
// own — it is a second reading of bytes that are already in the cohort under
// their own field names, which is precisely why keeping the constituents
// costs no fidelity and why dropping them would.
//
// The three-state result is documented on setEmptySelection. Note that a
// constituent that is missing is not "not selected" here in the sense the
// mask records: it contributes no bit AND no evidence that the row was
// answered, so a row of nothing but missing constituents falls to null
// rather than to an empty mask.
func (p *dataPlan) renderMRSet(s *mrSetColumn, c []byte) (string, *errors.CodedError) {
	var b strings.Builder
	anyPresent := false
	for i := range s.elements {
		e := &s.elements[i]
		col := &p.cols[e.col]
		selected, present, err := p.elementState(e, col, c)
		if err != nil {
			return "", err
		}
		if present {
			anyPresent = true
		}
		if !selected {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(setElementDelimiter)
		}
		b.WriteString(e.field)
	}
	if b.Len() > 0 {
		return b.String(), nil
	}
	if anyPresent {
		return setEmptySelection, nil
	}
	return "", nil
}

// elementState reads one constituent for one case and reports whether it
// counts as selected, and whether it carried a value at all.
//
// "Present" is what separates an empty mask from a null, so it is defined
// exactly: a numeric datum is present unless it is the system-missing
// sentinel or matches the variable's USER-missing predicate — a refusal code
// is not an answer to a multi-select — and a string datum is present unless
// it is blank or one of the shared import path's null sentinel tokens, which
// is the same rule scanCases applies to a categorical column.
//
// System-missing is tested BEFORE the user-missing predicate for the reason
// missingReason states: SPSS spells an open-ended range with its LOWEST
// sentinel, which is the same double as the default sysmis sentinel.
func (p *dataPlan) elementState(e *mrSetElement, col *dataColumn, c []byte) (selected, present bool, err *errors.CodedError) {
	if !e.numeric {
		text, derr := p.decodeStringDatum(col, p.stringBytes(col, c))
		if derr != nil {
			return false, false, derr
		}
		key := dictKey(text)
		if rendersAsNull(key) {
			return false, false, nil
		}
		return key == e.countedText, true, nil
	}

	seg := c[col.offset : col.offset+col.span]
	bits := p.bo.Uint64(seg)
	if p.isSysmis(bits) {
		return false, false, nil
	}
	value := math.Float64frombits(bits)
	if col.missing.match(value) {
		return false, false, nil
	}
	return value == e.counted, true, nil
}

// ---------------------------------------------------------------------------
// Nullability
// ---------------------------------------------------------------------------

// scanMRSetNulls establishes each derived column's nullable flag as a FACT,
// by walking every case exactly as the main scan does.
//
// It cannot be derived from the constituents' per-column statistics. Those
// say that SOME case of a column was missing; the derived column is null
// only where EVERY constituent of one case was, and no per-column
// accumulation can answer a question about a row.
//
// It is a second pass, and that is the price of not declaring a fact the
// reader has not checked: PulseSchema's contract is that nullability is
// scanned rather than sampled, and over-declaring the flag would be a
// harmless lie today and an unfalsifiable one later. The pass is skipped
// entirely when the file declares no derivable set, which is every file
// without multiple-response sets, and it short-circuits per column once the
// flag is known.
func scanMRSetNulls(plan *dataPlan, sets []*mrSetColumn, body []byte, cases int) error {
	if len(sets) == 0 {
		return nil
	}
	remaining := len(sets)
	for n := 0; n < cases && remaining > 0; n++ {
		c := body[n*plan.stride : (n+1)*plan.stride]
		for _, s := range sets {
			if s.nullable {
				continue
			}
			cell, err := plan.renderMRSet(s, c)
			if err != nil {
				return err
			}
			if cell == "" {
				s.nullable = true
				remaining--
			}
		}
	}
	return nil
}
