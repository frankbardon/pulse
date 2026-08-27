package spss

// Categorical user-missing codes: flagged in place, never duplicated
// into a sibling.
//
// # The asymmetry with missing.go is deliberate — do not "fix" it
//
// missing.go gives a NUMERIC variable a generated `<var>_missing`
// sibling because there is nowhere else for the reason to live: `f64`
// has no dictionary, and the null bitmap is one bit that can say a value
// is absent but never why. The reason has to be materialised as data or
// it is gone.
//
// A CATEGORICAL variable is the opposite case. Its column already stores
// the SPSS code as a dictionary entry — `Q1: 1=Yes, 2=No, 9=Refused`
// imports as a categorical whose dictionary holds "1", "2", "9" — so the
// missing code is preserved losslessly by the ordinary mapping, and a
// row that was refused still says 9. Nothing is missing, so nothing needs
// reconstructing. A sibling here would restate a value that is already
// there, and it would do it on EVERY variable of an all-categorical
// survey: a 200-question questionnaire whose every item carries a
// "Refused" code would double to 400 columns to carry no new information.
//
// So the two arms of one mapping look different on purpose. The numeric
// arm materialises the reason; the categorical arm flags the entry that
// already holds it. Both are "preserve, do not degrade" — they differ
// only because the substrate does.
//
// # This is also where string user-missing lands
//
// missing.go's compileMissingTest returns nil for a string variable and
// says the categorical arm owns it. This is that arm. A string variable
// maps to `categorical_*` where the value IS the dictionary entry, so the
// same resolution applies with no special case: the codes stay, and the
// entries are flagged.
//
// Record 7/22 long-string missing values land on the SAME variable.missing
// slot a record type 2 specification lands on (E3-S4's binding pass does
// that deliberately, because the two mean the same thing and 7/22 exists
// only because a record type 2 cannot carry a missing value for a string
// wider than eight bytes). They are therefore covered by the same call
// here, with no branch — see TestMissing_LongStringCodesAreFlagged.
//
// # Confirming E4-S2's classification rule
//
// missing.go's labelsCodeTheVariable decides whether a numeric variable's
// value labels CODE it (categorical) or merely annotate its missing states
// (numeric plus a sibling). E4-S2 introduced the rule and flagged it for
// this story to confirm or override. It is CONFIRMED, unchanged, and the
// reason is visible from this side:
//
//   - `INCOME: 97=Refused, 98=Don't know, 99=N/A`, all three declared
//     missing, is a continuous measurement. Classifying it categorical
//     would build one dictionary entry per distinct income — the
//     free-text pathology — and would put its three real reasons where
//     no reason vocabulary can reach them. The numeric arm serves it.
//   - `Q1: 1=Yes, 2=No, 9=Refused` is a coded question. Two of its labels
//     name ordinary answers, so it is categorical, and 9 is one entry
//     among three with this file's flag on it.
//
// The rule is exactly the line between "the sibling is the only home for
// the reason" and "the dictionary already is". Overriding it would send
// one of those two cases to the wrong arm.
//
// # Discoverability: the flag, and one summary
//
// A flag nobody sees is not a resolution. An analyst computing a
// percentage base over Q1 needs to exclude 9 from the denominator, and
// needs to know that 9 is the one to exclude — the cohort's dictionary
// holds SPSS codes, not labels, so "Refused" appears nowhere in it.
//
// Two surfaces, and they answer different questions:
//
//   - The SIDECAR flag (Category.Missing) is the persistent, per-entry,
//     machine-readable record. It survives the import, it is what an
//     export and any downstream tooling read, and it is exact.
//   - PULSE_SPSS_CATEGORICAL_USER_MISSING is the IMPORT-TIME signal. It
//     rides Reader.Warnings, so it reaches ImportReport.SourceWarnings,
//     the managed-import Result, and the `--json` envelope's warnings
//     array — the surfaces a person at a CLI or an agent calling
//     pulse_import actually sees. "The information is in a JSON file next
//     to the cohort" is not discoverable by anyone who does not already
//     know to look.
//
// It is ONE diagnostic for the whole file, not one per variable. Per
// variable is the house style elsewhere in this package, and it is wrong
// here for the same reason a sibling per variable is wrong: on an
// all-categorical survey it would emit hundreds of lines and bury the
// signal. The prose names the first few variables and their codes, and
// Details carries every one of them under
// errors.DetailSPSSMissingCategories as a field name -> flagged entries
// map, so a machine consumer loses nothing to the prose cap.
//
// The exclusion an analyst writes is FILTER_EXCLUDE over the DICTIONARY
// ENTRY, which is the SPSS code:
//
//	{"type": "FILTER_EXCLUDE", "field": "Q1", "values": ["9"]}
//
// not `"values": ["Refused"]`. processing's exclude filterer resolves
// each value through the field's dictionary and returns PROCESSING_CONFIG
// for one it cannot find, so a label typed there fails loudly rather than
// filtering nothing — see TestSPSS_CategoricalMissingCodesAreExcludable.

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// missingCategorySummaryCap is the number of variables the summary
// warning names in its prose before it says "and N more". The full set is
// always in Details, so the cap costs a machine consumer nothing; it
// exists so a 200-variable survey produces one readable line rather than
// one unreadable one.
const missingCategorySummaryCap = 5

