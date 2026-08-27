package pulse

import (
	"strings"

	"github.com/frankbardon/pulse/imports"
	"github.com/frankbardon/pulse/io/spss"
)

// pulseSidecarSuffixes lists the filename suffixes Pulse itself writes
// beside a cohort. Every entry ends in ".json", so every entry is a
// file a *.json directory walk would otherwise pick up and try to
// parse as its own document.
//
// The constants are referenced rather than re-spelled so a suffix
// rename in the owning package cannot silently un-skip its sidecar.
var pulseSidecarSuffixes = []string{
	imports.SidecarSuffix, // ".meta.json"  — managed-import handle metadata
	spss.SidecarSuffix,    // ".spss.json"  — SPSS dictionary metadata
}

// isPulseSidecarName reports whether name is one of Pulse's own
// sidecar documents, identified by suffix.
//
// This exists because a config-directory loader that walks *.json and
// hard-fails on an unparseable file is pointed at a directory that may
// legitimately also hold cohorts — PULSE_LABEL_TABLES_DIR aimed at a
// data directory is entirely reasonable — and a cohort drags its
// sidecars along with it. Failing pulse.New on a file Pulse wrote,
// with a message blaming a malformed label table, is a false alarm.
//
// It is deliberately an EXCLUSION LIST of files Pulse knows are not
// the loader's documents, not general tolerance of unparseable JSON.
// A malformed label table still hard-fails with its path named: that
// strictness is what turns a typo into an error instead of a table
// that silently is not there.
//
// Not covered here (and not needing to be): the sidecar point-lookup
// index, cohort.pulse.<keyhash>.idx, which is not *.json and so never
// reaches a loader's parse attempt.
func isPulseSidecarName(name string) bool {
	for _, suffix := range pulseSidecarSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
