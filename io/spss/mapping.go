package spss

// The SPSS dictionary → Pulse schema mapping.
//
// This is where the effort's governing principle becomes code: where a
// mapping would silently discard information it preserves it or says so
// out loud, and it never degrades quietly.
//
// # What decides a column's type
//
// SPSS has exactly two native types — an IEEE 754 double and a
// fixed-width byte string. Everything else (dates, times, currency,
// booleans, categorical codes) is a double wearing a print format. So the
// mapping reads three inputs and nothing else: the variable's declared
// type, its PRINT FORMAT type code, and whether a record type 3/4 value
// label set is bound to it. The measurement level (record 7/11) does NOT
// select a type — see "Measure level" below.
//
//	SPSS                                     Pulse
//	--------------------------------------   --------------------------
//	string, any width                        categorical_u8/u16/u32
//	numeric, value-labelled                  categorical_u8/u16/u32
//	numeric, DATE/ADATE/EDATE/SDATE/JDATE    date       (see widening)
//	numeric, DATETIME                        datetime   (see precision)
//	numeric, TIME/DTIME                      f64 seconds
//	numeric, anything else                   f64
//
// Numerics map to `f64` and never to a narrower integer type and never to
// `decimal128`. Both were considered and rejected: range-probing a column
// the source has already declared double-precision is a sample-based
// guess that an out-of-sample fractional value breaks, and a 128-bit
// decimal manufactures precision the source never had. `f64` IS the
// source's own representation.
//
// # Why the dictionary holds CODES, not labels
//
// A Pulse categorical dictionary entry is text, and its position in the
// dictionary is the on-wire ID. The entry text this mapping writes is the
// SPSS VALUE's canonical rendering — `"5"` for the numeric code 5, the
// trimmed bytes for a string — and never the value label.
//
// That is the fidelity choice. Pulse IDs are positional (0, 1, 2, …)
// while SPSS codes are arbitrary (1, 2, 5, 9 or -1, 0, 1); whichever of
// the two the dictionary does not hold has to be recovered from the
// sidecar. Codes are the load-bearing half: downstream SPSS syntax says
// `IF q1 EQ 5`, an export that invented codes would silently break it,
// and a cohort that has lost its sidecar can still be read correctly if
// its dictionary holds codes. Labels are display text, and Pulse already
// has an output-time mechanism for display text (LabelTables /
// LabelBinding) that this mapping's recorded triple can populate.
//
// It also keeps the reader honest: the cell text ReadRows emits for a
// categorical column is unchanged from the least-opinionated rendering
// E2-S4 wrote, so the dictionary entry and the cell always agree by
// construction rather than by a second rendering rule that could drift.
//
// # The code ↔ label ↔ ID triple
//
// Every categorical column records one categoryEntry per dictionary
// entry, carrying the SPSS value, the SPSS label (when the file declared
// one) and the Pulse ID. E4-S1's sidecar reads it off this mapping. Two
// shapes matter and are both representable:
//
//   - A declared label with no matching datum still occupies its ID, so
//     the file's own code ordering survives an import that never observed
//     the code.
//   - A datum with no declared label is APPENDED in first-seen order —
//     an unlabelled numeric code is perfectly legal SPSS — and its entry
//     carries a code and an ID but no label. `labelled` is what says so.
//
// # Ordering is the contract
//
// Dictionary entry order IS the encoding, so it is built deterministically
// from the file: every declared value-label key first, in record 3 order
// across the sets bound to the variable in record order, then every value
// the data section carried that no label declared, in first-seen order.
//
// # Widening and downcasting, both loud
//
// `date` is an UNSIGNED epoch-day count, so it cannot express an instant
// before 1970-01-01 — and SPSS files carry birth dates. It is also day
// resolution, so a time of day on a DATE-formatted variable would vanish.
// Rather than write a wrapped or truncated value, a day-resolution column
// holding either widens to `datetime` (lossless, and the date-family
// groupers day-truncate it) with PULSE_SPSS_DATE_WIDENED.
//
// Below that, `datetime` is second resolution. A temporal column holding
// a fractional second, a non-finite double or a second count outside
// int64 drops to `f64` raw SPSS seconds with PULSE_SPSS_TEMPORAL_PRECISION
// — lossless, since the print format is retained for export.
//
// # Measure level
//
// Record 7/11's measurement level feeds the smart-default HINTS
// (nominal/ordinal → AGG_FREQUENCY + GROUP_CATEGORY, scale → AGG_SUM +
// GROUP_RANGE) recorded on each column for the sidecar. It deliberately
// does not select a field type: it is optional metadata that plenty of
// files omit, and letting it steer typing would make two otherwise
// identical files map differently. Where the level disagrees with what
// Pulse will actually default from the mapped type — a `scale` variable
// that carries value labels and therefore maps categorical — the
// disagreement is reported as PULSE_SPSS_MEASURE_LEVEL_MISMATCH rather
// than resolved by guessing.

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// SPSS print/write format TYPE codes, from the PSPP output-format table.
// Only the codes this mapping dispatches on are named; every other code
// falls through to the plain-numeric arm, which is the lossless default.
const (
	fmtDATE     uint8 = 20 // dd-mmm-yyyy
	fmtTIME     uint8 = 21 // hh:mm:ss.s — a DURATION, not an instant
	fmtDATETIME uint8 = 22 // dd-mmm-yyyy hh:mm:ss.s
	fmtADATE    uint8 = 23 // mm/dd/yyyy
	fmtJDATE    uint8 = 24 // yyyyddd
	fmtDTIME    uint8 = 25 // dd hh:mm:ss.s — a DURATION, not an instant
	fmtEDATE    uint8 = 38 // dd.mm.yyyy
	fmtSDATE    uint8 = 39 // yyyy/mm/dd
)

