// Package strictjson is the ONE strict request decoder: every JSON
// request root Pulse accepts — the CLI request files, the MCP tool
// arguments, a Compose sweep body, a for_each item and a rendered
// request template — decodes through it, so an unknown key anywhere in
// the document is refused the same way on every path.
//
// The decode is encoding/json with DisallowUnknownFields semantics, but
// the refusal is located: before decoding, the raw document is walked
// against the target type's reflected json tags, and the FIRST object
// carrying an unrecognised key (an object's own keys before its
// children's, children in document order) is reported with its path
// (`sort[0]`, `crosstab.cell`, indexes included), the offending keys,
// the object's valid keys and a nearest-key suggestion. The standard
// library's own error names the key alone.
//
// Leaf package: stdlib + types + errors only, so internal/descriptor
// (no-execute), internal/service, internal/cli, internal/mcp,
// internal/sweep and internal/template can all import it
// (TestStrictJSON_ImportBoundary).
package strictjson

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// MetaKey is the example sidecar block (`internal/examples/`) a request
// file may carry at its root; Options.AllowMeta tolerates it there.
const MetaKey = "_meta"

// Location detail keys for an unknown key inside a nested request: the
// service's located-refusal keys (descx.RefusalAt), so a JSON caller
// and a library caller see the same details for the same request.
const (
	LocationRequest = "request" // ComposedRequest.requests[i]
	LocationFit     = "fit"     // ComposedRequest.fits[i].request
	LocationStage   = "stage"   // ChainRequest.stages[i].request
)

// Options tunes one decode.
type Options struct {
	// Hidden returns the keys of struct type t the calling instance
	// hides (a feature-profile-gated slot). A hidden key is refused
	// exactly as an unknown one and never appears in valid_keys or a
	// suggestion. Nil hides nothing.
	Hidden func(t reflect.Type) []string

	// AllowMeta tolerates a `_meta` key at the document root (the
	// examples sidecar block), as the lenient decode always did. A
	// rendered template leaves it false: there `_meta` is refused.
	AllowMeta bool

	// DocumentOrder reports the first unknown key in document order,
	// depth first — the key encoding/json's DisallowUnknownFields names
	// (a rendered template's contract). The default reports an object's
	// own keys before any child's, so a request root's misspelled slot
	// is refused before anything nested inside it (descx.SlotRefusal's
	// order).
	DocumentOrder bool

	// Aliases returns known misnomers for struct type t's keys — an
	// unknown key mapped to the valid key no edit distance would reach
	// (a synth correlation's `correlation` is `rho`). The refusal
	// suggests the alias ahead of the nearest-key guess
	// (UnknownFieldErrorAliased). Nil aliases nothing.
	Aliases func(t reflect.Type) map[string]string
}

// Unknown is the first object carrying unrecognised keys.
type Unknown struct {
	// Path is the object's location relative to the innermost request
	// (Location resets it): "" for that request's root, else e.g.
	// "sort[0]" or "crosstab.cell".
	Path string
	// FullPath is the object's location from the document root.
	FullPath string
	// Keys are the unrecognised (or hidden) keys, sorted.
	Keys []string
	// First is the object's first unrecognised key in document order.
	First string
	// Valid are the object's visible keys, in declaration order.
	Valid []string
	// Type is the object's struct type.
	Type reflect.Type
	// LocationKey / LocationIndex name the nested request Path is
	// relative to (LocationRequest / LocationFit / LocationStage and
	// its index); LocationKey is "" outside a nested request.
	LocationKey   string
	LocationIndex int
	// Aliases are Options.Aliases for Type, carried to the refusal.
	Aliases map[string]string
}

// KeyPath is the path of the first offending key: Path + "." + Keys[0]
// (Keys[0] alone at a request root).
func (u *Unknown) KeyPath() string { return joinPath(u.Path, u.Keys[0]) }

// FullKeyPath is KeyPath from the document root.
func (u *Unknown) FullKeyPath() string { return joinPath(u.FullPath, u.Keys[0]) }

