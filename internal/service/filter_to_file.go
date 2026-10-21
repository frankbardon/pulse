package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
// Every arm writes atomically: the output streams into dst's stage
// (`<dst>.pulse.tmp-staged`) and is published by one rename, so a failed
// run leaves dst as it was. The single-file and anchored arms stream
// rows from the input too (filterSingleFileStream): memory never grows
// with the row count.
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

// FilterToFileOptions is the options form of the filter-to-file run
// (Pulse.FilterToFileWithOptions, `pulse cohort filter`): an optional
// include-set (Set tested on IncludeField), an optional
// FILTER_EXPRESSION predicate, and Flatten. Without Flatten at least
// one predicate is required, exactly as FilterToFileBySetAndExpr
// requires one.
type FilterToFileOptions struct {
	IncludeField string
	Set          processing.MemberSet
	Expression   string
	// Flatten writes a shard archive's surviving rows, shard by shard in
	// archive order, into ONE single-file cohort under the archive's
	// canonical (widened) schema instead of a per-shard archive; with
	// no predicate it is a pure flatten. On a single-file or anchored
	// input — already one file — it is a no-op: the output is what the
	// same call without Flatten writes (no predicate keeps every row).
	Flatten bool
}

// FilterToFileOutcome is FilterToFileWithOptions' report.
type FilterToFileOutcome struct {
	// Records is the number of records written to dst.
	Records int64
	// Flattened is true when a shard archive was flattened into a
	// single-file cohort (Flatten over an archive input).
	Flattened bool
}

// FilterToFileWithOptions is the filter-to-file run behind every CLI
// `cohort filter` call: FilterToFileBySetAndExpr plus the Flatten
// mode. Input-shape dispatch is FilterToFile's.
func (s *Service) FilterToFileWithOptions(ctx context.Context, src, dst string, opts FilterToFileOptions) (*FilterToFileOutcome, error) {
	if opts.Set == nil && opts.Expression == "" && !opts.Flatten {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires at least one of include-set or filter expression (or flatten)")
	}
	if opts.Set != nil && opts.IncludeField == "" {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file include-set requires a non-empty include field name")
	}
	n, flattened, err := s.filterToFileRun(ctx, src, dst, filterPlan{
		set:          opts.Set,
		includeField: opts.IncludeField,
		filterExpr:   opts.Expression,
		flatten:      opts.Flatten,
	})
	if err != nil {
		return nil, err
	}
	return &FilterToFileOutcome{Records: n, Flattened: flattened}, nil
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
	// flatten writes an archive input as ONE single-file cohort
	// (FilterToFileOptions.Flatten); a no-op on a single-file input.
	flatten bool
}

// hasPredicate reports whether the plan filters at all: a flatten
// without one keeps every row.
func (p filterPlan) hasPredicate() bool {
	return p.set != nil || p.filterExpr != "" || len(p.direct) > 0
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

	// Header + schema only, on every arm: an anchored shard's entry
	// stream, an archive's central directory + canonical entry (random
	// access), a single file's prefix — never a record payload.
	if archivePath, anchor, ok := splitAnchorPath(src); ok {
		if anchor == encx.ReservedSchemaName {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
				"cannot filter the reserved canonical schema entry",
				map[string]any{"entry": anchor})
		}
		a, err := openArchiveFile(fsys, archivePath)
		if err != nil {
			return nil, err
		}
		defer a.Close()
		rc, err := a.arch.Open(anchor)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return readSchemaFromReader(bufio.NewReader(rc))
	}

	isArchive, err := s.pathIsShardArchive(src)
	if err != nil {
		return nil, err
	}
	if isArchive {
		a, err := openArchiveFile(fsys, src)
		if err != nil {
			return nil, err
		}
		defer a.Close()
		doc, err := readSchemaDocEntry(a.arch)
		if err != nil {
			return nil, err
		}
		return doc.Schema, nil
	}

	f, err := fsys.Open(src)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", src))
	}
	defer f.Close()
	return readSchemaFromReader(bufio.NewReader(f))
}

// readSchemaFromReader reads a single-file header + schema block off r.
func readSchemaFromReader(br io.Reader) (*encoding.Schema, error) {
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
	n, _, err := s.filterToFileRun(ctx, src, dst, plan)
	return n, err
}

