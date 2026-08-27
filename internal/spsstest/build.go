package spsstest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Build renders spec into the bytes of a `.sav` system file.
//
// It is a pure function: the same spec always yields byte-identical output.
// Every input is validated first and any problem is reported as an error —
// nothing is coerced, truncated or padded into legality, because a fixture
// that quietly differs from what its author declared is worse than no fixture.
func Build(spec Spec) ([]byte, error) {
	plan, err := validate(spec)
	if err != nil {
		return nil, err
	}

	e := &enc{bo: plan.spec.ByteOrder.binary()}
	writeHeader(e, plan)
	for i := range plan.vars {
		writeVariableRecords(e, plan.vars[i])
	}
	for _, set := range plan.valueLabels {
		writeValueLabelRecords(e, set)
	}
	writeDocumentRecord(e, plan)
	writeExtensionRecords(e, plan)
	writeTerminator(e)
	writeData(e, plan)

	if e.err != nil {
		return nil, e.err
	}
	return e.buf.Bytes(), nil
}

// plan is a validated, fully-defaulted Spec: formats resolved, element
// indices computed. Emission reads only from a plan, so it contains no
// conditional logic that could differ from what validation checked.
type plan struct {
	spec Spec
	vars []resolvedVar
	// valueLabels mirrors spec.ValueLabels with variable names already
	// resolved to 1-based dictionary element indices.
	valueLabels []resolvedLabelSet
	// nominalCaseSize is the total number of 8-byte elements per case.
	nominalCaseSize int32
	// weightIndex is the 1-based dictionary element index of the weight
	// variable, or 0 when unweighted.
	weightIndex int32
	ncases      int32

	// extensions are the record type 7 payloads, already rendered and
	// already in emission order. Rendering them during validation is what
	// lets a malformed set definition be reported as a spec error rather
	// than emitted as bytes no reader can make sense of.
	extensions []renderedExtension
}

// magicFor returns the 4-byte header magic a data-section encoding requires.
func magicFor(c Compression) string {
	if c == CompressionZSAV {
		return "$FL3"
	}
	return "$FL2"
}

// zsavBlockSize is the uncompressed block size a ZSAV data section is cut
// into: the spec's own when it sets one, else the conventional 0x3ff000.
func (p plan) zsavBlockSize() int {
	if p.spec.ZSAVBlockSize > 0 {
		return p.spec.ZSAVBlockSize
	}
	return ZSAVBlockSize
}

// bias is the compression bias the header declares: the spec's own when it
// sets one, else the conventional 100. It is resolved here rather than at
// each use so the header field and the bytecode encoder can never disagree
// about the number, which would produce a file that decodes to values offset
// by a constant.
func (p plan) bias() float64 {
	if p.spec.CompressionBias != 0 {
		return p.spec.CompressionBias
	}
	return CompressionBias
}

// renderedExtension is one record type 7 ready to emit.
type renderedExtension struct {
	subtype int32
	size    int32
	payload []byte
}

type resolvedVar struct {
	Var
	// elemIndex is the 1-based dictionary index of the variable's first
	// element. The spec counts string continuation records, so this is not the
	// variable's ordinal position.
	elemIndex int32
}

type resolvedLabelSet struct {
	labels     []ValueLabel
	varIndices []int32
	// width is the width of the variables in the set: 0 for numeric, else the
	// common string width. Determines how each 8-byte value is encoded.
	width int
}

