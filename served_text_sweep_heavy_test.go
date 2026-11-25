//go:build heavy

package pulse

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// servedTextAllowlist is TestProfileServedTextNoHiddenFeature's
// shrink-only baseline (internal/doccheck): path = the profile, entry =
// "<surface> | <token> | <text>".
const servedTextAllowlist = "served-text"

// TestProfileServedTextNoHiddenFeature (U32 E4-S2, PRD FR-24): for every
// single-feature-hidden profile — per built-in feature, the largest valid
// profile hiding it (allFeaturesBut) — nothing the instance serves names
// a hidden operator, a hidden feature's MCP tool or a hidden capability's
// slot token: the manifest's string values, the MCP tools / prompts /
// resources listings and every prompt body (string values only, so the
// registration-time input schemas' structural keys are not prose), the
// skill metadata and every served skill body, every example summary and
// body, and every file `ExportReference` writes. A glossary term's own
// spellings are exempt from the slot-token check (exportSlotExempt), and
// a same-spelled homonym sentence is not a hit (slotTokenLeaks).
//
// It also holds the core tools' descriptions to their fences: under a
// single hidden feature the prose scrub drops no sentence of a core
// (always-mounted) tool's description — the served text is exactly the
// fence render for the instance — so a clause about one feature is
// fenced, never left to take its whole sentence down (#238).
//
// Findings reconcile against the committed allowlist
// (internal/doccheck/allowlists/served-text.txt): code-owned hits are
// fixed, never listed. Non-vacuous: the unprofiled instance's surfaces
// name a token some swept profile hides, and the slot tokens of every
// slot-owning capability with wire tokens.
func TestProfileServedTextNoHiddenFeature(t *testing.T) {
	t.Parallel()

	allow, err := doccheck.Load(afero.NewOsFs(), doccheck.AllowlistFile(servedTextAllowlist))
	if err != nil {
		t.Fatal(err)
	}
	toolSource := map[string]string{}
	for _, m := range toolmeta.Meta() {
		toolSource[m.Name] = m.Description
	}
	core := map[string]bool{}
	for _, b := range descx.MCPToolBindings() {
		if b.Feature == "" {
			core[b.Tool] = true
		}
	}

	full, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	var fullText strings.Builder
	for _, s := range servedTexts(t, full) {
		for _, line := range s.lines {
			fullText.WriteString(line + "\n")
		}
	}

	type profiled struct {
		key string
		p   *Pulse
	}
	var profiles []profiled
	seen := map[string]bool{}
	for _, f := range descx.Features() {
		feats := allFeaturesBut(f.Name)
		sig := strings.Join(feats, ",")
		if seen[sig] {
			continue
		}
		p, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: feats}})
		if err != nil {
			continue // a feature no valid profile can drop
		}
		seen[sig] = true
		profiles = append(profiles, profiled{"all-but/" + f.Name, p})
	}
	if len(profiles) < 100 {
		t.Fatalf("only %d single-feature-hidden profiles: the feature walk is broken", len(profiles))
	}

	var vs []doccheck.Violation
	add := func(profile, surface, token, text string) {
		vs = append(vs, doccheck.Violation{
			Path:    profile,
			Entry:   surface + " | " + token + " | " + strings.ReplaceAll(text, "\t", " "),
			Message: "served text names a feature the profile hides: fence the clause (skills, tool descriptions) or fix the code that serves it",
		})
	}
	named, slotNamed := 0, map[string]bool{}
	for _, pr := range profiles {
		hidden := errorsHiddenTokens(pr.p)
		slots := hiddenSlotTokens(pr.p)
		for _, s := range servedTexts(t, pr.p) {
			for _, line := range s.lines {
				if tok := namesHiddenToken(line, hidden); tok != "" {
					add(pr.key, s.surface, tok, line)
				}
				if exportSlotExempt(s.exemptPath, line) {
					continue
				}
				for _, sentence := range slotTokenLeaks(line, slots) {
					add(pr.key, s.surface, "slot token", sentence)
				}
			}
		}
		inst := pr.p.svc.InstanceSnapshot()
		for name, desc := range listedToolDescriptions(t, pr.p) {
			if !core[name] {
				continue
			}
			if want := descx.RenderProseFences(toolSource[name], inst.Enabled); desc != want {
				add(pr.key, "tools/list "+name, "sentence dropped", firstMissingLine(want, desc))
			}
		}
		if namesHiddenToken(fullText.String(), hidden) != "" {
			named++
		}
		if len(slots) > 0 && len(slotTokenLeaks(fullText.String(), slots)) > 0 {
			slotNamed[pr.key] = true
		}
	}
	for _, p := range allow.Reconcile(vs).Problems(allow.File) {
		t.Error(p)
	}
	if named == 0 {
		t.Error("the unprofiled surfaces name no token any swept profile hides: vacuous")
	}
	for _, c := range descx.SlotTokenCapabilities() {
		if toks, _ := descx.SlotTokensOf(c); len(toks) > 0 && !slotNamed["all-but/"+c] {
			t.Errorf("the unprofiled surfaces name no slot token all-but/%s hides: vacuous", c)
		}
	}
}

