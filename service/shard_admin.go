package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// shardAdminTempPattern is the file-name suffix used for the temp
// file written during atomic temp+rename mutations. The full
// convention is `<archive>.tmp-<random>`; afero.TempFile generates the
// random suffix.
const shardAdminTempPattern = ".pulse.tmp-"

// CreateShardArchive builds a fresh Pulse shard archive at archivePath
// from the constituent shardPaths. The first shard seeds the canonical
// schema; every other shard is validated via
// encoding.ValidateStructuralCohesion + encoding.ValidateDictPrefixRule.
// Dictionary growth across shards accumulates into the canonical
// schema. The archive is written to a temp path on the same filesystem
// and Rename'd over the destination — partial writes never appear at
// archivePath (atomic temp+rename, §7.1).
//
// Set-width AUTO-WIDEN applies here exactly as it does on AddShard: a
// set column whose merged dictionary outgrows its declared bitmask, or
// whose rung differs between two of the shards being seeded, is promoted
// to the narrowest rung that holds every member, across the canonical
// schema and every shard accumulated so far. Refusing at create while
// auto-widening at add was an asymmetry with no defence — the same two
// files produce an archive or an error depending only on whether they
// arrived together or one after the other.
//
// Widening emits a MANDATORY PULSE_SHARD_SET_WIDENED warning on the
// result, which is why this returns a *CreateShardArchiveResult rather
// than a bare error. A silent whole-archive rewrite is exactly the
// failure class the byte-layout contract exists to prevent: the shards
// stored in the archive are no longer byte-identical to the `.pulse`
// files the caller named, and nothing else would say so.
//
// A union above the widest rung has nowhere to go and stays fatal
// (PULSE_SHARD_DICT_WIDTH_OVERFLOW).
//
// Errors:
//   - PULSE_SHARD_RESERVED_NAME — any shardPath has basename `_schema.pulse`.
//   - PULSE_SHARD_NAME_COLLISION — two shardPaths share a basename.
//   - PULSE_SHARD_SCHEMA_MISMATCH / PULSE_SHARD_DICT_DIVERGENCE — propagated from cohesion validators.
//   - PULSE_SHARD_DICT_WIDTH_OVERFLOW — a set union past the widest rung.
//   - SERVICE_VALIDATION — empty shardPaths.
//   - SERVICE_RESOURCE — I/O failure on any input/output.
func (s *Service) CreateShardArchive(ctx context.Context, archivePath string, shardPaths []string) (*CreateShardArchiveResult, error) {
	if len(shardPaths) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"CreateShardArchive requires at least one shard path")
	}

	fsys := s.fs.Fs()

	// Reserved-name + basename-collision precheck. Both errors are
	// caller-fixable; surface them before any I/O.
	if err := validateBasenames(shardPaths); err != nil {
		return nil, err
	}

	result := &CreateShardArchiveResult{Archive: archivePath,
		Warnings: []encoding.CohesionWarning{}}

	// Read all shard payloads + reconcile set rungs + validate cohesion
	// + accumulate canonical schema. `shards` holds every shard already
	// folded in; a widen triggered by shard N re-lays-out all of them,
	// which is why they are carried as a slice rather than streamed
	// straight into the zip.
	shards := make([]shardPayload, 0, len(shardPaths))

	var canonical *encoding.Schema
	for i, p := range shardPaths {
		data, err := afero.ReadFile(fsys, p)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				fmt.Sprintf("CreateShardArchive: reading shard source %s", p))
		}
		schema, err := readSinglePulseSchema(data)
		if err != nil {
			return nil, err
		}
		base := filepath.Base(p)
		if i == 0 {
			canonical = cloneSchemaForArchive(schema)
			shards = append(shards, shardPayload{name: base, payload: data})
			continue
		}

		// Set-rung reconciliation runs BEFORE strict cohesion, for the
		// same reason it does on AddShard: a divergent rung fails
		// cohesion on the type byte and on every offset it shifts, so
		// cohesion first would refuse the shard before the planner saw
		// it. See Service.AddShard for the full argument.
		plans, perr := encoding.PlanSetWidening(canonical, schema)
		if perr != nil {
			if !errors.HasCode(perr, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
				if _, cerr := encoding.ValidateStructuralCohesion(canonical, schema); cerr != nil {
					return nil, cerr
				}
			}
			return nil, perr
		}
		if len(plans) > 0 {
			widenedCanonical, widenedData, widenings, werr := applySetWidening(
				plans, canonical, shards, data)
			if werr != nil {
				return nil, werr
			}
			canonical = widenedCanonical
			data = widenedData
			result.Widened = append(result.Widened, widenings...)
			for _, w := range widenings {
				result.Warnings = append(result.Warnings, setWidenedWarning(w, archivePath))
			}
			// Re-read from the widened bytes so the strict pass below
			// compares the layout that will actually be stored.
			schema, err = readSinglePulseSchema(data)
			if err != nil {
				return nil, err
			}
		}

		cohesionWarnings, cerr := encoding.ValidateStructuralCohesion(canonical, schema)
		result.Warnings = append(result.Warnings, cohesionWarnings...)
		if cerr != nil {
			return nil, cerr
		}
		extended, remap, err := encoding.MergeDictUnion(canonical, schema)
		if err != nil {
			return nil, err
		}
		canonical = extended
		if len(remap) > 0 {
			rewritten, rerr := encoding.RewriteShardCategoricals(data, canonical, remap)
			if rerr != nil {
				return nil, rerr
			}
			data = rewritten
		}
		shards = append(shards, shardPayload{name: base, payload: data})
	}

	var aggregate uint64
	for _, sh := range shards {
		n, err := recordCountFromBytes(sh.payload, canonical)
		if err != nil {
			return nil, err
		}
		aggregate += uint64(n)
	}

	// Assemble the archive bytes in memory.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	schemaPayload, err := buildSchemaDocPayload(canonical, aggregate, uint16(len(shards)))
	if err != nil {
		return nil, err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return nil, err
	}
	for _, sh := range shards {
		if err := writeArchiveEntry(zw, sh.name, sh.payload); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"CreateShardArchive: finalising zip writer")
	}

	if err := atomicWrite(fsys, archivePath, buf.Bytes()); err != nil {
		return nil, err
	}
	result.ShardCount = len(shards)
	return result, nil
}

