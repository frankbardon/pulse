package spss

// The SYNTHESIS front-end of the dictionary writer: a `.pulse` schema alone
// becomes a `.sav` dictionary, for the cohort that has no metadata sidecar.
//
// That cohort is not an edge case. Anything produced by synth, by a CSV
// import or by a processing run has no SPSS provenance at all, and exporting
// one to `.sav` is an ordinary thing to want. What makes it delicate is that
// the schema states LESS than an SPSS dictionary does, so every gap is a
// decision, and the wrong kind of decision here is the one that looks
// helpful.
//
// # The rule: state what the cohort knows, invent nothing that addresses data
//
// A measure level or a display width is a presentation default. Getting one
// wrong costs a column width in somebody's data editor and is recoverable by
// looking at the data. A VALUE CODE is not: SPSS syntax addresses values, so
// `IF q1 EQ 5` is a reference, and a writer that assigns 5 to whatever
// happens to sit at dictionary position 5 has silently re-pointed it.
//
// So a categorical column with no recorded codes is emitted as a STRING
// variable carrying the dictionary text, not as a numeric variable with
// invented codes and labels. The text is what the cohort actually holds; the
// codes are what it does not. This is the same reasoning that makes a stale
// sidecar an error rather than a fallback — see sidecar_read.go — applied to
// the case where there is nothing to be stale.
//
// A `set_*` column is the one place synthesis expands rather than narrows.
// SPSS has no set type, but it has the shape a set came from: N indicator
// variables and a multiple-dichotomy definition naming them. Emitting one
// indicator per dictionary entry, NAMED for that entry, is the exact inverse
// of the import that derives a set from an MD set (see mrset.go), so the
// round trip closes: entry order is bit order is member order.

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Synthesised print/write formats. SPSS has exactly two storage types, so
// every one of these is an F (plain numeric), an A (string) or a temporal
// code over the same double.
const (
	fmtA uint8 = 1 // string
	fmtF uint8 = 5 // plain numeric
)

// Format-width ceilings. The width field is a single byte either way, so
// 255 is the hard structural limit; the two ceilings differ because the
// format types do.
const (
	// numericFormatMaxWidth is the widest SPSS accepts for an F format —
	// 40 characters, far more than any double needs to print.
	numericFormatMaxWidth = 40

	// stringFormatMaxWidth is the widest an A format may state. It is the
	// same 255 a record type 2 `type` field can declare, because an A
	// format's width IS the variable's declared byte width; a very long
	// string states it per physical segment, never as its logical total.
	stringFormatMaxWidth = maxSegmentWidth
)

// synthSetCountedValue is the value a synthesised multiple-dichotomy set
// counts as "selected". 1 is what SPSS survey files use and what the import
// side parses back with no special case.
const synthSetCountedValue = 1

// synthesiseDictionary builds the emission model from the cohort schema
// alone.
func synthesiseDictionary(req DictionaryRequest) (*outFile, error) {
	f := &outFile{
		compression: req.Compression,
		bias:        writerCompressionBias,
		caseCount:   req.Cases,
		sysmis:      defaultSysmis,

		// A synthesised dictionary DECLARES its sentinels in record 7/4,
		// where a sidecar-driven one only re-emits a 7/4 the source had.
		// The two paths differ because their jobs differ: reproducing a
		// file means saying what it said, including saying nothing;
		// writing a new one means stating what this writer means, and the
		// sentinel a data section is about to be written with is exactly
		// that.
		machineFloat: &MachineFloat{
			Sysmis:  Float(defaultSysmis),
			Highest: Float(writerHighest),
			Lowest:  Float(writerLowest),
		},

		// Every synthesised variable carries a measure level and an
		// alignment, so the record is emitted.
		displayParams: true,
	}

	minter := newNameMinter()
	for i := range req.Schema.Fields {
		vars, err := synthesiseField(req.Schema, i, minter, f)
		if err != nil {
			return nil, err
		}
		f.vars = append(f.vars, vars...)
	}
	if err := checkFinalNames(f); err != nil {
		return nil, err
	}
	return f, nil
}