// validate checks every rule the spec imposes and returns the emission plan.
//
// The FIRST thing it does is transcode the spec into its wire charset (see
// charset.go). Everything after that point — width checks, delimiter checks,
// fixed-field checks — therefore operates on wire bytes, which is the only
// way an SPSS width check can be right: a declared width is a BYTE count,
// and a UTF-8 string authored in Go source is not the byte sequence the file
// will hold. It is also where the printable-text rule now lives, because
// only the codec knows whether a high byte is ambiguous (no record 7/20) or
// perfectly well defined (one is declared).
func validate(spec Spec) (plan, error) {
	var p plan

	cs, err := specWireCodec(spec.CharacterEncoding)
	if err != nil {
		return p, err
	}
	spec, err = transcodeSpec(spec, cs)
	if err != nil {
		return p, err
	}

	// Very long strings expand BEFORE anything else looks at the variable
	// list, so every rule below — width checks, element indices, datum
	// widths, the record 7/11 slot count — is stated about the PHYSICAL
	// variables the file carries. The caller's logical view is kept only
	// where a record needs it: records 7/21 and 7/22 state a variable's
	// declared width, and for a very long string that is the logical
	// total, not the 255 its head segment declares.
	logical := logicalIndex(spec.Vars)
	// The record-2 missing check for a very long string has to happen
	// BEFORE the expansion, not after: expansion replaces the caller's one
	// logical variable with several physical 255-byte ones, each a copy of
	// the original struct, so a Missing declared on it would silently be
	// emitted once per segment.
	for i, v := range spec.Vars {
		if v.Missing != nil && v.Width > MaxStringWidth {
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) is a %d-byte very long string with a record type 2 Missing; a record type 2 cannot carry a missing value for a string wider than %d bytes — use Spec.LongStringMissingValues, which is what record 7/22 exists for", i, v.Name, v.Width, MaxShortStringWidth)
		}
	}
	spec, vlsDecls, err := expandVeryLongStrings(spec)
	if err != nil {
		return p, err
	}
	p.spec = spec

	switch spec.ByteOrder {
	case LittleEndian, BigEndian:
	default:
		return p, fmt.Errorf("spsstest: ByteOrder %d is neither LittleEndian nor BigEndian", int(spec.ByteOrder))
	}
	switch spec.Compression {
	case CompressionNone, CompressionBytecode, CompressionZSAV:
	default:
		return p, fmt.Errorf("spsstest: %s data sections are not implemented; uncompressed, bytecode and ZSAV are supported today", spec.Compression)
	}
	if spec.ZSAVBlockSize < 0 {
		return p, fmt.Errorf("spsstest: ZSAVBlockSize %d is negative; use 0 for the conventional %d", spec.ZSAVBlockSize, ZSAVBlockSize)
	}
	if spec.CompressionBias != 0 && (math.IsNaN(spec.CompressionBias) || math.IsInf(spec.CompressionBias, 0)) {
		return p, fmt.Errorf("spsstest: compression bias %v is not a finite number", spec.CompressionBias)
	}
	if len(spec.Vars) == 0 {
		return p, fmt.Errorf("spsstest: spec declares no variables; a system file needs at least one")
	}
	if err := checkFixed("file label", spec.FileLabel, headerFileLabelLen); err != nil {
		return p, err
	}
	if err := checkFixed("product name", stringOr(spec.ProductName, DefaultProductName), headerProdNameLen); err != nil {
		return p, err
	}
	if err := checkExact("creation date", stringOr(spec.CreationDate, DefaultCreationDate), headerCreationDateLen); err != nil {
		return p, err
	}
	if err := checkExact("creation time", stringOr(spec.CreationTime, DefaultCreationTime), headerCreationTimeLen); err != nil {
		return p, err
	}

	seen := make(map[string]int, len(spec.Vars))
	byName := make(map[string]resolvedVar, len(spec.Vars))
	elem := int32(1)
	for i, v := range spec.Vars {
		if !validShortName(v.Name) {
			return p, fmt.Errorf("spsstest: Vars[%d] name %q is not a legal SPSS short name: 1..%d bytes, starting with A-Z or one of @#$, continuing with A-Z, 0-9 or one of ._@#$ (names are stored upper-case; this package will not upper-case one for you)", i, v.Name, MaxShortNameLen)
		}
		if prev, dup := seen[v.Name]; dup {
			return p, fmt.Errorf("spsstest: Vars[%d] repeats the name %q already used by Vars[%d]", i, v.Name, prev)
		}
		seen[v.Name] = i

		if v.Width < 0 {
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) has negative width %d; use 0 for numeric", i, v.Name, v.Width)
		}
		if v.Width > MaxStringWidth {
			// Unreachable for a caller-declared variable: a width over
			// MaxStringWidth was already expanded into physical segments
			// of at most MaxStringWidth. It stays as defence in depth,
			// because a record type 2 cannot express a wider type field.
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) width %d exceeds %d, the widest a record type 2 type field can express", i, v.Name, v.Width, MaxStringWidth)
		}
		if len(v.Label) > MaxVarLabelLen {
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) label is %d bytes, over the %d-byte limit", i, v.Name, len(v.Label), MaxVarLabelLen)
		}

		rv := resolvedVar{Var: v, elemIndex: elem}
		if rv.Print.isZero() {
			rv.Print = defaultFormat(v)
		}
		if rv.Write.isZero() {
			rv.Write = rv.Print
		}
		if err := checkFormat(i, v.Name, rv.Print, "print"); err != nil {
			return p, err
		}
		if err := checkFormat(i, v.Name, rv.Write, "write"); err != nil {
			return p, err
		}
		if err := checkMissingValues(v.Missing, v); err != nil {
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) Missing: %w", i, v.Name, err)
		}

		p.vars = append(p.vars, rv)
		byName[v.Name] = rv
		elem += int32(v.segments())
	}
	p.nominalCaseSize = elem - 1

	if spec.WeightVar != "" {
		wv, ok := byName[spec.WeightVar]
		if !ok {
			return p, fmt.Errorf("spsstest: WeightVar %q names no declared variable", spec.WeightVar)
		}
		if wv.IsString() {
			return p, fmt.Errorf("spsstest: WeightVar %q is a string variable; only numeric variables can weight cases", spec.WeightVar)
		}
		p.weightIndex = wv.elemIndex
	}

	for si, set := range spec.ValueLabels {
		rs, err := resolveLabelSet(si, set, byName)
		if err != nil {
			return p, err
		}
		p.valueLabels = append(p.valueLabels, rs)
	}

	for ci, row := range spec.Cases {
		if len(row) != len(spec.Vars) {
			return p, fmt.Errorf("spsstest: Cases[%d] has %d values but the spec declares %d variables", ci, len(row), len(spec.Vars))
		}
		for vi, val := range row {
			if err := checkDatum(val, spec.Vars[vi]); err != nil {
				return p, fmt.Errorf("spsstest: Cases[%d][%d] (%s): %w", ci, vi, spec.Vars[vi].Name, err)
			}
		}
	}

	p.ncases = int32(len(spec.Cases))
	if spec.UnknownCaseCount {
		p.ncases = -1
	}

	for i, line := range spec.Documents {
		if len(line) > DocumentLineLen {
			return p, fmt.Errorf("spsstest: Documents[%d] is %d bytes, over the %d-byte document line width; a document line is a fixed-width field, and wrapping it here would invent a line the caller did not write", i, len(line), DocumentLineLen)
		}
	}

	ext, err := planExtensions(spec, p.vars, byName, vlsDecls, logical)
	if err != nil {
		return p, err
	}
	p.extensions = ext
	return p, nil
}

