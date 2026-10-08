package pulse

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// exportSlotExempt reports whether line of the exported file path is
// exempt from the slot-token check: a glossary term's forms line — the
// glossary page's `**Forms:**`, the glossary skill's `Written as:` —
// lists the term's own spellings verbatim ("transition matrices" for
// stochastic-matrix). A term is kept or dropped by its owning
// operators, never token-scrubbed; hidden operator and tool names are
// still checked on these lines.
func exportSlotExempt(path, line string) bool {
	l := strings.TrimSpace(line)
	switch path {
	case "glossary.md":
		return strings.HasPrefix(l, "**Forms:**")
	case "skills/glossary.md":
		return strings.HasPrefix(l, "Written as:")
	}
	return false
}

// TestProfileExportTreeSweep (U21 E2-S3, PRD FR-41 export half): for
// every published example feature profile, every private fixture and,
// per slot-owning capability with wire tokens, the largest profile
// hiding it, no file Pulse.ExportReference writes — catalog, reading
// pages, glossary, skill pages, index, SUMMARY — names a hidden
// operator, hidden-feature MCP tool or hidden capability's slot token.
// Non-vacuous: the unprofiled export names a hidden token of some
// shipped profile, and of every all-but profile's capability.
func TestProfileExportTreeSweep(t *testing.T) {
	full, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	fullFS := afero.NewMemMapFs()
	if err := full.ExportReference(fullFS, "ref", ExportReferenceOptions{}); err != nil {
		t.Fatalf("unprofiled export: %v", err)
	}
	fullTree := exportTree(t, fullFS, "ref")

	pulses := shippedProfilePulses(t)
	for _, c := range descx.SlotTokenCapabilities() {
		if toks, _ := descx.SlotTokensOf(c); len(toks) == 0 {
			continue
		}
		p, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: allFeaturesBut(c)}})
		if err != nil {
			t.Fatalf("New without %s: %v", c, err)
		}
		pulses["all-but/"+c] = p
	}

	shippedNamed := 0
	for _, key := range slices.Sorted(maps.Keys(pulses)) {
		p := pulses[key]
		t.Run(key, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			if err := p.ExportReference(fsys, "ref", ExportReferenceOptions{}); err != nil {
				t.Fatalf("export: %v", err)
			}
			tree := exportTree(t, fsys, "ref")
			if len(tree) == 0 {
				t.Fatal("empty export: vacuous")
			}
			hidden := errorsHiddenTokens(p)
			slots := hiddenSlotTokens(p)
			for _, path := range slices.Sorted(maps.Keys(tree)) {
				for _, line := range strings.Split(tree[path], "\n") {
					if tok := namesHiddenToken(line, hidden); tok != "" {
						t.Errorf("%s names hidden %s: %q", path, tok, line)
					}
					if exportSlotExempt(path, line) {
						continue
					}
					for _, s := range slotTokenLeaks(line, slots) {
						t.Errorf("%s names a hidden slot token: %q", path, s)
					}
				}
			}
			named, slotNamed := false, false
			for path, body := range fullTree {
				for _, line := range strings.Split(body, "\n") {
					if namesHiddenToken(line, hidden) != "" {
						named = true
					}
					if !exportSlotExempt(path, line) && len(slotTokenLeaks(line, slots)) > 0 {
						slotNamed = true
					}
				}
			}
			if named && !strings.HasPrefix(key, "all-but/") {
				shippedNamed++
			}
			if strings.HasPrefix(key, "all-but/") && !slotNamed {
				t.Errorf("the unprofiled export names no slot token %s hides: vacuous", key)
			}
		})
	}
	if shippedNamed == 0 {
		t.Error("the unprofiled export names no token any shipped profile hides: the sweep is vacuous")
	}
}
