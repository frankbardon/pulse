package template

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/errors"
)

// isoDateLayout is the single date literal shape a `date` variable — and a
// `period` range boundary — accepts.
//
// It is deliberately the strict subset of the layouts the execution layer
// tolerates. Everything accepted here is accepted downstream, so a
// template that resolves can always be executed; the reverse is not
// promised, and does not need to be. Narrowing to one unambiguous spelling
// keeps `03/04/2024` from meaning two different days depending on who
// wrote the template.
const isoDateLayout = "2006-01-02"

// unresolved is the type of the Unresolved sentinel. It is unexported and
// carries no fields, so the only value of this type a caller can obtain is
// Unresolved itself — the sentinel cannot be forged or confused with a
// resolved value of any JSON kind.
type unresolved struct{}

// String renders the sentinel for %v / %s formatting, so an unresolved
// variable in a diagnostic reads as such rather than as an empty struct.
func (unresolved) String() string { return "<unresolved>" }

// Unresolved is the value a declared variable takes when neither the
// caller nor the declaration supplied one.
//
// It is a distinct sentinel rather than a nil any on purpose: a JSON null
// also decodes to nil, and the render walk's `$when` guards turn entirely
// on the resolved/unresolved distinction being exact. Test it with
// IsUnresolved rather than comparing against nil.
var Unresolved any = unresolved{}

// IsUnresolved reports whether v is the Unresolved sentinel. It is the
// only supported way to ask the question — a resolved value is never equal
// to Unresolved, including a resolved empty string, empty list, zero, or
// false.
func IsUnresolved(v any) bool {
	_, ok := v.(unresolved)
	return ok
}

// Resolution is a template's declared variables resolved against a
// caller-supplied map: every declared name mapped either to its concrete
// JSON value or to Unresolved. It is the sole input to the render walk's
// substitution and `$when` guard evaluation.
//
// Resolved values are decoded with json.Number in place of float64, so an
// integer supplied by the caller reaches the rendered request as the exact
// literal it was written as — a u64 field value never round-trips through
// float64 and never loses its low bits.
//
// A Resolution is read-only once returned; every accessor is nil-safe.
type Resolution struct {
	// names holds the declared variable names in author order, so
	// iteration over a Resolution is deterministic where a bare map
	// would not be.
	names []string

	// values maps every declared name to its resolved value or to the
	// Unresolved sentinel. Every declared name is present — absence from
	// this map means the template does not declare the name at all.
	values map[string]any
}

// Names returns the declared variable names in author order. The returned
// slice is a copy.
func (r *Resolution) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.names))
	copy(out, r.names)
	return out
}

// Len reports how many variables the resolution covers, resolved and
// unresolved alike.
func (r *Resolution) Len() int {
	if r == nil {
		return 0
	}
	return len(r.names)
}

// Declared reports whether name is one of the template's declared
// variables, regardless of whether it resolved.
func (r *Resolution) Declared(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.values[name]
	return ok
}

// Get returns the resolved value for name and whether it resolved. An
// undeclared name and an unresolved variable both report false — the
// render walk treats them identically, since neither can be substituted.
// Use Declared to tell them apart.
func (r *Resolution) Get(name string) (any, bool) {
	if r == nil {
		return nil, false
	}
	v, ok := r.values[name]
	if !ok || IsUnresolved(v) {
		return nil, false
	}
	return v, true
}

