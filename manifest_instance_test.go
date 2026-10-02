package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// manifestJSONOutsideOwnedLists marshals m with the sections other units
// own (skills, examples, error lists) removed: what remains must not name
// a hidden feature.
func manifestJSONOutsideOwnedLists(t *testing.T, m *descriptor.Manifest) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	for _, k := range []string{"skills", "examples_count", "example_categories", "example_tags",
		"error_codes", "error_codes_count", "error_domains"} {
		delete(obj, k)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("re-marshal manifest: %v", err)
	}
	return string(out)
}

// containsToken reports whether s mentions name as a whole token (a
// maximal run of [A-Za-z0-9_]).
func containsToken(s, name string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		if (start == 0 || !isWordByte(s[start-1])) && (end == len(s) || !isWordByte(s[end])) {
			return true
		}
		i = start + 1
	}
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// TestManifest_FixturesNameNoHiddenFeature: for every fixture profile,
// no hidden operator or MCP tool name appears anywhere in the instance
// manifest outside skills / examples / error lists; a hidden capability
// has no block, no command / operation and no MCP tool; a hidden
// io_format is in neither format list; and the manifest carries the
// instance's digest.
func TestManifest_FixturesNameNoHiddenFeature(t *testing.T) {
	blockOf := map[string]func(*descriptor.Manifest) bool{
		"capability:facet":         func(m *descriptor.Manifest) bool { return m.Facet != nil },
		"capability:process_chain": func(m *descriptor.Manifest) bool { return m.ProcessChain != nil },
		"capability:joins":         func(m *descriptor.Manifest) bool { return m.Join != nil },
		"capability:crosstab":      func(m *descriptor.Manifest) bool { return m.Crosstab != nil },
		"capability:export":        func(m *descriptor.Manifest) bool { return m.Export != nil },
		"capability:import":        func(m *descriptor.Manifest) bool { return m.Import != nil },
	}
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{})
			snap := p.svc.InstanceSnapshot()
			m := p.Manifest(context.Background())
			if m.FeatureSetDigest == "" || m.FeatureSetDigest != p.FeatureSetDigest() {
				t.Errorf("feature_set_digest = %q, want the instance's %q", m.FeatureSetDigest, p.FeatureSetDigest())
			}
			body := manifestJSONOutsideOwnedLists(t, m)

			hidden := snap.HiddenNames()
			if len(hidden) == 0 {
				t.Fatal("fixture hides nothing; the test would be vacuous")
			}
			for _, h := range hidden {
				if !strings.Contains(h, ":") && containsToken(body, h) {
					t.Errorf("hidden operator %s appears in the manifest", h)
				}
			}

			for _, b := range descx.MCPToolBindings() {
				if b.Feature == "" || snap.Enabled(b.Feature) {
					continue
				}
				if containsToken(body, b.Tool) {
					t.Errorf("MCP tool %s (owned by hidden %s) appears in the manifest", b.Tool, b.Feature)
				}
			}

			var listed []string
			for _, ops := range [][]descriptor.Operator{m.Components.Aggregators, m.Components.Attributes,
				m.Components.Filterers, m.Components.Groupers, m.Components.Windows, m.Components.Features} {
				for _, o := range ops {
					listed = append(listed, o.Name)
				}
			}
			for _, ts := range append(append([]descriptor.TestMeta{}, m.Tests...), m.PostTests...) {
				listed = append(listed, ts.Family)
			}
			for _, r := range m.Regressions {
				listed = append(listed, r.Name)
			}
			for _, o := range m.Overlays {
				listed = append(listed, string(o.Kind))
			}
			for _, n := range listed {
				if !snap.Enabled(n) {
					t.Errorf("manifest lists an entry for %q, which the instance does not offer", n)
				}
			}

			for _, tool := range m.MCPTools {
				b, ok := descx.MCPToolBindingOf(tool.Name)
				if !ok || (b.Feature != "" && !snap.Enabled(b.Feature)) {
					t.Errorf("mcp_tools entry %q is not an offered tool", tool.Name)
				}
			}

			cmds := map[string]bool{}
			for _, c := range append(append([]descriptor.Command{}, m.Commands...), m.Operations...) {
				cmds[c.Name] = true
			}
			for _, b := range descx.CommandBindings() {
				if b.Feature != "" && !snap.Enabled(b.Feature) && cmds[b.Command] {
					t.Errorf("command %q is listed though %s is hidden", b.Command, b.Feature)
				}
				if (b.Core != "" || b.Ungated) && !cmds[b.Command] {
					t.Errorf("always-present command %q is missing", b.Command)
				}
			}

			for capName, present := range blockOf {
				if got, want := present(m), snap.Enabled(capName); got != want {
					t.Errorf("%s block present = %v, want %v", capName, got, want)
				}
			}

			var formats []string
			if m.Import != nil {
				for _, f := range m.Import.Formats {
					formats = append(formats, f.Name)
					if f.Export && !snap.Enabled("capability:export") {
						t.Errorf("import format %s claims export though capability:export is hidden", f.Name)
					}
				}
			}
			if m.Export != nil {
				for _, f := range m.Export.Formats {
					formats = append(formats, f.Name)
				}
			}
			for _, f := range formats {
				if !snap.Enabled(descx.FeatureName(descx.FeatureKindIOFormat, f)) {
					t.Errorf("hidden io_format %s is listed", f)
				}
			}

			if !snap.Enabled("capability:synth") && len(m.SynthDistributions) != 0 {
				t.Errorf("synth_distributions lists %d entries though capability:synth is hidden", len(m.SynthDistributions))
			}
		})
	}
}

