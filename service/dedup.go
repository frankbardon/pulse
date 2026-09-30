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
//   - SERVICE_VALIDATION — path is a shard archive (zip magic). Grouped
//     shard payloads are not accepted by the archive tooling yet (the
//     archive refuses a 0x02 shard with PULSE_SHARD_SCHEMA_MISMATCH), so
//     deduping an archive is refused here, explicitly, rather than
//     producing an archive the rest of the shard surface cannot open.
//     details.layout = "shard_archive".
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
			fmt.Sprintf("dedup does not support shard archive cohorts yet: %s is a shard archive (zip magic), and shard archives do not accept grouped (format 0x02) shards; dedup a single-file cohort, or extract the shards and dedup each before re-archiving is supported", path),
			map[string]any{"cohort": path, "layout": "shard_archive"})
	}
	return nil
}
