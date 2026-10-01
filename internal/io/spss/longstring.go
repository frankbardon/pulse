package spss

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// Very long strings, and the two records that decorate them.
//
// # The two segmentations, which are NOT the same thing
//
// A `.sav` cuts string data twice, and the two cuts nest:
//
//  1. **The 8-byte element segmentation.** Every string variable wider than
//     eight bytes occupies several 8-byte data elements, declared by the
//     record type 2 continuation records (type field -1). One VARIABLE,
//     several ELEMENTS. dict_parse.go folds those continuations into
//     variable.segments and nothing downstream ever sees them.
//
//  2. **The very-long-string segmentation, this file.** A string wider than
//     255 bytes cannot state its width in the record type 2 `type` field at
//     all — that field tops out at 255 — so SPSS splits ONE LOGICAL variable
//     across SEVERAL PHYSICAL VARIABLES and records the join in the record
//     7/14 extension. One LOGICAL variable, several PHYSICAL variables.
//
// Layer 2 sits on top of layer 1: each physical variable of a very long
// string is itself 8-byte segmented. A 600-byte string is three physical
// variables (255 + 255 + 96 declared bytes) occupying 32 + 32 + 12 = 76
// elements. Both cuts must be undone, in that order, to recover the value.
//
// # The 252 that is not 255
//
// A non-final physical segment DECLARES 255 bytes but carries only 252 of
// the logical value; the remaining three are unused padding. That is why the
// segment count divides by 252 and not by 255, and getting it wrong shifts
// every byte after the first segment. PSPP and ReadStat both use 252.
//
// # Reassemble first, decode second
//
// The reassembly concatenates RAW BYTES and only then hands the result to
// the charset decoder. Decoding each segment on its own would corrupt any
// multi-byte character whose bytes straddle a segment boundary — a real
// fidelity bug, not a theoretical one, because the boundary falls at a fixed
// byte offset that knows nothing about character boundaries.
//
// # Retention
//
// The fold does not discard the physical layout. Each folded variable keeps
// a *vlsLayout naming every physical variable, its declared width and the
// bytes of the logical value it carried, and dictionary.veryLongStrings
// keeps the record 7/14 declarations verbatim. That is what lets a write
// path re-segment the value into the same physical variables the source
// declared.

const (
	// maxSegmentWidth is the widest string a record type 2 `type` field can
	// declare, and hence the width of every very-long-string segment but
	// the last.
	maxSegmentWidth = 255

	// segmentContentWidth is how many bytes of the LOGICAL value each
	// non-final physical segment actually carries. It is 252, not the 255
	// the segment declares: the last three bytes of a non-final segment are
	// unused. This constant is the whole segmentation arithmetic.
	segmentContentWidth = 252

	// maxVeryLongStringWidth is the widest string SPSS itself supports.
	// A record 7/14 declaring more than this is not describing a file SPSS
	// could have written.
	maxVeryLongStringWidth = 32767

	// vlsEntrySeparator separates the NAME=WIDTH entries of a record 7/14
	// payload: a NUL byte followed by a tab. Writers vary about whether the
	// final entry carries one, so the split is tolerant of both.
	vlsEntrySeparator = "\x00\t"
)

// vlsDeclaration is one record 7/14 entry: a variable's short name and the
// logical byte width its physical segments add up to.
type vlsDeclaration struct {
	// name is the record type 2 SHORT name of the HEAD physical variable.
	// Record 7/14 keys by short name, as records 7/5, 7/7 and 7/19 do.
	name string

	// width is the LOGICAL byte width: 256..32767.
	width int

	// offset is the byte offset of the owning extension record's payload,
	// kept for diagnostics.
	offset int
}

// vlsSegment is one PHYSICAL variable of a very long string.
type vlsSegment struct {
	// name is the physical variable's record type 2 short name. SPSS
	// generates these; only the first is the name a caller ever sees.
	name string

	// width is the physical variable's DECLARED byte width — 255 for every
	// segment but the last, which declares the remainder.
	width int

	// content is how many bytes of the LOGICAL value this segment carries:
	// segmentContentWidth for a non-final segment, and the final segment's
	// own declared width for the last. It is NOT width: a non-final
	// segment's last three declared bytes are unused padding.
	content int

	// elements is how many 8-byte data elements the physical variable
	// occupies — ceil(width/8), the layer-1 segmentation of this one
	// physical variable.
	elements int
}