// TestManifest_FixtureKeepsEnabledFeatures guards the filter against
// over-reach: what survey-crosstab enables is still described.
func TestManifest_FixtureKeepsEnabledFeatures(t *testing.T) {
	m := newFixturePulse(t, "survey-crosstab", Options{}).Manifest(context.Background())
	if m.Crosstab == nil || m.Facet == nil {
		t.Fatalf("crosstab / facet blocks missing: crosstab=%v facet=%v", m.Crosstab != nil, m.Facet != nil)
	}
	if m.Import != nil || m.Export != nil {
		t.Errorf("import / export blocks present though survey-crosstab enables neither capability")
	}
	names := map[string]bool{}
	for _, o := range m.Components.Aggregators {
		names[o.Name] = true
	}
	for _, o := range m.Overlays {
		names[string(o.Kind)] = true
	}
	for _, o := range m.Tests {
		names[o.Name] = true
	}
	for _, want := range []string{"AGG_WELFORD", "AGG_FREQUENCY", "OVERLAY_PAIRWISE_WELCH_T", "OVERLAY_SHARE_OF_ROW", "TEST_CHISQ"} {
		if !names[want] {
			t.Errorf("enabled %s is missing from the manifest", want)
		}
	}
	cmds := map[string]bool{}
	for _, c := range m.Commands {
		cmds[c.Name] = true
	}
	for _, want := range []string{"process", "facet", "inspect", "predict", "manifest", "schema", "mcp", "version"} {
		if !cmds[want] {
			t.Errorf("command %q missing", want)
		}
	}
	if cmds["compose"] || cmds["shard create"] {
		t.Errorf("hidden capability commands listed: %v", cmds)
	}
}

// TestManifest_DefaultInstanceIsFull: a profile-free instance describes
// the full registry — byte-identical to the CLI's BuildManifest, digest
// included.
func TestManifest_DefaultInstanceIsFull(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(descx.BuildManifest())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("default instance manifest differs from the full-registry BuildManifest")
	}
	if d := descx.BuildManifest().FeatureSetDigest; d != p.FeatureSetDigest() {
		t.Errorf("BuildManifest digest %q != default instance digest %q", d, p.FeatureSetDigest())
	}
}

// TestManifest_HiddenExtensionAbsent: an extension operator the profile
// omits is absent from the manifest, and its digest-bearing unscoped
// twin (BuildManifestWithExtensions) agrees with a profile-free instance
// that registers the same operator.
func TestManifest_HiddenExtensionAbsent(t *testing.T) {
	ext := Extensions{Aggregators: []AggregatorRegistration{
		{Name: "AGG_ACME_HIDDEN", Description: "Stub.", Factory: featureSetStubAggFactory},
		{Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: featureSetStubAggFactory},
	}}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: ext,
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := manifestJSONOutsideOwnedLists(t, p.Manifest(context.Background()))
	if containsToken(body, "AGG_ACME_HIDDEN") {
		t.Error("hidden extension operator appears in the manifest")
	}
	if !containsToken(body, "AGG_ACME_KEPT") {
		t.Error("enabled extension operator missing from the manifest")
	}

	full, err := New(Options{FS: memFsWith(t, nil), Extensions: ext})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d := descx.BuildManifestWithExtensions(full.svc.ExtensionsSnapshot()).FeatureSetDigest; d != full.FeatureSetDigest() {
		t.Errorf("unscoped manifest digest %q != profile-free instance digest %q", d, full.FeatureSetDigest())
	}
}
