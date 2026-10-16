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
//   - Otherwise the code is the entry's position plus one, in dictionary
//     (first-seen) order, and the entry text survives as the code's label
//     whenever the binding has no label of its own for it.
//
// The plan marks every such code Known, because it is now a stated value
// rather than a gap: the data encoder writes it like a sidecar's code.

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
		if !s.Fields[at].Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL,
				"spss: a value-label binding names "+strconv.Quote(b.Field)+", a "+s.Fields[at].Type.String()+
					" field; value labels bind to categorical_* fields only, whose dictionary supplies the codes",
				map[string]any{"field": b.Field, "type": s.Fields[at].Type.String(), "option": "--labels"})
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
// whether the codes are the entries' own integers.
func categoryCodes(entries []string) ([]float64, bool) {
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
	if !own {
		for i := range codes {
			codes[i] = float64(i + 1)
		}
	}
	return codes, own
}

// labelledCategorical turns v — a categorical field's variable — into a
// numeric variable with value labels from b.
func labelledCategorical(v *outVar, fld *encoding.Field, b *ValueLabels) {
	_, entries := dictionaryWidth(fld.Dictionary)
	codes, _ := categoryCodes(entries)

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
