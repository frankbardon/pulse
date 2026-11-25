package mcpserve_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// initializeProtocolVersion serves one MCP initialize exchange asking for
// requested and returns the protocolVersion the server answered.
func initializeProtocolVersion(t *testing.T, requested string) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); _ = inW.Close(); _ = outW.Close() }()

	go func() { _ = mcpserve.Serve(ctx, p, mcpserve.Options{}, inR, outW) }()
	go func() {
		_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"protocolVersion":"` + requested + `","capabilities":{},` +
			`"clientInfo":{"name":"mcpserve-protocol-test","version":"0"}}}` + "\n"))
	}()

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(outR).ReadString('\n')
		lines <- line
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initialize response")
	}
	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal initialize response %q: %v", line, err)
	}
	return resp.Result.ProtocolVersion
}

// TestServe_NegotiatesEverySDKRevision holds the negotiation docs/src/mcp/
// protocol.md states: the server narrows nothing, so every revision the
// go-sdk supports before 2026-07-28 is echoed by initialize, and any other
// value (2026-07-28 itself, an unknown or future date) is answered with
// 2025-11-25 rather than refused.
func TestServe_NegotiatesEverySDKRevision(t *testing.T) {
	t.Parallel()

	for _, v := range mcpsdk.SupportedProtocolVersions() {
		want := v
		if v >= "2026-07-28" {
			want = "2025-11-25"
		}
		if got := initializeProtocolVersion(t, v); got != want {
			t.Errorf("initialize %s answered %q, want %q", v, got, want)
		}
	}
	if got := initializeProtocolVersion(t, "2099-01-01"); got != "2025-11-25" {
		t.Errorf("initialize with an unknown revision answered %q, want 2025-11-25", got)
	}
}
