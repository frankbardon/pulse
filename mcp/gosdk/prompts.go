package gosdk

import (
	"context"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prompt name constants. Exposed via the MCP prompts/list capability so
// clients can surface them as slash commands or auto-inject the bootstrap
// message into a session.
const (
	PromptBootstrap     = "pulse-bootstrap"
	PromptAuthorRequest = "pulse-author-request"
)

// Prompt descriptions — what the prompt does, when to call it. These strings
// reach the LLM via the MCP prompts/list response, so they should read like
// tool descriptions: imperative, no marketing.
const (
	DescPromptBootstrap     = "Inject the Pulse session-bootstrap instructions into the conversation. Tells the assistant which tools to call (and in what order) before authoring any request, and where the authoritative request-shape references live. Useful when starting a fresh session against a Pulse MCP server."
	DescPromptAuthorRequest = "Guided workflow for authoring a Pulse request as JSON against a cohort's schema. Takes one argument: `question` — the analytical question being answered. Produces a sequence of tool-call instructions the assistant should follow to discover the right operators and example template."
)

// promptRoleUser is the MCP "user" message role. go-sdk types Role as a bare
// string and does not export a constant, so we pin it here.
const promptRoleUser mcpsdk.Role = "user"

// registerPrompts attaches the prompts/list + prompts/get capability to the
// caller-supplied server. The bootstrap prompt is the primary signal we use
// to steer remote LLM clients away from inferring request shapes from external
// documentation or source code — and toward the manifest + example library.
// A prompt whose mcp_extra:prompt_* feature the instance does not offer is
// not registered. Every description and body loses the sentences naming a
// feature the instance hides; the scrub runs once, here, never per call.
func registerPrompts(s *mcpsdk.Server, p *pulse.Pulse) {
	inst := instanceOf(p)
	scrub := descx.NewProseScrub(inst)
	if promptEnabled(inst, PromptBootstrap) {
		desc := scrub.Text(DescPromptBootstrap)
		s.AddPrompt(&mcpsdk.Prompt{
			Name:        PromptBootstrap,
			Description: desc,
		}, bootstrapPromptHandler(desc, scrub.Text(bootstrapPromptBody)))
	}

	if promptEnabled(inst, PromptAuthorRequest) {
		desc := scrub.Text(DescPromptAuthorRequest)
		s.AddPrompt(&mcpsdk.Prompt{
			Name:        PromptAuthorRequest,
			Description: desc,
			Arguments: []*mcpsdk.PromptArgument{
				{
					Name:        "question",
					Description: scrub.Text(authorRequestQuestionDesc),
					Required:    true,
				},
			},
		}, authorRequestPromptHandler(desc, scrub.Text(authorRequestFlow)))
	}

	registerIntentPrompts(s, inst, scrub)
}

// authorRequestQuestionDesc describes the author-request prompt argument.
const authorRequestQuestionDesc = "The analytical question being answered. Used to drive example-library and skill-pack discovery."

// bootstrapPromptBody is the canonical "how to use Pulse" preamble. Hand-
// authored, kept short so clients with token budgets can inject it at the top
// of every session.
const bootstrapPromptBody = `You are using a Pulse MCP server to answer analytical questions about tabular data.

# Authoritative references for THIS Pulse deployment

The operator catalog, request-shape contracts, and runnable examples ship with the server. Do not infer request shapes from external documentation, blog posts, or source code — those may be out of date for this deployment.

1. **pulse_manifest** — call once at session start, cache the result. Lists every registered aggregator, attribute, filterer, grouper, window, feature, regression, and statistical test, with their params, accepted field types, and streamability flags.
2. **pulse_examples_search** + **pulse_examples_get** — the example library. Search by keywords, tags, or category to find a runnable template that matches the user's question; fetch the body and modify the field names for the target cohort.
3. **pulse_skills_list** + **pulse_skills_get** — domain guides for operator families (regression-modeling, statistical-testing, financial-cohorts, geospatial-cohorts, etc.).
4. **pulse_errors_lookup** — per-code Message + Fixup detail.

# Recommended flow for a new question

1. Call ` + "`pulse_manifest`" + ` if you haven't this session.
2. Call ` + "`pulse_examples_search`" + ` with keywords from the user's question.
3. If a match is found, ` + "`pulse_examples_get`" + ` and clone the body. Swap field names to match the target cohort.
4. Call ` + "`pulse_predict`" + ` to validate the assembled request. Then call ` + "`pulse_process`" + ` to execute it.
5. On any error code in the response envelope, call ` + "`pulse_errors_lookup`" + ` for the prescribed fix.

# When the example library does not match

Fall back to ` + "`pulse_skills_get`" + ` for the operator family you need, then assemble the request from the manifest's operator metadata. Still do not infer from external sources — every operator-shape question can be answered locally.
`

// bootstrapPromptHandler serves the (registration-time scrubbed)
// bootstrap description and body.
func bootstrapPromptHandler(desc, body string) mcpsdk.PromptHandler {
	return func(_ context.Context, _ *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		return &mcpsdk.GetPromptResult{
			Description: desc,
			Messages: []*mcpsdk.PromptMessage{
				{Role: promptRoleUser, Content: &mcpsdk.TextContent{Text: body}},
			},
		}, nil
	}
}

// authorRequestFlow is the author-request body after the quoted
// question. It is scrubbed at registration; the caller's question is
// user content and is never scrubbed.
const authorRequestFlow = "Follow this discovery flow:\n\n" +
	"1. Call `pulse_manifest` once (skip if you already have it cached this session).\n" +
	"2. Call `pulse_examples_search` with the most distinctive keywords from the question. Try several searches if the first returns nothing useful.\n" +
	"3. If a relevant example exists, `pulse_examples_get` to retrieve the runnable body. Modify field names for the target cohort.\n" +
	"4. If no example matches, identify the operator family from the manifest and call `pulse_skills_get` on the relevant skill (e.g. `regression-modeling`, `statistical-testing`).\n" +
	"5. Submit the assembled request to `pulse_predict` to validate. If validation passes, submit it to `pulse_process` to execute. If validation fails with structured suggestions, apply the suggested fixups and retry `pulse_predict`.\n" +
	"6. On any error code in the response, call `pulse_errors_lookup` for the prescribed fix.\n\n" +
	"Do not infer request shapes from external documentation or source code — the manifest + example library are authoritative for this deployment."

// authorRequestPromptHandler serves the author-request prompt with the
// caller's question spliced ahead of the pre-scrubbed flow.
func authorRequestPromptHandler(desc, flow string) mcpsdk.PromptHandler {
	return func(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		var question string
		if req != nil && req.Params != nil {
			question = req.Params.Arguments["question"]
		}
		body := "Author a Pulse request for this analytical question:\n\n" +
			"> " + question + "\n\n" + flow
		return &mcpsdk.GetPromptResult{
			Description: desc,
			Messages: []*mcpsdk.PromptMessage{
				{Role: promptRoleUser, Content: &mcpsdk.TextContent{Text: body}},
			},
		}, nil
	}
}

// RegisteredPrompts returns the global canonical list of prompt names
// Register can mount: the two hand-written prompts, then one
// `pulse-<intent>` prompt per analytic intent in intent-registry order.
// It describes the build, not an instance: a feature profile mounts only
// the prompts it offers, and this list does not shrink. Stable order.
// Used by tests + manifest aggregation.
func RegisteredPrompts() []string {
	out := []string{
		PromptBootstrap,
		PromptAuthorRequest,
	}
	for _, ip := range descx.MCPIntentPrompts() {
		out = append(out, ip.Prompt)
	}
	return out
}

// intentPromptCohortDesc describes every intent prompt's cohort argument.
const intentPromptCohortDesc = "Path to the .pulse cohort to analyse (relative to the data directory). Omit it and the assistant asks which cohort to use."

// intentPromptRoleDesc describes one field-hint argument; the role name
// is spliced in.
func intentPromptRoleDesc(role string) string {
	return "Field hint for the `" + role + "` role: one or more cohort field names, comma-separated. Passed to pulse_recommend as fields hints."
}

// intentPromptDesc is the description of one intent prompt.
func intentPromptDesc(in descriptor.Intent, roles []string) string {
	return "Guided `" + in.ID + "` workflow (" + in.Label + "). " +
		"Inspects the cohort, drafts a request with pulse_recommend, confirms it with pulse_explain, runs it with pulse_process and explains the result. " +
		"Every argument is optional: `cohort` plus one field hint per role (" + backtickList(roles) + ")."
}

// intentPromptFlow is the shared body template after the inputs block,
// for one intent. It is scrubbed at registration; the caller's cohort
// and field hints are user content and never scrubbed.
func intentPromptFlow(intentID string) string {
	return "Follow this guided workflow:\n\n" +
		"1. Call `pulse_inspect` on the cohort to read its fields and types. If no cohort was given, ask the user which cohort to use first.\n" +
		"2. Call `pulse_recommend` with `intent: \"" + intentID + "\"`, the cohort, and `fields` set to every field hint above, in role order. Pick the top recommendation whose `bound` is true, and fill each `needs[].param` with the user.\n" +
		"3. Call `pulse_explain` with the draft as `request` and read its summary back to the user. Confirm it answers their question before running anything.\n" +
		"4. Call `pulse_process` with the confirmed request.\n" +
		"5. Call `pulse_explain` with the same `request` and the result as `response`, then present its findings and caveats.\n" +
		"6. On any error code, call `pulse_errors_lookup` for the prescribed fix.\n\n" +
		"Do not author the request from memory: the recommended draft is predict-checked against this deployment."
}

// intentRoles returns the union of an intent's shape role names, in
// first-seen order.
func intentRoles(in descriptor.Intent) []string {
	var out []string
	seen := map[string]bool{}
	for _, sh := range in.Shapes {
		for _, r := range sh.Roles {
			if !seen[r.Name] {
				seen[r.Name] = true
				out = append(out, r.Name)
			}
		}
	}
	return out
}

func backtickList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return strings.Join(quoted, ", ")
}

