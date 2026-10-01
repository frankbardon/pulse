package template

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// The three pieces of template syntax the body carries. They are the whole
// grammar — there is no expression language here, and deliberately so: a
// template body is a request with holes in it, not a program.
const (
	// markerKey is the lone key that turns an object into a slot marker.
	// An object is a marker IFF it has exactly this one key and its value
	// is a string. An object carrying `$var` alongside any other key is
	// literal data and is walked normally.
	markerKey = "$var"

	// guardKey is the optional key that makes an object conditional. Its
	// value names a variable; the object survives iff that variable
	// resolved. The key itself never reaches the output.
	guardKey = "$when"

	// interpOpen and interpClose delimit a string-interpolation token.
	interpOpen  = "{{"
	interpClose = "}}"

	// interpEscape is the escape for a literal interpOpen. It is a
	// doubling rather than a backslash so the template stays ordinary
	// JSON at rest — a backslash would need escaping in the JSON string
	// too, and `\\{\\{` is unreadable.
	interpEscape = "{{{{"
)

// rootGuardMessage is shared by the declaration-time and render-time root
// guard checks, which must report the fault identically.
const rootGuardMessage = "template body carries a `" + guardKey +
	"` guard at its root; the root body is the request itself and cannot be dropped — " +
	"guard the individual slots inside it instead"

// RenderJSON resolves a template's variables against a caller-supplied map
// and walks the body, returning the rendered JSON.
//
// It is the substitution half of rendering: the result is well-formed JSON
// shaped like the target request, but it has not yet been decoded into the
// target type. Strict decode is a separate step.
//
// The walk applies exactly three transformations:
//
//  1. Slot markers. `{"$var": "bucket"}` is replaced, whole, by the
//     resolved value of `bucket` — TYPE-PRESERVING, so
//     `{"interval": {"$var": "bucket"}}` renders to `{"interval": 10}` and
//     never to `{"interval": "10"}`. An object is a marker only if `$var`
//     is its single key and its value is a string; `{"$var": "x",
//     "other": 1}` is literal data.
//
//  2. String sugar. Inside a string VALUE, `{{name}}` interpolates the
//     variable's natural text — strings verbatim, numbers as their exact
//     literal, booleans as `true`/`false`, dates as their ISO string.
//     `{{{{` is the escape for a literal `{{`. A `list` or a `period`
//     variable has no natural text and inside a string is
//     PULSE_TEMPLATE_VAR_TYPE — splice it with a marker instead. Object
//     KEYS are not interpolated (declaration validation rejects `{{` in a
//     key outright, so this is never a silent no-op).
//
//  3. `$when` guards. `{"$when": "segments", …}` survives iff `segments`
//     resolved; the key is stripped either way. A dropped object is
//     REMOVED from its parent array (the slice is compacted — no null
//     hole is left behind, which would decode to a nil operator slot) or
//     its key is removed from its parent object. `$when` on the root body
//     is an error.
//
// Guards evaluate BEFORE the walk descends, so an unresolved marker inside
// a dropped block never raises. That ordering is the whole point of the
// guard: it is how an author says "this slot is optional".
//
// Resolution is presence semantics, not truthiness. A variable resolved to
// "", [], 0, or false is resolved, and a block guarded on it STAYS — a
// legitimate zero has to be templatable.
//
// A surviving unguarded marker or `{{token}}` naming a variable that
// resolved to nothing is PULSE_TEMPLATE_UNRESOLVED. A marker never
// silently vanishes: optionality is spelled `$when`, always.
//
// Substituted values are spliced as data and are NOT re-walked. A caller
// who supplies the string "{{metric}}" gets that string verbatim in the
// rendered request, not a second round of interpolation.
//
// Faults follow the family's provenance rule. A body that references a
// variable the template does not declare is a template AUTHOR error and is
// caught at declaration time by Validate, not here. A variable that is
// declared but resolved to nothing is PULSE_TEMPLATE_UNRESOLVED at render.
// Every returned error carries errors.DetailTemplate and, when the fault
// is variable-scoped, errors.DetailVariable, plus the body path under
// "path".
func RenderJSON(t *Template, supplied map[string]any) (json.RawMessage, error) {
	res, err := Resolve(t, supplied)
	if err != nil {
		return nil, err
	}

	body, err := decodeJSON(t.Body)
	if err != nil {
		// Unreachable in practice: Validate (via Resolve) has already
		// established the body is a decodable JSON object.
		return nil, invalidAt(t, bodyPath, "", "template body is not decodable JSON: "+err.Error())
	}

	r := &renderer{t: t, res: res}
	rendered, keep, err := r.value(body, bodyPath)
	if err != nil {
		return nil, err
	}
	if !keep {
		// Also unreachable: a root guard is rejected at declaration time.
		return nil, invalidAt(t, bodyPath, "", rootGuardMessage)
	}
	return marshalRendered(t, rendered)
}

