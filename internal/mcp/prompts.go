package mcp

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Prompt name constants. Exposed via the MCP prompts/list capability so
// clients can surface them as slash commands or auto-inject the bootstrap
// message into a session.
const (
	PromptBootstrap     = "pulse-bootstrap"
	PromptAuthorRequest = "pulse-author-request"
)

// Prompt descriptions — what the prompt does, when to call it. These
// strings reach the LLM via the MCP prompts/list response, so they should
// read like tool descriptions: imperative, no marketing.
const (
	DescPromptBootstrap     = "Inject the Pulse session-bootstrap instructions into the conversation. Tells the assistant which tools to call (and in what order) before authoring any request, and where the authoritative request-shape references live. Useful when starting a fresh session against a Pulse MCP server."
	DescPromptAuthorRequest = "Guided workflow for authoring a Pulse request from a natural-language question. Takes one argument: `question` — the user's analytical ask in plain English. Produces a sequence of tool-call instructions the assistant should follow to discover the right operators and example template."
)

// registerPrompts attaches the prompts/list + prompts/get capability to the
// MCP server. The bootstrap prompt is the primary signal we use to steer
// remote LLM clients away from inferring request shapes from external
// documentation or source code — and toward the manifest + example library.
func registerPrompts(s *server.MCPServer) {
	s.AddPrompt(
		mcpgo.NewPrompt(
			PromptBootstrap,
			mcpgo.WithPromptDescription(DescPromptBootstrap),
		),
		bootstrapPromptHandler,
	)

	s.AddPrompt(
		mcpgo.NewPrompt(
			PromptAuthorRequest,
			mcpgo.WithPromptDescription(DescPromptAuthorRequest),
			mcpgo.WithArgument("question",
				mcpgo.ArgumentDescription("The user's analytical question in plain English. Passed through to pulse_ask in the recommended flow."),
				mcpgo.RequiredArgument(),
			),
		),
		authorRequestPromptHandler,
	)
}

// bootstrapPromptBody is the canonical "how to use Pulse" preamble. Hand-
// authored, kept short so clients with token budgets can inject it at the
// top of every session. Mirrors the discovery flow encoded in the tool
// descriptions; the prompt exists for clients that surface prompts more
// prominently than tool descriptions.
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
4. Call ` + "`pulse_ask`" + ` (preferred) with the request and a source path, OR ` + "`pulse_predict`" + ` then ` + "`pulse_process`" + ` for finer control.
5. On any error code in the response envelope, call ` + "`pulse_errors_lookup`" + ` for the prescribed fix.

# When the example library does not match

Fall back to ` + "`pulse_skills_get`" + ` for the operator family you need, then assemble the request from the manifest's operator metadata. Still do not infer from external sources — every operator-shape question can be answered locally.
`

func bootstrapPromptHandler(_ context.Context, _ mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
	return &mcpgo.GetPromptResult{
		Description: DescPromptBootstrap,
		Messages: []mcpgo.PromptMessage{
			mcpgo.NewPromptMessage(mcpgo.RoleUser, mcpgo.NewTextContent(bootstrapPromptBody)),
		},
	}, nil
}

func authorRequestPromptHandler(_ context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
	question := req.Params.Arguments["question"]
	body := "Author a Pulse request for this analytical question:\n\n" +
		"> " + question + "\n\n" +
		"Follow this discovery flow:\n\n" +
		"1. Call `pulse_manifest` once (skip if you already have it cached this session).\n" +
		"2. Call `pulse_examples_search` with the most distinctive keywords from the question. Try several searches if the first returns nothing useful.\n" +
		"3. If a relevant example exists, `pulse_examples_get` to retrieve the runnable body. Modify field names for the target cohort.\n" +
		"4. If no example matches, identify the operator family from the manifest and call `pulse_skills_get` on the relevant skill (e.g. `regression-modeling`, `statistical-testing`).\n" +
		"5. Call `pulse_ask` with the source path and the request body, OR with a natural-language `query` if the assembled request still feels uncertain.\n" +
		"6. On any error code in the response, call `pulse_errors_lookup` for the prescribed fix.\n\n" +
		"Do not infer request shapes from external documentation or source code — the manifest + example library are authoritative for this deployment."
	return &mcpgo.GetPromptResult{
		Description: DescPromptAuthorRequest,
		Messages: []mcpgo.PromptMessage{
			mcpgo.NewPromptMessage(mcpgo.RoleUser, mcpgo.NewTextContent(body)),
		},
	}, nil
}

// RegisteredPrompts returns the canonical list of prompt names this server
// registers. Stable order. Used by tests + manifest aggregation.
func RegisteredPrompts() []string {
	return []string{
		PromptBootstrap,
		PromptAuthorRequest,
	}
}
