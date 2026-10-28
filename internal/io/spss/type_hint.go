package spss

// Pulse type hints: the record 7/18 `@PulseType` variable attribute.
//
// SPSS has two storage types, so a `.pulse` column SPSS cannot type
// natively — every unsigned integer width, f32, packed_bool, decimal128 and
// a categorical wider than the narrowest rung its entries need — goes out as
// a plain double (or a string) and, before this file, came back as f64 (or
// the narrowest categorical rung). The export now records the Pulse type as a
// custom variable attribute and the import restores it when, and only when,
// every value fits it exactly.
//
// # Why a variable attribute
//
// Record 7/18 is SPSS's own extension point for per-variable metadata, it is
// already captured verbatim by this reader and carried by the sidecar, and
// every SPSS-reading tool either shows custom attributes or ignores them. The
// `@` prefix is SPSS's convention for an attribute hidden from Variable View,
// so the hint does not clutter an analyst's data editor. Nothing about the
// `.pulse` format or the metadata sidecar's schema moves: the hint lives in
// the `.sav` alone.
//
// # When a hint is written
//
// Only where the plain re-import would choose a DIFFERENT type, so an export
// whose every column already round-trips (an f64 / date / datetime /
// categorical_u8 cohort, and every SPSS-origin cohort nothing retyped) is
// byte-identical to what it was before hints existed. A value-labelled
// integer binding writes none: its labels make the re-import categorical,
// which is the documented shape of that path.
//
// # When a hint is honoured
//
// Never on trust. A hinted numeric is restored only when EVERY non-missing
// value fits the type exactly; one that does not — a hand-edited `.sav`, or a
// u64 at or past 2^53 that the double already rounded — falls back to the
// plain mapping with PULSE_SPSS_TYPE_NOT_RESTORED naming the variable, the
// hint and the first offending value. A file with no hint (every third-party
// file) maps exactly as before.

import (
	"bytes"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// pulseTypeAttr is the record 7/18 attribute name carrying a Pulse type hint.
const pulseTypeAttr = "@PulseType"

// maxExactHintInteger is the exclusive bound under which a double names one
// integer unambiguously. 2^53 itself is exact, but 2^53+1 rounds to it, so a
// u64 read back as 2^53 may not be the value that was exported.
const maxExactHintInteger = 1 << 53

// maxExactDecimalUnits bounds |value × 10^scale| for a hinted decimal: below
// it a double's rounding error is under a quarter of one unit in the last
// decimal place, so the shortest rendering of the stored double IS the
// decimal that was exported.
const maxExactDecimalUnits = 1 << 50

// typeHint is one parsed `@PulseType` value.
type typeHint struct {
	ft        encoding.FieldType
	precision uint8
	scale     uint8
}

// String spells the hint as the attribute stores it: the field type's own
// name, with `(precision,scale)` for decimal128.
func (h typeHint) String() string {
	if h.ft == encoding.FieldTypeDecimal128 {
		return "decimal128(" + strconv.Itoa(int(h.precision)) + "," + strconv.Itoa(int(h.scale)) + ")"
	}
	return h.ft.String()
}

// parseTypeHint reads an attribute value back into a hint. Only the types
// an export writes, plus the natural targets a hint may harmlessly restate
// (f64, date, datetime, categorical_u8), parse; anything else is refused.
func parseTypeHint(s string) (typeHint, bool) {
	if rest, ok := strings.CutPrefix(s, "decimal128("); ok {
		body, ok := strings.CutSuffix(rest, ")")
		if !ok {
			return typeHint{}, false
		}
		ps, ss, ok := strings.Cut(body, ",")
		if !ok {
			return typeHint{}, false
		}
		p, err1 := strconv.ParseUint(ps, 10, 8)
		sc, err2 := strconv.ParseUint(ss, 10, 8)
		if err1 != nil || err2 != nil || p < 1 || p > encoding.MaxDecimalPrecision || sc > p {
			return typeHint{}, false
		}
		return typeHint{ft: encoding.FieldTypeDecimal128, precision: uint8(p), scale: uint8(sc)}, true
	}
	ft, ok := encoding.ParseFieldType(s)
	if !ok {
		return typeHint{}, false
	}
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32,
		encoding.FieldTypeU64, encoding.FieldTypeF32, encoding.FieldTypeF64, encoding.FieldTypePackedBool,
		encoding.FieldTypeDate, encoding.FieldTypeDateTime,
		encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		return typeHint{ft: ft}, true
	}
	return typeHint{}, false
}

