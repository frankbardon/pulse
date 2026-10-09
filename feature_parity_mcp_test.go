package pulse

// TestProfileInvisibilityParity: the MCP half of the invisibility
// contract, over every published example feature profile, every
// private fixture and two inline profiles (mcp-hosts, mcp-prompts:
// invisibilityProfiles says why). Two arms:
//
//   - operators: the hidden-name parity harness (runHiddenParityWith)
//     driven through MCP tool calls — pulse_process, pulse_predict,
//     pulse_compose (slot and overlays), pulse_process_chain (stage 0,
//     stage 1, overlays) and pulse_facet_schema — so a hidden operator
//     reaches a client exactly as a never-registered one does.
//   - surfaces: every rendered MCP surface (tools/list before and after
//     the bind-on-inspect rebind, prompts/list + prompts/get, resources
//     and templates, pulse://schema, the cohort resource, pulse-skill://
//     reads, the skills / examples / manifest / errors tools) names no
//     hidden operator, tool or prompt; and each hidden tool, prompt,
//     skill and example reads byte-identically (after name substitution)
//     to one that was never registered.
//
// The go-sdk client side is installed by feature_parity_mcp_bridge_test.go
// (package pulse_test), because gosdk imports this package.

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// MCPParitySession is one in-memory MCP client session over one
// instance. Every byte slice is the canonical JSON a client receives.
// CallTool returns "ok: <result>", "error: <result>" (an IsError tool
// result) or "protocol error: <text>" — the harness's parityOutcome
// convention. List* return the raw listing plus the sorted names / URIs.
type MCPParitySession struct {
	CallTool              func(name string, args any) []byte
	ListTools             func() ([]byte, []string)
	ListPrompts           func() ([]byte, []string)
	GetPrompt             func(name string) []byte
	ListResources         func() ([]byte, []string)
	ListResourceTemplates func() []byte
	ReadResource          func(uri string) []byte
}

// NewMCPParitySession is installed by the package pulse_test bridge.
var NewMCPParitySession func(t *testing.T, p *Pulse) *MCPParitySession

// mcpSessions caches one session per instance for the harness arm: the
// harness calls run three times per cell (hidden, never, control).
type mcpSessions struct {
	t  *testing.T
	by map[*Pulse]*MCPParitySession
}

func (s *mcpSessions) of(p *Pulse) *MCPParitySession {
	if sess, ok := s.by[p]; ok {
		return sess
	}
	sess := NewMCPParitySession(s.t, p)
	s.by[p] = sess
	return sess
}

// mcpToolMounted reports whether the instance mounts tool. A hidden
// tool is a never-registered one — the tool-call cell below pins it —
// so an operator cell through an unmounted tool would compare two
// "unknown tool" errors and prove nothing about the operator.
func mcpToolMounted(sess *MCPParitySession, tool string) bool {
	_, names := sess.ListTools()
	i := sort.SearchStrings(names, tool)
	return i < len(names) && names[i] == tool
}

// mcpArgs is v as the JSON object a client sends.
func mcpArgs(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	return out
}