// vlsLayout is the retained physical layout of one logical very long string.
//
// It is what E5-S4 re-segments against, so it holds the source's own shape
// rather than anything derived at read time: how many physical variables,
// their names, and their declared widths.
type vlsLayout struct {
	// width is the LOGICAL byte width the record 7/14 declared, and the
	// sum of every segment's content. It is what columnMapping.declaredWidth
	// carries for a very long string — the logical total, NOT the 255 a
	// single segment declares.
	width int

	// segments are the physical variables in file order, head first.
	// Always at least two: a one-segment "very long string" is a
	// contradiction the parse rejects.
	segments []vlsSegment
}

// elements is the total number of 8-byte data elements the whole logical
// variable occupies, padding between segments included.
func (l *vlsLayout) elements() int {
	n := 0
	for _, s := range l.segments {
		n += s.elements
	}
	return n
}

// vlsSegmentCount returns how many physical variables a string of the given
// logical byte width occupies.
func vlsSegmentCount(width int) int {
	if width <= maxSegmentWidth {
		return 1
	}
	return (width + segmentContentWidth - 1) / segmentContentWidth
}

// vlsSegmentWidth returns the DECLARED byte width of segment i (0-based) of a
// string of the given logical width: 255 for every segment but the last,
// which declares whatever the 252-byte stride has left over.
func vlsSegmentWidth(width, i int) int {
	if i < vlsSegmentCount(width)-1 {
		return maxSegmentWidth
	}
	return width - i*segmentContentWidth
}

// vlsSegmentContent returns how many bytes of the LOGICAL value segment i
// carries: the 252-byte stride for every segment but the last, and the
// remainder for the last. It is not the segment's declared width — a
// non-final segment declares 255 and carries 252.
func vlsSegmentContent(width, i int) int {
	if i < vlsSegmentCount(width)-1 {
		return segmentContentWidth
	}
	return width - i*segmentContentWidth
}

// vlsSegmentWidthOK reports whether a physical segment's DECLARED width is
// one this reader will fold under.
//
// The last segment is exact: it declares the remainder, and any other number
// means the record and the variables disagree about where the value ends. A
// non-final segment is accepted anywhere in 252..255, because every width in
// that range rounds up to the same 256-byte element span and carries the same
// 252 content bytes — the layout is identical and only the record type 2
// `type` field moves. SPSS writes 255.
func vlsSegmentWidthOK(width, i, declared int) bool {
	if i == vlsSegmentCount(width)-1 {
		return declared == vlsSegmentWidth(width, i)
	}
	return declared >= segmentContentWidth && declared <= maxSegmentWidth
}

// buildVLSLayout records the physical layout of a fold from the variables it
// actually consumed.
//
// The declared widths and the element counts come from the FILE, not from
// what the scheme would have implied: retention for a write path is only
// worth anything if it reproduces the source's own shape.
func buildVLSLayout(width int, segs []variable) *vlsLayout {
	out := make([]vlsSegment, len(segs))
	for i, v := range segs {
		out[i] = vlsSegment{
			name:     v.name,
			width:    v.width,
			content:  vlsSegmentContent(width, i),
			elements: v.segments,
		}
	}
	return &vlsLayout{width: width, segments: out}
}

// ---------------------------------------------------------------------------
// Record 7/14 — the segmentation declaration
// ---------------------------------------------------------------------------

