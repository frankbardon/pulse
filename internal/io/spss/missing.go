package spss

// Numeric user-missing values, and the `<var>_missing` sibling columns
// that keep the REASON a value is missing.
//
// # The decision this file implements
//
// SPSS distinguishes several missing states: present, system-missing
// (`sysmis`, the one state the format has a sentinel for), and up to
// three discrete user-missing codes — or a lo..hi range, or a range plus
// one discrete code. Survey files use them to separate `refused` from
// `don't know` from `not applicable`, and those are reported separately
// and drive weighting.
//
// The `.pulse` null bitmap is ONE BIT. It records *that* a value is
// absent and can never record *why*. So the two obvious mappings both
// lose:
//
//   - Keep the codes as ordinary data (what this reader did before this
//     file existed) and AGG_SUM over an income column silently adds
//     99999 for every refusal.
//   - Collapse every missing state to null and the refused / don't-know /
//     not-applicable distinction — the thing the specification exists to
//     carry — is gone.
//
// The resolution is a DERIVED COLUMN. The analytic column keeps the real
// values and is null at every missing position, so AGG_SUM and AGG_MEAN
// are arithmetically correct; a generated `<var>_missing` sibling carries
// the reason as a categorical whose dictionary is the reason vocabulary.
// Nothing is lost, and the cost is one extra column per variable that
// actually declares a missing specification.
//
// # Scope: NUMERIC variables only
//
// A string variable maps to `categorical_*`, where the value IS the
// dictionary entry — a string user-missing code is already present and
// addressable in the main dictionary, and a sibling would be pure
// redundancy that doubles the schema width of an all-string survey file.
// The same is true of a value-labelled NUMERIC variable, which also maps
// to `categorical_*`. Both are the categorical arm of the mapping and
// belong to E4-S3, which owns flagging which dictionary entries are
// missing-coded; record 7/22 long-string missing values land on the same
// variable.missing slot and are covered by that same call. This file
// generates a sibling only for a variable whose resolved column is NOT
// dictionary-bearing.
//
// # The reason vocabulary
//
// Entry 0 is always "sysmis". After it come the DECLARED discrete codes
// in the specification's own order, then every further missing value the
// data actually carried, in first-seen order. A range is NOT enumerated —
// only its observed members get entries, which is why a range-missing
// column may need to widen past categorical_u8.
//
// A reason's text is the value label the file declares for that code
// where it declares one, and the code's own canonical rendering where it
// does not. The label is preferred because "Refused" is what a human
// wants to read; the code is the fallback because it always exists and is
// injective. Where a label would collide with a reason already taken by a
// DIFFERENT code, the code's rendering is used instead and
// PULSE_SPSS_VALUE_COLLISION warns — two missing codes sharing one
// dictionary entry would destroy exactly the distinction this file exists
// to preserve, so the vocabulary stays injective and the prettier name is
// what gives way.
//
// # "Present" is the null bit, not a dictionary entry
//
// A row whose value is present renders the sibling as the empty string,
// which the shared import path (io/import.go's isNullToken) reads as
// null before it consults any dictionary. The empty reason is therefore
// the sibling's null bitmap bit, and it is NOT materialised as a
// dictionary entry: an entry at ID 0 that no record could ever reference
// would be dead weight and would misreport the column's width. This is
// the same rule the main categorical mapping already applies — see
// PULSE_SPSS_NULL_TOKEN_COLLISION, "it contributes no dictionary entry".

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// MissingSiblingSuffix is appended to a variable's Pulse field name to
// name its generated user-missing reason column. It is a fixed
// convention rather than an option: an export has to recognise a derived
// column without being told, and a configurable suffix would make that a
// guess.
const MissingSiblingSuffix = "_missing"

// SysmisReason is the reason text a sibling carries for a system-missing
// datum. It always occupies dictionary ID 0 of a sibling column.
const SysmisReason = "sysmis"

// DerivedKindNumericMissing is the sidecar [Derived] Kind of a numeric
// user-missing reason sibling.
//
// This package owns the derived-kind vocabulary; E4-S1 reserved the
// registry slot and stated that entries may be extended additively with
// no SidecarFormatVersion bump. A consumer folding a cohort back to
// `.sav` matches on this string to know that the column is synthesised,
// must not be emitted as an SPSS variable, and instead supplies the
// missing CODE for every row of its single source variable.
const DerivedKindNumericMissing = "numeric_missing"

