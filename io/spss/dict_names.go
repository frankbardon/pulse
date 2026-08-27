package spss

// VARIABLE-NAME VALIDATION: the boundary Pulse does not police and SPSS
// does.
//
// # Why this exists at all
//
// A `.pulse` field name is any UTF-8 string of any length in any case —
// `encoding/` validates nothing about it, and nothing in `io/` does either.
// An SPSS variable name is a constrained identifier: at most 64 bytes, drawn
// from a restricted character set, opening with a letter, and unique across
// the dictionary without regard to case. Every cohort that was not itself
// read from a `.sav` can therefore carry a name no `.sav` can express, and
// there is no earlier pass that would have caught it.
//
// Nothing downstream of here catches it either, and that is the reason the
// check is a refusal rather than a warning. The three ways an illegal name
// fails are all quiet:
//
//   - Record 7/13 is a tab-separated list of `SHORT=LONG` pairs with no
//     escape mechanism. A name carrying '=' or a tab does not corrupt the
//     record visibly; it re-parses as a DIFFERENT, shorter pair list, so
//     some other variable silently acquires this one's name and this one
//     keeps its minted short name.
//   - Record 7/7 (multiple-response sets) is space-separated over the same
//     namespace, so a space inside a name splits one member into two, and
//     the set then names variables that do not exist.
//   - Two names that differ only in case are ONE name to SPSS. The second
//     7/13 mapping is dropped, and the file holds a column no name reaches.
//
// None of the three produces an unreadable file. They produce a
// well-formed file that says something other than what the cohort said,
// which is precisely the failure mode this effort refuses to ship.
//
// # Where it runs, and on which bytes
//
// After applyCharsetWrite and before emission, so that the 64-byte ceiling
// is measured on the bytes that are actually written. SPSS name lengths are
// BYTE counts, and a name of 40 characters is 40 bytes as ASCII and rather
// more as UTF-8, so measuring the UTF-8 form would pass names that overflow
// and fail names that do not. The CHARACTER-set rule is the mirror image and
// is checked on the UTF-8 form ([outVar.utf8Name]), because "is this a
// letter" is a question about characters and cannot be asked of a codepage
// byte. Diagnostics quote the UTF-8 form for the same reason: a codepage
// byte in an error message is mojibake in the one place a human reads.
//
// # What is deliberately NOT rejected
//
// Non-ASCII letters pass. SPSS in UTF-8 mode accepts them, the fixtures this
// package round-trips contain them (`Identität`), and a rule that rejected
// them would refuse to write back a file we had just read.
//
// SPSS's reserved keywords (ALL, AND, BY, EQ, GE, GT, LE, LT, NE, NOT, OR,
// TO, WITH) are NOT rejected. They are a restriction on the SYNTAX language,
// not on the file format: a `.sav` holding a variable called `TO` reads back
// correctly in every reader, and refusing to write one would be this package
// enforcing a rule that belongs to a program it does not run.
//
// A trailing '_' is not rejected either — SPSS documents it as reserved for
// its own generated names and discourages it, but does not forbid it, and a
// cohort imported from a `.sav` can legitimately carry one.

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/frankbardon/pulse/errors"
)

// maxVariableNameLen is the widest variable name SPSS accepts, in BYTES.
const maxVariableNameLen = 64

// validateNames is the whole name pass: every emitted variable name, every
// emitted short name, and every multiple-response set name.
//
// It runs over the intermediate model rather than over [DictionaryPlan] so
// that a refusal happens before a single byte is emitted. There is no
// half-written `.sav` to clean up on the failure path because there is no
// written `.sav` at all.
func validateNames(f *outFile) error {
	seen := make(map[string]string, len(f.vars))
	shorts := make(map[string]string, len(f.vars))

	for _, v := range f.vars {
		display := orDefault(v.utf8Name, v.name)

		if why := nameFault(display, len(v.name)); why != "" {
			return nameInvalid(display, v.fieldName, "variable", why)
		}

		// The record type 2 short name is a name in its own right — records
		// 7/5, 7/7, 7/14 and 7/19 all key by it — so it is held to the same
		// rule. Its 8-byte ceiling is applyCharsetWrite's, checked there on
		// the encoded bytes; what is left here is its character set.
		if why := nameFault(v.shortName, len(v.shortName)); why != "" {
			return nameInvalid(v.shortName, v.fieldName, "record type 2 short variable name", why)
		}

		key := nameKey(display)
		if prev, dup := seen[key]; dup {
			return nameCollision(display, prev, v.fieldName)
		}
		seen[key] = display

		skey := nameKey(v.shortName)
		if prev, dup := shorts[skey]; dup {
			return shortNameCollision(display, v.shortName, prev, v.fieldName)
		}
		shorts[skey] = display
	}

	for i := range f.mrSets {
		// A set name carries a leading '$' by SPSS convention and the
		// character rule admits it, so no special case is needed here. The
		// name is already in the file's charset by this point, and a set
		// name is ASCII in every file this package has met, so the UTF-8
		// form and the wire form are the same bytes.
		name := f.mrSets[i].Name
		if why := nameFault(name, len(name)); why != "" {
			return nameInvalid(name, "", "multiple-response set name", why)
		}
	}
	return nil
}

// nameKey folds a name for the case-insensitive comparison SPSS applies to
// variable names. Lower-casing rather than upper-casing matches
// schemaIndex, which is the other place in this package where an SPSS name
// meets a Pulse one.
func nameKey(s string) string { return strings.ToLower(s) }

// nameFault reports why a name cannot be an SPSS variable name, or "" when
// it can.
//
// name is the UTF-8 form, which is what the character rule is defined on;
// wireLen is the length of the EMITTED bytes, which is what the 64-byte
// ceiling is defined on. Passing both rather than deriving one from the
// other is what keeps a transcoded name from being measured in the wrong
// alphabet — see the file comment.
func nameFault(name string, wireLen int) string {
	if name == "" {
		return "it is empty, and every SPSS variable must be named"
	}
	if wireLen > maxVariableNameLen {
		return "it is " + strconv.Itoa(wireLen) + " bytes once encoded, past the " +
			strconv.Itoa(maxVariableNameLen) + "-byte ceiling SPSS puts on a name"
	}
	for i, r := range name {
		if i == 0 {
			if !isNameStart(r) {
				return "it begins with " + quoteRune(r) +
					"; an SPSS name must begin with a letter or one of '@', '#', '$'"
			}
			continue
		}
		if !isNameRune(r) {
			return "it contains " + quoteRune(r) + " at byte " + strconv.Itoa(i) +
				"; an SPSS name may carry only letters, digits and '.', '_', '$', '#', '@'"
		}
	}
	if strings.HasSuffix(name, ".") {
		return "it ends with '.', which SPSS reads as a command terminator rather than as part of a name"
	}
	return ""
}

// isNameStart reports whether r may open an SPSS variable name.
//
// '$' is admitted even though SPSS reserves a leading '$' for its own system
// variables: it is what a multiple-response set name must begin with, and
// this package validates set names through the same rule rather than
// maintaining a second, nearly identical one.
func isNameStart(r rune) bool {
	return unicode.IsLetter(r) || r == '@' || r == '#' || r == '$'
}

// isNameRune reports whether r may appear after the first character.
//
// Marks are admitted alongside letters so a decomposed accent — a base
// letter followed by a combining mark, which is one perceived character and
// two runes — is not rejected for its second half.
func isNameRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
		return true
	}
	switch r {
	case '.', '_', '$', '#', '@':
		return true
	}
	return false
}

// quoteRune renders a rune for a diagnostic, naming a control character by
// its code point rather than printing it into the message.
func quoteRune(r rune) string {
	if unicode.IsPrint(r) {
		return strconv.QuoteRune(r)
	}
	return "U+" + strings.ToUpper(strconv.FormatInt(int64(r), 16))
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

// nameInvalid reports a name a `.sav` cannot carry.
//
// what says which name it is — a variable name, a short name, a set name —
// because the three are minted differently and the fix differs with them.
// field names the COHORT column the name came from, which is what a caller
// renames; it is empty for a name no single column owns.
func nameInvalid(name, field, what, why string) error {
	details := map[string]any{errors.DetailSPSSVariable: name}
	if field != "" {
		details[errors.DetailSPSSField] = field
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_NAME_INVALID,
		"spss: "+strconv.Quote(name)+" is not a name a .sav can carry as a "+what+": "+why,
		details)
}

// nameCollision reports two emitted variables that would answer to one name.
//
// It is a refusal rather than a rename because both alternatives lose. Two
// 7/13 mappings for one name leave the second silently dropped, so the file
// holds a column no name reaches; renaming one produces a column whose name
// no consumer can map back to the cohort field it came from.
func nameCollision(name, previous, field string) error {
	details := map[string]any{
		errors.DetailSPSSVariable:     name,
		errors.DetailSPSSCollidesWith: previous,
	}
	if field != "" {
		details[errors.DetailSPSSField] = field
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_NAME_COLLISION,
		"spss: two emitted variables would both be named "+strconv.Quote(name)+
			" (the earlier one is "+strconv.Quote(previous)+
			"), and SPSS variable names are unique without regard to case; one of the two would be unreachable",
		details)
}

// shortNameCollision reports two variables sharing a record type 2 short
// name. It is the same fault one level down: records 7/5, 7/7, 7/14 and
// 7/19 key by the short name, so a shared one re-points whichever of them
// mentions it.
func shortNameCollision(name, short, previous, field string) error {
	details := map[string]any{
		errors.DetailSPSSVariable:     short,
		errors.DetailSPSSCollidesWith: previous,
	}
	if field != "" {
		details[errors.DetailSPSSField] = field
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_NAME_COLLISION,
		"spss: the variable "+strconv.Quote(name)+" and "+strconv.Quote(previous)+
			" would share the record type 2 short name "+strconv.Quote(short)+
			", which records 7/5, 7/7, 7/14 and 7/19 key by; each of those records would name only one of the two",
		details)
}
