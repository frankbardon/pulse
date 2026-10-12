package vectors

import (
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// reference.go is the one reading of a registered reference vector
// (pulse.Options.Extensions.ReferenceVectors) against the vector it is
// compared with, shared by predict (internal/descriptor, which sees the
// tables' labels and keys through the extensions snapshot) and the
// runtime (internal/processing, which holds the profiles too):
//
//   - ReferenceTables.Table resolves a table name
//     (PULSE_VECTOR_REFERENCE_UNKNOWN reason table_unknown);
//   - ReferenceTable.HasKey / Profile resolve a profile key
//     (reason key_unknown);
//   - ReferenceTable.Match aligns the table's labels to the compared
//     vector's — BY LABEL, never by position: the two label sets must be
//     equal (PULSE_VECTOR_REFERENCE_MISMATCH reason labels_differ), and
//     the returned permutation reorders a profile into the compared
//     vector's order.
//
// The expression form (ref("table", key)) is judged by
// ExprReferenceIssues (reference_expr.go); the comparison operators'
// `reference` param reads the same three steps.

// ReferenceTable is one registered reference-vector table.
type ReferenceTable struct {
	// Labels are the element labels, in the order each profile lists its
	// values.
	Labels []string
	// Keys are the profile keys, sorted.
	Keys []string
	// Profiles maps a key to its values, index-aligned with Labels. Nil
	// in the predict view, which carries Labels and Keys only.
	Profiles map[string][]float64
}

// ReferenceTables are the instance's reference-vector tables by name.
type ReferenceTables map[string]ReferenceTable

// NewReferenceTables copies the registered tables into the engine's
// read-only form (sorted keys; the embedder's maps and slices are not
// retained). Nil for none.
func NewReferenceTables(in map[string]types.ReferenceVectorTable) ReferenceTables {
	if len(in) == 0 {
		return nil
	}
	out := make(ReferenceTables, len(in))
	for name, t := range in {
		rt := ReferenceTable{
			Labels:   append([]string(nil), t.Labels...),
			Keys:     make([]string, 0, len(t.Profiles)),
			Profiles: make(map[string][]float64, len(t.Profiles)),
		}
		for k, v := range t.Profiles {
			rt.Keys = append(rt.Keys, k)
			rt.Profiles[k] = append([]float64(nil), v...)
		}
		sort.Strings(rt.Keys)
		out[name] = rt
	}
	return out
}

// Names returns the registered table names, sorted.
func (ts ReferenceTables) Names() []string {
	out := make([]string, 0, len(ts))
	for n := range ts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Table returns the table registered as name, or the
// PULSE_VECTOR_REFERENCE_UNKNOWN refusal (reason table_unknown, the
// registered names under "valid").
func (ts ReferenceTables) Table(name string) (ReferenceTable, *errors.CodedError) {
	if t, ok := ts[name]; ok {
		return t, nil
	}
	return ReferenceTable{}, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
		"reference vector table "+strconv.Quote(name)+" is not registered (Options.Extensions.ReferenceVectors)",
		map[string]any{"reason": ReferenceReasonTableUnknown, "table": name, "valid": ts.Names()})
}

// The PULSE_VECTOR_REFERENCE_UNKNOWN / _MISMATCH details.reason values.
const (
	// ReferenceReasonTableUnknown: no table is registered under the name.
	ReferenceReasonTableUnknown = "table_unknown"
	// ReferenceReasonTableNotLiteral: a ref() table argument is not a
	// string literal, so its labels cannot be known before a row.
	ReferenceReasonTableNotLiteral = "table_not_literal"
	// ReferenceReasonKeyUnknown: the table has no profile under the key.
	ReferenceReasonKeyUnknown = "key_unknown"
	// ReferenceReasonLabelsDiffer: the reference's labels are not the
	// compared vector's label set.
	ReferenceReasonLabelsDiffer = "labels_differ"
	// ReferenceReasonUnlabeledPartner: a ref() is compared with a vector
	// that carries no labels (a literal array, an arbitrary expression),
	// so nothing can align it by label.
	ReferenceReasonUnlabeledPartner = "unlabeled_partner"
)

// HasKey reports whether the table has a profile under key.
func (t ReferenceTable) HasKey(key string) bool {
	if t.Profiles != nil {
		_, ok := t.Profiles[key]
		return ok
	}
	i := sort.SearchStrings(t.Keys, key)
	return i < len(t.Keys) && t.Keys[i] == key
}

// KeyUnknown is the PULSE_VECTOR_REFERENCE_UNKNOWN refusal (reason
// key_unknown) of key in table name.
func (t ReferenceTable) KeyUnknown(name, key string) *errors.CodedError {
	details := map[string]any{"reason": ReferenceReasonKeyUnknown, "table": name, "key": key}
	if len(t.Keys) <= 50 {
		details["valid"] = append([]string(nil), t.Keys...)
	} else {
		details["key_count"] = len(t.Keys)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
		"reference vector table "+strconv.Quote(name)+" has no profile "+strconv.Quote(key), details)
}

// Match aligns the table's labels to labels, the labels of the vector
// the reference is compared with (named vector in details; "" when the
// compared side is not a named vector): perm[i] is the index, in the
// table, of labels[i]. The two label sets must be equal — same size, no
// label on one side only — else PULSE_VECTOR_REFERENCE_MISMATCH reason
// labels_differ with the labels only the vector has ("missing" from the
// table) and only the table has ("extra").
func (t ReferenceTable) Match(name, vector string, labels []string) ([]int, *errors.CodedError) {
	index := make(map[string]int, len(t.Labels))
	for i, l := range t.Labels {
		index[l] = i
	}
	perm := make([]int, len(labels))
	var missing []string
	used := make(map[string]bool, len(labels))
	for i, l := range labels {
		j, ok := index[l]
		if !ok || used[l] {
			missing = append(missing, l)
			continue
		}
		used[l] = true
		perm[i] = j
	}
	var extra []string
	for _, l := range t.Labels {
		if !used[l] {
			extra = append(extra, l)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return perm, nil
	}
	return nil, ReferenceMismatch(name, vector, t.Labels, labels, missing, extra)
}

// ReferenceMismatch is the PULSE_VECTOR_REFERENCE_MISMATCH refusal
// (reason labels_differ) of a reference labelled refLabels compared with
// a vector labelled labels: missing are the vector's labels the
// reference lacks, extra the reference's labels the vector lacks.
// reference names the source (a table name, or "" for an inline
// reference); vector the compared named vector ("" when unnamed).
func ReferenceMismatch(reference, vector string, refLabels, labels, missing, extra []string) *errors.CodedError {
	if missing == nil {
		missing = []string{}
	}
	if extra == nil {
		extra = []string{}
	}
	details := map[string]any{
		"reason":           ReferenceReasonLabelsDiffer,
		"reference_labels": append([]string(nil), refLabels...),
		"vector_labels":    append([]string(nil), labels...),
		"missing":          missing,
		"extra":            extra,
	}
	what := "the reference"
	if reference != "" {
		details["table"] = reference
		what = "reference vector table " + strconv.Quote(reference)
	}
	against := "the vector it is compared with"
	if vector != "" {
		details["vector"] = vector
		against = "vector " + strconv.Quote(vector)
	}
	msg := what + " labels [" + strings.Join(refLabels, ", ") + "] are not the label set of " + against +
		" [" + strings.Join(labels, ", ") + "]; a reference is matched by label, never by position"
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_MISMATCH, msg, details)
}

// Profile returns key's values reordered by perm (perm[i] indexes the
// table; nil keeps the table's own order) in a new slice; false when the
// table has no such profile or carries no values (the predict view).
func (t ReferenceTable) Profile(key string, perm []int) ([]float64, bool) {
	v, ok := t.Profiles[key]
	if !ok {
		return nil, false
	}
	if perm == nil {
		return append([]float64(nil), v...), true
	}
	out := make([]float64, len(perm))
	for i, j := range perm {
		out[i] = v[j]
	}
	return out, true
}
