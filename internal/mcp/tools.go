package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// Config carries the runtime configuration baked into the tool catalog at
// construction time. It exists so the catalog has NO dependency on process
// globals or package-level build state — everything a tool closure needs is
// threaded in explicitly.
//
// Version is the server/build identity string. No built-in tool surfaces it
// today, but the adapter (mcp/gosdk) reads it to populate the
// MCP server's Implementation.Version rather than a package-level constant.
// Reserved here so the single Config value is the one place runtime identity
// is injected.
type Config struct {
	Version string

	// DefaultReturn is the `return` preset an MCP request without its
	// own block is shaped by (return_default.go). Empty defers to the
	// instance default (pulse.Options.DefaultReturn, else the feature
	// profile's `return`), else the built-in `standard`. `full` restores
	// the library's unshaped output.
	DefaultReturn types.ReturnPreset
}

// InvokeFunc is the type-erased entry point for one tool: it strict-decodes
// raw arguments into the tool's typed In struct, calls the typed handler, and
// returns the typed Out as any. Coded errors from the facade (or the
// strict-decode layer) are returned verbatim — the adapter renders a
// *errors.CodedError as the structured {code, message, details} envelope.
type InvokeFunc func(ctx context.Context, p *pulse.Pulse, raw json.RawMessage) (any, error)

// ToolDescriptor is the SDK-free, type-erased descriptor for one registered
// MCP tool. It pairs the reflected input/output JSON Schemas (json.RawMessage,
// so no consumer needs the schema reflector) with an Invoke closure that
// round-trips raw JSON arguments through the typed handler. The go-sdk adapter
// mounts these onto a server via the low-level Server.AddTool path.
type ToolDescriptor struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Invoke       InvokeFunc
}

// decodeFunc unmarshals raw arguments into the typed In, applying any
// tool-specific strict validation (unknown-field rejection) before decode.
// inst is the calling instance's snapshot (nil: unscoped), so a request
// slot the instance hides is refused as an unknown key.
type decodeFunc[In any] func(raw json.RawMessage, inst *descx.InstanceSnapshot) (In, error)

// strictRequestDecode decodes a types.Request strictly (strictDecode):
// an unknown key at any depth is PULSE_REQUEST_UNKNOWN_FIELD.
func strictRequestDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (types.Request, error) {
	return strictDecode[types.Request](raw, inst)
}

// strictImportDecode decodes pulse_import's input, holding each `groups`
// entry to its exact {key, members} shape, under its own code; the rest
// of the input then decodes strictly (strictDecode). The group check is
// targeted and runs first because a
// misspelled key inside a group is not a harmless drop: `{"keys": [...],
// "members": [...]}` would decode as a KEYLESS tuple group, which skips
// the member-constancy check and writes a differently-encoded cohort
// without a word. An unknown key there is PULSE_GROUP_DECLARATION_INVALID.
//
// Each `schema` entry is held to the ImportSchemaField shape for the same
// reason: a misspelled `nullable` would otherwise import the field
// non-nullable without a word. An unknown key there (or a `schema` that
// is not an array of objects) is PULSE_IMPORT_SCHEMA_INVALID. Any other
// unknown key is PULSE_REQUEST_UNKNOWN_FIELD.
func strictImportDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (ImportIn, error) {
	if err := checkGroupsShape(raw, "pulse_import"); err != nil {
		return ImportIn{}, err
	}
	if err := checkSchemaShape(raw); err != nil {
		return ImportIn{}, err
	}
	return strictDecode[ImportIn](raw, inst)
}

