package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// FilterToFile reads the .pulse cohort at src, evaluates filterExpr
// against every record using FILTER_EXPRESSION semantics, and writes a
// new .pulse cohort at dst containing only records that pass. Custom
// expr functions and lookup tables registered via
// pulse.Options.Extensions are honored.
//
// Three input shapes are supported, dispatched on the first four bytes:
//
//   - Single-file cohort (magic "PULSE\x00\x00\x00"): the dst is a
//     single-file cohort whose header + schema bytes are copied
//     byte-for-byte from src; only the record payload differs.
//
//   - Shard archive (magic "PK\x03\x04"): the dst is a new shard
//     archive preserving the per-shard layout — one input shard maps
//     to one output shard at the same basename, in central-directory
//     (insertion) order. Per-shard header + schema + categorical
//     dictionary bytes are copied byte-for-byte (records inside each
//     shard reference that shard's local dictionary, which is preserved
//     under the prefix-rule invariant). Shards with zero surviving
//     records are written as empty payloads (header + schema + zero
//     record bytes). The canonical `_schema.pulse` trailer is
//     refreshed: aggregate_record_count = sum of surviving rows across
//     shards, shard_count = unchanged.
//
//   - Anchor syntax `archive.pulse#shard.pulse`: resolves to a single
//     shard inside an archive; dst is a single-file cohort built from
//     that shard alone.
//
// Returns the number of records written to dst (sum across shards for
// archive inputs).
func (s *Service) FilterToFile(ctx context.Context, src, dst, filterExpr string) (int64, error) {
	if filterExpr == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty filter expression")
	}
	return s.filterToFile(ctx, src, dst, filterPlan{filterExpr: filterExpr})
}

// FilterToFileBySetAndExpr applies an include-set predicate (membership
// test against an externally-supplied list of values) and optionally a
// filter expression to every record in src, writing the survivors to
// dst. At least one of (set, filterExpr) must be supplied — if both,
// the set is tested first and the expression evaluated only on
// surviving rows (cheap-check-first short-circuit).
//
// Input-shape dispatch matches FilterToFile (single-file, shard
// archive, anchor). includeField names the field whose value is tested
// for membership in set; the set must have been built against the same
// canonical schema as src (the field is resolved per-call to keep the
// predicate schema-coherent across shards).
//
// Returns the number of records written to dst.
func (s *Service) FilterToFileBySetAndExpr(ctx context.Context, src, dst, includeField string, set processing.MemberSet, filterExpr string) (int64, error) {
	if set == nil && filterExpr == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires at least one of include-set or filter expression")
	}
	if set != nil && includeField == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file include-set requires a non-empty include field name")
	}
	return s.filterToFile(ctx, src, dst, filterPlan{
		set:          set,
		includeField: includeField,
		filterExpr:   filterExpr,
	})
}

// filterPlan captures the per-call predicate inputs so the input-shape
// dispatch (single-file, shard archive, anchor) doesn't have to take a
// growing list of optional arguments.
type filterPlan struct {
	set          processing.MemberSet
	includeField string
	filterExpr   string
}

// ResolveCanonicalSchema reads the canonical schema for src without
// streaming the record payload. Single-file cohorts return their own
// schema; shard archives return the canonical `_schema.pulse` schema;
// anchored shards return the anchored shard's local schema. Used by
// callers (notably the CLI cohort-filter include-from path) that need
// schema introspection to build an include-set before invoking
// FilterToFileBySetAndExpr.
func (s *Service) ResolveCanonicalSchema(_ context.Context, src string) (*encoding.Schema, error) {
	if src == "" {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"ResolveCanonicalSchema requires a non-empty path")
	}
	fsys := s.fs.Fs()

	if archivePath, anchor, ok := splitAnchorPath(src); ok {
		payload, err := s.readAnchoredShardBytes(archivePath, anchor)
		if err != nil {
			return nil, err
		}
		return readSchemaFromBytes(payload)
	}

	data, err := afero.ReadFile(fsys, src)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", src))
	}

	if isShardArchiveMagic(data) {
		arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		doc, err := readSchemaDocEntry(arch)
		if err != nil {
			return nil, err
		}
		return doc.Schema, nil
	}

	return readSchemaFromBytes(data)
}

