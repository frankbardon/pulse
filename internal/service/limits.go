package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// limitsPreflight is the process pre-flight for the instance resource
// limits: it refuses the first certain breach predict reports for req
// over schema (descx.LimitRefusal — the shared rule) with
// PULSE_LIMIT_EXCEEDED. Every Request arm calls it right after
// checkFieldRefs, so a certain breach is refused before any record is
// decoded. It reads only the schema and the header counts in in
// (limitInputs; a join host's built by openJoinStream).
func (s *Service) limitsPreflight(req *types.Request, schema *encoding.Schema, in descx.LimitInputs) error {
	if err := descx.LimitRefusal(req, schema, s.instance, s.Limits(), in); err != nil {
		return markLocated(err)
	}
	return nil
}

// MemoryBounded reports whether the instance sets MaxEstimatedMemory —
// the only limit predict's memory estimate (and so its parallel-arm
// probes) serves.
func (s *Service) MemoryBounded() bool { return s.memoryBounded() }

// memoryBounded reports whether MaxEstimatedMemory is set — the only
// rule that needs the record counts, so an Unlimited memory limit (the
// default) reads none.
func (s *Service) memoryBounded() bool {
	return !limits.IsUnlimited(s.Limits().MaxEstimatedMemory)
}

// limitInputs are the run facts the pre-flight reads for a request
// scanning cohort (opened from path) — the figures predict reads from
// the same header (cohortCounts). Counts are read only when
// MaxEstimatedMemory is set; otherwise Records is -1 (unknown), which
// no refusing rule needs.
func (s *Service) limitInputs(ctx context.Context, cohort *Cohort, path string) (descx.LimitInputs, error) {
	in := descx.LimitInputs{
		Records:        -1,
		JoinRightRows:  -1,
		Extensions:     s.ExtensionsSnapshot(),
		FusionDisabled: s.disableCrosstabFusion,
		ShardWorkers:   s.shardWorkers,
		DecodeWorkers:  s.decodeWorkers,
		DefaultWeight:  s.defaultWeight,
	}
	if !s.memoryBounded() {
		return in, nil
	}
	in.Mappable = decodeMappable(cohort)
	c, err := s.cohortCounts(ctx, cohort, path)
	if err != nil {
		return in, err
	}
	in.Records, in.ShardRecords, in.DirectoryBytes = c.Records, c.ShardRecords, c.DirectoryBytes
	return in, nil
}

// decodeMappable reports whether the parallel-decode reducer can map
// cohort — the fs half of canParallelDecode: a single file (no shards)
// on an fs that resolves a real on-disk path. Predict reads the same
// answer through DecodeMappable.
func decodeMappable(cohort *Cohort) bool {
	if cohort == nil || cohort.fs == nil || len(cohort.Shards()) > 0 {
		return false
	}
	_, ok := resolveRealPath(cohort.fs, cohort.path)
	return ok
}

// DecodeMappable opens the cohort at path and reports whether the
// parallel-decode reducer could map it (decodeMappable) — predict's
// PredictOptions.DecodeMappable, so the memory estimate picks the arm
// the run picks. An open failure answers false; the pass that loads the
// schema reports it. No record is read.
func (s *Service) DecodeMappable(ctx context.Context, path string) bool {
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return false
	}
	return decodeMappable(cohort)
}

// cohortCounts are the header facts of cohort (opened from path) the
// limit rules read: an archive's per-shard counts from its manifest and
// its central-directory bytes (what Open parsed), a single file's — or
// an anchor-opened shard's — header-only count (CountRecords, never
// Cohort.RecordCount).
func (s *Service) cohortCounts(ctx context.Context, cohort *Cohort, path string) (descx.CohortCounts, error) {
	var c descx.CohortCounts
	if shards := cohort.Shards(); len(shards) > 0 {
		for _, sh := range shards {
			c.ShardRecords = append(c.ShardRecords, sh.RecordCount)
			c.Records += sh.RecordCount
		}
		c.DirectoryBytes = cohort.directoryBytes
		return c, nil
	}
	n, err := s.CountRecords(ctx, path)
	if err != nil {
		return descx.CohortCounts{Records: -1}, err
	}
	c.Records = int64(n)
	return c, nil
}

// CohortCounts opens the cohort at path and returns the header facts
// the limit pre-flight reads for it (cohortCounts) whether or not a
// memory limit is set — predict's PredictOptions.CohortCounter, so a
// Compose slot's limit findings carry the runtime's own figures. No
// record is read.
func (s *Service) CohortCounts(ctx context.Context, path string) (descx.CohortCounts, error) {
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return descx.CohortCounts{Records: -1}, err
	}
	return s.cohortCounts(ctx, cohort, path)
}

// composeSlotsPreflight refuses a Compose call carrying more than
// MaxComposeSlots slots — explicit plus sweep-expanded — the whole call,
// before any slot runs (FailFast does not apply). sweep.Expand calls it
// with the total before rendering a single sweep slot.
func (s *Service) composeSlotsPreflight(slots int) error {
	if err := limits.CheckComposeSlots(s.Limits(), slots); err != nil {
		return err
	}
	return nil
}
