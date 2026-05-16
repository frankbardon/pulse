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
// Errors:
//   - PULSE_SHARD_RESERVED_NAME — any shardPath has basename `_schema.pulse`.
//   - PULSE_SHARD_NAME_COLLISION — two shardPaths share a basename.
//   - PULSE_SHARD_SCHEMA_MISMATCH / PULSE_SHARD_DICT_DIVERGENCE — propagated from cohesion validators.
//   - SERVICE_VALIDATION — empty shardPaths.
//   - SERVICE_RESOURCE — I/O failure on any input/output.
func (s *Service) CreateShardArchive(ctx context.Context, archivePath string, shardPaths []string) error {
	if len(shardPaths) == 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION,
			"CreateShardArchive requires at least one shard path")
	}

	fsys := s.fs.Fs()

	// Reserved-name + basename-collision precheck. Both errors are
	// caller-fixable; surface them before any I/O.
	if err := validateBasenames(shardPaths); err != nil {
		return err
	}

	// Read all shard payloads + validate cohesion + accumulate canonical schema.
	type incoming struct {
		basename string
		payload  []byte
		schema   *encoding.Schema
	}
	shards := make([]incoming, 0, len(shardPaths))

	var canonical *encoding.Schema
	for i, p := range shardPaths {
		data, err := afero.ReadFile(fsys, p)
		if err != nil {
			return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				fmt.Sprintf("CreateShardArchive: reading shard source %s", p))
		}
		schema, err := readSinglePulseSchema(data)
		if err != nil {
			return err
		}
		base := filepath.Base(p)
		if i == 0 {
			canonical = cloneSchemaForArchive(schema)
		} else {
			if _, err := encoding.ValidateStructuralCohesion(canonical, schema); err != nil {
				return err
			}
			extended, err := encoding.ValidateDictPrefixRule(canonical, schema)
			if err != nil {
				return err
			}
			canonical = extended
		}
		shards = append(shards, incoming{basename: base, payload: data, schema: schema})
	}

	var aggregate uint64
	for _, sh := range shards {
		n, err := recordCountFromBytes(sh.payload, canonical)
		if err != nil {
			return err
		}
		aggregate += uint64(n)
	}

	// Assemble the archive bytes in memory.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	schemaPayload, err := buildSchemaDocPayload(canonical, aggregate, uint16(len(shards)))
	if err != nil {
		return err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return err
	}
	for _, sh := range shards {
		if err := writeArchiveEntry(zw, sh.basename, sh.payload); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"CreateShardArchive: finalising zip writer")
	}

	return atomicWrite(fsys, archivePath, buf.Bytes())
}

// AddShard appends shardPath to the archive at archivePath. The
// incoming shard is validated against the archive's canonical schema
// via ValidateStructuralCohesion + ValidateDictPrefixRule. If the dict
// prefix rule extends the canonical dictionary, the rewritten
// `_schema.pulse` payload reflects the extension.
//
// v1 strategy: read the full archive into memory, append the new shard
// entry, rewrite the central directory + EOCD, and write the resulting
// bytes back via the atomic temp+rename pattern (§7.1). A future PR may
// implement true in-place append on top of lower-level zip primitives;
// the in-memory rebuild keeps semantics identical and crash-safe with
// minimal code surface.
func (s *Service) AddShard(ctx context.Context, archivePath, shardPath string) error {
	fsys := s.fs.Fs()

	base := filepath.Base(shardPath)
	if base == encoding.ReservedSchemaName {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			fmt.Sprintf("cannot add a shard with the reserved basename %q", encoding.ReservedSchemaName),
			map[string]any{"basename": base})
	}

	archiveBytes, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: reading archive %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return err
	}

	// Read incoming shard bytes.
	incomingBytes, err := afero.ReadFile(fsys, shardPath)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: reading shard source %s", shardPath))
	}
	incomingSchema, err := readSinglePulseSchema(incomingBytes)
	if err != nil {
		return err
	}

	// Read canonical schema doc.
	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return err
	}

	// Cohesion + dict prefix.
	if _, err := encoding.ValidateStructuralCohesion(canonicalDoc.Schema, incomingSchema); err != nil {
		return err
	}
	canonicalSchema, err := encoding.ValidateDictPrefixRule(canonicalDoc.Schema, incomingSchema)
	if err != nil {
		return err
	}

	// Enumerate existing shard payloads, detecting name collision.
	type entry struct {
		name    string
		payload []byte
	}
	existing := make([]entry, 0, len(arch.Entries()))
	for _, e := range arch.Entries() {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}
		if e.Name == base {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_NAME_COLLISION,
				fmt.Sprintf("archive already contains a shard named %q", base),
				map[string]any{"basename": base})
		}
		payload, perr := readEntryBytes(arch, e.Name)
		if perr != nil {
			return perr
		}
		existing = append(existing, entry{name: e.Name, payload: payload})
	}

	// Aggregate record count: sum across existing shards + the new
	// shard, computed from per-shard payloads. We re-peek each existing
	// shard's record count rather than trust _schema.pulse's cached
	// aggregate (the cached value may lag if the archive was edited
	// outside Pulse).
	newShardCount := uint16(len(existing) + 1)
	var aggregate uint64
	for _, e := range existing {
		n, perr := recordCountFromBytes(e.payload, canonicalSchema)
		if perr != nil {
			return perr
		}
		aggregate += uint64(n)
	}
	{
		n, perr := recordCountFromBytes(incomingBytes, canonicalSchema)
		if perr != nil {
			return perr
		}
		aggregate += uint64(n)
	}

	// Rebuild the archive: canonical schema (possibly extended) + every
	// previously-stored shard + the new shard at the tail.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	schemaPayload, err := buildSchemaDocPayload(canonicalSchema, aggregate, newShardCount)
	if err != nil {
		return err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return err
	}
	for _, e := range existing {
		if err := writeArchiveEntry(zw, e.name, e.payload); err != nil {
			return err
		}
	}
	if err := writeArchiveEntry(zw, base, incomingBytes); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"AddShard: finalising zip writer")
	}

	return atomicWrite(fsys, archivePath, buf.Bytes())
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

// ---- helpers ----

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
// payload bytes against the canonical schema's per-record size. Bit-
// packed-only schemas (ByteSize() == 0) return 0; that mirrors
// PeekShardRecordCount's conservative fallback.
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
	recordSize := 0
	for _, f := range schema.Fields {
		recordSize += f.Type.ByteSize()
	}
	if recordSize == 0 {
		return 0, nil
	}
	return int64(r.Len()) / int64(recordSize), nil
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

// atomicWrite writes data to dst via the temp+rename pattern. Uses
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
