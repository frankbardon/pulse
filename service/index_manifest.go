package service

import (
	"fmt"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// recordIndexInManifest upserts the entry describing idx (just written
// to indexPath) into cohortPath's sidecar index manifest, creating the
// manifest when it does not exist yet, and returns the manifest's path.
//
// A manifest being CREATED is additionally SEEDED from the directory
// listing when the filesystem supports one: a corpus indexed by a Pulse
// that predates the manifest has sidecars nobody recorded, and without
// the seed the first new build would publish a manifest naming only
// itself — which a manifest-first listing then reports as the whole
// truth. Seeding is best-effort by design: a backend with no directory
// listing (object storage) is exactly the case the manifest exists for,
// and there is nothing there to migrate that a listing could have
// found anyway. Service.ListIndexes covers the remaining gap from the
// other side by unioning the on-disk sidecars it can see.
//
// A manifest write failure is a hard error rather than a warning: the
// sidecar is written by then, but an index nothing can discover is not
// meaningfully built, and BuildIndex is idempotent so a retry is free.
func (s *Service) recordIndexInManifest(fsys afero.Fs, cohortPath, indexPath string, idx *encoding.Index) (string, error) {
	manifest, present, err := encoding.ReadIndexManifest(fsys, cohortPath)
	if err != nil {
		return "", err
	}
	if !present {
		manifest = encoding.NewIndexManifest(cohortPath)
		seedIndexManifest(fsys, cohortPath, manifest)
	}

	manifest.Upsert(encoding.IndexManifestEntryFor(indexPath, idx))
	if err := encoding.WriteIndexManifest(fsys, cohortPath, manifest); err != nil {
		return "", err
	}
	return encoding.IndexManifestPath(cohortPath), nil
}

// seedIndexManifest adds an entry for every sidecar already on disk
// beside cohortPath. Best-effort: any failure to list the directory or
// to read one sidecar leaves the manifest as it was, because a seed
// that cannot complete must not stop the build whose index it was
// merely trying to keep company.
func seedIndexManifest(fsys afero.Fs, cohortPath string, manifest *encoding.IndexManifest) {
	dir := filepath.Dir(cohortPath)
	re := sidecarIndexNameRegexp(filepath.Base(cohortPath))

	entries, err := afero.ReadDir(fsys, dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() || !re.MatchString(ent.Name()) {
			continue
		}
		idx, err := encoding.ReadIndexFile(fsys, filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		manifest.Upsert(encoding.IndexManifestEntryFor(ent.Name(), idx))
	}
}

// forgetIndexInManifest drops indexPath's entry from cohortPath's
// manifest, reporting whether an entry was present. A cohort with no
// manifest reports false with no error — there is nothing to prune.
//
// This is what makes `pulse index drop` the repair path for
// PULSE_INDEX_MANIFEST_STALE: an entry can be pruned even when the
// sidecar file it names is already gone.
func (s *Service) forgetIndexInManifest(fsys afero.Fs, cohortPath, indexPath string) (bool, error) {
	manifest, present, err := encoding.ReadIndexManifest(fsys, cohortPath)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	if !manifest.Remove(filepath.Base(indexPath)) {
		return false, nil
	}
	if err := encoding.WriteIndexManifest(fsys, cohortPath, manifest); err != nil {
		return false, err
	}
	return true, nil
}

// listIndexesFromManifest turns a parsed manifest into IndexInfo
// values WITHOUT reading a single sidecar's bucket table — the counts
// ride the manifest for exactly this reason, so discovering a cohort's
// indexes over object storage costs one GET for the document plus one
// existence probe (a HEAD) per entry, rather than a full download of
// every index.
//
// A named sidecar that is not present is PULSE_INDEX_MANIFEST_STALE,
// not a skipped entry: a listing that quietly reports two indexes for a
// manifest claiming three is indistinguishable from a correct answer.
func listIndexesFromManifest(fsys afero.Fs, cohortPath string, manifest *encoding.IndexManifest) ([]IndexInfo, error) {
	dir := filepath.Dir(cohortPath)
	out := make([]IndexInfo, 0, len(manifest.Indexes))

	for _, entry := range manifest.Indexes {
		full := filepath.Join(dir, entry.IndexPath)
		exists, err := afero.Exists(fsys, full)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				fmt.Sprintf("checking manifest-named sidecar index existence: %s", full))
		}
		if !exists {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_STALE,
				"sidecar index manifest names an index file that is not present",
				map[string]any{
					"manifest_path": encoding.IndexManifestPath(cohortPath),
					"cohort":        cohortPath,
					"index_path":    full,
					"fields":        entry.KeyNames(),
				})
		}
		out = append(out, IndexInfo{
			IndexPath:      full,
			Keys:           entry.KeyNames(),
			DistinctKeys:   entry.DistinctKeys,
			IndexedRecords: entry.IndexedRecords,
		})
	}
	return out, nil
}