// planExtensions renders every record type 7 the spec asks for, in ascending
// subtype order, with RawExtensions last. The order is fixed rather than
// caller-controlled so output stays byte-deterministic.
func planExtensions(spec Spec, vars []resolvedVar, byName map[string]resolvedVar,
	vlsDecls []vlsSpecDecl, logical map[string]logicalVar,
) ([]renderedExtension, error) {
	var out []renderedExtension

	// Extension payloads carry int32s and int64s, so they are as
	// byte-ordered as the header is. Rendering them little-endian inside a
	// big-endian file would produce records that frame correctly and decode
	// to nonsense — the exact failure a whole-file byte order exists to
	// avoid.
	bo := spec.ByteOrder.binary()

	if mi := spec.MachineIntegerInfo; mi != nil {
		e := &enc{bo: bo}
		for _, v := range []int32{mi.VersionMajor, mi.VersionMinor, mi.VersionRevision,
			mi.MachineCode, mi.FloatingPointRep, mi.CompressionCode, mi.Endianness, mi.CharacterCode} {
			e.i32(v)
		}
		out = append(out, renderedExtension{subtype: SubtypeMachineInteger, size: 4, payload: e.buf.Bytes()})
	}

	if mf := spec.MachineFloatInfo; mf != nil {
		e := &enc{bo: bo}
		e.f64(mf.SysMis)
		e.f64(mf.Highest)
		e.f64(mf.Lowest)
		out = append(out, renderedExtension{subtype: SubtypeMachineFloat, size: 8, payload: e.buf.Bytes()})
	}

	if text, err := renderSetRecord(spec, SubtypeVariableSets, byName); err != nil {
		return nil, err
	} else if text != "" {
		out = append(out, renderedExtension{subtype: SubtypeVariableSets, size: 1, payload: []byte(text)})
	}

	if text, err := renderSetRecord(spec, SubtypeMRSets, byName); err != nil {
		return nil, err
	} else if text != "" {
		out = append(out, renderedExtension{subtype: SubtypeMRSets, size: 1, payload: []byte(text)})
	}

	if spec.DisplayParams {
		e := &enc{bo: bo}
		for _, v := range vars {
			if v.Measure < MeasureUnset || v.Measure > MeasureScale {
				return nil, fmt.Errorf("spsstest: %s has Measure %d, outside the 0..3 the record 7/11 measure field defines", v.Name, v.Measure)
			}
			if v.Align < AlignLeft || v.Align > AlignCenter {
				return nil, fmt.Errorf("spsstest: %s has Align %d, outside the 0..2 the record 7/11 alignment field defines", v.Name, v.Align)
			}
			width := v.DisplayWidth
			if width == 0 {
				width = v.Print.Width
			}
			if width < 0 || width > 255 {
				return nil, fmt.Errorf("spsstest: %s has DisplayWidth %d, outside 0..255", v.Name, width)
			}
			e.i32(int32(v.Measure))
			if !spec.OmitDisplayWidth {
				e.i32(int32(width))
			}
			e.i32(int32(v.Align))
		}
		out = append(out, renderedExtension{subtype: SubtypeDisplayParams, size: 4, payload: e.buf.Bytes()})
	}

	if text, err := renderLongNames(vars); err != nil {
		return nil, err
	} else if text != "" {
		out = append(out, renderedExtension{subtype: SubtypeLongNames, size: 1, payload: []byte(text)})
	}

	if len(vlsDecls) > 0 {
		out = append(out, renderedExtension{
			subtype: SubtypeVeryLongStrings, size: 1,
			payload: []byte(renderVeryLongStrings(vlsDecls)),
		})
	}

	if spec.CaseCount64 != nil {
		if *spec.CaseCount64 < 0 {
			return nil, fmt.Errorf("spsstest: CaseCount64 is %d; a case count cannot be negative", *spec.CaseCount64)
		}
		e := &enc{bo: bo}
		e.i64(1) // the spec's constant leading field, and an endianness probe
		e.i64(*spec.CaseCount64)
		out = append(out, renderedExtension{subtype: SubtypeNumberOfCases, size: 8, payload: e.buf.Bytes()})
	}

	if spec.FileAttributes != "" {
		if err := checkExtensionText("FileAttributes", spec.FileAttributes); err != nil {
			return nil, err
		}
		out = append(out, renderedExtension{subtype: SubtypeFileAttributes, size: 1, payload: []byte(spec.FileAttributes)})
	}
	if spec.VarAttributes != "" {
		if err := checkExtensionText("VarAttributes", spec.VarAttributes); err != nil {
			return nil, err
		}
		out = append(out, renderedExtension{subtype: SubtypeVarAttributes, size: 1, payload: []byte(spec.VarAttributes)})
	}

	if text, err := renderSetRecord(spec, SubtypeMRSetsExtended, byName); err != nil {
		return nil, err
	} else if text != "" {
		out = append(out, renderedExtension{subtype: SubtypeMRSetsExtended, size: 1, payload: []byte(text)})
	}

	if spec.CharacterEncoding != "" {
		if !isASCIIPrintable(spec.CharacterEncoding) {
			return nil, fmt.Errorf("spsstest: CharacterEncoding %q is not printable 7-bit ASCII; a charset name that needs a charset to read is a contradiction", spec.CharacterEncoding)
		}
		out = append(out, renderedExtension{subtype: SubtypeCharacterEncoding, size: 1, payload: []byte(spec.CharacterEncoding)})
	}

	if payload, err := renderLongStringValueLabels(spec, logical, bo); err != nil {
		return nil, err
	} else if len(payload) > 0 {
		out = append(out, renderedExtension{subtype: SubtypeLongStringValueLabels, size: 1, payload: payload})
	}

	if payload, err := renderLongStringMissingValues(spec, logical, bo); err != nil {
		return nil, err
	} else if len(payload) > 0 {
		out = append(out, renderedExtension{subtype: SubtypeLongStringMissing, size: 1, payload: payload})
	}

	for i, raw := range spec.RawExtensions {
		size := raw.Size
		if size == 0 {
			size = 1
		}
		if size < 0 {
			return nil, fmt.Errorf("spsstest: RawExtensions[%d] declares element size %d; it cannot be negative", i, size)
		}
		if len(raw.Payload)%int(size) != 0 {
			return nil, fmt.Errorf("spsstest: RawExtensions[%d] has a %d-byte payload, which is not a multiple of its %d-byte element size; the count field is derived from the two and would not describe the bytes", i, len(raw.Payload), size)
		}
		out = append(out, renderedExtension{subtype: raw.Subtype, size: size, payload: append([]byte(nil), raw.Payload...)})
	}
	return out, nil
}

