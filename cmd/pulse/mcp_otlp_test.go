package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// rawOTLPReceiver is an in-process OTLP/HTTP collector that keeps each
// POST body by path. The CLI tests search the uncompressed protobuf for
// names (span and metric names are plain strings on the wire), so this
// package needs no OTel import.
type rawOTLPReceiver struct {
	srv *httptest.Server

	mu     sync.Mutex
	bodies map[string][]byte
}

func newRawOTLPReceiver(t *testing.T) *rawOTLPReceiver {
	t.Helper()
	r := &rawOTLPReceiver{bodies: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if req.Header.Get("Content-Type") != "application/x-protobuf" {
			http.Error(w, "want application/x-protobuf", http.StatusUnsupportedMediaType)
			return
		}
		r.mu.Lock()
		r.bodies[req.URL.Path] = append(r.bodies[req.URL.Path], body...)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rawOTLPReceiver) has(path, s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Contains(r.bodies[path], []byte(s))
}

// mcpProc is one `pulse mcp` subprocess with its JSON-RPC stdin and
// line-scanned stdout / stderr.
type mcpProc struct {
	t       *testing.T
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	outCh   chan string
	errCh   chan string
	stdout  []string
	stderr  []string
	startup string
}

// startMCP starts `pulse mcp args...` with extra env appended and waits
// for the startup line.
func startMCP(t *testing.T, env []string, args ...string) *mcpProc {
	t.Helper()
	cmd := cliProcess(t, append([]string{"mcp"}, args...)...)
	cmd.Env = append(cmd.Env, env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	m := &mcpProc{t: t, cmd: cmd, stdin: stdin, outCh: make(chan string, 64), errCh: make(chan string, 64)}
	scan := func(r io.Reader, ch chan string) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			ch <- sc.Text()
		}
		close(ch)
	}
	go scan(outPipe, m.outCh)
	go scan(errPipe, m.errCh)
	deadline := time.After(10 * time.Second)
	for m.startup == "" {
		select {
		case l, ok := <-m.errCh:
			if !ok {
				t.Fatalf("stderr closed before the startup line: %v", m.stderr)
			}
			m.stderr = append(m.stderr, l)
			if strings.HasPrefix(l, "pulse mcp: serving over stdio") {
				m.startup = l
			}
		case <-deadline:
			t.Fatalf("no startup line within 10s: %v", m.stderr)
		}
	}
	return m
}

func (m *mcpProc) call(id, msg string) {
	m.t.Helper()
	if _, err := io.WriteString(m.stdin, msg+"\n"); err != nil {
		m.t.Fatalf("write stdin: %v", err)
	}
	if id == "" {
		return
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case l, ok := <-m.outCh:
			if !ok {
				m.t.Fatalf("stdout closed awaiting id %s: %v", id, m.stdout)
			}
			m.stdout = append(m.stdout, l)
			if strings.Contains(l, `"id":`+id+`,`) || strings.Contains(l, `"id":`+id+`}`) {
				return
			}
		case <-timeout:
			m.t.Fatalf("no response to id %s: %v", id, m.stdout)
		}
	}
}

