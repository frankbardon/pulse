# Embedder Walkthrough

**Audience:** Go developers putting Pulse inside a service or a binary
for the first time. Each step is one decision; its page holds the detail.

Start from the [Go API Overview](overview.md) (module path, the facade
methods, a minimum embed), then work down this list. Every step is
optional except the first.

## 1. Construct the instance

`pulse.New(pulse.Options{…})` is the only entry point: no config file,
no global state. Pick where cohorts live (`DataDir`, or an `afero.Fs`
in `FS`), then the engine-wide defaults your callers inherit (time zone,
weight, multiplicity correction). Build one instance per filesystem
boundary and share it across goroutines.
[pulse.New & Options](options.md), [Custom Filesystems](custom-fs.md).

## 2. Register extensions

Operators, regression models, overlays, expression functions, lookup
tables, skills and examples of your own go in `Options.Extensions`.
Predict, the manifest, MCP and the runtime treat them exactly like
built-ins, and `pulse.New` refuses a malformed one.
[Writing Operators (extend)](extend.md),
[Label, Range & Lookup Tables](tables.md).

## 3. Choose a feature profile

A feature profile is the instance's allowlist of operators and
capabilities. A refused feature disappears from the manifest, the MCP
tool list and the skills, and a request that names it fails with a
coded error. Use one when callers (or agents) should see a subset.
[Feature Profiles](feature-profiles.md).

## 4. Set limits

`Options.Limits` bounds runaway requests: groups, crosstab cells, the
memory estimate, fan-out, the request timeout. Predict reports a request
that would exceed one before it runs.
[Tuning Limits](tuning-limits.md).

## 5. Wire observability

`Options.Logger`, `Options.Hooks` and `Options.Metrics` are off by
default and independent; OpenTelemetry and Prometheus adapters ship in
the module. [Observability](observability.md).

## 6. Shape responses

`Options.DefaultReturn` (or a request's `return` block) chooses which
parts of a response are computed and sent, so a caller that needs only
the rows does not pay for the rest.
[Response Shaping](response-shaping.md).

## 7. Serve MCP

To expose the instance to an AI agent, run the ready-made server
(`mcpserve`) on stdio, or mount Pulse's tools on a go-sdk server you
already run (`mcp/gosdk`). The tools follow the instance: its
extensions, profile and limits.
[Serving MCP](mcp-serving.md).
