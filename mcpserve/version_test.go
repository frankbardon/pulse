package mcpserve_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/mcpserve"
)

// initializeServerVersion serves one MCP initialize exchange and returns the
// serverInfo.version the server advertised.
func initializeServerVersion(t *testing.T, opts mcpserve.Options) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); _ = inW.Close(); _ = outW.Close() }()

	go func() { _ = mcpserve.Serve(ctx, p, opts, inR, outW) }()
	go func() {
		_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"protocolVersion":"2024-11-05","capabilities":{},` +
			`"clientInfo":{"name":"mcpserve-version-test","version":"0"}}}` + "\n"))
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
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal initialize response %q: %v", line, err)
	}
	return resp.Result.ServerInfo.Version
}

// TestServe_EmptyVersionAdvertisesBuildVersion: with Options.Version unset the
// server identifies itself with pulse.Version(), not a hardcoded literal.
func TestServe_EmptyVersionAdvertisesBuildVersion(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	if got := initializeServerVersion(t, mcpserve.Options{}); got != "v0.0.0-test" {
		t.Fatalf("serverInfo.version = %q, want pulse.Version() = %q", got, pulse.Version())
	}
}

// TestServe_ExplicitVersionWins: a caller-supplied identity is advertised as-is.
func TestServe_ExplicitVersionWins(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	if got := initializeServerVersion(t, mcpserve.Options{Version: "9.9.9"}); got != "9.9.9" {
		t.Fatalf("serverInfo.version = %q, want 9.9.9", got)
	}
}
