package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
)

// TestCommandBindings_Complete: the command → feature table covers every
// manifest command and operation exactly once, names nothing else, and
// every binding sets exactly one target that resolves.
func TestCommandBindings_Complete(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range append(commands(), operations()...) {
		if listed[c.Name] {
			t.Errorf("command %q is listed twice across commands() / operations()", c.Name)
		}
		listed[c.Name] = true
		if _, ok := CommandBindingOf(c.Name); !ok {
			t.Errorf("command %q has no binding in features.go (commandBindings)", c.Name)
		}
	}
	bound := map[string]bool{}
	for _, b := range CommandBindings() {
		if bound[b.Command] {
			t.Errorf("command %q is bound twice", b.Command)
		}
		bound[b.Command] = true
		if !listed[b.Command] {
			t.Errorf("binding names %q, which is neither a manifest command nor an operation", b.Command)
		}
		set := 0
		for _, on := range []bool{b.Feature != "", b.Core != "", b.Ungated} {
			if on {
				set++
			}
		}
		if set != 1 {
			t.Errorf("command %q binding must set exactly one of Feature, Core and Ungated", b.Command)
			continue
		}
		switch {
		case b.Core != "":
			if !IsCoreSurface(b.Core) {
				t.Errorf("command %q is bound to unknown core surface %q", b.Command, b.Core)
			}
		case b.Feature != "":
			if k, ok := FeatureKindOf(b.Feature); !ok || k != FeatureKindCapability {
				t.Errorf("command %q is bound to %q, which is not an existing capability", b.Command, b.Feature)
			}
		}
	}
}

func manifestJSON(t *testing.T, m *descriptor.Manifest) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestBuildManifestForInstance_FullScopedEqualsBuildManifest: a scoped
// instance enabling every reached built-in (a profile-free pulse.New)
// describes exactly what BuildManifest does, digest included, and the
// nil instance is BuildManifest itself.
func TestBuildManifestForInstance_FullScopedEqualsBuildManifest(t *testing.T) {
	want := manifestJSON(t, BuildManifest())
	full := NewInstanceSnapshot(nil, FeatureSet{Enabled: ReachedFeatureNames(buildinfo.Version())})
	if got := manifestJSON(t, BuildManifestForInstance(full)); got != want {
		t.Error("full scoped instance manifest differs from BuildManifest")
	}
	if got := manifestJSON(t, BuildManifestForInstance(nil)); got != want {
		t.Error("nil instance manifest differs from BuildManifest")
	}
	if BuildManifest().FeatureSetDigest != full.Digest() {
		t.Errorf("BuildManifest digest %q != full instance digest %q", BuildManifest().FeatureSetDigest, full.Digest())
	}
}

// TestBuildManifestForInstance_ScopingLeavesTablesIntact: building a
// scoped manifest (filter + prose scrub) must not write through to the
// shared capability tables a later full build reads.
func TestBuildManifestForInstance_ScopingLeavesTablesIntact(t *testing.T) {
	before := manifestJSON(t, BuildManifest())
	enabled := []string{"capability:process", "capability:crosstab", "AGG_COUNT", "GROUP_CATEGORY"}
	var hidden []string
	for _, n := range FeatureNames() {
		if n != enabled[0] && n != enabled[1] && n != enabled[2] && n != enabled[3] {
			hidden = append(hidden, n)
		}
	}
	scoped := BuildManifestForInstance(NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: hidden}))
	if scoped.Crosstab == nil || scoped.Facet != nil || scoped.Join != nil || scoped.ProcessChain != nil ||
		scoped.Export != nil || scoped.Import != nil {
		t.Errorf("capability blocks: crosstab=%v facet=%v join=%v chain=%v export=%v import=%v",
			scoped.Crosstab != nil, scoped.Facet != nil, scoped.Join != nil, scoped.ProcessChain != nil,
			scoped.Export != nil, scoped.Import != nil)
	}
	if got := scoped.Crosstab.RecomputeAggregators; len(got) != 0 {
		t.Errorf("crosstab recompute list names hidden aggregators: %v", got)
	}
	if after := manifestJSON(t, BuildManifest()); after != before {
		t.Error("a scoped build changed the full manifest: the scrub wrote through to a shared table")
	}
}

