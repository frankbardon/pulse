package template

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// Parse decodes a template document from its JSON bytes and runs
// declaration validation on the result. It is the single entry point the
// store uses for every discovered file, so a malformed document is
// rejected at load rather than at first render.
//
// Malformed JSON returns PULSE_TEMPLATE_INVALID wrapping the decoder
// fault. That error carries no template detail — the bytes did not parse,
// so the document's own name is unknowable; the caller (which knows the
// file path) is the one positioned to name it. Every error raised after
// the decode succeeds carries errors.DetailTemplate.
//
// Unknown top-level keys are tolerated on the document wrapper. The strict
// unknown-field decode is applied to the RENDERED body against its target
// request type, not to the wrapper.
func Parse(data []byte) (*Template, error) {
	var t Template
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_TEMPLATE_INVALID,
			"template document is not valid JSON: "+err.Error())
	}
	if err := Validate(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Validate runs declaration validation over a template document. It checks
// the declaration only — it never resolves a variable, renders a body, or
// touches the filesystem — and returns the first fault it finds:
//
//   - absent or unrecognised target      → PULSE_TEMPLATE_TARGET_UNKNOWN
//   - absent, malformed, non-object, or
//     empty body                         → PULSE_TEMPLATE_INVALID
//   - empty or duplicate variable name   → PULSE_TEMPLATE_INVALID
//   - absent or unrecognised var type    → PULSE_TEMPLATE_INVALID
//   - enum without values                → PULSE_TEMPLATE_INVALID
//   - list without items, with nested
//     items, or with non-scalar items    → PULSE_TEMPLATE_INVALID
//   - a default whose JSON type
//     contradicts its declared type      → PULSE_TEMPLATE_INVALID
//
// Two things are deliberately NOT faults here. Required together with a
// default is legal: the default resolves the variable, so the pair can
// never leave it unresolved. And Name is not required — a template
// unmarshaled directly in Go may leave it empty, because naming is the
// store's job (it derives the name from the file's path).
//
// Value-level checks that depend on a resolved value — an enum default's
// membership, a date default's parse, a period default's ranges-XOR-table
// shape — belong to variable resolution, which runs the identical type
// check over defaults and caller-supplied values alike and reports
// PULSE_TEMPLATE_VAR_* codes. Validate stops at JSON kind.
//
// Every returned error carries the template under errors.DetailTemplate
// and, when the fault is variable-scoped, the variable under
// errors.DetailVariable.
func Validate(t *Template) error {
	if t == nil {
		return errors.NewCodedError(errors.PULSE_TEMPLATE_INVALID,
			"template document is nil")
	}
	if err := validateTarget(t); err != nil {
		return err
	}
	if err := validateBody(t); err != nil {
		return err
	}
	return validateVariables(t)
}

// validateTarget enforces the closed target enum. Absent and unrecognised
// share the code because they share the remedy: state one of the five
// lowercase wire spellings.
func validateTarget(t *Template) error {
	if t.Target == "" {
		return invalidTarget(t, "template target is absent; set it to one of "+targetList())
	}
	if !t.Target.Valid() {
		return invalidTarget(t, "template target "+strconv.Quote(t.Target.String())+
			" is not recognised; valid targets are "+targetList())
	}
	return nil
}

// validateBody enforces that the body is present, well-formed, a JSON
// object, and carries at least one key. An empty object renders to an
// empty request, which is never what an author meant.
func validateBody(t *Template) error {
	body := bytes.TrimSpace(t.Body)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return invalid(t, "template body is absent; a template must carry a `body` object holding the parameterised request")
	}
	if !json.Valid(body) {
		return invalid(t, "template body is not valid JSON")
	}
	if body[0] != '{' {
		return invalid(t, "template body must be a JSON object, matching the shape of the target request root")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return invalid(t, "template body is not a decodable JSON object: "+err.Error())
	}
	if len(fields) == 0 {
		return invalid(t, "template body is empty; a template must carry a `body` object holding the parameterised request")
	}
	return nil
}

