package spss

// The SIDECAR front-end of the dictionary writer: a validated
// [Document] plus the cohort's `.pulse` schema become the intermediate
// [outFile] that dict_write.go emits.
//
// Its whole job is to put back what the file said. Almost nothing here
// decides anything — the measure levels, print and write formats, missing
// specifications, documents, attributes, response-set definitions and value
// codes are transcribed, not derived, and the few places that do decide are
// commented with why the source's own answer is not the right one to re-emit.
//
// The three deliberate departures from verbatim, all of them because
// re-emitting the source's value would produce a file that is WRONG rather
// than merely less faithful:
//
//   - Byte order, the header layout code and the record 7/3 endianness field
//     describe THESE bytes, which are always little-endian.
//   - prod_name identifies the program that wrote THESE bytes.
//   - Record 7/20 declares the charset THESE bytes are in — which, since
//     E5-S4, IS the source's, because the strings are encoded back into it
//     before emission. The one thing this file does about that is record
//     what the source's charset was, so the transcode pass can resolve
//     against it; everything else is charset_write.go's.

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// dictionaryFromSidecar builds the emission model from recorded source
// metadata.
func dictionaryFromSidecar(req DictionaryRequest) (*outFile, error) {
	doc := req.Sidecar.Document
	p := &doc.Payload
	cohort := req.Sidecar.Cohort

	byName := schemaIndex(req.Schema)

	f := &outFile{
		fileLabel:    p.Source.FileLabel,
		creationDate: p.Source.CreationDate,
		creationTime: p.Source.CreationTime,
		compression:  req.Compression,
		// The bias governs how the DATA section encodes integers, and the
		// data section here is the one this writer is about to produce, so
		// the source's recorded bias describes bytes that no longer exist.
		// 100 is the value the spec calls for and every reader assumes.
		bias:       writerCompressionBias,
		caseCount:  req.Cases,
		sysmis:     defaultSysmis,
		documents:  copyDocumentLines(p.Documents),
		productRaw: p.ProductInfo,
		fileAttrs:  p.FileAttributes,
		varAttrs:   p.VariableAttributes,
		mrSets:     p.MultipleResponseSets,
		varSets:    p.VariableSets,

		// The charset the SOURCE's strings were in. resolveWriteCharset
		// resolves the ENCODER from the same document, so this is not the
		// input to that decision; it is what the verbatim-passthrough check
		// compares against when a caller has overridden the target.
		sourceCharset: p.Charset.ResolvedName,
	}
	if p.Weight != nil {
		f.weightName = p.Weight.Variable
	}

	// Record 7/4 goes back out verbatim when the source carried one, and is
	// omitted when it did not — an absent 7/4 is what the reader reads as
	// "the spec default", which is exactly what such a file meant.
	//
	// The sentinel the DATA section must then use is chosen by the same
	// rule the reader applies (applyMachineFloat): a declared triple is
	// adopted only when it is ordered sysmis < lowest < highest. Mirroring
	// the rule here is what keeps the two halves from disagreeing about
	// which double means "missing" in a file we wrote ourselves.
	if mf := p.Source.MachineFloat; mf != nil {
		f.machineFloat = mf
		if float64(mf.Sysmis) < float64(mf.Lowest) && float64(mf.Lowest) < float64(mf.Highest) {
			f.sysmis = float64(mf.Sysmis)
		}
	}

	for i := range p.Variables {
		v, err := sidecarVariable(&p.Variables[i], req.Schema, byName, cohort, req.Sidecar.Path,
			p.Source.ByteOrder == SourceByteOrderBig)
		if err != nil {
			return nil, err
		}
		f.vars = append(f.vars, v)
	}

	sidecarMissingLabels(f, p)

	// Record 7/11 is positional over every variable, so it is all or
	// nothing: it is emitted when the source carried one for any variable,
	// and omitted when it carried none. Emitting a synthesised 7/11 for a
	// file that had none would state measure levels the source never chose.
	for i := range p.Variables {
		if p.Variables[i].HasDisplayParams {
			f.displayParams = true
			break
		}
	}
	return f, nil
}

