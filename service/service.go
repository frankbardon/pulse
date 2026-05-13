package service

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Service is the orchestration layer connecting filesystem, encoding, and processing.
type Service struct {
	fs              *fs.Config
	disableDefaults bool
}

// New creates a new Service with the given filesystem configuration.
// Smart-defaults resolution runs by default; call DisableDefaults to opt
// out per instance (or pass pulse.Options{DisableDefaults: true} via the
// facade).
func New(fsConfig *fs.Config) *Service {
	return &Service{fs: fsConfig}
}

// SetDisableDefaults toggles the smart-defaults pass. Predict still
// computes and reports DefaultsApplied independently; this flag governs
// only what the runtime mutates before Process / Compose execution.
func (s *Service) SetDisableDefaults(disabled bool) {
	s.disableDefaults = disabled
}

// applyDefaults runs descriptor.ResolveDefaults against the cohort schema
// unless defaults are disabled on this Service. Mutates req in place.
func (s *Service) applyDefaults(req *types.Request, schema *encoding.Schema) {
	if s.disableDefaults || req == nil || schema == nil {
		return
	}
	descriptor.ResolveDefaults(req, schema)
}

// Open reads a .pulse file and returns a Cohort with the parsed schema.
func (s *Service) Open(_ context.Context, path string) (*Cohort, error) {
	data, err := afero.ReadFile(s.fs.Fs(), path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", path))
	}

	r := bytes.NewReader(data)

	if err := encoding.ReadHeader(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("invalid pulse file: %s", path))
	}

	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading schema from: %s", path))
	}

	return &Cohort{
		path:   path,
		schema: schema,
		fs:     s.fs.Fs(),
	}, nil
}

// Process executes a single request against the specified cohort.
// Records are streamed from disk — the full file is never held in memory as raw bytes
// alongside the decoded records.
func (s *Service) Process(ctx context.Context, req *types.Request) (*types.Response, error) {
	if req.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "request cohort is required")
	}

	path := resolveCohortPath(req.Cohort)

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	// Smart-defaults resolution: fill in operator types that the caller
	// omitted, based on each named field's schema type. Caller can opt
	// out via SetDisableDefaults / pulse.Options{DisableDefaults: true}.
	s.applyDefaults(req, cohort.Schema())

	iter := newStreamingIterator(s.fs.Fs(), path, cohort.Schema())
	defer iter.Close()

	proc := processing.NewProcessor(cohort.Schema())
	resp, err := proc.Process(ctx, req, iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}

	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}

	return resp, nil
}

// AskInput captures the per-call options the facade hands the service.
// Mirrors the public pulse.AskRequest shape but reads the cohort path
// from the embedded request's Cohort so the service does not duplicate
// the path-resolution dance.
type AskInput struct {
	// Request is the structured processing request. Required.
	Request *types.Request

	// OnInvalid controls behavior when predict reports the request as
	// invalid. "" / "abort" returns a SERVICE_VALIDATION error; "suggest"
	// returns a successful AskOutput with structured Suggestions populated.
	OnInvalid string

	// PredictOnly skips Process even when predict succeeds. Predict
	// always runs; the caller can read schema info, streamability,
	// suggestions, and defaults_applied from PredictResult.
	PredictOnly bool
}

// AskOutput is the service-level envelope returned by Service.Ask.
// The facade re-marshals into the public pulse.AskResponse — see pulse.go.
type AskOutput struct {
	// Predict is always populated when Ask returns without error.
	Predict *descriptor.PredictResult

	// Process is the execution result. Nil when PredictOnly=true,
	// nil when predict reported the request invalid (OnInvalid="suggest").
	Process *types.Response

	// Suggestions enumerates structured Fixup entries derived from every
	// predict error code's metadata template, de-duplicated by
	// (Code, Action). Empty when there were no errors or when
	// OnInvalid != "suggest".
	Suggestions []errors.Fixup

	// Errors / Warnings echo the predict envelope's entries so callers
	// can present them without re-running predict. Never nil — empty slices
	// when there are none.
	Errors   []*descriptor.EnvelopeEntry
	Warnings []*descriptor.EnvelopeEntry
}