// bodyPath is the root of the dotted body path carried in render faults.
const bodyPath = "body"

// marshalRendered re-marshals the walked tree. HTML escaping is off so a
// string carrying <, > or & survives as written rather than arriving as a
// < escape — the bytes decode identically either way, but the
// rendered JSON is read by humans and diffed in tests.
func marshalRendered(t *Template, tree any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tree); err != nil {
		return nil, invalidAt(t, bodyPath, "",
			"rendered template body is not representable as JSON: "+err.Error())
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// renderer carries the walk's immutable inputs. It holds no cursor: the
// walk is a pure recursion and the path is threaded as an argument, so a
// renderer is safe to reuse and impossible to leave in a half-walked
// state.
type renderer struct {
	t   *Template
	res *Resolution
}

// value renders one node. The bool reports whether the node survives; a
// false means a `$when` guard dropped it and the parent must omit it
// entirely rather than substitute a null.
func (r *renderer) value(v any, path string) (any, bool, error) {
	switch node := v.(type) {
	case map[string]any:
		return r.object(node, path)
	case []any:
		return r.array(node, path)
	case string:
		s, err := r.interpolate(node, path)
		if err != nil {
			return nil, false, err
		}
		return s, true, nil
	default:
		// Numbers (json.Number), bools, and null pass through untouched.
		return v, true, nil
	}
}

// object renders a JSON object: marker first, then guard, then children.
//
// The order is load-bearing. A marker is consumed whole and never
// descended into. A guard is evaluated BEFORE any child is rendered, so an
// unresolved marker inside a doomed subtree raises nothing — the subtree
// is never visited.
func (r *renderer) object(obj map[string]any, path string) (any, bool, error) {
	if name, ok := markerName(obj); ok {
		val, err := r.substitute(name, path)
		if err != nil {
			return nil, false, err
		}
		return val, true, nil
	}

	if raw, guarded := obj[guardKey]; guarded {
		name, ok := guardVarName(raw)
		if !ok {
			return nil, false, invalidGuardValue(r.t, path, raw)
		}
		if !r.res.Declared(name) {
			return nil, false, r.undeclared(name, path, subjectGuard)
		}
		if !r.res.IsResolved(name) {
			return nil, false, nil // dropped — do not descend
		}
	}

	// Keys are walked in sorted order so the first fault reported for a
	// body with several is deterministic across runs; Go's map iteration
	// order is not.
	out := make(map[string]any, len(obj))
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if key == guardKey {
			continue // a passing guard is stripped from the output
		}
		child, keep, err := r.value(obj[key], childPath(path, key))
		if err != nil {
			return nil, false, err
		}
		if !keep {
			continue // a dropped object value takes its parent key with it
		}
		out[key] = child
	}
	return out, true, nil
}

// array renders a JSON array, compacting out every dropped element. The
// compaction is the point: leaving a null behind would decode to a nil
// operator slot, which is exactly the silent-omission failure this whole
// surface exists to prevent.
func (r *renderer) array(elems []any, path string) (any, bool, error) {
	out := make([]any, 0, len(elems))
	for i, e := range elems {
		child, keep, err := r.value(e, indexPath(path, i))
		if err != nil {
			return nil, false, err
		}
		if !keep {
			continue
		}
		out = append(out, child)
	}
	return out, true, nil
}

