package mcp

import (
	"context"
	"errors"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/imports"
	"github.com/frankbardon/pulse/skills"
)

// This file holds the SDK-free typed tool handlers: one exported
// func(ctx, *pulse.Pulse, In) (Out, error) per registered MCP tool. Each
// handler calls the public facade exclusively and returns the facade's
// errors verbatim — coded errors are never flattened to strings here; the
// adapter (mcp/gosdk) renders a *errors.CodedError as the
// structured {code, message, details} envelope. The type-erased
// ToolDescriptor.Invoke (tools.go) wraps these with strict unmarshal.

// errMissingArg is the plain validation error a thin tool returns when a
// required argument is absent. It is intentionally NOT a coded error: it
// mirrors the legacy "missing or invalid 'x'" guard, surfaced to the LLM
// as a tool-error result so it can self-correct.
func errMissingArg(name string) error {
	return errors.New("missing or invalid '" + name + "'")
}

// HandleInspect runs pulse_inspect: header-only schema introspection.
func HandleInspect(ctx context.Context, p *pulse.Pulse, in InspectIn) (InspectOut, error) {
	if in.Path == "" {
		return InspectOut{}, errMissingArg("path")
	}
	res, err := p.Inspect(ctx, in.Path)
	if err != nil {
		return InspectOut{}, err
	}
	return *res, nil
}

// HandlePredict runs pulse_predict: no-execute request validation.
func HandlePredict(ctx context.Context, p *pulse.Pulse, in PredictIn) (PredictOut, error) {
	req := in
	res, err := p.Predict(ctx, &req)
	if err != nil {
		return PredictOut{}, err
	}
	return *res, nil
}

// HandleProcess runs pulse_process: the buffered aggregation pipeline.
func HandleProcess(ctx context.Context, p *pulse.Pulse, in ProcessIn) (ProcessOut, error) {
	req := in
	res, err := p.Process(ctx, &req)
	if err != nil {
		return ProcessOut{}, err
	}
	return *res, nil
}

// HandleCompose runs pulse_compose: multi-request batch + overlay fold.
func HandleCompose(ctx context.Context, p *pulse.Pulse, in ComposeIn) (ComposeOut, error) {
	req := in
	res, err := p.Compose(ctx, &req)
	if err != nil {
		return ComposeOut{}, err
	}
	return *res, nil
}

// HandleProcessChain runs pulse_process_chain: a source-rooted linear chain.
func HandleProcessChain(ctx context.Context, p *pulse.Pulse, in ProcessChainIn) (ProcessChainOut, error) {
	req := in
	res, err := p.ProcessChain(ctx, &req)
	if err != nil {
		return ProcessChainOut{}, err
	}
	return *res, nil
}

// HandleSample runs pulse_sample: returns up to Count rows (default 10).
func HandleSample(ctx context.Context, p *pulse.Pulse, in SampleIn) (SampleOut, error) {
	if in.Path == "" {
		return SampleOut{}, errMissingArg("path")
	}
	count := 10
	if in.Count > 0 {
		count = in.Count
	}
	rows, err := p.Sample(ctx, in.Path, count)
	if err != nil {
		return SampleOut{}, err
	}
	out := SampleOut{Rows: make([]map[string]any, len(rows))}
	copy(out.Rows, rows)
	return out, nil
}

// HandleFacet runs pulse_facet: distinct values for a single field.
func HandleFacet(ctx context.Context, p *pulse.Pulse, in FacetIn) (FacetOut, error) {
	if in.Path == "" {
		return FacetOut{}, errMissingArg("path")
	}
	if in.Field == "" {
		return FacetOut{}, errMissingArg("field")
	}
	values, err := p.Facet(ctx, in.Path, in.Field)
	if err != nil {
		return FacetOut{}, err
	}
	if values == nil {
		values = []string{}
	}
	return FacetOut{Values: values}, nil
}

// HandleFacetSchema runs pulse_facet_schema: the rich faceting endpoint.
func HandleFacetSchema(ctx context.Context, p *pulse.Pulse, in FacetSchemaIn) (FacetSchemaOut, error) {
	req := in
	res, err := p.FacetSchema(ctx, &req)
	if err != nil {
		return FacetSchemaOut{}, err
	}
	return *res, nil
}

// HandleManifest runs pulse_manifest: the slim bootstrap blob. Prose
// descriptions live in skills and are fetched via pulse_skills_get;
// duplicating them in the per-session bootstrap is the bloat --slim avoids.
func HandleManifest(ctx context.Context, p *pulse.Pulse, _ ManifestIn) (ManifestOut, error) {
	slim := descriptor.SlimManifest(p.Manifest(ctx))
	if slim == nil {
		return ManifestOut{}, nil
	}
	return *slim, nil
}

// HandleSkillsList runs pulse_skills_list: the embedded skill-pack index.
func HandleSkillsList(_ context.Context, _ *pulse.Pulse, _ SkillsListIn) (SkillsListOut, error) {
	return SkillsListOut{Skills: skills.List()}, nil
}

// HandleSkillsGet runs pulse_skills_get: the markdown body of one skill.
func HandleSkillsGet(_ context.Context, _ *pulse.Pulse, in SkillsGetIn) (SkillsGetOut, error) {
	if in.Name == "" {
		return SkillsGetOut{}, errMissingArg("name")
	}
	body, ok := skills.Get(in.Name)
	if !ok {
		return SkillsGetOut{}, errors.New("skill " + strconvQuote(in.Name) + " not found")
	}
	return SkillsGetOut{Body: body}, nil
}