// synthesiseField turns one cohort field into the variable (or, for a
// `set_*` column, the variables) that carry it.
func synthesiseField(s *encoding.Schema, at int, minter *nameMinter, f *outFile) ([]*outVar, error) {
	fld := &s.Fields[at]
	if fld.Type.IsSet() {
		return synthesiseSet(s, at, minter, f)
	}

	v := &outVar{
		name:      fld.Name,
		label:     fld.Description,
		hasLabel:  fld.Description != "",
		field:     at,
		fieldName: fld.Name,
		fieldType: fld.Type,
		setBit:    -1,
	}
	if err := mintNames(v, minter, fld.Name); err != nil {
		return nil, err
	}

	switch {
	case fld.Type.HasDictionary():
		// A categorical goes out as a STRING carrying the dictionary text.
		// See the file comment: the codes are the one thing this path is
		// not entitled to invent.
		width, entries := dictionaryWidth(fld.Dictionary)
		v.width = width
		v.print = Format{Code: fmtA, Width: stringFormatWidth(width)}
		v.write = v.print
		v.measure = int32(measureNominal)
		v.align = int32(alignLeft)
		v.enc = EncodeText
		v.categories = make([]CategoryCode, len(entries))
		for id, text := range entries {
			// Known stays FALSE: this value came from the cohort's own
			// dictionary text, not from a recorded SPSS code. The plan
			// says so rather than presenting a guess as a fact.
			v.categories[id] = CategoryCode{Text: text}
		}

		// The width above is a count of UTF-8 bytes and the segments are
		// deliberately left unlaid: both are decided by applyCharsetWrite,
		// which measures the values in the charset the file is actually
		// written in and lays out the segments that measurement needs. A
		// dictionary entry is 7 bytes as UTF-8 and 6 as windows-1252, so
		// laying out a very long string here would be segmenting the wrong
		// value. widthDerived is what tells that pass this width is a
		// derivation to be recomputed rather than a source's declaration to
		// be preserved.
		v.widthDerived = true
		v.segments = nil

	case fld.Type == encoding.FieldTypeDate:
		v.print = Format{Code: fmtDATE, Width: 11}
		v.write = v.print
		v.measure = int32(measureScale)
		v.align = int32(alignRight)
		v.enc = EncodeDateDays
		v.segments = numericSegment(v.shortName)

	case fld.Type == encoding.FieldTypeDateTime:
		v.print = Format{Code: fmtDATETIME, Width: 20}
		v.write = v.print
		v.measure = int32(measureScale)
		v.align = int32(alignRight)
		v.enc = EncodeDateTimeSeconds
		v.segments = numericSegment(v.shortName)

	default:
		v.print = numericFormat(fld)
		v.write = v.print
		v.measure = numericMeasure(fld.Type)
		v.align = int32(alignRight)
		v.enc = EncodeNumeric
		v.segments = numericSegment(v.shortName)
	}

	v.displayWidth = int32(v.print.Width)
	return []*outVar{v}, nil
}

// synthesiseSet expands a `set_*` column into one indicator variable per
// dictionary entry plus the record 7/7 definition that binds them.
//
// The member's LONG NAME is the dictionary entry text, and that is not
// cosmetic: the import derives a set column's dictionary entries from its
// members' field NAMES (see planMRSet in mrset.go), so naming each member
// for its entry is what makes the mask survive a round trip bit for bit.
func synthesiseSet(s *encoding.Schema, at int, minter *nameMinter, f *outFile) ([]*outVar, error) {
	fld := &s.Fields[at]
	_, entries := dictionaryWidth(fld.Dictionary)
	if len(entries) == 0 {
		return nil, cannotExpress(fld.Name,
			"it is a set column with an empty dictionary, so there is no member variable for an SPSS multiple-response set to name")
	}

	setName := "$" + fld.Name
	counted := strconv.Itoa(synthSetCountedValue)
	members := make([]*outVar, 0, len(entries))
	shortNames := make([]string, 0, len(entries))

	for bit, text := range entries {
		v := &outVar{
			field:     at,
			fieldName: fld.Name,
			fieldType: fld.Type,
			setBit:    bit,

			// An indicator is a 0/1 numeric. It carries no value labels:
			// "0 = No, 1 = Yes" would be text the cohort never held, and
			// the entry's meaning is already in the variable's name.
			print:        Format{Code: fmtF, Width: 1},
			write:        Format{Code: fmtF, Width: 1},
			measure:      int32(measureNominal),
			align:        int32(alignRight),
			displayWidth: 1,
			enc:          EncodeSetMember,
			countedValue: synthSetCountedValue,
		}
		v.name = text
		if err := mintNames(v, minter, text); err != nil {
			return nil, err
		}
		v.segments = numericSegment(v.shortName)
		members = append(members, v)
		shortNames = append(shortNames, v.shortName)
	}

	label := fld.Description
	if label == "" {
		label = fld.Name
	}
	f.mrSets = append(f.mrSets, MRSet{
		Name:         setName,
		Kind:         MRSetKindDichotomy,
		Label:        label,
		Subtype:      extMRSets,
		Variables:    shortNames,
		CountedValue: &counted,
	})
	return members, nil
}

