package gosdk_test

import (
	"context"
	stderrors "errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/spf13/afero"
)

// intentPromptToolFeatures are the features every intent prompt depends
// on: the tools its script calls that are not core.
var intentPromptToolFeatures = []string{"capability:recommend", "capability:explain", "capability:process"}

// allIntentPromptFeatures returns every intent prompt's feature.
func allIntentPromptFeatures() []string {
	var out []string
	for _, ip := range descx.MCPIntentPrompts() {
		out = append(out, ip.Feature)
	}
	return out
}

// analyticIntents returns the analytic intent IDs in registry order.
func analyticIntents() []string {
	var out []string
	for _, in := range pulse.Intents() {
		if in.Analytic {
			out = append(out, in.ID)
		}
	}
	return out
}

// TestRegister_IntentPromptsMountProfileFree pins that a profile-free
// instance mounts every registered prompt: the two hand-written ones
// plus one per analytic intent.
func TestRegister_IntentPromptsMountProfileFree(t *testing.T) {
	c, cancel := profiledServer(t, afero.NewMemMapFs(), nil, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	want := gosdk.RegisteredPrompts()
	slices.Sort(want)
	if got := promptNames(t, c); !slices.Equal(got, want) {
		t.Errorf("profile-free prompts = %v, want %v", got, want)
	}
	if n := len(descx.MCPIntentPrompts()); n != len(analyticIntents()) || n != 12 {
		t.Errorf("intent prompts = %d, want one per analytic intent (12)", n)
	}
}

// TestRegister_IntentPromptFeatureHidesPrompt pins that a profile
// omitting one intent prompt's feature unmounts exactly that prompt.
func TestRegister_IntentPromptFeatureHidesPrompt(t *testing.T) {
	fp, err := pulse.ExampleFeatureProfile("read-only-analyst")
	if err != nil {
		t.Fatal(err)
	}
	const hidden = "mcp_extra:prompt_compare_groups"
	fp.Features = slices.DeleteFunc(fp.Features, func(f string) bool { return f == hidden })
	c, cancel := profiledServer(t, afero.NewMemMapFs(), fp, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	got := promptNames(t, c)
	if slices.Contains(got, "pulse-compare-groups") {
		t.Errorf("hidden prompt pulse-compare-groups still mounted: %v", got)
	}
	for _, ip := range descx.MCPIntentPrompts() {
		if ip.Feature != hidden && !slices.Contains(got, ip.Prompt) {
			t.Errorf("enabled prompt %s not mounted: %v", ip.Prompt, got)
		}
	}
}

// TestFeatureProfile_IntentPromptNeedsItsTools pins the dependency
// edges: an intent prompt without any one of the tools its script
// calls is refused at pulse.New.
func TestFeatureProfile_IntentPromptNeedsItsTools(t *testing.T) {
	for _, missing := range intentPromptToolFeatures {
		t.Run(missing, func(t *testing.T) {
			features := []string{"mcp_extra:prompt_describe", "AGG_SUM"}
			for _, f := range intentPromptToolFeatures {
				if f != missing {
					features = append(features, f)
				}
			}
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: &pulse.FeatureProfile{Features: features}})
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_FEATURE_PROFILE_DEPENDENCY {
				t.Fatalf("want PULSE_FEATURE_PROFILE_DEPENDENCY, got %v", err)
			}
		})
	}
	deps, ok := descx.FeatureDependencies("mcp_extra:prompt_describe")
	if !ok {
		t.Fatal("mcp_extra:prompt_describe is not a feature row")
	}
	want := [][]string{{"capability:recommend"}, {"capability:explain"}, {"capability:process"}}
	if !slices.EqualFunc(deps, want, slices.Equal[[]string]) {
		t.Errorf("DependsOn = %v, want %v", deps, want)
	}
}

// TestRegister_IntentPromptFollowsOntologyPrune pins that an intent
// whose every serving operator is hidden mounts no prompt, even with
// the prompt's feature enabled, and that the surviving intents do.
func TestRegister_IntentPromptFollowsOntologyPrune(t *testing.T) {
	minimal, err := pulse.ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatal(err)
	}
	features := append(append([]string(nil), minimal.Features...), allIntentPromptFeatures()...)
	for _, f := range intentPromptToolFeatures {
		if !slices.Contains(features, f) {
			features = append(features, f)
		}
	}
	fp := &pulse.FeatureProfile{Features: features}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	g := facadebridge.InstanceSnapshot(p).Ontology()
	var want, pruned []string
	for _, ip := range descx.MCPIntentPrompts() {
		if _, ok := g.Node(descx.OntologyID(descriptor.OntologyNodeIntent, ip.Intent)); ok {
			want = append(want, ip.Prompt)
		} else {
			pruned = append(pruned, ip.Prompt)
		}
	}
	if len(want) == 0 || len(pruned) == 0 {
		t.Fatalf("vacuous: mounted %v, pruned %v", want, pruned)
	}
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	slices.Sort(want)
	if got := promptNames(t, c); !slices.Equal(got, want) {
		t.Errorf("prompts = %v, want the surviving intents' %v (pruned %v)", got, want, pruned)
	}
}