// IsResolved reports whether name resolved to a concrete value. A value of
// "", [], 0, or false is resolved — resolution is presence semantics, not
// truthiness.
func (r *Resolution) IsResolved(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// All returns the full resolved set as a map, with Unresolved standing in
// for every variable that did not resolve. The returned map is a copy;
// mutating it does not affect the Resolution.
func (r *Resolution) All() map[string]any {
	if r == nil {
		return nil
	}
	out := make(map[string]any, len(r.values))
	maps.Copy(out, r.values)
	return out
}

// Resolve checks a caller-supplied variable map against a template's
// declarations and returns the resolved set. It performs no rendering and
// touches no filesystem.
//
// Resolution order, per declared variable: caller-supplied value, then the
// declaration's `default`, then Unresolved. A supplied nil (or a supplied
// JSON null) means "supplied nothing" and falls through to the default,
// mirroring the declaration side where an explicit null `default` means
// "no default". A supplied "", [], 0, or false is a real value and
// resolves — the render walk's `$when` guards are presence semantics, not
// truthiness, so a legitimate zero stays templatable.
//
// Faults, in the order they are detected:
//
//   - a declaration fault of any kind        → whatever Validate reports
//   - a supplied name the template does not
//     declare                                → PULSE_TEMPLATE_VAR_UNKNOWN
//   - a supplied value whose JSON kind or
//     value contradicts its declared type    → PULSE_TEMPLATE_VAR_TYPE
//   - a supplied string outside an enum's
//     declared `values`                      → PULSE_TEMPLATE_VAR_ENUM
//   - a `required` variable resolving to
//     nothing                                → PULSE_TEMPLATE_VAR_MISSING
//
// The error code is chosen by the value's PROVENANCE, not by when the
// fault is found. A bad caller-supplied value is a caller error and gets
// the PULSE_TEMPLATE_VAR_* code. A bad declared `default` is a template
// AUTHOR error and gets PULSE_TEMPLATE_INVALID wherever it surfaces —
// which is why Validate runs the identical value check over defaults at
// declaration time, so a template carrying an unparseable date default
// fails when it is registered rather than lying in wait until someone
// renders it.
//
// Every returned error carries the template under errors.DetailTemplate
// and, when the fault is variable-scoped, the variable under
// errors.DetailVariable.
func Resolve(t *Template, supplied map[string]any) (*Resolution, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	if err := rejectUnknownSupplied(t, supplied); err != nil {
		return nil, err
	}

	res := &Resolution{
		names:  make([]string, 0, len(t.Variables)),
		values: make(map[string]any, len(t.Variables)),
	}
	for _, v := range t.Variables {
		value, ok, err := resolveOne(t, v, supplied)
		if err != nil {
			return nil, err
		}
		res.names = append(res.names, v.Name)
		if !ok {
			if v.Required {
				return nil, missingVar(t, v.Name)
			}
			res.values[v.Name] = Unresolved
			continue
		}
		res.values[v.Name] = value
	}
	return res, nil
}

// rejectUnknownSupplied fails on any supplied name the template does not
// declare. Unknown names are rejected rather than ignored: silently
// dropping one yields a request that looks parameterised but is not, and a
// typo against a declared name would otherwise surface much later as a
// mystifying "required variable missing". Offenders are reported in sorted
// order so the fault is deterministic across runs.
func rejectUnknownSupplied(t *Template, supplied map[string]any) error {
	if len(supplied) == 0 {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(supplied)) {
		if _, ok := t.Variable(name); ok {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_VAR_UNKNOWN,
			"template does not declare a variable named "+strconv.Quote(name)+
				"; declared variables are "+declaredList(t),
			map[string]any{
				errors.DetailTemplate: t.Name,
				errors.DetailVariable: name,
				"declared":            t.VariableNames(),
			})
	}
	return nil
}

// resolveOne applies the resolution order to a single declaration and
// returns the resolved value, whether it resolved, and any fault. The
// supplied value and the declared default go through the identical value
// check — only the provenance, and therefore the reported code, differs.
func resolveOne(t *Template, v *Variable, supplied map[string]any) (any, bool, error) {
	if raw, present := supplied[v.Name]; present && raw != nil {
		value, err := normalizeSupplied(t, v, raw)
		if err != nil {
			return nil, false, err
		}
		// A supplied JSON null decodes to nil and means "supplied
		// nothing", falling through to the default below.
		if value != nil {
			if err := checkValue(t, v, value, fromCaller); err != nil {
				return nil, false, err
			}
			return value, true, nil
		}
	}

	raw := bytes.TrimSpace(v.Default)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, false, invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
			" has an undecodable `default`: "+err.Error())
	}
	if err := checkValue(t, v, value, fromDefault); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// normalizeSupplied puts a caller-supplied Go value into the same decoded
