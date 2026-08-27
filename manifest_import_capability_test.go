package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	pformat "github.com/frankbardon/pulse/io/format"
)

// TestManifestImportCapability_MatchesFormatRegistry pins the manifest's
// hand-declared import table against the io/format dispatch it
// describes.
//
// The table is hand-declared because descriptor/ is the no-execute
// layer: importing io/format there would drag the arrow, parquet and
// excel adapters into every manifest build for the sake of a list of
// strings. That trade buys a drift risk, and this test is the price —
// it lives in the root package, which already depends on both, so the
// descriptor import graph stays clean.
//
// It checks both directions. A format in SupportedImport with no
// manifest entry is invisible to an LLM planner; a manifest entry with
// no reader is a promise the engine cannot keep.
func TestManifestImportCapability_MatchesFormatRegistry(t *testing.T) {
	m := descriptor.BuildManifest()

	declared := make(map[string]descriptor.ImportFormatCapability, len(m.Import.Formats))
	for _, f := range m.Import.Formats {
		declared[f.Name] = f
	}
	registered := make(map[string]bool, len(pformat.SupportedImport))
	for _, f := range pformat.SupportedImport {
		registered[f] = true
	}

	for name := range registered {
		if _, ok := declared[name]; !ok {
			t.Errorf("io/format.SupportedImport carries %q but Manifest.Import.Formats does not", name)
		}
	}
	for name := range declared {
		if !registered[name] {
			t.Errorf("Manifest.Import.Formats declares %q but io/format.SupportedImport does not; the manifest promises a reader that is not registered", name)
		}
	}

	// Every declared extension must actually resolve to its own format
	// through FromExt — the dispatch users hit when they omit --format.
	for name, cap := range declared {
		for _, ext := range cap.Extensions {
			if got := pformat.FromExt("file" + ext); got != name {
				t.Errorf("FromExt(%q) = %q, but Manifest.Import.Formats[%q] claims the extension", ext, got, name)
			}
		}
	}
}
