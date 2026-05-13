// Package pulse is a high-performance, self-describing tabular data processing engine.
//
// Pulse ships as a CLI binary and as an embeddable Go library.
// The library is the primary deliverable; the CLI is a thin adapter over it.
package pulse

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/examples"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/imports"
	"github.com/frankbardon/pulse/internal/query"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/service"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Type aliases re-exported from the types package so embedders can use
// pulse.Request instead of types.Request.
type (
	Request         = types.Request
	Response        = types.Response
	ComposedRequest = types.ComposedRequest

	// SynthSpec is the parsed synthesis request shape.
	SynthSpec = synth.Spec
	// SynthResult is the result of a successful Synth call.
	SynthResult = synth.Result
	// SynthOptions modulate the deterministic seed and other knobs.
	SynthOptions = synth.Options
	// Profile is the cohort statistical summary used by from-profile.
	Profile = synth.Profile
	// ProfileOptions modulate which statistics the profiler captures.
	ProfileOptions = synth.ProfileOptions

	// Example is the full record returned by ExampleGet — runnable
	// request JSON plus the indexed metadata.
	Example = examples.Example
	// ExampleSummary is the lightweight projection returned by
	// ExamplesSearch.
	ExampleSummary = examples.ExampleSummary

	// ErrorMetadata is the depth-on-demand projection returned by
	// ErrorLookup, ErrorsByDomain, and ErrorsSearch. Carries the code,
	// domain, canonical Message, and materialised Fixup templates.
	ErrorMetadata = errors.LookupResult
	// ErrorFixup is one repair template attached to an error code.
	ErrorFixup = errors.Fixup
)

// Record is a row of field→value data returned by Sample.
type Record = map[string]any

// Options configures a Pulse instance.
type Options struct {
	// DataDir is the base directory for cohort files.
	// Defaults to PULSE_DATA_DIR if empty and FS is not set.
	DataDir string

	// FS is an optional custom filesystem.
	// When set, DataDir is ignored for filesystem construction.
	FS afero.Fs

	// DisableDefaults turns off the smart-defaults pass that infers
	// operator Type from the named field's schema type when the caller
	// omits it. Defaults to false (defaults enabled). Predict still
	// computes and reports DefaultsApplied independently — this flag
	// governs only what the runtime mutates on the live request.
	DisableDefaults bool

	// ImportsDir overrides the managed-imports directory. Defaults to
	// imports.DefaultImportsDir (resolved relative to the Pulse fs
	// root). Honoured before the PULSE_IMPORTS_DIR env var.
	ImportsDir string

	// ImportTTL overrides the default TTL applied to managed imports
	// when the caller does not pass one. Zero falls back to the
	// PULSE_IMPORT_TTL env var, then to imports.DefaultTTL. Negative
	// values pin imports (never expire) by default.
	ImportTTL time.Duration

	// ImportSourceFS is the afero.Fs used to read source files when
	// ImportFile / pulse_import receives an absolute path. Defaults to
	// afero.NewOsFs() on real OS installs, or to FS when FS is an
	// in-memory MemMapFs (so tests stay hermetic). Setting this
	// disables the default jail: the explicit fs IS the boundary.
	ImportSourceFS afero.Fs

	// ImportSourceJailRoot confines absolute source paths to a
	// directory tree. Empty string defaults to os.Getwd() at New
	// time — the natural sandbox for an MCP server or CLI invocation.
	// Ignored when ImportSourceFS is set explicitly.
	ImportSourceJailRoot string
}

// Pulse is the top-level library facade. It wraps the service layer and
// provides a clean API for embedding Pulse into Go programs.
type Pulse struct {
	svc     *service.Service
	fsys    afero.Fs
	imports *imports.Manager
}