// checkExtensionText validates a free-text extension payload. Newlines are
// legal here — the attribute records are line-structured — so the check is
// printable ASCII plus \n and \t, not the stricter isASCIIPrintable.
func checkExtensionText(what, s string) error {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\t' {
			continue
		}
		if c < 0x20 || c > 0x7E {
			return fmt.Errorf("spsstest: %s contains byte 0x%02X at index %d; extension text must be printable 7-bit ASCII, tab or newline", what, c, i)
		}
	}
	return nil
}

// renderLongNames builds the record 7/13 payload: TAB-separated SHORT=Long
// pairs, no trailing tab.
func renderLongNames(vars []resolvedVar) (string, error) {
	var parts []string
	for _, v := range vars {
		if v.LongName == "" {
			continue
		}
		if strings.ContainsAny(v.LongName, "=\t\n") {
			return "", fmt.Errorf("spsstest: %s has LongName %q containing '=', a tab or a newline; those are the record 7/13 payload's own delimiters and there is no escape for them", v.Name, v.LongName)
		}
		if len(v.LongName) > MaxLongNameLen {
			return "", fmt.Errorf("spsstest: %s has a %d-byte LongName, over the %d-byte SPSS limit", v.Name, len(v.LongName), MaxLongNameLen)
		}
		parts = append(parts, v.Name+"="+v.LongName)
	}
	return strings.Join(parts, "\t"), nil
}

// renderSetRecord builds the text payload of one of the three set-carrying
// subtypes: 5, 7 and 19. Subtype 5 additionally carries any VariableSets,
// which are emitted after the response sets.
func renderSetRecord(spec Spec, subtype int32, byName map[string]resolvedVar) (string, error) {
	var b strings.Builder
	for i, set := range spec.MultipleResponseSets {
		st := set.Subtype
		if st == 0 {
			st = SubtypeMRSets
		}
		switch st {
		case SubtypeVariableSets, SubtypeMRSets, SubtypeMRSetsExtended:
		default:
			return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) declares subtype %d; a response set rides record 7/5, 7/7 or 7/19", i, set.Name, st)
		}
		if st != subtype {
			continue
		}
		text, err := renderMRSet(i, set, byName)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}

	if subtype == SubtypeVariableSets {
		for i, vs := range spec.VariableSets {
			if vs.Name == "" || strings.HasPrefix(vs.Name, "$") {
				return "", fmt.Errorf("spsstest: VariableSets[%d] name %q is empty or begins with '$'; a leading '$' is what marks a multiple-response set, so a variable set carrying one would be indistinguishable from one", i, vs.Name)
			}
			if len(vs.Vars) == 0 {
				return "", fmt.Errorf("spsstest: VariableSets[%d] (%s) names no variables", i, vs.Name)
			}
			for _, name := range vs.Vars {
				if _, ok := byName[name]; !ok {
					return "", fmt.Errorf("spsstest: VariableSets[%d] (%s) names no declared variable: %q", i, vs.Name, name)
				}
			}
			b.WriteString(vs.Name + "=")
			for _, name := range vs.Vars {
				b.WriteString(" " + name)
			}
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// renderMRSet builds one multiple-response set definition line.
//
// The grammar, from the specification:
//
//	name '=' ( 'C' ' ' | 'D' counted | 'E' ' ' ('1'|'11') ' ' counted )
//	         ' ' counted-label ( ' ' varname )* '\n'
//
// where a counted string is a decimal byte length, a space, and that many
// bytes.
func renderMRSet(i int, set MRSet, byName map[string]resolvedVar) (string, error) {
	if !strings.HasPrefix(set.Name, "$") {
		return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] name %q does not begin with '$'; SPSS requires it, and it is the only thing separating a response set from a plain variable set inside record 7/5", i, set.Name)
	}
	if strings.ContainsAny(set.Name, "= \n") {
		return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] name %q contains '=', a space or a newline, which are the payload's delimiters", i, set.Name)
	}
	if len(set.Vars) == 0 {
		return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) names no variables", i, set.Name)
	}
	for _, name := range set.Vars {
		if _, ok := byName[name]; !ok {
			return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) names no declared variable: %q", i, set.Name, name)
		}
	}

	var b strings.Builder
	b.WriteString(set.Name)
	b.WriteString("=")
	switch set.Kind {
	case MRCategory:
		if set.CountedValue != "" {
			return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) is a multiple-category set but declares a CountedValue; only a dichotomy has one, and the wire form has nowhere to put it", i, set.Name)
		}
		if set.Extended {
			return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) is a multiple-category set but sets Extended; the 'E' form is a dichotomy form", i, set.Name)
		}
		b.WriteString("C ")
	case MRDichotomy:
		if set.CountedValue == "" {
			return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) is a multiple-dichotomy set with no CountedValue; without it nothing says which value counts as selected", i, set.Name)
		}
		if set.Extended {
			b.WriteString("E ")
			if set.LabelFromVarLabel {
				b.WriteString("11 ")
			} else {
				b.WriteString("1 ")
			}
		} else {
			if set.LabelFromVarLabel {
				return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) sets LabelFromVarLabel without Extended; the label source is only expressible in the 'E' form", i, set.Name)
			}
			b.WriteString("D")
		}
		b.WriteString(countedString(set.CountedValue))
		b.WriteString(" ")
	default:
		return "", fmt.Errorf("spsstest: MultipleResponseSets[%d] (%s) has no Kind; use MRDichotomy or MRCategory", i, set.Name)
	}
	b.WriteString(countedString(set.Label))
	for _, name := range set.Vars {
		b.WriteString(" " + name)
	}
	b.WriteString("\n")
	return b.String(), nil
}

