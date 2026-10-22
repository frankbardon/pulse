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
//     OverlayPayload (shape-discriminated scalar/series/matrix, plus
//     non-nesting named parts).
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
		{reflect.TypeFor[descriptor.RecommendRequest](), "recommend request", featRecommend},
		{reflect.TypeFor[descriptor.ExplainRequest](), "explain request (a request root to describe, or a result root to read with its request companion)", featExplain},
		{reflect.TypeFor[types.Response](), "process / predict result", ""},
		{reflect.TypeFor[types.ComposedResponse](), "compose result", featCompose},
		{reflect.TypeFor[types.ChainResponse](), "process-chain result", featProcessChain},
		{reflect.TypeFor[types.FacetResult](), "facet result", featFacet},
		{reflect.TypeFor[types.LookupResult](), "point-lookup result", featLookup},
		{reflect.TypeFor[descriptor.RecommendResult](), "recommend result", featRecommend},
		{reflect.TypeFor[descriptor.ExplainResult](), "explain result", featExplain},
		{reflect.TypeFor[descriptor.Advisory](), "predict advisory (one entry of a predict result's advisories)", ""},
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
//   - the capability-gated slots inst hides (HiddenSlotKeys:
//     Request.crosstab / joins / overlays, the overlays slot of each
//     other request root and of each result root) are not properties;
//   - a root whose capability is hidden (compose, process_chain, facet,
//     sample, lookup, recommend, explain) is not an entry point;
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
		// present (point lookup, recommend and explain are not validated
		// request bodies there).
		if name != "Request" && strings.HasSuffix(name, "Request") && name != "LookupRequest" && name != "RecommendRequest" && name != "ExplainRequest" {
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
		reflect.TypeFor[types.MatrixType]():      stringify(types.AllMatrixTypes()),
	}
	for t, vals := range m {
		m[t] = filterNames(vals, inst.Enabled)
	}
	// An instance's extension operators are operator types it accepts:
	// the snapshot (already profile-filtered) adds each category's
	// registered names to that category's enum, so a request naming one
	// validates against the instance's schema like a built-in.
	// Extension synth distributions are not payload-reachable.
	if snap := inst.Extensions(); snap != nil {
		for _, c := range []struct {
			t   reflect.Type
			ops []descriptor.OperatorMeta
		}{
			{reflect.TypeFor[types.AggregationType](), snap.Aggregators},
			{reflect.TypeFor[types.AttributeType](), snap.Attributes},
			{reflect.TypeFor[types.FiltererType](), snap.Filterers},
			{reflect.TypeFor[types.GroupType](), snap.Groupers},
			{reflect.TypeFor[types.WindowType](), snap.Windows},
			{reflect.TypeFor[types.FeatureType](), snap.Features},
			{reflect.TypeFor[types.TestType](), snap.Tests},
			{reflect.TypeFor[types.MatrixType](), snap.MatrixOps},
			{reflect.TypeFor[types.RegressionType](), snap.Regressions},
		} {
			if len(c.ops) == 0 {
				continue
			}
			vals := m[c.t]
			for _, op := range c.ops {
				vals = append(vals, op.Name)
			}
			sort.Strings(vals)
			m[c.t] = vals
		}
	}
	// The multiplicity method / family sets are closed vocabularies, not
	// features: never filtered by the instance (a hidden
	// capability:multiplicity drops the slots that reach them, and the
	// defs with them).
	m[reflect.TypeFor[types.MultiplicityMethod]()] = stringify(types.AllMultiplicityMethods())
	m[reflect.TypeFor[types.MultiplicityFamily]()] = stringify(types.AllMultiplicityFamilies())
	// Likewise the vector coercions, kinds and standardizations and the matrix encodings / kinds (a
	// hidden capability:matrices drops the `vectors` and `matrices`
	// slots, and VectorSpec / MatrixSpec with them).
	m[reflect.TypeFor[types.VectorCoerce]()] = stringify(types.AllVectorCoerces())
	m[reflect.TypeFor[types.VectorKind]()] = stringify(types.AllVectorKinds())
	m[reflect.TypeFor[types.VectorStandardize]()] = stringify(types.AllVectorStandardizes())
	m[reflect.TypeFor[types.MatrixEncoding]()] = stringify(types.AllMatrixEncodings())
	m[reflect.TypeFor[types.MatrixKind]()] = stringify(types.AllMatrixKinds())
	// The response-shaping presets: a closed vocabulary on an ungated
	// slot.
	m[reflect.TypeFor[types.ReturnPreset]()] = stringify(types.AllReturnPresets())
	// The sweep modes and rank orders: closed vocabularies (a hidden
	// capability:compose_sweep drops the `sweep` slot, and SweepSpec /
	// SweepRank with it).
	m[reflect.TypeFor[types.SweepMode]()] = stringify(types.AllSweepModes())
	m[reflect.TypeFor[types.SweepRankOrder]()] = stringify(types.AllSweepRankOrders())
	// The regression covariance estimators: a closed vocabulary on the
	// ungated regressions slot.
	m[reflect.TypeFor[types.CovarianceType]()] = stringify(types.AllCovarianceTypes())
	// Explain's closed vocabularies: its detail levels, modes, roots and
	// the finding verdicts.
	m[reflect.TypeFor[descriptor.ExplainDetail]()] = stringify([]descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull})
	m[reflect.TypeFor[descriptor.ExplainMode]()] = stringify([]descriptor.ExplainMode{descriptor.ExplainModeRequest, descriptor.ExplainModeResponse})
	m[reflect.TypeFor[descriptor.ExplainRoot]()] = stringify(explainRoots())
	m[reflect.TypeFor[descriptor.Verdict]()] = stringify(descriptor.Verdicts())
	return m
}

