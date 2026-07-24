package service

import (
	"bytes"
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Lookup resolves a single-key point lookup against the cohort named in
// req.Cohort: derive the sidecar index path for req.Field (the same
// derivation Service.BuildIndex used to write it), load the sidecar,
// resolve req.Value to on-wire key bytes, probe the hash bucket for a
// byte-equal entry, and — on a hit — read the matching record via
// encoding.RecordLocator.ReadRecordAt with a single reused DecodePlan
// projected to req.ReturnColumns.
//
// Multiplicity: when the matched bucket entry carries more than one
// row-id (a duplicate key value in the source cohort), Lookup always
// takes RowIDs[0] — see types.LookupMultiplicity's doc comment. The
// full assert-unique / first / all mode matrix is E2-S2's scope.
func (s *Service) Lookup(ctx context.Context, req *types.LookupRequest) (*types.LookupResult, error) {
	if req == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "lookup requires a request")
	}
	if req.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "lookup requires a cohort")
	}
	if req.Field == "" {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "lookup requires a non-empty field")
	}

	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	schema := cohort.Schema()

	keyField := schema.Field(req.Field)
	if keyField == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"lookup key field not found in schema",
			map[string]any{"field": req.Field})
	}

	returnCols, err := resolveLookupReturnColumns(schema, req.ReturnColumns)
	if err != nil {
		return nil, err
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	indexPath := encoding.SidecarIndexPath(path, []string{req.Field})
	exists, err := afero.Exists(fsys, indexPath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("checking sidecar index existence: %s", indexPath))
	}
	if !exists {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MISSING,
			"no sidecar point-lookup index found for this field",
			map[string]any{"cohort": path, "field": req.Field, "index_path": indexPath})
	}

	idx, err := encoding.ReadIndexFile(fsys, indexPath)
	if err != nil {
		return nil, err
	}

	keyBytes, err := processing.ResolveLookupKeyBytes(keyField, req.Value)
	if err != nil {
		return nil, err
	}

	entry, ok := findIndexEntry(idx, keyBytes)
	if !ok || len(entry.RowIDs) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_NOT_FOUND,
			"no record matches the requested key",
			map[string]any{"cohort": path, "field": req.Field, "value": req.Value})
	}
	// v1: always take the first row-id — see types.LookupMultiplicity.
	rowID := entry.RowIDs[0]

	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading cohort file for lookup: %s", path))
	}
	reader := bytes.NewReader(raw)
	loc, err := encoding.NewRecordLocator(reader, schema)
	if err != nil {
		return nil, err
	}

	plan, err := schema.BuildDecodePlan(returnCols)
	if err != nil {
		return nil, err
	}

	wantCols := make(map[string]bool, len(returnCols))
	for _, name := range returnCols {
		wantCols[name] = true
	}
	keep := encoding.FieldFilter(func(name string) bool { return wantCols[name] })

	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	if err := loc.ReadRecordAt(reader, rowID, values, nulls, wide, keep, plan); err != nil {
		return nil, err
	}

	rec := processing.NewRecordWithWide(schema, values, nulls, wide)
	all := rec.AllValues()
	row := make(map[string]any, len(returnCols))
	for _, name := range returnCols {
		if v, present := all[name]; present {
			row[name] = v
		}
	}

	return &types.LookupResult{Rows: []map[string]any{row}}, nil
}

// resolveLookupReturnColumns validates req.ReturnColumns against schema
// (every name must resolve to a real field) and defaults to every
// schema field, in declaration order, when the request supplied none —
// matching Sample's no-projection default.
func resolveLookupReturnColumns(schema *encoding.Schema, requested []string) ([]string, error) {
	if len(requested) == 0 {
		out := make([]string, len(schema.Fields))
		for i := range schema.Fields {
			out[i] = schema.Fields[i].Name
		}
		return out, nil
	}
	for _, name := range requested {
		if schema.Field(name) == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"lookup return column not found in schema",
				map[string]any{"field": name})
		}
	}
	out := make([]string, len(requested))
	copy(out, requested)
	return out, nil
}

// findIndexEntry probes idx's hash bucket for keyBytes and scans the
// bucket's entries for a byte-equal Key — mirrors the read-side half of
// the encoding.BucketIndex / encoding.HashKey contract that
// Service.BuildIndex's write side already exercises. Returns
// (nil-entry, false) when idx has zero buckets (empty index) or no
// entry in the resolved bucket matches keyBytes exactly.
func findIndexEntry(idx *encoding.Index, keyBytes []byte) (encoding.IndexEntry, bool) {
	if len(idx.Buckets) == 0 {
		return encoding.IndexEntry{}, false
	}
	bi := encoding.BucketIndex(keyBytes, uint32(len(idx.Buckets)))
	for _, e := range idx.Buckets[bi].Entries {
		if bytes.Equal(e.Key, keyBytes) {
			return e, true
		}
	}
	return encoding.IndexEntry{}, false
}
