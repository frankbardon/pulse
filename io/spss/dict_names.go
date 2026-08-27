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
	"unicode/utf8"

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

// ---------------------------------------------------------------------------
// The opt-in rewrite
// ---------------------------------------------------------------------------

// NameRename records one variable a [WriterOptions.SanitiseNames] export
// renamed: the cohort field it came from, and the SPSS name it went out as.
//
// It is a pair rather than a map entry because the report is ORDERED — a
// caller reading it back wants schema order, which is the order the emitted
// file's variables are in.
type NameRename struct {
	// Field is the cohort field the variable is written from. Empty for a
	// name no single column owns, which today is only a multiple-response
	// set name.
	Field string `json:"field"`

	// From is the name the cohort asked for and a `.sav` cannot carry.
	From string `json:"from"`

	// Name is the legal SPSS name that was emitted instead.
	Name string `json:"name"`
}

// sanitiseNames rewrites every emitted name that is not a legal SPSS name,
// and returns what it changed.
//
// It runs only when [WriterOptions.SanitiseNames] is set and only on the
// SYNTHESISED front-end — see the option's own documentation for why the
// sidecar path is exempt.
//
// # Order of business, and why it is two passes
//
// Pass one RESERVES every name that is already legal. Pass two rewrites the
// rest, and may not land on a reserved name. Doing it in one pass would let
// a rewritten name claim a slot a later, perfectly legal column was going to
// need — so `q1 x` would become `q1_x` and the real `q1_x` two columns along
// would then be the one that had to move. The column that was already
// correct is the one that must not be disturbed.
//
// Rewrites are deterministic: the same cohort yields the same names, because
// the derivation is a pure function of the name and the collision counter
// walks the variables in emission order.
//
// # What it does NOT touch
//
// The record type 2 SHORT name. [sanitiseShortName] already folds any Pulse
// name onto a legal, unique 8-byte handle, so a short name is never the
// illegal one; and re-minting it here would have to re-run the whole minter
// to stay unique, for no gain. The record 7/13 LONG name IS updated, because
// that is the name a reader actually reports — leaving it behind would emit
// a file whose variables still answer to the illegal name the rewrite was
// supposed to remove.
//
// It runs BEFORE applyCharsetWrite, because the transcode measures and
// segments names it is handed. One consequence is worth naming: the 64-byte
// ceiling is applied here to the UTF-8 form, so a name that fits as UTF-8 and
// overflows once encoded into a narrower codepage is still a hard
// PULSE_SPSS_NAME_INVALID from validateNames. That is the honest answer —
// the alternative is truncating a name against a width the caller cannot see.
func sanitiseNames(f *outFile) []NameRename {
	taken := make(map[string]bool, len(f.vars))
	sets := make(map[string]bool, len(f.mrSets))

	// Pass one: whatever is already legal keeps its name outright.
	for _, v := range f.vars {
		if nameFault(v.name, len(v.name)) == "" {
			taken[nameKey(v.name)] = true
		}
	}
	for i := range f.mrSets {
		if nameFault(f.mrSets[i].Name, len(f.mrSets[i].Name)) == "" {
			sets[nameKey(f.mrSets[i].Name)] = true
		}
	}

	shorts := make(map[string]bool, len(f.vars))
	for _, v := range f.vars {
		shorts[nameKey(v.shortName)] = true
		for _, seg := range v.segments {
			shorts[nameKey(seg.Name)] = true
		}
	}

	var out []NameRename

	// Pass two: everything else is derived, uniquified and recorded.
	for _, v := range f.vars {
		if nameFault(v.name, len(v.name)) == "" {
			continue
		}
		from := v.name
		name := uniqueName(sanitiseVariableName(from), taken)
		taken[nameKey(name)] = true
		v.name = name

		// mintNames' rule, re-applied: a long name is emitted whenever the
		// real name is not byte-identical to the short one, and suppressed
		// when it is. Leaving a stale longName here would re-introduce the
		// name this rewrite exists to remove.
		if name != v.shortName {
			v.longName = name
		} else {
			v.longName = ""
		}
		out = append(out, NameRename{Field: v.fieldName, From: from, Name: name})
	}

	// The record type 2 SHORT name is almost always legal by
	// construction — sanitiseShortName upper-cases, truncates and maps
	// every disallowed byte to '_'. Almost: it admits '.' anywhere, and a
	// name ENDING in '.' is a command terminator to SPSS, so a cohort
	// field called `total.` mints the illegal short name `TOTAL.`. Repair
	// rather than re-mint, so a column whose short name was already fine
	// keeps it and the emitted bytes do not move for cohorts with nothing
	// to fix.
	for _, v := range f.vars {
		// Every segment name is a short name in its own right, and the HEAD
		// segment's is the one records 7/13 and 7/14 key by — so a repair
		// that moved v.shortName and left the segment behind would emit a
		// 7/13 mapping keyed to a name no variable answers to, and the
		// mapping would be dropped by any reader. Repair them together.
		for i := range v.segments {
			if v.segments[i].Name != v.shortName {
				continue
			}
			if nameFault(v.shortName, len(v.shortName)) != "" {
				v.segments[i].Name = repairInto(v.shortName, shorts)
			}
		}
		if nameFault(v.shortName, len(v.shortName)) != "" {
			short := v.shortName
			if len(v.segments) > 0 && v.segments[0].Name != short {
				// The head segment was already repaired above; reuse it so
				// the two cannot diverge.
				short = v.segments[0].Name
			} else {
				short = repairInto(short, shorts)
			}
			v.shortName = short
			if v.name != short && v.longName == "" {
				v.longName = v.name
			}
		}
		for i := range v.segments {
			if nameFault(v.segments[i].Name, len(v.segments[i].Name)) == "" {
				continue
			}
			v.segments[i].Name = repairInto(v.segments[i].Name, shorts)
		}
	}

	for i := range f.mrSets {
		from := f.mrSets[i].Name
		if nameFault(from, len(from)) == "" {
			continue
		}
		// A response-set name is conventionally '$'-prefixed and the
		// character rule admits that, so the prefix is held aside and the
		// rest sanitised as an ordinary name.
		name := "$" + uniqueName(sanitiseVariableName(strings.TrimPrefix(from, "$")), sets)
		sets[nameKey(name)] = true
		f.mrSets[i].Name = name
		out = append(out, NameRename{From: from, Name: name})
	}

	// The weighting variable is named, not indexed, in the model; a rename
	// that left it pointing at a name no variable answers to would silently
	// unweight the file.
	if f.weightName != "" {
		for _, r := range out {
			if r.From == f.weightName {
				f.weightName = r.Name
				break
			}
		}
	}
	return out
}