// explainRoots is every ExplainRoot spelling: the five request roots,
// then the four result roots.
func explainRoots() []descriptor.ExplainRoot {
	return []descriptor.ExplainRoot{
		descriptor.ExplainRootRequest, descriptor.ExplainRootComposed, descriptor.ExplainRootChain,
		descriptor.ExplainRootFacet, descriptor.ExplainRootSample,
		descriptor.ExplainRootResponse, descriptor.ExplainRootComposedResponse,
		descriptor.ExplainRootChainResponse, descriptor.ExplainRootFacetResult,
	}
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
		reflect.TypeFor[types.ChainResponse](), reflect.TypeFor[types.FacetResult](), reflect.TypeFor[types.LookupResult](),
		reflect.TypeFor[descriptor.RecommendResult](), reflect.TypeFor[descriptor.ExplainResult]())
	// ExplainRequest is deliberately not a request root here: it carries
	// RESULTS to read (a Response, …), whose undefined figures arrive as
	// null, so the result structs it reaches keep their nullable floats.
	// The request roots it carries are reached from their own entries.
	requests := reach(reflect.TypeFor[types.Request](), reflect.TypeFor[types.ComposedRequest](),
		reflect.TypeFor[types.ChainRequest](), reflect.TypeFor[types.FacetRequest](),
		reflect.TypeFor[types.SampleRequest](), reflect.TypeFor[types.LookupRequest](),
		reflect.TypeFor[descriptor.RecommendRequest]())
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
	case reflect.TypeFor[types.CovarianceLags]():
		return covarianceLagsDef()
	case reflect.TypeFor[types.PriorPrecision]():
		return priorPrecisionDef()
	case reflect.TypeFor[types.FitResult]():
		return b.fitResultDef()
	case reflect.TypeFor[types.MatrixValues]():
		return b.matrixValuesDef(t)
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

// fieldKey names one struct field by its json name.
type fieldKey struct {
	t    reflect.Type
	name string
}