// registerIntentPrompts mounts one prompt per analytic intent whose
// feature the instance enables and whose intent survives the
// instance's ontology prune. Descriptions, argument descriptions and
// the flow are scrubbed once, here.
func registerIntentPrompts(s *mcpsdk.Server, inst *descx.InstanceSnapshot, scrub descx.ProseScrub) {
	intents := map[string]descriptor.Intent{}
	for _, in := range descx.Intents() {
		intents[in.ID] = in
	}
	for _, ip := range descx.MCPIntentPrompts() {
		if !intentPromptEnabled(inst, ip) {
			continue
		}
		in := intents[ip.Intent]
		roles := intentRoles(in)
		args := []*mcpsdk.PromptArgument{{Name: "cohort", Description: scrub.Text(intentPromptCohortDesc)}}
		for _, r := range roles {
			args = append(args, &mcpsdk.PromptArgument{Name: r, Description: scrub.Text(intentPromptRoleDesc(r))})
		}
		desc := scrub.Text(intentPromptDesc(in, roles))
		s.AddPrompt(&mcpsdk.Prompt{
			Name:        ip.Prompt,
			Description: desc,
			Arguments:   args,
		}, intentPromptHandler(desc, in, roles, scrub.Text(intentPromptFlow(in.ID))))
	}
}