// inspectSession initializes the session and runs one pulse_inspect.
func (m *mcpProc) inspectSession() {
	m.call("1", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	m.call("", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	m.call("2", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pulse_inspect","arguments":{"path":"test.pulse"}}}`)
}

// finish closes stdin (the server exits, flushing its exporters) and
// drains both streams; stdout must be pure JSON-RPC.
func (m *mcpProc) finish() {
	m.t.Helper()
	_ = m.stdin.Close()
	for l := range m.outCh {
		m.stdout = append(m.stdout, l)
	}
	for l := range m.errCh {
		m.stderr = append(m.stderr, l)
	}
	_ = m.cmd.Wait()
	for _, l := range m.stdout {
		var msg map[string]any
		if err := json.Unmarshal([]byte(l), &msg); err != nil || msg["jsonrpc"] != "2.0" {
			m.t.Errorf("stdout carries a non-JSON-RPC line: %q", l)
		}
	}
}

// metricsAddrBeforeOTLPRe reads the bound --metrics-addr when the OTLP
// signals follow it on the startup line.
var metricsAddrBeforeOTLPRe = regexp.MustCompile(`metrics: ([0-9.:]+), otlp:`)

// TestMCP_OTLPExportWithMetricsAddr: with OTEL_EXPORTER_OTLP_ENDPOINT
// naming an OTLP/HTTP receiver AND --metrics-addr, one MCP call reaches
// the receiver as a pulse.inspect span plus Pulse metrics under the
// OTEL_SERVICE_NAME resource, the /metrics scrape counts the same
// operation (the fan-out feeds both), and stdout stays JSON-RPC.
func TestMCP_OTLPExportWithMetricsAddr(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	createTestPulseFile(t, dataDir)
	rcv := newRawOTLPReceiver(t)

	m := startMCP(t, []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + rcv.srv.URL,
		"OTEL_SERVICE_NAME=pulse-otlp-e2e",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
	}, "--data-dir", dataDir, "--metrics-addr", "127.0.0.1:0")
	if !strings.Contains(m.startup, ", otlp: traces+metrics)") {
		t.Errorf("startup line = %q, want the OTLP signals named", m.startup)
	}
	if strings.Contains(m.startup, rcv.srv.URL) {
		t.Errorf("startup line names the endpoint: %q", m.startup)
	}
	addr := metricsAddrBeforeOTLPRe.FindStringSubmatch(m.startup)
	if addr == nil {
		t.Fatalf("startup line names no metrics address: %q", m.startup)
	}
	m.inspectSession()

	resp, err := http.Get("http://" + addr[1] + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `pulse_operations_total{op="inspect",code="ok"`) {
		t.Errorf("/metrics does not count the inspect:\n%s", body)
	}

	m.finish()
	for _, want := range []string{"pulse.inspect", "pulse-otlp-e2e"} {
		if !rcv.has("/v1/traces", want) {
			t.Errorf("OTLP traces carry no %q", want)
		}
	}
	for _, want := range []string{"pulse_operations_total", "inspect", "pulse-otlp-e2e"} {
		if !rcv.has("/v1/metrics", want) {
			t.Errorf("OTLP metrics carry no %q", want)
		}
	}
}

// TestMCP_OTLPRefusesGRPC: a gRPC protocol fails startup with CLI_INPUT
// naming the variable and value — no server, exit status 1.
func TestMCP_OTLPRefusesGRPC(t *testing.T) {
	t.Parallel()

	cmd := cliProcess(t, "mcp", "--data-dir", t.TempDir())
	cmd.Env = append(cmd.Env, "OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1", "OTEL_EXPORTER_OTLP_PROTOCOL=grpc")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 {
		t.Fatalf("exit = %v, want status 1; stderr %q", err, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	s := errb.String()
	if !strings.Contains(s, "CLI_INPUT") || !strings.Contains(s, "OTEL_EXPORTER_OTLP_PROTOCOL=grpc") || !strings.Contains(s, "HTTP only") {
		t.Errorf("stderr = %q", s)
	}
	if strings.Contains(s, "serving over stdio") {
		t.Errorf("a refused config started serving: %q", s)
	}
}

// TestMCP_OTLPUnreachableEndpointKeepsServing: a collector that refuses
// connections never stops the server — the call is answered, and the
// failed export surfaces as a warn record on the --log-level logger.
func TestMCP_OTLPUnreachableEndpointKeepsServing(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	_ = ln.Close()

	dataDir := t.TempDir()
	createTestPulseFile(t, dataDir)
	m := startMCP(t, []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + dead,
		"OTEL_EXPORTER_OTLP_TIMEOUT=300", // ms: bounds each export's retries
	}, "--data-dir", dataDir, "--log-level", "warn", "--log-format", "json")
	m.inspectSession()
	var answered bool
	for _, l := range m.stdout {
		if strings.Contains(l, `"id":2`) && strings.Contains(l, `"result"`) {
			answered = true
		}
	}
	if !answered {
		t.Errorf("pulse_inspect unanswered with an unreachable collector: %v", m.stdout)
	}
	m.finish()
	var warned bool
	for _, l := range m.stderr {
		if strings.Contains(l, `"level":"WARN"`) && strings.Contains(l, "otlp export failed") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no OTLP export warning on the logger: %v", m.stderr)
	}
}