// substitute resolves a slot marker to its value, spliced whole so the
// JSON type is preserved.
func (r *renderer) substitute(name, path string) (any, error) {
	if !r.res.Declared(name) {
		return nil, r.undeclared(name, path, subjectMarker)
	}
	val, ok := r.res.Get(name)
	if !ok {
		return nil, r.unresolved(name, path, subjectMarker)
	}
	return val, nil
}

// interpolate expands every `{{name}}` token in a string value. A string
// with no token is returned untouched, so the common case allocates
// nothing.
func (r *renderer) interpolate(s, path string) (string, error) {
	if !strings.Contains(s, interpOpen) {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	err := walkInterpolation(r.t, s, path,
		func(literal string) { b.WriteString(literal) },
		func(name string) error {
			text, err := r.scalarText(name, path)
			if err != nil {
				return err
			}
			b.WriteString(text)
			return nil
		})
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

// scalarText renders a resolved value as the text an interpolation splices
// in. A json.Number is written via String() rather than reformatted, so
// the exact literal the caller wrote survives — a u64 beyond float64's
// exact-integer range keeps its low bits, and 1.50 does not become 1.5.
func (r *renderer) scalarText(name, path string) (string, error) {
	if !r.res.Declared(name) {
		return "", r.undeclared(name, path, subjectInterp)
	}
	val, ok := r.res.Get(name)
	if !ok {
		return "", r.unresolved(name, path, subjectInterp)
	}
	switch v := val.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case []any:
		return "", r.notScalar(name, path, "a JSON array")
	case map[string]any:
		return "", r.notScalar(name, path, "a JSON object")
	default:
		return "", r.notScalar(name, path, observedKind(val))
	}
}

// markerName reports the variable a slot-marker object references.
//
// The recognition rule is exact and total: an object is a marker IFF it
// has exactly one key, that key is `$var`, and its value is a string. Any
// other shape — `$var` beside another key, `$var` holding a number — is
// not a marker. Surrounding whitespace in the name is trimmed, matching
// the interpolation tokens.
func markerName(obj map[string]any) (string, bool) {
	if len(obj) != 1 {
		return "", false
	}
	raw, ok := obj[markerKey]
	if !ok {
		return "", false
	}
	name, ok := raw.(string)
	if !ok {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	return name, true
}

// isLoneMarkerKey reports whether obj has exactly one key and it is
// `$var`, regardless of the value's kind. It is how declaration validation
// catches a near-miss marker such as `{"$var": 3}`: that object is not a
// marker by the rule above, so it would survive into the request as
// literal data and fail the strict decode with a mystifying message.
// Rejecting it at declaration time is the loud failure instead.
func isLoneMarkerKey(obj map[string]any) bool {
	if len(obj) != 1 {
		return false
	}
	_, ok := obj[markerKey]
	return ok
}

// guardVarName extracts the variable name from a `$when` value. The value
// must be a non-empty string; whitespace is trimmed.
func guardVarName(raw any) (string, bool) {
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

// walkInterpolation parses a string's interpolation grammar, handing every
// literal run to lit and every token's variable name to ref. It is the
// single parser: declaration validation walks with a no-op lit to check
// the referenced names, and the render walk walks with a builder. One
// parser means the two can never disagree about what a token is.
//
// The grammar, in scan order:
//
//	{{{{     a literal `{{`, consumed as four bytes
//	{{name}} a token; name is trimmed and must be non-empty
//	}}       outside a token, ordinary text
//
// An unterminated `{{` and an empty `{{}}` are template AUTHOR errors and
// report PULSE_TEMPLATE_INVALID. They are never treated as literal text:
// silently passing `{{metric` through would put a broken token in the
// rendered request.
func walkInterpolation(t *Template, s, path string, lit func(string), ref func(name string) error) error {
	for i := 0; i < len(s); {
		rel := strings.Index(s[i:], interpOpen)
		if rel < 0 {
			lit(s[i:])
			return nil
		}
		open := i + rel
		lit(s[i:open])

		if strings.HasPrefix(s[open:], interpEscape) {
			lit(interpOpen)
			i = open + len(interpEscape)
			continue
		}

		rest := s[open+len(interpOpen):]
		closeRel := strings.Index(rest, interpClose)
		if closeRel < 0 {
			return invalidAt(t, path, "", "template body at "+path+
				" carries an unterminated interpolation token: `"+interpOpen+
				"` with no closing `"+interpClose+"`; write `"+interpEscape+
				"` for a literal `"+interpOpen+"`")
		}
		name := strings.TrimSpace(rest[:closeRel])
		if name == "" {
			return invalidAt(t, path, "", "template body at "+path+
				" carries an empty interpolation token `"+interpOpen+interpClose+
				"`; name a declared variable inside it")
		}
		if err := ref(name); err != nil {
			return err
		}
		i = open + len(interpOpen) + closeRel + len(interpClose)
	}
	return nil
}

// subject names, in error prose, which piece of syntax raised a fault.
type subject string

const (
	subjectMarker subject = "slot marker `{\"" + markerKey + "\": …}`"
	subjectGuard  subject = "`" + guardKey + "` guard"
	subjectInterp subject = "interpolation token `" + interpOpen + interpClose + "`"
)

// undeclared builds the reference-to-an-undeclared-variable fault. It is a
// template AUTHOR error, so it is PULSE_TEMPLATE_INVALID — and declaration
// validation catches every instance of it before a render can be attempted,
// which makes this path defensive rather than reachable.
func (r *renderer) undeclared(name, path string, s subject) error {
	return undeclaredRef(r.t, name, path, s)
}

// unresolved builds the render-time fault for a marker or token naming a
// variable that resolved to nothing and was not guarded.
func (r *renderer) unresolved(name, path string, s subject) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_UNRESOLVED,
		"template body at "+path+": "+string(s)+" references variable "+strconv.Quote(name)+
			", which resolved to nothing; supply a value, give the declaration a `default`, "+
			"or make the slot optional with "+strconv.Quote(guardKey)+": "+strconv.Quote(name),
		renderDetails(r.t, name, path))
}

// notScalar builds the has-no-natural-text fault for a list or period
// variable referenced inside a string. It is PULSE_TEMPLATE_VAR_TYPE: the
// variable's value is the wrong kind for where it was used.
func (r *renderer) notScalar(name, path, kind string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_VAR_TYPE,
		"template body at "+path+": "+string(subjectInterp)+" references variable "+
			strconv.Quote(name)+", which resolved to "+kind+
			"; only string, number, integer, boolean, field, enum, and date variables have a text form — "+
			"splice this one with a "+strconv.Quote(markerKey)+" marker instead of interpolating it",
		renderDetails(r.t, name, path))
}

