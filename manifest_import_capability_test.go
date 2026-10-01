package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	pio "github.com/frankbardon/pulse/io"
)

// TestManifestImportCapability_MatchesFormatRegistry pins the manifest's
// hand-declared import table against the io factory it describes.
//
// The table is hand-declared because descriptor/ is the no-execute
// layer: importing the io factory there would drag the arrow, parquet and
// excel adapters into every manifest build for the sake of a list of
// strings. That trade buys a drift risk, and this test is the price —
// it lives in the root package, which already depends on both, so the
// descriptor import graph stays clean.
//
// It checks both directions. A format io.Formats() can read with no
// manifest entry is invisible to an LLM planner; a manifest entry with
// no reader is a promise the engine cannot keep. Each entry's Export
// flag must equal Format.CanWrite, and every advertised extension must
// resolve back through io.FormatFromPath.
func TestManifestImportCapability_MatchesFormatRegistry(t *testing.T) {
	m := descx.BuildManifest()

	declared := make(map[string]descriptor.ImportFormatCapability, len(m.Import.Formats))
	for _, f := range m.Import.Formats {
		declared[f.Name] = f
	}
	registered := make(map[string]bool)
	for _, f := range pio.Formats() {
		if f.CanRead() {
			registered[f.String()] = true
		}
	}

	for name := range registered {
		if _, ok := declared[name]; !ok {
			t.Errorf("io.Formats() reads %q but Manifest.Import.Formats does not", name)
		}
	}
	for name := range declared {
		if !registered[name] {
			t.Errorf("Manifest.Import.Formats declares %q but io.Formats() cannot read it; the manifest promises a reader that is not registered", name)
		}
	}

	// Every declared extension must actually resolve to its own format
	// through FormatFromPath — the dispatch users hit when they omit
	// --format — and the Export flag must match the factory's writer set.
	for name, cap := range declared {
		for _, ext := range cap.Extensions {
			if got := pio.FormatFromPath("file" + ext); got.String() != name {
				t.Errorf("FormatFromPath(%q) = %q, but Manifest.Import.Formats[%q] claims the extension", ext, got, name)
			}
		}
		if want := pio.Format(name).CanWrite(); cap.Export != want {
			t.Errorf("Manifest.Import.Formats[%q].Export = %v, but io.Format(%q).CanWrite() = %v", name, cap.Export, name, want)
		}
	}
}