// ---------------------------------------------------------------------------
// Types and formats
// ---------------------------------------------------------------------------

// numericFormat picks the F format for a non-temporal, non-dictionary field.
//
// The width is chosen so the type's whole range prints without SPSS falling
// back to scientific notation. It is display metadata: the stored value is
// an IEEE double either way, so a width that is too small costs rendering
// and never a digit of data.
func numericFormat(fld *encoding.Field) Format {
	switch fld.Type {
	case encoding.FieldTypePackedBool:
		return Format{Code: fmtF, Width: 1}
	case encoding.FieldTypeU4:
		return Format{Code: fmtF, Width: 2}
	case encoding.FieldTypeU8:
		return Format{Code: fmtF, Width: 3}
	case encoding.FieldTypeU16:
		return Format{Code: fmtF, Width: 5}
	case encoding.FieldTypeU32:
		return Format{Code: fmtF, Width: 10}
	case encoding.FieldTypeU64:
		return Format{Code: fmtF, Width: 20}
	case encoding.FieldTypeDecimal128:
		// A decimal's own precision and scale are the only place in the
		// `.pulse` schema that states how a number is meant to be READ, so
		// they are carried across even though SPSS stores the value as a
		// double like any other.
		dec := int(fld.Scale)
		if dec > 16 {
			dec = 16
		}
		width := int(fld.Precision) + 2
		if width < dec+2 {
			width = dec + 2
		}
		return Format{Code: fmtF, Width: numericFormatWidth(width), Decimals: dec}
	default:
		// f32 and f64. F8.2 is SPSS's own default numeric format.
		return Format{Code: fmtF, Width: 8, Decimals: 2}
	}
}

// numericMeasure picks the record 7/11 measurement level.
//
// A boolean is nominal — it is a two-category answer, not a quantity — and
// everything else numeric is scale. The levels drive Pulse's smart defaults
// on the way back in (see defaultHints in mapping.go), so this is the one
// synthesised field that changes what an operator does by default.
func numericMeasure(ft encoding.FieldType) int32 {
	if ft == encoding.FieldTypePackedBool {
		return int32(measureNominal)
	}
	return int32(measureScale)
}

// numericFormatWidth clamps an F format's width. Clamping is safe here and
// only here: an F width is a rendering hint over a double, so a width that
// is too small costs digits on screen and never a digit of stored data.
func numericFormatWidth(w int) int { return clampWidth(w, numericFormatMaxWidth) }

// stringFormatWidth clamps an A format's width to what the one-byte field
// can carry.
//
// A logical width past 255 is NOT a truncation: such a variable is emitted
// as several physical segments, each declaring its own width, and the
// clamped 255 is exactly what the head segment declares. The value itself is
// carried in full by the segmentation — see resegment.
func stringFormatWidth(w int) int { return clampWidth(w, stringFormatMaxWidth) }

func clampWidth(w, max int) int {
	if w < 1 {
		return 1
	}
	if w > max {
		return max
	}
	return w
}

// dictionaryWidth returns the widest entry's byte length and the entries in
// ID order. The width is at least 1: SPSS has no zero-width string variable.
func dictionaryWidth(d *encoding.Dictionary) (int, []string) {
	if d == nil {
		return 1, nil
	}
	entries := d.Values()
	width := 1
	for _, e := range entries {
		if len(e) > width {
			width = len(e)
		}
	}
	return width, entries
}

// numericSegment is the single-element physical layout of a numeric.
func numericSegment(short string) []SegmentPlan {
	return []SegmentPlan{{Name: short, Width: 0, Content: 0, Elements: 1}}
}

// vlsSegmentName is the short name of segment i of a very long string.
// SPSS's own scheme, which the reader's fold accepts because it matches on
// widths and adjacency rather than on names.
func vlsSegmentName(base string, i int) string {
	if i == 0 {
		return base
	}
	suffix := strconv.Itoa(i)
	keep := shortNameLen - len(suffix)
	if keep > len(base) {
		keep = len(base)
	}
	return base[:keep] + suffix
}

// ---------------------------------------------------------------------------
// Names
// ---------------------------------------------------------------------------

// nameMinter hands out unique 8-byte record type 2 short names.
//
// Pulse field names are permissive — any UTF-8, any length, any case — and
// SPSS short names are not. The long name (record 7/13) is what carries the
// real name; this only has to produce a unique, legal 8-byte handle for the
// records that key by one.
//
// E5-S5 owns name VALIDATION — the 64-byte ceiling, the character set, the
// case-insensitive uniqueness rule, and the coded errors for each. This
// minter exists because the writer cannot emit a record type 2 at all
// without a short name; it is not that validation and does not stand in for
// it.
type nameMinter struct {
	used map[string]bool
}

