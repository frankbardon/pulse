package spss

// VALUE LABELS on the synthesised path: a categorical field bound through
// [WriterOptions.ValueLabels] goes out as a NUMERIC variable carrying value
// labels, instead of the STRING variable an unbound categorical becomes.
//
// dict_synth.go's rule is that a categorical with no recorded codes is
// written as its dictionary text, because a code is a reference SPSS syntax
// addresses and inventing one re-points it. A binding is the caller asking
// for codes, so the codes here are chosen by a stated, deterministic rule
// rather than guessed — and the rule prefers the codes the cohort already
// holds:
//
//   - When EVERY dictionary entry is the canonical decimal spelling of an
//     integer a double holds exactly, the entries ARE the codes. That is the
//     SPSS-origin cohort that lost its sidecar (a Materialize before the
//     sidecar was carried, a CSV of SPSS codes): its dictionary holds "1",
//     "2", "9", and those are written back as 1, 2, 9.
//   - Otherwise, when the binding states an entry ORDER (a label table's
//     own entry order — the key order of its file), each entry's code is its
//     position in that order plus one. The code is the TABLE position, not a
//     rank among the entries present, so every cohort bound to one table
//     codes an entry alike however few of its entries it holds. Entries the
//     order does not list follow it — codes len(order)+1, … — in byte-wise
//     sorted order.
//   - With no order, the code is the entry's rank in byte-wise sorted
//     dictionary order plus one. Never the dictionary's first-seen order:
//     that is an accident of which row came first, and it would re-code a
//     variable between two exports of the same population.
//
// The entry text survives as the code's label whenever the binding has no
// label of its own for it.
//
// The plan marks every such code Known, because it is now a stated value
// rather than a gap: the data encoder writes it like a sidecar's code.
//
// An INTEGER-CODED field (u4 / u8 / u16 / u32) binds too, and needs none of
// the above: it has no dictionary and its stored integers already are the
// codes, so its variable stays the unbound numeric one and the binding's
// keys — the decimal spellings of those integers — become its value labels
// (checkIntegerKeys, labelledInteger). u64 is not integer-coded here: its
// values past 2^53 have no exact double.

import (
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
)

// maxExactCode is the largest integer magnitude every double represents
// exactly (2^53). A dictionary entry past it cannot be its own code without
// rounding, so such a dictionary takes positional codes instead.
const maxExactCode = 1 << 53

// valueLabelBindings validates the caller's value-label bindings against the
// schema and indexes them by field position.
//
// Every refusal is a schema fact, so it is raised from the dictionary build
// and `pulse export predict` reaches it through the same call.
func valueLabelBindings(s *encoding.Schema, vls []ValueLabels) (map[int]*ValueLabels, error) {
	if len(vls) == 0 {
		return nil, nil
	}
	byName := make(map[string]int, len(s.Fields))
	for i := range s.Fields {
		byName[s.Fields[i].Name] = i
	}
	out := make(map[int]*ValueLabels, len(vls))
	for i := range vls {
		b := &vls[i]
		at, ok := byName[b.Field]
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_FIELD_UNKNOWN,
				"spss: a value-label binding names "+strconv.Quote(b.Field)+
					", which is not a field of the cohort being exported",
				map[string]any{"field": b.Field, "option": "--labels"})
		}
		ft := s.Fields[at].Type
		if max, coded := integerCodeMax(ft); coded {
			if err := checkIntegerKeys(b, ft, max); err != nil {
				return nil, err
			}
		} else if !ft.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL,
				"spss: a value-label binding names "+strconv.Quote(b.Field)+", a "+ft.String()+
					" field; value labels bind to categorical_* fields, whose dictionary supplies the codes, "+
					"and to integer-coded u4 / u8 / u16 / u32 fields, whose stored integers are the codes",
				map[string]any{"field": b.Field, "type": ft.String(), "option": "--labels"})
		}
		if _, dup := out[at]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_DUPLICATE_BINDING,
				"spss: two value-label bindings name "+strconv.Quote(b.Field)+"; a variable carries one label set",
				map[string]any{"field": b.Field, "option": "--labels"})
		}
		out[at] = b
	}
	return out, nil
}

// integerCodeMax is the shared integer-coded rule (iio.IntegerLabelCodeMax):
// a row export and a `.sav` export accept the same fields.
func integerCodeMax(ft encoding.FieldType) (uint64, bool) {
	return iio.IntegerLabelCodeMax(ft)
}