// filterToFileRun is the one filter-to-file run: it dispatches on the
// input shape and reports, beside the records written, whether an
// archive was flattened.
func (s *Service) filterToFileRun(ctx context.Context, src, dst string, plan filterPlan) (int64, bool, error) {
	if src == "" {
		return 0, false, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty input path")
	}
	if dst == "" {
		return 0, false, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"filter_to_file requires a non-empty output path")
	}

	// The bound-model set the predicate's coef() reads: a facade
	// FilterToFileRequest.Models load (BindFilterModels) — else the
	// instance's registered populations, as Process binds them.
	ctx, err := s.bindRequestModels(ctx, model.Refs{})
	if err != nil {
		return 0, false, err
	}
	plan.models = model.SetFrom(ctx)

	fsys := s.fs.Fs()

	// Anchor syntax: archive#shard → single-shard payload → single-file
	// output (flatten is a no-op: one shard is already one file). The
	// shard's entry is streamed off the archive by random access.
	if archivePath, anchor, ok := splitAnchorPath(src); ok {
		n, err := s.filterAnchoredShardToFile(ctx, fsys, archivePath, anchor, dst, plan)
		return n, false, err
	}

	// An archive is read by random access, one shard at a time
	// (filterShardArchiveToFile / flattenShardArchiveToFile); a single
	// file is streamed (filterSingleFileStream).
	isArchive, err := s.pathIsShardArchive(src)
	if err != nil {
		return 0, false, err
	}
	if isArchive {
		a, err := openArchiveFile(fsys, src)
		if err != nil {
			return 0, false, err
		}
		defer a.Close()
		if plan.flatten {
			n, err := s.flattenShardArchiveToFile(ctx, fsys, dst, plan, a)
			return n, err == nil, err
		}
		n, err := s.filterShardArchiveToFile(ctx, fsys, dst, plan, a)
		return n, false, err
	}

	f, err := fsys.Open(src)
	if err != nil {
		return 0, false, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", src))
	}
	defer f.Close()
	n, err := s.filterSingleFileStream(ctx, fsys, f, dst, plan)
	return n, false, err
}

// filterSingleFileStream runs the single-file filter loop over a
// standalone `.pulse` payload streamed off src and publishes the result
// at dst. Used both for direct single-file inputs and for anchor-syntax
// inputs (the anchored shard's entry is a complete standalone payload).
//
// The output is the source's header + schema bytes verbatim, then every
// kept physical row's bytes in source order (a predicate-free run keeps
// every WHOLE row; a truncated tail is dropped either way). Rows stream
// from src through one shardCopyBuffer read window into dst's stage
// (stagedStream) and are published by one rename: memory is O(the
// windows + the schema block), never O(input) or O(output), and dst is
// untouched until the rename — a failed run leaves it as it was and
// removes the stage.
func (s *Service) filterSingleFileStream(ctx context.Context, fsys afero.Fs, src io.Reader, dst string, plan filterPlan) (int64, error) {
	window := bufio.NewReaderSize(src, shardCopyBuffer)
	// The preamble is captured as it is parsed, so it is copied
	// byte-for-byte (ReadHeader / ReadSchema consume exactly their own
	// bytes).
	var preamble bytes.Buffer
	tee := io.TeeReader(window, &preamble)
	pulseVersion, err := encoding.ReadHeader(tee)
	if errors.HasCode(err, errors.PULSE_COHORT_COMPRESSED) {
		return 0, err // the one refusal that names its fix
	}
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"invalid pulse file header")
	}
	schema, err := encoding.ReadSchema(tee, pulseVersion)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading schema")
	}

	// The predicate builds (and any config refusal fires) before the
	// stage opens.
	var filterFn processing.FilterFunc
	var keep encx.FieldFilter
	if plan.hasPredicate() {
		if filterFn, err = s.buildCombinedFilter(schema, plan); err != nil {
			return 0, err
		}
		keep = s.fieldFilterForPlan(schema, plan)
	}

	out, err := newStagedStream(fsys, dst)
	if err != nil {
		return 0, err
	}
	defer out.abort()
	if _, err := out.Write(preamble.Bytes()); err != nil {
		return 0, out.wrap(err, "writing filtered cohort")
	}
	var written int64
	if filterFn == nil {
		written, err = copyWholeRecords(ctx, window, int64(schema.RecordByteSize()), out)
	} else {
		written, err = s.streamFilterRecords(ctx, window, schema, filterFn, keep, out)
	}
	if err != nil {
		return 0, err
	}
	if err := out.commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// copyWholeRecords copies every whole stride-sized record from r to w