// validateVariables enforces the per-declaration rules and name
// uniqueness, walking declarations in author order so the first fault
// reported is the first fault written.
func validateVariables(t *Template) error {
	seen := make(map[string]struct{}, len(t.Variables))
	for i, v := range t.Variables {
		if v == nil {
			return invalid(t, "template variable declaration at position "+strconv.Itoa(i)+" is null")
		}
		if v.Name == "" {
			return invalidVar(t, "", "template variable declaration at position "+strconv.Itoa(i)+" has an empty name")
		}
		if _, dup := seen[v.Name]; dup {
			return invalidVar(t, v.Name, "template declares variable "+strconv.Quote(v.Name)+" more than once")
		}
		seen[v.Name] = struct{}{}

		if err := validateVarType(t, v); err != nil {
			return err
		}
		if err := validateDefault(t, v); err != nil {
			return err
		}
	}
	return nil
}

// validateVarType enforces the closed VarType enum plus the two
// type-specific declaration slots: enum's Values and list's Items.
func validateVarType(t *Template, v *Variable) error {
	if v.Type == "" {
		return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
			" has no type; declare one of "+varTypeList())
	}
	if !v.Type.Valid() {
		return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
			" has unrecognised type "+strconv.Quote(v.Type.String())+"; valid types are "+varTypeList())
	}

	switch v.Type {
	case VarEnum:
		if len(v.Values) == 0 {
			return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
				" is declared enum but lists no `values`; an enum with no members can never resolve")
		}
	case VarList:
		if v.Items == "" {
			return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
				" is declared list but names no `items` element type")
		}
		if !v.Items.Valid() {
			return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
				" declares unrecognised `items` type "+strconv.Quote(v.Items.String())+
				"; valid types are "+varTypeList())
		}
		if v.Items == VarList {
			return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
				" declares `items` of type list; lists do not nest")
		}
		if !v.Items.IsScalar() {
			return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
				" declares non-scalar `items` type "+strconv.Quote(v.Items.String())+
				"; a list element type must be one of "+scalarTypeList())
		}
	}
	return nil
}

// validateDefault checks a declared default against its own variable's
// declared type at JSON-kind granularity. An absent default, and an
// explicit JSON null, are both "no default" and check nothing.
func validateDefault(t *Template, v *Variable) error {
	raw := bytes.TrimSpace(v.Default)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if !json.Valid(raw) {
		return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
			" has a `default` that is not valid JSON")
	}

	value, err := decodeJSON(raw)
	if err != nil {
		return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
			" has an undecodable `default`: "+err.Error())
	}

	if v.Type == VarList {
		elems, ok := value.([]any)
		if !ok {
			return defaultKindMismatch(t, v, v.Type, value)
		}
		for _, e := range elems {
			if !kindMatches(v.Items, e) {
				return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
					" has a `default` list element that is not "+describeKind(v.Items)+
					", contradicting its declared `items` type "+strconv.Quote(v.Items.String()))
			}
		}
		return nil
	}

	if !kindMatches(v.Type, value) {
		return defaultKindMismatch(t, v, v.Type, value)
	}
	return nil
}

