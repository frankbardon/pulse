package processing

import (
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/types"
)

// ATTR_PREVIOUS_STATE — per record of a long panel, the state its
// subject held at the subject's previous time point.
//
// Buffered only (Compute, no Row): a subject's records may sit anywhere
// in the cohort, so no row-local or partition-local pass is right —
// AttributeType.Streamable is false, the merge gate never lists it, and
// the fused crosstab declines it as a buffered attribute.
//
// Per subject (SubjectField's value; a u64 by its exact word, a
// decimal128 by its mantissa), the records are ordered by OrderField's
// instant (a date's epoch day, a datetime's epoch second — never local
// time) and grouped into distinct time points. A record at time point
// k > 1 reads the state at time point k − 1:
//
//   - one record there: its state (a null state is null — no
//     carry-forward to an earlier one);
//   - a tie (two or more records): the tied state when every tied
//     state agrees (an all-null tie is null), else null — counted
//     (n_ambiguous records, n_subjects_ambiguous affected) and warned once per
//     slot (PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE, attributeWarner); under
//     Strict the run fails with that code (attributeStrictRefuser).
//
// Records tied with each other each read the same earlier time point.
// A subject's first time point is null; a record with a null subject or
// a null order value is null and is never a predecessor. Every null is
// AttributeNull — never 0, which is the first dictionary index. The
// output value is the earlier record's state code: its dictionary index
// (categorical) or 0 / 1 (packed_bool).
//
// The weight is irrelevant (weighting class not-weightable): a record's
// previous state is a value read off another record, not an estimate.
type previousStateAttribute struct {
	label   string
	field   string
	subject string
	order   string
	keyOf   func(*Record) (prevSubjectKey, bool)

	// nAmbiguous counts the records nulled for an ambiguous tie,
	// nAmbiguousSubjects the subjects holding one or more.
	nAmbiguous         int64
	nAmbiguousSubjects int64

	// The Components tallies: nSubjects the subjects holding a record
	// with a present subject and order, nFirst the records at their
	// subject's first time point, nGap the records whose previous time
	// point's state is null.
	nSubjects, nFirst, nGap int64

	// sortedPath records which path the last Compute took (tests pin
	// that sorted input skips the bucket-and-sort).
	sortedPath bool
}

var (
	_ AttributeComputer      = (*previousStateAttribute)(nil)
	_ attributeWarner        = (*previousStateAttribute)(nil)
	_ attributeStrictRefuser = (*previousStateAttribute)(nil)
	_ MetaAttribute          = (*previousStateAttribute)(nil)
)

// prevSubjectKey is a subject's identity: w the value's bits (a float64
// echo, or a u64's exact word), s a decimal128's mantissa.
type prevSubjectKey struct {
	w uint64
	s string
}

// newPreviousStateAttribute judges attr through the shared slot rule
// (prevstate.Check — predict raises the same refusal) and builds the
// computer. A field the schema does not carry never reaches here on a
// request path (the field-reference walk refuses it on both sides); a
// direct construction refuses it rather than reading a column that
// decodes null.
func newPreviousStateAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if cerr := prevstate.Check(attr, schema); cerr != nil {
		return nil, cerr
	}
	label := attr.Label
	if label == "" {
		label = defaultAttributeLabel(attr)
	}
	a := &previousStateAttribute{label: label, field: attr.Field, subject: attr.SubjectField, order: attr.OrderField}
	var subjectType encoding.FieldType
	if schema != nil {
		for _, r := range prevstate.FieldRefs(attr) {
			f := schema.Field(r.Name)
			if f == nil {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					string(types.ATTR_PREVIOUS_STATE)+" "+string(r.Slot)+" "+r.Name+" is not a cohort field (it reads source columns only)",
					map[string]any{"attribute": string(types.ATTR_PREVIOUS_STATE), "slot": string(r.Slot), "field": r.Name})
			}
			if r.Slot == fieldaccept.SlotSubject {
				subjectType = f.Type
			}
		}
	}
	a.keyOf = prevSubjectKeyReader(a.subject, subjectType)
	return a, nil
}