// countedString renders the format's counted-string form: a decimal byte
// length, a space, then the bytes.
func countedString(s string) string {
	return strconv.Itoa(len(s)) + " " + s
}

// resolveLabelSet validates one record 3/4 pair and resolves its variable
// names to dictionary element indices.
func resolveLabelSet(si int, set ValueLabelSet, byName map[string]resolvedVar) (resolvedLabelSet, error) {
	var rs resolvedLabelSet
	if len(set.Vars) == 0 {
		return rs, fmt.Errorf("spsstest: ValueLabels[%d] names no variables; a record type 4 must name at least one", si)
	}
	if len(set.Labels) == 0 {
		return rs, fmt.Errorf("spsstest: ValueLabels[%d] carries no labels; an empty record type 3 is not useful", si)
	}

	width := -1
	for vi, name := range set.Vars {
		v, ok := byName[name]
		if !ok {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d].Vars[%d] names no declared variable: %q", si, vi, name)
		}
		if v.Width > MaxShortStringWidth {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d] applies to %q, a %d-byte string; strings wider than %d need record 7/21 long string value labels, which is out of scope", si, name, v.Width, MaxShortStringWidth)
		}
		if width == -1 {
			width = v.Width
		} else if v.Width != width {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d] mixes variable widths (%d and %d); every variable in one record type 4 must have the same type and width", si, width, v.Width)
		}
		rs.varIndices = append(rs.varIndices, v.elemIndex)
	}
	rs.width = width

	// A synthetic Var standing in for the set's common type, so a value label
	// is checked by exactly the same rule as a data datum.
	proto := Var{Name: set.Vars[0], Width: width}
	for li, l := range set.Labels {
		if l.Label == "" || len(l.Label) > MaxValueLabelLen {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d].Labels[%d] is %d bytes; a value label must be 1..%d bytes", si, li, len(l.Label), MaxValueLabelLen)
		}
		if l.Value.kind == kindSysMis {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d].Labels[%d] labels the system-missing value; SPSS labels user-missing codes instead", si, li)
		}
		if err := checkDatum(l.Value, proto); err != nil {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d].Labels[%d]: %w", si, li, err)
		}
	}
	rs.labels = set.Labels
	return rs, nil
}

// checkDatum validates one Value against the variable it belongs to.
func checkDatum(val Value, v Var) error {
	switch val.kind {
	case kindInvalid:
		return fmt.Errorf("uninitialised Value; use Num, Text or SysMis")
	case kindNum:
		if v.IsString() {
			return fmt.Errorf("%s given for string variable of width %d; use Text", val, v.Width)
		}
		if math.IsNaN(val.num) {
			return fmt.Errorf("NaN is not representable; use SysMis for the system-missing sentinel")
		}
	case kindText:
		if !v.IsString() {
			return fmt.Errorf("%s given for numeric variable; use Num", val)
		}
		if len(val.str) > v.Width {
			return fmt.Errorf("%s is %d bytes, over the declared width %d; widening would silently change the file's dictionary", val, len(val.str), v.Width)
		}
	case kindSysMis:
		if v.IsString() {
			return fmt.Errorf("SysMis() given for string variable; SPSS has no system-missing state for strings, use Text(\"\") for an all-spaces value")
		}
	}
	return nil
}

// checkMissingValues validates one record type 2 missing-value
// specification against the variable that declares it.
//
// Nothing is coerced. A specification that cannot be written exactly as
// declared is an error, because a fixture whose missing spec quietly
// differs from what its author wrote is worse than no fixture — the whole
// point of one is to be the known-good answer a reader is checked against.
func checkMissingValues(m *MissingValues, v Var) error {
	if m == nil {
		return nil
	}
	if m.Range == nil && len(m.Discrete) == 0 {
		return fmt.Errorf("declares neither a range nor a discrete value; use a nil *MissingValues for a variable with no missing-value specification")
	}

	if m.Range != nil {
		if v.IsString() {
			return fmt.Errorf("declares a lo..hi range on a string variable of width %d; the format has no range form for strings, and n_missing_values would have to be negative to express one", v.Width)
		}
		if math.IsNaN(m.Range.Low) || math.IsNaN(m.Range.High) {
			return fmt.Errorf("range bound is NaN; a NaN bound can never compare true against a datum, so the range would match nothing")
		}
		if m.Range.Low > m.Range.High {
			return fmt.Errorf("range low %v is above high %v; the bounds are inclusive and ordered, and an inverted pair matches nothing", m.Range.Low, m.Range.High)
		}
		if len(m.Discrete) > 1 {
			return fmt.Errorf("declares a range plus %d discrete value(s); the format's range-plus-discrete form (n_missing_values -3) carries exactly one", len(m.Discrete))
		}
	} else if len(m.Discrete) > MaxDiscreteMissingValues {
		return fmt.Errorf("declares %d discrete missing value(s), over the %d a record type 2 can carry; a wider vocabulary needs a range", len(m.Discrete), MaxDiscreteMissingValues)
	}

	for i, val := range m.Discrete {
		if val.kind == kindSysMis {
			return fmt.Errorf("slot Discrete[%d] is SysMis(); the system-missing state is not a user-missing code, and a slot holding the sentinel could never be told apart from a sysmis datum", i)
		}
		if err := checkDatum(val, v); err != nil {
			return fmt.Errorf("slot Discrete[%d]: %w", i, err)
		}
		// The slot is eight bytes whatever the variable declares,
		// because that is what the format fixes it at. A value that does
		// not fit is rejected rather than cut: a silently truncated
		// missing code would compare equal to a different datum.
		if val.kind == kindText && len(val.str) > MaxShortStringWidth {
			return fmt.Errorf("slot Discrete[%d] (%s) is %d bytes, over the %d a record type 2 missing-value slot holds; a wider string's missing values ride record 7/22 (Spec.LongStringMissingValues)", i, val, len(val.str), MaxShortStringWidth)
		}
	}
	return nil
}