func newNameMinter() *nameMinter { return &nameMinter{used: make(map[string]bool)} }

// mint derives a unique short name from want.
func (m *nameMinter) mint(want string) string {
	base := sanitiseShortName(want)
	if !m.used[base] {
		m.used[base] = true
		return base
	}
	for n := 2; ; n++ {
		suffix := strconv.Itoa(n)
		keep := shortNameLen - len(suffix)
		if keep > len(base) {
			keep = len(base)
		}
		cand := base[:keep] + suffix
		if !m.used[cand] {
			m.used[cand] = true
			return cand
		}
	}
}

// sanitiseShortName folds a Pulse name onto the record type 2 name field:
// upper case, at most 8 bytes, opening with a letter, and carrying only the
// bytes SPSS allows in a name.
func sanitiseShortName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < shortNameLen; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - 32)
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.', c == '@', c == '#', c == '$':
			b.WriteByte(c)
		default:
			// A non-ASCII or punctuation byte becomes '_' rather than
			// being dropped, so two names differing only there stay
			// different and the collision counter, not silence, resolves
			// them.
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "V"
	}
	if c := out[0]; !(c >= 'A' && c <= 'Z') && c != '@' && c != '#' && c != '$' {
		if len(out) == shortNameLen {
			out = out[:shortNameLen-1]
		}
		out = "V" + out
	}
	return out
}

// mintNames assigns a variable its short name and, where the real name
// differs, the record 7/13 long name that supersedes it.
//
// A long name is emitted whenever the Pulse name is not byte-identical to
// the minted short name — including when the only difference is CASE. The
// short name is upper-cased by construction, so without the 7/13 entry a
// field called `age` would come back as `AGE`, and a round trip that changes
// a field's name has broken every request that referenced it.
func mintNames(v *outVar, minter *nameMinter, want string) error {
	v.shortName = minter.mint(want)
	if want != v.shortName {
		if strings.ContainsAny(want, "=\t\n") {
			// '=' and tab are the record 7/13 payload's own delimiters and
			// the format gives no escape for them, so such a name cannot
			// be written down at all.
			return cannotExpress(want,
				"its name contains '=', a tab or a newline, which are the record 7/13 long-name payload's own delimiters and have no escape")
		}
		if len(want) > maxLongNameLen {
			return cannotExpress(want,
				"its name is "+strconv.Itoa(len(want))+" bytes, past the "+
					strconv.Itoa(maxLongNameLen)+"-byte ceiling SPSS puts on a variable name")
		}
		v.longName = want
	}
	return nil
}

// maxLongNameLen is the widest variable name SPSS accepts.
const maxLongNameLen = 64

// checkFinalNames rejects a plan in which two variables would answer to one
// name.
//
// It is a WRITE-side check on the synthesised path only, and it is not
// E5-S5's validation pass: it catches the collision this file can CREATE —
// a `set_*` member named for its dictionary entry landing on a name some
// other column already has — rather than auditing names in general. Two
// variables sharing a name is not survivable: record 7/13 drops the second
// silently, and the file then holds a column no name reaches.
func checkFinalNames(f *outFile) error {
	seen := make(map[string]string, len(f.vars))
	for _, v := range f.vars {
		key := strings.ToUpper(v.name)
		if prev, dup := seen[key]; dup {
			return cannotExpress(v.name,
				"another column ("+prev+") already emits a variable of that name, and SPSS variable names are "+
					"case-insensitively unique; one of the two would be unreachable")
		}
		seen[key] = v.name
	}
	return nil
}

// cannotExpress reports a cohort column that has no `.sav` representation.
//
// PULSE_SPSS_EXPORT_UNSUPPORTED is the closest existing code and its
// details already carry the offending name, but the fit is not exact — the
// code was minted for "Pulse cannot write .sav at all".
//
// E5-S4 took the one case that was really a WIDTH question — a dictionary
// entry past the 32767-byte ceiling SPSS puts on a string variable — and it
// now raises PULSE_SPSS_WIDTH_OVERFLOW from applyCharsetWrite, where the
// width is measured on the bytes that are actually written rather than on
// their UTF-8 form. What is left here is entirely about NAMES: a name
// carrying a record 7/13 delimiter, a name past 64 bytes, and two columns
// that would answer to one name. E5-S5 owns the name-validation error family
// and should reclassify all three.
func cannotExpress(field, why string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_EXPORT_UNSUPPORTED,
		"spss: the cohort column "+strconv.Quote(field)+" cannot be expressed in a .sav dictionary: "+why,
		map[string]any{errors.DetailSPSSVariable: field})
}