// JSON shape a declared default arrives in: strings, bools, json.Number,
// []any, and map[string]any. Round-tripping through JSON is what makes the
// check uniform — an int, an int64, a uint64, a float32, a named string
// type, and a json.RawMessage all normalize to the same shapes, so the
// acceptance rules never have to enumerate Go types.
//
// UseNumber is load-bearing: without it a caller-supplied uint64 beyond
// float64's exact-integer range would silently lose its low bits on the
// way into the rendered request.
func normalizeSupplied(t *Template, v *Variable, raw any) (any, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, valueFault(t, v.Name, fromCaller, errors.PULSE_TEMPLATE_VAR_TYPE,
			"template variable "+strconv.Quote(v.Name)+
				": the supplied value is not representable as JSON: "+err.Error(), nil)
	}
	value, err := decodeJSON(encoded)
	if err != nil {
		return nil, valueFault(t, v.Name, fromCaller, errors.PULSE_TEMPLATE_VAR_TYPE,
			"template variable "+strconv.Quote(v.Name)+
				": the supplied value is not decodable JSON: "+err.Error(), nil)
	}
	return value, nil
}

// provenance records where a value being checked came from. It selects the
// error code — a caller's mistake and a template author's mistake are
// different failures with different remedies, and the checking logic is
// identical for both.
type provenance int

const (
	// fromCaller marks a value supplied in the render call's variables
	// map. Faults are the caller's: PULSE_TEMPLATE_VAR_TYPE / _ENUM.
	fromCaller provenance = iota

	// fromDefault marks a value written as a declaration's `default`.
	// Faults are the template author's: PULSE_TEMPLATE_INVALID, wherever
	// they are detected.
	fromDefault
)

// code maps a caller-facing code onto the provenance-correct one.
func (p provenance) code(callerCode errors.Code) errors.Code {
	if p == fromDefault {
		return errors.PULSE_TEMPLATE_INVALID
	}
	return callerCode
}

// origin names, for error prose, where the offending value came from.
func (p provenance) origin() string {
	if p == fromDefault {
		return "the declared `default`"
	}
	return "the supplied value"
}

// checkValue validates one decoded JSON value against a variable's
// declaration. It is the single acceptance rule for both caller-supplied
// values and declared defaults; src decides only which code a fault
// reports.
//
// Acceptance, by declared type:
//
//	string, field  a JSON string
//	number         any JSON number
//	integer        a JSON number with no fractional part (1 and 1.0, not 1.5)
//	boolean        a JSON bool
//	enum           a JSON string that is a member of the declared `values`
//	list           a JSON array whose every element satisfies `items`
//	date           a JSON string parsing as YYYY-MM-DD
//	period         a JSON object carrying exactly one of `ranges` / `table`
//
// `field` is shape-checked only (it is a string). It exists as a distinct
// type so cohort-bound field-name constraining can be layered on later
// without a wire change.
func checkValue(t *Template, v *Variable, value any, src provenance) error {
	switch v.Type {
	case VarEnum:
		s, ok := value.(string)
		if !ok {
			return kindFault(t, v, v.Type, value, src, -1)
		}
		if !slices.Contains(v.Values, s) {
			return valueFault(t, v.Name, src, errors.PULSE_TEMPLATE_VAR_ENUM,
				"template variable "+strconv.Quote(v.Name)+": "+src.origin()+" "+
					strconv.Quote(s)+" is not a member of the declared `values` set "+
					quotedList(v.Values)+"; membership is exact and case-sensitive",
				map[string]any{"value": s, "values": slices.Clone(v.Values)})
		}
		return nil

	case VarList:
		elems, ok := value.([]any)
		if !ok {
			return kindFault(t, v, v.Type, value, src, -1)
		}
		for i, e := range elems {
			if err := checkScalar(t, v, v.Items, e, src, i); err != nil {
				return err
			}
		}
		return nil

	case VarPeriod:
		obj, ok := value.(map[string]any)
		if !ok {
			return kindFault(t, v, v.Type, value, src, -1)
		}
		return checkPeriod(t, v, obj, src)

	default:
		return checkScalar(t, v, v.Type, value, src, -1)
	}
}

