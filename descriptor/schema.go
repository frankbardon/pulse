package descriptor

import (
	"encoding/json"
	"reflect"
	"sort"

	"github.com/frankbardon/pulse/types"
)

// PayloadSchemaFormatVersion tracks the envelope format_version (see
// Envelope.FormatVersion / NewEnvelope). The published payload schema's
// $id embeds it; bump the two in lockstep. Guarded by
// TestPayloadSchema_VersionMatchesEnvelope.
const PayloadSchemaFormatVersion = "1.1"

const (
	// payloadSchemaDialect is the JSON Schema draft the contract targets.
	payloadSchemaDialect = "https://json-schema.org/draft/2020-12/schema"

	// payloadSchemaID is the canonical, version-stamped identifier. It is
	// also the stable github.io URL the docs workflow publishes the file
	// to, so the $id doubles as a retrieval URI.
	payloadSchemaID = "https://frankbardon.github.io/pulse/payload-schema.json"
)

// BuildPayloadSchema returns the bundled JSON Schema (draft 2020-12)
// describing every public Pulse payload — the request envelopes
// (Request, ComposedRequest, ChainRequest, FacetRequest, SampleRequest)
// and the universal output Envelope plus every operation result it can
// wrap.
//
// The schema is generated three ways, not hand-maintained:
//
//  1. Reflection over the Go payload structs (descriptor stays free of
//     service/ and processing/ imports — only types + this package). A
//     struct field change surfaces as a golden diff, forcing a regen.
//  2. Registry-injected enums: the high-cardinality discriminant enums
//     (operator + overlay-kind + regression families) draw their value
//     lists from types.All*Types() / AllOverlayKinds() / AllRegressionTypes(),
//     so registering a new operator changes the schema and trips the gate.
//  3. Hand-tuned strict unions for the two shapes reflection cannot
//     express faithfully: OverlayRef (at-most-one populated arm) and
//     OverlayPayload (shape-discriminated scalar/series/matrix).
//
// v1 boundaries (documented in docs/src/contract/payload-schema.md):
//   - Operator Params (json.RawMessage) stay an open object — there is no
//     central declarative per-operator input-param source (even the MCP
//     binder treats params as open); the per-operator param schema lives
//     alongside each operator's processor.
//   - The small closed mode enums (OverlayScope, OverlayShape,
//     CrosstabNormalize, etc.) are typed as plain strings: they have no
//     All*() registry helper, so a hardcoded enum here would rot silently.
//
// The result marshals deterministically: every object is a map (encoding
// /json sorts keys) and every enum / required list is sorted, so the
// golden is stable across runs and Go versions.
func BuildPayloadSchema() json.RawMessage {
	b := newSchemaBuilder()

	// Entry points. Order matters only for the root oneOf, which we sort.
	// Both request and result shapes are first-class entry points. The
	// Envelope's data slot is genuinely polymorphic (any operation —
	// process, manifest, predict, inspect, …), so it stays open; consumers
	// validate the unwrapped result against its own def (#/$defs/Response,
	// etc.) rather than relying on the envelope to constrain it.
	entries := []struct {
		t    reflect.Type
		note string
	}{
		{reflect.TypeFor[types.Request](), "process / predict request"},
		{reflect.TypeFor[types.ComposedRequest](), "compose request"},
		{reflect.TypeFor[types.ChainRequest](), "process-chain request"},
		{reflect.TypeFor[types.FacetRequest](), "facet request"},
		{reflect.TypeFor[types.SampleRequest](), "sample request"},
		{reflect.TypeFor[types.Response](), "process / predict result"},
		{reflect.TypeFor[types.ComposedResponse](), "compose result"},
		{reflect.TypeFor[types.ChainResponse](), "process-chain result"},
		{reflect.TypeFor[types.FacetResult](), "facet result"},
		{reflect.TypeFor[Envelope](), "universal --json output envelope (data wraps the operation result)"},
	}

	var rootOneOf []any
	for _, e := range entries {
		name := b.register(e.t)
		rootOneOf = append(rootOneOf, map[string]any{"$ref": "#/$defs/" + name})
	}
	sort.Slice(rootOneOf, func(i, j int) bool {
		return rootOneOf[i].(map[string]any)["$ref"].(string) < rootOneOf[j].(map[string]any)["$ref"].(string)
	})

	root := map[string]any{
		"$schema":     payloadSchemaDialect,
		"$id":         payloadSchemaID,
		"title":       "Pulse payload contract",
		"description": "JSON Schema for every public Pulse payload. Validate a request against #/$defs/Request (or ComposedRequest / ChainRequest / FacetRequest / SampleRequest); all --json output is #/$defs/Envelope. format_version " + PayloadSchemaFormatVersion + ".",
		"oneOf":       rootOneOf,
		"$defs":       b.defs,
	}

	// MarshalIndent for a human-readable, diffable golden + published file.
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		// The input is composed entirely of marshalable maps/slices/strings;
		// a failure here is a programming error, surfaced loudly.
		panic("descriptor: BuildPayloadSchema marshal: " + err.Error())
	}
	return out
}

