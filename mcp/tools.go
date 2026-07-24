package mcp

import (
	"context"
	"encoding/json"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/toolmeta"
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
type decodeFunc[In any] func(raw json.RawMessage) (In, error)

// lenientDecode unmarshals raw into In, ignoring unknown fields (the standard
// encoding/json behaviour). Used by tools without a strict-decode contract.
func lenientDecode[In any](raw json.RawMessage) (In, error) {
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
func strictRequestDecode(raw json.RawMessage) (types.Request, error) {
	if ce := checkUnknownRequestKeys(raw); ce != nil {
		return types.Request{}, ce
	}
	return lenientDecode[types.Request](raw)
}

// strictComposedDecode applies the per-request strict check across a
// ComposedRequest's Requests slot.
func strictComposedDecode(raw json.RawMessage) (types.ComposedRequest, error) {
	if ce := checkUnknownKeysComposed(raw); ce != nil {
		return types.ComposedRequest{}, ce
	}
	return lenientDecode[types.ComposedRequest](raw)
}

// strictChainDecode applies the per-stage strict check across a ChainRequest's
// Stages slot.
func strictChainDecode(raw json.RawMessage) (types.ChainRequest, error) {
	if ce := checkUnknownKeysChain(raw); ce != nil {
		return types.ChainRequest{}, ce
	}
	return lenientDecode[types.ChainRequest](raw)
}

// makeInvoke composes a decode function with a typed handler into the
// type-erased InvokeFunc. Errors (decode or handler) are returned verbatim.
func makeInvoke[In, Out any](decode decodeFunc[In], h func(context.Context, *pulse.Pulse, In) (Out, error)) InvokeFunc {
	return func(ctx context.Context, p *pulse.Pulse, raw json.RawMessage) (any, error) {
		in, err := decode(raw)
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

// invokers maps each tool name to its type-erased Invoke. cfg is threaded in
// so future tool closures can capture runtime configuration; today the catalog
// is config-independent.
func invokers(_ Config) map[string]InvokeFunc {
	return map[string]InvokeFunc{
		toolmeta.ToolInspect:        makeInvoke(lenientDecode[InspectIn], HandleInspect),
		toolmeta.ToolPredict:        makeInvoke(strictRequestDecode, HandlePredict),
		toolmeta.ToolProcess:        makeInvoke(strictRequestDecode, HandleProcess),
		toolmeta.ToolProcessChain:   makeInvoke(strictChainDecode, HandleProcessChain),
		toolmeta.ToolCompose:        makeInvoke(strictComposedDecode, HandleCompose),
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
		toolmeta.ToolImport:         makeInvoke(lenientDecode[ImportIn], HandleImport),
		toolmeta.ToolDrop:           makeInvoke(lenientDecode[DropIn], HandleDrop),
		toolmeta.ToolImportsList:    makeInvoke(lenientDecode[ImportsListIn], HandleImportsList),
		toolmeta.ToolLabelTables:    makeInvoke(lenientDecode[LabelTablesIn], HandleLabelTables),
		toolmeta.ToolLabelResolve:   makeInvoke(lenientDecode[LabelResolveIn], HandleLabelResolve),
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
