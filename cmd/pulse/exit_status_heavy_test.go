//go:build heavy

package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	pcli "github.com/frankbardon/pulse/internal/cli"
)

// TestExitStatus_EveryLeaf runs each row: a bad flag exits 2 with no
// envelope; a fatal error exits 1 in text mode (stderr) and under --json
// (an envelope with errors, nothing added to stderr); success exits 0,
// warnings included; a warning --strict escalates exits 1.
func TestExitStatus_EveryLeaf(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	f := newExitFixture(t)
	for _, c := range exitCases(f) {
		cmd := leafCommand(t, c.leaf)
		jsonFlag := hasFlag(cmd, "json")
		t.Run(c.leaf, func(t *testing.T) {
			if s, out, _ := runExit(t, leafArgs(c.leaf, false, []string{"--definitely-not-a-flag"})...); s != pcli.ExitUsage || isEnvelope(out) {
				t.Errorf("bad flag: exit %d (want 2), stdout envelope=%v", s, isEnvelope(out))
			}
			if c.fatal != nil {
				if s, _, errOut := runExit(t, leafArgs(c.leaf, false, c.fatal)...); s != pcli.ExitFatal || !strings.Contains(errOut, "error: ") {
					t.Errorf("fatal (text): exit %d (want 1), stderr %q", s, errOut)
				}
				if jsonFlag {
					s, out, errOut := runExit(t, leafArgs(c.leaf, true, c.fatal)...)
					env := decodeExitEnvelope(t, "fatal (--json)", out)
					if s != pcli.ExitFatal || env == nil || len(env.Errors) == 0 {
						t.Errorf("fatal (--json): exit %d (want 1), envelope %s", s, out)
					}
					if strings.Contains(errOut, "error: ") {
						t.Errorf("fatal (--json): main added to stderr: %q", errOut)
					}
				}
			}
			s, out, errOut := runExit(t, leafArgs(c.leaf, jsonFlag, c.ok)...)
			if s != pcli.ExitOK {
				t.Fatalf("ok: exit %d (want 0)\nstdout %s\nstderr %s", s, out, errOut)
			}
			if jsonFlag {
				if env := decodeExitEnvelope(t, "ok", out); env != nil && len(env.Errors) != 0 {
					t.Errorf("ok: exit 0 with errors in the envelope: %s", out)
				}
			}
			if c.warn != nil {
				s, out, _ := runExit(t, leafArgs(c.leaf, true, c.warn)...)
				env := decodeExitEnvelope(t, "warn", out)
				if s != pcli.ExitOK || env == nil || len(env.Warnings) == 0 || len(env.Errors) != 0 {
					t.Errorf("warning-only: exit %d (want 0), envelope %s", s, out)
				}
			}
			if c.strict != nil {
				s, out, _ := runExit(t, leafArgs(c.leaf, true, c.strict)...)
				if env := decodeExitEnvelope(t, "strict", out); s != pcli.ExitFatal || env == nil || len(env.Errors) == 0 {
					t.Errorf("--strict escalation: exit %d (want 1), envelope %s", s, out)
				}
			}
		})
	}
}

// TestExitStatus_Binary is the acceptance case run as a real process:
// `pulse api process -r bad.json --json; echo $?` prints 1, stdout is
// the envelope alone and stderr stays empty. Text mode exits 1 with the
// error on stderr; a warning-only predict exits 0; a bad flag exits 2.
func TestExitStatus_Binary(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	f := newExitFixture(t)
	status := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := cliProcess(t, args...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return code, out.String(), errb.String()
	}

	code, out, errOut := status("api", "process", "-r", f.p("bad.json"), "--json")
	if code != 1 {
		t.Fatalf("pulse api process -r bad.json --json; echo $? = %d, want 1\n%s", code, out)
	}
	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Errors) != 1 || env.Errors[0].Code != "SERVICE_RESOURCE" {
		t.Fatalf("stdout is not the fatal envelope (%v):\n%s", err, out)
	}
	if errOut != "" {
		t.Fatalf("--json fatal added to stderr: %q", errOut)
	}
	if code, _, errOut := status("api", "process", "-r", f.p("bad.json")); code != 1 || !strings.Contains(errOut, "SERVICE_RESOURCE") {
		t.Fatalf("text mode: exit %d (want 1), stderr %q", code, errOut)
	}
	if code, out, _ := status("api", "predict-facet", "-r", f.p("facet_warn.json"), "--json"); code != 0 || !strings.Contains(out, `"warnings": [`) {
		t.Fatalf("warning-only: exit %d (want 0)\n%s", code, out)
	}
	if code, _, _ := status("api", "process", "--definitely-not-a-flag"); code != 2 {
		t.Fatalf("bad flag: exit %d (want 2)", code)
	}
}