// MissingMode selects how an SPSS numeric variable's USER-missing values
// are represented in the cohort. It has no effect on system-missing,
// which is a null under every mode, nor on categorical columns, whose
// codes stay in the main dictionary.
type MissingMode int

const (
	// MissingAuto is the default: the analytic column is null at every
	// missing position and a generated `<var>_missing` sibling carries
	// the reason. Lossless — both the arithmetic and the reason survive.
	MissingAuto MissingMode = iota

	// MissingNull suppresses the sibling columns. Every user-missing
	// value still becomes a null in the analytic column — the
	// arithmetic is the same under both modes — but the REASON is not
	// represented in the cohort at all.
	//
	// It exists for callers who genuinely want the slimmer schema and
	// have accepted that cost. The full missing-value specification
	// still rides the metadata sidecar either way, so a re-import can
	// recover the vocabulary even though this cohort cannot say which
	// row had which reason.
	MissingNull
)

// String returns the flag spelling of the mode.
func (m MissingMode) String() string {
	switch m {
	case MissingAuto:
		return "auto"
	case MissingNull:
		return "null"
	default:
		return "MissingMode(" + strconv.Itoa(int(m)) + ")"
	}
}

// ParseMissingMode resolves the `--spss-missing` spelling of a mode.
//
// An empty string is not an instruction and resolves to the default. An
// unrecognised one is PULSE_SPSS_MISSING_MODE_INVALID rather than a
// silent fall back to the default, because the two modes produce
// different schemas: substituting one for a typo of the other would hand
// the caller a cohort they did not ask for.
func ParseMissingMode(s string) (MissingMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return MissingAuto, nil
	case "auto":
		return MissingAuto, nil
	case "null":
		return MissingNull, nil
	}
	return MissingAuto, errors.NewCodedErrorWithDetails(
		errors.PULSE_SPSS_MISSING_MODE_INVALID,
		"spss: "+strconv.Quote(s)+" is not a user-missing handling mode; the two are \"auto\" (the default — a null in the analytic column plus a "+
			strconv.Quote("<var>"+MissingSiblingSuffix)+" sibling carrying the reason) and \"null\" (a plain null, no sibling, the reason not represented)",
		map[string]any{errors.DetailSPSSMissingMode: s})
}

// ---------------------------------------------------------------------------
// Deciding whether a datum is user-missing
// ---------------------------------------------------------------------------

// missingTest is a variable's user-missing specification compiled to a
// predicate over a numeric datum.
//
// It is deliberately NOT a re-reading of missingSpec at each cell: the
// spec's shape discriminant lives in a signed count field, and deciding
// per datum which of its slots are range bounds and which are discrete
// codes would put the format's most easily misread field on the hot path
// of every case.
type missingTest struct {
	// discrete are the discrete missing codes.
	discrete []float64

	// hasRange reports that low..high is in force.
	hasRange bool

	// low and high are the INCLUSIVE range bounds.
	//
	// SPSS spells an open-ended range with LOWEST / HIGHEST, which are
	// -DBL_MAX and +DBL_MAX — and -DBL_MAX is the same double as the
	// default system-missing sentinel. Every caller therefore tests for
	// sysmis BEFORE it calls match, or a LOWEST-bounded range swallows
	// every sysmis datum and reports it as a user-missing one.
	low, high float64
}

// compileMissingTest builds the predicate for one variable, returning nil
// when the variable declares no USER-missing values this file acts on.
//
// A string variable returns nil: its missing values are text, its column
// is dictionary-bearing, and the categorical arm owns it.
func compileMissingTest(v variable) *missingTest {
	spec := v.missing
	if v.isString() || spec.count() == 0 {
		return nil
	}
	t := &missingTest{}
	from := 0
	if spec.isRange() {
		if len(spec.numeric) < 2 {
			// A negative code whose slots did not decode to two bounds.
			// The raw slots are still on the dictionary and in the
			// sidecar; what cannot be done is test against a range that
			// was never recovered.
			return nil
		}
		t.hasRange = true
		t.low, t.high = spec.numeric[0], spec.numeric[1]
		from = 2
	}
	for i := from; i < len(spec.numeric); i++ {
		t.discrete = append(t.discrete, spec.numeric[i])
	}
	if !t.hasRange && len(t.discrete) == 0 {
		return nil
	}
	return t
}