// numericHint reports whether the hint targets a plain SPSS numeric.
func (h typeHint) numericHint() bool {
	switch h.ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32,
		encoding.FieldTypeU64, encoding.FieldTypeF32, encoding.FieldTypeF64, encoding.FieldTypePackedBool,
		encoding.FieldTypeDecimal128:
		return true
	}
	return false
}

// unsignedMax is the largest value an integer hint holds exactly, false for
// a hint that is not an integer.
func (h typeHint) unsignedMax() (float64, bool) {
	switch h.ft {
	case encoding.FieldTypePackedBool:
		return 1, true
	case encoding.FieldTypeU4:
		return 15, true
	case encoding.FieldTypeU8:
		return math.MaxUint8, true
	case encoding.FieldTypeU16:
		return math.MaxUint16, true
	case encoding.FieldTypeU32:
		return math.MaxUint32, true
	case encoding.FieldTypeU64:
		return maxExactHintInteger - 1, true
	}
	return 0, false
}

// fits reports whether one non-missing double holds a value of the hinted
// type exactly.
func (h typeHint) fits(v float64) bool {
	if max, ok := h.unsignedMax(); ok {
		return v == math.Trunc(v) && v >= 0 && v <= max
	}
	switch h.ft {
	case encoding.FieldTypeF64:
		return true
	case encoding.FieldTypeF32:
		return v == float64(float32(v))
	case encoding.FieldTypeDecimal128:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v)*math.Pow10(int(h.scale)) >= maxExactDecimalUnits {
			return false
		}
		text := strconv.FormatFloat(math.Abs(v), 'f', -1, 64)
		whole, frac, _ := strings.Cut(text, ".")
		whole = strings.TrimLeft(whole, "0")
		return len(frac) <= int(h.scale) && len(whole) <= int(h.precision)-int(h.scale)
	}
	return false
}