// AddShard appends shardPath to the archive at archivePath. The
// incoming shard is validated against the archive's canonical schema
// via ValidateStructuralCohesion + MergeDictUnion. If the union-merge
// extends the canonical dictionary, the rewritten `_schema.pulse`
// payload reflects the extension.
//
// When a merged dictionary outgrows a set_* field's bitmask, the field
// is WIDENED to the narrowest rung that holds it — across the canonical
// schema and every shard payload in the archive — rather than the add
// being refused. Refusing forces a full re-import (re-read the source,
// re-infer, rebuild), which is strictly more expensive than the
// mechanical re-stride it would be avoiding. Widening emits a MANDATORY
// PULSE_SHARD_SET_WIDENED warning on the result: an expensive
// whole-archive rewrite that happens silently is indistinguishable from
// a cheap append. A union above the widest rung has nowhere to go and
// stays fatal (PULSE_SHARD_DICT_WIDTH_OVERFLOW).
//
// v1 strategy: read the full archive into memory, append the new shard
// entry, rewrite the central directory + EOCD, and write the resulting
// bytes back via the atomic temp+fsync+rename pattern (§7.1). That
// atomicity is what makes the widen safe without locking — concurrency
// is caller-owned by contract, and a mid-rewrite failure leaves the
// original archive byte-identical and openable rather than half-widened
// (which would open, and decode every post-widen field at the wrong
// offset).
func (s *Service) AddShard(ctx context.Context, archivePath, shardPath string) (*AddShardResult, error) {
	fsys := s.fs.Fs()

	base := filepath.Base(shardPath)
	if base == encoding.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			fmt.Sprintf("cannot add a shard with the reserved basename %q", encoding.ReservedSchemaName),
			map[string]any{"basename": base})
	}

	archiveBytes, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: reading archive %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, err
	}

	// Read incoming shard bytes.
	incomingBytes, err := afero.ReadFile(fsys, shardPath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: reading shard source %s", shardPath))
	}
	incomingSchema, err := readSinglePulseSchema(incomingBytes)
	if err != nil {
		return nil, err
	}

	// Read canonical schema doc.
	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return nil, err
	}

	result := &AddShardResult{Archive: archivePath, Added: shardPath,
		Warnings: []encoding.CohesionWarning{}}

	// Set-rung reconciliation is PLANNED before strict cohesion runs,
	// and that order is the whole of the relaxation. A set column whose
	// rung differs between the archive and the arriving shard fails
	// ValidateStructuralCohesion twice over — on the type byte, and on
	// every ByteOffset the rung shift moves — so cohesion running first
	// would refuse the shard before the planner ever saw it. That is the
	// case a re-import of a new period actually produces: the source
	// infers the rung its own data needs.
	//
	// Nothing else loosens. The plan covers the set-rung dimension only,
	// and the strict validator runs below over the RECONCILED schemas,
	// where a name, a non-set type, a bit position or a categorical
	// width that diverges is still fatal.
	plans, err := encoding.PlanSetWidening(canonicalDoc.Schema, incomingSchema)
	if err != nil {
		// A width overflow past the widest rung is the one fatal
		// set-width verdict and names itself precisely. Anything else
		// means the two schemas are not positionally comparable at all,
		// and the structural validator has the better diagnostic — so
		// give it the chance to speak before falling back.
		if !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
			if _, cerr := encoding.ValidateStructuralCohesion(canonicalDoc.Schema, incomingSchema); cerr != nil {
				return nil, cerr
			}
		}
		return nil, err
	}

	// Enumerate existing shard payloads, detecting name collision. This
	// happens BEFORE the widen so a collision costs nothing, and because
	// an auto-widen has to rewrite every one of these payloads.
	existing := make([]shardPayload, 0, len(arch.Entries()))
	for _, e := range arch.Entries() {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}
		if e.Name == base {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_NAME_COLLISION,
				fmt.Sprintf("archive already contains a shard named %q", base),
				map[string]any{"basename": base})
		}
		payload, perr := readEntryBytes(arch, e.Name)
		if perr != nil {
			return nil, perr
		}
		existing = append(existing, shardPayload{name: e.Name, payload: payload})
	}

	canonicalSchema := canonicalDoc.Schema

	// Set-width auto-widen. The plan answers the question MergeDictUnion
	// cannot: a union past the declared bitmask, or a rung the two sides
	// disagree on, is only fatal if nothing reconciles them first.
	if len(plans) > 0 {
		widened, widenedIncoming, widenings, werr := applySetWidening(
			plans, canonicalSchema, existing, incomingBytes)
		if werr != nil {
			return nil, werr
		}
		canonicalSchema = widened
		incomingBytes = widenedIncoming
		result.Widened = widenings
		for _, w := range widenings {
			result.Warnings = append(result.Warnings, setWidenedWarning(w, archivePath))
		}
		// Re-read the incoming schema from the widened bytes: the
		// canonical block and the shard block were produced by
		// different helpers, and the strict cohesion pass below is what
		// proves they agree. A mismatch there is a layout divergence,
		// not a user error.
		incomingSchema, err = readSinglePulseSchema(incomingBytes)
		if err != nil {
			return nil, err
		}
	}

	// Strict structural cohesion over the RECONCILED schemas. Every
	// dimension but the set rung is enforced here exactly as before, and
	// the set rung is enforced too — the plan above has made the two
	// sides agree, so a divergence surviving to this point is a widen
	// bug rather than a caller error.
	cohesionWarnings, err := encoding.ValidateStructuralCohesion(canonicalSchema, incomingSchema)
	result.Warnings = append(result.Warnings, cohesionWarnings...)
	if err != nil {
		return nil, err
	}

	// Union-merge the dictionaries. When the incoming shard's
	// dictionaries diverge from canonical, the union is adopted and the
	// incoming bytes are rewritten to use canonical indices before being
	// placed in the archive.
	canonicalSchema, remap, err := encoding.MergeDictUnion(canonicalSchema, incomingSchema)
	if err != nil {
		return nil, err
	}
	if len(remap) > 0 {
		rewritten, rerr := encoding.RewriteShardCategoricals(incomingBytes, canonicalSchema, remap)
		if rerr != nil {
			return nil, rerr
		}
		incomingBytes = rewritten
	}

	// Aggregate record count: sum across existing shards + the new
	// shard, computed from per-shard payloads. We re-peek each existing
	// shard's record count rather than trust _schema.pulse's cached
	// aggregate (the cached value may lag if the archive was edited
	// outside Pulse). Post-widen the payloads carry the new stride and
	// canonicalSchema the new type bytes, so the two agree.
	newShardCount := uint16(len(existing) + 1)
	var aggregate uint64
	for _, e := range existing {
		n, perr := recordCountFromBytes(e.payload, canonicalSchema)
		if perr != nil {
			return nil, perr
		}
		aggregate += uint64(n)
	}
	{
		n, perr := recordCountFromBytes(incomingBytes, canonicalSchema)
		if perr != nil {
			return nil, perr
		}
		aggregate += uint64(n)
	}

	// Rebuild the archive: canonical schema (possibly extended and/or
	// widened) + every previously-stored shard + the new shard at the
	// tail.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	schemaPayload, err := buildSchemaDocPayload(canonicalSchema, aggregate, newShardCount)
	if err != nil {
		return nil, err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return nil, err
	}
	for _, e := range existing {
		if err := writeArchiveEntry(zw, e.name, e.payload); err != nil {
			return nil, err
		}
	}
	if err := writeArchiveEntry(zw, base, incomingBytes); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"AddShard: finalising zip writer")
	}

	if err := atomicWrite(fsys, archivePath, buf.Bytes()); err != nil {
		return nil, err
	}
	result.ShardCount = int(newShardCount)
	return result, nil
}