// match reports whether a numeric datum is one of the variable's
// user-missing values. A nil test matches nothing, which is what makes
// "this variable declares no missing values" the same code path as
// "this value is present".
//
// The caller must already have excluded system-missing; see missingTest.
func (t *missingTest) match(v float64) bool {
	if t == nil {
		return false
	}
	for _, d := range t.discrete {
		if v == d {
			return true
		}
	}
	return t.hasRange && v >= t.low && v <= t.high
}

// declaredDiscrete returns the discrete codes the specification declares,
// which always get a dictionary entry whether or not any case carried
// them. A declared code is a finite, stated vocabulary — the same reason
// a declared value label occupies an ID it may never be used under.
func (t *missingTest) declaredDiscrete() []float64 {
	if t == nil {
		return nil
	}
	return t.discrete
}

// ---------------------------------------------------------------------------
// The sibling column
// ---------------------------------------------------------------------------

// missingReason is one entry of a sibling's reason vocabulary.
type missingReason struct {
	// id is the Pulse dictionary ID, which is the entry's position.
	id uint32

	// text is the dictionary entry: the reason as it appears in the
	// cohort.
	text string

	// sysmis marks the one entry that stands for system-missing rather
	// than for a user-missing code.
	sysmis bool

	// code is the SPSS numeric value this reason stands for. Meaningless
	// when sysmis.
	code float64

	// label is the value label the file declared for code, "" when it
	// declared none. It is recorded even when text fell back to the code
	// rendering because the label collided, so a consumer can still see
	// what the file said.
	label string

	// declared reports that the code appears in the variable's
	// missing-value SPECIFICATION as a discrete value. False for a value
	// that only a range plus the data section put here.
	declared bool

	// observed reports that at least one case carried this state.
	observed bool
}

// missingSibling is one generated `<var>_missing` column.
type missingSibling struct {
	// name is the generated Pulse field name.
	name string

	// source is the Pulse field name of the variable it was derived from.
	source string

	// fieldType is the resolved categorical width.
	fieldType encoding.FieldType

	// reasons are the vocabulary in dictionary ID order.
	reasons []missingReason

	// byValue maps a missing code's canonical rendering to its reason
	// text. The key is the rendering rather than the float64 because
	// that is how the rest of this package defines distinctness, so two
	// bit patterns that render alike are one entry here too.
	byValue map[string]string
}

// values returns the dictionary entry texts in ID order.
func (s *missingSibling) values() []string {
	out := make([]string, len(s.reasons))
	for i, r := range s.reasons {
		out[i] = r.text
	}
	return out
}

// reasonFor renders the sibling cell for a user-missing datum.
//
// The fallback names the code itself. It cannot fire for a mapping built
// by buildMapping — the scan walks every case, so every observed missing
// value already has an entry — and it is kept because the alternative on
// an unmapped value would be an empty cell, which reads as "present" and
// would silently deny that the datum was missing at all.
func (s *missingSibling) reasonFor(v float64) string {
	raw := formatNumericValue(v)
	if text, ok := s.byValue[raw]; ok {
		return text
	}
	return raw
}

// ---------------------------------------------------------------------------
// Building the sibling
// ---------------------------------------------------------------------------

