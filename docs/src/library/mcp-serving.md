# Serving MCP (`mcpserve`, `mcp/gosdk`)

**Audience:** embedders exposing their own `*pulse.Pulse` (extensions,
profile, limits) to an AI agent.

The stock `pulse mcp` binary cannot load your Go extensions. Serve the
instance from your own process instead, with one of two packages:

| Package | Use when |
|---|---|
| [`mcpserve`](https://pkg.go.dev/github.com/frankbardon/pulse/mcpserve) | You want a ready server: Pulse builds it and serves stdio (`ServeStdio`) or any reader / writer pair (`Serve`). |
| [`mcp/gosdk`](https://pkg.go.dev/github.com/frankbardon/pulse/mcp/gosdk) | You already run a [go-sdk](https://github.com/modelcontextprotocol/go-sdk) server, or need another transport (streamable HTTP). `Register` mounts Pulse's tools, resources and prompts on it; you own creation and serving. |

Both mount what the instance offers: a feature profile hides tools,
prompts and the cohort listing exactly as under `pulse mcp`.

## Stdio, the ready server

```go compile
package main

import (
	"log"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
)

func main() {
	opts := mcpserve.Options{BindOnOpen: true}
	// NewPulse applies opts.FeatureProfileFile (or PULSE_FEATURE_PROFILE)
	// before pulse.New; Options.Extensions go in the first argument.
	p, err := mcpserve.NewPulse(pulse.Options{DataDir: "/var/data/pulse"}, opts)
	if err != nil {
		log.Fatal(err)
	}
	if err := mcpserve.ServeStdio(p, opts); err != nil {
		log.Fatal(err)
	}
}
```

`mcpserve.Describe(p, opts)` returns what the server will report
(profile, return preset, limits), for a startup log line.

## Streamable HTTP, your own server

```go compile
package main

import (
	"log"
	"net/http"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	p, err := pulse.New(pulse.Options{DataDir: "/var/data/pulse"})
	if err != nil {
		log.Fatal(err)
	}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "my-host", Version: pulse.Version()}, nil)
	// One server shared by every HTTP session: leave BindOnInspect off,
	// since a bound schema would leak across sessions.
	if err := gosdk.Register(srv, p, gosdk.Config{}); err != nil {
		log.Fatal(err)
	}
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv }, nil)
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", handler))
}
```

A per-session server (build and `Register` one inside the factory) can
turn `BindOnInspect` on.

## Where the detail lives

- [MCP Integration](../mcp/index.md): the tool, resource and prompt
  surface, bind-on-inspect, response presets, transport caveats.
- [Feature Profiles → The MCP server](feature-profiles.md#the-mcp-server).
- [Adding an MCP Tool](../internals/adding-mcp-tool.md) (contributors).
- GoDoc: [`mcpserve`](https://pkg.go.dev/github.com/frankbardon/pulse/mcpserve),
  [`gosdk`](https://pkg.go.dev/github.com/frankbardon/pulse/mcp/gosdk).