// missingCategoryEntryCap is the same idea one level down: the number of
// flagged dictionary entries named per variable in the prose.
const missingCategoryEntryCap = 4

// missingCategories is one categorical column's flagged entries, in
// dictionary ID order.
type missingCategories struct {
	// field is the Pulse field name of the column.
	field string

	// values are the flagged DICTIONARY ENTRY texts — the SPSS codes as
	// the cohort stores them, which is what an exclusion filter takes
	// verbatim. Deduplicated: two categoryEntry rows sharing one id
	// contribute one value.
	values []string
}

// markMissingCategories flags every dictionary entry of a resolved
// categorical column whose SPSS value is one of the variable's
// user-missing codes, and returns the flagged entry texts in dictionary
// ID order.
//
// It runs AFTER resolveCategories, because it flags entries rather than
// creating them: the categorical mapping is deliberately untouched by the
// missing specification, so that a refusal code is an ordinary value with
// an ordinary ID whether or not anything ever reads this flag.
//
// It returns nil when the variable declares no user-missing values, which
// is the overwhelmingly common case and the one that must stay free.
func (m *mapping) markMissingCategories(col *columnMapping, v variable) []string {
	if v.missing.count() == 0 || len(col.categories) == 0 {
		return nil
	}

	match := m.categoricalMissingMatcher(v)
	if match == nil {
		return nil
	}

	var values []string
	seen := make(map[uint32]bool, len(col.categories))
	for i := range col.categories {
		c := &col.categories[i]
		if !match(c) {
			continue
		}
		c.missing = true
		// One value per ID. Two entries share an ID only when two
		// distinct source values collapsed to one dictionary text
		// (PULSE_SPSS_VALUE_COLLISION), and an exclusion filter names
		// that text once.
		if !seen[c.id] {
			seen[c.id] = true
			values = append(values, c.value)
		}
	}
	return values
}

// categoricalMissingMatcher compiles the variable's user-missing
// specification to a predicate over a resolved dictionary entry, or nil
// when it declares nothing this arm acts on.
//
// The numeric and string halves are different tests because the format
// stores them differently, not because they mean different things: a
// numeric specification is up to three doubles or a lo..hi range, and a
// string one is up to three fixed-width byte slots with no range form.
func (m *mapping) categoricalMissingMatcher(v variable) func(*categoryEntry) bool {
	if !v.isString() {
		// compileMissingTest already carries the range / discrete /
		// range-plus-one shape discrimination and the LOWEST-bound trap.
		// Reusing it is what keeps the categorical flag and the numeric
		// sibling agreeing about what "user-missing" means — two
		// independent readings of the same signed count field would
		// eventually disagree.
		t := compileMissingTest(v)
		if t == nil {
			return nil
		}
		return func(c *categoryEntry) bool { return c.numeric && t.match(c.code) }
	}

	keys := m.stringMissingKeys(v)
	if len(keys) == 0 {
		return nil
	}
	return func(c *categoryEntry) bool { return !c.numeric && keys[c.value] }
}