// buildMissingSibling assembles one variable's reason vocabulary from its
// declared specification and what the scan observed.
//
// It returns nil when the column gets no sibling. It never returns nil
// for a variable the caller has already decided carries a test: the
// vocabulary always holds at least the sysmis entry.
func (m *mapping) buildMissingSibling(col *columnMapping, v variable,
	labels []valueLabel, st *columnStats,
) (*missingSibling, error) {
	sib := &missingSibling{
		name:    col.name + MissingSiblingSuffix,
		source:  col.name,
		byValue: make(map[string]string),
	}

	// Entry 0 is always sysmis. It is the one missing state every
	// numeric variable can carry regardless of what it declares, so it
	// is part of the vocabulary whether or not this file used it.
	sib.reasons = append(sib.reasons, missingReason{
		id: 0, text: SysmisReason, sysmis: true, observed: st.sawSysmis,
	})

	byLabel := numericLabelIndex(labels, m.plan)
	taken := map[string]int{SysmisReason: 0}
	collided := false

	add := func(code float64, declared bool) {
		raw := formatNumericValue(code)
		if _, seen := sib.byValue[raw]; seen {
			// Already in the vocabulary — a declared code the data also
			// carried, reached first through the specification.
			return
		}
		text := raw
		if label := byLabel[raw]; label != "" {
			if at, clash := taken[label]; !clash || sib.reasons[at].text == raw {
				text = label
			} else {
				// The prettier name is what gives way: a shared entry
				// would collapse two distinct missing codes into one
				// reason, which is the exact loss this column exists to
				// prevent.
				collided = true
			}
		}
		if _, clash := taken[text]; clash {
			// Only reachable when a LABEL of one code equals the
			// canonical rendering of another, or the literal "sysmis".
			text = raw
			collided = true
		}
		id := uint32(len(sib.reasons))
		taken[text] = len(sib.reasons)
		sib.reasons = append(sib.reasons, missingReason{
			id: id, text: text, code: code, label: byLabel[raw], declared: declared,
		})
		sib.byValue[raw] = text
	}

	// The declared discrete codes first, in the specification's own
	// order, so the source's own sequence survives in the Pulse IDs.
	for _, code := range col.missing.declaredDiscrete() {
		add(code, true)
	}
	// Then everything the data carried that the specification did not
	// name one by one — the members of a range, which is never
	// enumerated because a range is not a finite vocabulary.
	for _, code := range st.missingValues {
		add(code, false)
	}

	// Mark what the data actually carried.
	for _, code := range st.missingValues {
		raw := formatNumericValue(code)
		text := sib.byValue[raw]
		if at, ok := taken[text]; ok {
			sib.reasons[at].observed = true
		}
	}

	if collided {
		m.warn(errors.PULSE_SPSS_VALUE_COLLISION, v,
			"two of this variable's missing values would share one reason in %q, so the value label of the later one was replaced by its numeric code to keep the reasons distinguishable",
			sib.name)
	}

	ft, ok := categoricalTypeFor(len(sib.reasons))
	if !ok {
		return nil, categoricalOverflowError(v, len(sib.reasons))
	}
	sib.fieldType = ft
	return sib, nil
}

