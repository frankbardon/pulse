package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
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
//     A grouped (0x02) source therefore yields a 0x02 destination
//     carrying the source's group descriptors and dictionaries
//     UNCHANGED — surviving physical rows are copied by reader
//     position, so every row's entry index stays valid. Entries no
//     surviving row references are carried, not pruned (a deliberate
//     v1 choice: pruning costs a pass and renumbers indices); inspect's
//     ratio is then surviving rows / carried entries. Re-deduping the
//     output with the same groups prunes. A filter that keeps nothing
//     writes the preamble alone: a valid, empty 0x02 cohort. Records
//     are handed their row's entry indices (RecordReader.GroupIndex),
//     so a filter over one group's members takes the per-entry
//     precompute.
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

// FilterToFileFilterers is FilterToFile for the facade's
// FilterToFileRequest: filterExpr is the request's predicate (its
// Expression, or the expression its translatable structured filterers
// translate to — FilterToFileRequest.canonicalPredicate) and direct the
// structured filterers it does not translate (an extension filterer,
// FILTER_TRUE / FILTER_FALSE, FILTER_SET_*, the zone-capable
// FILTER_DATE_RANGES), built through the engine
// filterer registry exactly as Process builds them; a row is kept only
// when filterExpr (if non-empty) and every direct filterer pass. At
// least one of the two must be supplied. Non-empty filterers are
// judged by the shared field rule
// (internal/descriptor.FieldRefRefusals, filterer half) against the
// cohort schema — the canonical one for a shard archive — before the
// expression compiles, so an unknown or empty Field is refused with the
// code, message and details Process and predict use rather than an
// expression compile error. timeZone is the request zone the
// expression's to_day() reads (Request.TimeZone's rule; "" inherits
// Options.DefaultTimeZone, else UTC).
func (s *Service) FilterToFileFilterers(ctx context.Context, src, dst string, filterers []*types.Filterer, filterExpr string, direct []*types.Filterer, timeZone string) (int64, error) {
	if filterExpr == "" && len(direct) == 0 {
		return 0, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty filter expression")
	}
	return s.filterToFile(ctx, src, dst, filterPlan{filterExpr: filterExpr, filterers: filterers, direct: direct, timeZone: timeZone})
}

// FilterToFileZones resolves the zones a FilterToFileRequest's
// structured filterers read — the one zone-resolution pass Process runs
// (resolveZones), over a Request{Filterers, TimeZone} against src's
// canonical schema (an anchored shard's own) — and returns the applied
// ones as the dedup-hash key (internal/descriptor.AppliedZones). The
// facade calls it before hashing or any reuse, so a zone refusal is
// Process's own code, message and details with the request's slot
// indices: an explicit `tz` / `tz_field` on an operator that is not
// zone-capable (an extension filterer included), a `tz` on a `date`
// field, an unknown zone. With no zone-capable filterer and no `tz` /
// `tz_field`, nothing can resolve: src is not read and the result is
// nil. The run re-resolves the same zones per schema
// (buildPredicateFilter), so the key and the kept rows agree.
func (s *Service) FilterToFileZones(ctx context.Context, src string, filterers []*types.Filterer, timeZone string) ([]descx.AppliedZone, error) {
	needs := false
	for _, f := range filterers {
		if f != nil && (f.TimeZone != "" || f.TimeZoneField != "" || descx.IsZoneCapable(string(f.Type))) {
			needs = true
			break
		}
	}
	if !needs {
		return nil, nil
	}
	schema, err := s.ResolveCanonicalSchema(ctx, src)
	if err != nil {
		return nil, err
	}
	zones, err := s.resolveZones(&types.Request{Filterers: filterers, TimeZone: timeZone}, schema)
	if err != nil {
		return nil, err
	}
	return descx.AppliedZones(zones, s.ZoneLoader()), nil
}

