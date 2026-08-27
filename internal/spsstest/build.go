package spsstest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
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

	e := &enc{bo: binary.LittleEndian}
	writeHeader(e, plan)
	for i := range plan.vars {
		writeVariableRecords(e, plan.vars[i])
	}
	for _, set := range plan.valueLabels {
		writeValueLabelRecords(e, set)
	}
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
func validate(spec Spec) (plan, error) {
	var p plan
	p.spec = spec

	if spec.ByteOrder != LittleEndian {
		return p, fmt.Errorf("spsstest: %s output is not implemented; only little-endian is supported today", spec.ByteOrder)
	}
	if spec.Compression != CompressionNone {
		return p, fmt.Errorf("spsstest: %s data sections are not implemented; only uncompressed is supported today", spec.Compression)
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
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) width %d exceeds %d; wider strings need the record 7/14 very-long-string scheme, which is out of scope", i, v.Name, v.Width, MaxStringWidth)
		}
		if len(v.Label) > MaxVarLabelLen {
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) label is %d bytes, over the %d-byte limit", i, v.Name, len(v.Label), MaxVarLabelLen)
		}
		if !isASCIIPrintable(v.Label) {
			return p, fmt.Errorf("spsstest: Vars[%d] (%s) label is not printable 7-bit ASCII; a non-ASCII label needs a declared encoding (record 7/20), which is out of scope", i, v.Name)
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
	return p, nil
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
		if !isASCIIPrintable(l.Label) {
			return rs, fmt.Errorf("spsstest: ValueLabels[%d].Labels[%d] is not printable 7-bit ASCII; a non-ASCII label needs a declared encoding (record 7/20), which is out of scope", si, li)
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
		if !isASCIIPrintable(val.str) {
			return fmt.Errorf("%s is not printable 7-bit ASCII; non-ASCII data needs a declared encoding (record 7/20), which is out of scope", val)
		}
	case kindSysMis:
		if v.IsString() {
			return fmt.Errorf("SysMis() given for string variable; SPSS has no system-missing state for strings, use Text(\"\") for an all-spaces value")
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

func checkFixed(what, s string, n int) error {
	if len(s) > n {
		return fmt.Errorf("spsstest: %s is %d bytes, over the %d-byte header field", what, len(s), n)
	}
	if !isASCIIPrintable(s) {
		return fmt.Errorf("spsstest: %s is not printable 7-bit ASCII", what)
	}
	return nil
}

func checkExact(what, s string, n int) error {
	if len(s) != n {
		return fmt.Errorf("spsstest: %s must be exactly %d bytes, got %d (%q)", what, n, len(s), s)
	}
	if !isASCIIPrintable(s) {
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
	e.ascii("$FL2", headerRecTypeLen) // $FL3 marks ZSAV, which is out of scope
	e.ascii(stringOr(p.spec.ProductName, DefaultProductName), headerProdNameLen)
	e.i32(2) // layout_code: written in file byte order, so it doubles as the endianness probe
	e.i32(p.nominalCaseSize)
	e.i32(int32(p.spec.Compression))
	e.i32(p.weightIndex)
	e.i32(p.ncases)
	e.f64(CompressionBias) // written even when uncompressed, as PSPP does
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
	e.i32(0) // n_missing_values: missing-value specs are out of scope
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

// writeTerminator emits record type 999, which closes the dictionary.
func writeTerminator(e *enc) {
	e.i32(recTypeTerminator)
	e.i32(0) // filler
}

// writeData emits the uncompressed data section: every case in order, every
// variable in order, 8 bytes per element.
func writeData(e *enc, p plan) {
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

func (e *enc) zeros(n int) {
	if e.err != nil || n <= 0 {
		return
	}
	e.buf.Write(make([]byte, n))
}