// TestRegister_IntentPromptContent pins the shared template: the
// arguments are cohort plus the intent's role union, the caller's
// values are spliced verbatim, and the script runs inspect → recommend
// → explain(request) → process → explain(response) in that order.
func TestRegister_IntentPromptContent(t *testing.T) {
	c, cancel := profiledServer(t, afero.NewMemMapFs(), nil, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	ctx := context.Background()
	list, err := c.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	var args []string
	for _, pr := range list.Prompts {
		if pr.Name != "pulse-compare-groups" {
			continue
		}
		for _, a := range pr.Arguments {
			args = append(args, a.Name)
			if a.Required {
				t.Errorf("argument %s is required; every intent prompt argument is optional", a.Name)
			}
		}
	}
	if !slices.Equal(args, []string{"cohort", "outcome", "group"}) {
		t.Errorf("pulse-compare-groups arguments = %v, want [cohort outcome group]", args)
	}

	text := promptText(t, c, "pulse-compare-groups", map[string]string{"cohort": "sales.pulse", "outcome": "revenue", "group": "region"})
	for _, want := range []string{"intent `compare_groups`", "Cohort: `sales.pulse`", "- outcome: revenue", "- group: region", "`intent: \"compare_groups\"`"} {
		if !strings.Contains(text, want) {
			t.Errorf("body lacks %q:\n%s", want, text)
		}
	}
	steps := []string{"1. Call `pulse_inspect`", "2. Call `pulse_recommend`", "3. Call `pulse_explain` with the draft as `request`", "4. Call `pulse_process`", "5. Call `pulse_explain` with the same `request` and the result as `response`"}
	last := -1
	for _, s := range steps {
		i := strings.Index(text, s)
		if i <= last {
			t.Errorf("step %q missing or out of order:\n%s", s, text)
		}
		last = i
	}

	bare := promptText(t, c, "pulse-flows", nil)
	if !strings.Contains(bare, "Cohort: not given.") || !strings.Contains(bare, "Field hints: none given.") {
		t.Errorf("argument-free body does not say what is missing:\n%s", bare)
	}
}

// TestSkillsCoverAllMCPPrompts is the prompt coverage gate. Every
// analytic intent has exactly one generated prompt and no tooling
// intent has one; every registered prompt is documented by name in the
// MCP prompt table (docs/src/mcp/index.md); and the session-bootstrap
// skill names each hand-written prompt inside a fence of its own
// feature plus the generated `pulse-<intent>` family. Skills name no
// concrete intent prompt: a served body must not name a prompt the
// instance may hide.
func TestSkillsCoverAllMCPPrompts(t *testing.T) {
	// Intent ↔ prompt bijection over the analytic intents.
	byPrompt := map[string]string{}
	for _, ip := range descx.MCPIntentPrompts() {
		byPrompt[ip.Prompt] = ip.Intent
	}
	for _, in := range pulse.Intents() {
		name := "pulse-" + strings.ReplaceAll(in.ID, "_", "-")
		_, has := byPrompt[name]
		switch {
		case in.Analytic && !has:
			t.Errorf("analytic intent %s has no prompt %s", in.ID, name)
		case !in.Analytic && has:
			t.Errorf("tooling intent %s must not have a prompt (it routes to tools)", in.ID)
		}
	}
	registered := gosdk.RegisteredPrompts()
	for name := range byPrompt {
		if !slices.Contains(registered, name) {
			t.Errorf("intent prompt %s is not in RegisteredPrompts", name)
		}
	}

	// Docs: every registered prompt has a row in the MCP prompt table.
	doc, err := os.ReadFile("../../docs/src/mcp/index.md")
	if err != nil {
		t.Fatalf("read MCP docs: %v", err)
	}
	for _, name := range registered {
		if !regexp.MustCompile("(?m)^\\| `" + regexp.QuoteMeta(name) + "`").Match(doc) {
			t.Errorf("prompt %s has no row in the docs/src/mcp/index.md prompt table", name)
		}
	}

	// Skills: session-bootstrap names each hand-written prompt fenced
	// by its feature, and the generated family by pattern only.
	raw, ok := skills.Raw("session-bootstrap")
	if !ok {
		t.Fatal("session-bootstrap skill missing")
	}
	features := descx.MCPPromptFeatures()
	for _, name := range registered {
		if _, generated := byPrompt[name]; generated {
			if strings.Contains(raw, "`"+name+"`") {
				t.Errorf("session-bootstrap names intent prompt %s: name the `pulse-<intent>` family instead", name)
			}
			continue
		}
		fenced := regexp.MustCompile("<!-- feature: " + regexp.QuoteMeta(features[name]) + " -->[^\\n]*`" + regexp.QuoteMeta(name) + "`")
		if !fenced.MatchString(raw) {
			t.Errorf("session-bootstrap does not name prompt %s inside a %s fence", name, features[name])
		}
	}
	if !strings.Contains(raw, "`pulse-<intent>`") {
		t.Error("session-bootstrap does not name the `pulse-<intent>` intent-prompt family")
	}
}
