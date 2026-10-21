package service

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// index_archive.go is point lookup over a SHARD ARCHIVE: the hybrid
// design (U51 #359). An archive index is
//
//   - one v3 sidecar per shard, at the shard's ANCHOR sidecar path
//     (encx.ArchiveShardIndexPath — "archive.pulse#shard.pulse.<hash>.idx"),
//     row ids in-shard, fingerprint and size over the shard's stored
//     payload: byte-identical to what an anchored build of that shard
//     writes, so the index format does not move and an anchored lookup
//     reads the same file;
//   - one entry in the archive's manifest ("archive.pulse.indexes.json")
//     listing every COVERED shard by name + size + zip-central-directory
//     CRC32 (encx.IndexManifestShard). The identity is never a bare
//     name: a same-name replacement moves its CRC32.
//
// Nothing is stored inside the zip. A lookup reads the central
// directory, refuses any shard the manifest does not cover
// (PULSE_INDEX_STALE, details.shards_uncovered — never a silent miss),
// fans the probe out across every shard, unions the hits in archive
// order and decodes each hit through its shard's section.

// anchorShardModTime is the source modification time an anchored shard
// reports (anchorFileInfo.ModTime, the zero time). An archive's
// per-shard sidecar records it as SourceModTime so the sidecar stays
// byte-identical to an anchored build's — and so an anchored lookup's
// stat check (size + this mtime) serves it hash-free. The archive path
// itself never consults it: shard identity there is name + size +
// CRC32.
func anchorShardModTime() int64 {
	return (&anchorFileInfo{}).ModTime().UnixNano()
}

// archiveShardIdentities maps each shard entry of the open archive to
// its central-directory record (name, size, CRC32).
func archiveShardIdentities(a *archiveFile) map[string]encx.ArchiveEntry {
	out := make(map[string]encx.ArchiveEntry)
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		out[e.Name] = e
	}
	return out
}

// buildArchiveIndex is BuildIndex for a shard archive: ONE sequential
// scan of every shard (the serial shard iterator, canonical schema,
// exact u64 words), each shard's key -> in-shard-row multimap laid out
// by the same indexBucketBuilder a single-file build uses and written
// to that shard's sidecar as soon as the scan leaves the shard, so at
// most one shard's keys are resident. Each sidecar fingerprints the
// shard's stored payload (a second read of that shard, as a single-file
// build re-reads the file). The manifest entry lists every shard with
// its identity and is upserted last.
func (s *Service) buildArchiveIndex(fsys afero.Fs, cohort *Cohort, path string, keyFields []string, keys []encx.IndexKeySpec, fields []*encoding.Field) (*BuildIndexResult, error) {
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	ident := archiveShardIdentities(a)

	shards := cohort.Shards()
	cover := make([]encx.IndexManifestShard, 0, len(shards))
	paths := make([]string, 0, len(shards))
	var b indexBucketBuilder
	flush := func(k int) error {
		name := shards[k].Filename
		e, ok := ident[name]
		if !ok {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
				fmt.Sprintf("shard %q not present in archive central directory", name),
				map[string]any{"archive": path, "entry": name})
		}
		sect, err := a.storedShard(name)
		if err != nil {
			return err
		}
		fp, err := encx.ComputeFingerprint(sect)
		if err != nil {
			return err
		}
		buckets := b.buckets()
		idx := &encx.Index{
			Fingerprint:   fp,
			Keys:          keys,
			Buckets:       buckets,
			SourceSize:    uint64(e.Size),
			SourceModTime: anchorShardModTime(),
		}
		shardPath := encx.ArchiveShardIndexPath(path, name, keyFields)
		if err := encx.WriteIndexFile(fsys, shardPath, idx); err != nil {
			return err
		}
		distinct, records := indexBucketCounts(buckets)
		cover = append(cover, encx.IndexManifestShard{
			Name: name, Size: e.Size, CRC32: e.CRC32,
			IndexPath: filepath.Base(shardPath), DistinctKeys: distinct, IndexedRecords: records,
		})
		paths = append(paths, shardPath)
		return nil
	}

	iter := newShardIter(fsys, path, cohort.Schema(), shards)
	defer iter.Close()
	next := 0
	for iter.Next() {
		for ; next < iter.shardIdx; next++ {
			if err := flush(next); err != nil {
				return nil, err
			}
		}
		if key, ok := processing.CompositeKeyFieldOnWireBytes(iter.Record(), fields); ok {
			b.add(key, uint64(iter.shardRec-1))
		}
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("scanning shard archive for index build: %s", path))
	}
	for ; next < len(shards); next++ {
		if err := flush(next); err != nil {
			return nil, err
		}
	}

	indexPath := encx.SidecarIndexPath(path, keyFields)
	entry := encx.IndexManifestEntryFor(indexPath, &encx.Index{Keys: keys})
	entry.Shards = cover
	for _, sh := range cover {
		entry.DistinctKeys += sh.DistinctKeys
		entry.IndexedRecords += sh.IndexedRecords
	}
	manifest, present, err := encx.ReadIndexManifest(fsys, path)
	if err != nil {
		return nil, err
	}
	if !present {
		manifest = encx.NewIndexManifest(path)
	}
	manifest.Upsert(entry)
	if err := encx.WriteIndexManifest(fsys, path, manifest); err != nil {
		return nil, err
	}
	return &BuildIndexResult{
		IndexPath:       indexPath,
		ManifestPath:    encx.IndexManifestPath(path),
		DistinctKeys:    entry.DistinctKeys,
		IndexedRecords:  entry.IndexedRecords,
		ShardIndexPaths: paths,
	}, nil
}