// servedText is one rendered surface: its lines, the label findings
// carry, and the export-style path exportSlotExempt keys on.
type servedText struct {
	surface, exemptPath string
	lines               []string
}

// servedTexts renders every surface the sweep reads for p.
func servedTexts(t *testing.T, p *Pulse) []servedText {
	t.Helper()
	var out []servedText
	addJSON := func(surface string, raw []byte) {
		out = append(out, servedText{surface: surface, lines: splitLines(jsonStringValues(t, raw))})
	}
	addText := func(surface, exemptPath, text string) {
		out = append(out, servedText{surface: surface, exemptPath: exemptPath, lines: strings.Split(text, "\n")})
	}

	m, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	addJSON("manifest", m)

	sess := NewMCPParitySession(t, p)
	raw, _ := sess.ListTools()
	addJSON("tools/list", raw)
	if raw, prompts := sess.ListPrompts(); raw != nil {
		addJSON("prompts/list", raw)
		for _, name := range prompts {
			addJSON("prompts/get "+name, sess.GetPrompt(name))
		}
	}
	raw, _ = sess.ListResources()
	addJSON("resources/list", raw)
	addJSON("resources/templates/list", sess.ListResourceTemplates())

	md, err := json.Marshal(p.Skills())
	if err != nil {
		t.Fatal(err)
	}
	addJSON("skills metadata", md)
	for _, s := range p.Skills() {
		body, _ := p.Skill(s.Name)
		addText("skill "+s.Name, "skills/"+s.Name+".md", body)
	}

	ex := p.ExamplesSearch("", nil, "")
	exj, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	addJSON("examples search", exj)
	for _, e := range ex {
		got, _ := p.ExampleGet(e.Name)
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		addJSON("example "+e.Name, b)
	}

	fsys := afero.NewMemMapFs()
	if err := p.ExportReference(fsys, "ref", ExportReferenceOptions{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	tree := exportTree(t, fsys, "ref")
	for _, path := range slices.Sorted(maps.Keys(tree)) {
		addText("export "+path, path, tree[path])
	}
	return out
}

// listedToolDescriptions returns tools/list's name → description.
func listedToolDescriptions(t *testing.T, p *Pulse) map[string]string {
	t.Helper()
	raw, _ := NewMCPParitySession(t, p).ListTools()
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range listed.Tools {
		out[tool.Name] = tool.Description
	}
	return out
}

// jsonStringValues returns every string VALUE of a JSON document (object
// keys excluded), in a deterministic order.
func jsonStringValues(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode served JSON: %v", err)
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(x)) {
				walk(x[k])
			}
		}
	}
	walk(doc)
	return out
}

func splitLines(texts []string) []string {
	var out []string
	for _, s := range texts {
		out = append(out, strings.Split(s, "\n")...)
	}
	return out
}

// firstMissingLine returns the first line of want absent from got.
func firstMissingLine(want, got string) string {
	for _, line := range strings.Split(want, "\n") {
		if !strings.Contains(got, line) {
			return line
		}
	}
	return want
}
