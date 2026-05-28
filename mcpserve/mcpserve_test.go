package mcpserve_test

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
)

// TestServe_InitializeRoundTrip drives the public facade end-to-end: it sends
// an MCP initialize request over an in-memory transport and asserts the
// server identifies itself. This proves Serve wires internal/mcp through to a
// working JSON-RPC loop.
func TestServe_InitializeRoundTrip(t *testing.T) {
	p, err := pulse.New(pulse.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() { serveErr <- mcpserve.Serve(ctx, p, mcpserve.Options{BindOnOpen: true}, inR, outW) }()

	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2024-11-05","capabilities":{},` +
		`"clientInfo":{"name":"mcpserve-test","version":"0"}}}` + "\n"
	go func() { _, _ = inW.Write([]byte(initReq)) }()

	type readResult struct {
		line string
		err  error
	}
	lines := make(chan readResult, 1)
	go func() {
		line, err := bufio.NewReader(outR).ReadString('\n')
		lines <- readResult{line, err}
	}()

	select {
	case got := <-lines:
		if got.err != nil {
			t.Fatalf("read initialize response: %v", got.err)
		}
		if !strings.Contains(got.line, `"result"`) {
			t.Fatalf("response is not a result: %s", got.line)
		}
		if !strings.Contains(got.line, "pulse") {
			t.Fatalf("response does not identify the pulse server: %s", got.line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initialize response")
	}

	cancel()
	_ = inW.Close()
	_ = outW.Close()
}