// filterPlan captures the per-call predicate inputs so the input-shape
// dispatch (single-file, shard archive, anchor) doesn't have to take a
// growing list of optional arguments.
type filterPlan struct {
	set          processing.MemberSet
	includeField string
	filterExpr   string
	// filterers is the structured predicate filterExpr was translated
	// from (FilterToFileFilterers), judged by the shared field rule.
	filterers []*types.Filterer
	// direct is the subset of filterers built through the engine
	// filterer registry rather than translated into filterExpr (an
	// extension filterer, FILTER_TRUE / FILTER_FALSE, FILTER_SET_*,
	// FILTER_DATE_RANGES).
	direct []*types.Filterer
	// models is the bound-model set the expression's coef() reads
	// (nil binds nothing).
	models *model.Set
	// timeZone is the request zone the expression's to_day() reads
	// (FilterToFileRequest.TimeZone); "" inherits the instance default.
	timeZone string
}

// BindFilterModels returns ctx carrying refs loaded — every file read
// and decoded under the Request.Models rule (model.CheckRefs /
// LoadFiles, the instance's registered populations as the outer set) —
// marked bound, so a FilterToFile* call under it compiles its
// predicate's coef() against them. No refs binds the populations alone.
func (s *Service) BindFilterModels(ctx context.Context, refs []types.ModelRef) (context.Context, error) {
	ctx, err := s.bindRequestModels(ctx, model.Refs{Models: refs})
	if err != nil {
		return ctx, err
	}
	return withModelsBound(ctx, model.SetFrom(ctx)), nil
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
		arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
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
	pulseVersion, err := encoding.ReadHeader(br)
	if errors.HasCode(err, errors.PULSE_COHORT_COMPRESSED) {
		return nil, err // the one refusal that names its fix
	}
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"invalid pulse file header")
	}
	schema, err := encoding.ReadSchema(br, pulseVersion)
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

	// The bound-model set the predicate's coef() reads: a facade
	// FilterToFileRequest.Models load (BindFilterModels) — else the
	// instance's registered populations, as Process binds them.
	ctx, err := s.bindRequestModels(ctx, model.Refs{})
	if err != nil {
		return 0, err
	}
	plan.models = model.SetFrom(ctx)

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
	pulseVersion, err := encoding.ReadHeader(br)
	if errors.HasCode(err, errors.PULSE_COHORT_COMPRESSED) {
		return 0, err // the one refusal that names its fix
	}
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"invalid pulse file header")
	}
	schema, err := encoding.ReadSchema(br, pulseVersion)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading schema")
	}
	headerSchemaEnd := int64(len(data)) - int64(br.Len())

	filterFn, err := s.buildCombinedFilter(schema, plan)
	if err != nil {
		return 0, err
	}
	keep := s.fieldFilterForPlan(schema, plan)

	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(data[:headerSchemaEnd])

	written, err := s.streamFilterRecords(ctx, data, br, schema, filterFn, keep, out)
	if err != nil {
		return 0, err
	}

	if err := afero.WriteFile(fsys, dst, out.Bytes(), 0644); err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing filtered cohort to: %s", dst))
	}
	return written, nil
}

// skipShardPreamble advances br past a shard's header and schema block.
// The block is read LAYOUT-ONLY: the caller needs only its end offset
// (the bytes are copied verbatim, and rows decode through the canonical
// schema), so the shard's dictionaries and group entry tables are read
// past, never allocated (TestSkipShardPreamble_SkipsDictionaries).
func skipShardPreamble(br *bytes.Reader, name string) error {
	pulseVersion, err := encoding.ReadHeader(br)
	if err != nil {
		return errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("reading shard %q header", name))
	}
	if _, err := encx.ReadSchemaLayout(br, pulseVersion); err != nil {
		return errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("reading shard %q schema", name))
	}
	return nil
}