// New creates a new Pulse instance with the given options.
func New(opts Options) (*Pulse, error) {
	var fsCfg *fs.Config
	var err error

	if opts.FS != nil {
		// Custom FS provided: use it directly.
		fsCfg, err = fs.New(fs.WithFs(opts.FS))
		if err != nil {
			return nil, fmt.Errorf("pulse: configuring filesystem: %w", err)
		}
	} else if opts.DataDir != "" {
		// Explicit data directory.
		fsCfg, err = fs.New(fs.WithDataDir(opts.DataDir))
		if err != nil {
			return nil, fmt.Errorf("pulse: configuring filesystem: %w", err)
		}
	} else {
		// Fall back to environment defaults.
		fsCfg, err = fs.Default()
		if err != nil {
			return nil, fmt.Errorf("pulse: configuring filesystem: %w", err)
		}
	}

	svc := service.New(fsCfg)
	svc.SetDisableDefaults(opts.DisableDefaults)

	importsMgr, err := imports.New(fsCfg.Fs(), imports.Options{
		ImportsDir:     opts.ImportsDir,
		DefaultTTL:     opts.ImportTTL,
		SourceFS:       opts.ImportSourceFS,
		SourceJailRoot: opts.ImportSourceJailRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("pulse: configuring imports manager: %w", err)
	}

	return &Pulse{
		svc:     svc,
		fsys:    fsCfg.Fs(),
		imports: importsMgr,
	}, nil
}

// Open reads a .pulse file and returns a Cohort with the parsed schema.
func (p *Pulse) Open(ctx context.Context, path string) (*Cohort, error) {
	inner, err := p.svc.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Cohort{inner: inner}, nil
}

// Process executes a single processing request against a cohort.
func (p *Pulse) Process(ctx context.Context, req *Request) (*Response, error) {
	resp, err := p.svc.Process(ctx, req)
	if err == nil && req != nil && req.Cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(req.Cohort))
	}
	return resp, err
}

// Row is a single result row in a processing stream.
type Row = service.Row

// RowIter is a pull-based iterator over a processing result. Each call
// to Next returns the next row or (nil, false, nil) on exhaustion. Close
// releases underlying resources. Metadata returns the run metadata once
// available (always present after the iterator is drained).
type RowIter = service.RowIter

// ProcessStream executes a request and returns a pull-based row iterator
// over the result. Equivalent to Process for any request shape — same
// gates, same errors — but streaming consumers (HTTP responders, NDJSON
// writers, downstream pipelines) can drain rows one at a time without
// buffering the full result in their own memory.
//
// Predict's Streamable flag reports whether the underlying execution
// avoids buffering inside the engine; ProcessStream wraps the result
// regardless, so the API is stable for non-streamable requests too.
func (p *Pulse) ProcessStream(ctx context.Context, req *Request) (RowIter, error) {
	return p.svc.ProcessStream(ctx, req)
}

// Compose executes multiple requests, returning a response for each.
func (p *Pulse) Compose(ctx context.Context, req *ComposedRequest) ([]*Response, error) {
	return p.svc.Compose(ctx, req)
}

// ComposeOptions controls parallel execution. See service.ComposeOptions.
type ComposeOptions = service.ComposeOptions

// ComposeParallel runs every request in req concurrently across a bounded
// worker pool. Responses are returned in input order. Workers share the
// engine's read-only registries; each Process call constructs fresh
// stateful operators per request, so concurrent execution is safe.
//
// Defaults: MaxWorkers = runtime.GOMAXPROCS(0), no per-request timeout,
// FailFast = true (set FailFast=false to collect every request's outcome
// instead of cancelling siblings on first error).
func (p *Pulse) ComposeParallel(ctx context.Context, req *ComposedRequest, opts ComposeOptions) ([]*Response, error) {
	return p.svc.ComposeParallel(ctx, req, opts)
}

// Import converts tabular source data into a .pulse file.
// The job's FS field is set to the Pulse instance's filesystem if not already set.
func (p *Pulse) Import(ctx context.Context, job *pio.ImportJob) (*pio.ImportReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	return job.Run(ctx)
}

// Export converts a .pulse file into tabular output.
// The job's FS field is set to the Pulse instance's filesystem if not already set.
func (p *Pulse) Export(ctx context.Context, job *pio.ExportJob) (*pio.ExportReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	return job.Run(ctx)
}

// Convert chains import and export with no intermediate file on disk.
// The job's FS field is set to the Pulse instance's filesystem if not already set.
func (p *Pulse) Convert(ctx context.Context, job *pio.ConvertJob) (*pio.ConvertReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	return job.Run(ctx)
}

