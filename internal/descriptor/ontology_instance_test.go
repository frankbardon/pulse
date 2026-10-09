package descriptor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/internal/skills"
)

func exID(name string) string { return OntologyID(descriptor.OntologyNodeExample, name) }
func intentID(name string) string {
	return OntologyID(descriptor.OntologyNodeIntent, name)
}
func termID(name string) string {
	return OntologyID(descriptor.OntologyNodeGlossaryTerm, name)
}

// pruneFixtureSources is a small synthetic graph exercising every skill
// and example prune rule against real feature names.
func pruneFixtureSources() ontologySources {
	return ontologySources{
		features: Features(),
		skills: []skills.Metadata{
			{Name: "op-agg-sum", Kind: "operator", Operator: "AGG_SUM"},
			{Name: "op-agg-count", Kind: "operator", Operator: "AGG_COUNT"},
			{Name: "topical-needs", Kind: "design", Requires: []string{featCrosstab, "TEST_WELCH"}},
			{Name: "topical-free", Kind: "design"},
			{Name: "type-u8", Kind: "type"},
		},
		skillBody: func(name string) (string, bool) {
			if name == "op-agg-count" {
				return "## See\n\n- `tags=[linked]`\n", true
			}
			return "", true
		},
		examples: []ontologyExample{
			{Name: "uses-sum", Operators: []string{"AGG_SUM"}},
			{Name: "contrast", Description: "Counts rows, unlike AGG_SUM.", Operators: []string{"AGG_MAX"}},
			{Name: "xtab", Operators: []string{"AGG_MAX"}, Body: json.RawMessage(`{"crosstab":{"rows":[]}}`)},
			{Name: "declares", Capabilities: []string{featStream}},
			{Name: "linked", Tags: []string{"linked"}, Operators: []string{"AGG_MAX"}},
		},
	}
}

func TestOntologyPrune_SkillsAndExamples(t *testing.T) {
	base := buildOntology(pruneFixtureSources())
	if p := base.Problems(); len(p) > 0 {
		t.Fatalf("fixture problems: %v", p)
	}
	for _, tc := range []struct {
		name    string
		hide    []string
		gone    []string
		visible []string
	}{
		{
			name:    "atomic skill follows its operator; example exemplifying it and example routing to it go",
			hide:    []string{"AGG_SUM"},
			gone:    []string{skillID("op-agg-sum"), exID("uses-sum"), exID("contrast"), opID("AGG_SUM")},
			visible: []string{skillID("op-agg-count"), exID("xtab"), exID("declares"), exID("linked"), skillID("type-u8")},
		},
		{
			name:    "topical requires means AND: any one hidden prunes",
			hide:    []string{"TEST_WELCH"},
			gone:    []string{skillID("topical-needs")},
			visible: []string{skillID("topical-free"), exID("uses-sum")},
		},
		{
			name:    "example detector capability",
			hide:    []string{featCrosstab},
			gone:    []string{exID("xtab"), skillID("topical-needs")},
			visible: []string{exID("uses-sum"), exID("linked")},
		},
		{
			name: "_meta.capabilities",
			hide: []string{featStream},
			gone: []string{exID("declares")},
		},
		{
			name:    "a hidden skill's See edge never prunes the example it links",
			hide:    []string{"AGG_COUNT"},
			gone:    []string{skillID("op-agg-count")},
			visible: []string{exID("linked")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := pruneOntology(base, hidingSnapshot(tc.hide...))
			for _, id := range tc.gone {
				if !base.Has(id) {
					t.Fatalf("premise: base lacks %s", id)
				}
				if g.Has(id) {
					t.Errorf("%s survived", id)
				}
			}
			for _, id := range tc.visible {
				if !g.Has(id) {
					t.Errorf("%s pruned", id)
				}
			}
			for _, e := range g.Ontology().Edges {
				if !g.Has(e.From) || !g.Has(e.To) {
					t.Errorf("edge %+v touches a pruned node", e)
				}
			}
		})
	}
}