// defaultFormat derives the print format for a variable that declared none:
// F8.2 for numeric (the SPSS default), A<width> for string.
func defaultFormat(v Var) Format {
	if v.IsString() {
		return Format{Type: FormatA, Width: v.Width}
	}
	return Format{Type: FormatF, Width: 8, Decimals: 2}
}

func checkFormat(i int, name string, f Format, which string) error {
	if f.Width < 1 || f.Width > 255 {
		return fmt.Errorf("spsstest: Vars[%d] (%s) %s format width %d is outside 1..255", i, name, which, f.Width)
	}
	if f.Decimals < 0 || f.Decimals > 255 {
		return fmt.Errorf("spsstest: Vars[%d] (%s) %s format decimal count %d is outside 0..255", i, name, which, f.Decimals)
	}
	return nil
}

// checkFixed and checkExact see WIRE bytes: validate transcodes the spec
// before it checks anything, so both the length and the printability rules
// below are statements about the file and not about the Go source. That is
// why they use isWirePrintable rather than isASCIIPrintable — a high byte
// here has already been proved to be the declared charset's encoding of a
// printable character, and the ASCII gate for an undeclared charset has
// already run.
func checkFixed(what, s string, n int) error {
	if len(s) > n {
		return fmt.Errorf("spsstest: %s is %d bytes, over the %d-byte header field", what, len(s), n)
	}
	if !isWirePrintable(s) {
		return fmt.Errorf("spsstest: %s is not printable 7-bit ASCII", what)
	}
	return nil
}

func checkExact(what, s string, n int) error {
	if len(s) != n {
		return fmt.Errorf("spsstest: %s must be exactly %d bytes, got %d (%q)", what, n, len(s), s)
	}
	if !isWirePrintable(s) {
		return fmt.Errorf("spsstest: %s is not printable 7-bit ASCII", what)
	}
	return nil
}

func stringOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ---------------------------------------------------------------------------
// Emission
// ---------------------------------------------------------------------------

// writeHeader emits the 176-byte file header record.
func writeHeader(e *enc, p plan) {
	// "$FL3" is the format's mark for a zlib-compressed file and "$FL2"
	// covers the other two encodings. It is derived from the compression
	// choice rather than declared, so the magic and the compression field
	// can never disagree.
	e.ascii(magicFor(p.spec.Compression), headerRecTypeLen)
	e.ascii(stringOr(p.spec.ProductName, DefaultProductName), headerProdNameLen)
	e.i32(2) // layout_code: written in file byte order, so it doubles as the endianness probe
	e.i32(p.nominalCaseSize)
	e.i32(int32(p.spec.Compression))
	e.i32(p.weightIndex)
	e.i32(p.ncases)
	e.f64(p.bias()) // written even when uncompressed, as PSPP does
	e.ascii(stringOr(p.spec.CreationDate, DefaultCreationDate), headerCreationDateLen)
	e.ascii(stringOr(p.spec.CreationTime, DefaultCreationTime), headerCreationTimeLen)
	e.ascii(p.spec.FileLabel, headerFileLabelLen)
	e.zeros(headerPaddingLen)
}

// writeVariableRecords emits the variable's record type 2 plus one
// continuation record per 8 bytes of string width past the first 8.
func writeVariableRecords(e *enc, v resolvedVar) {
	varType := int32(v.Width) // 0 for numeric, byte width for string
	hasLabel := int32(0)
	if v.Label != "" {
		hasLabel = 1
	}

	e.i32(recTypeVariable)
	e.i32(varType)
	e.i32(hasLabel)
	e.i32(v.Missing.code()) // 0, 1..3, -2 or -3; derived, never declared
	e.i32(v.Print.pack())
	e.i32(v.Write.pack())
	e.ascii(v.Name, shortNameLen)

	if hasLabel == 1 {
		// label_len carries the true byte length; the payload is padded out to
		// a multiple of 4 bytes with zeros. Readers must use label_len, not
		// the padded size, to recover the text.
		e.i32(int32(len(v.Label)))
		e.raw([]byte(v.Label))
		e.zeros(roundUp(len(v.Label), 4) - len(v.Label))
	}

	// The missing-value slots come AFTER the label, not before it: the
	// label is length-prefixed and 4-byte aligned, so a reader that swapped
	// the two would desynchronise on the first labelled variable that also
	// declares missing values.
	writeMissingValueSlots(e, v.Missing)

	for seg := 1; seg < v.segments(); seg++ {
		e.i32(recTypeVariable)
		e.i32(typeStringContinuation)
		e.i32(0) // has_var_label
		e.i32(0) // n_missing_values
		e.i32(0) // print
		e.i32(0) // write
		e.ascii("", shortNameLen)
	}
}