// checkSchemaShape rejects a pulse_import `schema` that is not an array
// of objects, or whose entry carries a key outside ImportSchemaField, with
// PULSE_IMPORT_SCHEMA_INVALID (details: reason, index, error).
func checkSchemaShape(raw json.RawMessage) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	body, ok := top["schema"]
	if !ok || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SCHEMA_INVALID,
			"pulse_import schema: must be an array of {name, type, nullable, description} objects: "+err.Error(),
			map[string]any{"slot": "schema", "reason": "malformed", "error": err.Error()})
	}
	for i, entry := range entries {
		dec := json.NewDecoder(bytes.NewReader(entry))
		dec.DisallowUnknownFields()
		var f ImportSchemaField
		if err := dec.Decode(&f); err != nil {
			reason := "malformed"
			if strings.HasPrefix(err.Error(), "json: unknown field") {
				reason = "unknown_key"
			}
			return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SCHEMA_INVALID,
				"pulse_import schema["+strconv.Itoa(i)+`]: each entry is {"name", "type", "nullable", "description", "precision", "scale", "dim", "labels", "kind", "scale_min", "scale_max"} with no other keys: `+err.Error(),
				map[string]any{"slot": "schema", "reason": reason, "index": i, "error": err.Error()})
		}
	}
	return nil
}

// strictDedupDecode is strictImportDecode's twin for pulse_dedup: the
// same `groups` slot, held to the same exact {key, members} shape for
// the same reason.
func strictDedupDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (DedupIn, error) {
	if err := checkGroupsShape(raw, "pulse_dedup"); err != nil {
		return DedupIn{}, err
	}
	return strictDecode[DedupIn](raw, inst)
}

// checkGroupsShape rejects an unknown key inside any `groups` entry of
// raw with PULSE_GROUP_DECLARATION_INVALID.
func checkGroupsShape(raw json.RawMessage, tool string) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	groups, ok := top["groups"]
	if !ok {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(groups))
	dec.DisallowUnknownFields()
	var strict []pio.GroupDecl
	if err := dec.Decode(&strict); err != nil {
		return perr.NewCodedErrorWithDetails(perr.PULSE_GROUP_DECLARATION_INVALID,
			tool+` groups: each entry is {"key": [field, ...], "members": [field, ...]} with no other keys: `+err.Error(),
			map[string]any{"slot": "groups"})
	}
	return nil
}

// materializeKeys are pulse_materialize's top-level argument keys.
var materializeKeys = []string{"out", "overwrite", "request"}

// strictMaterializeDecode decodes pulse_materialize's input. An unknown
// top-level key is PULSE_REQUEST_UNKNOWN_FIELD over the tool's own keys
// — a request spread at the root (`windows` beside `out`) is refused
// with a pointer at `request`, never decoded as an empty request — and
// the nested `request` gets pulse_process's strict check.
func strictMaterializeDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (MaterializeIn, error) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) == nil {
		var unknown []string
		for k := range top {
			if !slices.Contains(materializeKeys, k) {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			return MaterializeIn{}, unknownMaterializeKeys(unknown)
		}
		if body, ok := top["request"]; ok {
			// The nested request decodes strictly on its own, so its
			// details.path is request-relative as on pulse_process.
			if ce := checkUnknownRequestKeys(body, inst); ce != nil {
				return MaterializeIn{}, ce
			}
		}
	}
	return strictDecode[MaterializeIn](raw, inst)
}

// unknownMaterializeKeys is pulse_materialize's unknown-argument
// refusal: PULSE_REQUEST_UNKNOWN_FIELD with the request-slot refusal's
// details (unknown_keys, suggestions, valid_keys) over the tool's own
// keys, its message saying where a request slot belongs.
func unknownMaterializeKeys(unknown []string) *perr.CodedError {
	sort.Strings(unknown)
	suggestions := make(map[string]any, len(unknown))
	for _, k := range unknown {
		if near := descx.NearestKey(k, materializeKeys); near != "" {
			suggestions[k] = near
		}
	}
	return perr.NewCodedErrorWithDetails(perr.PULSE_REQUEST_UNKNOWN_FIELD,
		"pulse_materialize arguments contain unrecognized key(s): "+strings.Join(unknown, ", ")+
			". The arguments are request, out and overwrite; every request slot (cohort, features, windows, ...) goes INSIDE request.",
		map[string]any{
			"unknown_keys": unknown,
			"suggestions":  suggestions,
			"valid_keys":   append([]string(nil), materializeKeys...),
		})
}

// strictComposedDecode decodes a ComposedRequest strictly: the root's
// own keys, then every slot request (details.request) and fit request
// (details.fit) at any depth.
func strictComposedDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (types.ComposedRequest, error) {
	return strictDecode[types.ComposedRequest](raw, inst)
}

