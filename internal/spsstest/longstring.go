package spsstest

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Very long strings, record 7/14, and the two records that decorate them.
//
// A string wider than [MaxStringWidth] cannot state its width in the record
// type 2 `type` field at all, so SPSS splits ONE LOGICAL variable across
// SEVERAL PHYSICAL VARIABLES and writes a record 7/14 saying how to rejoin
// them. That is a second, outer segmentation sitting on top of the ordinary
// 8-byte element segmentation every string over eight bytes already uses:
// each physical variable is itself cut into 8-byte elements.
//
// The emitter hides the outer cut from the caller. A [Spec] declares one
// [Var] with the logical width and each case carries one [Text] value; the
// expansion below turns that into the physical variables the file needs,
// splits every datum across them, and renders the record 7/14 that puts them
// back together. A reader that gets the nesting wrong reads a shifted value,
// which is exactly what these fixtures exist to catch.

// vlsSpecDecl is one record 7/14 entry the emitter will write.
type vlsSpecDecl struct {
	// name is the HEAD physical variable's short name — the same name the
	// caller declared, because segment 0 keeps it.
	name string
	// width is the logical byte width.
	width int
}

// expandVeryLongStrings rewrites a spec so that every very long string is
// replaced by the physical variables the file actually carries, and returns
// the record 7/14 declarations describing the split.
//
// It runs on an ALREADY-TRANSCODED spec, so every width it measures is a byte
// count in the file's own charset. Running it before the transcode would
// segment a Go-source string and then change its length, which is the exact
// bug the byte-count rule exists to prevent.
func expandVeryLongStrings(spec Spec) (Spec, []vlsSpecDecl, error) {
	any := false
	for _, v := range spec.Vars {
		if v.IsVeryLongString() {
			any = true
			break
		}
	}
	if !any {
		return spec, nil, nil
	}

	var decls []vlsSpecDecl
	vars := make([]Var, 0, len(spec.Vars))
	// segCounts is the per-logical-variable segment count, used to expand
	// the cases in the same order.
	segCounts := make([]int, len(spec.Vars))

	for i, v := range spec.Vars {
		if !v.IsVeryLongString() {
			segCounts[i] = 1
			vars = append(vars, v)
			continue
		}
		if v.Width > MaxVeryLongStringWidth {
			return spec, nil, fmt.Errorf("spsstest: Vars[%d] (%s) width %d exceeds %d, the widest string variable SPSS supports", i, v.Name, v.Width, MaxVeryLongStringWidth)
		}
		if !v.Print.isZero() || !v.Write.isZero() {
			return spec, nil, fmt.Errorf("spsstest: Vars[%d] (%s) is a very long string and declares a print or write format; the formats are derived per physical segment (A255, then A<remainder>), so a single caller-supplied format could not describe the file", i, v.Name)
		}

		n := VeryLongStringSegmentCount(v.Width)
		segCounts[i] = n
		decls = append(decls, vlsSpecDecl{name: v.Name, width: v.Width})
		for s := 0; s < n; s++ {
			w := VeryLongStringSegmentWidthAt(v.Width, s)
			seg := Var{
				Name:  VeryLongStringSegmentName(v.Name, s),
				Width: w,
				Print: Format{Type: FormatA, Width: w},
				Write: Format{Type: FormatA, Width: w},
				// The variable label, the long name and the display
				// parameters belong to the LOGICAL variable, so only the
				// head segment carries them. A trailing segment is an
				// implementation detail of the file and SPSS leaves it
				// undecorated.
				Measure:      v.Measure,
				DisplayWidth: v.DisplayWidth,
				Align:        v.Align,
			}
			if s == 0 {
				seg.Label = v.Label
				seg.LongName = v.LongName
			}
			vars = append(vars, seg)
		}
	}

	cases := make([][]Value, len(spec.Cases))
	for ci, row := range spec.Cases {
		if len(row) != len(spec.Vars) {
			// Left for validate to report against the caller's own
			// indices, which are still the logical ones at this point.
			return spec, nil, fmt.Errorf("spsstest: Cases[%d] has %d values but the spec declares %d variables", ci, len(row), len(spec.Vars))
		}
		out := make([]Value, 0, len(vars))
		for vi, val := range row {
			if segCounts[vi] == 1 {
				out = append(out, val)
				continue
			}
			v := spec.Vars[vi]
			if val.kind != kindText {
				return spec, nil, fmt.Errorf("spsstest: Cases[%d][%d] (%s): %s given for a very long string variable of width %d; use Text", ci, vi, v.Name, val, v.Width)
			}
			if len(val.str) > v.Width {
				return spec, nil, fmt.Errorf("spsstest: Cases[%d][%d] (%s): %s is %d bytes, over the declared width %d; widening would silently change the file's dictionary", ci, vi, v.Name, val, len(val.str), v.Width)
			}
			for _, piece := range splitVeryLongString(val.str, v.Width) {
				out = append(out, Text(piece))
			}
		}
		cases[ci] = out
	}

	spec.Vars = vars
	spec.Cases = cases
	return spec, decls, nil
}