// undeclaredRef builds the body-references-an-undeclared-variable fault,
// shared by declaration validation and the render walk's defensive check.
func undeclaredRef(t *Template, name, path string, s subject) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_INVALID,
		"template body at "+path+": "+string(s)+" references variable "+strconv.Quote(name)+
			", which the template does not declare; declared variables are "+declaredList(t),
		renderDetails(t, name, path))
}

// invalidGuardValue builds the fault for a `$when` whose value is not a
// non-empty string.
func invalidGuardValue(t *Template, path string, raw any) error {
	return invalidAt(t, path, "", "template body at "+path+" carries a "+
		strconv.Quote(guardKey)+" guard whose value is "+observedKind(raw)+
		"; a guard must be a non-empty JSON string naming a declared variable")
}

// invalidAt builds a body-scoped PULSE_TEMPLATE_INVALID carrying the body
// path, and the variable when one is known.
func invalidAt(t *Template, path, variable, msg string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_INVALID, msg,
		renderDetails(t, variable, path))
}

// renderDetails is the detail map every body-scoped fault carries.
func renderDetails(t *Template, variable, path string) map[string]any {
	return map[string]any{
		errors.DetailTemplate: t.Name,
		errors.DetailVariable: variable,
		"path":                path,
	}
}

// childPath extends a body path with an object key.
func childPath(path, key string) string { return path + "." + key }

// indexPath extends a body path with an array index.
func indexPath(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }
