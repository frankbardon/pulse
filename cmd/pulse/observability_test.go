package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// cliHelperEnv re-enters this test binary as the real `pulse` binary
// (TestCLIHelperProcess), so a test sees the process's actual stdout and
// stderr file descriptors — the only way to prove logs never reach
// stdout, where `--json` output and the MCP JSON-RPC transport live.
const cliHelperEnv = "GO_WANT_PULSE_CLI_HELPER"

// TestCLIHelperProcess is not a test: it runs buildApp on the arguments
// after "--" and exits, when re-executed by runCLIProcess.
func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv(cliHelperEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if err := buildApp().Run(context.Background(), append([]string{"pulse"}, args...)); err != nil {
		_, _ = os.Stderr.WriteString("error: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(0)
}

func cliProcess(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), cliHelperEnv+"=1", "PULSE_FEATURE_PROFILE=")
	return cmd
}

// runCLIProcess runs `pulse args...` as a subprocess, returning stdout
// and stderr separately.
func runCLIProcess(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := cliProcess(t, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("pulse %v: %v\nstderr: %s", args, err, errb.String())
	}
	return out.String(), errb.String()
}

// TestLogFlags_AcceptedOnEveryLeaf: --log-level / --log-format are root
// persistent flags, so every actionable leaf parses them after its own
// name (urfave/cli v3 flags persist unless Local) and lists them in help.
func TestLogFlags_AcceptedOnEveryLeaf(t *testing.T) {
	leaves := cliLeaves()
	if len(leaves) < 20 {
		t.Fatalf("cliLeaves() = %d leaves; the walk is broken", len(leaves))
	}
	for _, leaf := range leaves {
		t.Run(leaf, func(t *testing.T) {
			app := buildApp()
			var buf bytes.Buffer
			setWriterRecursive(app, &buf)
			app.ErrWriter = io.Discard
			args := append(append([]string{"pulse"}, strings.Fields(leaf)[1:]...),
				"--log-level", "info", "--log-format", "json", "--help")
			if err := app.Run(context.Background(), args); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if !strings.Contains(buf.String(), "--log-level") || !strings.Contains(buf.String(), "--log-format") {
				t.Errorf("%s --help does not list the log flags:\n%s", leaf, buf.String())
			}
		})
	}
}

// TestLogFlags_BadValueIsRefused: an unknown level fails before any leaf
// runs.
func TestLogFlags_BadValueIsRefused(t *testing.T) {
	if _, err := runApp(t, "version", "--log-level", "loud"); err == nil || !strings.Contains(err.Error(), "--log-level loud") {
		t.Fatalf("--log-level loud: err = %v", err)
	}
}

// TestLogFlags_StderrOnly: `pulse <leaf> --log-level debug` writes its
// records to stderr and leaves stdout byte-identical to the flagless run,
// `--json` envelopes included.
func TestLogFlags_StderrOnly(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)
	reqPath := filepath.Join(dir, "request.json")
	req := `{"cohort": {"filename": "` + pulsePath + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age"}]}`
	if err := os.WriteFile(reqPath, []byte(req), 0o644); err != nil {
		t.Fatal(err)
	}

	leaves := [][]string{
		{"api", "process", "--request", reqPath, "--json"},
		{"api", "process", "--request", reqPath},
		{"api", "predict", "--request", reqPath, "--json"},
		{"cohort", "inspect", pulsePath, "--json"},
	}
	for _, args := range leaves {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			plain, plainErr := runCLIProcess(t, args...)
			if plainErr != "" {
				t.Errorf("flagless run wrote stderr: %q", plainErr)
			}
			for _, format := range []string{"text", "json"} {
				logged, stderr := runCLIProcess(t, append(args, "--log-level", "debug", "--log-format", format)...)
				if logged != plain {
					t.Errorf("--log-format %s: stdout changed\n got  %q\n want %q", format, logged, plain)
				}
				if !strings.Contains(stderr, "op=new") && !strings.Contains(stderr, `"op":"new"`) {
					t.Errorf("--log-format %s: no log records on stderr: %q", format, stderr)
				}
			}
		})
	}
}

var metricsAddrRe = regexp.MustCompile(`metrics: ([0-9.:]+)\)`)

// TestMCP_ObservabilityFlags: `pulse mcp --log-level info --log-format
// json --metrics-addr 127.0.0.1:0` keeps stdout pure JSON-RPC, logs to
// stderr, names the effective return / log level / bound exporter on the
// startup line, and serves /metrics while it runs.
func TestMCP_ObservabilityFlags(t *testing.T) {
	dataDir := t.TempDir()
	createTestPulseFile(t, dataDir)

	cmd := cliProcess(t, "mcp", "--data-dir", dataDir,
		"--log-level", "info", "--log-format", "json", "--metrics-addr", "127.0.0.1:0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	// Stderr: wait for the startup line, collect the rest.
	stderrLines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stderrPipe)
		for sc.Scan() {
			stderrLines <- sc.Text()
		}
		close(stderrLines)
	}()
	var stderr []string
	var startup string
	deadline := time.After(10 * time.Second)
	for startup == "" {
		select {
		case l, ok := <-stderrLines:
			if !ok {
				t.Fatalf("stderr closed before the startup line: %v", stderr)
			}
			stderr = append(stderr, l)
			if strings.HasPrefix(l, "pulse mcp: serving over stdio") {
				startup = l
			}
		case <-deadline:
			t.Fatalf("no startup line within 10s: %v", stderr)
		}
	}
	if !strings.Contains(startup, ", return: standard") || !strings.Contains(startup, ", log-level: info") {
		t.Errorf("startup line = %q", startup)
	}
	m := metricsAddrRe.FindStringSubmatch(startup)
	if m == nil || strings.HasSuffix(m[1], ":0") {
		t.Fatalf("startup line names no bound metrics address: %q", startup)
	}

	// A JSON-RPC session that runs an operation.
	stdoutLines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stdoutPipe)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			stdoutLines <- sc.Text()
		}
		close(stdoutLines)
	}()
	send := func(msg string) {
		if _, err := io.WriteString(stdin, msg+"\n"); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	}
	var stdout []string
	await := func(id string) {
		t.Helper()
		timeout := time.After(10 * time.Second)
		for {
			select {
			case l, ok := <-stdoutLines:
				if !ok {
					t.Fatalf("stdout closed awaiting id %s: %v", id, stdout)
				}
				stdout = append(stdout, l)
				if strings.Contains(l, `"id":`+id+`,`) || strings.Contains(l, `"id":`+id+`}`) {
					return
				}
			case <-timeout:
				t.Fatalf("no response to id %s: %v", id, stdout)
			}
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	await("1")
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pulse_inspect","arguments":{"path":"test.pulse"}}}`)
	await("2")

	// /metrics is live while the server runs.
	resp, err := http.Get("http://" + m[1] + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pulse_operations_total") {
		t.Errorf("GET /metrics = %d:\n%s", resp.StatusCode, body)
	}

	_ = stdin.Close()
	for l := range stdoutLines {
		stdout = append(stdout, l)
	}
	for l := range stderrLines {
		stderr = append(stderr, l)
	}
	_ = cmd.Wait()

	for _, l := range stdout {
		var msg map[string]any
		if err := json.Unmarshal([]byte(l), &msg); err != nil || msg["jsonrpc"] != "2.0" {
			t.Errorf("stdout carries a non-JSON-RPC line: %q", l)
		}
	}
	var logged bool
	for _, l := range stderr {
		if strings.Contains(l, `"level":"INFO"`) {
			logged = true
		}
	}
	if !logged {
		t.Errorf("--log-level info wrote no JSON records to stderr: %v", stderr)
	}
}

// TestMCP_NoMetricsAddrOpensNoPort: without --metrics-addr the startup
// line names no exporter and no log level.
func TestMCP_NoMetricsAddrOpensNoPort(t *testing.T) {
	_, stderr := runCLIProcess(t, "mcp", "--data-dir", t.TempDir())
	if !strings.Contains(stderr, ", return: standard)") {
		t.Errorf("startup line = %q, want it to end at return: standard", stderr)
	}
	if strings.Contains(stderr, "metrics:") || strings.Contains(stderr, "log-level:") {
		t.Errorf("flagless startup line names observability settings: %q", stderr)
	}
}