// HandleExamplesSearch runs pulse_examples_search. All three filters are
// optional and ANDed.
func HandleExamplesSearch(_ context.Context, p *pulse.Pulse, in ExamplesSearchIn) (ExamplesSearchOut, error) {
	results := p.ExamplesSearch(in.Query, in.Tags, in.Category)
	if results == nil {
		results = []pulse.ExampleSummary{}
	}
	return ExamplesSearchOut{Results: results}, nil
}

// HandleExamplesGet runs pulse_examples_get: a single runnable example.
func HandleExamplesGet(_ context.Context, p *pulse.Pulse, in ExamplesGetIn) (ExamplesGetOut, error) {
	if in.Name == "" {
		return ExamplesGetOut{}, errMissingArg("name")
	}
	ex, ok := p.ExampleGet(in.Name)
	if !ok {
		return ExamplesGetOut{}, errors.New("example " + strconvQuote(in.Name) + " not found")
	}
	return *ex, nil
}

// HandleErrorsLookup runs pulse_errors_lookup. At least one axis must be
// set; supplied axes are intersected.
func HandleErrorsLookup(_ context.Context, _ *pulse.Pulse, in ErrorsLookupIn) (ErrorsLookupOut, error) {
	if in.Code == "" && in.Domain == "" && in.Query == "" {
		return ErrorsLookupOut{}, errors.New("specify at least one of code, domain, query")
	}
	return ErrorsLookupOut{Results: intersectErrorLookup(in.Code, in.Domain, in.Query)}, nil
}

// HandleImport runs pulse_import: converts a tabular source into a managed
// .pulse handle. On success the adapter fires schema-bind-on-inspect.
func HandleImport(ctx context.Context, p *pulse.Pulse, in ImportIn) (ImportOut, error) {
	if in.Source == "" {
		return ImportOut{}, errMissingArg("source")
	}
	spec := imports.Spec{
		SourcePath: in.Source,
		Format:     in.Format,
		Handle:     in.Handle,
		Sheet:      in.Sheet,
		Overwrite:  in.Overwrite,
	}
	if in.TTL != "" {
		d, err := imports.ParseTTL(in.TTL)
		if err != nil {
			return ImportOut{}, err
		}
		spec.TTL = d
	}
	res, err := p.ImportFile(ctx, spec)
	if err != nil {
		return ImportOut{}, err
	}
	return *res, nil
}

// HandleDrop runs pulse_drop: removes a managed import handle.
func HandleDrop(ctx context.Context, p *pulse.Pulse, in DropIn) (DropOut, error) {
	if in.Handle == "" {
		return DropOut{}, errMissingArg("handle")
	}
	if err := p.Drop(ctx, in.Handle); err != nil {
		return DropOut{}, err
	}
	return DropOut{Handle: in.Handle, Dropped: true}, nil
}

// HandleImportsList runs pulse_imports_list: managed-import pool snapshot.
func HandleImportsList(ctx context.Context, p *pulse.Pulse, _ ImportsListIn) (ImportsListOut, error) {
	entries, err := p.Imports(ctx)
	if err != nil {
		return ImportsListOut{}, err
	}
	if entries == nil {
		entries = []pulse.ImportEntry{}
	}
	return ImportsListOut{Imports: entries}, nil
}

// HandleLabelTables runs pulse_label_tables: the INPUT-direction discovery
// companion to pulse_label_resolve.
func HandleLabelTables(_ context.Context, p *pulse.Pulse, _ LabelTablesIn) (LabelTablesOut, error) {
	tables := p.LabelTables()
	if tables == nil {
		tables = []pulse.LabelTableInfo{}
	}
	return LabelTablesOut{Tables: tables}, nil
}

// HandleLabelResolve runs pulse_label_resolve: reverse-resolves a name to
// the raw categorical key(s) a filter / grouper expects.
func HandleLabelResolve(_ context.Context, p *pulse.Pulse, in LabelResolveIn) (LabelResolveOut, error) {
	if in.Table == "" {
		return LabelResolveOut{}, errMissingArg("table")
	}
	matches, err := p.ResolveLabel(in.Table, in.Query, in.Limit)
	if err != nil {
		return LabelResolveOut{}, err
	}
	if matches == nil {
		matches = []pulse.LabelMatch{}
	}
	return LabelResolveOut{Matches: matches}, nil
}

// intersectErrorLookup composes the three lookup axes. Each non-empty axis
// contributes a candidate set; the final result is the intersection. When
// only one axis is set, the result is that axis's natural output
// (preserving Search's ranking; ByDomain's alphabetical order; Lookup's
// 0/1 element). Returns a non-nil empty slice when nothing matches.
func intersectErrorLookup(code, domain, query string) []perr.LookupResult {
	axes := make([][]perr.LookupResult, 0, 3)
	if code != "" {
		hit, ok := perr.Lookup(code)
		if ok {
			axes = append(axes, []perr.LookupResult{hit})
		} else {
			axes = append(axes, []perr.LookupResult{})
		}
	}
	if domain != "" {
		axes = append(axes, perr.ByDomain(domain))
	}
	if query != "" {
		axes = append(axes, perr.Search(query))
	}
	if len(axes) == 0 {
		return []perr.LookupResult{}
	}
	base := axes[0]
	for i := 1; i < len(axes); i++ {
		present := make(map[string]struct{}, len(axes[i]))
		for _, r := range axes[i] {
			present[r.Code] = struct{}{}
		}
		next := base[:0:0]
		for _, r := range base {
			if _, ok := present[r.Code]; ok {
				next = append(next, r)
			}
		}
		base = next
	}
	if base == nil {
		return []perr.LookupResult{}
	}
	return base
}

// strconvQuote wraps strconv.Quote so the not-found messages stay
// fmt.Sprintf-free (the descriptor structural-defense ban does not reach
// this package, but the convention is kept repo-wide).
func strconvQuote(s string) string {
	return `"` + s + `"`
}