// applyVeryLongStrings parses record 7/14 into dictionary.veryLongStrings.
//
// It only PARSES. The structural fold runs later, from foldVeryLongStrings,
// because subtypes 7/11 and 7/13 are positional over the variable list and
// the format does not promise 7/14 comes last. Splitting the two keeps the
// interpretation independent of record order, exactly as applyExtensions
// itself is.
//
// The payload is a list of NAME=WIDTH entries separated by a NUL and a tab.
func (p *parser) applyVeryLongStrings(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 1, -1) {
		return
	}
	for _, entry := range strings.Split(x.text(), vlsEntrySeparator) {
		// A trailing separator, a lone NUL terminator or a writer that
		// used only the tab all land here as empty or NUL-padded text.
		entry = strings.Trim(entry, "\x00\t")
		if strings.TrimSpace(entry) == "" {
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			p.warnVLS(d, x, x.payloadOffset, "",
				"the entry %q carries no '='; the record is a list of NAME=WIDTH pairs separated by a NUL byte and a tab", entry)
			continue
		}
		name := strings.TrimSpace(entry[:eq])
		width, err := strconv.Atoi(strings.TrimSpace(entry[eq+1:]))
		if err != nil {
			p.warnVLS(d, x, x.payloadOffset, name,
				"the entry %q does not state a decimal byte width after its '='", entry)
			continue
		}
		switch {
		case width <= maxSegmentWidth:
			p.warnVLS(d, x, x.payloadOffset, name,
				"the entry declares a width of %d, but the very-long-string scheme exists only for strings wider than %d bytes; a variable this narrow states its own width in its record type 2",
				width, maxSegmentWidth)
			continue
		case width > maxVeryLongStringWidth:
			p.warnVLS(d, x, x.payloadOffset, name,
				"the entry declares a width of %d, past the %d-byte ceiling SPSS itself imposes on a string variable",
				width, maxVeryLongStringWidth)
			continue
		}
		d.veryLongStrings = append(d.veryLongStrings, vlsDeclaration{
			name: name, width: width, offset: x.payloadOffset,
		})
	}
}

// foldVeryLongStrings collapses each record 7/14 declaration's physical
// variables into the one logical variable they encode.
//
// It runs after applyExtensions rather than inside it: 7/11 and 7/13 address
// the variable list positionally and by name, and a fold performed before
// them would move the ground under both.
//
// Every fault is a WARNING and leaves the physical variables in place. That
// is not a quiet degradation: a fold only JOINS columns that are already
// present, so refusing to fold loses no bytes at all — the caller sees the
// segments as the separate columns the dictionary literally declares, and
// PULSE_SPSS_VERY_LONG_STRING_INVALID says why.
func (p *parser) foldVeryLongStrings(d *dictionary) {
	if len(d.veryLongStrings) == 0 {
		return
	}

	// consumed marks each variable index a fold has claimed, so two
	// declarations cannot both take the same physical segment.
	consumed := make([]bool, len(d.vars))
	folds := make(map[int]*vlsLayout, len(d.veryLongStrings))

	byShort := make(map[string]int, len(d.vars))
	for i, v := range d.vars {
		byShort[strings.ToUpper(v.name)] = i
	}

	for _, decl := range d.veryLongStrings {
		head, ok := byShort[strings.ToUpper(decl.name)]
		if !ok {
			p.warnVLSDecl(d, decl,
				"no record type 2 in this dictionary declares the variable %q the entry names; the segmentation was not applied",
				decl.name)
			continue
		}
		if consumed[head] || folds[head] != nil {
			p.warnVLSDecl(d, decl,
				"the variable %q is already part of another very long string; the second segmentation was not applied", decl.name)
			continue
		}

		n := vlsSegmentCount(decl.width)
		if head+n > len(d.vars) {
			p.warnVLSDecl(d, decl,
				"the declared width %d needs %d physical variable(s) starting at %q, but only %d variable(s) follow it; the segmentation was not applied",
				decl.width, n, decl.name, len(d.vars)-head)
			continue
		}

		// Every physical segment must be a string of exactly the width
		// the scheme implies, unclaimed, and immediately adjacent in the
		// element stream. Anything else means this record does not
		// describe this dictionary, and joining on it would splice
		// unrelated columns together.
		segs := make([]variable, n)
		bad := ""
		nextIndex := d.vars[head].index
		for i := 0; i < n; i++ {
			seg := d.vars[head+i]
			switch {
			case consumed[head+i]:
				bad = "segment " + strconv.Itoa(i+1) + " (" + seg.name + ") is already part of another very long string"
			case !seg.isString():
				bad = "segment " + strconv.Itoa(i+1) + " (" + seg.name + ") is a numeric variable, not a string"
			case seg.vls != nil:
				bad = "segment " + strconv.Itoa(i+1) + " (" + seg.name + ") is itself a folded very long string"
			case !vlsSegmentWidthOK(decl.width, i, seg.width):
				bad = "segment " + strconv.Itoa(i+1) + " (" + seg.name + ") declares width " +
					strconv.Itoa(seg.width) + ", but the declared total of " + strconv.Itoa(decl.width) +
					" requires " + strconv.Itoa(vlsSegmentWidth(decl.width, i))
			case seg.index != nextIndex:
				bad = "segment " + strconv.Itoa(i+1) + " (" + seg.name + ") starts at dictionary element " +
					strconv.Itoa(int(seg.index)) + ", not the " + strconv.Itoa(int(nextIndex)) +
					" the previous segment ends at"
			}
			if bad != "" {
				break
			}
			segs[i] = seg
			nextIndex = seg.index + int32(seg.segments)
		}
		if bad != "" {
			p.warnVLSDecl(d, decl,
				"the physical variables following %q do not match the declared width %d: %s; the segmentation was not applied and its %d segment(s) import as separate columns",
				decl.name, decl.width, bad, n)
			continue
		}

		folds[head] = buildVLSLayout(decl.width, segs)
		for i := 0; i < n; i++ {
			consumed[head+i] = true
		}
	}

	if len(folds) == 0 {
		return
	}

	// Rebuild the variable list, keeping each head and dropping the
	// physical segments it swallowed. elementCount is untouched: a fold
	// changes how the case is CARVED, never how wide it is.
	out := make([]variable, 0, len(d.vars))
	skip := 0
	for i := range d.vars {
		if skip > 0 {
			skip--
			continue
		}
		v := d.vars[i]
		if layout := folds[i]; layout != nil {
			v.vls = layout
			v.width = layout.width
			v.segments = layout.elements()
			skip = len(layout.segments) - 1
		}
		out = append(out, v)
	}
	d.vars = out
}