// TestOntologyPrune_Operators: real base graph — the non-feature
// operator rules (synth distributions, REG spec modifiers), MCP tools,
// and the flattened any-of DependsOn edges never pruning an enabled
// feature.
func TestOntologyPrune_Operators(t *testing.T) {
	base := BaseOntology()
	g := pruneOntology(base, hidingSnapshot(featSynth))
	if g.Has(opID("normal")) || g.Has(skillID("op-synth-normal")) {
		t.Error("synth distribution survives hidden capability:synth")
	}

	g = pruneOntology(base, hidingSnapshot("REG_GLM"))
	if g.Has(opID("REG_GLM")) || !g.Has(opID("REG_RESAMPLE")) || !g.Has(skillID("op-reg-mod-resample")) {
		t.Error("hiding one regression: REG_GLM must go, the REG_RESAMPLE modifier stay")
	}
	g = pruneOntology(base, hidingSnapshot(regressionNames()...))
	if g.Has(opID("REG_RESAMPLE")) || g.Has(skillID("op-reg-mod-selection")) {
		t.Error("every regression hidden: the spec modifiers must go")
	}

	g = pruneOntology(base, hidingSnapshot(featLookup))
	if g.Has(OntologyID(descriptor.OntologyNodeMCPTool, "pulse_lookup")) || g.Has(skillID("tool-lookup")) {
		t.Error("pulse_lookup / tool-lookup survive hidden capability:lookup")
	}
	if !g.Has(OntologyID(descriptor.OntologyNodeMCPTool, "pulse_inspect")) || !g.Has(skillID("tool-inspect")) {
		t.Error("a core tool was pruned")
	}

	// An enabled feature with a flattened any-of edge to a hidden one
	// stays: find one in the base.
	var host, dep string
	for _, f := range Features() {
		for _, group := range f.DependsOn {
			if len(group) > 1 {
				host, dep = f.Name, group[0]
			}
		}
	}
	if host == "" {
		t.Skip("no any-of dependency in the feature table")
	}
	g = pruneOntology(base, hidingSnapshot(dep))
	if !g.Has(featureNodeID(t, host)) {
		t.Errorf("%s pruned because one any-of member (%s) is hidden", host, dep)
	}
}

// TestOntologyPrune_Intents: an intent goes when every operator serving
// it is hidden; one served by nobody (tooling) always stays.
func TestOntologyPrune_Intents(t *testing.T) {
	base := BaseOntology()
	servers := base.OperatorsServing(IntentDrivers)
	if len(servers) == 0 {
		t.Fatal("premise: drivers has servers")
	}
	g := pruneOntology(base, hidingSnapshot(servers[1:]...))
	if !g.Has(intentID(IntentDrivers)) {
		t.Error("drivers pruned while one server is enabled")
	}
	g = pruneOntology(base, hidingSnapshot(servers...))
	if g.Has(intentID(IntentDrivers)) {
		t.Error("drivers survives with every server hidden")
	}
	if len(base.OperatorsServing(IntentLookup)) != 0 {
		t.Fatal("premise: no operator serves lookup")
	}
	if !g.Has(intentID(IntentLookup)) {
		t.Error("an intent no operator serves was pruned")
	}
}