// Type aliases re-exported from the imports package so embedders can
// use pulse.ImportSpec instead of imports.Spec.
type (
	ImportSpec   = imports.Spec
	ImportResult = imports.Result
	ImportEntry  = imports.Entry
)

// ImportFile auto-detects the source format, converts the source into a
// managed .pulse file under the imports pool, and returns the resulting
// handle. Pulse-native sources pass through unchanged (no copy, no
// sidecar). Sliding-window TTL applies to managed handles — every
// subsequent Inspect / Predict / Process / Sample / Facet / Ask against
// the handle bumps the expiry forward.
func (p *Pulse) ImportFile(ctx context.Context, spec ImportSpec) (*ImportResult, error) {
	return p.imports.Open(ctx, spec)
}

// Drop removes a managed import handle (and its sidecar) from the pool.
// Returns PULSE_IMPORT_SOURCE_MISSING when the handle is unknown.
func (p *Pulse) Drop(ctx context.Context, handle string) error {
	return p.imports.Drop(ctx, handle)
}

// Imports returns a snapshot of the managed-imports pool. Sweep is not
// invoked; expired entries are flagged via Entry.Expired so callers can
// render them. Results are sorted by handle name.
func (p *Pulse) Imports(ctx context.Context) ([]ImportEntry, error) {
	return p.imports.List(ctx)
}

// SweepImports removes every expired managed handle and returns the
// list of swept handle names. Invoked opportunistically by ImportFile
// and exposed here for callers that want explicit control (CLI
// maintenance, periodic ticker, etc.).
func (p *Pulse) SweepImports(ctx context.Context) ([]string, error) {
	return p.imports.Sweep(ctx)
}

// ResolveImport returns the managed-pool path for a handle, or
// PULSE_IMPORT_SOURCE_MISSING when no such handle exists. Embedders
// who want to address a managed handle by name (instead of by path)
// run path through this resolver before passing it to Inspect/Process.
func (p *Pulse) ResolveImport(ctx context.Context, handle string) (string, error) {
	return p.imports.Resolve(ctx, handle)
}

// touchManaged is the hook each read-path facade method fires on the
// resolved cohort path. Best-effort: errors are intentionally swallowed
// — TTL slides are an optimisation, not a correctness path.
func (p *Pulse) touchManaged(ctx context.Context, path string) {
	if p.imports == nil || path == "" {
		return
	}
	_ = p.imports.Touch(ctx, path)
}

// Inspect reads a .pulse file header and schema, returning structured field information.
// It never reads record data.
func (p *Pulse) Inspect(ctx context.Context, path string) (*descriptor.InspectResult, error) {
	data, err := afero.ReadFile(p.fsys, path)
	if err != nil {
		return nil, fmt.Errorf("pulse: reading file for inspect: %w", err)
	}

	env := descriptor.InspectFromBytes(data, nil)
	if len(env.Errors) > 0 {
		return nil, fmt.Errorf("pulse: inspect: %s", env.Errors[0].Message)
	}

	result, ok := env.Data.(*descriptor.InspectResult)
	if !ok {
		return nil, fmt.Errorf("pulse: inspect returned unexpected type")
	}
	p.touchManaged(ctx, path)
	return result, nil
}

// Predict validates a request against a .pulse file without executing it.
// It reads only the header and schema, never record data.
func (p *Pulse) Predict(ctx context.Context, req *Request) (*descriptor.PredictResult, error) {
	if req.Cohort == nil {
		return nil, fmt.Errorf("pulse: predict requires a cohort")
	}

	path := resolveCohortPath(req.Cohort)

	data, err := afero.ReadFile(p.fsys, path)
	if err != nil {
		return nil, fmt.Errorf("pulse: reading file for predict: %w", err)
	}

	env := descriptor.PredictFromBytes(data, req, nil)
	if len(env.Errors) > 0 {
		// Return the result (which has Valid=false) rather than erroring.
		result, ok := env.Data.(*descriptor.PredictResult)
		if ok {
			p.touchManaged(ctx, path)
			return result, nil
		}
		return nil, fmt.Errorf("pulse: predict: %s", env.Errors[0].Message)
	}

	result, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		return nil, fmt.Errorf("pulse: predict returned unexpected type")
	}
	p.touchManaged(ctx, path)
	return result, nil
}