// sidecarVariable transcribes one recorded variable.
func sidecarVariable(v *Variable, schema *encoding.Schema, byName map[string]int,
	cohort, sidecar string, sourceBigEndian bool,
) (*outVar, error) {
	out := &outVar{
		name:      v.Name,
		shortName: v.ShortName,
		longName:  v.LongName,
		label:     v.Label,
		hasLabel:  v.HasLabel,
		width:     v.DeclaredWidth,
		print:     v.PrintFormat,
		write:     v.WriteFormat,
		measure:   measureCode(v.Measure),
		align:     alignCode(v.Alignment),
		field:     -1,
		setBit:    -1,
	}
	if out.shortName == "" {
		out.shortName = v.Name
	}
	out.displayWidth = int32(v.PrintFormat.Width)
	if v.DisplayWidth != nil {
		out.displayWidth = *v.DisplayWidth
	}
	if out.displayWidth < 0 || out.displayWidth > 255 {
		// The 7/11 width field is a display hint, so a value outside what
		// the record can carry is clamped rather than refused: it costs a
		// column width in somebody's data editor, not a datum.
		out.displayWidth = int32(v.PrintFormat.Width)
	}

	out.segments = sidecarSegments(v)

	if err := sidecarMissing(out, v, cohort, sidecar, sourceBigEndian); err != nil {
		return nil, err
	}

	// Bind the cohort column. A fresh sidecar cannot name a variable this
	// cohort has no field for — the fingerprint pins the cohort's bytes —
	// so a miss here means the document does not describe this cohort and
	// no part of it can be trusted.
	at, ok := byName[strings.ToLower(v.Name)]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_INVALID,
			"spss: the metadata sidecar declares the variable "+strconv.Quote(v.Name)+
				", which this cohort's schema has no field for; the document does not describe this cohort",
			map[string]any{
				errors.DetailSPSSCohort:   cohort,
				errors.DetailSPSSSidecar:  sidecar,
				errors.DetailSPSSVariable: v.Name,
			})
	}
	out.field = at
	out.fieldName = schema.Fields[at].Name
	out.fieldType = schema.Fields[at].Type

	sidecarCategories(out, v)
	out.enc = sidecarEncoding(v)
	return out, nil
}

// sidecarSegments reproduces the physical segmentation the source used.
//
// A very long string's layout is taken from the record 7/14 shape the import
// retained rather than re-derived from the width, because reproducing the
// source's own segment names and declared widths is the whole point of
// having kept them: a re-derived layout that happens to differ would still
// read back correctly here and would still not be the file the source was.
func sidecarSegments(v *Variable) []SegmentPlan {
	if vls := v.VeryLongString; vls != nil && len(vls.Segments) > 0 {
		out := make([]SegmentPlan, 0, len(vls.Segments))
		for _, s := range vls.Segments {
			out = append(out, SegmentPlan{
				Name:     s.Name,
				Width:    s.Width,
				Content:  s.Content,
				Elements: s.Elements,
			})
		}
		return out
	}
	if v.DeclaredWidth > 0 {
		return []SegmentPlan{{
			Name:     v.ShortName,
			Width:    v.DeclaredWidth,
			Content:  v.DeclaredWidth,
			Elements: (v.DeclaredWidth + elementSize - 1) / elementSize,
		}}
	}
	return []SegmentPlan{{Name: v.ShortName, Width: 0, Content: 0, Elements: 1}}
}

