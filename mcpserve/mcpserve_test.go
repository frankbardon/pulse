package mcpserve_test

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/spf13/afero"
)

// TestServe_InitializeRoundTrip drives the public facade end-to-end: it sends
// an MCP initialize request over an in-memory transport and asserts the
// server identifies itself. This proves Serve wires the mcp/gosdk adapter through to a
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

// countingFs counts filesystem Open calls so the test can prove whether the
// server walked the data root at startup. afero.Walk reaches a directory's
// entries through Open, and the cohort-resource scan is the only thing
// registration touches the filesystem for.
type countingFs struct {
	afero.Fs
	mu    sync.Mutex
	opens int
}

func (c *countingFs) Open(name string) (afero.File, error) {
	c.mu.Lock()
	c.opens++
	c.mu.Unlock()
	return c.Fs.Open(name)
}

func (c *countingFs) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens
}

func (c *countingFs) reset() {
	c.mu.Lock()
	c.opens = 0
	c.mu.Unlock()
}

// TestServe_DisableCohortScanSkipsTheStartupWalk asserts the embedder-facing
// half of the knob: Options.DisableCohortScan reaches the gosdk Config, so a
// host serving its own Pulse pays no directory walk. The enabled arm is in the
// same test so a scan that stopped running could not make the disabled arm
// pass vacuously.
func TestServe_DisableCohortScanSkipsTheStartupWalk(t *testing.T) {
	for _, tc := range []struct {
		name      string
		opts      mcpserve.Options
		wantOpens bool
	}{
		{name: "scan on (default)", opts: mcpserve.Options{}, wantOpens: true},
		{name: "scan disabled", opts: mcpserve.Options{DisableCohortScan: true}, wantOpens: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := &countingFs{Fs: afero.NewMemMapFs()}
			p, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}

			inR, inW := io.Pipe()
			outR, outW := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			fsys.reset()
			go func() { _ = mcpserve.Serve(ctx, p, tc.opts, inR, outW) }()

			initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
				`{"protocolVersion":"2024-11-05","capabilities":{},` +
				`"clientInfo":{"name":"mcpserve-test","version":"0"}}}` + "\n"
			go func() { _, _ = inW.Write([]byte(initReq)) }()

			lines := make(chan string, 1)
			go func() {
				line, _ := bufio.NewReader(outR).ReadString('\n')
				lines <- line
			}()
			select {
			case line := <-lines:
				if !strings.Contains(line, `"result"`) {
					t.Fatalf("response is not a result: %s", line)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for initialize response")
			}

			if got := fsys.count() > 0; got != tc.wantOpens {
				t.Errorf("filesystem walked = %v (%d opens), want %v", got, fsys.count(), tc.wantOpens)
			}

			cancel()
			_ = inW.Close()
			_ = outW.Close()
		})
	}
}