// prevSubjectKeyReader returns the subject-identity read for a subject
// field of type ft: a u64 by its exact word (distinct values above 2^53
// share a float64 echo), a decimal128 by its mantissa, a float by its
// bits with -0 folded onto 0 and NaN read as no identity, and every
// other type by its float64 echo's bits (exact for each).
func prevSubjectKeyReader(name string, ft encoding.FieldType) func(*Record) (prevSubjectKey, bool) {
	switch {
	case ft == encoding.FieldTypeU64:
		return func(r *Record) (prevSubjectKey, bool) {
			v, ok := r.Uint64Value(name)
			return prevSubjectKey{w: v}, ok
		}
	case ft.IsDecimal():
		return func(r *Record) (prevSubjectKey, bool) {
			d, ok := r.DecimalValue(name)
			if !ok {
				return prevSubjectKey{}, false
			}
			return prevSubjectKey{s: d.Mantissa().String()}, true
		}
	}
	return func(r *Record) (prevSubjectKey, bool) {
		v, ok := r.NumericValue(name)
		if !ok || math.IsNaN(v) {
			return prevSubjectKey{}, false
		}
		if v == 0 {
			v = 0 // fold -0 onto 0
		}
		return prevSubjectKey{w: math.Float64bits(v)}, true
	}
}

// prevRow is one keyed record: its index in the input and its instant.
type prevRow struct {
	idx   int
	order float64
}

// The state a time point leaves for the next one.
const (
	prevNone      = iota // the subject's first time point: nothing before it
	prevValue            // one state (or a tie on one state)
	prevNull             // a null state (or a tie on null)
	prevAmbiguous        // a tie on differing states
)

// Compute emits every record's previous state (see the type comment).
//
// Two paths, one answer. When the keyed records (present subject and
// order) already sit sorted — each subject's records contiguous, its
// order non-decreasing; a record with a null subject or order breaks no
// run — one O(N) check (sortedRuns) hands them over as runs and a
// single pass walks them with O(subjects) extra state. Otherwise the
// records are bucketed per subject and each bucket stably sorted by
// order. Either way each subject's rows reach walkSubject in order with
// a tie's records in record order, so the output, the tallies and the
// warning are identical (TestPreviousState_SortedPathMatchesSlowPath).
func (a *previousStateAttribute) Compute(records []*Record, _ string) ([]float64, error) {
	out := a.reset(len(records))
	if rows, starts, ok := a.sortedRuns(records); ok {
		a.sortedPath = true
		a.computeRuns(records, rows, starts, out)
		return out, nil
	}
	a.sortedPath = false
	a.computeBucketed(records, out)
	return out, nil
}

// reset zeroes the tallies and returns n null outputs.
func (a *previousStateAttribute) reset(n int) []float64 {
	a.nAmbiguous, a.nAmbiguousSubjects = 0, 0
	a.nSubjects, a.nFirst, a.nGap = 0, 0, 0
	out := make([]float64, n)
	for i := range out {
		out[i] = AttributeNull
	}
	return out
}

// sortedRuns is the sorted-input check: the keyed rows in record order
// and the start of each subject's run in them, ok only when every
// subject's keyed records are contiguous and non-decreasing in order
// (ties allowed). It stops at the first record that breaks either rule,
// so a shuffled input costs a few records' reads (and the rows slice's
// allocation), not a pass; the worst case — sorted but for the last
// record — is one extra pass before the bucket-and-sort path.
func (a *previousStateAttribute) sortedRuns(records []*Record) (rows []prevRow, starts []int, ok bool) {
	seen := map[prevSubjectKey]struct{}{}
	rows = make([]prevRow, 0, len(records))
	var cur prevSubjectKey
	for i, r := range records {
		k, ok := a.keyOf(r)
		if !ok {
			continue
		}
		o, ok := r.NumericValue(a.order)
		if !ok || math.IsNaN(o) {
			continue
		}
		if len(rows) == 0 || k != cur {
			if _, again := seen[k]; again {
				return nil, nil, false
			}
			seen[k] = struct{}{}
			cur = k
			starts = append(starts, len(rows))
		} else if o < rows[len(rows)-1].order {
			return nil, nil, false
		}
		rows = append(rows, prevRow{idx: i, order: o})
	}
	return rows, starts, true
}

// computeRuns is the sorted path: each run of rows (starts[s] up to the
// next start) is one subject's records, already in order.
func (a *previousStateAttribute) computeRuns(records []*Record, rows []prevRow, starts []int, out []float64) {
	a.nSubjects = int64(len(starts))
	for s, lo := range starts {
		hi := len(rows)
		if s+1 < len(starts) {
			hi = starts[s+1]
		}
		a.walkSubject(records, rows[lo:hi], out)
	}
}

