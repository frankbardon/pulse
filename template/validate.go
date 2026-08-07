package template

import (
	"bytes"
	"encoding/json"
	"maps"
	"math"
	"slices"
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
// The document wrapper is decoded strictly: an unknown key on the wrapper,
// or on a variable declaration, is PULSE_TEMPLATE_INVALID. A typo'd
// "varaibles" would otherwise parse cleanly into a template with zero
// variables and fail much later — or worse, render a request with every
// marker unresolved — which is exactly the silent-failure class this whole
// surface exists to eliminate.
//
// The strictness stops at `body`. The body stays raw JSON here and is
// strict-decoded against its target request type after rendering, since
// before substitution it is not a request and its markers are not request
// fields.
func Parse(data []byte) (*Template, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var t Template
	if err := dec.Decode(&t); err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_TEMPLATE_INVALID,
			parseFaultMessage(err))
	}
	if dec.More() {
		return nil, errors.NewCodedError(errors.PULSE_TEMPLATE_INVALID,
			"template document carries trailing content after the closing brace; a template file holds exactly one JSON object")
	}
	if err := Validate(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// parseFaultMessage distinguishes an unknown wrapper key from genuinely
// malformed bytes, so the remedy in the message matches the fault. The
// encoding/json decoder reports the former with a stable "json: unknown
// field" prefix and no dedicated error type to assert on.
func parseFaultMessage(err error) string {
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return "template document carries an unrecognised key (" + err.Error() +
			"); a template holds only `name`, `description`, `target`, `variables`, and `body`"
	}
	return "template document is not valid JSON: " + err.Error()
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
//   - a default that fails the declared
//     type's acceptance rule             → PULSE_TEMPLATE_INVALID
//   - a body marker, `$when` guard, or
//     `{{token}}` naming an undeclared
//     variable                           → PULSE_TEMPLATE_INVALID
//   - a malformed body marker, guard, or
//     interpolation token                → PULSE_TEMPLATE_INVALID
//   - a `$when` on the root body         → PULSE_TEMPLATE_INVALID
//
// Two things are deliberately NOT faults here. Required together with a
// default is legal: the default resolves the variable, so the pair can
// never leave it unresolved. And Name is not required — a template
// unmarshaled directly in Go may leave it empty, because naming is the
// store's job (it derives the name from the file's path).
//
// Default checking is fully semantic, not merely JSON-kind deep: an enum
// default is membership-checked against `values`, a date default is
// parsed, and a period default's ranges-XOR-table shape is enforced. It
// runs the identical checkValue used on caller-supplied values at render
// — only the provenance differs, and provenance is what selects the code.
// A bad default is a template AUTHOR error, so it is always
// PULSE_TEMPLATE_INVALID rather than a PULSE_TEMPLATE_VAR_* code, and
// catching it here is what makes fail-fast-at-registration real: a
// template with an unparseable date default must not lie in wait until
// someone renders it.
//
// The body is scanned too, but only for the three pieces of template
// syntax it carries — slot markers, `$when` guards, and `{{token}}`
// interpolation. Every name they reference must be DECLARED. A body that
// says `{"$var": "metrc"}` when the template declares `metric` is a
// template author's typo, so it is PULSE_TEMPLATE_INVALID and it fails
// here, at registration, rather than lying in wait until someone renders.
// That is a different failure from a declared-but-unresolved variable,
// which is a render-time PULSE_TEMPLATE_UNRESOLVED and cannot be known
// until a caller's variable map arrives.
//
// The scan does NOT check request semantics. Body keys, operator names,
// and field names stay unvalidated here — before substitution the body is
// not a request, and its keys are checked by the strict decode that runs
// after rendering.
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
	if err := validateVariables(t); err != nil {
		return err
	}
	return validateBodyReferences(t)
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
// declared type, using the same acceptance rule a caller-supplied value
// goes through at render. An absent default, and an explicit JSON null,
// are both "no default" and check nothing.
//
// The fromDefault provenance is what turns every fault checkValue can
// raise into PULSE_TEMPLATE_INVALID: the fault is in the document, not in
// anybody's call.
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
	return checkValue(t, v, value, fromDefault)
}