// spssEpochOffsetSeconds is the gap between the SPSS epoch (1582-10-14
// 00:00:00 UTC, the day before the Gregorian calendar was adopted) and
// the Unix epoch. Every SPSS temporal datum counts seconds from the
// former and every Pulse temporal value counts from the latter, so this
// constant is the entire conversion.
const spssEpochOffsetSeconds int64 = 12219379200

// dateLayout is the text form a `date`-mapped column renders to. It is
// encoding.DateFormats[0], so encoding.ParseDate — which io/import.go's
// convertValue calls — reads it back to the exact epoch-day this mapping
// intended.
const dateLayout = "2006-01-02"

// defaultCardinalityWarnFraction is the share of the case count a
// categorical column's distinct-value count must exceed before the
// mapping raises PULSE_SPSS_CARDINALITY_HIGH. Half the rows distinct is
// already far past any coded question and well into free text.
const defaultCardinalityWarnFraction = 0.5

// cardinalityWarnMinCases is the case-count floor below which the
// cardinality ratio means nothing: in a four-case fixture three distinct
// values is 75% and is not schema bloat. Below the floor the check is
// skipped entirely rather than scaled, because any scaling rule would be
// a second invented threshold.
const cardinalityWarnMinCases = 100

// columnKind is the mapping decision for one variable: which family of
// Pulse type it lands in, and hence how ReadRows renders its cells.
type columnKind int

const (
	// kindNumeric is a plain double: f64, rendered as the shortest
	// decimal string that round-trips.
	kindNumeric columnKind = iota
	// kindDuration is a TIME or DTIME variable. It is f64 like
	// kindNumeric and renders identically — the distinction exists so
	// the sidecar can tell a duration from a plain measurement, and so
	// a duration is never mistaken for an instant.
	kindDuration
	// kindDate is an epoch-day `date` column.
	kindDate
	// kindDateTime is an epoch-seconds `datetime` column.
	kindDateTime
	// kindCategorical is a dictionary-bearing column: every string
	// variable, and every value-labelled numeric.
	kindCategorical
)