// filterShardArchiveToFile applies the filter to every shard in the
// source archive and writes a new shard archive to dst. One input shard
// → one output shard, basename preserved, central-directory order
// preserved. Empty shards (zero surviving records) are kept so the
// shard_count metadata stays stable across the filter.
func (s *Service) filterShardArchiveToFile(ctx context.Context, fsys afero.Fs, dst string, plan filterPlan, data []byte) (int64, error) {
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
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
	keep := s.fieldFilterForPlan(canonicalSchema, plan)

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
		if e.Name == encx.ReservedSchemaName {
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
		if err := skipShardPreamble(br, e.Name); err != nil {
			return 0, err
		}
		localHeaderSchemaEnd := int64(len(shardBytes)) - int64(br.Len())

		buf := bytes.NewBuffer(make([]byte, 0, len(shardBytes)))
		buf.Write(shardBytes[:localHeaderSchemaEnd])
		written, ferr := s.streamFilterRecords(ctx, shardBytes, br, canonicalSchema, filterFn, keep, buf)
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
	if err := writeArchiveEntry(zw, encx.ReservedSchemaName, schemaPayload); err != nil {
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
//
// keep narrows per-record map population to the fields the predicate
// actually reads (built once via fieldFilterForPlan). A nil keep falls
// through to the full-decode path — used when the predicate's field set
// can't be proven (malformed expression, unknown extension hook).
func (s *Service) streamFilterRecords(ctx context.Context, data []byte, br *bytes.Reader, schema *encoding.Schema, filterFn processing.FilterFunc, keep encx.FieldFilter, out *bytes.Buffer) (int64, error) {
	mapSize := len(schema.Fields)
	if keep != nil {
		mapSize = 0
		for _, f := range schema.Fields {
			if keep(f.Name) {
				mapSize++
			}
		}
		if mapSize == 0 {
			mapSize = 1
		}
	}
	values := make(map[string]float64, mapSize)
	nulls := make(map[string]bool, mapSize)
	wide := make(map[string]any, mapSize)
	rr := encx.NewRecordReader(br, schema)
	// The map decode echoes a u64 above 2^53 lossily; capture its exact
	// word for the record the filter reads (nil when no u64 field).
	exact := encx.NewExactU64Words(schema)
	rr.CaptureExactU64(exact)
	// A grouped (0x02) schema: hand each record its row's parent-group
	// entry indices so a filter over one group's members takes the
	// per-entry precompute instead of evaluating per row. Only when the
	// records are decoded against the file's own schema — an entry index
	// is meaningless against any other dictionary (Record.SetGroupIndices
	// refuses a foreign schema regardless).
	var groupIdx []uint32
	if schema.HasGroups() {
		groupIdx = make([]uint32, len(schema.Groups))
	}

	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		posStart := int64(len(data)) - int64(br.Len())
		err := rr.ReadRecordWithWideProjected(values, nulls, wide, keep)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		posEnd := int64(len(data)) - int64(br.Len())

		rec := processing.NewRecordWithWide(schema, values, nulls, wide)
		exact.ApplyTo(rec)
		if groupIdx != nil && readGroupIndices(rr, groupIdx) {
			rec.SetGroupIndices(schema, groupIdx)
		}
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

// readGroupIndices fills idx with the entry index of every parent group
// on the record rr just decoded, reporting false when any is unknown.
func readGroupIndices(rr *encx.RecordReader, idx []uint32) bool {
	for g := range idx {
		e, ok := rr.GroupIndex(g)
		if !ok {
			return false
		}
		idx[g] = e
	}
	return true
}

// fieldFilterForPlan returns a per-field projection filter narrowing
// per-record map population to the fields the plan's predicates actually
// read. Returns nil when the field set can't be narrowed safely
// (malformed expression, extension hook without introspection) — the
// caller then falls through to the full-decode path via
// ReadRecordWithWideProjected's nil-filter contract.
//
// The set is built by feeding a synthetic types.Request through
// processing.NeededFields (the same machinery the buffered Process path
// uses since v0.12.3) and then unioning the MemberSet include field.
// Saves map allocations + float64 conversions for unreferenced columns;
// byte reads still happen so file offsets stay aligned.
func (s *Service) fieldFilterForPlan(schema *encoding.Schema, plan filterPlan) encx.FieldFilter {
	if schema == nil {
		return nil
	}
	req := &types.Request{Filterers: plan.predicateFilterers()}
	set := s.neededFields(context.Background(), req, schema)
	if set.IsWide() {
		return nil
	}
	if plan.set != nil && plan.includeField != "" {
		set.Add(plan.includeField)
	}
	if set.Len() == 0 {
		return nil
	}
	return func(name string) bool { return set.Has(name) }
}

// predicateFilterers is the plan's predicate as the filterer list
// Process would build: filterExpr as one FILTER_EXPRESSION (when set),
// then every direct filterer in request order.
func (p filterPlan) predicateFilterers() []*types.Filterer {
	out := make([]*types.Filterer, 0, 1+len(p.direct))
	if p.filterExpr != "" {
		out = append(out, &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: p.filterExpr})
	}
	return append(out, p.direct...)
}

// buildPredicateFilter resolves the plan's predicate (filterExpr plus
// the direct filterers) into one FilterFunc bound to schema, each
// filterer built through the engine filterer registry — the build
// Process uses — so an extension filterer runs here exactly as it runs
// there. Extensions (operators, expr functions, lookup tables)
// registered on the Service are honored; coef() reads plan.models (nil
// binds nothing). Returns nil when the plan carries no predicate.
//
// The predicate runs zoned exactly as a Process filterer does: it is
// resolved (resolveZones) and handed through the one zone choke point
// (zoned), so a to_day() call and a zone-capable filterer
// (FILTER_DATE_RANGES) without its own `tz` / `tz_field` read the
// inherited zone: timeZone (the FilterToFileRequest's `time_zone`, in
// Request.TimeZone's place), else Options.DefaultTimeZone, else UTC.
func (s *Service) buildPredicateFilter(schema *encoding.Schema, plan filterPlan) (processing.FilterFunc, error) {
	filterers := plan.predicateFilterers()
	if len(filterers) == 0 {
		return nil, nil
	}
	req := &types.Request{Filterers: filterers, TimeZone: plan.timeZone}
	zones, err := s.resolveZones(req, schema)
	if err != nil {
		return nil, err
	}
	req = s.zoned(req, zones)
	fns, err := processing.BuildFiltersWithModels(
		req.Filterers,
		schema,
		s.extensions,
		plan.models,
	)
	if err != nil {
		return nil, err
	}
	if len(fns) == 1 {
		return fns[0], nil
	}
	return func(rec *processing.Record) (bool, error) {
		for _, fn := range fns {
			ok, err := fn(rec)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}, nil
}

// buildCombinedFilter composes an optional MemberSet membership test
// with the plan's optional predicate (FILTER_EXPRESSION plus direct
// filterers, buildPredicateFilter) into one FilterFunc. The set
// predicate runs first when both are present, so per-row work
// short-circuits on misses without paying the predicate's cost.
func (s *Service) buildCombinedFilter(schema *encoding.Schema, plan filterPlan) (processing.FilterFunc, error) {
	if len(plan.filterers) > 0 {
		if err := s.checkFieldRefs(context.Background(), &types.Request{Filterers: plan.filterers}, schema); err != nil {
			return nil, err
		}
	}
	var setFn processing.FilterFunc
	if plan.set != nil {
		fn, err := processing.BuildMemberSetPredicate(plan.set, schema, plan.includeField)
		if err != nil {
			return nil, err
		}
		setFn = fn
	}

	exprFn, err := s.buildPredicateFilter(schema, plan)
	if err != nil {
		return nil, err
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
	if entry == encx.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot filter the reserved canonical schema entry",
			map[string]any{"entry": entry})
	}
	data, err := afero.ReadFile(s.fs.Fs(), archivePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file for anchor: %s", archivePath))
	}
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
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
