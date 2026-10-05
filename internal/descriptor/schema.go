package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
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
//     internal/service/ and internal/processing/ imports — only types + this package). A
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
//
// It is the profile-free view: BuildPayloadSchemaForInstance(nil).
func BuildPayloadSchema() json.RawMessage {
	return BuildPayloadSchemaForInstance(nil)
}

// payloadEntry is one root entry point of the payload schema. feature,
// when set, is the capability that offers the entry: an instance hiding
// it omits the entry (and every def reachable only through it).
type payloadEntry struct {
	t       reflect.Type
	note    string
	feature string
}

// payloadEntries lists the schema's root entry points. Request,
// Response and Envelope are ungated: every instance describes them.
func payloadEntries() []payloadEntry {
	return []payloadEntry{
		{reflect.TypeFor[types.Request](), "process / predict request", ""},
		{reflect.TypeFor[types.ComposedRequest](), "compose request", featCompose},
		{reflect.TypeFor[types.ChainRequest](), "process-chain request", featProcessChain},
		{reflect.TypeFor[types.FacetRequest](), "facet request", featFacet},
		{reflect.TypeFor[types.SampleRequest](), "sample request", featSample},
		{reflect.TypeFor[types.LookupRequest](), "point-lookup request", featLookup},
		{reflect.TypeFor[types.Response](), "process / predict result", ""},
		{reflect.TypeFor[types.ComposedResponse](), "compose result", featCompose},
		{reflect.TypeFor[types.ChainResponse](), "process-chain result", featProcessChain},
		{reflect.TypeFor[types.FacetResult](), "facet result", featFacet},
		{reflect.TypeFor[types.LookupResult](), "point-lookup result", featLookup},
		{reflect.TypeFor[descriptor.Envelope](), "universal --json output envelope (data wraps the operation result)", ""},
	}
}

// payloadSchemaDigestPrefix leads the root $comment; the instance's
// feature_set_digest follows it.
const payloadSchemaDigestPrefix = "feature_set_digest: "

// BuildPayloadSchemaForInstance returns the payload schema of ONE
// instance — only what inst offers:
//
//   - the registry-backed enums (operator, overlay-kind and regression
//     families) list only enabled names;
//   - the capability-gated request slots inst hides (HiddenSlotKeys:
//     Request.crosstab / joins / overlays and the overlays slot of each
//     other request root) are not properties;
//   - a root whose capability is hidden (compose, process_chain, facet,
//     sample, lookup) is not an entry point;
//
// and every def reachable only through something omitted is absent,
// because defs are registered by walking from the surviving entries.
// Request, Response and Envelope are always present. $id is unchanged;
// the root $comment carries the instance's feature_set_digest (the same
// value its manifest carries). A nil / unscoped instance is the full
// registry view BuildPayloadSchema serves.
func BuildPayloadSchemaForInstance(inst *InstanceSnapshot) json.RawMessage {
	out, err := PayloadSchemaForInstance(inst)
	if err != nil {
		// The input is composed entirely of marshalable maps/slices/strings;
		// a failure here is a programming error, surfaced loudly.
		panic("descriptor: BuildPayloadSchema marshal: " + err.Error())
	}
	return out
}

