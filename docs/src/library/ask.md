# pulse.Ask — Unified Entry Point

**Audience:** Go embedders who want a single call that validates a
request and then optionally executes it.

`Ask` is the one-shot facade. It collapses `predict`, `process`, and
the natural-language `query` parser into a single typed call. The
MCP server uses this same method internally for the `pulse_ask`
tool.

> **LLM agents using MCP:** the corresponding LLM-facing surface is
> the `pulse_ask` MCP tool, documented in `skills/mcp-integration.md`
> and `skills/request-recipes.md`.

## When to use Ask vs Process

| Goal | Reach for |
|---|---|
| Validate a request without running it | `Predict` (or `Ask{Predict: true}`) |
| Validate then execute in one call | `Ask` |
| Translate a natural-language string into a request and execute | `Ask` with `Query` set |
| Execute a request you've already validated separately | `Process` (lower overhead) |

If you're already inside a tight loop that validates once and runs
many similar requests, prefer `Process` — `Ask` does the predict pass
on every call.

## Request shape

From [`pulse.go`](https://github.com/frankbardon/pulse/blob/main/pulse.go):

```go
type AskRequest struct {
    File      string         `json:"file,omitempty"`
    Request   *types.Request `json:"request,omitempty"`
    Query     string         `json:"query,omitempty"`
    OnInvalid string         `json:"on_invalid,omitempty"`
    Predict   bool           `json:"predict,omitempty"`
}
```

| Field | Meaning |
|---|---|
| `File`      | Cohort path. When set and `Request.Cohort` is nil, Ask synthesises a `Cohort` from the path. |
| `Request`   | Structured `types.Request`. Optional when `Query` is set — the parser fills empty slots. |
| `Query`     | Natural-language query string ("average revenue by region"). Parsed against the cohort's schema. |
| `OnInvalid` | `"abort"` (default) returns a `SERVICE_VALIDATION` error on predict-invalid; `"suggest"` returns the response with `Suggestions` populated. |
| `Predict`   | When `true`, skip execution after a successful predict. The "what would happen if I ran this" probe. |

## Response shape

```go
type AskResponse struct {
    FormatVersion   string                      `json:"format_version"`
    Predict         *descriptor.PredictResult   `json:"predict"`
    Process         *Response                   `json:"process,omitempty"`
    Suggestions     []errors.Fixup              `json:"suggestions,omitempty"`
    QueryResolution *QueryResolution            `json:"query_resolution,omitempty"`
    Errors          []*descriptor.EnvelopeEntry `json:"errors"`
    Warnings        []*descriptor.EnvelopeEntry `json:"warnings"`
}
```

- `Predict` is always populated.
- `Process` is set only when execution ran.
- `Suggestions` is populated only when predict reported invalid and
  `OnInvalid == "suggest"`.
- `QueryResolution` is set only when `Query` was non-empty; it echoes
  the parser's matched fields and aggregate confidence in `[0, 1]`.

## Examples

### Structured request, predict-only

```go
resp, err := p.Ask(ctx, &pulse.AskRequest{
    Request: &pulse.Request{
        Cohort: &types.Cohort{Filename: "sales.pulse"},
        Aggregations: []*types.Aggregation{
            {Type: types.AGG_SUM, Field: "revenue", Label: "total"},
        },
    },
    Predict: true,
})
```

### Natural-language query

```go
resp, err := p.Ask(ctx, &pulse.AskRequest{
    File:  "sales.pulse",
    Query: "average revenue by region",
})
fmt.Printf("matched: %v (conf %.2f)\n",
    resp.QueryResolution.MatchedFields,
    resp.QueryResolution.Confidence)
```

The parser fills the structured request from the query and runs
`Process`. Explicit fields in `Request` always win on collision —
the parser only fills empty slots.

### Query plus a partial structured request

```go
resp, err := p.Ask(ctx, &pulse.AskRequest{
    File: "sales.pulse",
    Request: &pulse.Request{
        Filterers: []*types.Filterer{
            {Type: types.FILTER_RANGE, Field: "revenue", Values: []string{"100", "1000"}},
        },
    },
    Query: "average revenue by region",
})
```

The structured `Filterers` win; the parser supplies `Aggregations`
and `Groups` from the query.

### Suggest fixups instead of erroring

```go
resp, err := p.Ask(ctx, &pulse.AskRequest{
    Request:   req,
    OnInvalid: "suggest",
})
for _, fix := range resp.Suggestions {
    fmt.Println(fix.Code, fix.Message, fix.Hint)
}
```

Fixup templates live in `errors/fixup_metadata.go` and are documented
per code in `skills/error-code-reference.md`.

## Errors and warnings

`AskResponse.Errors` and `AskResponse.Warnings` flatten the descriptor
envelope's entries plus any issues the query parser raised
(`PULSE_QUERY_UNRESOLVED`, `PULSE_QUERY_AMBIGUOUS`). The arrays are
always present (never nil) so JSON consumers can index without
null-checks — same shape as the descriptor envelope.

`FormatVersion` mirrors the descriptor envelope version (`"1.0"`)
so callers can gate on a single value across endpoints.