// mcpParityEntryPoints are the MCP twins of the facade entry points.
func mcpParityEntryPoints(sessions *mcpSessions) []parityEntryPoint {
	call := func(t *testing.T, h *parityHost, tool string, args any) ([]byte, bool) {
		sess := sessions.of(h.p)
		if !mcpToolMounted(sess, tool) {
			return nil, false
		}
		return sess.CallTool(tool, mcpArgs(t, args)), true
	}
	return []parityEntryPoint{
		{name: "MCP/pulse_process", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			req, ok := h.request(c, op)
			if !ok {
				return nil, false
			}
			return call(t, h, "pulse_process", req)
		}},
		{name: "MCP/pulse_predict", neverOK: predictNoErrorChannel, vacuousOK: predictCrosstabVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			req, ok := h.request(c, op)
			if !ok {
				return nil, false
			}
			return call(t, h, "pulse_predict", req)
		}},
		{name: "MCP/pulse_compose", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			req, ok := h.request(c, op)
			if !ok {
				return nil, false
			}
			return call(t, h, "pulse_compose", &ComposedRequest{Requests: []*Request{req}})
		}},
		{name: "MCP/pulse_compose/overlays", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			req, ok := composeOverlayRequest(h, c, op)
			if !ok {
				return nil, false
			}
			return call(t, h, "pulse_compose", req)
		}},
		{name: "MCP/pulse_process_chain/stage0", vacuousOK: chainVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			req, ok := h.request(c, op)
			if !ok {
				return nil, false
			}
			return call(t, h, "pulse_process_chain", &ChainRequest{
				Cohort: &types.Cohort{Filename: h.cohort},
				Stages: []*types.ChainStage{{Name: "probe", Request: req}},
			})
		}},
		{name: "MCP/pulse_process_chain/stage1", vacuousOK: chainVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			if c.request == nil || h.chainBase == nil || !h.p.svc.InstanceSnapshot().Enabled(string(types.AGG_SUM)) {
				return nil, false
			}
			stage1 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: h.chainFields.num, Label: "total"}}}
			c.request(stage1, op, h.chainFields)
			return call(t, h, "pulse_process_chain", &ChainRequest{
				Cohort: &types.Cohort{Filename: h.cohort},
				Stages: []*types.ChainStage{{Name: "base", Request: h.chainBase()}, {Name: "probe", Request: stage1}},
			})
		}},
		{name: "MCP/pulse_process_chain/overlays", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			if c.chain == nil || h.chainBase == nil {
				return nil, false
			}
			req := &ChainRequest{
				Cohort: &types.Cohort{Filename: h.cohort},
				Stages: []*types.ChainStage{{Name: "base", Request: h.chainBase()}},
			}
			c.chain(req, op, h.fields)
			return call(t, h, "pulse_process_chain", req)
		}},
		{name: "MCP/pulse_facet_schema", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
			if c.facet == nil {
				return nil, false
			}
			req := &types.FacetRequest{Cohort: &types.Cohort{Filename: h.cohort}, Fields: []string{h.fields.num}}
			c.facet(req, op, h.fields)
			return call(t, h, "pulse_facet_schema", req)
		}},
	}
}

// invisibilityProfiles is every profile the gate runs: the published
// examples (keyed "example-<name>", inline) and the private fixtures
// (by file name). Returns the harness profile map and the sorted keys.
func invisibilityProfiles(t *testing.T) (map[string][]string, []string) {
	t.Helper()
	inline := map[string][]string{}
	names := append([]string(nil), featureSetFixtures...)
	examples := ExampleFeatureProfiles()
	if len(examples) == 0 {
		t.Fatal("no published example feature profile: the gate would skip them")
	}
	for _, name := range examples {
		fp, err := ExampleFeatureProfile(name)
		if err != nil {
			t.Fatalf("ExampleFeatureProfile(%s): %v", name, err)
		}
		inline["example-"+name] = fp.Features
		names = append(names, "example-"+name)
	}
	// No published profile both hides an operator and mounts every
	// request-carrying tool, so the pulse_compose / pulse_process_chain /
	// pulse_facet_schema cells would never run: one inline host profile
	// adds those capabilities (and one overlay kind per host it reaches)
	// to the minimal example's operators.
	minimal, err := ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatalf("ExampleFeatureProfile(minimal): %v", err)
	}
	inline["mcp-hosts"] = append(append([]string(nil), minimal.Features...),
		"capability:compose", "capability:process_chain", "capability:facet", "capability:crosstab",
		"OVERLAY_SHARE_OF_ROW", "OVERLAY_DELTA_VS_SIBLING", "OVERLAY_DELTA_VS_STAGE")
	// No published profile both mounts a prompt and hides a tool its
	// body names, so the prompt-body scrub would go unchecked: a
	// prompt-only profile (no request host) hides pulse_process, which
	// both prompt bodies name.
	inline["mcp-prompts"] = []string{"mcp_extra:prompt_bootstrap", "mcp_extra:prompt_author_request", "capability:facet"}
	names = append(names, "mcp-hosts", "mcp-prompts")
	sort.Strings(names)
	return inline, names
}

func TestProfileInvisibilityParity(t *testing.T) {
	if NewMCPParitySession == nil {
		t.Fatal("MCP bridge not installed (feature_parity_mcp_bridge_test.go)")
	}
	inline, names := invisibilityProfiles(t)
	t.Run("operators", func(t *testing.T) {
		sessions := &mcpSessions{t: t, by: map[*Pulse]*MCPParitySession{}}
		// mcp-prompts offers no base operator (like the empty fixture,
		// whose vacuity the harness allows by name): surfaces only.
		var hosts []string
		for _, n := range names {
			if n != "mcp-prompts" {
				hosts = append(hosts, n)
			}
		}
		runHiddenParityWith(t, parityFS(t), parityHostConfig{profiles: inline}, hosts, parityCategories, mcpParityEntryPoints(sessions))
	})
	t.Run("surfaces", func(t *testing.T) {
		fsys := parityFS(t)
		full := newParityHostWith(t, fsys, "", parityHostConfig{})
		fullSess := NewMCPParitySession(t, full.p)
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				h := newParityHostWith(t, fsys, name, parityHostConfig{profiles: inline})
				checkMCPSurfaces(t, h, NewMCPParitySession(t, h.p), fullSess)
			})
		}
	})
}