func readSchemaFromBytes(data []byte) (*encoding.Schema, error) {
	br := bytes.NewReader(data)
	if err := encoding.ReadHeader(br); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"invalid pulse file header")
	}
	schema, err := encoding.ReadSchema(br)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading schema")
	}
	return schema, nil
}

func (s *Service) filterToFile(ctx context.Context, src, dst string, plan filterPlan) (int64, error) {
	if src == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty input path")
	}
	if dst == "" {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty output path")
	}

	fsys := s.fs.Fs()

	// Anchor syntax: archive#shard → single-shard payload → single-file
	// output. Reuses service.OpenAnchor's archive resolution path.
	if archivePath, anchor, ok := splitAnchorPath(src); ok {
		payload, err := s.readAnchoredShardBytes(archivePath, anchor)
		if err != nil {
			return 0, err
		}
		return s.filterSingleFileBytesToFile(ctx, fsys, payload, dst, plan)
	}

	data, err := afero.ReadFile(fsys, src)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", src))
	}

	if isShardArchiveMagic(data) {
		return s.filterShardArchiveToFile(ctx, fsys, dst, plan, data)
	}

	return s.filterSingleFileBytesToFile(ctx, fsys, data, dst, plan)
}

// filterSingleFileBytesToFile runs the single-file filter loop over an
// in-memory byte slice and writes the result to dst. Used both for
// direct single-file inputs and for anchor-syntax inputs (where the
// anchored shard's bytes form a complete standalone payload).
func (s *Service) filterSingleFileBytesToFile(ctx context.Context, fsys afero.Fs, data []byte, dst string, plan filterPlan) (int64, error) {
	br := bytes.NewReader(data)
	if err := encoding.ReadHeader(br); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"invalid pulse file header")
	}
	schema, err := encoding.ReadSchema(br)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading schema")
	}
	headerSchemaEnd := int64(len(data)) - int64(br.Len())

	filterFn, err := s.buildCombinedFilter(schema, plan)
	if err != nil {
		return 0, err
	}

	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(data[:headerSchemaEnd])

	written, err := s.streamFilterRecords(ctx, data, br, schema, filterFn, out)
	if err != nil {
		return 0, err
	}

	if err := afero.WriteFile(fsys, dst, out.Bytes(), 0644); err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing filtered cohort to: %s", dst))
	}
	return written, nil
}

// filterShardArchiveToFile applies the filter to every shard in the
// source archive and writes a new shard archive to dst. One input shard
// → one output shard, basename preserved, central-directory order
// preserved. Empty shards (zero surviving records) are kept so the
// shard_count metadata stays stable across the filter.
func (s *Service) filterShardArchiveToFile(ctx context.Context, fsys afero.Fs, dst string, plan filterPlan, data []byte) (int64, error) {
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}

	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return 0, err
	}
	canonicalSchema := canonicalDoc.Schema

	filterFn, err := s.buildCombinedFilter(canonicalSchema, plan)
	if err != nil {
		return 0, err
	}

	// Per-shard filter + accumulate output payloads.
	type filteredShard struct {
		name    string
		payload []byte
		written int64
	}
	entries := arch.Entries()
	out := make([]filteredShard, 0, len(entries))
	var totalWritten int64
	for _, e := range entries {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}
		shardBytes, perr := readEntryBytes(arch, e.Name)
		if perr != nil {
			return 0, perr
		}

		// Parse this shard's local header + schema so we can copy those
		// bytes verbatim into the output payload. Records are decoded
		// against the canonical schema so filter expressions see a
		// consistent dictionary across shards, but local schema bytes
		// (with the local dictionary subset) stay on the output shard.
		// The cohesion invariant guarantees record layout matches.
		br := bytes.NewReader(shardBytes)
		if err := encoding.ReadHeader(br); err != nil {
			return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
				fmt.Sprintf("reading shard %q header", e.Name))
		}
		if _, err := encoding.ReadSchema(br); err != nil {
			return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
				fmt.Sprintf("reading shard %q schema", e.Name))
		}
		localHeaderSchemaEnd := int64(len(shardBytes)) - int64(br.Len())

		buf := bytes.NewBuffer(make([]byte, 0, len(shardBytes)))
		buf.Write(shardBytes[:localHeaderSchemaEnd])
		written, ferr := s.streamFilterRecords(ctx, shardBytes, br, canonicalSchema, filterFn, buf)
		if ferr != nil {
			return 0, ferr
		}
		out = append(out, filteredShard{name: e.Name, payload: buf.Bytes(), written: written})
		totalWritten += written
	}

	// Rebuild the archive: refresh _schema.pulse trailer (aggregate +
	// shard_count) then emit each filtered shard payload.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	schemaPayload, err := buildSchemaDocPayload(canonicalSchema, uint64(totalWritten), uint16(len(out)))
	if err != nil {
		return 0, err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return 0, err
	}
	for _, sh := range out {
		if err := writeArchiveEntry(zw, sh.name, sh.payload); err != nil {
			return 0, err
		}
	}
	if err := zw.Close(); err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"FilterToFile: finalising shard archive zip writer")
	}

	if err := atomicWrite(fsys, dst, buf.Bytes()); err != nil {
		return 0, err
	}
	return totalWritten, nil
}