// TestOntologyPrune_FollowUps: a follow_up edge goes with its hidden
// target while the source operator and its other follow-ups stay.
func TestOntologyPrune_FollowUps(t *testing.T) {
	base := BaseOntology()
	anova, tukey, pairwise := opID("TEST_ANOVA_F"), opID("TEST_TUKEY_HSD"), opID("OVERLAY_PAIRWISE_WELCH_T")
	for _, to := range []string{tukey, pairwise} {
		if !hasEdge(base, ontoEdge(anova, to, descriptor.OntologyEdgeFollowUp)) {
			t.Fatalf("premise: base carries follow_up %s → %s", anova, to)
		}
	}
	g := pruneOntology(base, hidingSnapshot("TEST_TUKEY_HSD"))
	if g.Has(tukey) || !g.Has(anova) {
		t.Fatal("hiding TEST_TUKEY_HSD: it must go, TEST_ANOVA_F stay")
	}
	for _, e := range g.Out(anova, descriptor.OntologyEdgeFollowUp) {
		if e.To == tukey {
			t.Errorf("follow_up into hidden %s survives", tukey)
		}
	}
	if !hasEdge(g, ontoEdge(anova, pairwise, descriptor.OntologyEdgeFollowUp)) {
		t.Error("an enabled follow-up target was pruned with its hidden sibling")
	}

	g = pruneOntology(base, hidingSnapshot("REG_OLS"))
	if n := len(g.Out(opID("TEST_PEARSON_R"), descriptor.OntologyEdgeFollowUp)); n != 0 || !g.Has(opID("TEST_PEARSON_R")) {
		t.Errorf("hiding REG_OLS: TEST_PEARSON_R must stay with no follow_up, has %d", n)
	}
}

// termUsers returns the operators whose Purpose cites term.
func termUsers(g *OntologyGraph, term string) []string {
	var out []string
	for _, e := range g.In(termID(term), descriptor.OntologyEdgeUsesTerm) {
		n, _ := g.Node(e.From)
		out = append(out, n.Name)
	}
	return out
}

// TestOntologyPrune_Glossary: a term goes only when it has an operator
// user and every user is hidden; its SeeAlso edges go with it; a term
// with no operator user stays.
func TestOntologyPrune_Glossary(t *testing.T) {
	base := BaseOntology()
	var term string
	var users []string
	for _, tm := range Glossary() {
		if u := termUsers(base, tm.ID); len(u) == 1 {
			term, users = tm.ID, u
			break
		}
	}
	if term == "" {
		t.Fatal("premise: a term with one operator user")
	}
	g := pruneOntology(base, hidingSnapshot(users...))
	if g.Has(termID(term)) {
		t.Errorf("%s survives with its only user %v hidden", term, users)
	}
	if in := g.In(termID(term), descriptor.OntologyEdgeRoutesTo); len(in) != 0 {
		t.Errorf("SeeAlso into pruned %s kept: %+v", term, in)
	}
	var orphan string
	for _, tm := range Glossary() {
		if len(termUsers(base, tm.ID)) == 0 {
			orphan = tm.ID
			break
		}
	}
	if orphan == "" {
		t.Skip("every term has an operator user")
	}
	all := hidingSnapshot(ReachedFeatureNames(buildinfo.Version())...)
	if g := pruneOntology(base, all); !g.Has(termID(orphan)) {
		t.Errorf("%s (no operator user) pruned", orphan)
	}
}

// TestDiscovery_VirtualSkillsRenderPrunedGraph: the profiled intents /
// glossary bodies drop what the graph pruned, the full instance's are
// the registry renders, and both stay listed.
func TestDiscovery_VirtualSkillsRenderPrunedGraph(t *testing.T) {
	base := BaseOntology()
	users := termUsers(base, "alpha")
	hide := append(append([]string{}, base.OperatorsServing(IntentDrivers)...), users...)
	d := hidingSnapshot(hide...).Discovery()

	intents, ok := d.Skill(skills.VirtualIntents)
	if !ok || strings.Contains(intents, "### "+IntentDrivers+"\n") {
		t.Errorf("intents skill still lists drivers (ok=%v)", ok)
	}
	if !strings.Contains(intents, "### "+IntentDescribe+"\n") {
		t.Error("intents skill lost an enabled intent")
	}
	glossary, ok := d.Skill(skills.VirtualGlossary)
	if !ok || strings.Contains(glossary, "## alpha\n") {
		t.Errorf("glossary skill still carries alpha (ok=%v)", ok)
	}
	for _, line := range strings.Split(glossary, "\n") {
		if strings.HasPrefix(line, "See also: ") && slices.Contains(strings.Split(strings.TrimPrefix(line, "See also: "), ", "), "alpha") {
			t.Errorf("See also still links pruned alpha: %q", line)
		}
	}
	listed := skillNames(d.Skills())
	for _, v := range skills.ReservedVirtualNames() {
		if !slices.Contains(listed, v) {
			t.Errorf("%s not listed", v)
		}
	}
	for _, v := range []string{skills.VirtualIntents, skills.VirtualGlossary} {
		want, _ := skills.Get(v)
		if got, _ := fullDiscovery.Skill(v); got != want {
			t.Errorf("full-instance %s differs from the registry render", v)
		}
	}
}