// TestBuildManifestForInstance_IOFormatsFiltered: a present import /
// export block lists only enabled formats, and import entries stop
// claiming export when capability:export is hidden.
func TestBuildManifestForInstance_IOFormatsFiltered(t *testing.T) {
	enabled := []string{"capability:process", "capability:import", "io_format:csv", "io_format:spss"}
	m := BuildManifestForInstance(NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: []string{"capability:export", "io_format:tsv"}}))
	if m.Export != nil {
		t.Error("export block present though capability:export is hidden")
	}
	if m.Import == nil {
		t.Fatal("import block missing")
	}
	var names []string
	for _, f := range m.Import.Formats {
		names = append(names, f.Name)
		if f.Export {
			t.Errorf("format %s claims export though capability:export is hidden", f.Name)
		}
	}
	if len(names) != 2 || names[0] != "csv" || names[1] != "spss" {
		t.Errorf("import formats = %v, want [csv spss]", names)
	}

	m = BuildManifestForInstance(NewInstanceSnapshot(nil, FeatureSet{Enabled: append(enabled, "capability:export")}))
	if m.Export == nil || len(m.Export.Formats) != 2 {
		t.Fatalf("export block = %+v, want csv + spss", m.Export)
	}
	for _, f := range m.Import.Formats {
		if !f.Export {
			t.Errorf("format %s lost its export flag though capability:export is enabled", f.Name)
		}
	}
}

// TestBuildManifestForInstance_SynthHidden: synth distributions vanish
// (as an empty list, not null) with capability:synth.
func TestBuildManifestForInstance_SynthHidden(t *testing.T) {
	m := BuildManifestForInstance(NewInstanceSnapshot(nil, FeatureSet{Enabled: []string{"capability:process"}, Hidden: []string{"capability:synth"}}))
	if m.SynthDistributions == nil || len(m.SynthDistributions) != 0 {
		t.Errorf("synth_distributions = %v, want empty", m.SynthDistributions)
	}
	if len(BuildManifest().SynthDistributions) == 0 {
		t.Error("full manifest lists no synth distributions; the check above is vacuous")
	}
}

func TestRedactProse(t *testing.T) {
	hidden := map[string]struct{}{"AGG_MODE": {}, "pulse_compose": {}, "OVERLAY_INDEX_VS_REF": {}}
	cases := []struct{ in, want string }{
		{"No mention here. Still none.", "No mention here. Still none."},
		{"Counts rows. Prefer AGG_MODE for ties. Streams.", "Counts rows. Streams."},
		{"Use pulse_compose for batches.", ""},
		{"Mirrors OVERLAY_PANEL_INDEX_VS_REF exactly.", "Mirrors OVERLAY_PANEL_INDEX_VS_REF exactly."},
		{"Named OVERLAY_INDEX_VS_REF__x here.", "Named OVERLAY_INDEX_VS_REF__x here."},
		{"Rejects AGG_MODE_X too.", "Rejects AGG_MODE_X too."},
		{"First (AGG_MODE). Second.", "Second."},
	}
	for _, tc := range cases {
		if got := redactProse(tc.in, hidden); got != tc.want {
			t.Errorf("redactProse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubManifest_ListsAndKeys(t *testing.T) {
	hidden := map[string]struct{}{"AGG_MODE": {}}
	in := &descriptor.Manifest{
		ProcessChain: &descriptor.ProcessChainCapability{
			MergeableAggregators: []string{"AGG_COUNT", "AGG_MODE"},
			RejectionRules:       []string{"rejects AGG_MODE", "rejects windows"},
		},
		ComponentsSchemas: descriptor.ComponentsSchemasBlock{Aggregators: map[string]descriptor.ComponentSchema{
			"AGG_COUNT": {}, "AGG_MODE": {},
		}},
		ErrorCodes: []string{"AGG_MODE"},
	}
	out := scrubManifest(in, hidden)
	if got := out.ProcessChain.MergeableAggregators; len(got) != 1 || got[0] != "AGG_COUNT" {
		t.Errorf("mergeable aggregators = %v", got)
	}
	if got := out.ProcessChain.RejectionRules; len(got) != 1 || got[0] != "rejects windows" {
		t.Errorf("rejection rules = %v", got)
	}
	if _, ok := out.ComponentsSchemas.Aggregators["AGG_MODE"]; ok {
		t.Error("hidden map key kept")
	}
	if len(out.ErrorCodes) != 1 {
		t.Error("error codes are not the scrub's to filter")
	}
	if len(in.ProcessChain.MergeableAggregators) != 2 || len(in.ComponentsSchemas.Aggregators) != 2 {
		t.Error("scrub mutated its input")
	}
}
