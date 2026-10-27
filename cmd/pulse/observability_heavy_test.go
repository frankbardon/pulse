//go:build heavy

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
