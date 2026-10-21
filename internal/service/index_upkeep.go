package service

import (
	"fmt"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// index_upkeep.go keeps an archive's point-lookup indexes in step with
// `shard add` / `shard remove` (U51 #359). An archive index is one v3
// sidecar per shard plus a manifest entry recording each covered
// shard's identity (index_archive.go), so:
//
//   - an ordinary add (stored shards copied raw) builds the NEW shard's
//     sidecar for every archive index — O(new shard), one scan for all
//     indexes — and appends its record to each entry;
//   - a remove drops the removed shard's record from each entry and
//     deletes its sidecar, touching no other file;
//   - an add that re-encodes the stored shards (an archive-level set
//     widen, a constant-group promotion) maintains nothing: every stored
//     shard's CRC32 moves, so each per-shard sidecar is reported
//     invalidated (AddShardResult.InvalidatedSidecars) and lookups refuse
//     with PULSE_INDEX_STALE until the index is rebuilt.
//
// BUILD BEFORE PUBLISH. Every new sidecar and the updated manifest are
// written to stages beside their final paths while the new archive is
// still itself a stage; any failure up to that point aborts the whole
// mutation and leaves the archive, every sidecar and the manifest
// byte-identical. Only then is the archive renamed, then each sidecar,
// then the manifest — the order in which a reader that lands between
// two renames sees an uncovered shard (a loud PULSE_INDEX_STALE), never
// a sidecar describing bytes the archive does not hold. A rename that
// fails AFTER the archive is published cannot be rolled back; it is
// reported as a PULSE_INDEX_STALE warning naming the shard the index no
// longer covers, and the mutation itself stands.

// stagedFile is one upkeep output: written at stage, renamed to final
// on publish.
type stagedFile struct {
	stage, final string
}

// indexUpkeep is the staged index half of one shard add / remove.
type indexUpkeep struct {
	fsys    afero.Fs
	archive string
	shard   string
	action  string // "added" | "removed"

	sidecars []stagedFile
	manifest stagedFile
	// remove names the sidecar files a remove deletes once the
	// manifest no longer names them.
	remove []string
	// touched is the warning's per-index detail, one per entry changed.
	touched []map[string]any
	done    bool
}

// upkeepStagePath is the deterministic stage of a sidecar or manifest:
// `<final>.pulse.tmp-staged` beside it, swept like an archive stage.
func upkeepStagePath(final string) string {
	return final + shardAdminTempPattern + shardStagedSuffix
}

// readArchiveIndexes loads the archive's index manifest and returns it
// with the positions of its ARCHIVE entries (those recording shards).
// No manifest, or one with no archive entry, is (nil, nil, nil): there
// is nothing to keep in step. An unreadable manifest keeps its
// PULSE_INDEX_MANIFEST_INVALID and aborts the mutation — the add or
// remove cannot keep an index it cannot read.
func readArchiveIndexes(fsys afero.Fs, archivePath string) (*encx.IndexManifest, []int, error) {
	manifest, present, err := encx.ReadIndexManifest(fsys, archivePath)
	if err != nil || !present {
		return nil, nil, err
	}
	var entries []int
	for i, e := range manifest.Indexes {
		if len(e.Shards) > 0 {
			entries = append(entries, i)
		}
	}
	if len(entries) == 0 {
		return nil, nil, nil
	}
	return manifest, entries, nil
}

// staleArchiveSidecars is every per-shard sidecar the manifest's archive
// entries name, in manifest order then archive order — what
// Pulse.InvalidatedSidecars reports for the archive — with the
// archive-wide rebuild command.
func staleArchiveSidecars(archivePath string, manifest *encx.IndexManifest, entries []int) []StaleSidecar {
	out := []StaleSidecar{}
	dir := filepath.Dir(archivePath)
	for _, i := range entries {
		e := manifest.Indexes[i]
		keys := e.KeyNames()
		for _, sh := range e.Shards {
			out = append(out, StaleSidecar{
				Path:    filepath.Join(dir, sh.IndexPath),
				Kind:    SidecarKindPointLookupIndex,
				Keys:    append([]string(nil), keys...),
				Rebuild: RebuildIndexCommand(archivePath, keys),
			})
		}
	}
	return out
}

// upkeepKeyFields resolves an archive entry's ordered key tuple against
// the archive's (new) canonical schema. A key the schema no longer
// holds, holds at another type, or cannot key an index means the
// manifest does not describe this archive: PULSE_INDEX_MANIFEST_INVALID,
// and the mutation aborts.
func upkeepKeyFields(archivePath string, schema *encoding.Schema, e encx.IndexManifestEntry) ([]encx.IndexKeySpec, []*encoding.Field, error) {
	keys := make([]encx.IndexKeySpec, 0, len(e.Keys))
	fields := make([]*encoding.Field, 0, len(e.Keys))
	for _, k := range e.Keys {
		f := schema.Field(k.Name)
		if f == nil || f.Type.String() != k.Type || !processing.IsIndexKeyableFieldType(f.Type) {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_INVALID,
				"sidecar index manifest names a key field the archive's schema does not hold at the recorded type",
				map[string]any{
					"manifest_path": encx.IndexManifestPath(archivePath),
					"cohort":        archivePath,
					"index_path":    e.IndexPath,
					"field":         k.Name,
					"type":          k.Type,
					"reason":        "key_field_not_in_schema",
				})
		}
		keys = append(keys, encx.IndexKeySpec{Name: f.Name, Type: f.Type})
		fields = append(fields, f)
	}
	return keys, fields, nil
}