// sidecarMissing transcribes the missing-value specification.
//
// The RAW eight-byte slots are what goes back on the wire, never the decoded
// floats beside them. The slots are exact by construction; a float that has
// been through JSON and back is one round trip away from the value the file
// declared, and a missing-value code that misses by an ULP silently stops
// matching the data it was written for.
//
// A string wider than eight bytes cannot carry its specification in a record
// type 2 at all — the slot is fixed at eight bytes by the format — so it
// goes out as record 7/22 instead. That is the same mechanical rule the
// import applied in reverse, which is why the document does not record which
// record a specification arrived on.
func sidecarMissing(out *outVar, v *Variable, cohort, sidecar string, sourceBigEndian bool) error {
	m := v.Missing
	if m == nil || len(m.Raw) == 0 {
		return nil
	}
	want := int(m.Code)
	if want < 0 {
		want = -want
	}
	if want != len(m.Raw) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_INVALID,
			"spss: the metadata sidecar declares n_missing_values "+strconv.Itoa(int(m.Code))+
				" for the variable "+strconv.Quote(v.Name)+" but records "+strconv.Itoa(len(m.Raw))+
				" raw slot(s); the two cannot both be right and re-emitting either would change what the variable declares missing",
			map[string]any{
				errors.DetailSPSSCohort:   cohort,
				errors.DetailSPSSSidecar:  sidecar,
				errors.DetailSPSSVariable: v.Name,
			})
	}
	// A NUMERIC slot is a flt64, so its bytes are in the SOURCE file's
	// order — and these bytes are always little-endian (see the departures
	// listed at the top of this file). Re-emitting a big-endian source's
	// slots verbatim would declare eight bytes that decode here as some
	// unrelated subnormal, so the variable would silently stop declaring
	// anything missing at all. A STRING slot is characters and has no byte
	// order, so it is never reversed.
	swap := sourceBigEndian && v.DeclaredWidth == 0

	slots := make([][elementSize]byte, 0, len(m.Raw))
	for _, raw := range m.Raw {
		var slot [elementSize]byte
		// Short slots are space-padded rather than zero-padded: 0x20 is
		// what SPSS pads a string datum with, so a padded missing value
		// still compares equal to the datum it names.
		copy(slot[:], padTo(string(raw), elementSize))
		if swap {
			for i, j := 0, elementSize-1; i < j; i, j = i+1, j-1 {
				slot[i], slot[j] = slot[j], slot[i]
			}
		}
		slots = append(slots, slot)
	}

	if v.DeclaredWidth > maxShortStringWidth {
		out.longMissing = slots
		return nil
	}
	out.missingCode = m.Code
	out.missingSlots = slots
	return nil
}

// sidecarMissingLabels puts back the value labels a plain numeric variable
// declared on its USER-MISSING codes.
//
// They are the one class of value label that does not travel through
// [Variable.Categories]. A numeric variable whose labels sit only on its
// missing codes — an income column labelled at 97/98/99 and nowhere else —
// is not a coded variable, so the import maps it to a plain f64 and moves
// the labels into the `<var>_missing` sibling's [Derived.Reasons], which is
// the only place they survive. Reading [Variable.Categories] alone therefore
// emits the file without them: the codes come back (foldRestore writes them
// into the nulls) but the file no longer says what 97 MEANT, and a
// re-import of it shows the reason column as bare numerals.
//
// So the labels are read from the registry, which is where the import put
// them, and re-emitted as ordinary records 3/4 on the source variable. Only
// a reason that recorded a label produces a pair — an unlabelled missing
// code is legal SPSS and inventing a label for it would put a string in the
// file the source never had — and the sysmis reason never does, because the
// system-missing state is not a value a record type 3 can name.
//
// It is deliberately a no-op for every other derived kind and for a
// variable that already carries categories: a categorical variable's
// missing codes ARE dictionary entries, and sidecarCategories has already
// emitted their labels.
func sidecarMissingLabels(f *outFile, p *Payload) {
	if len(p.Derived) == 0 {
		return
	}
	byField := make(map[string]*outVar, len(f.vars))
	for _, v := range f.vars {
		byField[strings.ToLower(v.fieldName)] = v
	}

	for i := range p.Derived {
		d := &p.Derived[i]
		if d.Kind != DerivedKindNumericMissing || len(d.Sources) != 1 {
			continue
		}
		out, ok := byField[strings.ToLower(d.Sources[0])]
		if !ok || len(out.categories) > 0 {
			continue
		}
		for _, r := range d.Reasons {
			if r.Sysmis || r.Label == "" || r.Code == nil {
				continue
			}
			out.labels = append(out.labels, outLabel{
				numeric: float64(*r.Code),
				label:   r.Label,
			})
		}
	}
}