// Error converts u into the PULSE_REQUEST_UNKNOWN_FIELD refusal,
// located by details[LocationKey] when u sits in a nested request.
func (u *Unknown) Error() *perr.CodedError {
	ce := UnknownFieldErrorAliased(u.Path, u.Keys, u.Valid, u.Aliases)
	if u.LocationKey != "" {
		ce.Details[u.LocationKey] = u.LocationIndex
	}
	return ce
}

// Decode strictly decodes raw into a fresh T (see DecodeInto).
func Decode[T any](raw []byte, opts Options) (T, error) {
	var out T
	err := DecodeInto(raw, &out, opts)
	return out, err
}

// DecodeInto strictly decodes raw into out (a non-nil pointer). An
// unrecognised or hidden key anywhere is a *errors.CodedError
// PULSE_REQUEST_UNKNOWN_FIELD (UnknownFieldError's shape, located);
// any other fault (malformed JSON, a wrong-typed value) is the
// encoding/json error, unchanged, for the caller to wrap.
func DecodeInto(raw []byte, out any, opts Options) error {
	if u := Check(raw, reflect.TypeOf(out), opts); u != nil {
		return u.Error()
	}
	return decodeStrict(raw, out, opts)
}

// Check walks raw against type t (a pointer chain is dereferenced) and
// returns the first object carrying an unrecognised or hidden key, or
// nil. Malformed JSON yields nil: the decode reports it.
func Check(raw []byte, t reflect.Type, opts Options) *Unknown {
	w := walker{opts: opts}
	return w.value(t, bytes.TrimSpace(raw), "", "", true)
}

// decodeStrict is the backstop decode: DisallowUnknownFields over the
// document (the root `_meta` removed when tolerated), so a key the walk
// cannot see — none is known — still refuses rather than drops.
func decodeStrict(raw []byte, out any, opts Options) error {
	body := raw
	if opts.AllowMeta {
		if stripped, ok := stripMeta(raw); ok {
			body = stripped
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if field, ok := UnknownFieldName(err); ok {
			return UnknownFieldError("", []string{field}, nil)
		}
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		// Trailing data after the value: json.Unmarshal refuses it, so
		// the strict decode does too.
		return errors.New("invalid character after top-level value")
	}
	return nil
}

// stripMeta removes a root `_meta` key, reporting whether it found one.
func stripMeta(raw []byte) ([]byte, bool) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil, false
	}
	if _, ok := top[MetaKey]; !ok {
		return nil, false
	}
	delete(top, MetaKey)
	b, err := json.Marshal(top)
	if err != nil {
		return nil, false
	}
	return b, true
}

// UnknownFieldName extracts the key from encoding/json's
// DisallowUnknownFields fault; ok reports whether err was that fault.
func UnknownFieldName(err error) (string, bool) {
	const prefix = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	quoted := strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	if name, uerr := strconv.Unquote(quoted); uerr == nil {
		return name, true
	}
	return quoted, true
}

// joinPath appends key to the object path p.
func joinPath(p, key string) string {
	if p == "" {
		return key
	}
	return p + "." + key
}