// writeMissingValueSlots emits the abs(n_missing_values) eight-byte slots a
// missing-value specification occupies: the range bounds first when there is
// a range, then the discrete values in declaration order.
//
// A string value occupies the same eight bytes, space-padded — the slot
// width is fixed by the format regardless of the variable's declared width,
// which is why a record type 2 cannot carry one for a wider string at all.
func writeMissingValueSlots(e *enc, m *MissingValues) {
	if m == nil {
		return
	}
	if m.Range != nil {
		e.f64(m.Range.Low)
		e.f64(m.Range.High)
	}
	for _, val := range m.Discrete {
		if val.kind == kindText {
			e.ascii(val.str, ElementSize)
			continue
		}
		e.f64(val.num)
	}
}

// writeValueLabelRecords emits one record type 3 immediately followed by the
// record type 4 that binds it to variables. The spec requires that pairing and
// that adjacency.
func writeValueLabelRecords(e *enc, set resolvedLabelSet) {
	e.i32(recTypeValueLabel)
	e.i32(int32(len(set.labels)))
	for _, l := range set.labels {
		if set.width == 0 {
			e.f64(l.Value.num)
		} else {
			// A short-string value occupies the same 8 bytes, space-padded.
			e.ascii(l.Value.str, ElementSize)
		}
		// A one-byte length, then the text, then zero padding so that the
		// length byte and the text together fill a multiple of 8 bytes.
		e.raw([]byte{byte(len(l.Label))})
		e.raw([]byte(l.Label))
		e.zeros(roundUp(len(l.Label)+1, ElementSize) - (len(l.Label) + 1))
	}

	e.i32(recTypeLabelVars)
	e.i32(int32(len(set.varIndices)))
	for _, idx := range set.varIndices {
		e.i32(idx)
	}
}

// writeDocumentRecord emits record type 6: a line count, then that many
// fixed-width space-padded lines.
func writeDocumentRecord(e *enc, p plan) {
	if len(p.spec.Documents) == 0 {
		return
	}
	e.i32(recTypeDocument)
	e.i32(int32(len(p.spec.Documents)))
	for _, line := range p.spec.Documents {
		e.ascii(line, DocumentLineLen)
	}
}

// writeExtensionRecords emits every planned record type 7. The count field is
// derived from the payload length rather than declared, so the two can never
// disagree.
func writeExtensionRecords(e *enc, p plan) {
	for _, x := range p.extensions {
		e.i32(recTypeExtension)
		e.i32(x.subtype)
		e.i32(x.size)
		e.i32(int32(len(x.payload)) / x.size)
		e.raw(x.payload)
	}
}

// writeTerminator emits record type 999, which closes the dictionary.
func writeTerminator(e *enc) {
	e.i32(recTypeTerminator)
	e.i32(0) // filler
}

// writeData emits the data section in whichever encoding the spec asked for.
func writeData(e *enc, p plan) {
	switch p.spec.Compression {
	case CompressionBytecode:
		writeBytecodeData(e, p)
	case CompressionZSAV:
		writeZSAVData(e, p)
	default:
		writeUncompressedData(e, p)
	}
}

// writeUncompressedData emits the uncompressed data section: every case in
// order, every variable in order, 8 bytes per element.
func writeUncompressedData(e *enc, p plan) {
	for _, row := range p.spec.Cases {
		for vi, val := range row {
			v := p.vars[vi]
			switch {
			case val.kind == kindSysMis:
				e.f64(SysMisDouble)
			case v.IsString():
				// Space-pad to the declared width, then on out to the segment
				// boundary — both paddings are spaces, so this is one pad to
				// segments*8.
				e.ascii(val.str, v.segments()*ElementSize)
			default:
				e.f64(val.num)
			}
		}
	}
}

// Bytecode command bytes, per the specification. The emitter spells them out
// here rather than sharing a table with the reader on purpose: this package
// is the reader's ground truth, and a shared table would let one misreading
// of the spec satisfy both halves and pass every test.
const (
	cmdPad    byte = 0
	cmdIntMin byte = 1
	cmdIntMax byte = 251
	cmdEOF    byte = 252
	cmdRaw    byte = 253
	cmdSpaces byte = 254
	cmdSysMis byte = 255

	// cmdBlockSize is the number of command bytes that travel together
	// ahead of their payloads.
	cmdBlockSize = 8
)

// writeBytecodeData emits the data section in SPSS's default bytecode
// encoding: blocks of eight command bytes, each block followed immediately by
// the eight-byte payloads the commands in it asked for, in command order.
//
// The output is a pure function of the spec, exactly as the uncompressed path
// is: which command each datum gets is decided by the datum and the bias
// alone, so the same spec always yields the same bytes.
func writeBytecodeData(e *enc, p plan) {
	writeBytecodeStream(e, p)
}

// writeBytecodeStream emits the command stream itself into any sink.
//
// It is split out from [writeBytecodeData] because ZSAV needs the same
// stream, byte for byte, in a buffer instead of in the file: the zlib blocks
// hold a bytecode stream, they do not replace it. Sharing the emitter is what
// makes a bytecode fixture and a ZSAV fixture built from one spec carry
// literally the same commands.
func writeBytecodeStream(e *enc, p plan) {
	w := &bytecodeWriter{e: e, bias: p.bias()}
	for _, row := range p.spec.Cases {
		for vi, val := range row {
			v := p.vars[vi]
			switch {
			case val.kind == kindSysMis:
				w.command(cmdSysMis, nil)
			case v.IsString():
				writeStringSegments(w, val.str, v.segments())
			default:
				writeNumber(w, val.num)
			}
		}
	}
	// The end-of-file command, then whatever padding rounds the final
	// block out to eight commands. Both are part of the encoding a real
	// writer produces, so both are exercised here.
	w.command(cmdEOF, nil)
	w.flush()
}