// stageAddUpkeep builds, for every archive index in manifest, the new
// shard's sidecar from the FINISHED BUT UNPUBLISHED archive at staged
// (its central directory carries the identity the published archive
// will) and the manifest with the shard's record appended, all into
// stages. One scan of the new shard feeds every index. On error every
// stage written so far is removed.
func stageAddUpkeep(fsys afero.Fs, archivePath, staged string, schema *encoding.Schema, shard string, manifest *encx.IndexManifest, entries []int) (_ *indexUpkeep, err error) {
	u := &indexUpkeep{fsys: fsys, archive: archivePath, shard: shard, action: "added"}
	defer func() {
		if err != nil {
			u.abort()
		}
	}()

	keys := make([][]encx.IndexKeySpec, len(entries))
	fields := make([][]*encoding.Field, len(entries))
	builders := make([]indexBucketBuilder, len(entries))
	for j, i := range entries {
		if keys[j], fields[j], err = upkeepKeyFields(archivePath, schema, manifest.Indexes[i]); err != nil {
			return nil, err
		}
	}

	iter := newShardIter(fsys, staged, schema, []ShardEntry{{Filename: shard}})
	for iter.Next() {
		for j := range entries {
			if key, ok := processing.CompositeKeyFieldOnWireBytes(iter.Record(), fields[j]); ok {
				builders[j].add(key, uint64(iter.shardRec-1))
			}
		}
	}
	iterErr := iter.Err()
	iter.Close()
	if iterErr != nil {
		return nil, errors.WrapCodedError(iterErr, errors.SERVICE_RESOURCE,
			fmt.Sprintf("scanning shard %q for index upkeep: %s", shard, archivePath))
	}

	a, err := openArchiveFile(fsys, staged)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	e, ok := archiveShardIdentities(a)[shard]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			fmt.Sprintf("shard %q not present in staged archive central directory", shard),
			map[string]any{"archive": archivePath, "entry": shard})
	}

	dir := filepath.Dir(archivePath)
	for j, i := range entries {
		entry := &manifest.Indexes[i]
		buckets := builders[j].buckets()
		idx, err := shardSidecarIndex(a, e, keys[j], buckets)
		if err != nil {
			return nil, err
		}
		final := encx.ArchiveShardIndexPath(archivePath, shard, entry.KeyNames())
		sf := stagedFile{stage: upkeepStagePath(final), final: final}
		u.sidecars = append(u.sidecars, sf)
		if err := encx.WriteIndexFile(fsys, sf.stage, idx); err != nil {
			return nil, err
		}
		distinct, records := indexBucketCounts(buckets)
		// A record already under this name (a shard removed by a path
		// that did not keep the index) describes other bytes: replace it.
		kept := entry.Shards[:0:0]
		for _, old := range entry.Shards {
			if old.Name == shard {
				entry.DistinctKeys -= old.DistinctKeys
				entry.IndexedRecords -= old.IndexedRecords
				continue
			}
			kept = append(kept, old)
		}
		entry.Shards = append(kept, encx.IndexManifestShard{
			Name: shard, Size: e.Size, CRC32: e.CRC32,
			IndexPath: filepath.Base(final), DistinctKeys: distinct, IndexedRecords: records,
		})
		entry.DistinctKeys += distinct
		entry.IndexedRecords += records
		u.touched = append(u.touched, map[string]any{
			"keys":             entry.KeyNames(),
			"index_path":       filepath.Join(dir, entry.IndexPath),
			"shard_index_path": final,
		})
	}
	if err := u.stageManifest(manifest); err != nil {
		return nil, err
	}
	return u, nil
}