// RemoveShard rewrites the archive at archivePath omitting the named
// shard. The canonical schema is preserved (orphaned dictionary
// entries are retained — canonical only grows). Returns
// PULSE_SHARD_MISSING when the named shard is not present.
func (s *Service) RemoveShard(ctx context.Context, archivePath, shardBasename string) error {
	fsys := s.fs.Fs()
	if shardBasename == encoding.ReservedSchemaName {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot remove the reserved canonical schema entry",
			map[string]any{"basename": shardBasename})
	}

	archiveBytes, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("RemoveShard: reading archive %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return err
	}

	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return err
	}

	type entry struct {
		name    string
		payload []byte
	}
	kept := make([]entry, 0)
	found := false
	for _, e := range arch.Entries() {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}
		if e.Name == shardBasename {
			found = true
			continue
		}
		payload, perr := readEntryBytes(arch, e.Name)
		if perr != nil {
			return perr
		}
		kept = append(kept, entry{name: e.Name, payload: payload})
	}
	if !found {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			fmt.Sprintf("shard %q not present in archive", shardBasename),
			map[string]any{"entry": shardBasename})
	}

	// Recompute aggregate record count over the kept shards.
	var aggregate uint64
	for _, e := range kept {
		n, perr := recordCountFromBytes(e.payload, canonicalDoc.Schema)
		if perr != nil {
			return perr
		}
		aggregate += uint64(n)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	schemaPayload, err := buildSchemaDocPayload(canonicalDoc.Schema, aggregate, uint16(len(kept)))
	if err != nil {
		return err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return err
	}
	for _, e := range kept {
		if err := writeArchiveEntry(zw, e.name, e.payload); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"RemoveShard: finalising zip writer")
	}
	return atomicWrite(fsys, archivePath, buf.Bytes())
}

