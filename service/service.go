package service

import (
	"bytes"
	"context"
	"fmt"
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