// PayloadSchemaForInstance is BuildPayloadSchemaForInstance returning
// the (programming-error-only) marshal failure instead of panicking —
// the form the Pulse.PayloadSchema facade serves.
func PayloadSchemaForInstance(inst *InstanceSnapshot) (json.RawMessage, error) {
	b := newSchemaBuilder(inst)

	// Entry points. Order matters only for the root oneOf, which we sort.
	// Both request and result shapes are first-class entry points. The
	// Envelope's data slot is genuinely polymorphic (any operation —
	// process, manifest, predict, inspect, …), so it stays open; consumers
	// validate the unwrapped result against its own def (#/$defs/Response,
	// etc.) rather than relying on the envelope to constrain it.
	var rootOneOf []any
	var altRequests []string
	for _, e := range payloadEntries() {
		if e.feature != "" && !inst.Enabled(e.feature) {
			continue
		}
		name := b.register(e.t)
		rootOneOf = append(rootOneOf, map[string]any{"$ref": "#/$defs/" + name})
		// The root description names the alternative request roots
		// present (point lookup is not a validated request body there).
		if name != "Request" && strings.HasSuffix(name, "Request") && name != "LookupRequest" {
			altRequests = append(altRequests, name)
		}
	}
	sort.Slice(rootOneOf, func(i, j int) bool {
		return rootOneOf[i].(map[string]any)["$ref"].(string) < rootOneOf[j].(map[string]any)["$ref"].(string)
	})

	validate := "#/$defs/Request"
	if len(altRequests) > 0 {
		validate += " (or " + strings.Join(altRequests, " / ") + ")"
	}
	root := map[string]any{
		"$schema":     payloadSchemaDialect,
		"$id":         payloadSchemaID,
		"$comment":    payloadSchemaDigestPrefix + manifestDigest(inst),
		"title":       "Pulse payload contract",
		"description": "JSON Schema for every public Pulse payload. Validate a request against " + validate + "; all --json output is #/$defs/Envelope. format_version " + PayloadSchemaFormatVersion + ".",
		"oneOf":       rootOneOf,
		"$defs":       b.defs,
	}

	// MarshalIndent for a human-readable, diffable golden + published file.
	return json.MarshalIndent(root, "", "  ")
}

// rawMessageType and anyType are reused across the reflective walk.
var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	byteType       = reflect.TypeFor[byte]()
)

// enumValues maps an enum string type to its registry-backed value list.
// Only the high-cardinality, drift-prone families are listed; the small
// closed mode enums intentionally fall through to a plain string schema.
//
// On a scoped instance each list keeps only the names inst enables; a
// family left with nothing enabled is an empty enum (no value is valid).
func enumValues(inst *InstanceSnapshot) map[reflect.Type][]string {
	m := map[reflect.Type][]string{
		reflect.TypeFor[types.AggregationType](): stringify(types.AllAggregationTypes()),
		reflect.TypeFor[types.FiltererType]():    stringify(types.AllFiltererTypes()),
		reflect.TypeFor[types.GroupType]():       stringify(types.AllGroupTypes()),
		reflect.TypeFor[types.AttributeType]():   stringify(types.AllAttributeTypes()),
		reflect.TypeFor[types.WindowType]():      stringify(types.AllWindowTypes()),
		reflect.TypeFor[types.FeatureType]():     stringify(types.AllFeatureTypes()),
		reflect.TypeFor[types.TestType]():        stringify(types.AllTestTypes()),
		reflect.TypeFor[types.OverlayKind]():     stringify(types.AllOverlayKinds()),
		reflect.TypeFor[types.RegressionType]():  stringify(types.AllRegressionTypes()),
	}
	for t, vals := range m {
		m[t] = filterNames(vals, inst.Enabled)
	}
	// The multiplicity method / family sets are closed vocabularies, not
	// features: never filtered by the instance (a hidden
	// capability:multiplicity drops the slots that reach them, and the
	// defs with them).
	m[reflect.TypeFor[types.MultiplicityMethod]()] = stringify(types.AllMultiplicityMethods())
	m[reflect.TypeFor[types.MultiplicityFamily]()] = stringify(types.AllMultiplicityFamilies())
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
	inst  *InstanceSnapshot

	// resultOnly holds the struct types reachable from a result root but
	// from no request root. A float slot in one of them is "number or
	// null": the wire writes an undefined figure (NaN / ±Inf in Go) as
	// null (types.MarshalFinite). Request floats stay "number" — a
	// decoded request never carries a non-finite float.
	resultOnly map[reflect.Type]bool
	// floatNull is set while a resultOnly struct's fields are described.
	floatNull bool
}