// checkScalar validates one scalar value against a scalar type. index is
// the element position when the value is a list element, or -1 when it is
// the variable's own value.
func checkScalar(t *Template, v *Variable, vt VarType, value any, src provenance, index int) error {
	if !kindMatches(vt, value) {
		return kindFault(t, v, vt, value, src, index)
	}
	if vt != VarDate {
		return nil
	}
	s, _ := value.(string)
	if !isISODate(s) {
		return valueFault(t, v.Name, src, errors.PULSE_TEMPLATE_VAR_TYPE,
			position(v, index)+": "+src.origin()+" "+strconv.Quote(s)+
				" is not a date; write it as YYYY-MM-DD",
			map[string]any{"declared": vt.String(), "value": s})
	}
	return nil
}

// checkPeriod validates the labeled-date-range shape a `period` variable
// carries: exactly one of an inline `ranges` array or a named `table`
// reference, mirroring the GROUP_DATE_RANGES / FILTER_DATE_RANGES params.
//
// Validation is shape plus per-boundary date parse ONLY. Overlap and
// duplicate-label detection stay in the execution layer's range
// compilation, so this package never has to reach for processing/. A
// period that passes here can still be rejected at execution — that is the
// intended division, not a gap.
func checkPeriod(t *Template, v *Variable, obj map[string]any, src provenance) error {
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if key != "ranges" && key != "table" {
			return periodFault(t, v, src, "carries unknown key "+strconv.Quote(key)+
				"; a period object holds exactly one of `ranges` or `table`")
		}
	}

	rangesRaw, hasRanges := obj["ranges"]
	tableRaw, hasTable := obj["table"]

	switch {
	case hasRanges && hasTable:
		return periodFault(t, v, src,
			"carries both `ranges` and `table`; a period object holds exactly one of them")
	case !hasRanges && !hasTable:
		return periodFault(t, v, src,
			"carries neither `ranges` nor `table`; a period object holds exactly one of them")
	case hasTable:
		name, ok := tableRaw.(string)
		if !ok {
			return periodFault(t, v, src, "declares a `table` that is "+observedKind(tableRaw)+
				"; it must be a JSON string naming a registered range table")
		}
		if name == "" {
			return periodFault(t, v, src, "declares an empty `table` name")
		}
		return nil
	}

	elems, ok := rangesRaw.([]any)
	if !ok {
		return periodFault(t, v, src, "declares `ranges` as "+observedKind(rangesRaw)+
			"; it must be a JSON array of {label, start, end} objects")
	}
	if len(elems) == 0 {
		return periodFault(t, v, src, "declares an empty `ranges` array; supply at least one range")
	}
	for i, e := range elems {
		if err := checkRange(t, v, src, i, e); err != nil {
			return err
		}
	}
	return nil
}