// through one window-sized buffer, dropping a truncated tail, and
// returns the records copied. A non-positive stride copies nothing
// (Schema.RecordCountForPayload's rule).
func copyWholeRecords(ctx context.Context, r io.Reader, stride int64, w io.Writer) (int64, error) {
	if stride <= 0 {
		return 0, nil
	}
	buf := make([]byte, max(shardCopyBuffer/stride, 1)*stride)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		got, err := io.ReadFull(r, buf)
		whole := int64(got) / stride
		if whole > 0 {
			if _, werr := w.Write(buf[:whole*stride]); werr != nil {
				return 0, errors.WrapCodedError(werr, errors.SERVICE_RESOURCE,
					"writing filtered records")
			}
			n += whole
		}
		switch err {
		case nil:
		case io.EOF, io.ErrUnexpectedEOF:
			return n, nil
		default:
			if errors.HasCode(err, errors.ENCODING_IO) {
				return 0, err // an anchored entry's read, already coded
			}
			return 0, errors.WrapCodedError(err, errors.ENCODING_IO, "reading cohort records")
		}
	}
}

// filterAnchoredShardToFile streams the anchored entry of archivePath
// (one entry, read off the archive by random access — never the whole
// archive, and never the whole entry) through filterSingleFileStream.
// Same semantics as service.OpenAnchor's resolution.
func (s *Service) filterAnchoredShardToFile(ctx context.Context, fsys afero.Fs, archivePath, entry, dst string, plan filterPlan) (int64, error) {
	if entry == encx.ReservedSchemaName {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot filter the reserved canonical schema entry",
			map[string]any{"entry": entry})
	}
	a, err := openArchiveFile(fsys, archivePath)
	if err != nil {
		return 0, err
	}
	defer a.Close()
	rc, err := a.arch.Open(entry)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return s.filterSingleFileStream(ctx, fsys, &entryReadError{r: rc, entry: entry, archive: archivePath}, dst, plan)
}

// entryReadError tags a failed read of an anchored entry with the code
// and message reading it whole always carried (ENCODING_IO), leaving
// io.EOF / io.ErrUnexpectedEOF — the stream's own boundaries — as they
// are.
type entryReadError struct {
	r              io.Reader
	entry, archive string
}

func (e *entryReadError) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		err = errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading anchored shard %q from %s", e.entry, e.archive))
	}
	return n, err
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
//
// The source archive a is read by random access: its canonical entry,
// then each shard's payload in turn — one input shard resident at a
// time, never the whole source archive.
func (s *Service) filterShardArchiveToFile(ctx context.Context, fsys afero.Fs, dst string, plan filterPlan, a *archiveFile) (int64, error) {
	arch := a.arch

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

	entries := arch.Entries()
	// One output shard per input shard: refuse a count the SHRD
	// trailer's u16 shard_count cannot hold before filtering anything.
	shardCount := 0
	for _, e := range entries {
		if e.Name != encx.ReservedSchemaName {
			shardCount++
		}
	}
	if err := checkArchiveShardCount(shardCount, dst); err != nil {
		return 0, err
	}

	// `_schema.pulse` leads the archive and carries the surviving
	// aggregate, which is known only once every shard is filtered, so
	// the filtered shards are SPOOLED (one file beside dst, one shard
	// resident at a time) and then streamed into the archive writer.
	// The writer opens first: its sweep of `<dst>.pulse.tmp-*` would
	// otherwise remove the spool, which carries the same prefix so a
	// crashed run's spool is swept with its stage.
	w, err := newArchiveWriter(fsys, dst)
	if err != nil {
		return 0, err
	}
	defer w.abort()
	spool, err := newFilterSpool(fsys, dst)
	if err != nil {
		return 0, err
	}
	defer spool.discard()

	type spooled struct {
		name     string
		off, len int64
	}
	out := make([]spooled, 0, shardCount)
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

		off := spool.n
		if _, err := spool.Write(shardBytes[:localHeaderSchemaEnd]); err != nil {
			return 0, spool.wrap(err)
		}
		written, ferr := s.streamFilterRecords(ctx, br, canonicalSchema, filterFn, keep, spool)
		if ferr != nil {
			return 0, ferr
		}
		out = append(out, spooled{name: e.Name, off: off, len: spool.n - off})
		totalWritten += written
	}
	if err := spool.bw.Flush(); err != nil {
		return 0, spool.wrap(err)
	}

	// Rebuild the archive: refreshed _schema.pulse trailer (aggregate +
	// shard_count), then each filtered shard streamed off the spool.
	if err := w.writeSchemaDoc(canonicalSchema, uint64(totalWritten), len(out)); err != nil {
		return 0, err
	}
	for _, sh := range out {
		ew, err := w.createEntry(sh.name)
		if err != nil {
			return 0, err
		}
		if _, err := io.CopyBuffer(ew, io.NewSectionReader(spool.f, sh.off, sh.len), w.buf); err != nil {
			return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				fmt.Sprintf("writing filtered shard %q", sh.name))
		}
	}
	if err := w.commit(); err != nil {
		return 0, err
	}
	return totalWritten, nil
}

