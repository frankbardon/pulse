package mcpserve_test

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
)

// Serve an engine's full MCP surface (tools, prompts, resources) over
// stdin/stdout, the transport an MCP client uses when it spawns the
// server. Extensions on the engine are served like built-ins. Serve
// blocks until ctx is cancelled or the client disconnects, so this
// example compiles but does not run.
func ExampleServe() {
	p, err := pulse.New(pulse.Options{DataDir: "/srv/cohorts"})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := mcpserve.Serve(ctx, p, mcpserve.Options{BindOnOpen: true}, os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