// mcpHiddenNames is what a profiled instance must never name, derived
// from the feature table independently of the scrub: every bare
// (operator) feature it hides, every MCP tool whose owning feature it
// hides, and every prompt whose feature it hides.
type mcpHiddenNames struct {
	tokens  map[string]bool // operators + tools: whole [A-Za-z0-9_] runs
	prompts []string        // prompt names carry '-': matched as substrings
	tools   []string
}

func hiddenMCPNames(t *testing.T, p *Pulse) mcpHiddenNames {
	t.Helper()
	inst := p.svc.InstanceSnapshot()
	out := mcpHiddenNames{tokens: map[string]bool{}}
	for _, n := range descx.FeatureNames() {
		if !strings.Contains(n, ":") && inst.Hidden(n) {
			out.tokens[n] = true
		}
	}
	for _, b := range descx.MCPToolBindings() {
		if b.Feature != "" && inst.Hidden(b.Feature) {
			out.tokens[b.Tool] = true
			out.tools = append(out.tools, b.Tool)
		}
	}
	for prompt, feature := range descx.MCPPromptFeatures() {
		if inst.Hidden(feature) {
			out.prompts = append(out.prompts, prompt)
		}
	}
	sort.Strings(out.tools)
	sort.Strings(out.prompts)
	if len(out.tokens) == 0 {
		t.Fatal("profile hides nothing: the sweep would be vacuous")
	}
	return out
}

// leaks returns the hidden names text carries.
func (h mcpHiddenNames) leaks(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
	}) {
		if h.tokens[tok] && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	for _, pr := range h.prompts {
		if strings.Contains(text, pr) {
			out = append(out, pr)
		}
	}
	return out
}

// toolPayload decodes the text content of a successful tool result.
func toolPayload(t *testing.T, outcome []byte, into any) {
	t.Helper()
	raw, ok := strings.CutPrefix(string(outcome), "ok: ")
	if !ok {
		t.Fatalf("tool call failed: %s", outcome)
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil || len(res.Content) == 0 {
		t.Fatalf("decode tool result: %v %s", err, raw)
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), into); err != nil {
		t.Fatalf("decode tool payload: %v", err)
	}
}

func listedSkills(t *testing.T, sess *MCPParitySession) []string {
	t.Helper()
	var out struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	toolPayload(t, sess.CallTool("pulse_skills_list", map[string]any{}), &out)
	names := make([]string, len(out.Skills))
	for i, s := range out.Skills {
		names[i] = s.Name
	}
	return names
}

func listedExamples(t *testing.T, sess *MCPParitySession) []string {
	t.Helper()
	var out struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	toolPayload(t, sess.CallTool("pulse_examples_search", map[string]any{}), &out)
	names := make([]string, len(out.Results))
	for i, r := range out.Results {
		names[i] = r.Name
	}
	return names
}

// minus returns the entries of all absent from kept.
func minus(all, kept []string) []string {
	in := map[string]bool{}
	for _, k := range kept {
		in[k] = true
	}
	var out []string
	for _, a := range all {
		if !in[a] {
			out = append(out, a)
		}
	}
	return out
}

// substParity requires hidden's outcome to equal never's after
// substituting the names.
func substParity(t *testing.T, surface, hidden, never string, got, want []byte) {
	t.Helper()
	if subst := strings.ReplaceAll(string(got), hidden, never); subst != string(want) {
		t.Errorf("%s: hidden %s diverges from never-registered %s\nhidden: %s\nnever:  %s", surface, hidden, never, got, want)
	}
}