// rawMessageType and anyType are reused across the reflective walk.
var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	byteType       = reflect.TypeFor[byte]()
)

// enumValues maps an enum string type to its registry-backed value list.
// Only the high-cardinality, drift-prone families are listed; the small
// closed mode enums intentionally fall through to a plain string schema.
func enumValues() map[reflect.Type][]string {
	m := map[reflect.Type][]string{
		reflect.TypeFor[types.AggregationType](): stringify(types.AllAggregationTypes()),
		reflect.TypeFor[types.FiltererType]():    stringify(types.AllFiltererTypes()),
		reflect.TypeFor[types.GroupType]():       stringify(types.AllGroupTypes()),
		reflect.TypeFor[types.AttributeType]():   stringify(types.AllAttributeTypes()),
		reflect.TypeFor[types.WindowType]():      stringify(types.AllWindowTypes()),
		reflect.TypeFor[types.FeatureType]():     stringify(types.AllFeatureTypes()),
		reflect.TypeFor[types.TestType]():        stringify(types.AllTestTypes()),
		reflect.TypeFor[types.OverlayKind]():     stringify(types.AllOverlayKinds()),
		reflect.TypeFor[types.RegressionType](): stringify(types.AllRegressionTypes()),
	}
	return m
}

// stringify converts a slice of ~string enum constants to []string sorted
// alphabetically (stable against const-declaration reordering).
func stringify[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

type schemaBuilder struct {
	defs  map[string]any
	enums map[reflect.Type][]string
}

func newSchemaBuilder() *schemaBuilder {
	return &schemaBuilder{
		defs:  map[string]any{},
		enums: enumValues(),
	}
}

// register ensures a $def exists for the named type t and returns its def
// name. Reserves the name before recursing so cyclic graphs ref cleanly.
func (b *schemaBuilder) register(t reflect.Type) string {
	name := t.Name()
	if _, ok := b.defs[name]; ok {
		return name
	}
	b.defs[name] = true // placeholder breaks recursion cycles
	b.defs[name] = b.defFor(t)
	return name
}

// defFor builds the actual $def body for a named type (enum, strict
// union, or struct).
func (b *schemaBuilder) defFor(t reflect.Type) any {
	// Strict unions override their reflected struct shape.
	switch t {
	case reflect.TypeFor[types.OverlayRef]():
		return b.overlayRefDef(t)
	case reflect.TypeFor[types.OverlayPayload]():
		return b.overlayPayloadDef(t)
	}
	// Registry-backed enum.
	if vals, ok := b.enums[t]; ok {
		anyVals := make([]any, len(vals))
		for i, v := range vals {
			anyVals[i] = v
		}
		return map[string]any{"type": "string", "enum": anyVals}
	}
	// Struct.
	if t.Kind() == reflect.Struct {
		return b.structSchema(t)
	}
	// Named non-struct (e.g. AxisKey []any) — inline its underlying shape.
	return b.schemaFor(t, true)
}

// schemaFor returns an inline schema or a $ref for a field type. inlineNamed
// short-circuits the named-type→$ref rule for defFor's own underlying-type
// expansion.
func (b *schemaBuilder) schemaFor(t reflect.Type, inlineNamed bool) any {
	// Pointers: Go json omits nil pointers tagged omitempty and emits the
	// pointee shape otherwise — schema-wise a pointer is its element.
	if t.Kind() == reflect.Pointer {
		return b.schemaFor(t.Elem(), inlineNamed)
	}

	// json.RawMessage — operator-specific config; open object (v1 boundary).
	if t == rawMessageType {
		return map[string]any{
			"description": "Operator-specific configuration as raw JSON. The per-operator param schema lives alongside the operator's processor; see the manifest.",
		}
	}

	// Named types route to $defs (strict unions, enums, structs), unless
	// this is defFor expanding the named type's own underlying shape.
	if !inlineNamed && t.Name() != "" && t.PkgPath() != "" {
		if _, isEnum := b.enums[t]; isEnum || t.Kind() == reflect.Struct ||
			t == reflect.TypeFor[types.OverlayRef]() || t == reflect.TypeFor[types.OverlayPayload]() {
			return map[string]any{"$ref": "#/$defs/" + b.register(t)}
		}
	}

	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Interface:
		return true // any
	case reflect.Slice, reflect.Array:
		if t.Elem() == byteType {
			// []byte marshals as a base64 string.
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		// A nil slice without omitempty marshals as JSON null, so arrays
		// permit null; encoding/json never emits null for omitempty slices.
		return map[string]any{
			"type":  []any{"array", "null"},
			"items": b.schemaFor(t.Elem(), false),
		}
	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": b.schemaFor(t.Elem(), false),
		}
	case reflect.Struct:
		// Anonymous/inline struct (no name) — expand in place.
		return b.structSchema(t)
	default:
		return true
	}
}