// Ask is the unified pipeline that collapses inspect → predict → process
// into one call. It opens the cohort file, validates the request against
// the schema via Predict, and on success runs Process. The exposed
// pulse.Ask facade is a thin shim over this method.
//
// OnInvalid governs predict-invalid behavior:
//   - "" or "abort" — return a SERVICE_VALIDATION error wrapping the predict envelope.
//   - "suggest"     — return AskOutput with Suggestions populated; Process stays nil.
//
// PredictOnly skips execution even on a valid request (the caller's
// "what would happen if I ran this?" probe).
func (s *Service) Ask(ctx context.Context, in AskInput) (*AskOutput, error) {
	if in.Request == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "ask requires a request")
	}
	if in.Request.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "ask requires a cohort")
	}

	mode := in.OnInvalid
	if mode == "" {
		mode = "abort"
	}
	if mode != "abort" && mode != "suggest" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"ask: on_invalid must be \"abort\" or \"suggest\"",
			map[string]any{"on_invalid": in.OnInvalid},
		)
	}

	path := resolveCohortPath(in.Request.Cohort)

	data, err := afero.ReadFile(s.fs.Fs(), path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("ask: reading cohort file: %s", path))
	}

	env := descriptor.PredictFromBytes(data, in.Request, nil)
	result, _ := env.Data.(*descriptor.PredictResult)
	if result == nil {
		return nil, errors.NewCodedError(errors.SERVICE_INTERNAL,
			"ask: predict returned no result")
	}

	out := &AskOutput{
		Predict:  result,
		Errors:   normalizedEntries(env.Errors),
		Warnings: normalizedEntries(env.Warnings),
	}

	if !result.Valid {
		if mode == "abort" {
			return nil, errors.NewCodedErrorWithDetails(
				errors.SERVICE_VALIDATION,
				"ask: request failed predict validation",
				map[string]any{
					"errors":   out.Errors,
					"warnings": out.Warnings,
				},
			)
		}
		out.Suggestions = materializeFixups(out.Errors, in.Request)
		return out, nil
	}

	if in.PredictOnly {
		return out, nil
	}

	resp, err := s.Process(ctx, in.Request)
	if err != nil {
		return nil, err
	}
	out.Process = resp
	return out, nil
}

// normalizedEntries returns a non-nil slice so JSON serializers emit
// `[]` rather than `null` for callers that consume the envelope shape.
func normalizedEntries(in []*descriptor.EnvelopeEntry) []*descriptor.EnvelopeEntry {
	if in == nil {
		return []*descriptor.EnvelopeEntry{}
	}
	return in
}

// materializeFixups walks the predict envelope's errors, looks up each
// code's structured fixup templates via errors.MetadataFor, and returns
// a de-duplicated slice sorted by (Code, Action, Hint) for stable output.
// Entries whose code is FixupNotApplicable contribute nothing.
func materializeFixups(entries []*descriptor.EnvelopeEntry, req *types.Request) []errors.Fixup {
	type key struct {
		code   string
		action errors.FixupAction
		hint   string
	}
	seen := make(map[key]struct{})
	var out []errors.Fixup
	for _, e := range entries {
		if e == nil {
			continue
		}
		fixups := errors.Code(e.Code).Fixup(req)
		for _, f := range fixups {
			k := key{code: e.Code, action: f.Action, hint: f.Hint}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		return out[i].Hint < out[j].Hint
	})
	return out
}

// Compose executes multiple requests, returning a response for each.
func (s *Service) Compose(ctx context.Context, composed *types.ComposedRequest) ([]*types.Response, error) {
	if composed == nil || len(composed.Requests) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}

	responses := make([]*types.Response, len(composed.Requests))
	for i, req := range composed.Requests {
		resp, err := s.Process(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		responses[i] = resp
	}

	return responses, nil
}

// Sample returns up to n rows from the cohort as maps of field name to value.
// Streams from disk — stops reading as soon as n rows are collected.
func (s *Service) Sample(ctx context.Context, path string, n int) ([]map[string]any, error) {
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	iter := newStreamingIterator(s.fs.Fs(), path, cohort.Schema())
	defer iter.Close()

	var rows []map[string]any
	for iter.Next() && len(rows) < n {
		rows = append(rows, iter.Record().AllValues())
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}

	return rows, nil
}

// Facet returns distinct values for the named field in the cohort.
// For categorical fields, it returns the dictionary values.
// For numeric fields, it returns string representations of all distinct values seen.
func (s *Service) Facet(ctx context.Context, path string, field string) ([]string, error) {
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	f := cohort.Schema().Field(field)
	if f == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q not found in schema", field))
	}

	// For categorical fields, return dictionary values directly
	if f.Type.IsCategorical() && f.Dictionary != nil {
		return f.Dictionary.Values(), nil
	}

	// For numeric fields, stream records and collect distinct values.
	iter := newStreamingIterator(s.fs.Fs(), path, cohort.Schema())
	defer iter.Close()

	seen := make(map[float64]struct{})
	var values []string
	for iter.Next() {
		v, ok := iter.Record().NumericValue(field)
		if !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		values = append(values, strconv.FormatFloat(v, 'f', -1, 64))
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}

	return values, nil
}

// resolveCohortPath builds the file path from a Cohort specification.
func resolveCohortPath(c *types.Cohort) string {
	if c.DataDir != "" {
		return c.DataDir + "/" + c.Filename
	}
	return c.Filename
}