// Sample returns up to n rows from the cohort as maps of field name to value.
func (p *Pulse) Sample(ctx context.Context, path string, n int) ([]Record, error) {
	rows, err := p.svc.Sample(ctx, path, n)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return rows, err
}

// Synth materializes a synthetic .pulse file at output from spec. The
// generator is deterministic for a given (spec, opts.Seed) pair: same
// seed produces a byte-identical file.
func (p *Pulse) Synth(_ context.Context, spec *SynthSpec, output string, opts SynthOptions) (*SynthResult, error) {
	return synth.Synth(p.fsys, spec, output, opts)
}

// Profile reads a .pulse file at path and returns a statistical summary
// suitable for from-profile synthesis. The profile retains no individual
// rows from the source data.
func (p *Pulse) Profile(_ context.Context, path string, opts ProfileOptions) (*Profile, error) {
	return synth.ProfileFile(p.fsys, path, opts)
}

// Facet returns distinct values for the named field in the cohort.
func (p *Pulse) Facet(ctx context.Context, path string, field string) ([]string, error) {
	values, err := p.svc.Facet(ctx, path, field)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return values, err
}

// AskRequest is the input to pulse.Ask — the unified one-shot facade
// that collapses (optional) import, inspect, predict, and execute
// into a single call.
//
// Cohort selection precedence (first non-empty wins):
//  1. Request.Cohort.Filename — explicit cohort the caller already has.
//  2. File — back-compat shorthand for Request.Cohort.Filename when
//     Request.Cohort is nil.
//  3. Source — a tabular source file (csv, tsv, ndjson, jsonarray,
//     parquet, arrow, excel) or an existing .pulse. Ask imports it via
//     the managed pool (default TTL 7d) and uses the resulting handle.
//     This is the path that collapses pulse_import + pulse_ask into a
//     single call — preferred for one-shot analytical queries.
//
// OnInvalid governs what Ask does when predict reports the request as
// invalid:
//   - "" or "abort" — return a SERVICE_VALIDATION error.
//   - "suggest"     — return an AskResponse with Suggestions populated.
//
// Predict=true skips execution even on a valid request (the LLM's
// "what would happen if I ran this?" probe).
type AskRequest struct {
	File    string         `json:"file,omitempty"`
	Request *types.Request `json:"request,omitempty"`
	// Query is an optional natural-language query string. When set, the
	// server parses it against the cohort's schema and synthesises a
	// types.Request. The parsed request fills only the slots the
	// supplied Request left empty; explicit fields in Request always
	// win on collision. When Query is empty, Ask behaves exactly as
	// before (structured Request only).
	Query     string `json:"query,omitempty"`
	OnInvalid string `json:"on_invalid,omitempty"`
	Predict   bool   `json:"predict,omitempty"`

	// Source is an optional source-file path (csv, tsv, ndjson,
	// jsonarray, parquet, arrow, excel, or .pulse). When set and
	// Request.Cohort is unspecified, Ask runs the managed-import
	// pipeline first (same semantics as pulse.ImportFile / pulse_import)
	// and uses the resulting handle as the cohort for the rest of the
	// call. The import is sliding-window TTL-tracked — subsequent Ask
	// / Process / Inspect calls against the same handle bump expiry.
	Source string `json:"source,omitempty"`

	// SourceFormat overrides extension-based format detection on the
	// Source path. Empty falls back to imports.FormatFromExt(Source).
	SourceFormat string `json:"source_format,omitempty"`

	// SourceTTL overrides the default 7-day TTL on the auto-import.
	// Accepts the same forms as pulse_import: Go duration ("24h",
	// "30m"), day suffix ("7d", "30d"), or "pin" for never-expire.
	SourceTTL string `json:"source_ttl,omitempty"`

	// SourceHandle overrides the managed handle name (defaults to the
	// source basename without extension).
	SourceHandle string `json:"source_handle,omitempty"`

	// SourceSheet selects an Excel sheet; ignored for non-Excel sources.
	SourceSheet string `json:"source_sheet,omitempty"`

	// SourceOverwrite replaces an existing managed handle of the same
	// name. Defaults to false (collision -> PULSE_IMPORT_HANDLE_EXISTS).
	SourceOverwrite bool `json:"source_overwrite,omitempty"`
}

