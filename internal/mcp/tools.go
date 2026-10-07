package mcp

import (
	"bytes"
	"context"
	"encoding/json"

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

// lenientDecode unmarshals raw into In, ignoring unknown fields (the standard
// encoding/json behaviour). Used by tools without a strict-decode contract.
func lenientDecode[In any](raw json.RawMessage, _ *descx.InstanceSnapshot) (In, error) {
	var in In
	if len(raw) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, err
	}
	return in, nil
}

// strictRequestDecode rejects unknown top-level keys on a types.Request before
// decoding, turning a silently-dropped slot into PULSE_REQUEST_UNKNOWN_FIELD.
func strictRequestDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (types.Request, error) {
	if ce := checkUnknownRequestKeys(raw, inst); ce != nil {
		return types.Request{}, ce
	}
	return lenientDecode[types.Request](raw, inst)
}

// strictImportDecode decodes pulse_import's input, holding each `groups`
// entry to its exact {key, members} shape. The rest of the input stays
// lenient, as it always was. The strictness is targeted because a
// misspelled key inside a group is not a harmless drop: `{"keys": [...],
// "members": [...]}` would decode as a KEYLESS tuple group, which skips
// the member-constancy check and writes a differently-encoded cohort
// without a word. An unknown key there is PULSE_GROUP_DECLARATION_INVALID.
func strictImportDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (ImportIn, error) {
	if err := checkGroupsShape(raw, "pulse_import"); err != nil {
		return ImportIn{}, err
	}
	return lenientDecode[ImportIn](raw, inst)
}

// strictDedupDecode is strictImportDecode's twin for pulse_dedup: the
// same `groups` slot, held to the same exact {key, members} shape for
// the same reason.
func strictDedupDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (DedupIn, error) {
	if err := checkGroupsShape(raw, "pulse_dedup"); err != nil {
		return DedupIn{}, err
	}
	return lenientDecode[DedupIn](raw, inst)
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

// strictComposedDecode applies the per-request strict check across a
// ComposedRequest's Requests slot.
func strictComposedDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (types.ComposedRequest, error) {
	if ce := checkUnknownKeysComposed(raw, inst); ce != nil {
		return types.ComposedRequest{}, ce
	}
	return lenientDecode[types.ComposedRequest](raw, inst)
}

// strictChainDecode applies the per-stage strict check across a ChainRequest's
// Stages slot.
func strictChainDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (types.ChainRequest, error) {
	if ce := checkUnknownKeysChain(raw, inst); ce != nil {
		return types.ChainRequest{}, ce
	}
	return lenientDecode[types.ChainRequest](raw, inst)
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
		toolmeta.ToolInspect:        makeInvoke(lenientDecode[InspectIn], HandleInspect),
		toolmeta.ToolPredict:        makeInvoke(withReturnDefault(cfg, strictRequestDecode, fillRequestReturn), HandlePredict),
		toolmeta.ToolProcess:        makeInvoke(withReturnDefault(cfg, strictRequestDecode, fillRequestReturn), HandleProcess),
		toolmeta.ToolProcessChain:   makeInvoke(withReturnDefault(cfg, strictChainDecode, fillChainReturn), HandleProcessChain),
		toolmeta.ToolCompose:        makeInvoke(withReturnDefault(cfg, strictComposedDecode, fillComposeReturn), HandleCompose),
		toolmeta.ToolSample:         makeInvoke(lenientDecode[SampleIn], HandleSample),
		toolmeta.ToolFacet:          makeInvoke(lenientDecode[FacetIn], HandleFacet),
		toolmeta.ToolFacetSchema:    makeInvoke(lenientDecode[FacetSchemaIn], HandleFacetSchema),
		toolmeta.ToolLookup:         makeInvoke(lenientDecode[LookupIn], HandleLookup),
		toolmeta.ToolSkillsList:     makeInvoke(lenientDecode[SkillsListIn], HandleSkillsList),
		toolmeta.ToolSkillsGet:      makeInvoke(lenientDecode[SkillsGetIn], HandleSkillsGet),
		toolmeta.ToolManifest:       makeInvoke(lenientDecode[ManifestIn], HandleManifest),
		toolmeta.ToolExamplesSearch: makeInvoke(lenientDecode[ExamplesSearchIn], HandleExamplesSearch),
		toolmeta.ToolExamplesGet:    makeInvoke(lenientDecode[ExamplesGetIn], HandleExamplesGet),
		toolmeta.ToolErrorsLookup:   makeInvoke(lenientDecode[ErrorsLookupIn], HandleErrorsLookup),
		toolmeta.ToolImport:         makeInvoke(strictImportDecode, HandleImport),
		toolmeta.ToolDedup:          makeInvoke(strictDedupDecode, HandleDedup),
		toolmeta.ToolDrop:           makeInvoke(lenientDecode[DropIn], HandleDrop),
		toolmeta.ToolImportsList:    makeInvoke(lenientDecode[ImportsListIn], HandleImportsList),
		toolmeta.ToolLabelTables:    makeInvoke(lenientDecode[LabelTablesIn], HandleLabelTables),
		toolmeta.ToolLabelResolve:   makeInvoke(lenientDecode[LabelResolveIn], HandleLabelResolve),
		toolmeta.ToolRangeTables:    makeInvoke(lenientDecode[RangeTablesIn], HandleRangeTables),
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