// render is the cell text of a value that fits: integers in plain decimal
// (the shortest float rendering would spell 1e+06), a decimal in positional
// notation, f32 as the shortest double rendering.
func (h typeHint) render(v float64) string {
	if _, ok := h.unsignedMax(); ok {
		return strconv.FormatUint(uint64(v), 10)
	}
	if h.ft == encoding.FieldTypeDecimal128 {
		if v == 0 {
			return "0"
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return formatNumericValue(v)
}

// ---------------------------------------------------------------------------
// Export: which variables carry a hint
// ---------------------------------------------------------------------------

// exportTypeHint returns the hint a variable carries, false when the plain
// re-import already yields its cohort type.
func exportTypeHint(v *outVar, fld *encoding.Field) (typeHint, bool) {
	switch v.enc {
	case EncodeNumeric:
		if len(v.labels) > 0 {
			// Value labels make the re-import categorical; a numeric hint
			// would only be refused there.
			return typeHint{}, false
		}
		switch fld.Type {
		case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32,
			encoding.FieldTypeU64, encoding.FieldTypeF32, encoding.FieldTypePackedBool:
			return typeHint{ft: fld.Type}, true
		case encoding.FieldTypeDecimal128:
			return typeHint{ft: fld.Type, precision: fld.Precision, scale: fld.Scale}, true
		}
	case EncodeText, EncodeCategoricalCode:
		if fld.Type == encoding.FieldTypeCategoricalU16 || fld.Type == encoding.FieldTypeCategoricalU32 {
			return typeHint{ft: fld.Type}, true
		}
	}
	return typeHint{}, false
}

// applyTypeHints writes the record 7/18 type hints. It runs after the
// charset pass, so v.name is already the wire bytes the attribute must key
// on, and it OWNS the `@PulseType` attribute: a hint carried verbatim from a
// source file (a re-export of a re-import) is dropped and re-derived from the
// cohort, so a column retyped since cannot keep a stale hint.
//
// A source 7/18 this package cannot parse is left verbatim and no hint is
// added to it — rewriting bytes it cannot read would corrupt them — so such a
// cohort re-imports with the plain mapping, as it always did.
func applyTypeHints(f *outFile, s *encoding.Schema) {
	type want struct {
		name string
		hint typeHint
	}
	var wants []want
	for _, v := range f.vars {
		if v.field < 0 || v.field >= len(s.Fields) {
			continue
		}
		if h, ok := exportTypeHint(v, &s.Fields[v.field]); ok {
			wants = append(wants, want{name: v.name, hint: h})
		}
	}

	var (
		sets    []attrSet
		trailer []byte
	)
	if f.varAttrs != nil {
		parsed, tail, ok := parseVarAttributes(f.varAttrs.Raw)
		if !ok {
			return
		}
		sets, trailer = parsed, tail
	}
	stripped := false
	for i := range sets {
		kept := sets[i].attrs[:0:0]
		for _, a := range sets[i].attrs {
			if strings.EqualFold(string(a.key), pulseTypeAttr) {
				stripped = true
				continue
			}
			kept = append(kept, a)
		}
		sets[i].attrs = kept
	}
	if len(wants) == 0 && !stripped {
		return
	}
	for _, w := range wants {
		attr := attrEntry{key: []byte(pulseTypeAttr),
			raw: []byte(pulseTypeAttr + "('" + w.hint.String() + "'\n)")}
		placed := false
		for i := range sets {
			if strings.EqualFold(string(sets[i].name), w.name) {
				sets[i].attrs = append(sets[i].attrs, attr)
				placed = true
				break
			}
		}
		if !placed {
			sets = append(sets, attrSet{name: []byte(w.name), attrs: []attrEntry{attr}})
		}
	}

	raw := renderVarAttributes(sets, trailer)
	if len(raw) == 0 {
		f.varAttrs = nil
		return
	}
	f.varAttrs = &RawText{Subtype: extVarAttributes, Raw: raw}
}

// ---------------------------------------------------------------------------
// Record 7/18 text
// ---------------------------------------------------------------------------

// attrSet is one `name:attr(...)attr(...)` run of a record 7/18 payload.
type attrSet struct {
	name  []byte
	attrs []attrEntry
}

// attrEntry is one attribute: its key and its whole `key('v'\n...)` bytes,
// kept verbatim so a rewrite that drops a sibling changes nothing else.
type attrEntry struct {
	key    []byte
	raw    []byte
	values [][]byte
}

// parseVarAttributes splits a record 7/18 payload the way PSPP reads one:
// `name:` then attributes `key(` each value up to a newline, the last
// followed by `)`; sets separated by `/`. A value is quoted with `'`. Blank
// trailing bytes (some writers end the text with a newline) are returned as
// the trailer. ok is false for anything that does not follow the grammar.
func parseVarAttributes(raw []byte) (sets []attrSet, trailer []byte, ok bool) {
	pos := 0
	for pos < len(raw) {
		colon := bytes.IndexByte(raw[pos:], ':')
		if colon <= 0 {
			return nil, nil, false
		}
		set := attrSet{name: raw[pos : pos+colon]}
		pos += colon + 1
		for {
			paren := bytes.IndexByte(raw[pos:], '(')
			if paren <= 0 {
				return nil, nil, false
			}
			start := pos
			entry := attrEntry{key: raw[pos : pos+paren]}
			pos += paren + 1
			for {
				nl := bytes.IndexByte(raw[pos:], '\n')
				if nl < 0 {
					return nil, nil, false
				}
				entry.values = append(entry.values, raw[pos:pos+nl])
				pos += nl + 1
				if pos < len(raw) && raw[pos] == ')' {
					pos++
					break
				}
			}
			entry.raw = raw[start:pos]
			set.attrs = append(set.attrs, entry)
			if len(bytes.TrimSpace(raw[pos:])) == 0 {
				trailer = raw[pos:]
				pos = len(raw)
				break
			}
			if raw[pos] == '/' {
				pos++
				break
			}
		}
		sets = append(sets, set)
	}
	return sets, trailer, true
}

// renderVarAttributes is parseVarAttributes' inverse; a set left with no
// attribute is dropped, and the trailer follows a non-empty text.
func renderVarAttributes(sets []attrSet, trailer []byte) []byte {
	var out []byte
	for _, s := range sets {
		if len(s.attrs) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, '/')
		}
		out = append(out, s.name...)
		out = append(out, ':')
		for _, a := range s.attrs {
			out = append(out, a.raw...)
		}
	}
	if len(out) > 0 {
		out = append(out, trailer...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Import: reading the hints back
// ---------------------------------------------------------------------------

// importedHint is one variable's raw `@PulseType` text.
type importedHint struct {
	text string
	hint typeHint
	ok   bool
}

// typeHintsFrom reads every `@PulseType` attribute in a file's record 7/18,
// keyed by lower-cased variable name. A file with no 7/18, or one this
// grammar cannot read, yields none — the plain mapping, unchanged.
func typeHintsFrom(d *dictionary) map[string]importedHint {
	x, ok := d.rawExtension(extVarAttributes)
	if !ok {
		return nil
	}
	payload := x.payload
	if dec := d.charset.dec; dec != nil {
		if text, at := dec.decodeString(string(payload)); at < 0 {
			payload = []byte(text)
		}
	}
	sets, _, ok := parseVarAttributes(payload)
	if !ok {
		return nil
	}
	var out map[string]importedHint
	for _, s := range sets {
		for _, a := range s.attrs {
			if !strings.EqualFold(string(a.key), pulseTypeAttr) {
				continue
			}
			if out == nil {
				out = make(map[string]importedHint)
			}
			ih := importedHint{}
			if len(a.values) == 1 {
				v := a.values[0]
				if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
					ih.text = string(v[1 : len(v)-1])
					ih.hint, ih.ok = parseTypeHint(ih.text)
				} else {
					ih.text = string(v)
				}
			}
			out[strings.ToLower(string(s.name))] = ih
		}
	}
	return out
}

// warnTypeNotRestored records one hint the mapping did not honour.
func (m *mapping) warnTypeNotRestored(v variable, hint, why string, value *float64) {
	ce := mapError(errors.PULSE_SPSS_TYPE_NOT_RESTORED, v,
		"the variable carries the Pulse type hint %q, which was not restored: %s; it maps as if it carried no hint",
		hint, why)
	ce.Details[errors.DetailSPSSPulseType] = hint
	if value != nil {
		ce.Details[errors.DetailSPSSValue] = formatNumericValue(*value)
	}
	m.warnings = append(m.warnings, ce)
}

// applyTypeHint restores a resolved column to its hinted type, or warns and
// leaves the plain mapping in place. kind is the dictionary classification;
// col.kind the resolved one (a temporal column may have dropped to f64).
func (m *mapping) applyTypeHint(col *columnMapping, at int, v variable, kind columnKind, ih *importedHint, st *columnStats) {
	if !ih.ok {
		m.warnTypeNotRestored(v, ih.text, "it is not a type this reader restores", nil)
		return
	}
	h := ih.hint
	switch {
	case kind == kindNumeric && col.kind == kindNumeric:
		if !h.numericHint() {
			m.warnTypeNotRestored(v, ih.text, "a plain SPSS numeric cannot carry "+h.ft.String(), nil)
			return
		}
		if h.ft == encoding.FieldTypeF64 {
			return
		}
		if st.hintMisfit {
			m.plan.cols[at].hint = nil
			value := st.hintMisfitValue
			m.warnTypeNotRestored(v, ih.text, "a value does not fit "+h.String()+" exactly", &value)
			return
		}
		col.fieldType, col.precision, col.scale = h.ft, h.precision, h.scale

	case col.kind == kindCategorical:
		if !h.ft.IsCategorical() {
			m.warnTypeNotRestored(v, ih.text, "the variable maps to a categorical (a string, or value-labelled codes)", nil)
			return
		}
		if n := len(col.values); uint64(n) > uint64(h.ft.MaxCategoricalEntries()) {
			m.warnTypeNotRestored(v, ih.text, "its "+strconv.Itoa(n)+" dictionary entries do not fit "+h.ft.String(), nil)
			return
		}
		col.fieldType = h.ft

	default:
		if h.ft != col.fieldType {
			m.warnTypeNotRestored(v, ih.text, "the variable's print format maps it to "+col.fieldType.String(), nil)
		}
	}
}