// splitVeryLongString cuts one logical value into the content of each
// physical segment.
//
// The value is space-padded to its full declared width FIRST and only then
// cut, which is what SPSS does and what makes the cut deterministic: a
// segment boundary lands at a fixed byte offset regardless of how long the
// value happens to be. Each non-final segment then takes
// [VeryLongStringSegmentWidth] bytes — 252, not the 255 it declares — and
// the emitter's ordinary string padding fills the rest of the segment out.
func splitVeryLongString(s string, width int) []string {
	if len(s) < width {
		s += strings.Repeat(" ", width-len(s))
	}
	n := VeryLongStringSegmentCount(width)
	out := make([]string, 0, n)
	at := 0
	for i := 0; i < n; i++ {
		take := VeryLongStringSegmentContentAt(width, i)
		out = append(out, s[at:at+take])
		at += take
	}
	return out
}

// renderVeryLongStrings builds the record 7/14 payload: NAME=WIDTH entries,
// each followed by a NUL byte and a tab.
//
// The trailing separator on the LAST entry is written too. Writers differ
// about it and a reader must accept either, but emitting it is the more
// common form and the one that leaves no ambiguity about where the payload
// ends.
func renderVeryLongStrings(decls []vlsSpecDecl) string {
	var b strings.Builder
	for _, d := range decls {
		b.WriteString(d.name)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%d", d.width)
		b.WriteString("\x00\t")
	}
	return b.String()
}

// logicalVar is one caller-declared variable, kept across the expansion so
// records 7/21 and 7/22 can be validated against the LOGICAL width rather
// than against the 255 a head segment declares.
type logicalVar struct {
	Var
	index int
}

// logicalIndex builds the name lookup records 7/21 and 7/22 resolve against:
// short name first, long name second, both case-insensitive. It is the same
// two-step the reader performs, and for the same reason — writers disagree
// about which name these records carry.
func logicalIndex(vars []Var) map[string]logicalVar {
	out := make(map[string]logicalVar, len(vars)*2)
	for i, v := range vars {
		out[strings.ToUpper(v.Name)] = logicalVar{Var: v, index: i}
	}
	for i, v := range vars {
		if v.LongName == "" {
			continue
		}
		key := strings.ToUpper(v.LongName)
		if _, taken := out[key]; taken {
			continue
		}
		out[key] = logicalVar{Var: v, index: i}
	}
	return out
}