// intentPromptHandler serves one intent prompt: a header naming the
// intent, the caller's cohort and field hints (verbatim user content),
// then the pre-scrubbed flow.
func intentPromptHandler(desc string, in descriptor.Intent, roles []string, flow string) mcpsdk.PromptHandler {
	return func(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		var argv map[string]string
		if req != nil && req.Params != nil {
			argv = req.Params.Arguments
		}
		var b strings.Builder
		b.WriteString("Answer a \"" + in.Label + "\" question (intent `" + in.ID + "`) on a Pulse cohort.\n\n")
		if c := strings.TrimSpace(argv["cohort"]); c != "" {
			b.WriteString("Cohort: `" + c + "`\n")
		} else {
			b.WriteString("Cohort: not given.\n")
		}
		var hints []string
		for _, r := range roles {
			if v := strings.TrimSpace(argv[r]); v != "" {
				hints = append(hints, "- "+r+": "+v)
			}
		}
		if len(hints) == 0 {
			b.WriteString("Field hints: none given.\n\n")
		} else {
			b.WriteString("Field hints:\n" + strings.Join(hints, "\n") + "\n\n")
		}
		b.WriteString(flow)
		return &mcpsdk.GetPromptResult{
			Description: desc,
			Messages: []*mcpsdk.PromptMessage{
				{Role: promptRoleUser, Content: &mcpsdk.TextContent{Text: b.String()}},
			},
		}, nil
	}
}