// strictChainDecode decodes a ChainRequest strictly: the root's own
// keys, then every stage's request (details.stage) at any depth.
func strictChainDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (types.ChainRequest, error) {
	return strictDecode[types.ChainRequest](raw, inst)
}

// Alternative pulse_predict roots, each with the capability that owns
// the request shape it carries. A hidden capability's key is not an
// alternative: it falls through to the bare-request check and is
// refused as an unknown key, exactly as on a build without it.
var predictRoots = []struct{ key, feature string }{
	{"composed", descx.FeatureName(descx.FeatureKindCapability, "compose")},
	{"facet", descx.FeatureName(descx.FeatureKindCapability, "facet")},
	{"chain", descx.FeatureName(descx.FeatureKindCapability, "process_chain")},
}

// strictPredictDecode decodes pulse_predict's input: a bare request
// (strictRequestDecode, unchanged) unless the body carries an
// alternative root key. An alternative must stand ALONE at the root —
// beside any other key, or another alternative, the call is
// SERVICE_VALIDATION — and its body is decoded with its own tool's
// strict decode.
func strictPredictDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (PredictIn, error) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		req, err := strictRequestDecode(raw, inst)
		return PredictIn{Request: req}, err
	}
	var roots []string
	for _, r := range predictRoots {
		if _, ok := top[r.key]; ok && !inst.Hidden(r.feature) {
			roots = append(roots, r.key)
		}
	}
	if len(roots) == 0 {
		req, err := strictRequestDecode(raw, inst)
		return PredictIn{Request: req}, err
	}
	if len(top) > 1 {
		keys := make([]string, 0, len(top))
		for k := range top {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return PredictIn{}, perr.NewCodedErrorWithDetails(perr.SERVICE_VALIDATION,
			"pulse_predict: send a bare request, or exactly one of composed / facet / chain alone at the root; got "+strings.Join(keys, ", "),
			map[string]any{"keys": keys, "roots": roots})
	}
	body := top[roots[0]]
	var in PredictIn
	switch roots[0] {
	case "composed":
		c, err := strictComposedDecode(body, inst)
		if err != nil {
			return PredictIn{}, err
		}
		in.Composed = &c
	case "facet":
		f, err := strictDecode[types.FacetRequest](body, inst)
		if err != nil {
			return PredictIn{}, err
		}
		in.Facet = &f
	case "chain":
		c, err := strictChainDecode(body, inst)
		if err != nil {
			return PredictIn{}, err
		}
		in.Chain = &c
	}
	return in, nil
}

// fillPredictReturn applies the MCP `return` default to whichever root
// pulse_predict decoded, as its execution tool would (a facet request
// carries no `return`).
func fillPredictReturn(in *PredictIn, def *types.Return) {
	switch {
	case in.Composed != nil:
		fillComposeReturn(in.Composed, def)
	case in.Chain != nil:
		fillChainReturn(in.Chain, def)
	case in.Facet == nil:
		fillReturn(&in.Request, def)
	}
}