// numericLabelIndex projects a variable's value labels onto the canonical
// renderings of their codes, so a missing code can be given the name the
// file gave it.
//
// A label declared on the system-missing sentinel is skipped for the same
// reason resolveCategories skips it: no case can carry the sentinel as a
// datum, so the label names nothing.
func numericLabelIndex(labels []valueLabel, plan *dataPlan) map[string]string {
	if len(labels) == 0 || plan == nil {
		return nil
	}
	out := make(map[string]string, len(labels))
	for _, l := range labels {
		code := l.numeric(plan.bo)
		if plan.isSysmisValue(code) || l.label == "" {
			continue
		}
		raw := formatNumericValue(code)
		if _, seen := out[raw]; !seen {
			out[raw] = l.label
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The cohort's column layout
// ---------------------------------------------------------------------------

// outputSlot names one column of the emitted cohort: the source variable
// it belongs to, and whether it is that variable's generated reason
// sibling rather than the variable itself.
//
// The cohort is NOT one column per SPSS variable once siblings exist, so
// this is what ReadHeader, the schema and the row decoder all address by.
// Nothing else may derive the layout independently — two derivations
// that disagree would put a cell under the wrong field name.
type outputSlot struct {
	// col is the index into dictionary.vars and mapping.cols. It is -1 on
	// a derived multiple-dichotomy set slot, which belongs to no single
	// variable — every consumer that indexes by it must test mrSet first,
	// and -1 rather than 0 is what makes forgetting to a panic instead of
	// a silently wrong column.
	col int

	// sibling reports that this slot is the generated reason column.
	sibling bool

	// mrSet reports that this slot is a derived multiple-dichotomy set_*
	// convenience column, indexed by mrIndex rather than by col. See
	// mrset.go for why such a column is additive.
	mrSet bool

	// mrIndex is the index into mapping.mrSets / dataPlan.mrSets.
	// Meaningful only when mrSet is set.
	mrIndex int

	// name is the Pulse field name of the slot.
	name string
}

// planOutputs decides the cohort's column layout FROM THE DICTIONARY
// ALONE, and reports a generated name that collides with a real one.
//
// It reads no data. That is deliberate and load-bearing: ReadHeader is
// supposed to be a dictionary-cheap call, and computing the layout here
// rather than after the scan keeps it one — while still guaranteeing that
// the names ReadHeader returns are exactly the fields the schema declares,
// because both call this.
//
// A sibling is placed immediately after the variable it belongs to rather
// than in a block at the end, so a cohort's columns read in the order a
// person thinks about them.
func planOutputs(d *dictionary, opts mappingOptions) ([]outputSlot, []*mrSetColumn, []*errors.CodedError, error) {
	labels := valueLabelsByVariable(d)

	// SPSS variable names are case-insensitive, so the collision index is
	// too: a generated `income_missing` beside a declared `INCOME_MISSING`
	// is one name to SPSS, and an export that emitted the real variable
	// after dropping the derived one would produce a file whose columns
	// cannot be told apart.
	owner := make(map[string]string, len(d.vars))
	for _, v := range d.vars {
		name := v.fieldName()
		if _, dup := owner[strings.ToUpper(name)]; !dup {
			owner[strings.ToUpper(name)] = name
		}
	}

	out := make([]outputSlot, 0, len(d.vars))
	for i, v := range d.vars {
		name := v.fieldName()
		out = append(out, outputSlot{col: i, name: name})
		if opts.missingMode != MissingAuto {
			continue
		}
		// Only the non-dictionary-bearing numeric arm gets a sibling.
		// A string variable and a value-labelled numeric are both
		// categorical, where the code IS the dictionary entry.
		if classify(v, labelsCodeTheVariable(v, labels[i], d.byteOrder, d.sysmis)) == kindCategorical {
			continue
		}
		if compileMissingTest(v) == nil {
			continue
		}
		derived := name + MissingSiblingSuffix
		if existing, clash := owner[strings.ToUpper(derived)]; clash {
			return nil, nil, nil, derivedNameCollision(v, derived, existing)
		}
		owner[strings.ToUpper(derived)] = derived
		out = append(out, outputSlot{col: i, sibling: true, name: derived})
	}

	// The derived multiple-dichotomy columns are placed last because they
	// depend on the layout above: each one sits immediately after the LAST
	// of its constituents, which is only knowable once every variable and
	// every reason sibling has a position. The name index is threaded
	// through so a set column cannot claim a name a variable or a sibling
	// already holds.
	sets, warnings := planMRSets(d, owner)
	return placeMRSets(out, sets), sets, warnings, nil
}

// placeMRSets splices each derived set column into the layout immediately
// after the LAST of its constituents.
//
// "After the last" rather than "after the first" or "at the end" is the only
// placement that keeps two properties at once. A summary column must not
// precede any of the parts it summarises — a reader meeting `media` before
// `Q1A` would reasonably take the constituents for a decomposition of it
// rather than the other way round — and the column should stay adjacent to
// its battery rather than exiled to a block at the end, which is E4-S2's
// rule for a reason sibling applied to a column with many sources. When the
// constituents are contiguous, which is the ordinary case, the two rules
// agree and the whole battery reads as a block followed by its summary.
//
// Sets are spliced in definition order, so two sets sharing a last
// constituent land in the order the file declared them.
func placeMRSets(out []outputSlot, sets []*mrSetColumn) []outputSlot {
	if len(sets) == 0 {
		return out
	}
	// after[i] is the set columns to emit once layout slot i has been
	// emitted. Built by locating each set's last constituent slot.
	after := make(map[int][]int, len(sets))
	for si, s := range sets {
		last := -1
		for at, slot := range out {
			if slot.mrSet || slot.col < 0 {
				continue
			}
			for i := range s.elements {
				if s.elements[i].col == slot.col && at > last {
					last = at
				}
			}
		}
		if last < 0 {
			// Unreachable: planMRSet refuses a set whose members do not
			// all resolve, and every resolved member has a layout slot.
			last = len(out) - 1
		}
		after[last] = append(after[last], si)
	}

	placed := make([]outputSlot, 0, len(out)+len(sets))
	for at, slot := range out {
		placed = append(placed, slot)
		for _, si := range after[at] {
			placed = append(placed, outputSlot{
				col: -1, mrSet: true, mrIndex: si, name: sets[si].name,
			})
		}
	}
	return placed
}

// derivedNameCollision is the refusal for a generated column name a real
// variable already holds.
//
// It is a hard error rather than a rename because both alternatives lose:
// two fields of one name cannot be addressed unambiguously, and a
// silently renamed sibling is a column no consumer can map back to its
// source. Both sides are named, because knowing only one of them does not
// say what to rename.
func derivedNameCollision(v variable, derived, existing string) *errors.CodedError {
	ce := mapError(errors.PULSE_SPSS_DERIVED_NAME_COLLISION, v,
		"the variable declares user-missing values, so the import would generate the reason column %q — but this file already declares a variable named %q, and SPSS variable names are case-insensitive; rename that variable, or import with --spss-missing=null to suppress the sibling and lose the reason",
		derived, existing)
	ce.Details[errors.DetailSPSSDerived] = derived
	ce.Details[errors.DetailSPSSCollidesWith] = existing
	return ce
}
