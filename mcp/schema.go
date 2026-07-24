package mcp

import (
	"encoding/json"
	"reflect"

	"github.com/frankbardon/pulse/mcp/toolmeta"
	"github.com/google/jsonschema-go/jsonschema"
)

// ToolSchema is the reflected, SDK-free descriptor for one registered MCP tool:
// its name, description, and the input/output JSON Schemas (draft 2020-12)
// reflected from the typed In/Out contract structs. The schemas are carried as
// json.RawMessage so consumers — and the thin go-sdk adapter — never need to
// import the schema reflector or any MCP SDK.
type ToolSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

// registry holds the reflected schema per tool name, plus a stable order that
// mirrors toolmeta.Names(). reflectErrors records any reflection failure so the
// unit test can assert the set is empty rather than the package panicking at
// init.
var (
	registry      = map[string]ToolSchema{}
	order         []string
	reflectErrors = map[string]error{}
)

// reflectSchema reflects a JSON Schema for t and marshals it to json.RawMessage.
// A fallback open-object schema is returned on error; the error is surfaced to
// the caller for recording.
func reflectSchema(t reflect.Type) (json.RawMessage, error) {
	s, err := jsonschema.ForType(t, nil)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`), err
	}
	body, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`), err
	}
	return json.RawMessage(body), nil
}

// register reflects the input/output schemas for one tool and stores the
// descriptor. Reflection errors are recorded (not panicked) so a single bad
// contract surfaces in the test rather than crashing every importer.
func register(name, description string, in, out reflect.Type) {
	inSchema, inErr := reflectSchema(in)
	outSchema, outErr := reflectSchema(out)
	if inErr != nil {
		reflectErrors[name+":in"] = inErr
	}
	if outErr != nil {
		reflectErrors[name+":out"] = outErr
	}
	registry[name] = ToolSchema{
		Name:         name,
		Description:  description,
		InputSchema:  inSchema,
		OutputSchema: outSchema,
	}
	order = append(order, name)
}

func init() {
	m := toolmeta.Meta()
	desc := make(map[string]string, len(m))
	for _, e := range m {
		desc[e.Name] = e.Description
	}
	d := func(name string) string { return desc[name] }

	register(toolmeta.ToolInspect, d(toolmeta.ToolInspect), reflect.TypeFor[InspectIn](), reflect.TypeFor[InspectOut]())
	register(toolmeta.ToolPredict, d(toolmeta.ToolPredict), reflect.TypeFor[PredictIn](), reflect.TypeFor[PredictOut]())
	register(toolmeta.ToolProcess, d(toolmeta.ToolProcess), reflect.TypeFor[ProcessIn](), reflect.TypeFor[ProcessOut]())
	register(toolmeta.ToolProcessChain, d(toolmeta.ToolProcessChain), reflect.TypeFor[ProcessChainIn](), reflect.TypeFor[ProcessChainOut]())
	register(toolmeta.ToolCompose, d(toolmeta.ToolCompose), reflect.TypeFor[ComposeIn](), reflect.TypeFor[ComposeOut]())
	register(toolmeta.ToolSample, d(toolmeta.ToolSample), reflect.TypeFor[SampleIn](), reflect.TypeFor[SampleOut]())
	register(toolmeta.ToolFacet, d(toolmeta.ToolFacet), reflect.TypeFor[FacetIn](), reflect.TypeFor[FacetOut]())
	register(toolmeta.ToolFacetSchema, d(toolmeta.ToolFacetSchema), reflect.TypeFor[FacetSchemaIn](), reflect.TypeFor[FacetSchemaOut]())
	register(toolmeta.ToolLookup, d(toolmeta.ToolLookup), reflect.TypeFor[LookupIn](), reflect.TypeFor[LookupOut]())
	register(toolmeta.ToolSkillsList, d(toolmeta.ToolSkillsList), reflect.TypeFor[SkillsListIn](), reflect.TypeFor[SkillsListOut]())
	register(toolmeta.ToolSkillsGet, d(toolmeta.ToolSkillsGet), reflect.TypeFor[SkillsGetIn](), reflect.TypeFor[SkillsGetOut]())
	register(toolmeta.ToolManifest, d(toolmeta.ToolManifest), reflect.TypeFor[ManifestIn](), reflect.TypeFor[ManifestOut]())
	register(toolmeta.ToolExamplesSearch, d(toolmeta.ToolExamplesSearch), reflect.TypeFor[ExamplesSearchIn](), reflect.TypeFor[ExamplesSearchOut]())
	register(toolmeta.ToolExamplesGet, d(toolmeta.ToolExamplesGet), reflect.TypeFor[ExamplesGetIn](), reflect.TypeFor[ExamplesGetOut]())
	register(toolmeta.ToolErrorsLookup, d(toolmeta.ToolErrorsLookup), reflect.TypeFor[ErrorsLookupIn](), reflect.TypeFor[ErrorsLookupOut]())
	register(toolmeta.ToolImport, d(toolmeta.ToolImport), reflect.TypeFor[ImportIn](), reflect.TypeFor[ImportOut]())
	register(toolmeta.ToolDrop, d(toolmeta.ToolDrop), reflect.TypeFor[DropIn](), reflect.TypeFor[DropOut]())
	register(toolmeta.ToolImportsList, d(toolmeta.ToolImportsList), reflect.TypeFor[ImportsListIn](), reflect.TypeFor[ImportsListOut]())
	register(toolmeta.ToolLabelTables, d(toolmeta.ToolLabelTables), reflect.TypeFor[LabelTablesIn](), reflect.TypeFor[LabelTablesOut]())
	register(toolmeta.ToolLabelResolve, d(toolmeta.ToolLabelResolve), reflect.TypeFor[LabelResolveIn](), reflect.TypeFor[LabelResolveOut]())
}

// Schemas returns the reflected descriptor for every registered tool in stable
// registration order (matching toolmeta.Names()).
func Schemas() []ToolSchema {
	out := make([]ToolSchema, 0, len(order))
	for _, name := range order {
		out = append(out, registry[name])
	}
	return out
}

// SchemaFor returns the reflected descriptor for the named tool.
func SchemaFor(name string) (ToolSchema, bool) {
	ts, ok := registry[name]
	return ts, ok
}