// sidecarCategories projects the recorded code / label / ID triple onto the
// two things emission needs: the value labels, and the ID-indexed value
// table the data encoder writes through.
//
// Only a DECLARED label produces a record 3/4 or 7/21 pair. An entry with
// Labelled false is a code the data carried and no record type 3 named —
// perfectly legal SPSS — and inventing a label for it would put a string in
// the file the source never had. An entry with Observed false is the mirror
// case, a declared label nothing used, and it IS emitted: the source
// declared it, so the round trip owes it back.
func sidecarCategories(out *outVar, v *Variable) {
	if len(v.Categories) == 0 {
		return
	}
	maxID := uint32(0)
	for _, c := range v.Categories {
		if c.ID > maxID {
			maxID = c.ID
		}
	}
	out.categories = make([]CategoryCode, maxID+1)
	seen := make([]bool, maxID+1)

	for _, c := range v.Categories {
		if c.Labelled {
			l := outLabel{label: c.Label}
			if c.Numeric && c.Code != nil {
				l.numeric = float64(*c.Code)
			} else if c.Text != nil {
				l.text = *c.Text
			} else {
				l.text = c.Value
			}
			out.labels = append(out.labels, l)
		}

		// The FIRST entry for an ID wins and a second flags the ID
		// ambiguous. Two entries share an ID only when two distinct source
		// values collapsed onto one dictionary text
		// (PULSE_SPSS_VALUE_COLLISION); the collision is carried rather
		// than hidden, because picking between them on a write is a guess
		// about which row meant which.
		if seen[c.ID] {
			out.categories[c.ID].Ambiguous = true
			continue
		}
		seen[c.ID] = true
		entry := CategoryCode{Known: true}
		if c.Numeric && c.Code != nil {
			entry.Code = float64(*c.Code)
		}
		if c.Text != nil {
			entry.Text = *c.Text
		} else {
			entry.Text = c.Value
		}
		out.categories[c.ID] = entry
	}
}

// sidecarEncoding picks the data encoder's dispatch from what the SOURCE
// variable was, not from what the cohort field became.
//
// The source is the authority in this direction: every SPSS string maps to a
// Pulse categorical, so reading the cohort's type would answer "categorical"
// for both a coded numeric question and a free-text field, and only one of
// those two goes back out as an eight-byte double.
func sidecarEncoding(v *Variable) ValueEncoding {
	if v.DeclaredWidth > 0 || v.TypeCode > 0 {
		return EncodeText
	}
	switch v.Kind {
	case "date":
		return EncodeDateDays
	case "datetime":
		return EncodeDateTimeSeconds
	case "categorical":
		return EncodeCategoricalCode
	default:
		// "numeric" and "duration" alike, and the MOYR / QYR / WKYR
		// formats that landed on "numeric" carrying raw SPSS seconds: the
		// import converted none of them, so emission converts none of them
		// either and the retained print format is what makes them render.
		return EncodeNumeric
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// schemaIndex maps lower-cased cohort field names to their positions. SPSS
// names are case-insensitive and Pulse names are not, so the fold is on the
// SPSS side of the boundary, where it belongs.
func schemaIndex(s *encoding.Schema) map[string]int {
	out := make(map[string]int, len(s.Fields))
	for i := range s.Fields {
		key := strings.ToLower(s.Fields[i].Name)
		if _, dup := out[key]; !dup {
			out[key] = i
		}
	}
	return out
}

// copyDocumentLines returns the record type 6 lines.
//
// The import kept them untrimmed at their full fixed 80-byte width precisely
// so this step is a copy, and a copy is all it is: the length policy belongs
// to applyCharsetWrite, which measures a line AFTER encoding it and refuses
// one that overflows, and to writeDocumentRecord, which pads what is left
// out to the fixed width. Cutting a line here would be cutting UTF-8 bytes
// off a value whose width is a count of SOURCE-charset bytes — a rune-count
// rule applied to a byte-count field, which is the exact confusion E5-S4
// exists to remove.
func copyDocumentLines(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

// measureCode maps the sidecar's measure spelling onto the record 7/11 code.
func measureCode(s string) int32 {
	switch s {
	case "nominal":
		return int32(measureNominal)
	case "ordinal":
		return int32(measureOrdinal)
	case "scale":
		return int32(measureScale)
	default:
		return int32(measureUnset)
	}
}

// alignCode maps the sidecar's alignment spelling onto the record 7/11 code.
func alignCode(s string) int32 {
	switch s {
	case "right":
		return int32(alignRight)
	case "center":
		return int32(alignCenter)
	default:
		return int32(alignLeft)
	}
}