// ListShards opens the archive and returns its ShardEntry slice in
// central-directory order (reuses Service.Open which peeks per-shard
// headers to populate RecordCount). Single-file cohorts return an
// empty slice.
func (s *Service) ListShards(ctx context.Context, archivePath string) ([]ShardEntry, error) {
	cohort, err := s.Open(ctx, archivePath)
	if err != nil {
		return nil, err
	}
	return cohort.Shards(), nil
}

// ExtractShard returns an io.ReadCloser over the named shard's
// standalone single-file `.pulse` bytes. The closer releases the
// archive resources; callers must invoke Close after draining.
func (s *Service) ExtractShard(ctx context.Context, archivePath, shardBasename string) (io.ReadCloser, error) {
	fsys := s.fs.Fs()
	archiveBytes, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("ExtractShard: reading archive %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, err
	}
	if shardBasename == encoding.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot extract the reserved canonical schema entry as a shard",
			map[string]any{"basename": shardBasename})
	}
	rc, err := arch.Open(shardBasename)
	if err != nil {
		return nil, err
	}
	return rc, nil
}

// validateBasenames runs the reserved-name and uniqueness checks over
// the caller-supplied shard paths. Returns the first failure
// encountered. Order-stable so test output is deterministic.
func validateBasenames(paths []string) error {
	seen := make(map[string]struct{}, len(paths))
	// Process in input order so error messages reference the first
	// offending basename.
	for _, p := range paths {
		base := filepath.Base(p)
		if base == encoding.ReservedSchemaName {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
				fmt.Sprintf("shard path %q has the reserved basename %q",
					p, encoding.ReservedSchemaName),
				map[string]any{"path": p, "basename": base})
		}
		if _, dup := seen[base]; dup {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_NAME_COLLISION,
				fmt.Sprintf("shard basename %q appears more than once in the create list", base),
				map[string]any{"basename": base})
		}
		seen[base] = struct{}{}
	}
	return nil
}