// warnVLS records a record 7/14 diagnostic against an extension record.
func (p *parser) warnVLS(d *dictionary, x extensionRecord, off int, variable string, format string, args ...any) {
	before := len(d.warnings)
	p.warnExtension(d, errors.PULSE_SPSS_VERY_LONG_STRING_INVALID, x, off, format, args...)
	if variable != "" && len(d.warnings) > before {
		d.warnings[len(d.warnings)-1].Details[errors.DetailSPSSVariable] = variable
	}
}

// warnVLSDecl records a diagnostic against an already-parsed declaration,
// after the extension record itself has gone out of scope.
func (p *parser) warnVLSDecl(d *dictionary, decl vlsDeclaration, format string, args ...any) {
	p.warnVLS(d, extensionRecord{subtype: extVeryLongStrings, offset: decl.offset}, decl.offset, decl.name, format, args...)
}

// ---------------------------------------------------------------------------
// Record 7/21 — long string value labels
// ---------------------------------------------------------------------------

// longStringLabelSet is one record 7/21 entry, staged before binding.
type longStringLabelSet struct {
	// name is the variable the entry names. Writers disagree about
	// whether that is the record type 2 short name or the record 7/13
	// long name, so the binding tries both.
	name string

	// width is the byte width the record states for the variable.
	width int

	// labels are the (value, label) pairs in record order, which becomes
	// dictionary ID order exactly as a record type 3's does.
	labels []valueLabel

	// offset is the byte offset of the entry, for diagnostics.
	offset int
}

// applyLongStringValueLabels parses record 7/21 into a staged list.
//
// Layout, repeated to the end of the payload:
//
//	int32 name_len; byte name[name_len]
//	int32 var_width
//	int32 n_labels
//	  int32 value_len; byte value[value_len]
//	  int32 label_len; byte label[label_len]
//
// Binding waits for bindLongStringValueLabels, which runs after the record
// 7/14 fold so that an entry naming a very long string finds the LOGICAL
// variable rather than its head segment.
func (p *parser) applyLongStringValueLabels(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 1, -1) {
		return
	}
	c := &extCursor{b: x.payload, bo: p.bo}
	for !c.done() {
		start := c.off
		name, ok := c.counted()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
				"the payload ran out inside a variable name; the remaining %d byte(s) were skipped", len(x.payload)-start)
			return
		}
		width, wok := c.i32()
		count, cok := c.i32()
		if !wok || !cok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"the entry for variable %q ends before its width and label count; the remaining payload was skipped", name)
			return
		}
		if count < 0 {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
				"the entry for variable %q declares %d label(s); a count cannot be negative, so the remaining payload was skipped", name, count)
			return
		}
		set := longStringLabelSet{name: name, width: int(width), offset: x.payloadOffset + start}
		truncated := false
		for i := int32(0); i < count; i++ {
			value, vok := c.counted()
			label, lok := c.counted()
			if !vok || !lok {
				p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
					"the entry for variable %q declares %d label(s) but the payload ran out after %d; the remaining payload was skipped", name, count, i)
				truncated = true
				break
			}
			set.labels = append(set.labels, valueLabel{
				label: label, longValue: value, longString: true,
			})
		}
		if len(set.labels) > 0 {
			d.longStringLabels = append(d.longStringLabels, set)
		}
		if truncated {
			return
		}
	}
}