func newSchemaBuilder(inst *InstanceSnapshot) *schemaBuilder {
	return &schemaBuilder{
		defs:       map[string]any{},
		enums:      enumValues(inst),
		inst:       inst,
		resultOnly: resultOnlyStructs(),
	}
}

// resultOnlyStructs walks the struct graph from the result roots and
// from the request roots and returns the structs only the former reach
// (ChainResponse echoes a ChainRequest, so request-side structs reached
// through a result are excluded).
func resultOnlyStructs() map[reflect.Type]bool {
	reach := func(roots ...reflect.Type) map[reflect.Type]bool {
		seen := map[reflect.Type]bool{}
		var walk func(t reflect.Type)
		walk = func(t reflect.Type) {
			for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
				t = t.Elem()
			}
			if t.Kind() != reflect.Struct || seen[t] {
				return
			}
			seen[t] = true
			for i := 0; i < t.NumField(); i++ {
				if f := t.Field(i); f.PkgPath == "" {
					if _, _, skip := jsonFieldName(f); !skip {
						walk(f.Type)
					}
				}
			}
		}
		for _, r := range roots {
			walk(r)
		}
		return seen
	}
	results := reach(reflect.TypeFor[types.Response](), reflect.TypeFor[types.ComposedResponse](),
		reflect.TypeFor[types.ChainResponse](), reflect.TypeFor[types.FacetResult](), reflect.TypeFor[types.LookupResult]())
	requests := reach(reflect.TypeFor[types.Request](), reflect.TypeFor[types.ComposedRequest](),
		reflect.TypeFor[types.ChainRequest](), reflect.TypeFor[types.FacetRequest](),
		reflect.TypeFor[types.SampleRequest](), reflect.TypeFor[types.LookupRequest]())
	for t := range requests {
		delete(results, t)
	}
	return results
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
	case reflect.TypeFor[types.SlotWeight]():
		return b.slotWeightDef()
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
		if b.floatNull {
			return map[string]any{"type": []any{"number", "null"}}
		}
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
//
// A capability-gated request slot the instance hides is skipped like a
// "-" field, so neither it nor any def reachable only through it appears.
func (b *schemaBuilder) structSchema(t reflect.Type) any {
	prevFloatNull := b.floatNull
	b.floatNull = b.resultOnly[t]
	defer func() { b.floatNull = prevFloatNull }()
	props := map[string]any{}
	var required []string
	hidden := HiddenSlotKeys(reflect.New(t).Interface(), b.inst)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		name, opts, skip := jsonFieldName(f)
		if skip || slices.Contains(hidden, name) {
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
	prevFloatNull := b.floatNull
	b.floatNull = b.resultOnly[t]
	defer func() { b.floatNull = prevFloatNull }()
	props := map[string]any{}
	armForShape := map[string]string{}
	// The adjusted twins (p_adjusted / significant_adjusted) ride a
	// capability; an instance hiding it drops them like any gated slot.
	hidden := HiddenSlotKeys(reflect.New(t).Interface(), b.inst)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, skip := jsonFieldName(f)
		if skip || slices.Contains(hidden, name) {
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

// slotWeightDef builds the per-slot weight union. types.SlotWeight has
// no exported fields (its three states are behind constructors), so
// reflection would describe an empty object; on the wire it is a
// field-name string, a WeightSpec object, or null — and null is NOT
// absence: an absent key inherits the request / instance weight, null
// opts the slot out.
func (b *schemaBuilder) slotWeightDef() any {
	return map[string]any{
		"description": "Per-slot row weight. Absent: inherit the request weight, then the instance default. null: opt this slot out (run unweighted). A string names the weight field (kind probability); an object is a full WeightSpec.",
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"$ref": "#/$defs/" + b.register(reflect.TypeFor[types.WeightSpec]())},
			map[string]any{"type": "null"},
		},
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