// decodeJSON decodes raw JSON into an any tree with UseNumber, so an
// integral default survives as its exact literal rather than round-tripping
// through float64 and losing u64 fidelity.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// kindMatches reports whether a decoded JSON value satisfies the JSON kind
// a declared type accepts. It is kind-level only: a VarDate string is not
// parsed, a VarEnum string is not membership-checked, and a VarPeriod
// object's ranges-XOR-table shape is not inspected. Those are value-level
// checks owned by variable resolution.
func kindMatches(vt VarType, value any) bool {
	switch vt {
	case VarString, VarField, VarDate, VarEnum:
		_, ok := value.(string)
		return ok
	case VarNumber:
		_, ok := value.(json.Number)
		return ok
	case VarInteger:
		n, ok := value.(json.Number)
		return ok && isIntegral(n)
	case VarBoolean:
		_, ok := value.(bool)
		return ok
	case VarList:
		_, ok := value.([]any)
		return ok
	case VarPeriod:
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

// isIntegral reports whether a JSON number carries no fractional part.
// 1 and 1.0 qualify; 1.5 does not. The int64 path runs first so a value
// beyond float64's exact-integer range is not misjudged by the float
// fallback.
func isIntegral(n json.Number) bool {
	if _, err := n.Int64(); err == nil {
		return true
	}
	f, err := n.Float64()
	if err != nil {
		return false
	}
	return !math.IsInf(f, 0) && !math.IsNaN(f) && math.Trunc(f) == f
}

// describeKind renders the JSON kind a declared type accepts, for use in
// error prose.
func describeKind(vt VarType) string {
	switch vt {
	case VarString, VarField, VarDate, VarEnum:
		return "a JSON string"
	case VarNumber:
		return "a JSON number"
	case VarInteger:
		return "a JSON integer"
	case VarBoolean:
		return "a JSON boolean"
	case VarList:
		return "a JSON array"
	case VarPeriod:
		return "a JSON object"
	default:
		return "a JSON value"
	}
}

// observedKind names the JSON kind a decoded value actually carries.
func observedKind(value any) string {
	switch value.(type) {
	case string:
		return "a JSON string"
	case json.Number:
		return "a JSON number"
	case bool:
		return "a JSON boolean"
	case []any:
		return "a JSON array"
	case map[string]any:
		return "a JSON object"
	default:
		return "an unrecognised JSON value"
	}
}

// defaultKindMismatch builds the canonical declared-vs-supplied default
// mismatch error.
func defaultKindMismatch(t *Template, v *Variable, want VarType, got any) error {
	return invalidVar(t, v.Name, "template variable "+strconv.Quote(v.Name)+
		" is declared "+strconv.Quote(want.String())+" but its `default` is "+observedKind(got)+
		"; a default must be "+describeKind(want))
}

// targetList renders the valid target spellings for error prose.
func targetList() string {
	out := make([]string, 0, len(allTargets))
	for _, k := range allTargets {
		out = append(out, strconv.Quote(k.String()))
	}
	return strings.Join(out, ", ")
}

// varTypeList renders the valid variable-type spellings for error prose.
func varTypeList() string {
	out := make([]string, 0, len(allVarTypes))
	for _, k := range allVarTypes {
		out = append(out, strconv.Quote(k.String()))
	}
	return strings.Join(out, ", ")
}

// scalarTypeList renders the scalar variable-type spellings — the set
// legal as a list's element type — for error prose.
func scalarTypeList() string {
	out := make([]string, 0, len(allVarTypes))
	for _, k := range allVarTypes {
		if k.IsScalar() {
			out = append(out, strconv.Quote(k.String()))
		}
	}
	return strings.Join(out, ", ")
}

// invalid builds a template-scoped PULSE_TEMPLATE_INVALID.
func invalid(t *Template, msg string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_INVALID, msg,
		map[string]any{errors.DetailTemplate: t.Name})
}

// invalidVar builds a variable-scoped PULSE_TEMPLATE_INVALID, naming the
// offending variable under errors.DetailVariable.
func invalidVar(t *Template, variable, msg string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_INVALID, msg,
		map[string]any{
			errors.DetailTemplate: t.Name,
			errors.DetailVariable: variable,
		})
}

// invalidTarget builds a PULSE_TEMPLATE_TARGET_UNKNOWN carrying the
// offending target value alongside the template name.
func invalidTarget(t *Template, msg string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_TARGET_UNKNOWN, msg,
		map[string]any{
			errors.DetailTemplate: t.Name,
			"target":              t.Target.String(),
		})
}
