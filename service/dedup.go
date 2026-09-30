package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// DedupPreflight performs the layout dispatch a retro-dedup needs
// before the byte work starts (io.DedupJob, which speaks only the
// single-file layout). It mirrors WidenSetField's dispatch exactly:
//
//   - SERVICE_VALIDATION — path is an anchored shard
//     (`archive.pulse#shard.pulse`): a shard inside an archive cannot
//     be regrouped alone, because every shard shares the canonical
//     schema.
//   - SERVICE_VALIDATION — path is a shard archive (zip magic).
//     details.layout = "shard_archive". Shard archives DO carry grouped
//     shards (shard create/add union-merge their dictionaries), but an
//     archive-wide dedup is a different operation from a per-file one:
//     the viability gate, constant elision and group suggestion would
//     have to be decided over the union of every shard (a cross-shard
//     observation pass), and every shard rewritten atomically to one
//     layout. That is not built; the supported route is to dedup each
//     source shard (or `shard extract` + dedup) and `shard create` the
//     results, which reconciles them to one layout.
//   - SERVICE_RESOURCE — the cohort does not exist / cannot be opened.
//
// A nil return means path is a single-file cohort the job may read.
func (s *Service) DedupPreflight(_ context.Context, path string) error {
	if archivePath, anchor, ok := splitAnchorPath(path); ok {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("dedup does not accept an anchored shard path (%s): a shard inside an archive cannot be regrouped alone, because every shard in an archive shares the canonical schema", path),
			map[string]any{"cohort": path, "archive": archivePath, "shard": anchor})
	}
	archive, err := s.pathIsShardArchive(path)
	if err != nil {
		return err
	}
	if archive {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("dedup does not support shard archive cohorts: %s is a shard archive (zip magic), and an archive-wide dedup would have to decide groups over every shard at once; dedup each source shard (or `pulse shard extract` then dedup) and `pulse shard create` the results — archives union-merge grouped shards", path),
			map[string]any{"cohort": path, "layout": "shard_archive"})
	}
	return nil
}