// filterSpool is the archive arm's scratch file: every filtered shard
// payload appended in archive order, read back by section once the
// aggregate is known. It sits beside dst as `<dst>.pulse.tmp-spool`, so
// the next writer's sweep removes one a crash left behind.
type filterSpool struct {
	fsys afero.Fs
	path string
	f    afero.File
	bw   *bufio.Writer
	n    int64
}

func newFilterSpool(fsys afero.Fs, dst string) (*filterSpool, error) {
	path := filepath.Join(filepath.Dir(dst), filepath.Base(dst)+shardAdminTempPattern+"spool")
	f, err := fsys.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating filter spool for %s", dst))
	}
	return &filterSpool{fsys: fsys, path: path, f: f, bw: bufio.NewWriterSize(f, shardCopyBuffer)}, nil
}

func (s *filterSpool) Write(p []byte) (int, error) {
	n, err := s.bw.Write(p)
	s.n += int64(n)
	return n, err
}

func (s *filterSpool) wrap(err error) error {
	return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
		fmt.Sprintf("writing filter spool %s", s.path))
}

// discard closes and removes the spool.
func (s *filterSpool) discard() {
	_ = s.f.Close()
	_ = s.fsys.Remove(s.path)
}

// flattenShardArchiveToFile writes the archive's surviving rows into ONE
// single-file cohort at dst (FilterToFileOptions.Flatten). The output
// schema is the archive's CANONICAL `_schema.pulse` schema — the widened
// set rungs, the union dictionaries and, for a grouped (0x02) archive,
// the canonical parent-group dictionaries. Every shard's rows already
// address those (a shard dictionary, categorical, set or group, is a
// prefix of the canonical one, and every shard shares the canonical
// physical stride), so each surviving row is copied by position with
// no remap, and the rows land in global archive order: shards in
// central-directory order, rows in shard order.
//
// Memory is O(one shard): a pure flatten (no predicate) streams each
// stored shard's records through the copy window; a filtered flatten
// loads one shard at a time, as the archive arm does. The output is
// streamed into dst's stage and published by one rename — no
// whole-output buffer, and dst is untouched until the rename.
func (s *Service) flattenShardArchiveToFile(ctx context.Context, fsys afero.Fs, dst string, plan filterPlan, a *archiveFile) (int64, error) {
	canonicalDoc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		return 0, err
	}
	schema := canonicalDoc.Schema

	var filterFn processing.FilterFunc
	var keep encx.FieldFilter
	if plan.hasPredicate() {
		filterFn, err = s.buildCombinedFilter(schema, plan)
		if err != nil {
			return 0, err
		}
		keep = s.fieldFilterForPlan(schema, plan)
	}

	out, err := newStagedStream(fsys, dst)
	if err != nil {
		return 0, err
	}
	defer out.abort()
	if err := encx.WritePreamble(out, schema); err != nil {
		return 0, err
	}
	stride := int64(schema.RecordByteSize())
	if stride <= 0 {
		return 0, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"cannot flatten an archive whose canonical schema has no record stride",
			map[string]any{"archive": a.path})
	}
	buf := make([]byte, shardCopyBuffer)
	window := bufio.NewReaderSize(nil, shardCopyBuffer)

	var total int64
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if filterFn == nil && a.arch.IsStored(e.Name) {
			n, err := copyShardRecords(a, e.Name, stride, out, window, buf)
			if err != nil {
				return 0, err
			}
			total += n
			continue
		}
		shardBytes, err := readEntryBytes(a.arch, e.Name)
		if err != nil {
			return 0, err
		}
		br := bytes.NewReader(shardBytes)
		if err := skipShardPreamble(br, e.Name); err != nil {
			return 0, err
		}
		if filterFn == nil {
			// A deflated shard (a third-party zip tool's), whole
			// records only.
			n := int64(br.Len()) / stride
			start := int64(len(shardBytes)) - int64(br.Len())
			if _, err := out.Write(shardBytes[start : start+n*stride]); err != nil {
				return 0, out.wrap(err, "writing flattened cohort")
			}
			total += n
			continue
		}
		n, err := s.streamFilterRecords(ctx, br, schema, filterFn, keep, out)
		if err != nil {
			return 0, err
		}
		total += n
	}
	if err := out.commit(); err != nil {
		return 0, err
	}
	return total, nil
}