// checkIntegerKeys validates a binding on an integer-coded field. Such a
// field has no dictionary, so its value labels are the binding's own
// entries: Labels must be present (nil means the table could not list
// them) and every key must be the canonical decimal spelling of a value
// the field can store (iio.CheckIntegerLabelKeys, the check a row export
// makes too).
func checkIntegerKeys(b *ValueLabels, ft encoding.FieldType, max uint64) error {
	if b.Labels == nil {
		return errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_TABLE_NOT_ENUMERABLE,
			"spss: a value-label binding names "+strconv.Quote(b.Field)+", a "+ft.String()+
				" field with no dictionary, so its value labels must be the binding's own entries; the binding lists none "+
				"(a Lookup-only label table with no Order, or a binding with no Labels)",
			map[string]any{"field": b.Field, "type": ft.String(), "option": "--labels"})
	}
	keys := make([]string, 0, len(b.Labels))
	for k := range b.Labels {
		keys = append(keys, k)
	}
	return iio.CheckIntegerLabelKeys("spss", b.Field, ft, max, keys)
}

// labelledInteger attaches b's entries to v — an integer-coded field's
// numeric variable — as value labels. The codes are the keys' integers
// (checkIntegerKeys has validated them); every other attribute of the
// variable, its data encoding included, is the unbound one. A label equal
// to the code's own spelling, or empty, is omitted, as on a categorical.
func labelledInteger(v *outVar, b *ValueLabels) {
	for k, label := range b.Labels {
		if label == "" || label == k {
			continue
		}
		n, _ := strconv.ParseUint(k, 10, 64)
		v.labels = append(v.labels, outLabel{numeric: float64(n), label: label})
	}
	// Value labels go out in code order, which is the order SPSS lists them.
	sort.SliceStable(v.labels, func(a, c int) bool { return v.labels[a].numeric < v.labels[c].numeric })
}

// sidecarWithLabels refuses a value-label binding beside an honoured
// metadata sidecar: the sidecar already carries the source's own codes and
// labels, and overriding them from a table is a choice the caller has to
// make explicitly with --ignore-sidecar.
func sidecarWithLabels(req DictionaryRequest) error {
	fields := make([]string, 0, len(req.Options.ValueLabels))
	for _, b := range req.Options.ValueLabels {
		fields = append(fields, b.Field)
	}
	details := map[string]any{"option": "--labels", "fields": fields}
	if req.Sidecar != nil {
		details[errors.DetailSPSSSidecar] = req.Sidecar.Path
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_EXPORT_UNSUPPORTED,
		"spss: --labels is not available beside the cohort's SPSS metadata sidecar, which already carries the "+
			"source's value codes and labels and is what this export writes them from; pass --ignore-sidecar to "+
			"label from the binding instead, or drop --labels to keep the source's labels",
		details)
}

// categoryCodes returns each dictionary entry's SPSS code, in ID order, and
// whether the codes are the entries' own integers. order is the binding's
// stated entry order (ValueLabels.Order), nil for none — see the file
// comment for the rule.
func categoryCodes(entries, order []string) ([]float64, bool) {
	codes := make([]float64, len(entries))
	own := len(entries) > 0
	seen := make(map[int64]bool, len(entries))
	for i, e := range entries {
		n, err := strconv.ParseInt(e, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != e || n > maxExactCode || n < -maxExactCode || seen[n] {
			own = false
			break
		}
		seen[n] = true
		codes[i] = float64(n)
	}
	if own {
		return codes, true
	}

	position := make(map[string]int, len(order))
	for i, e := range order {
		if _, dup := position[e]; !dup {
			position[e] = i
		}
	}
	rest := make([]int, 0, len(entries))
	for id, e := range entries {
		if at, ok := position[e]; ok {
			codes[id] = float64(at + 1)
			continue
		}
		rest = append(rest, id)
	}
	sort.SliceStable(rest, func(a, b int) bool { return entries[rest[a]] < entries[rest[b]] })
	for k, id := range rest {
		codes[id] = float64(len(order) + k + 1)
	}
	return codes, false
}

// labelledCategorical turns v — a categorical field's variable — into a
// numeric variable with value labels from b.
func labelledCategorical(v *outVar, fld *encoding.Field, b *ValueLabels) {
	_, entries := dictionaryWidth(fld.Dictionary)
	codes, _ := categoryCodes(entries, b.Order)

	width := 1
	v.categories = make([]CategoryCode, len(entries))
	for id, text := range entries {
		code := codes[id]
		spelled := strconv.FormatFloat(code, 'f', -1, 64)
		if len(spelled) > width {
			width = len(spelled)
		}
		v.categories[id] = CategoryCode{Code: code, Text: text, Known: true}

		label, ok := b.Labels[text]
		if !ok {
			label = text
		}
		if label == "" || label == spelled {
			continue
		}
		v.labels = append(v.labels, outLabel{numeric: code, label: label})
	}
	// Value labels go out in code order, which is the order SPSS lists them.
	sort.SliceStable(v.labels, func(a, b int) bool { return v.labels[a].numeric < v.labels[b].numeric })

	v.width = 0
	v.widthDerived = false
	v.print = Format{Code: fmtF, Width: numericFormatWidth(width)}
	v.write = v.print
	v.measure = int32(measureNominal)
	v.align = int32(alignRight)
	v.enc = EncodeCategoricalCode
	v.segments = numericSegment(v.shortName)
	v.displayWidth = int32(v.print.Width)
}