// fieldOverrides replaces the reflected schema of a json.RawMessage
// field that is not operator params (the generic RawMessage rule
// describes params). A recommend draft is a request object whose
// caller-supplied values are "<placeholder>" strings, so it is an open
// object rather than #/$defs/Request: a placeholder may sit where the
// request takes a number.
var fieldOverrides = map[fieldKey]any{
	// A sweep body is a Request with axis placeholders — `{"$var":
	// "axis"}` may stand where the request takes a number — so it is an
	// open object, not #/$defs/Request; likewise its overlays.
	{reflect.TypeFor[types.SweepSpec](), "request"}: map[string]any{
		"type":        "object",
		"description": "The slot request body as wire JSON (the #/$defs/Request shape) with axis placeholders: `{{axis}}` inside a string, `{\"$var\": \"axis\"}` as a whole value. Each expanded body is decoded strictly into a Request.",
	},
	{reflect.TypeFor[types.SweepSpec](), "overlays"}: map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "object"},
		"description": "Compose overlay specs (the #/$defs/ComposeOverlaySpec shape) with axis placeholders, substituted per expanded slot and appended to the request's own overlays.",
	},
	{reflect.TypeFor[types.SweepAxis](), "values"}: map[string]any{
		"type":        "array",
		"minItems":    1,
		"items":       map[string]any{"type": []any{"number", "string", "boolean"}},
		"description": "The scalar values the axis takes, in expansion order.",
	},
	{reflect.TypeFor[descriptor.Recommendation](), "request"}: map[string]any{
		"type":        "object",
		"description": "The draft request as wire JSON (the #/$defs/Request shape). Every value the caller must supply is a \"<placeholder>\" string named after its wire key, listed in placeholders.",
	},
	{reflect.TypeFor[descriptor.RecommendStep](), "request"}: map[string]any{
		"type":        "object",
		"description": "The step tool's input as wire JSON: a {request, out} pair (request in the #/$defs/Request shape) for a step that writes a cohort, else a #/$defs/Request. Every value the caller must supply is a \"<placeholder>\" string, listed in placeholders.",
	},
}