func checkMCPSurfaces(t *testing.T, h *parityHost, sess, full *MCPParitySession) {
	hidden := hiddenMCPNames(t, h.p)
	rendered := map[string][]byte{}

	// Listings, before any rebind.
	raw, tools := sess.ListTools()
	rendered["tools/list"] = raw
	raw, prompts := sess.ListPrompts()
	rendered["prompts/list"] = raw
	for _, pr := range prompts {
		rendered["prompts/get "+pr] = sess.GetPrompt(pr)
	}
	raw, uris := sess.ListResources()
	rendered["resources/list"] = raw
	rendered["resources/templates/list"] = sess.ListResourceTemplates()
	rendered["read pulse://schema"] = sess.ReadResource("pulse://schema")
	rendered["read pulse://"+h.cohort] = sess.ReadResource("pulse://" + h.cohort)
	for _, uri := range uris {
		rendered["read "+uri] = sess.ReadResource(uri)
	}

	// Core discovery tools.
	skillNames := listedSkills(t, sess)
	exampleNames := listedExamples(t, sess)
	rendered["pulse_skills_list"] = sess.CallTool("pulse_skills_list", map[string]any{})
	for _, s := range skillNames {
		rendered["pulse_skills_get "+s] = sess.CallTool("pulse_skills_get", map[string]any{"name": s})
	}
	rendered["pulse_examples_search"] = sess.CallTool("pulse_examples_search", map[string]any{})
	for _, e := range exampleNames {
		rendered["pulse_examples_get "+e] = sess.CallTool("pulse_examples_get", map[string]any{"name": e})
	}
	rendered["pulse_manifest"] = sess.CallTool("pulse_manifest", map[string]any{})
	for _, domain := range []string{"CLI", "DATA", "ENCODING", "PROCESSING", "PULSE", "SERVICE"} {
		rendered["pulse_errors_lookup "+domain] = sess.CallTool("pulse_errors_lookup", map[string]any{"domain": domain})
	}

	// pulse_recommend, when mounted: every intent, unbound and bound to
	// the host cohort, names no hidden operator, tool or capability.
	if slices.Contains(tools, "pulse_recommend") {
		for _, in := range Intents() {
			rendered["pulse_recommend "+in.ID] = sess.CallTool("pulse_recommend", map[string]any{"intent": in.ID, "limit": 1000})
			rendered["pulse_recommend "+in.ID+" bound"] = sess.CallTool("pulse_recommend", map[string]any{"intent": in.ID, "cohort": h.cohort, "limit": 1000})
		}
	}

	// The bind-on-inspect rebind re-adds tools by name: re-render.
	rendered["pulse_inspect"] = sess.CallTool("pulse_inspect", map[string]any{"path": h.cohort})
	raw, rebound := sess.ListTools()
	rendered["tools/list (rebound)"] = raw

	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if leaked := hidden.leaks(string(rendered[k])); len(leaked) > 0 {
			t.Errorf("%s names hidden %v", k, leaked)
		}
	}

	// Hidden ≡ never registered, per hidden tool, prompt, skill, example.
	_, fullTools := full.ListTools()
	if got := minus(fullTools, tools); strings.Join(got, ",") != strings.Join(hidden.tools, ",") {
		t.Errorf("tools/list hides %v, the feature table hides %v", got, hidden.tools)
	}
	if got := minus(fullTools, rebound); strings.Join(got, ",") != strings.Join(hidden.tools, ",") {
		t.Errorf("rebound tools/list hides %v, the feature table hides %v", got, hidden.tools)
	}
	for _, tool := range hidden.tools {
		const never = "pulse_never_registered"
		substParity(t, "tools/call", tool, never, sess.CallTool(tool, map[string]any{}), sess.CallTool(never, map[string]any{}))
	}
	for _, pr := range hidden.prompts {
		const never = "pulse-never-registered"
		substParity(t, "prompts/get", pr, never, sess.GetPrompt(pr), sess.GetPrompt(never))
	}
	_, fullPrompts := full.ListPrompts()
	if got := minus(fullPrompts, prompts); strings.Join(got, ",") != strings.Join(hidden.prompts, ",") {
		t.Errorf("prompts/list hides %v, the feature table hides %v", got, hidden.prompts)
	}

	prunedSkills := minus(listedSkills(t, full), skillNames)
	if len(prunedSkills) == 0 {
		t.Error("no skill pruned: the skill cells would be vacuous")
	}
	for _, s := range prunedSkills {
		const never = "op-never-registered"
		substParity(t, "pulse_skills_get", s, never,
			sess.CallTool("pulse_skills_get", map[string]any{"name": s}),
			sess.CallTool("pulse_skills_get", map[string]any{"name": never}))
		substParity(t, "read pulse-skill://", s, never,
			sess.ReadResource("pulse-skill://"+s), sess.ReadResource("pulse-skill://"+never))
	}
	for _, e := range minus(listedExamples(t, full), exampleNames) {
		const never = "example-never-registered"
		substParity(t, "pulse_examples_get", e, never,
			sess.CallTool("pulse_examples_get", map[string]any{"name": e}),
			sess.CallTool("pulse_examples_get", map[string]any{"name": never}))
		// Search is a substring match, so a visible example may still
		// hit the query; the pruned one must not be among the results.
		if hits := sess.CallTool("pulse_examples_search", map[string]any{"query": e}); strings.Contains(string(hits), `\"name\":\"`+e+`\"`) {
			t.Errorf("pulse_examples_search by name returns pruned example %s", e)
		}
	}
}