// writeNumber emits one numeric element: a single command byte when the value
// is a whole number the bias can carry, and the escape otherwise.
func writeNumber(w *bytecodeWriter, v float64) {
	if cmd, ok := numberCommand(v, w.bias); ok {
		w.command(cmd, nil)
		return
	}
	var b [ElementSize]byte
	// The escape payload is a raw IEEE 754 double, so it is byte-ordered
	// exactly as an uncompressed element would be. Compression changes how
	// a datum is FRAMED, never how its bytes are laid out.
	w.e.bo.PutUint64(b[:], math.Float64bits(v))
	w.command(cmdRaw, b[:])
}

// numberCommand reports the command byte encoding v under bias, and whether v
// is encodable that way at all.
//
// The value must be a whole number and the SUM must land in [1, 251]. The
// sum's own integrality is checked too, so a fractional bias makes every
// value take the escape rather than rounding into a command byte that would
// decode back to a different number.
func numberCommand(v, bias float64) (byte, bool) {
	if v != math.Trunc(v) {
		// NaN fails here: Trunc(NaN) is NaN and NaN != NaN.
		return 0, false
	}
	sum := v + bias
	if sum != math.Trunc(sum) || math.IsInf(sum, 0) {
		return 0, false
	}
	if sum < float64(cmdIntMin) || sum > float64(cmdIntMax) {
		return 0, false
	}
	return byte(sum), true
}

// writeStringSegments emits a string value as its 8-byte segments: the
// all-spaces command for a segment the padding filled entirely, the escape
// for one carrying text.
func writeStringSegments(w *bytecodeWriter, s string, segments int) {
	room := segments * ElementSize
	if len(s) > room {
		// validate already rejected an over-wide datum, so this is
		// defence in depth: it must report rather than panic on a
		// negative pad, matching enc.ascii on the uncompressed path.
		w.e.fail(fmt.Errorf("spsstest: %q is %d bytes but the field is %d", s, len(s), room))
		return
	}
	padded := s + strings.Repeat(" ", room-len(s))
	for i := 0; i < segments; i++ {
		seg := padded[i*ElementSize : (i+1)*ElementSize]
		if seg == "        " {
			w.command(cmdSpaces, nil)
			continue
		}
		w.command(cmdRaw, []byte(seg))
	}
}

// bytecodeWriter buffers commands into blocks of eight and emits each block
// followed by its payloads. That interleaving is the encoding: a payload
// belongs to the block its command sat in, not to the position it would have
// had in a flat stream.
type bytecodeWriter struct {
	e    *enc
	bias float64
	cmds []byte
	data []byte
}

func (w *bytecodeWriter) command(cmd byte, payload []byte) {
	w.cmds = append(w.cmds, cmd)
	w.data = append(w.data, payload...)
	if len(w.cmds) == cmdBlockSize {
		w.emit()
	}
}

// flush writes any partial block, padding it out with the ignore command.
func (w *bytecodeWriter) flush() {
	if len(w.cmds) == 0 {
		return
	}
	w.emit()
}

func (w *bytecodeWriter) emit() {
	for len(w.cmds) < cmdBlockSize {
		w.cmds = append(w.cmds, cmdPad)
	}
	w.e.raw(w.cmds)
	w.e.raw(w.data)
	w.cmds = w.cmds[:0]
	w.data = w.data[:0]
}

func roundUp(n, mult int) int { return (n + mult - 1) / mult * mult }

// enc is the little byte sink everything is emitted through. The byte order is
// held as a field rather than hardcoded so big-endian output is a constructor
// change rather than a rewrite. The first error is sticky: emission never
// panics and never writes a short field silently.
type enc struct {
	buf bytes.Buffer
	bo  binary.ByteOrder
	err error
}

func (e *enc) i32(v int32) {
	var b [4]byte
	e.bo.PutUint32(b[:], uint32(v))
	e.raw(b[:])
}

func (e *enc) i64(v int64) {
	var b [8]byte
	e.bo.PutUint64(b[:], uint64(v))
	e.raw(b[:])
}

func (e *enc) f64(v float64) {
	var b [8]byte
	e.bo.PutUint64(b[:], math.Float64bits(v))
	e.raw(b[:])
}

func (e *enc) raw(b []byte) {
	if e.err != nil {
		return
	}
	e.buf.Write(b)
}

// ascii writes s into exactly n bytes, right-padded with spaces. Overflow is
// an error rather than a truncation: silently shortening a name or a label is
// how a fixture stops describing what its author wrote.
func (e *enc) ascii(s string, n int) {
	if e.err != nil {
		return
	}
	if len(s) > n {
		e.err = fmt.Errorf("spsstest: %q is %d bytes but the field is %d", s, len(s), n)
		return
	}
	e.buf.WriteString(s)
	e.buf.WriteString(strings.Repeat(" ", n-len(s)))
}

// fail records the first error. Emission is sticky: once something has gone
// wrong nothing further is written, and Build reports rather than returning
// bytes that do not describe the spec.
func (e *enc) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

func (e *enc) zeros(n int) {
	if e.err != nil || n <= 0 {
		return
	}
	e.buf.Write(make([]byte, n))
}
