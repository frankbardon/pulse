package pulse

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/spf13/afero"
)

// Sidecar kind vocabulary for StaleSidecar.Kind. It is closed and
// small: these are the two documents beside a cohort whose validity is
// bound to the cohort's BYTES, so these are the two a cohort-rewriting
// operation invalidates. The index MANIFEST
// (encoding.IndexManifestSuffix) is deliberately absent — it catalogues
// indexes rather than fingerprinting the cohort, so a rewrite leaves it
// accurate about which indexes exist while the indexes themselves go
// stale.
const (
	// SidecarKindPointLookupIndex is a `cohort.pulse.<keyhash>.idx`
	// point-lookup index. It carries a SHA-256 + size + mtime
	// fingerprint over the cohort and refuses to serve once the size
	// moves (PULSE_INDEX_STALE).
	SidecarKindPointLookupIndex = "point_lookup_index"
	// SidecarKindSPSSMetadata is a `cohort.pulse.spss.json` SPSS
	// metadata sidecar. Same fingerprint shape over the same cohort;
	// a size change is PULSE_SPSS_SIDECAR_STALE on the next
	// `pulse export spss`.
	SidecarKindSPSSMetadata = "spss_metadata"
)

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

// InvalidatedSidecars reports the sidecar documents beside the cohort at
// cohortPath whose validity is bound to the cohort's bytes — the ones a
// rewrite such as WidenSetField invalidates.
//
// Discovery goes through each sidecar's OWN naming authority, never a
// filename guess: point-lookup indexes come from ListIndexes (which
// reads the keyless discovery manifest first and unions any on-disk
// sidecar it does not name), and the SPSS metadata sidecar from
// spss.SidecarPath. Guessing would be wrong in the one direction that
// matters — an index filename is a hash of its key tuple, so a guess
// cannot recover the tuple the rebuild command needs.
//
// The result is ordered deterministically (indexes first, by path, then
// the SPSS sidecar) so a caller can print or diff it. An empty slice
// means there is nothing beside the cohort to rebuild; callers should
// print NOTHING rather than an empty section.
//
// A cohort with no sidecars, and a shard archive (which cannot carry a
// point-lookup index at all), both answer with an empty slice rather
// than an error: this is advisory reporting attached to an operation
// that has already succeeded, and failing it would turn a hint into an
// outage.
func (p *Pulse) InvalidatedSidecars(ctx context.Context, cohortPath string) ([]StaleSidecar, error) {
	out := []StaleSidecar{}

	indexes, err := p.svc.ListIndexes(ctx, cohortPath)
	switch {
	case err == nil:
		for _, idx := range indexes {
			out = append(out, StaleSidecar{
				Path:    idx.IndexPath,
				Kind:    SidecarKindPointLookupIndex,
				Keys:    append([]string(nil), idx.Keys...),
				Rebuild: rebuildIndexCommand(cohortPath, idx.Keys),
			})
		}
	case errors.HasCode(err, errors.PULSE_INDEX_UNSUPPORTED_SHARDED):
		// A shard archive cannot carry a point-lookup index, so there
		// is nothing to report rather than something to fail on.
	default:
		return nil, err
	}

	spssPath := spss.SidecarPath(cohortPath)
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

// rebuildIndexCommand renders the exact `pulse index build` invocation
// that reproduces an index over keys. The tuple is comma-joined in
// ORDER, matching the --key flag's own composite syntax, because key
// order is significant end to end and a reordered tuple builds a
// different index at a different path.
func rebuildIndexCommand(cohortPath string, keys []string) string {
	return fmt.Sprintf("pulse index build %s --key %s", cohortPath, strings.Join(keys, ","))
}