// makeInvoke composes a decode function with a typed handler into the
// type-erased InvokeFunc. Errors (decode or handler) are returned verbatim.
func makeInvoke[In, Out any](decode decodeFunc[In], h func(context.Context, *pulse.Pulse, In) (Out, error)) InvokeFunc {
	return func(ctx context.Context, p *pulse.Pulse, raw json.RawMessage) (any, error) {
		in, err := decode(raw, instanceOf(p))
		if err != nil {
			return nil, err
		}
		out, err := h(ctx, p, in)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

// instanceOf returns p's instance snapshot via the root facade's bridge
// hook, or nil (unscoped) when p is nil or the hook is not installed.
func instanceOf(p *pulse.Pulse) *descx.InstanceSnapshot {
	if p == nil || facadebridge.InstanceSnapshot == nil {
		return nil
	}
	return facadebridge.InstanceSnapshot(p)
}

// invokers maps each tool name to its type-erased Invoke. cfg is threaded in
// so tool closures capture runtime configuration: the request-carrying
// tools apply cfg's MCP `return` default at decode.
func invokers(cfg Config) map[string]InvokeFunc {
	return map[string]InvokeFunc{
		toolmeta.ToolInspect:        makeInvoke(strictDecode[InspectIn], HandleInspect),
		toolmeta.ToolPredict:        makeInvoke(withReturnDefault(cfg, strictPredictDecode, fillPredictReturn), HandlePredict),
		toolmeta.ToolProcess:        makeInvoke(withReturnDefault(cfg, strictRequestDecode, fillRequestReturn), HandleProcess),
		toolmeta.ToolProcessChain:   makeInvoke(withReturnDefault(cfg, strictChainDecode, fillChainReturn), HandleProcessChain),
		toolmeta.ToolCompose:        makeInvoke(withReturnDefault(cfg, strictComposedDecode, fillComposeReturn), HandleCompose),
		toolmeta.ToolSample:         makeInvoke(strictDecode[SampleIn], HandleSample),
		toolmeta.ToolFacet:          makeInvoke(strictDecode[FacetIn], HandleFacet),
		toolmeta.ToolFacetSchema:    makeInvoke(strictDecode[FacetSchemaIn], HandleFacetSchema),
		toolmeta.ToolLookup:         makeInvoke(strictDecode[LookupIn], HandleLookup),
		toolmeta.ToolSkillsList:     makeInvoke(strictDecode[SkillsListIn], HandleSkillsList),
		toolmeta.ToolSkillsGet:      makeInvoke(strictDecode[SkillsGetIn], HandleSkillsGet),
		toolmeta.ToolManifest:       makeInvoke(strictDecode[ManifestIn], HandleManifest),
		toolmeta.ToolExamplesSearch: makeInvoke(strictDecode[ExamplesSearchIn], HandleExamplesSearch),
		toolmeta.ToolExamplesGet:    makeInvoke(strictDecode[ExamplesGetIn], HandleExamplesGet),
		toolmeta.ToolErrorsLookup:   makeInvoke(strictDecode[ErrorsLookupIn], HandleErrorsLookup),
		toolmeta.ToolImport:         makeInvoke(strictImportDecode, HandleImport),
		toolmeta.ToolDedup:          makeInvoke(strictDedupDecode, HandleDedup),
		toolmeta.ToolMaterialize:    makeInvoke(strictMaterializeDecode, HandleMaterialize),
		toolmeta.ToolRake:           makeInvoke(strictDecode[RakeIn], HandleRake),
		toolmeta.ToolDrop:           makeInvoke(strictDecode[DropIn], HandleDrop),
		toolmeta.ToolImportsList:    makeInvoke(strictDecode[ImportsListIn], HandleImportsList),
		toolmeta.ToolLabelTables:    makeInvoke(strictDecode[LabelTablesIn], HandleLabelTables),
		toolmeta.ToolLabelResolve:   makeInvoke(strictDecode[LabelResolveIn], HandleLabelResolve),
		toolmeta.ToolRangeTables:    makeInvoke(strictDecode[RangeTablesIn], HandleRangeTables),
		toolmeta.ToolRecommend:      makeInvoke(strictDecode[RecommendIn], HandleRecommend),
		toolmeta.ToolExplain:        makeInvoke(strictDecode[ExplainIn], HandleExplain),
	}
}

// Tools returns the full type-erased tool catalog in stable order (matching
// toolmeta.Names() and Schemas()). Each descriptor carries the reflected
// input/output schema (from the init-time registry) and a config-baked Invoke.
func Tools(cfg Config) []ToolDescriptor {
	inv := invokers(cfg)
	names := toolmeta.Names()
	out := make([]ToolDescriptor, 0, len(names))
	for _, name := range names {
		ts, ok := SchemaFor(name)
		if !ok {
			continue
		}
		out = append(out, ToolDescriptor{
			Name:         name,
			Description:  ts.Description,
			InputSchema:  ts.InputSchema,
			OutputSchema: ts.OutputSchema,
			Invoke:       inv[name],
		})
	}
	return out
}