// checkRange validates one {label, start, end} entry. A nil, null, or
// empty boundary is an open bound and is left to the execution layer; a
// non-empty boundary must parse.
func checkRange(t *Template, v *Variable, src provenance, index int, elem any) error {
	obj, ok := elem.(map[string]any)
	if !ok {
		return periodFault(t, v, src, "declares `ranges` element "+strconv.Itoa(index)+
			" as "+observedKind(elem)+"; every range must be a {label, start, end} object")
	}
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if key != "label" && key != "start" && key != "end" {
			return periodFault(t, v, src, "declares `ranges` element "+strconv.Itoa(index)+
				" with unknown key "+strconv.Quote(key)+"; a range carries only `label`, `start`, and `end`")
		}
	}

	label, ok := obj["label"].(string)
	if !ok || label == "" {
		return periodFault(t, v, src, "declares `ranges` element "+strconv.Itoa(index)+
			" with no `label`; every range needs a non-empty label, which is the bucket key it emits")
	}
	for _, bound := range []string{"start", "end"} {
		raw, present := obj[bound]
		if !present || raw == nil {
			continue // an absent or null boundary is an open bound
		}
		s, ok := raw.(string)
		if !ok {
			return periodFault(t, v, src, "declares `ranges` element "+strconv.Itoa(index)+
				" with a `"+bound+"` that is "+observedKind(raw)+"; a boundary must be a YYYY-MM-DD string")
		}
		if s == "" {
			continue // an empty boundary is an open bound
		}
		if !isISODate(s) {
			return periodFault(t, v, src, "declares `ranges` element "+strconv.Itoa(index)+
				" with a `"+bound+"` of "+strconv.Quote(s)+" that is not a date; write it as YYYY-MM-DD")
		}
	}
	return nil
}

// isISODate reports whether s parses as an unambiguous YYYY-MM-DD date.
// The layout requires zero-padded fields, so "2024-1-1" is rejected, as is
// a real-looking but nonexistent day such as "2024-02-30".
func isISODate(s string) bool {
	_, err := time.Parse(isoDateLayout, s)
	return err == nil
}

// position names the value under check in error prose — the variable
// itself, or a specific element of its list.
func position(v *Variable, index int) string {
	subject := "template variable " + strconv.Quote(v.Name)
	if index >= 0 {
		subject += " element " + strconv.Itoa(index)
	}
	return subject
}

// kindFault builds the canonical declared-vs-observed JSON kind mismatch.
func kindFault(t *Template, v *Variable, want VarType, got any, src provenance, index int) error {
	msg := position(v, index) + ": " + src.origin() + " is " + observedKind(got) +
		", but the declared type " + strconv.Quote(want.String()) + " requires " + describeKind(want)
	details := map[string]any{
		"declared": want.String(),
		"supplied": observedKind(got),
	}
	if index >= 0 {
		details["index"] = index
	}
	return valueFault(t, v.Name, src, errors.PULSE_TEMPLATE_VAR_TYPE, msg, details)
}

// periodFault builds a period-shape fault with the shared prefix.
func periodFault(t *Template, v *Variable, src provenance, detail string) error {
	return valueFault(t, v.Name, src, errors.PULSE_TEMPLATE_VAR_TYPE,
		"template variable "+strconv.Quote(v.Name)+": "+src.origin()+" "+detail,
		map[string]any{"declared": VarPeriod.String()})
}

// valueFault builds a value-level fault, selecting the code from the
// value's provenance: a caller's value reports callerCode, a template
// author's `default` reports PULSE_TEMPLATE_INVALID.
func valueFault(t *Template, variable string, src provenance, callerCode errors.Code, msg string, extra map[string]any) error {
	details := map[string]any{
		errors.DetailTemplate: t.Name,
		errors.DetailVariable: variable,
	}
	maps.Copy(details, extra)
	return errors.NewCodedErrorWithDetails(src.code(callerCode), msg, details)
}

// missingVar builds the required-but-unresolved fault.
func missingVar(t *Template, variable string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_VAR_MISSING,
		"template variable "+strconv.Quote(variable)+" is declared required but resolved to nothing; "+
			"supply a value for it or give its declaration a `default`",
		map[string]any{
			errors.DetailTemplate: t.Name,
			errors.DetailVariable: variable,
		})
}

// declaredList renders a template's declared variable names for error
// prose.
func declaredList(t *Template) string {
	names := t.VariableNames()
	if len(names) == 0 {
		return "(none)"
	}
	return quotedList(names)
}

// quotedList renders a string slice as a quoted, comma-separated list.
func quotedList(values []string) string {
	out := make([]string, 0, len(values))
	for _, s := range values {
		out = append(out, strconv.Quote(s))
	}
	return strings.Join(out, ", ")
}