// structSchema reflects a struct into an object schema, honouring json
// tags, omitempty/omitzero (→ optional), and "-" (→ skipped).
func (b *schemaBuilder) structSchema(t reflect.Type) any {
	props := map[string]any{}
	var required []string

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		name, opts, skip := jsonFieldName(f)
		if skip {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && name == f.Name {
			// Untagged embedded struct — flatten its properties.
			emb := b.structSchema(f.Type).(map[string]any)
			if ep, ok := emb["properties"].(map[string]any); ok {
				for k, v := range ep {
					props[k] = v
				}
			}
			if er, ok := emb["required"].([]string); ok {
				required = append(required, er...)
			}
			continue
		}
		props[name] = b.schemaFor(f.Type, false)
		if !opts["omitempty"] && !opts["omitzero"] {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema
}

// overlayRefDef builds the strict OverlayRef union: an object whose arms
// are the discriminated-union pointer fields, at most one populated. An
// empty Ref is valid (several kinds use implicit margins), so the rule is
// maxProperties:1 rather than oneOf-exactly-one.
func (b *schemaBuilder) overlayRefDef(t reflect.Type) any {
	props := map[string]any{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, skip := jsonFieldName(f)
		if skip {
			continue
		}
		props[name] = b.schemaFor(f.Type, false)
	}
	return map[string]any{
		"type":                 "object",
		"description":          "Discriminated union: at most one arm is populated per spec. An empty Ref is valid for implicit-margin kinds.",
		"properties":           props,
		"additionalProperties": false,
		"maxProperties":        1,
	}
}

// overlayPayloadDef builds the strict OverlayPayload union: shape-keyed
// scalar/series/matrix, with the populated arm required for its shape.
func (b *schemaBuilder) overlayPayloadDef(t reflect.Type) any {
	props := map[string]any{}
	armForShape := map[string]string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, skip := jsonFieldName(f)
		if skip {
			continue
		}
		props[name] = b.schemaFor(f.Type, false)
		switch name {
		case "scalar":
			armForShape["scalar"] = "scalar"
		case "series":
			armForShape["series"] = "series"
		case "matrix":
			armForShape["matrix"] = "matrix"
		}
	}
	// shape is a closed enum here (the three OverlayShape values).
	props["shape"] = map[string]any{"type": "string", "enum": []any{"matrix", "scalar", "series"}}

	var allOf []any
	for _, shape := range []string{"matrix", "scalar", "series"} {
		allOf = append(allOf, map[string]any{
			"if":   map[string]any{"properties": map[string]any{"shape": map[string]any{"const": shape}}},
			"then": map[string]any{"required": []string{shape}},
		})
	}

	return map[string]any{
		"type":                 "object",
		"description":          "Discriminated union keyed by shape; the matching arm (scalar/series/matrix) is required for the declared shape.",
		"properties":           props,
		"additionalProperties": false,
		"required":             []string{"shape"},
		"allOf":                allOf,
	}
}

// jsonFieldName replicates encoding/json's field-name resolution: the tag
// name (or Go field name when untagged), the option set, and whether the
// field is skipped ("-").
func jsonFieldName(f reflect.StructField) (name string, opts map[string]bool, skip bool) {
	tag := f.Tag.Get("json")
	opts = map[string]bool{}
	if tag == "-" {
		return "", opts, true
	}
	name = f.Name
	if tag != "" {
		parts := splitComma(tag)
		if parts[0] != "" {
			name = parts[0]
		}
		for _, o := range parts[1:] {
			opts[o] = true
		}
	}
	return name, opts, false
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