var (
	rawMessageType      = reflect.TypeFor[json.RawMessage]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// objectShapes is the audited table of request types whose custom
// UnmarshalJSON accepts a JSON object of ANOTHER struct's shape: the
// walk checks the object against that shape. Every json.Unmarshaler
// reachable from a request root is listed here or in walkedUnmarshalers
// / opaqueUnmarshalers (TestStrictJSON_UnmarshalersAudited).
var objectShapes = map[reflect.Type]reflect.Type{
	// null | "field" | {field, kind}; the object form is a WeightSpec
	// (its own UnmarshalJSON refuses other keys too).
	reflect.TypeFor[types.SlotWeight](): reflect.TypeFor[types.WeightSpec](),
}

// walkedUnmarshalers are custom unmarshallers whose object form is the
// type's own exported fields: the walk checks them like a plain struct.
var walkedUnmarshalers = map[reflect.Type]bool{
	// SweepAxis keeps numbers as json.Number; its keys are its fields.
	reflect.TypeFor[types.SweepAxis](): true,
}

// opaqueUnmarshalers never accept a JSON object, so there is no key to
// check: the type's own UnmarshalJSON refuses an object outright.
var opaqueUnmarshalers = map[reflect.Type]bool{
	reflect.TypeFor[types.CovarianceLags](): true, // "auto" | integer | null
	reflect.TypeFor[types.PriorPrecision](): true, // number | array of numbers | null
}

// locatedFields are the struct fields holding a nested Request: the
// walk restarts Path there and locates it by the enclosing index.
var locatedFields = map[reflect.Type]map[string]string{
	reflect.TypeFor[types.ComposedRequest](): {"requests": LocationRequest},
	reflect.TypeFor[types.FitSpec]():         {"request": LocationFit},
	reflect.TypeFor[types.ChainStage]():      {"request": LocationStage},
}

// field is one decodable JSON key of a struct.
type field struct {
	name string
	typ  reflect.Type
}

// structFields is a struct's decodable keys, encoding/json's view:
// tagged names, untagged exported fields by Go name, embedded structs
// promoted (an outer name wins).
type structFields struct {
	byName map[string]field
	order  []string
}

var fieldCache sync.Map // reflect.Type -> *structFields

func fieldsOf(t reflect.Type) *structFields {
	if v, ok := fieldCache.Load(t); ok {
		return v.(*structFields)
	}
	sf := &structFields{byName: map[string]field{}}
	collectFields(t, sf, map[reflect.Type]bool{})
	fieldCache.Store(t, sf)
	return sf
}

func collectFields(t reflect.Type, sf *structFields, seen map[reflect.Type]bool) {
	if seen[t] {
		return
	}
	seen[t] = true
	var embedded []reflect.Type
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				embedded = append(embedded, ft)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if _, dup := sf.byName[name]; dup {
			continue
		}
		sf.byName[name] = field{name: name, typ: f.Type}
		sf.order = append(sf.order, name)
	}
	for _, et := range embedded {
		collectFields(et, sf, seen)
	}
}

// lookup finds key as encoding/json does: exact, else case-insensitive.
func (sf *structFields) lookup(key string) (field, bool) {
	if f, ok := sf.byName[key]; ok {
		return f, true
	}
	for _, n := range sf.order {
		if strings.EqualFold(n, key) {
			return sf.byName[n], true
		}
	}
	return field{}, false
}

// walker carries one Check's options and nested-request location.
type walker struct {
	opts     Options
	locKey   string
	locIndex int
}

// member is one key/value of a JSON object, in document order.
type member struct {
	key string
	val json.RawMessage
}

// objectMembers parses raw as a JSON object, keeping document order.
func objectMembers(raw []byte) ([]member, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := tok.(string)
		if !ok {
			return nil, false
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, false
		}
		out = append(out, member{key: key, val: val})
	}
	return out, true
}

// value walks raw against t. path is relative to the innermost nested
// request, full to the document root; root reports the document root.
func (w *walker) value(t reflect.Type, raw []byte, path, full string, root bool) *Unknown {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType {
		return nil // operator params: checked against the manifest, not here
	}
	if shape, ok := objectShapes[t]; ok {
		if raw[0] != '{' {
			return nil
		}
		t = shape
	} else if implements(t, jsonUnmarshalerType) && !walkedUnmarshalers[t] {
		return nil // opaqueUnmarshalers: no object form to check
	} else if implements(t, textUnmarshalerType) && raw[0] == '"' {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		return w.object(t, raw, path, full, root)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 || raw[0] != '[' {
			return nil
		}
		var elems []json.RawMessage
		if json.Unmarshal(raw, &elems) != nil {
			return nil
		}
		for i, e := range elems {
			idx := "[" + strconv.Itoa(i) + "]"
			if u := w.value(t.Elem(), bytes.TrimSpace(e), path+idx, full+idx, false); u != nil {
				return u
			}
		}
	case reflect.Map:
		if raw[0] != '{' {
			return nil
		}
		members, ok := objectMembers(raw)
		if !ok {
			return nil
		}
		for _, m := range members {
			if u := w.value(t.Elem(), bytes.TrimSpace(m.val), joinPath(path, m.key), joinPath(full, m.key), false); u != nil {
				return u
			}
		}
	}
	return nil
}