// sanitiseVariableName derives a legal SPSS variable name from an arbitrary
// Pulse field name.
//
// Every rune the name rule rejects becomes '_' rather than being dropped, so
// two names differing only in punctuation stay different names and the
// collision counter — not silence — is what resolves them. A name that does
// not open with a letter is prefixed rather than trimmed, because trimming
// "2024_revenue" to "revenue" throws away the part that made it distinct.
func sanitiseVariableName(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case i == 0 && isNameStart(r):
			b.WriteRune(r)
		case i == 0:
			// The first rune is not a legal opener. Prefix, then let the
			// rune itself through the ordinary body rule so a leading
			// digit survives as a digit.
			b.WriteByte('V')
			if isNameRune(r) {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		case isNameRune(r):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := truncateName(b.String(), maxVariableNameLen)
	if out == "" {
		return "V"
	}
	// A trailing '.' is a command terminator to SPSS, so it cannot be the
	// last byte even though it is a legal body character.
	for strings.HasSuffix(out, ".") {
		out = out[:len(out)-1] + "_"
	}
	return out
}

// repairInto repairs a short name and claims the result in taken, releasing
// the name it replaced. The uniqueness walk is the minter's: truncate to make
// room for a decimal suffix and count up.
func repairInto(name string, taken map[string]bool) string {
	delete(taken, nameKey(name))
	short := repairShortName(name)
	for n := 2; taken[nameKey(short)]; n++ {
		suffix := strconv.Itoa(n)
		keep := shortNameLen - len(suffix)
		if keep > len(short) {
			keep = len(short)
		}
		short = short[:keep] + suffix
	}
	taken[nameKey(short)] = true
	return short
}

// repairShortName fixes the one fault sanitiseShortName can leave behind: a
// trailing '.'. It replaces rather than trims so the name keeps its length
// and two short names that differed only there stay different.
func repairShortName(s string) string {
	for strings.HasSuffix(s, ".") {
		s = s[:len(s)-1] + "_"
	}
	if s == "" {
		return "V"
	}
	return s
}

// uniqueName returns name, or the first name_2, name_3, ... not already
// spoken for. Comparison is case-insensitive because SPSS's is.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[nameKey(name)] {
		return name
	}
	for n := 2; ; n++ {
		suffix := "_" + strconv.Itoa(n)
		cand := truncateName(name, maxVariableNameLen-len(suffix)) + suffix
		if !taken[nameKey(cand)] {
			return cand
		}
	}
}

// truncateName cuts a name to at most n BYTES without splitting a rune. The
// ceiling is a byte count because SPSS's is; the rune boundary matters
// because half a UTF-8 sequence is not a character in any charset.
func truncateName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// nameSanitised is the warning that makes the opt-in rewrite honest: it
// carries every rename, so an emitted file's variables can always be mapped
// back to the cohort fields they came from.
//
// The prose caps the list at three and the details do not — a cohort whose
// every column carries a space would otherwise report a truncated list as if
// it were the whole of it.
func nameSanitised(renames []NameRename) *errors.CodedError {
	pairs := make([]any, 0, len(renames))
	shown := make([]string, 0, 3)
	for i, r := range renames {
		pairs = append(pairs, map[string]any{"field": r.Field, "from": r.From, "name": r.Name})
		if i < 3 {
			shown = append(shown, strconv.Quote(r.From)+" -> "+strconv.Quote(r.Name))
		}
	}
	msg := "spss: --sanitise-names rewrote " + strconv.Itoa(len(renames)) +
		" name(s) that a .sav cannot carry: " + strings.Join(shown, ", ")
	if len(renames) > len(shown) {
		msg += ", and " + strconv.Itoa(len(renames)-len(shown)) + " more"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_NAME_SANITISED,
		msg+". The full list is under \"renames\"; the emitted variables carry the rewritten names.",
		map[string]any{errors.DetailSPSSRenames: pairs})
}