// stageRemoveUpkeep drops shard's record from every archive index that
// covers it and stages the updated manifest; the sidecars it names are
// deleted on publish. An index whose last shard goes loses its entry.
// Nil when no index covers the shard.
func stageRemoveUpkeep(fsys afero.Fs, archivePath, shard string, manifest *encx.IndexManifest, entries []int) (*indexUpkeep, error) {
	u := &indexUpkeep{fsys: fsys, archive: archivePath, shard: shard, action: "removed"}
	dir := filepath.Dir(archivePath)
	var drop []string
	for _, i := range entries {
		entry := &manifest.Indexes[i]
		kept := entry.Shards[:0:0]
		var gone *encx.IndexManifestShard
		for k := range entry.Shards {
			if entry.Shards[k].Name == shard {
				gone = &entry.Shards[k]
				continue
			}
			kept = append(kept, entry.Shards[k])
		}
		if gone == nil {
			continue
		}
		sidecar := filepath.Join(dir, gone.IndexPath)
		u.remove = append(u.remove, sidecar)
		if derived := encx.ArchiveShardIndexPath(archivePath, shard, entry.KeyNames()); derived != sidecar {
			u.remove = append(u.remove, derived)
		}
		entry.DistinctKeys -= gone.DistinctKeys
		entry.IndexedRecords -= gone.IndexedRecords
		u.touched = append(u.touched, map[string]any{
			"keys":             entry.KeyNames(),
			"index_path":       filepath.Join(dir, entry.IndexPath),
			"shard_index_path": sidecar,
		})
		entry.Shards = kept
		if len(kept) == 0 {
			drop = append(drop, entry.IndexPath)
		}
	}
	if len(u.touched) == 0 {
		return nil, nil
	}
	for _, base := range drop {
		manifest.Remove(base)
	}
	if err := u.stageManifest(manifest); err != nil {
		u.abort()
		return nil, err
	}
	return u, nil
}

// stageManifest renders the updated manifest into its stage.
func (u *indexUpkeep) stageManifest(manifest *encx.IndexManifest) error {
	raw, err := encx.MarshalIndexManifest(u.archive, manifest)
	if err != nil {
		return err
	}
	final := encx.IndexManifestPath(u.archive)
	u.manifest = stagedFile{stage: upkeepStagePath(final), final: final}
	if err := afero.WriteFile(u.fsys, u.manifest.stage, raw, 0o644); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("staging sidecar index manifest: %s", u.manifest.final))
	}
	return nil
}

// abort removes every stage. Idempotent; a no-op after publish.
func (u *indexUpkeep) abort() {
	if u == nil || u.done {
		return
	}
	u.done = true
	for _, sf := range u.sidecars {
		_ = u.fsys.Remove(sf.stage)
	}
	if u.manifest.stage != "" {
		_ = u.fsys.Remove(u.manifest.stage)
	}
}

// publish renames the staged sidecars, then the manifest, then deletes
// a remove's orphaned sidecars — called only after the archive rename.
// It returns the mandatory PULSE_SHARD_INDEXES_UPDATED warning, or, if a
// step fails, the PULSE_INDEX_STALE warning naming what the index no
// longer covers (the archive is already published, so the mutation
// stands and the index refuses loudly until rebuilt).
func (u *indexUpkeep) publish() encx.CohesionWarning {
	u.done = true
	for k, sf := range u.sidecars {
		if err := u.fsys.Rename(sf.stage, sf.final); err != nil {
			for _, rest := range u.sidecars[k:] {
				_ = u.fsys.Remove(rest.stage)
			}
			_ = u.fsys.Remove(u.manifest.stage)
			return u.failed(err)
		}
	}
	if err := u.fsys.Rename(u.manifest.stage, u.manifest.final); err != nil {
		_ = u.fsys.Remove(u.manifest.stage)
		return u.failed(err)
	}
	for _, p := range u.remove {
		// Best effort, like the archive stage sweep: the manifest no
		// longer names the file, so a leftover is unreferenced and a
		// later add of the same name overwrites it.
		_ = u.fsys.Remove(p)
	}
	verb := "extended"
	what := "with the new shard's sidecar"
	if u.action == "removed" {
		verb, what = "pruned", "of the removed shard's sidecar"
	}
	return encx.CohesionWarning{
		Code: string(errors.PULSE_SHARD_INDEXES_UPDATED),
		Message: fmt.Sprintf("shard %s %s: %d point-lookup index(es) %s %s and the index manifest updated",
			u.action, u.shard, len(u.touched), verb, what),
		Details: map[string]any{
			"archive": u.archive,
			"shard":   u.shard,
			"action":  u.action,
			"indexes": u.touched,
		},
	}
}

// failed is the post-publish PULSE_INDEX_STALE warning: the archive
// holds the mutation, the index does not.
func (u *indexUpkeep) failed(cause error) encx.CohesionWarning {
	w := encx.CohesionWarning{
		Code: string(errors.PULSE_INDEX_STALE),
		Message: fmt.Sprintf("shard %s %s, but publishing the point-lookup index upkeep failed (%v): lookups refuse until the index is rebuilt",
			u.action, u.shard, cause),
		Details: map[string]any{
			"cohort":  u.archive,
			"shard":   u.shard,
			"action":  u.action,
			"indexes": u.touched,
			"reason":  "index_upkeep_publish_failed",
		},
	}
	if u.action == "added" {
		w.Details["shards_uncovered"] = []string{u.shard}
	}
	return w
}