// object walks a JSON object against struct type t: its own keys
// first, then each member's value in document order.
func (w *walker) object(t reflect.Type, raw []byte, path, full string, root bool) *Unknown {
	if raw[0] != '{' {
		return nil
	}
	members, ok := objectMembers(raw)
	if !ok {
		return nil
	}
	sf := fieldsOf(t)
	var hidden []string
	if w.opts.Hidden != nil {
		hidden = w.opts.Hidden(t)
	}
	isHidden := func(name string) bool {
		for _, h := range hidden {
			if h == name {
				return true
			}
		}
		return false
	}

	located := locatedFields[t]
	known := func(m member) (field, bool) {
		if root && w.opts.AllowMeta && m.key == MetaKey {
			return field{}, true // tolerated; f.typ nil: not walked
		}
		f, ok := sf.lookup(m.key)
		if !ok || isHidden(f.name) {
			return field{}, false
		}
		return f, true
	}
	refuse := func() *Unknown {
		var unknown []string
		first := ""
		for _, m := range members {
			if _, ok := known(m); !ok {
				if first == "" {
					first = m.key
				}
				unknown = append(unknown, m.key)
			}
		}
		if len(unknown) == 0 {
			return nil
		}
		sort.Strings(unknown)
		valid := make([]string, 0, len(sf.order))
		for _, n := range sf.order {
			if !isHidden(n) {
				valid = append(valid, n)
			}
		}
		var aliases map[string]string
		if w.opts.Aliases != nil {
			aliases = w.opts.Aliases(t)
		}
		return &Unknown{Path: path, FullPath: full, Keys: unknown, First: first, Valid: valid, Type: t,
			LocationKey: w.locKey, LocationIndex: w.locIndex, Aliases: aliases}
	}
	if !w.opts.DocumentOrder {
		if u := refuse(); u != nil {
			return u
		}
	}
	for _, m := range members {
		f, ok := known(m)
		if !ok {
			return refuse() // DocumentOrder: this object holds the first
		}
		if f.typ == nil {
			continue // a tolerated root _meta
		}
		childFull := joinPath(full, m.key)
		if loc, ok := located[f.name]; ok {
			if u := w.located(loc, f.typ, bytes.TrimSpace(m.val), childFull); u != nil {
				return u
			}
			continue
		}
		if u := w.value(f.typ, bytes.TrimSpace(m.val), joinPath(path, m.key), childFull, false); u != nil {
			return u
		}
	}
	return nil
}

// located walks a field holding nested requests: each Request restarts
// the relative path and carries its index as the location. A slice of
// requests (ComposedRequest.requests) is indexed by its own elements;
// a single request (fits[i].request, stages[i].request) by the
// enclosing array element, recovered from the full path.
func (w *walker) located(key string, t reflect.Type, raw []byte, full string) *Unknown {
	saveKey, saveIdx := w.locKey, w.locIndex
	defer func() { w.locKey, w.locIndex = saveKey, saveIdx }()

	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() == reflect.Slice {
		if len(raw) == 0 || raw[0] != '[' {
			return nil
		}
		var elems []json.RawMessage
		if json.Unmarshal(raw, &elems) != nil {
			return nil
		}
		for i, e := range elems {
			w.locKey, w.locIndex = key, i
			if u := w.value(base.Elem(), bytes.TrimSpace(e), "", full+"["+strconv.Itoa(i)+"]", false); u != nil {
				return u
			}
		}
		return nil
	}
	w.locKey, w.locIndex = key, enclosingIndex(full)
	return w.value(t, raw, "", full, false)
}

// enclosingIndex is the last `[i]` index in full ("fits[2].request" →
// 2), or 0.
func enclosingIndex(full string) int {
	end := strings.LastIndexByte(full, ']')
	if end < 0 {
		return 0
	}
	start := strings.LastIndexByte(full[:end], '[')
	if start < 0 {
		return 0
	}
	n, err := strconv.Atoi(full[start+1 : end])
	if err != nil {
		return 0
	}
	return n
}

// implements reports whether t or *t implements iface.
func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}