// QueryResolution echoes the parser's decision when AskRequest.Query
// was set. Nil when no query parse fired.
type QueryResolution struct {
	// Query is the verbatim input the parser processed.
	Query string `json:"query"`

	// MatchedFields is every schema field name the parser resolved
	// against, in first-appearance order.
	MatchedFields []string `json:"matched_fields"`

	// Confidence is the aggregate parser confidence in [0, 1].
	// Product of per-step weights (verb match × per-field match),
	// floored at 0 on PULSE_QUERY_UNRESOLVED.
	Confidence float64 `json:"confidence"`
}

// AskResponse is the JSON-friendly envelope returned by pulse.Ask. It
// always carries the predict result; Process is populated only when
// execution ran; Suggestions is populated only on predict-invalid +
// OnInvalid="suggest".
//
// FormatVersion mirrors the descriptor envelope version so callers can
// gate on a single value across endpoints.
type AskResponse struct {
	FormatVersion   string                      `json:"format_version"`
	Predict         *descriptor.PredictResult   `json:"predict"`
	Process         *Response                   `json:"process,omitempty"`
	Suggestions     []errors.Fixup              `json:"suggestions,omitempty"`
	QueryResolution *QueryResolution            `json:"query_resolution,omitempty"`
	// Import is populated when AskRequest.Source triggered an
	// auto-import. Carries the same fields as ImportResult: managed
	// handle name, resulting path, format, row count, expiry. Nil
	// when no auto-import ran.
	Import   *ImportResult               `json:"import,omitempty"`
	Errors   []*descriptor.EnvelopeEntry `json:"errors"`
	Warnings []*descriptor.EnvelopeEntry `json:"warnings"`
}