// computeBucketed is the general path: bucket the keyed records per
// subject, then stably sort each bucket by order.
func (a *previousStateAttribute) computeBucketed(records []*Record, out []float64) {
	bySubject := map[prevSubjectKey][]prevRow{}
	for i, r := range records {
		k, ok := a.keyOf(r)
		if !ok {
			continue
		}
		o, ok := r.NumericValue(a.order)
		if !ok || math.IsNaN(o) {
			continue
		}
		bySubject[k] = append(bySubject[k], prevRow{idx: i, order: o})
	}
	a.nSubjects = int64(len(bySubject))
	for _, rows := range bySubject {
		// Appended in record order, so a stable sort keeps a tie's
		// records in record order (no output depends on it).
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].order < rows[j].order })
		a.walkSubject(records, rows, out)
	}
}

// walkSubject writes one subject's outputs and tallies from its rows in
// non-decreasing order, grouped into time points.
func (a *previousStateAttribute) walkSubject(records []*Record, rows []prevRow, out []float64) {
	kind, state := prevNone, 0.0
	ambiguous := false
	for j := 0; j < len(rows); {
		k := j + 1
		for k < len(rows) && rows[k].order == rows[j].order {
			k++
		}
		for _, row := range rows[j:k] {
			switch kind {
			case prevNone:
				a.nFirst++
			case prevValue:
				out[row.idx] = state
			case prevNull:
				a.nGap++
			case prevAmbiguous:
				a.nAmbiguous++
				ambiguous = true
			}
		}
		kind, state = a.timePointState(records, rows[j:k])
		j = k
	}
	if ambiguous {
		a.nAmbiguousSubjects++
	}
}

// timePointState is the state one time point's records leave for the
// next: their common state, a common null, or ambiguous when any two
// differ (a null and a value differ).
func (a *previousStateAttribute) timePointState(records []*Record, rows []prevRow) (int, float64) {
	s0, ok0 := records[rows[0].idx].NumericValue(a.field)
	for _, row := range rows[1:] {
		s, ok := records[row.idx].NumericValue(a.field)
		if ok != ok0 || (ok && s != s0) {
			return prevAmbiguous, 0
		}
	}
	if !ok0 {
		return prevNull, 0
	}
	return prevValue, s0
}

// Components satisfies MetaAttribute: the operator keys of the slot's
// Response.Components.Attributes entry (ComponentSchema in
// capabilities_attributes.go; mergeability None — the counts are of one
// whole-cohort pass). With the records whose subject or order is null,
// n_first + n_gap + n_ambiguous make up the floor's n_null.
func (a *previousStateAttribute) Components() (map[string]any, error) {
	return map[string]any{
		"n_subjects":  a.nSubjects,
		"n_first":     a.nFirst,
		"n_gap":       a.nGap,
		"n_ambiguous": a.nAmbiguous,
	}, nil
}

// ambiguousTie is the slot's PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE message
// and details (nil details when no record was ambiguous).
func (a *previousStateAttribute) ambiguousTie() (string, map[string]any) {
	if a.nAmbiguous == 0 {
		return "", nil
	}
	return a.label + ": " + strconv.FormatInt(a.nAmbiguous, 10) + " record(s) of " + strconv.FormatInt(a.nAmbiguousSubjects, 10) +
			" subject(s) follow a time point whose tied records (same " + a.order + ") carry different " + a.field +
			" states, so they have no single previous state and are null",
		map[string]any{
			"attribute":            a.label,
			"n_ambiguous":          a.nAmbiguous,
			"n_subjects_ambiguous": a.nAmbiguousSubjects,
		}
}

// attributeWarnings satisfies attributeWarner: one
// PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE warning when any record was
// nulled for an ambiguous tie.
func (a *previousStateAttribute) attributeWarnings() []*types.ResponseWarning {
	msg, details := a.ambiguousTie()
	if details == nil {
		return nil
	}
	return []*types.ResponseWarning{{
		Code:    string(errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE),
		Message: msg,
		Details: details,
	}}
}

// strictRefusal satisfies attributeStrictRefuser: under Strict an
// ambiguous tie fails the run with the warning's own code and details.
func (a *previousStateAttribute) strictRefusal() *errors.CodedError {
	msg, details := a.ambiguousTie()
	if details == nil {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE, msg, details)
}