// optionalFields are wire keys whose Go field carries no omitempty (the
// marshalled form always writes them) but that a caller may omit: an
// aggregation or grouper reads vector / fields instead of field on its
// vector operators (AGG_VEC_*, GROUP_KMEANS), and an attribute may read
// none (ATTR_FORMULA's expression, ATTR_REG_PREDICT's params.fit, a
// vector attribute's vector / fields). Requiring them made a strict
// validator refuse requests the server runs.
var optionalFields = map[fieldKey]bool{
	{reflect.TypeFor[types.Aggregation](), "field"}: true,
	{reflect.TypeFor[types.Group](), "field"}:       true,
	{reflect.TypeFor[types.Attribute](), "field"}:   true,
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
		if o, ok := fieldOverrides[fieldKey{t, name}]; ok {
			props[name] = o
		} else {
			props[name] = b.schemaFor(f.Type, false)
		}
		if !opts["omitempty"] && !opts["omitzero"] && !optionalFields[fieldKey{t, name}] {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if hasSlotError(t) {
		// A failed slot (a result carrying `error`, types.SlotError)
		// writes its identity and the error only: every other
		// always-written key is required only when `error` is absent.
		var always, figures []string
		for _, name := range required {
			if slotIdentityKeys[name] {
				always = append(always, name)
			} else {
				figures = append(figures, name)
			}
		}
		required = always
		if len(figures) > 0 {
			sort.Strings(figures)
			schema["if"] = map[string]any{"not": map[string]any{"required": []string{slotErrorKey}}}
			schema["then"] = map[string]any{"required": figures}
		}
	}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema
}

// slotErrorKey is the json key of a result's per-slot failure.
const slotErrorKey = "error"

// slotIdentityKeys are the always-written keys a failed slot still
// writes beside its `error` (types.SlotError): its name and type — a
// failed vector fit (types.VectorFitResult) its operator.
var slotIdentityKeys = map[string]bool{"name": true, "type": true, "operator": true}

// hasSlotError reports whether struct t carries a per-slot failure
// field — `error` of type *types.SlotError (a regression, test or
// matrix result, or a vector fit's summary).
func hasSlotError(t reflect.Type) bool {
	f, ok := t.FieldByName("Error")
	if !ok || f.Type != reflect.TypeFor[*types.SlotError]() {
		return false
	}
	name, _, skip := jsonFieldName(f)
	return !skip && name == slotErrorKey
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
// `parts` maps a part name to the same union WITHOUT `parts` — parts
// never nest, so the schema rejects a part carrying parts.
func (b *schemaBuilder) overlayPayloadDef(t reflect.Type) any {
	prevFloatNull := b.floatNull
	b.floatNull = b.resultOnly[t]
	defer func() { b.floatNull = prevFloatNull }()
	return b.overlayPayloadUnion(t, true)
}

// overlayPayloadUnion is overlayPayloadDef's body; withParts false
// drops the `parts` key (the per-part schema).
func (b *schemaBuilder) overlayPayloadUnion(t reflect.Type, withParts bool) any {
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
		if name == "parts" {
			if withParts {
				props[name] = map[string]any{
					"type":                 "object",
					"description":          "Named secondary payloads of a multi-output kind, beside the primary. Each part is a scalar | series | matrix payload; parts never nest.",
					"additionalProperties": b.overlayPayloadUnion(t, false),
				}
			}
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

// fitResultDef builds the ComposedResponse.fits entry union
// (types.FitResult, which writes its own wire form): a regression fit's
// RegressionResult object as is, or a vector fit's {"vector":
// VectorFitResult} — the `vector` key discriminates.
func (b *schemaBuilder) fitResultDef() any {
	return map[string]any{
		"description": "One fit's result: a regression fit's RegressionResult, or a vector fit's {vector: VectorFitResult}. The vector key discriminates; a regression entry never carries it.",
		"oneOf": []any{
			map[string]any{"$ref": "#/$defs/" + b.register(reflect.TypeFor[types.RegressionResult]())},
			map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"vector": map[string]any{"$ref": "#/$defs/" + b.register(reflect.TypeFor[types.VectorFitResult]())}},
				"required":             []string{"vector"},
				"additionalProperties": false,
			},
		},
	}
}

// matrixValuesDef is types.MatrixValues, which writes its own wire
// form: the reflected layout (row_keys / column_keys / values) under
// encoding full or upper, or under records the columns and one object
// per row — its key, a number or null per column, and the row's
// record fields (MAT_SEM's estimates: lval, op, rval, parameter,
// free).
func (b *schemaBuilder) matrixValuesDef(t reflect.Type) any {
	str := map[string]any{"type": "string"}
	records := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":     map[string]any{"$ref": "#/$defs/" + b.register(reflect.TypeFor[types.MatrixKind]())},
			"encoding": map[string]any{"const": string(types.MatrixEncodingRecords)},
			"columns":  map[string]any{"type": "array", "items": str},
			"rows": map[string]any{"type": "array", "items": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{types.MatrixRecordKey: str},
				"required":             []string{types.MatrixRecordKey},
				"additionalProperties": map[string]any{"type": []any{"number", "string", "boolean", "null"}},
			}},
			"labels": map[string]any{"type": "array", "items": str},
		},
		"required":             []string{"columns", "encoding", "kind", "rows"},
		"additionalProperties": false,
	}
	return map[string]any{
		"description": "One matrix: row_keys / column_keys / values under encoding full or upper; under records, columns and rows ({key, <column>: value}, in row order).",
		"oneOf":       []any{b.structSchema(t), records},
	}
}

// covarianceLagsDef is RegressionCovariance.lags. types.CovarianceLags
// has no exported fields (its states are behind constructors), so
// reflection would describe an empty object; on the wire it is a
// non-negative integer or "auto" (absent reads as "auto").
func covarianceLagsDef() any {
	return map[string]any{
		"description": "Newey-West truncation lag (covariance type hac): a non-negative integer, or \"auto\" (also the meaning when absent) for floor(4*(n/100)^(2/9)) over the fit's n rows.",
		"oneOf": []any{
			map[string]any{"type": "integer", "minimum": 0},
			map[string]any{"const": "auto"},
		},
	}
}

// priorPrecisionDef is RegressionSpec.prior_precision.
// types.PriorPrecision has no exported fields (its states are behind
// constructors), so reflection would describe an empty object; on the
// wire it is a non-negative number or an array of them.
func priorPrecisionDef() any {
	return map[string]any{
		"description": "Prior precision of the coefficients (the Bayesian linear fit): a number eps (Lambda0 = eps*I; absent or 0 = the default 1e-3), or one entry per coefficient, intercept first then the predictors in order (Lambda0 = diag; 0 = a flat prior on that coefficient).",
		"oneOf": []any{
			map[string]any{"type": "number", "minimum": 0},
			map[string]any{"type": "array", "items": map[string]any{"type": "number", "minimum": 0}},
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