// TestInstanceOntology_SharesAndExtends: profile-free, extension-free
// shares the base by pointer; extension operators and tables become
// nodes with their edges, pruned with the capability they need.
func TestInstanceOntology_SharesAndExtends(t *testing.T) {
	if hidingSnapshot().Ontology() != BaseOntology() {
		t.Error("hide-nothing instance does not share the base graph")
	}
	var nilSnap *InstanceSnapshot
	if nilSnap.Ontology() != BaseOntology() {
		t.Error("nil snapshot does not answer the base graph")
	}
	if UnscopedInstanceSnapshot(&ExtensionsSnapshot{}).Ontology() != BaseOntology() {
		t.Error("empty extensions do not share the base graph")
	}
	ext := &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_TOTAL"}},
		Purposes: map[string]descriptor.Purpose{
			"AGG_ACME_TOTAL": {Intents: []string{IntentDrivers}, Glossary: []string{"alpha"}},
		},
		LabelTables:  []descriptor.LabelTableMeta{{Name: "region"}},
		RangeTables:  []descriptor.RangeTableMeta{{Name: "region"}},
		LookupTables: []descriptor.LookupTableMeta{{Name: "rates"}},
	}
	g := UnscopedInstanceSnapshot(ext).Ontology()
	if g == BaseOntology() || BaseOntology().Has(opID("AGG_ACME_TOTAL")) {
		t.Fatal("extending mutated or reused the base graph")
	}
	label := OntologyID(descriptor.OntologyNodeTable, "label/region")
	rng := OntologyID(descriptor.OntologyNodeTable, "range/region")
	for _, e := range []descriptor.OntologyEdge{
		ontoEdge(opID("AGG_ACME_TOTAL"), intentID(IntentDrivers), descriptor.OntologyEdgeServesIntent),
		ontoEdge(opID("AGG_ACME_TOTAL"), termID("alpha"), descriptor.OntologyEdgeUsesTerm),
		ontoEdge(label, featLabels, descriptor.OntologyEdgeRequiresCapability),
		ontoEdge(rng, featRangeTables, descriptor.OntologyEdgeRequiresCapability),
	} {
		if !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	if !g.Has(OntologyID(descriptor.OntologyNodeTable, "lookup/rates")) {
		t.Error("lookup table node missing")
	}

	// Scoped: the extension operator (enabled) keeps drivers and alpha
	// alive with every built-in server hidden; a hidden capability:labels
	// prunes the label table only.
	var enabled []string
	hide := append(append([]string{featLabels}, BaseOntology().OperatorsServing(IntentDrivers)...), termUsers(BaseOntology(), "alpha")...)
	for _, n := range ReachedFeatureNames(buildinfo.Version()) {
		if !slices.Contains(hide, n) {
			enabled = append(enabled, n)
		}
	}
	inst := NewInstanceSnapshot(ext, FeatureSet{Enabled: append(enabled, "AGG_ACME_TOTAL"), Hidden: hide})
	g = inst.Ontology()
	if !g.Has(intentID(IntentDrivers)) || !g.Has(termID("alpha")) {
		t.Error("an enabled extension operator did not keep its intent / term")
	}
	if g.Has(label) || !g.Has(rng) {
		t.Error("hidden capability:labels: label table must go, range table stay")
	}
}
