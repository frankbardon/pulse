package encoding

import (
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// SplitAnchorPath splits an archive-and-anchor path of the form
// "archive.pulse#entry.pulse" into (archivePath, entry, true). Returns
// ("", "", false) when no `#` is present. Resolves at the FIRST `#` —
// a literal `#` in a filename is not supported in v1.
//
// The ONE anchor parser: pulse.Open (internal/service), predict,
// inspect, the anchored cohort builder and export all resolve an
// anchor through it, so an anchored path means the same entry on every
// surface.
func SplitAnchorPath(path string) (archivePath, entry string, ok bool) {
	archivePath, entry, ok = strings.Cut(path, "#")
	if !ok {
		return "", "", false
	}
	return archivePath, entry, true
}

// RefuseReservedAnchor is the refusal every anchored read raises for
// an anchor naming the reserved canonical entry `_schema.pulse`:
// PULSE_SHARD_RESERVED_NAME with details.entry. Nil for any other
// entry name. Raised BEFORE the archive is opened, so the verdict is
// the name's alone.
func RefuseReservedAnchor(entry string) error {
	if entry != ReservedSchemaName {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
		"cannot anchor-open the reserved canonical schema entry",
		map[string]any{"entry": entry})
}