// bindLongStringValueLabels attaches the staged record 7/21 entries to their
// variables, as ordinary value-label sets.
//
// They join dictionary.valueLabels rather than living in a parallel slot on
// purpose: from the schema mapping's point of view a long string value label
// IS a value label, and one binding path means one place where a declared
// label can be got wrong. The set carries longString so a write path can
// still tell which record it has to come back out of — the rule is anyway
// mechanical, since a width above eight bytes cannot ride records 3/4.
func (p *parser) bindLongStringValueLabels(d *dictionary) {
	for _, set := range d.longStringLabels {
		idx, ok := d.variableByName(set.name)
		if !ok {
			p.warnLongString(d, extLongStringValueLabels, set.offset, set.name,
				"record 7/21 declares %d value label(s) for the variable %q, which this dictionary does not contain under either its short or its long name; the labels were not applied",
				len(set.labels), set.name)
			continue
		}
		v := &d.vars[idx]
		if !v.isString() {
			p.warnLongString(d, extLongStringValueLabels, set.offset, set.name,
				"record 7/21 declares value labels for %q, which is a numeric variable; long string value labels apply only to strings, so they were not applied", set.name)
			continue
		}
		if set.width != v.width {
			// Not fatal to the binding: the values are self-delimiting,
			// so a stale width costs nothing but is worth surfacing —
			// it means the record and the dictionary were written by
			// two different passes.
			p.warnLongString(d, extLongStringValueLabels, set.offset, set.name,
				"record 7/21 states width %d for %q but the dictionary declares %d; the labels were still applied, matched on their values",
				set.width, set.name, v.width)
		}
		d.valueLabels = append(d.valueLabels, valueLabelSet{
			labels:     set.labels,
			varIndices: []int32{v.index},
			width:      v.width,
			longString: true,
			offset:     set.offset,
			varsOffset: set.offset,
		})
	}
}

// ---------------------------------------------------------------------------
// Record 7/22 — long string missing values
// ---------------------------------------------------------------------------

// longStringMissing is one record 7/22 entry, staged before binding.
type longStringMissing struct {
	name   string
	values [][elementSize]byte
	offset int
}

// applyLongStringMissingValues parses record 7/22 into a staged list.
//
// Layout, repeated to the end of the payload:
//
//	int32 name_len; byte name[name_len]
//	byte  n_missing_values          (1..3)
//	  int32 value_len; byte value[value_len]     (value_len is always 8)
//
// The eight bytes are not a truncation this reader invents: SPSS compares
// only the first eight bytes of a long string against a missing value, which
// is why the format fixes the slot at eight regardless of the variable's
// width.
func (p *parser) applyLongStringMissingValues(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 1, -1) {
		return
	}
	c := &extCursor{b: x.payload, bo: p.bo}
	for !c.done() {
		start := c.off
		name, ok := c.counted()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
				"the payload ran out inside a variable name; the remaining %d byte(s) were skipped", len(x.payload)-start)
			return
		}
		n, ok := c.byteAt()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"the entry for variable %q ends before its missing-value count; the remaining payload was skipped", name)
			return
		}
		if n < 1 || n > 3 {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
				"the entry for variable %q declares %d missing value(s); the format allows 1 to 3, so the remaining payload was skipped", name, n)
			return
		}
		entry := longStringMissing{name: name, offset: x.payloadOffset + start}
		truncated := false
		for i := 0; i < int(n); i++ {
			at := c.off
			value, ok := c.counted()
			if !ok {
				p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
					"the entry for variable %q declares %d missing value(s) but the payload ran out after %d; the remaining payload was skipped", name, n, i)
				truncated = true
				break
			}
			if len(value) != elementSize {
				p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+at,
					"missing value %d of variable %q is %d byte(s); the format fixes the slot at %d, because SPSS compares only the first %d bytes of a long string, so %s",
					i+1, name, len(value), elementSize, elementSize, shortOrPadded(len(value)))
			}
			var slot [elementSize]byte
			copy(slot[:], padTo(value, elementSize))
			entry.values = append(entry.values, slot)
		}
		if len(entry.values) > 0 {
			d.longStringMissing = append(d.longStringMissing, entry)
		}
		if truncated {
			return
		}
	}
}

