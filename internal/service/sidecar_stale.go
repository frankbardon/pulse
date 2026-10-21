package service

import (
	"context"
	"fmt"
	"strings"
)

// SidecarKindPointLookupIndex is StaleSidecar.Kind for a point-lookup
// index sidecar (`cohort.pulse.<keyhash>.idx`, or one per shard of an
// archive index). The root package re-exports it.
const SidecarKindPointLookupIndex = "point_lookup_index"

// StaleSidecar names one sidecar document beside a cohort that a
// cohort-rewriting operation has invalidated.
//
// Nothing here rebuilds anything, and that is the decision rather than
// an omission: a widen is a fast, bounded metadata operation, and
// rebuilding an index over a multi-million-row cohort behind it would
// turn it into an arbitrarily long one with no way for the caller to
// opt out. Both sidecars refuse to serve stale data on their own —
// their fingerprints cover the cohort's SIZE, which a widen always
// changes — so the risk being closed is not silent corruption but
// silent BREAKAGE: a corpus that stops answering lookups with nothing
// having said why.
type StaleSidecar struct {
	// Path is the sidecar's path, derived the same way its owner
	// derives it. Never guessed from a filename pattern.
	Path string `json:"path"`
	// Kind is one of the SidecarKind* constants.
	Kind string `json:"kind"`
	// Keys is the point-lookup index's ordered key tuple, empty for
	// every other kind. Order is significant: a reversed tuple is a
	// different index at a different path.
	Keys []string `json:"keys,omitempty"`
	// Rebuild is the command that regenerates this sidecar.
	//
	// For a point-lookup index it is exact and runnable, which is the
	// whole reason the key tuple has to be recovered first: the
	// sidecar's filename is a HASH of that tuple, so the command cannot
	// be reconstructed from the path. For the SPSS metadata sidecar it
	// names the import that produces one and leaves the source `.sav`
	// as a placeholder — the sidecar holds dictionary detail the
	// `.pulse` format has no slot for, so it cannot be regenerated from
	// the cohort at all, and pretending otherwise would be worse than
	// the placeholder.
	Rebuild string `json:"rebuild"`
}

// InvalidatedIndexSidecars is the point-lookup half of
// Pulse.InvalidatedSidecars: every index sidecar beside the cohort at
// path, discovered through ListIndexes (never a filename guess), in
// ListIndexes order. A shard archive's index is one sidecar per shard,
// and each is reported on its own with the archive-wide rebuild
// command. AddShard reports exactly this list when it re-encodes the
// archive's stored shards (a set widen, a constant-group promotion).
func (s *Service) InvalidatedIndexSidecars(ctx context.Context, path string) ([]StaleSidecar, error) {
	indexes, err := s.ListIndexes(ctx, path)
	if err != nil {
		return nil, err
	}
	out := []StaleSidecar{}
	for _, idx := range indexes {
		paths := idx.ShardIndexPaths
		if len(paths) == 0 {
			paths = []string{idx.IndexPath}
		}
		for _, p := range paths {
			out = append(out, StaleSidecar{
				Path:    p,
				Kind:    SidecarKindPointLookupIndex,
				Keys:    append([]string(nil), idx.Keys...),
				Rebuild: RebuildIndexCommand(path, idx.Keys),
			})
		}
	}
	return out, nil
}

// RebuildIndexCommand renders the exact `pulse index build` invocation
// that reproduces an index over keys. The tuple is comma-joined in
// ORDER, matching the --key flag's own composite syntax, because key
// order is significant end to end and a reordered tuple builds a
// different index at a different path.
func RebuildIndexCommand(cohortPath string, keys []string) string {
	return fmt.Sprintf("pulse index build %s --key %s", cohortPath, strings.Join(keys, ","))
}