// validateBodyReferences walks the decoded body and checks every variable
// reference against the declared set, plus the well-formedness of the
// three markers themselves.
//
// It runs after validateVariables so the declared set is known to be
// coherent, and it runs on EVERY validation — including the one Resolve
// performs on the way into a render — so the author-error class can never
// be reached by the render walk.
//
// Unlike the render walk, this scan descends into guarded objects. A guard
// is a runtime condition; a typo'd variable name inside a block that
// happens not to be selected today is still a typo, and hiding it until
// the day someone supplies that variable is the opposite of fail-fast.
func validateBodyReferences(t *Template) error {
	body, err := decodeJSON(t.Body)
	if err != nil {
		// Unreachable: validateBody has already established the body
		// decodes as a JSON object.
		return invalidAt(t, bodyPath, "", "template body is not decodable JSON: "+err.Error())
	}
	if obj, ok := body.(map[string]any); ok {
		if _, guarded := obj[guardKey]; guarded {
			return invalidAt(t, bodyPath, "", rootGuardMessage)
		}
	}

	declared := make(map[string]struct{}, len(t.Variables))
	for _, v := range t.Variables {
		if v != nil {
			declared[v.Name] = struct{}{}
		}
	}
	return (&bodyScan{t: t, declared: declared}).value(body, bodyPath)
}

// bodyScan is the declaration-time body walker. It mirrors the render
// walk's syntax recognition exactly — both go through markerName,
// guardVarName, and walkInterpolation — but it evaluates nothing and
// resolves nothing, so it needs no caller-supplied variables.
type bodyScan struct {
	t        *Template
	declared map[string]struct{}
}

// value dispatches on the node's JSON kind.
func (s *bodyScan) value(v any, path string) error {
	switch node := v.(type) {
	case map[string]any:
		return s.object(node, path)
	case []any:
		for i, e := range node {
			if err := s.value(e, indexPath(path, i)); err != nil {
				return err
			}
		}
		return nil
	case string:
		return s.text(node, path)
	default:
		return nil
	}
}

// object checks a marker, a guard, and every key, in that order.
func (s *bodyScan) object(obj map[string]any, path string) error {
	if isLoneMarkerKey(obj) {
		return s.marker(obj[markerKey], path)
	}

	if raw, guarded := obj[guardKey]; guarded {
		name, ok := guardVarName(raw)
		if !ok {
			return invalidGuardValue(s.t, path, raw)
		}
		if !s.isDeclared(name) {
			return undeclaredRef(s.t, name, path, subjectGuard)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if strings.Contains(key, interpOpen) {
			return invalidAt(s.t, childPath(path, key), "", "template body at "+path+
				" carries "+strconv.Quote(interpOpen)+" inside the object key "+strconv.Quote(key)+
				"; interpolation applies to string VALUES only, and a key left unexpanded would "+
				"reach the request verbatim")
		}
		if key == guardKey {
			continue
		}
		if err := s.value(obj[key], childPath(path, key)); err != nil {
			return err
		}
	}
	return nil
}

// marker checks a lone-`$var` object. The near-miss shapes — a non-string
// value, an empty name — are rejected here rather than passed through as
// literal data, because literal data carrying a `$var` key can only ever
// fail the post-render strict decode with a far worse message.
func (s *bodyScan) marker(raw any, path string) error {
	name, ok := raw.(string)
	if !ok {
		return invalidAt(s.t, path, "", "template body at "+path+" carries a lone "+
			strconv.Quote(markerKey)+" key whose value is "+observedKind(raw)+
			"; a slot marker is {"+strconv.Quote(markerKey)+": \"<variable name>\"} with a string name")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return invalidAt(s.t, path, "", "template body at "+path+" carries a "+
			strconv.Quote(markerKey)+" marker with an empty variable name")
	}
	if !s.isDeclared(name) {
		return undeclaredRef(s.t, name, path, subjectMarker)
	}
	return nil
}

// text checks every interpolation token in a string value.
func (s *bodyScan) text(str, path string) error {
	if !strings.Contains(str, interpOpen) {
		return nil
	}
	return walkInterpolation(s.t, str, path, func(string) {}, func(name string) error {
		if !s.isDeclared(name) {
			return undeclaredRef(s.t, name, path, subjectInterp)
		}
		return nil
	})
}

// isDeclared reports whether the template declares name. Lookup is exact
// and case-sensitive, matching Template.Variable.
func (s *bodyScan) isDeclared(name string) bool {
	_, ok := s.declared[name]
	return ok
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
// object's ranges-XOR-table shape is not inspected. Those value-level
// checks layer on top of it in checkValue (vars.go), which is the single
// acceptance rule for defaults and caller-supplied values alike.
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