// archiveIndexEntry returns the archive's manifest entry for the
// ordered key tuple, or PULSE_INDEX_MISSING (details carry the index's
// logical path) when no manifest or no entry exists. An unreadable
// manifest keeps its PULSE_INDEX_MANIFEST_INVALID.
func archiveIndexEntry(fsys afero.Fs, path string, keyFields []string) (encx.IndexManifestEntry, error) {
	indexPath := encx.SidecarIndexPath(path, keyFields)
	manifest, present, err := encx.ReadIndexManifest(fsys, path)
	if err != nil {
		return encx.IndexManifestEntry{}, err
	}
	if present {
		base := filepath.Base(indexPath)
		for _, e := range manifest.Indexes {
			if e.IndexPath == base && len(e.Shards) > 0 {
				return e, nil
			}
		}
	}
	return encx.IndexManifestEntry{}, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MISSING,
		"no sidecar point-lookup index found for these key fields",
		map[string]any{"cohort": path, "fields": keyFields, "index_path": indexPath})
}

// archiveCoverage judges the manifest entry against the archive's
// CURRENT shards, in archive order: a shard is covered when an entry
// shard carries its name, size and central-directory CRC32. It returns
// the covered shards' manifest records (archive order, one per current
// shard) and the names of the uncovered ones. A manifest shard the
// archive no longer holds is ignored — it covers nothing that can be
// read.
func archiveCoverage(shards []ShardEntry, ident map[string]encx.ArchiveEntry, entry encx.IndexManifestEntry) ([]encx.IndexManifestShard, []string) {
	byName := make(map[string]encx.IndexManifestShard, len(entry.Shards))
	for _, sh := range entry.Shards {
		byName[sh.Name] = sh
	}
	covered := make([]encx.IndexManifestShard, 0, len(shards))
	var uncovered []string
	for _, sh := range shards {
		e := ident[sh.Filename]
		m, ok := byName[sh.Filename]
		if !ok || m.Size != e.Size || m.CRC32 != e.CRC32 {
			uncovered = append(uncovered, sh.Filename)
			continue
		}
		covered = append(covered, m)
	}
	return covered, uncovered
}

// archiveIndexStale is the PULSE_INDEX_STALE refusal for an archive
// index that does not cover every shard: details.shards_uncovered names
// them in archive order.
func archiveIndexStale(path string, keyFields []string, uncovered []string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_STALE,
		"sidecar point-lookup index does not cover every shard of the archive: a shard was added, replaced or rewritten after the index was built",
		map[string]any{
			"cohort":           path,
			"fields":           keyFields,
			"index_path":       encx.SidecarIndexPath(path, keyFields),
			"shards_uncovered": uncovered,
		})
}

// archiveShardSidecarMissing is the refusal for a covered shard whose
// sidecar file is gone: the manifest names a file that is not present.
func archiveShardSidecarMissing(path string, keyFields []string, sidecar string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_STALE,
		"sidecar index manifest names an index file that is not present",
		map[string]any{
			"manifest_path": encx.IndexManifestPath(path),
			"cohort":        path,
			"index_path":    sidecar,
			"fields":        keyFields,
		})
}

// archiveHit is one matched record of an archive lookup: the shard's
// position in archive order and its in-shard row id.
type archiveHit struct {
	shard int
	row   uint64
}