func (k columnKind) String() string {
	switch k {
	case kindNumeric:
		return "numeric"
	case kindDuration:
		return "duration"
	case kindDate:
		return "date"
	case kindDateTime:
		return "datetime"
	case kindCategorical:
		return "categorical"
	default:
		return "columnKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// categoryEntry is one dictionary entry's provenance: the SPSS value it
// came from, the label SPSS attached to that value, and the Pulse
// dictionary ID the entry occupies.
//
// This is the triple E4-S1's sidecar persists and E5's export reads back.
// Two of its flags exist because neither half of the triple is
// guaranteed: `labelled` is false for a value the data carried but no
// record type 3 declared, and `observed` is false for a declared label no
// case ever used. An entry always has an id and a value; everything else
// is optional by construction.
//
// Entries may SHARE an id. That happens only when two distinct source
// values resolve to the same dictionary text — see
// PULSE_SPSS_VALUE_COLLISION — and it is represented rather than
// flattened so the ambiguity is visible to whatever has to re-emit a
// value later.
type categoryEntry struct {
	// id is the Pulse dictionary ID: the entry's position, and the value
	// stored on the wire.
	id uint32

	// value is the dictionary entry text at position id — the SPSS
	// value's canonical rendering, never the label.
	value string

	// numeric reports whether the source variable is numeric, and hence
	// whether code or text carries the SPSS value.
	numeric bool

	// code is the SPSS numeric value. Meaningful only when numeric.
	code float64

	// text is the SPSS string value with its declared-width padding
	// removed, exactly as the data section yields it. Meaningful only
	// when numeric is false. It can differ from value, which is
	// additionally trimmed of leading space to match what the shared
	// import path looks up.
	text string

	// label is the record type 3 value label.
	label string

	// labelled reports whether the file declared a label for this value.
	// False for an entry appended from the data section.
	labelled bool

	// observed reports whether at least one case carried this value.
	// False for a declared label nothing used.
	observed bool

	// missing reports that this entry's SPSS value is one of the
	// variable's USER-MISSING codes. See missing_categorical.go for why
	// a categorical column flags the code in place rather than
	// duplicating it into a `<var>_missing` sibling the way the numeric
	// arm does.
	missing bool
}

// columnMapping is one SPSS variable's resolved Pulse column.
type columnMapping struct {
	// name is the Pulse field name: the record 7/13 long name where the
	// file declares one, else the 8-byte short name.
	name string

	// kind is the mapping family, which also selects the cell rendering.
	kind columnKind

	// fieldType is the resolved .pulse type.
	fieldType encoding.FieldType

	// nullable is authoritative, not a guess: the whole data section was
	// scanned, so it is true exactly when some case carried a value the
	// import path reads as null (a system-missing numeric, or a string
	// that is blank or is one of the null sentinel tokens).
	nullable bool

	// description is the SPSS variable label, which becomes the Pulse
	// field description.
	description string

	// declaredWidth is the SPSS declared byte width of a string
	// variable, 0 for a numeric. Trailing spaces are trimmed on read, so
	// this is what an export needs to re-pad the value to the width the
	// source dictionary declared.
	//
	// For a record 7/14 very long string it is the LOGICAL total — 600
	// for a 600-byte string — never the 255 any one physical segment
	// declares. The per-segment widths live on vls below, because the two
	// answer different questions: declaredWidth says how far to pad the
	// value, vls says how to cut the padded result back into the physical
	// variables the source had.
	declaredWidth int

	// vls is the record 7/14 physical segmentation, non-nil only for a
	// string wider than 255 bytes. It is retained rather than discarded
	// after reassembly so a write path can reproduce the source's own
	// physical layout: how many variables, their widths, their names.
	vls *vlsLayout

	// printFormat and writeFormat are the SPSS output formats, carried
	// verbatim. They are what lets an export reconstruct the display
	// format, and for a TIME / DTIME / precision-downcast column they
	// are the only record of what the raw seconds mean.
	printFormat format
	writeFormat format

	// measure is the record 7/11 measurement level, or measureUnset.
	measure measureLevel

	// defaultAgg and defaultGroup are the smart-default hints the
	// measurement level implies, falling back to the mapped field type's
	// own defaults when the file declares no level. An empty aggregator
	// means "no default applies", matching the date family.
	defaultAgg   types.AggregationType
	defaultGroup types.GroupType

	// values are the dictionary entry texts in ID order. Empty for a
	// non-categorical column.
	values []string

	// categories is the code ↔ label ↔ ID triple, one entry per source
	// value. Empty for a non-categorical column.
	categories []categoryEntry

	// missing is the variable's user-missing specification compiled to a
	// predicate, nil when it declares none this mapping acts on. Non-nil
	// ONLY for the non-dictionary-bearing numeric arm — a categorical
	// column's missing codes stay in its own dictionary.
	//
	// It is independent of the missing MODE: a user-missing datum is a
	// null in the analytic column under both, because the arithmetic is
	// wrong either way if a refusal code is summed. The mode decides only
	// whether the REASON is preserved beside it.
	missing *missingTest

	// sibling is the generated `<var>_missing` reason column, nil when
	// the column gets none — no missing specification, a categorical
	// column, or MissingNull.
	sibling *missingSibling
}

// mapping is the whole dictionary resolved against the whole data
// section: one columnMapping per variable, plus the case geometry
// ReadRows decodes with.
type mapping struct {
	cols []columnMapping

	// out is the cohort's COLUMN LAYOUT: one entry per emitted Pulse
	// field, in field order. It is not one entry per SPSS variable —
	// a variable carrying user-missing values contributes two, itself
	// and its generated reason sibling.
	//
	// Everything that addresses a cohort column addresses it through
	// here: ReadHeader's names, schema()'s fields and decodeCase's row
	// slots. A second, independent derivation of the layout would put a
	// cell under the wrong field name the first time the two disagreed.
	out []outputSlot

	// plan carries the per-case geometry with each column's resolved
	// kind applied, so decoding and mapping can never disagree about
	// how a cell renders.
	plan *dataPlan

	// cases is the number of whole cases the scan walked.
	cases int

	// body is the data section resolved to its flat uncompressed form:
	// cases * plan.stride bytes, starting at element zero of case zero.
	// For an uncompressed file it aliases the file bytes; for a
	// compressed one it is the expansion decodeBytecode produced. Holding
	// it on the mapping is what keeps the compression flag from leaking
	// past readCaseData — ReadRows addresses a case by stride from index
	// zero either way.
	body []byte

	// charset is the file's character encoding declaration and the
	// decoder built from it, carried through from the dictionary.
	//
	// It is on the mapping and not only on the dictionary because the
	// write path consumes the mapping: re-encoding a column's values
	// needs the same charset the source declared, next to the same
	// declaredWidth the source declared, and separating the two would
	// invite an export that re-pads a UTF-8 string to a byte width
	// measured in another encoding.
	charset charsetInfo

	// missingCategories accumulates, in cohort order, every CATEGORICAL
	// column whose dictionary carries user-missing codes, with the entry
	// texts that were flagged. It feeds the one file-level
	// PULSE_SPSS_CATEGORICAL_USER_MISSING summary — see
	// missing_categorical.go for why the diagnostic is per file and not
	// per variable.
	missingCategories []missingCategories

	// warnings are the non-fatal mapping diagnostics. They are built
	// once with the mapping and never re-raised, because the mapping is
	// memoised for the life of the reader.
	warnings []*errors.CodedError
}

// decodeLabelKey decodes a short-string value label's VALUE slot.
//
// The key is a datum, not metadata: it is the same bytes a case of the same
// variable would carry, so it is trimmed to the declared byte width and
// stripped of its 0x20 padding before decoding, exactly as
// dataPlan.decodeStringDatum does. Doing it in the other order would leave a
// label key unable to compare equal to the datum it names.
func (m *mapping) decodeLabelKey(v variable, l valueLabel) (string, *errors.CodedError) {
	raw := l.text(v.width)
	if m.plan == nil || m.plan.cs == nil {
		return raw, nil
	}
	text, at := m.plan.cs.decodeString(raw)
	if at >= 0 {
		return "", charsetInvalid(m.plan.cs,
			"a value-label key of variable "+strconv.Quote(v.fieldName()),
			v.fieldName(), []byte(raw), at)
	}
	return text, nil
}

// mappingOptions are the tunables of the mapping pass.
type mappingOptions struct {
	// cardinalityWarnFraction is the share of the case count a
	// categorical column's distinct count must exceed to warn. A value
	// above 1 disables the check.
	cardinalityWarnFraction float64

	// missingMode selects how numeric USER-missing values are
	// represented. See MissingMode; the zero value is MissingAuto, the
	// fidelity-preserving split.
	missingMode MissingMode
}

func defaultMappingOptions() mappingOptions {
	return mappingOptions{cardinalityWarnFraction: defaultCardinalityWarnFraction}
}

// ---------------------------------------------------------------------------
// Building the mapping
// ---------------------------------------------------------------------------

// buildMapping resolves a parsed dictionary and its data section into a
// Pulse column mapping.
//
// It walks every case. That is not an optimisation target and it is not
// avoidable: the categorical widths are sized by the values actually
// present, the declared nullability has to be a fact rather than a
// sample, and the temporal widening / precision rules are statements
// about the whole column. A bounded sample would turn each of those into
// the guess this reader exists to replace.
func buildMapping(d *dictionary, data []byte, opts mappingOptions) (*mapping, error) {
	plan, err := buildDataPlan(d)
	if err != nil {
		return nil, err
	}
	body, cases, err := readCaseData(d, data, plan)
	if err != nil {
		return nil, err
	}

	labels := valueLabelsByVariable(d)
	kinds := make([]columnKind, len(d.vars))
	for i, v := range d.vars {
		kinds[i] = classify(v, labelsCodeTheVariable(v, labels[i], d.byteOrder, d.sysmis))
	}

	// The cohort's column layout is decided from the dictionary alone,
	// before a single case is read, so ReadHeader can answer without a
	// scan and cannot drift from what the schema declares. It is also
	// where a generated sibling name colliding with a real variable is
	// refused, which is a whole-file fault and should not wait until
	// after the data has been walked.
	out, err := planOutputs(d, opts)
	if err != nil {
		return nil, err
	}

	// The user-missing predicate has to be in force DURING the scan, not
	// merely at decode time: the scan is what establishes nullability,
	// the categorical widths and the temporal widening rules, and a
	// refusal code counted as an ordinary datum corrupts all three.
	for _, slot := range out {
		if slot.sibling || kinds[slot.col] == kindCategorical {
			continue
		}
		plan.cols[slot.col].missing = compileMissingTest(d.vars[slot.col])
	}

	stats, err := scanCases(plan, kinds, body, 0, cases)
	if err != nil {
		return nil, err
	}

	m := &mapping{plan: plan, cases: cases, body: body, charset: d.charset,
		out: out, cols: make([]columnMapping, len(d.vars))}
	for i, v := range d.vars {
		col, err := m.resolveColumn(i, v, kinds[i], labels[i], &stats[i], opts)
		if err != nil {
			return nil, err
		}
		m.cols[i] = col
		// The scan ran against the provisional kind; a temporal column
		// may have widened or dropped, so the decode plan takes the
		// RESOLVED kind — that is what keeps a rendered cell and its
		// declared field type in agreement.
		plan.cols[i].kind = col.kind
	}

	// One informational summary for the whole file, raised after every
	// column has resolved so it can be a single diagnostic rather than
	// one per variable.
	m.warnMissingCategories(m.missingCategories)

	// Sibling resolution runs after every column has settled, because a
	// sibling is named from its source column's FINAL Pulse field name.
	for _, slot := range out {
		if !slot.sibling {
			continue
		}
		sib, err := m.buildMissingSibling(&m.cols[slot.col], d.vars[slot.col],
			labels[slot.col], &stats[slot.col])
		if err != nil {
			return nil, err
		}
		m.cols[slot.col].sibling = sib
		plan.cols[slot.col].sibling = sib
	}
	plan.out = out
	return m, nil
}

// labelsCodeTheVariable reports whether a numeric variable's value labels
// CODE the variable — the categorical case — or merely annotate its
// missing states.
//
// The distinction is the difference between two very different files.
// `Q1: 1 = Yes, 2 = No, 9 = Refused` with a missing specification naming
// 9 is a coded question: two of its three labels are ordinary answers,
// so it is categorical and 9 is one dictionary entry among them.
// `INCOME: 97 = Refused, 98 = Don't know, 99 = N/A` with a missing
// specification naming all three is a CONTINUOUS variable whose only
// labels sit on its missing states — the near-universal shape in real
// survey files, and the reason this test exists.
//
// Treating the second as categorical would build a dictionary entry per
// distinct income, which is the free-text pathology the mapping warns
// about, applied to a column that is plainly a measurement. It also
// makes the labels unreachable as REASONS, which is what a
// `<var>_missing` sibling is for.
//
// So the rule is: labels code the variable when at least one of them
// names a value that is neither user-missing nor the system-missing
// sentinel. A label on the sentinel is skipped for the same reason
// resolveCategories skips it — no case can carry the sentinel as a datum,
// so the label names nothing and is not evidence of anything.
//
// It is independent of the missing MODE. The mode decides whether the
// reason is preserved beside the null, never what the column IS: two
// imports of one file must not disagree about a field's type.
func labelsCodeTheVariable(v variable, labels []valueLabel, bo binary.ByteOrder, sysmis float64) bool {
	if len(labels) == 0 {
		return false
	}
	if v.isString() {
		return true
	}
	t := compileMissingTest(v)
	if t == nil {
		return true
	}
	for _, l := range labels {
		code := l.numeric(bo)
		if math.Float64bits(code) == math.Float64bits(sysmis) || code == sysmis {
			continue
		}
		if !t.match(code) {
			return true
		}
	}
	return false
}

// classify picks a variable's kind from the dictionary alone — before any
// datum has been seen. A temporal print format outranks a value-label
// set: a labelled date is pathological, and preserving the instant is
// worth more than a category. The labels are still recorded on the
// column, so nothing is lost either way.
//
// labelled is labelsCodeTheVariable's answer, not merely "the file bound
// a label set to this variable": a set that names only user-missing codes
// annotates the missing states and does not make the variable
// categorical.
func classify(v variable, labelled bool) columnKind {
	if v.isString() {
		return kindCategorical
	}
	switch v.print.code {
	case fmtDATE, fmtADATE, fmtEDATE, fmtSDATE, fmtJDATE:
		return kindDate
	case fmtDATETIME:
		return kindDateTime
	case fmtTIME, fmtDTIME:
		return kindDuration
	}
	if labelled {
		return kindCategorical
	}
	return kindNumeric
}

// valueLabelsByVariable projects the record 3/4 sets onto the variables
// they bind to, preserving file order on both axes: sets in record order,
// and labels in the order the record listed them. That order is what
// becomes the dictionary ID order, so it is the contract.
func valueLabelsByVariable(d *dictionary) [][]valueLabel {
	out := make([][]valueLabel, len(d.vars))
	byIndex := make(map[int32]int, len(d.vars))
	for i, v := range d.vars {
		byIndex[v.index] = i
	}
	for _, set := range d.valueLabels {
		for _, idx := range set.varIndices {
			i, ok := byIndex[idx]
			if !ok {
				// A record type 4 naming a continuation element or an
				// index outside the dictionary. The parser already
				// warned; there is no variable to attach labels to.
				continue
			}
			out[i] = append(out[i], set.labels...)
		}
	}
	return out
}

// columnStats is what one scan pass accumulates for one column.
type columnStats struct {
	// sawNull records that at least one case carried a value the import
	// path reads as null.
	sawNull bool

	// nullTokenValue is a non-blank value that nonetheless reads as
	// null ("NA", "N/A", "NULL"). Empty when none was seen. An all-blank
	// string does not set it: blank IS SPSS's missing-string convention,
	// and warning about it would warn about almost every string file.
	nullTokenValue string

	// keys are the distinct dictionary keys in first-seen order, and
	// raws / codes are the source forms that produced them.
	keys  []string
	raws  []string
	codes []float64
	index map[string]int

	// inexact records a temporal value the target type cannot hold
	// exactly: a fractional second, a non-finite double, or a second
	// count outside int64.
	inexact bool

	// subDay records a day-resolution column carrying a time of day.
	subDay bool

	// preEpoch records a temporal value before 1970-01-01, which the
	// unsigned epoch-day `date` representation cannot express.
	preEpoch bool

	// collided records that two distinct source values resolved to one
	// dictionary key.
	collided bool

	// extras are the LOSING halves of those collisions: a source value
	// that found its key already taken by a different source value. They
	// are kept rather than dropped so the triple can record both values
	// against the shared id — an export that has to pick between them
	// needs to see that there was a choice.
	extras []extraValue

	// sawSysmis records that at least one case carried the
	// system-missing sentinel. It is held apart from sawNull, which is
	// true for every state the import path reads as null — a
	// user-missing datum and a null sentinel token set that one too, and
	// a reason vocabulary has to be able to say whether SYSMIS itself
	// occurred.
	sawSysmis bool

	// missingValues are the distinct USER-missing values the data
	// carried, in first-seen order, and missingSeen indexes them by
	// canonical rendering.
	//
	// Distinctness is by rendering rather than by float64 because that is
	// how the rest of this package defines it, so two bit patterns that
	// render alike are one reason here too. Only OBSERVED values are
	// collected: a range specification is not a finite vocabulary and
	// enumerating one would produce a dictionary of every double between
	// the bounds.
	missingValues []float64
	missingSeen   map[string]bool
}

// observeMissing records one user-missing datum, keeping the distinct
// values in first-seen order.
func (s *columnStats) observeMissing(v float64) {
	raw := formatNumericValue(v)
	if s.missingSeen == nil {
		s.missingSeen = make(map[string]bool)
	}
	if s.missingSeen[raw] {
		return
	}
	s.missingSeen[raw] = true
	s.missingValues = append(s.missingValues, v)
}

// extraValue is one source value that collided with an entry already
// present under the same dictionary key.
type extraValue struct {
	key  string
	raw  string
	code float64
}

// observe records one categorical datum, returning whether the key was
// already present under a DIFFERENT source form — the collision that
// makes the value-to-ID mapping non-injective.
func (s *columnStats) observe(key, raw string, code float64) bool {
	if s.index == nil {
		s.index = make(map[string]int)
	}
	if at, ok := s.index[key]; ok {
		if s.raws[at] == raw {
			return false
		}
		for _, e := range s.extras {
			if e.raw == raw {
				return true
			}
		}
		s.extras = append(s.extras, extraValue{key: key, raw: raw, code: code})
		return true
	}
	s.index[key] = len(s.keys)
	s.keys = append(s.keys, key)
	s.raws = append(s.raws, raw)
	s.codes = append(s.codes, code)
	return false
}

// scanCases walks every case once, accumulating per column exactly what
// the type resolution needs and nothing more.
//
// It is also where a string datum is first DECODED out of the file's
// declared character encoding, and therefore where an undecodable one is
// first seen. That placement is deliberate: this pass already visits every
// case, so the check costs nothing extra, and it means an import fails
// before it has produced a schema rather than part-way through the rows.
func scanCases(plan *dataPlan, kinds []columnKind, data []byte, start, cases int) ([]columnStats, error) {
	stats := make([]columnStats, len(plan.cols))

	for n := 0; n < cases; n++ {
		c := data[start+n*plan.stride : start+(n+1)*plan.stride]
		for i := range plan.cols {
			col := &plan.cols[i]
			seg := c[col.offset : col.offset+col.span]
			st := &stats[i]

			switch kinds[i] {
			case kindCategorical:
				var raw string
				var code float64
				if col.width == 0 {
					bits := plan.bo.Uint64(seg)
					if plan.isSysmis(bits) {
						st.sawNull = true
						st.sawSysmis = true
						continue
					}
					code = math.Float64frombits(bits)
					raw = formatNumericValue(code)
				} else {
					// stringBytes, not seg[:col.width]: a record 7/14
					// very long string is several physical variables
					// with padding between them, and the scan must see
					// the same reassembled value the decode will.
					text, err := plan.decodeStringDatum(col, plan.stringBytes(col, c))
					if err != nil {
						return nil, err
					}
					raw = text
				}
				key := dictKey(raw)
				if rendersAsNull(key) {
					st.sawNull = true
					if key != "" && st.nullTokenValue == "" {
						st.nullTokenValue = key
					}
					continue
				}
				if st.observe(key, raw, code) {
					st.collided = true
				}

			case kindDate, kindDateTime:
				bits := plan.bo.Uint64(seg)
				if plan.isSysmis(bits) {
					st.sawNull = true
					st.sawSysmis = true
					continue
				}
				value := math.Float64frombits(bits)
				if col.missing.match(value) {
					// A user-missing code on a temporal variable is a
					// REASON, not an instant. It has to be excluded here
					// and not merely at decode time: a refusal code of
					// 999 on a DATE column would otherwise read as an
					// instant three days after the SPSS epoch and widen
					// the whole column to datetime for being pre-1970.
					st.sawNull = true
					st.observeMissing(value)
					continue
				}
				sec, ok := spssSecondsExact(value)
				if !ok {
					st.inexact = true
					continue
				}
				if sec < 0 {
					st.preEpoch = true
				}
				if sec%encoding.SecondsPerDay != 0 {
					st.subDay = true
				}

			default: // kindNumeric, kindDuration
				bits := plan.bo.Uint64(seg)
				if plan.isSysmis(bits) {
					st.sawNull = true
					st.sawSysmis = true
					continue
				}
				// A user-missing datum is a null in the analytic column
				// under EVERY missing mode: summing a refusal code is
				// arithmetically wrong whether or not the reason is
				// preserved beside it. Nullability is therefore a fact
				// this pass establishes, exactly as it does for sysmis.
				if value := math.Float64frombits(bits); col.missing.match(value) {
					st.sawNull = true
					st.observeMissing(value)
				}
			}
		}
	}

	return stats, nil
}

// resolveColumn turns one variable plus its scan into a finished mapping.
func (m *mapping) resolveColumn(at int, v variable, kind columnKind,
	labels []valueLabel, st *columnStats, opts mappingOptions,
) (columnMapping, error) {
	col := columnMapping{
		name:          v.fieldName(),
		kind:          kind,
		nullable:      st.sawNull,
		description:   v.label,
		declaredWidth: v.width,
		printFormat:   v.print,
		writeFormat:   v.write,
		measure:       v.display.measure,
		vls:           v.vls,
	}
	if kind != kindCategorical {
		// Read back off the decode plan rather than recompiled, so the
		// predicate the scan ran under and the one the column records
		// are the same object by construction.
		col.missing = m.plan.cols[at].missing
	}

	if st.nullTokenValue != "" {
		m.warn(errors.PULSE_SPSS_NULL_TOKEN_COLLISION, v,
			"the value %q is one of the import pipeline's null sentinel tokens, so every case carrying it imports as null and its dictionary entry is unreachable",
			st.nullTokenValue)
	}

	switch kind {
	case kindDate:
		switch {
		case st.inexact:
			col.kind = kindNumeric
			col.fieldType = encoding.FieldTypeF64
			m.warnPrecision(v)
		case st.subDay || st.preEpoch:
			col.kind = kindDateTime
			col.fieldType = encoding.FieldTypeDateTime
			m.warnWidened(v, st)
		default:
			col.fieldType = encoding.FieldTypeDate
		}

	case kindDateTime:
		if st.inexact {
			col.kind = kindNumeric
			col.fieldType = encoding.FieldTypeF64
			m.warnPrecision(v)
			break
		}
		col.fieldType = encoding.FieldTypeDateTime

	case kindNumeric, kindDuration:
		col.fieldType = encoding.FieldTypeF64

	case kindCategorical:
		if err := m.resolveCategories(&col, v, labels, st, opts); err != nil {
			return columnMapping{}, err
		}
		// The missing specification does not shape the dictionary — a
		// refusal code is an ordinary entry with an ordinary ID — so the
		// flagging pass runs after it and only annotates. See
		// missing_categorical.go for why a categorical column flags in
		// place where a numeric one grows a sibling.
		if flagged := m.markMissingCategories(&col, v); len(flagged) > 0 {
			m.missingCategories = append(m.missingCategories,
				missingCategories{field: col.name, values: flagged})
		}
	}

	col.defaultAgg, col.defaultGroup = defaultHints(col.fieldType, col.measure)
	if col.measure == measureScale && col.fieldType.IsCategorical() {
		what := "carries value labels"
		if v.isString() {
			what = "is a string variable"
		}
		m.warn(errors.PULSE_SPSS_MEASURE_LEVEL_MISMATCH, v,
			"the variable declares measurement level scale but %s, so it maps to %s; its Pulse smart defaults will be %s / %s rather than the AGG_SUM / GROUP_RANGE the declared level implies",
			what, col.fieldType, types.AGG_FREQUENCY, types.GROUP_CATEGORY)
	}
	return col, nil
}

// resolveCategories builds the dictionary, the code ↔ label ↔ ID triple
// and the categorical width.
func (m *mapping) resolveCategories(col *columnMapping, v variable,
	labels []valueLabel, st *columnStats, opts mappingOptions,
) error {
	numeric := !v.isString()
	index := make(map[string]int, len(labels)+len(st.keys))

	// Declared value labels first, in file order — that ordering is what
	// preserves the source's own code sequence in the Pulse IDs.
	for _, l := range labels {
		var raw string
		var code float64
		if numeric {
			code = l.numeric(m.plan.bo)
			if m.plan.isSysmisValue(code) {
				// A label bound to the system-missing sentinel can
				// never match a datum; it would occupy an ID nothing
				// could ever reference.
				m.warn(errors.PULSE_SPSS_NULL_TOKEN_COLLISION, v,
					"the value label %q is declared on the system-missing sentinel, which no case can carry, so it contributes no dictionary entry",
					l.label)
				continue
			}
			raw = formatNumericValue(code)
		} else {
			text, err := m.decodeLabelKey(v, l)
			if err != nil {
				return err
			}
			raw = text
		}
		key := dictKey(raw)
		if rendersAsNull(key) {
			m.warn(errors.PULSE_SPSS_NULL_TOKEN_COLLISION, v,
				"the value label %q is declared on the value %q, which the import pipeline reads as null, so it contributes no dictionary entry",
				l.label, raw)
			continue
		}
		if addCategory(col, index, key, raw, code, numeric, l.label, true) {
			st.collided = true
		}
	}

	// Then every value the data carried that no label declared, in
	// first-seen order.
	for i, key := range st.keys {
		at, seen := index[key]
		if seen {
			col.categories[at].observed = true
			continue
		}
		if addCategory(col, index, key, st.raws[i], st.codes[i], numeric, "", false) {
			st.collided = true
		}
		col.categories[len(col.categories)-1].observed = true
	}

	// The losing half of every data-section collision, recorded against
	// the id its key already resolved to.
	for _, e := range st.extras {
		if addCategory(col, index, e.key, e.raw, e.code, numeric, "", false) {
			col.categories[len(col.categories)-1].observed = true
		}
	}

	if st.collided {
		m.warn(errors.PULSE_SPSS_VALUE_COLLISION, v,
			"two distinct values of this variable resolve to the same Pulse dictionary entry, so an export cannot tell which one to re-emit; the shared import path trims every cell, so values differing only in leading whitespace collapse")
	}

	distinct := len(col.values)
	ft, ok := categoricalTypeFor(distinct)
	if !ok {
		return categoricalOverflowError(v, distinct)
	}
	col.fieldType = ft

	if opts.cardinalityWarnFraction <= 1 && m.cases >= cardinalityWarnMinCases &&
		float64(distinct) > opts.cardinalityWarnFraction*float64(m.cases) {
		ce := mapError(errors.PULSE_SPSS_CARDINALITY_HIGH, v,
			"the variable has %d distinct value(s) across %d case(s), which maps to a %s dictionary of one entry per %.1f case(s); a near-unique categorical is the free-text signature and its inline dictionary block is read on every open",
			distinct, m.cases, col.fieldType, float64(m.cases)/float64(distinct))
		ce.Details[errors.DetailSPSSDistinct] = distinct
		ce.Details[errors.DetailSPSSActualCases] = m.cases
		m.warnings = append(m.warnings, ce)
	}
	return nil
}

// categoricalTypeFor picks the narrowest categorical type whose inline
// dictionary holds distinct entries, reporting false when none does.
//
// The u32 arm is a backstop rather than a live constraint — 4.29 billion
// entries is past any real cohort — but it is a hard failure rather than
// a truncation, because the only alternative is dropping values.
//
// The comparisons run in int64 so the u32 capacity does not overflow the
// int type on a 32-bit build.
func categoricalTypeFor(distinct int) (encoding.FieldType, bool) {
	n := int64(distinct)
	switch {
	case n < 0:
		return 0, false
	case n <= int64(encoding.FieldTypeCategoricalU8.MaxCategoricalEntries()):
		return encoding.FieldTypeCategoricalU8, true
	case n <= int64(encoding.FieldTypeCategoricalU16.MaxCategoricalEntries()):
		return encoding.FieldTypeCategoricalU16, true
	case n <= int64(encoding.FieldTypeCategoricalU32.MaxCategoricalEntries()):
		return encoding.FieldTypeCategoricalU32, true
	}
	return 0, false
}

// categoricalOverflowError is the refusal for a variable no categorical
// type can hold.
//
// It is a hard error and not a warning because every alternative loses
// data: truncating the dictionary drops values, and there is no wider
// dictionary-bearing type to widen to.
func categoricalOverflowError(v variable, distinct int) *errors.CodedError {
	ce := mapError(errors.PULSE_SPSS_CATEGORICAL_OVERFLOW, v,
		"the variable has %d distinct value(s), more than the %d a categorical_u32 dictionary can hold",
		distinct, encoding.FieldTypeCategoricalU32.MaxCategoricalEntries())
	ce.Details[errors.DetailSPSSDistinct] = distinct
	return ce
}

// addCategory appends one dictionary entry plus its triple, and reports
// whether the entry collided with one already present.
//
// A collision means two distinct source values resolved to one dictionary
// text. Both are recorded, against the SHARED id, rather than one being
// dropped: an export that has to pick between them needs to see that
// there was a choice.
func addCategory(col *columnMapping, index map[string]int,
	key, raw string, code float64, numeric bool, label string, labelled bool,
) bool {
	entry := categoryEntry{
		value: key, numeric: numeric, code: code,
		text: raw, label: label, labelled: labelled,
	}
	if at, ok := index[key]; ok {
		entry.id = col.categories[at].id
		col.categories = append(col.categories, entry)
		return true
	}
	entry.id = uint32(len(col.values))
	index[key] = len(col.categories)
	col.values = append(col.values, key)
	col.categories = append(col.categories, entry)
	return false
}

// defaultHints returns the smart-default aggregator and grouper for a
// column. The declared measurement level decides where the file declares
// one, which is the record 7/11 → Pulse-defaults path; otherwise the
// mapped field type's own default applies, mirroring
// descriptor/defaults.go. An empty aggregator means "no default", which
// is the date family's rule — summing instants is never the intent.
func defaultHints(ft encoding.FieldType, m measureLevel) (types.AggregationType, types.GroupType) {
	switch m {
	case measureNominal, measureOrdinal:
		return types.AGG_FREQUENCY, types.GROUP_CATEGORY
	case measureScale:
		return types.AGG_SUM, types.GROUP_RANGE
	}
	switch {
	case ft.IsCategorical():
		return types.AGG_FREQUENCY, types.GROUP_CATEGORY
	case ft == encoding.FieldTypeDate || ft == encoding.FieldTypeDateTime:
		return "", types.GROUP_DATE
	default:
		return types.AGG_SUM, types.GROUP_RANGE
	}
}

// ---------------------------------------------------------------------------
// The Pulse schema
// ---------------------------------------------------------------------------

// schema renders the mapping as the authoritative .pulse schema.
//
// A fresh schema — and a fresh dictionary per categorical column — is
// built on every call on purpose. encoding.Dictionary is mutable and the
// import path appends to it, so handing the same instance to two imports
// would let one import's values leak into the other's IDs.
func (m *mapping) schema() *encoding.Schema {
	fields := make([]encoding.Field, len(m.out))
	offset := 0
	for i, slot := range m.out {
		c := &m.cols[slot.col]

		name, ft, nullable, description := c.name, c.fieldType, c.nullable, c.description
		values := c.values
		if slot.sibling {
			sib := c.sibling
			name, ft, values = sib.name, sib.fieldType, sib.values()
			// A sibling is nullable by construction: a PRESENT value has
			// no reason, renders as the empty string and lands in the
			// null bitmap. Declaring it non-nullable would fail every
			// row of a column that is not missing everywhere.
			nullable = true
			description = missingSiblingDescription(c)
		}

		f := encoding.Field{
			Name:         name,
			Type:         ft,
			Nullable:     nullable,
			ByteOffset:   offset,
			CsvColumnIdx: i,
			Description:  description,
		}
		if ft.HasDictionary() {
			dict := encoding.NewDictionary()
			for _, v := range values {
				// Add never fails without a limit, and the width was
				// chosen to fit these exact entries.
				_, _ = dict.Add(v)
			}
			f.Dictionary = dict
		}
		fields[i] = f
		offset += ft.ByteSize()
	}
	return &encoding.Schema{Fields: fields}
}

// missingSiblingDescription is the Pulse field description of a generated
// reason column. It names the variable it belongs to rather than
// borrowing that variable's own label: a sibling described "Annual
// household income" reads as a second income column, which is the one
// thing an analyst must not believe about it.
func missingSiblingDescription(c *columnMapping) string {
	desc := "Why " + strconv.Quote(c.name) + " is missing, or null where it is present"
	if c.description != "" {
		desc += " (" + c.description + ")"
	}
	return desc
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// dictKey is the dictionary lookup key for a rendered cell.
//
// io/import.go trims every cell with strings.TrimSpace before it converts
// or looks anything up, so a dictionary entry that kept its surrounding
// whitespace could never be found and the importer would append a second,
// trimmed entry beside it — silently shifting every later ID and breaking
// the code ↔ ID triple this mapping exists to hold. The key therefore
// matches what the importer will actually look up.
func dictKey(s string) string { return strings.TrimSpace(s) }

// rendersAsNull reports whether a rendered cell is one of the null
// sentinel tokens io/import.go's isNullToken recognises: "", "na", "n/a"
// and "null", case-insensitively.
//
// It is a deliberate duplicate of an unexported function in the parent
// package rather than an import: the coupling is real either way, and
// stating it here keeps the mapping honest about which values it knows
// will import as null. A token added there without being added here
// costs a missed warning, never a wrong schema.
func rendersAsNull(s string) bool {
	switch len(s) {
	case 0:
		return true
	case 2:
		return strings.EqualFold(s, "na")
	case 3:
		return strings.EqualFold(s, "n/a")
	case 4:
		return strings.EqualFold(s, "null")
	}
	return false
}

// spssSecondsExact converts an SPSS temporal datum to whole Unix seconds,
// reporting false when the value cannot be represented exactly: a
// fractional second, a non-finite double, or a magnitude outside the
// int64 second range. A false return is what routes the column to f64 raw
// seconds instead of degrading it.
func spssSecondsExact(v float64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if v != math.Trunc(v) {
		return 0, false
	}
	// 9.2e18 is inside the int64 range with room to spare for the epoch
	// shift, and no real SPSS datum comes near it.
	if v > 9.2e18 || v < -9.2e18 {
		return 0, false
	}
	return int64(v) - spssEpochOffsetSeconds, true
}

// mapError builds a coded schema-mapping diagnostic naming the variable
// and the byte offset of the record type 2 that declared it.
func mapError(code errors.Code, v variable, format string, args ...any) *errors.CodedError {
	msg := "spss: variable " + strconv.Quote(v.fieldName()) + ": " + fmt.Sprintf(format, args...) +
		" [record type 2 at byte offset " + strconv.Itoa(v.offset) + "]"
	return errors.NewCodedErrorWithDetails(code, msg, map[string]any{
		errors.DetailSPSSRecord:   recordName(recTypeVariable),
		errors.DetailSPSSOffset:   v.offset,
		errors.DetailSPSSVariable: v.fieldName(),
	})
}

// warn records one non-fatal mapping diagnostic.
func (m *mapping) warn(code errors.Code, v variable, format string, args ...any) {
	m.warnings = append(m.warnings, mapError(code, v, format, args...))
}

// warnPrecision reports a temporal column dropped to f64 raw seconds.
func (m *mapping) warnPrecision(v variable) {
	ce := mapError(errors.PULSE_SPSS_TEMPORAL_PRECISION, v,
		"print format %d carries at least one value that is not a whole, finite second count, which neither date nor datetime can hold exactly; the variable maps to f64 raw SPSS seconds and the print format is retained for export",
		v.print.code)
	ce.Details[errors.DetailSPSSFormat] = v.print.code
	m.warnings = append(m.warnings, ce)
}

// warnWidened reports a day-resolution column widened to datetime.
func (m *mapping) warnWidened(v variable, st *columnStats) {
	reason := "carries a time of day that day resolution would truncate"
	switch {
	case st.preEpoch && st.subDay:
		reason = "carries instants before 1970-01-01 and times of day, neither of which the unsigned epoch-day date type holds"
	case st.preEpoch:
		reason = "carries instants before 1970-01-01, which the unsigned epoch-day date type cannot express"
	}
	ce := mapError(errors.PULSE_SPSS_DATE_WIDENED, v,
		"the day-resolution print format %d %s; the variable maps to datetime instead, which holds every value exactly and day-truncates under GROUP_DATE",
		v.print.code, reason)
	ce.Details[errors.DetailSPSSFormat] = v.print.code
	m.warnings = append(m.warnings, ce)
}
