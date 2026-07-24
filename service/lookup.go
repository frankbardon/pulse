package service

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Lookup resolves a point lookup against the cohort named in
// req.Cohort, keyed on the ordered tuple req.KeyComponents() returns
// (either req.Keys verbatim, for a composite/multi-column key, or the
// req.Field/req.Value single-key convenience path — see
// types.LookupRequest.KeyComponents): derive the sidecar index path for
// the ordered key-field names (the same derivation Service.BuildIndex
// used to write it), load the sidecar, validate the request's
// key-component count matches the loaded index's key-spec, resolve
// every component's literal to on-wire key bytes and concatenate them
// in order, probe the hash bucket for a byte-equal entry, and — on a
// hit — read the matching record via encoding.RecordLocator.ReadRecordAt
// with a single reused DecodePlan projected to req.ReturnColumns.
//
// Key column order is significant: KeyComponents in a different order
// derives a different sidecar path and a different composite key byte
// layout, even when the same set of columns/values is supplied.
//
// Multiplicity: when the matched bucket entry carries more than one
// row-id (a duplicate key value in the source cohort), req.Multiplicity
// selects the behavior — see types.LookupMultiplicity's doc comment.
// The zero value (unset) and LookupMultiplicityAssertUnique both mean
// "assert unique": Lookup fails with PULSE_LOOKUP_AMBIGUOUS when more
// than one row-id matches. LookupMultiplicityFirst takes the lowest
// row-id deterministically; LookupMultiplicityAll returns every
// matching row. Row order (both "first"'s pick and "all"'s slice
// order) is always ascending row-id — the sidecar bucket's RowIDs slice
// is not itself guaranteed sorted, so Lookup sorts a copy before use.
//
// Shard archive cohorts (detected via the cheap leading-magic-bytes
// dispatch Service.Open already performs) are rejected with
// PULSE_INDEX_UNSUPPORTED_SHARDED — sharded point-lookup is out of
// scope for v1. See Service.BuildIndex's doc comment for why.
func (s *Service) Lookup(ctx context.Context, req *types.LookupRequest) (*types.LookupResult, error) {
	if req == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "lookup requires a request")
	}
	if req.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "lookup requires a cohort")
	}
	components := req.KeyComponents()
	if len(components) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"lookup requires at least one key field (Field or Keys)")
	}
	keyFieldNames := make([]string, len(components))
	for i, c := range components {
		if c.Field == "" {
			return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "lookup requires a non-empty field")
		}
		keyFieldNames[i] = c.Field
	}

	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(cohort.Shards()) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_UNSUPPORTED_SHARDED,
			"point-lookup does not support shard archive cohorts",
			map[string]any{"cohort": path})
	}
	schema := cohort.Schema()

	keyFields := make([]*encoding.Field, len(components))
	for i, name := range keyFieldNames {
		f := schema.Field(name)
		if f == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"lookup key field not found in schema",
				map[string]any{"field": name})
		}
		keyFields[i] = f
	}

	returnCols, err := resolveLookupReturnColumns(schema, req.ReturnColumns)
	if err != nil {
		return nil, err
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	indexPath := encoding.SidecarIndexPath(path, keyFieldNames)
	exists, err := afero.Exists(fsys, indexPath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("checking sidecar index existence: %s", indexPath))
	}
	if !exists {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MISSING,
			"no sidecar point-lookup index found for these key fields",
			map[string]any{"cohort": path, "fields": keyFieldNames, "index_path": indexPath})
	}

	idx, err := encoding.ReadIndexFile(fsys, indexPath)
	if err != nil {
		return nil, err
	}

	// Read-path staleness check: a CHEAP size+mtime stat comparison, not
	// a full content hash. Hashing the whole .pulse on every Lookup would
	// defeat the point of an O(1) point lookup (multi-second per call on a
	// multi-GB cohort). The stat check catches the common mutation cases
	// — a re-import/re-export/rewrite changes the file size and/or mtime —
	// at effectively zero cost. The intentional residual gap is an
	// in-place edit that preserves BOTH size and mtime: Lookup will not
	// catch that (by design), but `pulse index verify` / Service.VerifyIndex
	// is the authoritative content check and recomputes the full SHA-256
	// fingerprint to confirm freshness conclusively.
	currentSize, currentModTime, err := statCohortFile(fsys, path)
	if err != nil {
		return nil, err
	}
	if currentSize != idx.SourceSize || currentModTime != idx.SourceModTime {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_STALE,
			"sidecar point-lookup index is stale: the cohort file's size or modification time no longer matches the snapshot taken at index build",
			map[string]any{"cohort": path, "fields": keyFieldNames, "index_path": indexPath})
	}

	if len(idx.Keys) != len(components) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"lookup key component count does not match the sidecar index's key-spec",
			map[string]any{"cohort": path, "requested": len(components), "index_key_count": len(idx.Keys)})
	}

	literals := make([]string, len(components))
	for i, c := range components {
		literals[i] = c.Value
	}
	keyBytes, err := processing.ResolveCompositeLookupKeyBytes(keyFields, literals)
	if err != nil {
		return nil, err
	}

	entry, ok := findIndexEntry(idx, keyBytes)
	if !ok || len(entry.RowIDs) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_NOT_FOUND,
			"no record matches the requested key",
			map[string]any{"cohort": path, "fields": keyFieldNames, "values": literals})
	}

	// Stable order = ascending row-id. entry.RowIDs is not guaranteed
	// sorted by the writer side, so sort a copy before applying the
	// multiplicity mode.
	rowIDs := append([]uint64(nil), entry.RowIDs...)
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })

	mode := req.Multiplicity
	if mode == "" {
		mode = types.LookupMultiplicityAssertUnique
	}
	switch mode {
	case types.LookupMultiplicityAssertUnique:
		if len(rowIDs) > 1 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_AMBIGUOUS,
				"key matched more than one row under assert-unique multiplicity",
				map[string]any{"cohort": path, "fields": keyFieldNames, "values": literals, "match_count": len(rowIDs)})
		}
		rowIDs = rowIDs[:1]
	case types.LookupMultiplicityFirst:
		rowIDs = rowIDs[:1]
	case types.LookupMultiplicityAll:
		// keep every matching row-id, ascending order already applied.
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"unknown lookup multiplicity mode",
			map[string]any{"multiplicity": string(mode)})
	}

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

	rows := make([]map[string]any, 0, len(rowIDs))
	for _, rowID := range rowIDs {
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
		rows = append(rows, row)
	}

	return &types.LookupResult{Rows: rows}, nil
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
