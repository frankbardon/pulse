package pulse

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/internal/spsssidecar"
	"github.com/spf13/afero"
)

// Sidecar kind vocabulary for StaleSidecar.Kind. It is closed and
// small: these are the two documents beside a cohort whose validity is
// bound to the cohort's BYTES, so these are the two a cohort-rewriting
// operation invalidates. The index MANIFEST
// (internal/encoding.IndexManifestSuffix) is deliberately absent — it catalogues
// indexes rather than fingerprinting the cohort, so a rewrite leaves it
// accurate about which indexes exist while the indexes themselves go
// stale.
const (
	// SidecarKindPointLookupIndex is a `cohort.pulse.<keyhash>.idx`
	// point-lookup index. It carries a SHA-256 + size + mtime
	// fingerprint over the cohort and refuses to serve once the size
	// moves (PULSE_INDEX_STALE).
	SidecarKindPointLookupIndex = service.SidecarKindPointLookupIndex
	// SidecarKindSPSSMetadata is a `cohort.pulse.spss.json` SPSS
	// metadata sidecar. Same fingerprint shape over the same cohort;
	// a size change is PULSE_SPSS_SIDECAR_STALE on the next
	// `pulse export spss`.
	SidecarKindSPSSMetadata = "spss_metadata"
)

// StaleSidecar names one sidecar document beside a cohort that a
// cohort-rewriting operation has invalidated: its path, kind, the
// point-lookup index's ordered key tuple and the command that rebuilds
// it. Nothing is rebuilt automatically — each sidecar self-invalidates
// through its own fingerprint (or, for an archive index, its shard
// coverage). Shared with AddShardResult.InvalidatedSidecars.
type StaleSidecar = service.StaleSidecar

// InvalidatedSidecars reports the sidecar documents beside the cohort at
// cohortPath whose validity is bound to the cohort's bytes — the ones a
// rewrite such as WidenSetField invalidates.
//
// Discovery goes through each sidecar's OWN naming authority, never a
// filename guess: point-lookup indexes come from ListIndexes (which
// reads the keyless discovery manifest first and unions any on-disk
// sidecar it does not name), and the SPSS metadata sidecar from
// spsssidecar.Path. Guessing would be wrong in the one direction that
// matters — an index filename is a hash of its key tuple, so a guess
// cannot recover the tuple the rebuild command needs.
//
// The result is ordered deterministically (indexes first, by path, then
// the SPSS sidecar) so a caller can print or diff it. An empty slice
// means there is nothing beside the cohort to rebuild; callers should
// print NOTHING rather than an empty section.
//
// A shard archive's point-lookup index is one sidecar per shard, and
// each is reported on its own (same Keys and Rebuild command — the
// rebuild is archive-wide), because each is a file whose validity is
// bound to its shard's bytes.
//
// A cohort with no sidecars answers with an empty slice rather than an
// error: this is advisory reporting attached to an operation that has
// already succeeded, and failing it would turn a hint into an outage.
func (p *Pulse) InvalidatedSidecars(ctx context.Context, cohortPath string) ([]StaleSidecar, error) {
	out, err := p.svc.InvalidatedIndexSidecars(ctx, cohortPath)
	if err != nil {
		return nil, err
	}

	spssPath := spsssidecar.Path(cohortPath)
	exists, err := afero.Exists(p.fsys, spssPath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("checking SPSS metadata sidecar existence: %s", spssPath))
	}
	if exists {
		out = append(out, StaleSidecar{
			Path:    spssPath,
			Kind:    SidecarKindSPSSMetadata,
			Rebuild: fmt.Sprintf("pulse import SOURCE.sav -o %s", cohortPath),
		})
	}

	return out, nil
}