// stringMissingKeys renders a string variable's declared missing values
// into the canonical dictionary keys they would occupy.
//
// The specification's slots are RAW SOURCE BYTES, already right-trimmed
// of their 0x20 padding by the parser exactly as a datum of the same
// variable is. They are decoded here through the same charset decoder the
// data section goes through, and keyed with the same dictKey the
// dictionary is keyed with, because a key produced any other way could
// not compare equal to the entry it names.
//
// A slot that will not decode is SKIPPED rather than raised. It cannot
// occur for a file this reader accepted — a datum of those same bytes
// would already have failed the import with PULSE_SPSS_CHARSET_INVALID —
// and if it somehow did, the raw eight-byte slots are on the sidecar
// verbatim either way. Failing an otherwise-good import to refuse a
// display flag would be the wrong trade.
func (m *mapping) stringMissingKeys(v variable) map[string]bool {
	spec := v.missing
	if len(spec.text) == 0 {
		return nil
	}
	out := make(map[string]bool, len(spec.text))
	for _, raw := range spec.text {
		text := raw
		if m.plan != nil && m.plan.cs != nil {
			decoded, at := m.plan.cs.decodeString(raw)
			if at >= 0 {
				continue
			}
			text = decoded
		}
		key := dictKey(text)
		if rendersAsNull(key) {
			// The value imports as null before any dictionary lookup, so
			// it has no entry to flag. resolveCategories already warned
			// PULSE_SPSS_NULL_TOKEN_COLLISION for it.
			continue
		}
		out[key] = true
	}
	return out
}

// warnMissingCategories raises the one file-level informational summary.
//
// It is a no-op when nothing was flagged, which keeps a cohort with no
// categorical user-missing codes — every file imported before this story
// — warning-identical to what it was.
func (m *mapping) warnMissingCategories(found []missingCategories) {
	if len(found) == 0 {
		return
	}

	// Deterministic: the mapping walks variables in file order, so
	// `found` is already in cohort order and the prose is stable. The
	// Details map is keyed by field name and encoding/json sorts map keys,
	// so the document is stable too.
	detail := make(map[string][]string, len(found))
	for _, f := range found {
		detail[f.field] = f.values
	}

	var b strings.Builder
	b.WriteString("spss: ")
	b.WriteString(strconv.Itoa(len(found)))
	if len(found) == 1 {
		b.WriteString(" categorical column declares user-missing codes, kept as ordinary dictionary entries: ")
	} else {
		b.WriteString(" categorical columns declare user-missing codes, kept as ordinary dictionary entries: ")
	}
	b.WriteString(summariseMissingCategories(found))
	b.WriteString(". Nothing was lost and no sibling column was generated — for a categorical the value IS the label, so the code is already in the column's own dictionary. But it is an ordinary value, so every aggregation counts it")
	b.WriteString("; exclude it from an analysis base with FILTER_EXCLUDE on the field, naming the dictionary ENTRY (the SPSS code, not its label). The sidecar flags the same entries under variables[].categories[].missing")

	ce := errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CATEGORICAL_USER_MISSING, b.String(),
		map[string]any{
			errors.DetailSPSSMissingCategories: detail,
			errors.DetailSPSSDistinct:          len(found),
		})
	m.warnings = append(m.warnings, ce)
}

// summariseMissingCategories renders the capped prose list.
func summariseMissingCategories(found []missingCategories) string {
	var parts []string
	for i, f := range found {
		if i == missingCategorySummaryCap {
			parts = append(parts, "and "+strconv.Itoa(len(found)-i)+" more")
			break
		}
		parts = append(parts, strconv.Quote(f.field)+" ("+joinCapped(f.values, missingCategoryEntryCap)+")")
	}
	return strings.Join(parts, ", ")
}

// joinCapped renders at most limit quoted values, then "and N more".
func joinCapped(values []string, limit int) string {
	if len(values) == 0 {
		// Unreachable: a summary entry exists only because at least one
		// dictionary entry was flagged. Kept so the renderer is total.
		return "no entries"
	}
	var parts []string
	for i, v := range values {
		if i == limit {
			parts = append(parts, "and "+strconv.Itoa(len(values)-i)+" more")
			break
		}
		parts = append(parts, strconv.Quote(v))
	}
	return strings.Join(parts, ", ")
}