// lookupArchiveHits is the index half of an archive lookup: the
// coverage refusal, the key-arity check, the literal resolution (ONCE,
// against the CANONICAL schema — a shard dictionary is a prefix of it,
// so the resolved ids are every shard's ids and a literal absent from
// one shard simply finds nothing there) and the per-shard probe. Hits
// come back in archive order, ascending row id within a shard — the
// ascending global-ordinal order the single-file twin's row ids take.
// Every shard's sidecar is validated before any hit is returned, so a
// refusal is never partial.
func lookupArchiveHits(fsys afero.Fs, a *archiveFile, cohort *Cohort, path string, keyFieldNames []string, keyFields []*encoding.Field, literals []string) ([]archiveHit, error) {
	entry, err := archiveIndexEntry(fsys, path, keyFieldNames)
	if err != nil {
		return nil, err
	}
	shards := cohort.Shards()
	ident := archiveShardIdentities(a)
	covered, uncovered := archiveCoverage(shards, ident, entry)
	if len(uncovered) > 0 {
		return nil, archiveIndexStale(path, keyFieldNames, uncovered)
	}
	if len(entry.Keys) != len(literals) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"lookup key component count does not match the sidecar index's key-spec",
			map[string]any{"cohort": path, "requested": len(literals), "index_key_count": len(entry.Keys)})
	}
	keyBytes, err := processing.ResolveCompositeLookupKeyBytes(keyFields, literals)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	var hits []archiveHit
	for k, cov := range covered {
		rows, stale, err := probeShardSidecar(fsys, filepath.Join(dir, cov.IndexPath), cov.Size, len(literals), keyBytes)
		if err != nil {
			if errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
				return nil, archiveShardSidecarMissing(path, keyFieldNames, filepath.Join(dir, cov.IndexPath))
			}
			return nil, err
		}
		if stale {
			uncovered = append(uncovered, cov.Name)
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i] < rows[j] })
		for _, r := range rows {
			hits = append(hits, archiveHit{shard: k, row: r})
		}
	}
	if len(uncovered) > 0 {
		return nil, archiveIndexStale(path, keyFieldNames, uncovered)
	}
	return hits, nil
}

// probeShardSidecar reads one shard sidecar's prefix and the ONE bucket
// keyBytes hashes to, returning a copy of the matched row ids. stale
// reports a sidecar that does not describe the covered shard (its
// recorded source size differs, or its key arity does): it covers
// nothing. A missing file is PULSE_INDEX_MISSING for the caller to
// re-code.
func probeShardSidecar(fsys afero.Fs, sidecar string, size int64, arity int, keyBytes []byte) ([]uint64, bool, error) {
	f, err := fsys.Open(sidecar)
	if err != nil {
		if exists, xerr := afero.Exists(fsys, sidecar); xerr == nil && !exists {
			return nil, false, errors.NewCodedError(errors.PULSE_INDEX_MISSING, "shard sidecar index is not present")
		}
		return nil, false, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening shard sidecar index file: %s", sidecar))
	}
	defer f.Close()
	meta, err := encx.ReadIndexMeta(f)
	if err != nil {
		return nil, false, err
	}
	if meta.SourceSize != uint64(size) || len(meta.Keys) != arity {
		return nil, true, nil
	}
	bucket, err := encx.ReadBucketByKey(f, meta, keyBytes)
	if err != nil {
		return nil, false, err
	}
	entry, ok := findBucketEntry(bucket, keyBytes)
	if !ok {
		return nil, false, nil
	}
	return append([]uint64(nil), entry.RowIDs...), false, nil
}

// readArchiveHits decodes each hit through its shard's stored section
// (a segment-aware read: one RecordLocator per shard over the CANONICAL
// schema, exactly as the shard iterator decodes), projected by plan /
// keep, with the exact u64 words captured.
func readArchiveHits(a *archiveFile, cohort *Cohort, path string, hits []archiveHit, plan *encx.DecodePlan, keep encx.FieldFilter) ([]lookupMatch, error) {
	schema := cohort.Schema()
	shards := cohort.Shards()
	exact := encx.NewExactU64Words(schema)
	out := make([]lookupMatch, 0, len(hits))
	var (
		cur  = -1
		sect *io.SectionReader
		loc  *encoding.RecordLocator
	)
	for _, h := range hits {
		if h.shard != cur {
			name := shards[h.shard].Filename
			s, err := a.storedShard(name)
			if err != nil {
				return nil, err
			}
			l, err := shardRecordLocator(s, schema)
			if err != nil {
				return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
					"reading shard header for lookup record read: "+path+"#"+name)
			}
			cur, sect, loc = h.shard, s, l
		}
		values := make(map[string]float64)
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		if err := encx.ReadRecordAtExact(loc, sect, h.row, values, nulls, wide, keep, plan, exact); err != nil {
			return nil, err
		}
		rec := processing.NewRecordWithWide(schema, values, nulls, wide)
		exact.ApplyTo(rec)
		out = append(out, lookupMatch{rec: rec, schema: schema, path: path})
	}
	return out, nil
}

// shardRecordLocator measures where a stored shard's record region
// starts (its own header + schema, read layout-only) and returns a
// locator that decodes the region against canonical — the stride is
// canonical's, which insert-time cohesion keeps equal to the shard's.
func shardRecordLocator(sect *io.SectionReader, canonical *encoding.Schema) (*encoding.RecordLocator, error) {
	if _, err := sect.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	version, err := encoding.ReadHeader(sect)
	if err != nil {
		return nil, err
	}
	if _, err := encx.ReadSchemaLayout(sect, version); err != nil {
		return nil, err
	}
	start, err := sect.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	loc := &encoding.RecordLocator{
		Schema:            canonical,
		RecordRegionStart: start,
		Stride:            int64(canonical.RecordByteSize()),
	}
	if loc.Stride > 0 && sect.Size() > start {
		loc.TotalRecords = uint64((sect.Size() - start) / loc.Stride)
	}
	return loc, nil
}