// Ask is the unified facade. It optionally imports a source file
// (when AskRequest.Source is set), validates the request against the
// cohort schema, and executes it. On validation failure it either
// errors (OnInvalid="abort") or returns structured fixup suggestions
// (OnInvalid="suggest"). Setting Predict=true skips execution after a
// successful predict pass.
//
// Cohort precedence: Request.Cohort > File > Source (auto-import).
// The auto-import path collapses pulse_import + pulse_ask into a
// single call — the preferred one-shot entry point for analytical
// queries on raw source files.
func (p *Pulse) Ask(ctx context.Context, req *AskRequest) (*AskResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("pulse: ask requires a request")
	}
	// Either Request or Query must be present.
	if req.Request == nil && req.Query == "" {
		return nil, fmt.Errorf("pulse: ask requires either a request body or a query")
	}

	// Ensure we have a Request struct to populate.
	if req.Request == nil {
		req.Request = &types.Request{}
	}

	resp := &AskResponse{FormatVersion: "1.0"}

	// Auto-import path: when Source is set and no explicit Cohort /
	// File was supplied, run the managed-import pipeline and use the
	// resulting handle as the cohort. The import metadata lands on
	// resp.Import so the caller sees the handle, path, and expiry.
	if req.Source != "" && req.Request.Cohort == nil && req.File == "" {
		spec := ImportSpec{
			SourcePath: req.Source,
			Format:     req.SourceFormat,
			Handle:     req.SourceHandle,
			Sheet:      req.SourceSheet,
			Overwrite:  req.SourceOverwrite,
		}
		if req.SourceTTL != "" {
			ttl, err := imports.ParseTTL(req.SourceTTL)
			if err != nil {
				return nil, fmt.Errorf("pulse: ask: parsing source_ttl: %w", err)
			}
			spec.TTL = ttl
		}
		imp, err := p.ImportFile(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("pulse: ask: auto-import: %w", err)
		}
		req.Request.Cohort = &types.Cohort{Filename: imp.Path}
		resp.Import = imp
	}

	// If the caller passed File but no Cohort, synthesise one so the
	// service can resolve the path consistently.
	if req.File != "" && req.Request.Cohort == nil {
		req.Request.Cohort = &types.Cohort{Filename: req.File}
	}

	// Natural-language query parse: read schema once, hand it to the
	// internal/query parser, and merge the parsed slots into the
	// supplied Request. Explicit fields in Request always win on
	// collision; query parsing only fills slots Request left empty.
	var queryIssues []query.Issue
	if req.Query != "" {
		if req.Request.Cohort == nil {
			return nil, fmt.Errorf("pulse: ask with query requires a cohort or file")
		}
		path := resolveCohortPath(req.Request.Cohort)
		data, err := afero.ReadFile(p.fsys, path)
		if err != nil {
			return nil, fmt.Errorf("pulse: ask: reading cohort for query parse: %w", err)
		}
		schema, schemaErr := readSchemaFromBytes(data)
		if schemaErr != nil {
			return nil, fmt.Errorf("pulse: ask: parsing schema for query parse: %w", schemaErr)
		}
		parsed := query.Parse(req.Query, schema, query.ParseOptions{File: req.Request.Cohort.Filename})
		// Preserve the caller's cohort exactly (file may include DataDir).
		parsed.Request.Cohort = req.Request.Cohort
		mergeParsedRequest(req.Request, parsed.Request)
		resp.QueryResolution = &QueryResolution{
			Query:         req.Query,
			MatchedFields: parsed.MatchedFields,
			Confidence:    parsed.Confidence,
		}
		queryIssues = parsed.Issues
	}

	out, err := p.svc.Ask(ctx, service.AskInput{
		Request:     req.Request,
		OnInvalid:   req.OnInvalid,
		PredictOnly: req.Predict,
	})
	if err != nil {
		// Service-level errors still benefit from the query resolution
		// being surfaced via the wrapped error path. Today the facade
		// returns the raw error; callers can re-parse details if they
		// need the resolution. (Keep behavior identical to pre-query.)
		return nil, err
	}
	if req.Request.Cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(req.Request.Cohort))
	}

	resp.Predict = out.Predict
	resp.Process = out.Process
	resp.Suggestions = out.Suggestions
	resp.Errors = out.Errors
	resp.Warnings = out.Warnings
	if resp.Errors == nil {
		resp.Errors = []*descriptor.EnvelopeEntry{}
	}
	if resp.Warnings == nil {
		resp.Warnings = []*descriptor.EnvelopeEntry{}
	}

	// Append parser-derived issues. PULSE_QUERY_UNRESOLVED becomes an
	// error when the parser produced no usable request structure
	// (Confidence == 0); otherwise it lands in warnings alongside
	// PULSE_QUERY_AMBIGUOUS.
	for _, iss := range queryIssues {
		entry := &descriptor.EnvelopeEntry{
			Code:    string(iss.Code),
			Message: iss.Message,
			Details: iss.Details,
		}
		if iss.Code == errors.PULSE_QUERY_UNRESOLVED && resp.QueryResolution != nil && resp.QueryResolution.Confidence == 0 {
			resp.Errors = append(resp.Errors, entry)
		} else {
			resp.Warnings = append(resp.Warnings, entry)
		}
	}

	return resp, nil
}

// readSchemaFromBytes opens a .pulse binary blob, skips the magic
// header, and parses the schema. Returns (schema, err). Used by Ask to
// resolve natural-language queries against the cohort's columns
// without round-tripping through descriptor.PredictFromBytes.
func readSchemaFromBytes(data []byte) (*encoding.Schema, error) {
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}
	return encoding.ReadSchema(r)
}

