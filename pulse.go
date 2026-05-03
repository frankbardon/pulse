// Package pulse is a high-performance, self-describing tabular data processing engine.
//
// Pulse ships as a CLI binary and as an embeddable Go library.
// The library is the primary deliverable; the CLI is a thin adapter over it.
package pulse

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
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
}

// Pulse is the top-level library facade. It wraps the service layer and
// provides a clean API for embedding Pulse into Go programs.
type Pulse struct {
	svc  *service.Service
	fsys afero.Fs
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

	return &Pulse{
		svc:  service.New(fsCfg),
		fsys: fsCfg.Fs(),
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
	return p.svc.Process(ctx, req)
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

// Inspect reads a .pulse file header and schema, returning structured field information.
// It never reads record data.
func (p *Pulse) Inspect(_ context.Context, path string) (*descriptor.InspectResult, error) {
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
	return result, nil
}

// Predict validates a request against a .pulse file without executing it.
// It reads only the header and schema, never record data.
func (p *Pulse) Predict(_ context.Context, req *Request) (*descriptor.PredictResult, error) {
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
			return result, nil
		}
		return nil, fmt.Errorf("pulse: predict: %s", env.Errors[0].Message)
	}

	result, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		return nil, fmt.Errorf("pulse: predict returned unexpected type")
	}
	return result, nil
}

// Sample returns up to n rows from the cohort as maps of field name to value.
func (p *Pulse) Sample(ctx context.Context, path string, n int) ([]Record, error) {
	return p.svc.Sample(ctx, path, n)
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
	return p.svc.Facet(ctx, path, field)
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
