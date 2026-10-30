//go:build heavy

package pulse_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testhook"
)

// TestExtensions_BuiltinParity runs every suite's {builtin, extension}
// pair through the cohort × mode matrix and requires identical output.
func TestExtensions_BuiltinParity(t *testing.T) {
	t.Parallel()

	large := parityLargeCohort(t.TempDir())
	for _, suite := range paritySuites() {
		for _, mode := range parityModes(large) {
			t.Run(suite.name+"/"+mode.name, func(t *testing.T) {
				probe := &parityProbe{}
				p, path := mode.open(t, suite.register(probe))
				schema := paritySchema(t)
				for _, row := range suite.rows {
					t.Run(row.name, func(t *testing.T) {
						bReq, eReq := row.builtin, row.extension
						if mode.decorate != nil {
							bReq, eReq = mode.decorate(bReq), mode.decorate(eReq)
						}
						if mode.decorate == nil {
							if got := processing.CanStreamRequest(bReq, schema); got != (!row.bufferedOnly && !row.extStreams) {
								t.Fatalf("CanStreamRequest(builtin) = %v but row.bufferedOnly = %v, row.extStreams = %v",
									got, row.bufferedOnly, row.extStreams)
							}
							// The extension twin must take the same
							// path as the built-in it reimplements,
							// except where extStreams names the
							// deliberate divergence.
							reg := testhook.Service(p).Extensions()
							if got := processing.CanStreamRequestWithExtensions(eReq, schema, reg); got == row.bufferedOnly {
								t.Fatalf("CanStreamRequestWithExtensions(extension) = %v but row.bufferedOnly = %v", got, row.bufferedOnly)
							}
						}
						extMerge := !row.serialOnly || row.extMerges
						if mode.mergeable {
							if got := processing.CanMergeRequest(bReq, schema); got == row.serialOnly {
								t.Fatalf("CanMergeRequest(builtin) = %v but row.serialOnly = %v; mode %s would not run the path it names",
									got, row.serialOnly, mode.name)
							}
							reg := testhook.Service(p).Extensions()
							if got := processing.CanMergeRequestWithExtensions(eReq, schema, reg); got != extMerge {
								t.Fatalf("CanMergeRequestWithExtensions(extension) = %v, want %v (serialOnly=%v extMerges=%v)",
									got, extMerge, row.serialOnly, row.extMerges)
							}
						}
						builtin := runParityArm(t, p, path, bReq).normalize(t, suite, false)
						probe.reset()
						ext := runParityArm(t, p, path, eReq).normalize(t, suite, true)
						assertParity(t, builtin, ext)
						for _, sub := range row.mustContain {
							if !strings.Contains(builtin.data+builtin.tests, sub) {
								t.Errorf("built-in Data / Tests lack %q: %s %s", sub, builtin.data, builtin.tests)
							}
						}
						want := mode.extPath
						if suite.noPath {
							want = ""
						} else if row.bufferedOnly && want != "" {
							want = "buffered"
						} else if row.extStreams && mode.decorate == nil {
							// Every undecorated mode streams the
							// extension twin — the per-shard and
							// parallel-decode workers fold it online
							// too, then Merge the partials.
							want = "online"
							if probe.decimalReads.Load() == 0 {
								t.Error("extension streamed a decimal128 target without reading DecimalValue")
							}
						}
						// Only the parallel modes merge, and there only
						// when the extension request clears the gate: a
						// Merge outside them, or none inside, means the
						// reducer ran a path the row does not name.
						if merges := probe.merges.Load(); mode.mergeable && extMerge && suite.mergeProbe {
							if merges == 0 {
								t.Errorf("mode %s: extension request is mergeable but Merge never ran", mode.name)
							}
						} else if merges != 0 {
							t.Errorf("mode %s: Merge ran %d times on a path that must not merge", mode.name, merges)
						}
						switch want {
						case "online":
							if probe.online.Load() == 0 || probe.buffered.Load() != 0 {
								t.Errorf("extension path: online=%d buffered=%d, want online only",
									probe.online.Load(), probe.buffered.Load())
							}
						case "buffered":
							if probe.buffered.Load() == 0 || probe.online.Load() != 0 {
								t.Errorf("extension path: online=%d buffered=%d, want buffered only",
									probe.online.Load(), probe.buffered.Load())
							}
						}
					})
				}
			})
		}
	}
}