// readSinglePulseSchema parses a single-file `.pulse` byte slice and
// returns the embedded schema. Used by Create/Add to seed cohesion
// validation before forwarding the payload into the archive.
func readSinglePulseSchema(data []byte) (*encoding.Schema, error) {
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard header")
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard schema")
	}
	return schema, nil
}

// cloneSchemaForArchive returns a deep enough copy of s suitable as the
// canonical schema seed. Reuses encoding's clone via re-marshal — keeps
// the unexported encoding.cloneSchema unused here. Field descriptors
// are copied by value; dictionaries are reconstructed.
func cloneSchemaForArchive(s *encoding.Schema) *encoding.Schema {
	if s == nil {
		return nil
	}
	out := &encoding.Schema{Fields: make([]encoding.Field, len(s.Fields))}
	for i, f := range s.Fields {
		out.Fields[i] = f
		if f.Dictionary != nil {
			d := encoding.NewDictionary()
			for _, v := range f.Dictionary.Values() {
				_, _ = d.Add(v)
			}
			out.Fields[i].Dictionary = d
		}
	}
	return out
}

// recordCountFromBytes derives the shard's record count from its
// payload bytes against the canonical schema's per-record stride. It
// reads the shard's own header and schema block only to position the
// reader at the first record and to reject a payload that is not a
// valid single-file cohort; the stride itself comes from the canonical
// schema, which per-shard structural cohesion guarantees agrees with
// the shard's own.
//
// The arithmetic is deliberately NOT written here. It is
// encoding.Schema.RecordCountForPayload — the one derivation behind
// every "how many records does this hold" answer — because a local
// sum of FieldType.ByteSize() drops the trailing per-record null
// bitmap and scores bit-packed fields (u4, packed_bool, ByteSize()==0)
// as zero bytes wide, which is exactly how this call site used to bake
// an inflated aggregate_record_count into `_schema.pulse`.
//
// A truncated tail FLOORS silently, matching
// Archive.PeekShardRecordCount on the read side. Every caller here
// (CreateShardArchive, AddShard, RemoveShard, CompactShardArchive)
// returns a bare error with no warning channel, and refreshing cached
// metadata must not be the operation that fails on an archive that
// still opens and still processes. `pulse shard verify` is the
// diagnostic arm that owns reporting a short tail.
func recordCountFromBytes(payload []byte, schema *encoding.Schema) (int64, error) {
	r := bytes.NewReader(payload)
	if err := encoding.ReadHeader(r); err != nil {
		return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"recordCountFromBytes: reading shard header")
	}
	if _, err := encoding.ReadSchema(r); err != nil {
		return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"recordCountFromBytes: reading shard schema")
	}
	count, _, ok := schema.RecordCountForPayload(int64(r.Len()))
	if !ok {
		// Field-less schema (non-positive stride): no records to
		// count, matching PeekShardRecordCount's zero fallback.
		return 0, nil
	}
	return count, nil
}