// mergeParsedRequest fills empty slots of dst from src. Explicit fields
// in dst win on collision so the caller's structured Request always
// dominates the parser's heuristic best guess. Only the slot
// categories the parser populates today are touched (Aggregations,
// Groups, Filterers, Windows, Sort, Tests); other slots in src are
// ignored even when dst has nothing in them.
func mergeParsedRequest(dst, src *types.Request) {
	if dst == nil || src == nil {
		return
	}
	if len(dst.Aggregations) == 0 && len(src.Aggregations) > 0 {
		dst.Aggregations = src.Aggregations
	}
	if len(dst.Groups) == 0 && len(src.Groups) > 0 {
		dst.Groups = src.Groups
	}
	if len(dst.Filterers) == 0 && len(src.Filterers) > 0 {
		dst.Filterers = src.Filterers
	}
	if len(dst.Windows) == 0 && len(src.Windows) > 0 {
		dst.Windows = src.Windows
	}
	if len(dst.Sort) == 0 && len(src.Sort) > 0 {
		dst.Sort = src.Sort
	}
	if len(dst.Tests) == 0 && len(src.Tests) > 0 {
		dst.Tests = src.Tests
	}
}

// ExamplesSearch returns summaries from the embedded request-example
// library matching the given filters. An empty filter is treated as
// "no constraint" for that dimension. Query is case-insensitive
// substring search across name, description, and operators; tags is
// ANDed; category is an exact match. Always returns a non-nil slice
// (possibly empty) for safe JSON marshaling.
func (p *Pulse) ExamplesSearch(query string, tags []string, category string) []ExampleSummary {
	return examples.Search(query, tags, category)
}

// ExampleGet returns the example whose _meta.name matches name. The
// returned Body is the request JSON with the _meta block stripped so
// it can be handed directly to Process / Predict.
func (p *Pulse) ExampleGet(name string) (*Example, bool) {
	return examples.Get(name)
}

// ErrorLookup returns the metadata projection for a single error code.
// Case-sensitive exact match. Returns (ErrorMetadata{}, false) when
// the code is unknown.
//
// The manifest carries only the alphabetized code-name list; per-code
// Message + Fixup detail lives behind this facade so per-session
// bootstrap stays lean. Use ErrorsByDomain / ErrorsSearch to enumerate
// in bulk.
func (p *Pulse) ErrorLookup(code string) (ErrorMetadata, bool) {
	return errors.Lookup(code)
}

// ErrorsByDomain returns every code's metadata in the named domain
// (CLI, DATA, ENCODING, PROCESSING, PULSE, SERVICE). Match is
// case-insensitive. Returns a non-nil empty slice when nothing
// matches; results are sorted alphabetically by code.
func (p *Pulse) ErrorsByDomain(domain string) []ErrorMetadata {
	return errors.ByDomain(domain)
}

// ErrorsSearch returns codes whose Message or Fixup hints contain the
// query (case-insensitive substring). Results are ranked by match
// source: description hits before fixup hits before code-name hits;
// ties resolve alphabetically. Returns a non-nil empty slice when
// nothing matches.
func (p *Pulse) ErrorsSearch(query string) []ErrorMetadata {
	return errors.Search(query)
}

// Manifest returns the root Pulse self-description. The manifest is
// deterministic and process-wide: it does not depend on cohort data or
// the filesystem. Callers cache the result for a session.
func (p *Pulse) Manifest(_ context.Context) *descriptor.Manifest {
	return descriptor.BuildManifest()
}

// Fs returns the underlying afero.Fs. Embedders (e.g. the MCP server) need
// this to enumerate .pulse files; processing methods route through service
// and never expose the filesystem directly.
func (p *Pulse) Fs() afero.Fs {
	return p.fsys
}

// resolveCohortPath builds the file path from a Cohort specification.
func resolveCohortPath(c *types.Cohort) string {
	if c.DataDir != "" {
		return c.DataDir + "/" + c.Filename
	}
	return c.Filename
}

// Cohort represents an opened .pulse file with its parsed schema.
// It wraps the service-layer Cohort to provide a clean public API.
type Cohort struct {
	inner *service.Cohort
}

// Schema returns the cohort's schema.
func (c *Cohort) Schema() *encoding.Schema {
	return c.inner.Schema()
}

// Field returns a pointer to the named field, or nil if not found.
func (c *Cohort) Field(name string) *encoding.Field {
	return c.inner.Schema().Field(name)
}

// Categorical returns the dictionary for a named categorical field.
// Returns nil, false if the field is not found or is not categorical.
func (c *Cohort) Categorical(name string) (*encoding.Dictionary, bool) {
	return c.inner.Schema().Categorical(name)
}