// streamFilterRecords iterates records from br (positioned after
// header+schema), applies filterFn against records decoded with schema,
// and writes raw record-byte ranges from data into out for kept rows.
// Returns the number of records written.
func (s *Service) streamFilterRecords(ctx context.Context, data []byte, br *bytes.Reader, schema *encoding.Schema, filterFn processing.FilterFunc, out *bytes.Buffer) (int64, error) {
	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool, len(schema.Fields))
	wide := make(map[string]any, len(schema.Fields))
	rr := encoding.NewRecordReader(br, schema)

	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		posStart := int64(len(data)) - int64(br.Len())
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		posEnd := int64(len(data)) - int64(br.Len())

		rec := processing.NewRecordWithWide(schema, values, nulls, wide)
		keep, ferr := filterFn(rec)
		if ferr != nil {
			return 0, ferr
		}
		if !keep {
			continue
		}
		out.Write(data[posStart:posEnd])
		written++
	}
	return written, nil
}

// buildSingleFilter resolves filterExpr into a single FilterFunc bound
// to schema. Extensions (expr functions + lookup tables) registered on
// the Service are honored.
func (s *Service) buildSingleFilter(schema *encoding.Schema, filterExpr string) (processing.FilterFunc, error) {
	fns, err := processing.BuildFilters(
		[]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: filterExpr}},
		schema,
		s.extensions,
	)
	if err != nil {
		return nil, err
	}
	return fns[0], nil
}

// buildCombinedFilter composes an optional MemberSet membership test
// with an optional FILTER_EXPRESSION into one FilterFunc. The set
// predicate runs first when both are present, so per-row work
// short-circuits on misses without paying the expr eval cost.
func (s *Service) buildCombinedFilter(schema *encoding.Schema, plan filterPlan) (processing.FilterFunc, error) {
	var setFn processing.FilterFunc
	if plan.set != nil {
		fn, err := processing.BuildMemberSetPredicate(plan.set, schema, plan.includeField)
		if err != nil {
			return nil, err
		}
		setFn = fn
	}

	var exprFn processing.FilterFunc
	if plan.filterExpr != "" {
		fn, err := s.buildSingleFilter(schema, plan.filterExpr)
		if err != nil {
			return nil, err
		}
		exprFn = fn
	}

	switch {
	case setFn != nil && exprFn != nil:
		return func(rec *processing.Record) (bool, error) {
			ok, err := setFn(rec)
			if err != nil || !ok {
				return false, err
			}
			return exprFn(rec)
		}, nil
	case setFn != nil:
		return setFn, nil
	case exprFn != nil:
		return exprFn, nil
	default:
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires at least one of include-set or filter expression")
	}
}

// readAnchoredShardBytes resolves an anchor (archivePath, entry) to the
// shard's standalone single-file `.pulse` payload bytes. Used by
// FilterToFile to support `archive.pulse#shard.pulse` syntax with the
// same semantics as service.OpenAnchor.
func (s *Service) readAnchoredShardBytes(archivePath, entry string) ([]byte, error) {
	if entry == encoding.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot filter the reserved canonical schema entry",
			map[string]any{"entry": entry})
	}
	data, err := afero.ReadFile(s.fs.Fs(), archivePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file for anchor: %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	rc, err := arch.Open(entry)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	payload, err := io.ReadAll(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading anchored shard %q from %s", entry, archivePath))
	}
	return payload, nil
}

// isShardArchiveMagic reports whether data begins with the zip magic
// "PK\x03\x04". Mirrors the dispatch check in service.Open.
func isShardArchiveMagic(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}