// readSchemaDocEntry parses the archive's reserved `_schema.pulse`
// entry.
func readSchemaDocEntry(arch *encoding.Archive) (*encoding.SchemaDoc, error) {
	rc, err := arch.Open(encoding.ReservedSchemaName)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	doc, err := encoding.ReadSchemaDoc(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading archive canonical schema")
	}
	return doc, nil
}

// readEntryBytes drains the named entry into a byte slice.
func readEntryBytes(arch *encoding.Archive, name string) ([]byte, error) {
	rc, err := arch.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading archive entry %q", name))
	}
	return data, nil
}

// buildSchemaDocPayload serialises a canonical schema + sharding
// metadata extension into a byte slice suitable for storage under
// encoding.ReservedSchemaName.
func buildSchemaDocPayload(schema *encoding.Schema, agg uint64, shardCount uint16) ([]byte, error) {
	var buf bytes.Buffer
	if err := encoding.WriteSchemaDoc(&buf, schema, agg, shardCount); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeArchiveEntry adds one store-only zip entry to zw.
func writeArchiveEntry(zw *zip.Writer, name string, payload []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating archive entry %q", name))
	}
	if _, err := w.Write(payload); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing archive entry %q", name))
	}
	return nil
}

// atomicWrite writes data to dst via the temp+fsync+rename pattern. Uses
// afero.TempFile so MemMapFs and OsFs both honour the contract. On
// failure the temp file is best-effort cleaned up; the canonical
// destination is never partially overwritten.
//
// Future optimisation: true in-place append for AddShard atop lower-
// level zip primitives. v1 reads the entire archive into memory and
// rewrites — semantically equivalent and crash-safe.
func atomicWrite(fsys afero.Fs, dst string, data []byte) error {
	dir := filepath.Dir(dst)
	if dir == "" || dir == "." {
		dir = ""
	}
	// MemMapFs ignores the dir argument; OsFs honours it. Both accept "".
	tmp, err := afero.TempFile(fsys, dir, filepath.Base(dst)+shardAdminTempPattern)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating temp file for atomic rename of %s", dst))
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing temp file for %s", dst))
	}
	// fsync BEFORE the rename: a rename that lands while the temp
	// file's contents are still only in the page cache publishes a name
	// pointing at bytes a crash can still lose. That matters most for
	// the set-widen rewrite, where the alternative to "old archive" is
	// not "missing archive" but "archive whose schema block promises a
	// stride its records do not have".
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("syncing temp file for %s", dst))
	}
	if err := tmp.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("closing temp file for %s", dst))
	}
	if err := fsys.Rename(tmpName, dst); err != nil {
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("renaming temp file %s to %s", tmpName, dst))
	}
	return nil
}
