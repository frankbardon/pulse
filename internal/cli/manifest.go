package cli

import (
	"io"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// WriteManifest writes the root `pulse --json` envelope: the full
// manifest, or with intent the manifest scoped to that intent
// (descx.BuildManifestForIntent over the unprofiled CLI instance), slimmed
// when slim is set. An unknown intent is a coded error envelope
// (PULSE_RECOMMEND_INTENT_UNKNOWN), not a manifest.
func WriteManifest(w io.Writer, slim bool, intent string) error {
	manifest := descx.BuildManifest()
	if intent != "" {
		scoped, err := descx.BuildManifestForIntent(nil, intent)
		if err != nil {
			return writeCodedErrorEnvelope(w, "CLI_ERROR", err)
		}
		manifest = scoped
	}
	if slim {
		manifest = descx.SlimManifest(manifest)
	}
	return writeJSON(w, descriptor.NewEnvelope(manifest))
}