// shortOrPadded names what was done with an off-length record 7/22 slot.
func shortOrPadded(n int) string {
	if n < elementSize {
		return "it was padded with spaces to the full slot"
	}
	return "only the first eight bytes were kept"
}

// padTo right-pads b with spaces to n bytes, or returns its first n bytes
// when it is longer. Spaces, not NULs: 0x20 is what SPSS pads a string datum
// with, so a padded missing value still compares equal to the datum it names.
func padTo(b string, n int) []byte {
	if len(b) >= n {
		return []byte(b[:n])
	}
	return []byte(b + strings.Repeat(" ", n-len(b)))
}

// bindLongStringMissingValues attaches the staged record 7/22 entries to
// their variables' missing-value specifications.
//
// They land on variable.missing, the same slot a record type 2 spec lands
// on, because they mean the same thing — the record exists only because a
// record type 2 cannot carry a missing value for a string wider than eight
// bytes. Where a file carries both, 7/22 wins and the collision warns: a
// record type 2 missing spec on a long string is malformed by construction,
// and the record type 2 bytes are still in the dictionary either way.
func (p *parser) bindLongStringMissingValues(d *dictionary) {
	for _, entry := range d.longStringMissing {
		idx, ok := d.variableByName(entry.name)
		if !ok {
			p.warnLongString(d, extLongStringMissing, entry.offset, entry.name,
				"record 7/22 declares %d missing value(s) for the variable %q, which this dictionary does not contain under either its short or its long name; they were not applied",
				len(entry.values), entry.name)
			continue
		}
		v := &d.vars[idx]
		if !v.isString() {
			p.warnLongString(d, extLongStringMissing, entry.offset, entry.name,
				"record 7/22 declares missing values for %q, which is a numeric variable; long string missing values apply only to strings, so they were not applied", entry.name)
			continue
		}
		if v.missing.count() > 0 {
			p.warnLongString(d, extLongStringMissing, entry.offset, entry.name,
				"%q carries a record type 2 missing-value specification as well as a record 7/22 one; the record 7/22 values were used, because the record type 2 form cannot express a missing value for a %d-byte string",
				entry.name, v.width)
		}
		spec := missingSpec{code: int32(len(entry.values))}
		for _, slot := range entry.values {
			spec.raw = append(spec.raw, slot)
			spec.text = append(spec.text, strings.TrimRight(string(slot[:]), " "))
		}
		v.missing = spec
	}
}

// warnLongString records a record 7/21 or 7/22 binding diagnostic. Binding
// happens after the extension walk, so the record itself is out of scope and
// only its subtype and offset survive.
func (p *parser) warnLongString(d *dictionary, subtype int32, off int, variable string, format string, args ...any) {
	x := extensionRecord{subtype: subtype, offset: off}
	before := len(d.warnings)
	p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, off, format, args...)
	if variable != "" && len(d.warnings) > before {
		d.warnings[len(d.warnings)-1].Details[errors.DetailSPSSVariable] = variable
	}
}

// ---------------------------------------------------------------------------
// Shared
// ---------------------------------------------------------------------------

// variableByName finds a variable by the name a record 7/21 or 7/22 entry
// spells, matching case-insensitively against the record 7/13 LONG name
// first and the record type 2 short name second.
//
// Long name first because that is what the world writes: records 7/21 and
// 7/22 postdate long names, and ReadStat — the C reader behind R's haven,
// Python's pyreadstat and much else — refuses outright to parse a file whose
// 7/21 entry names a variable by its short name when a long name exists.
// A writer that did so would be producing files most of the ecosystem
// rejects, so the long name is the name these records carry.
//
// The short name is still tried, because this reader has no reason to be as
// strict as ReadStat is: a file with no record 7/13 has only short names,
// and one that used them anyway has said something unambiguous that it would
// be pure pedantry to discard.
func (d *dictionary) variableByName(name string) (int, bool) {
	want := strings.ToUpper(strings.TrimSpace(name))
	if want == "" {
		return 0, false
	}
	for i, v := range d.vars {
		if v.longName != "" && strings.ToUpper(v.longName) == want {
			return i, true
		}
	}
	for i, v := range d.vars {
		if strings.ToUpper(v.name) == want {
			return i, true
		}
	}
	return 0, false
}