// copyShardRecords streams a stored shard's whole records to w through
// the shared read window and buf: its preamble is read layout-only and
// skipped, then floor(payload / stride) records are copied — never the
// shard whole.
func copyShardRecords(a *archiveFile, name string, stride int64, w io.Writer, window *bufio.Reader, buf []byte) (int64, error) {
	sect, err := a.storedShard(name)
	if err != nil {
		return 0, err
	}
	window.Reset(sect)
	cr := &countingReader{r: window}
	pulseVersion, err := encoding.ReadHeader(cr)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("reading shard %q header", name))
	}
	if _, err := encx.ReadSchemaLayout(cr, pulseVersion); err != nil {
		return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("reading shard %q schema", name))
	}
	n := max(sect.Size()-cr.n, 0) / stride
	if _, err := io.CopyBuffer(w, io.LimitReader(cr, n*stride), buf); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("copying shard %q records", name))
	}
	return n, nil
}

// streamFilterRecords iterates records from r (positioned after
// header+schema), applies filterFn against records decoded with schema,
// and writes each kept row's raw bytes — exactly the bytes its decode
// consumed from r, one physical row — into out. Returns the number of
// records written. Only the current row is held (rowCapture).
//
// keep narrows per-record map population to the fields the predicate
// actually reads (built once via fieldFilterForPlan). A nil keep falls
// through to the full-decode path — used when the predicate's field set
// can't be proven (malformed expression, unknown extension hook).
func (s *Service) streamFilterRecords(ctx context.Context, r io.Reader, schema *encoding.Schema, filterFn processing.FilterFunc, keep encx.FieldFilter, out io.Writer) (int64, error) {
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
	row := &rowCapture{r: r}
	rr := encx.NewRecordReader(row, schema)
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
		row.buf = row.buf[:0]
		err := rr.ReadRecordWithWideProjected(values, nulls, wide, keep)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}

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
		if _, err := out.Write(row.buf); err != nil {
			return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				"writing filtered records")
		}
		written++
	}
	return written, nil
}

// rowCapture records every byte read through it since the caller last
// reset buf: the RecordReader over it reads one physical row per record
// and never ahead (a grouped cohort's logical stream pulls exactly one
// physical stride per record), so after a decode buf is that row's
// on-disk bytes.
type rowCapture struct {
	r   io.Reader
	buf []byte
}

func (c *rowCapture) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.buf = append(c.buf, p[:n]...)
	return n, err
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

// isShardArchiveMagic reports whether data begins with the zip magic
// "PK\x03\x04". Mirrors the dispatch check in service.Open.
func isShardArchiveMagic(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}