// renderLongStringValueLabels builds the record 7/21 payload.
//
// Per entry: the variable name with its byte length, the variable's declared
// width, the label count, then each (value, label) pair with its own byte
// length. The value is space-padded out to the variable's width, which is
// what the format specifies — value_len and var_width are the same number.
func renderLongStringValueLabels(spec Spec, logical map[string]logicalVar) ([]byte, error) {
	if len(spec.LongStringValueLabels) == 0 {
		return nil, nil
	}
	e := &enc{bo: binary.LittleEndian}
	for i, set := range spec.LongStringValueLabels {
		v, ok := logical[strings.ToUpper(set.Var)]
		if !ok {
			return nil, fmt.Errorf("spsstest: LongStringValueLabels[%d] names %q, which is neither the short name nor the long name of any declared variable", i, set.Var)
		}
		if !v.IsString() {
			return nil, fmt.Errorf("spsstest: LongStringValueLabels[%d] names %q, a numeric variable; record 7/21 carries value labels for strings only", i, set.Var)
		}
		if v.Width <= MaxShortStringWidth {
			return nil, fmt.Errorf("spsstest: LongStringValueLabels[%d] names %q, a %d-byte string; a string of %d bytes or fewer carries its value labels in records 3/4, so use ValueLabels", i, set.Var, v.Width, MaxShortStringWidth)
		}
		if len(set.Labels) == 0 {
			return nil, fmt.Errorf("spsstest: LongStringValueLabels[%d] (%s) declares no labels; an empty entry is not expressible", i, set.Var)
		}
		e.i32(int32(len(set.Var)))
		e.raw([]byte(set.Var))
		e.i32(int32(v.Width))
		e.i32(int32(len(set.Labels)))
		for li, l := range set.Labels {
			if len(l.Value) > v.Width {
				return nil, fmt.Errorf("spsstest: LongStringValueLabels[%d].Labels[%d] (%s) labels the value %q, which is %d bytes and over the variable's declared width %d", i, li, set.Var, l.Value, len(l.Value), v.Width)
			}
			if len(l.Label) > MaxValueLabelLen {
				return nil, fmt.Errorf("spsstest: LongStringValueLabels[%d].Labels[%d] (%s) label is %d bytes, over the %d-byte limit", i, li, set.Var, len(l.Label), MaxValueLabelLen)
			}
			padded := l.Value + strings.Repeat(" ", v.Width-len(l.Value))
			e.i32(int32(len(padded)))
			e.raw([]byte(padded))
			e.i32(int32(len(l.Label)))
			e.raw([]byte(l.Label))
		}
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.buf.Bytes(), nil
}

// renderLongStringMissingValues builds the record 7/22 payload.
//
// Per entry: the variable name with its byte length, a ONE-BYTE count, then
// each value as a length-prefixed eight-byte slot. Eight is fixed by the
// format because SPSS compares only the first eight bytes of a long string
// against a missing value, so a shorter value is space-padded and a longer
// one is a spec error rather than a silent truncation.
func renderLongStringMissingValues(spec Spec, logical map[string]logicalVar) ([]byte, error) {
	if len(spec.LongStringMissingValues) == 0 {
		return nil, nil
	}
	e := &enc{bo: binary.LittleEndian}
	for i, entry := range spec.LongStringMissingValues {
		v, ok := logical[strings.ToUpper(entry.Var)]
		if !ok {
			return nil, fmt.Errorf("spsstest: LongStringMissingValues[%d] names %q, which is neither the short name nor the long name of any declared variable", i, entry.Var)
		}
		if !v.IsString() {
			return nil, fmt.Errorf("spsstest: LongStringMissingValues[%d] names %q, a numeric variable; record 7/22 carries missing values for strings only", i, entry.Var)
		}
		if v.Width <= MaxShortStringWidth {
			return nil, fmt.Errorf("spsstest: LongStringMissingValues[%d] names %q, a %d-byte string; a string of %d bytes or fewer carries its missing values in its record type 2", i, entry.Var, v.Width, MaxShortStringWidth)
		}
		if len(entry.Values) < 1 || len(entry.Values) > MaxLongStringMissingValues {
			return nil, fmt.Errorf("spsstest: LongStringMissingValues[%d] (%s) declares %d value(s); record 7/22 allows 1 to %d", i, entry.Var, len(entry.Values), MaxLongStringMissingValues)
		}
		e.i32(int32(len(entry.Var)))
		e.raw([]byte(entry.Var))
		e.raw([]byte{byte(len(entry.Values))})
		for vi, val := range entry.Values {
			if len(val) > ElementSize {
				return nil, fmt.Errorf("spsstest: LongStringMissingValues[%d].Values[%d] (%s) is %d bytes; record 7/22 fixes the slot at %d, because SPSS compares only the first %d bytes of a long string", i, vi, entry.Var, len(val), ElementSize, ElementSize)
			}
			padded := val + strings.Repeat(" ", ElementSize-len(val))
			e.i32(int32(len(padded)))
			e.raw([]byte(padded))
		}
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.buf.Bytes(), nil
}
