package main

// EXIT STATUS FOLLOWS THE ENVELOPE (#303).
//
// Scripts branch on the exit status: 0 = success (warnings and
// advisories allowed), 1 = a fatal error (in the --json envelope's
// `errors`, including one --strict escalated; on stderr in text mode),
// 2 = a usage error refused before any library call (no envelope). No
// per-domain codes. `pulse mcp` is a long-running server and is exempt.
//
// The table is checked against the binary's command tree (cliLeaves), in
// both directions, so a new leaf cannot land without a row and a removed
// leaf cannot leave a stale one.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	pcli "github.com/frankbardon/pulse/internal/cli"
	cli "github.com/urfave/cli/v3"
)

// wantEnvelopeExit fails t unless err is the envelope-fatal exit: status
// 1 with nothing for main to print (the --json envelope carries it).
func wantEnvelopeExit(t *testing.T, err error) {
	t.Helper()
	if status, printErr := pcli.ExitStatus(err); status != pcli.ExitFatal || printErr {
		t.Fatalf("err = %v: exit (%d, print=%v), want the envelope-fatal exit (1, print=false)", err, status, printErr)
	}
}

// exitExempt are the leaves the exit-status contract does not cover.
var exitExempt = map[string]string{
	"pulse mcp": "a long-running stdio server; its status is the transport's, not an envelope's",
}

// exitCase is one leaf's row. Args follow the leaf path; the harness adds
// --json (right after the path) for a leaf that has the flag.
type exitCase struct {
	leaf string
	// fatal is an invocation that fails in the leaf (exit 1). nil only
	// with noFatal saying why the leaf has no reachable fatal path.
	fatal   []string
	noFatal string
	// ok succeeds (exit 0, an envelope with no errors under --json).
	ok []string
	// warn succeeds with warnings on the envelope (exit 0); strict is
	// the same input under --strict, the warning escalated (exit 1).
	warn, strict []string
}

type exitFixture struct {
	dir, csv, cohort, cohort2, missing string
}

func (f exitFixture) p(name string) string { return filepath.Join(f.dir, name) }

func writeExitFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newExitFixture builds every input the table needs in one data dir.
func newExitFixture(t *testing.T) exitFixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Setenv("PULSE_IMPORTS_DIR", "imports")
	t.Setenv("PULSE_IMPORT_TTL", "7d")
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	f := exitFixture{dir: dir, missing: filepath.Join(dir, "missing.pulse")}
	f.cohort = createTestPulseFile(t, dir) // also writes source.csv
	f.csv = f.p("source.csv")
	mustRun := func(args ...string) {
		t.Helper()
		if out, err := runApp(t, args...); err != nil {
			t.Fatalf("fixture %v: %v\n%s", args, err, out)
		}
	}
	f.cohort2 = f.p("second.pulse")
	mustRun("import", "csv", "-i", f.csv, "-o", f.cohort2)
	for _, n := range []string{"a", "b", "c"} {
		mustRun("import", "csv", "-i", f.csv, "-o", f.p(n+".pulse"))
	}
	mustRun("index", "build", f.cohort, "--key", "age")
	for _, ext := range []string{"parquet", "arrow", "xlsx", "sav"} {
		format := map[string]string{"parquet": "parquet", "arrow": "arrow", "xlsx": "excel", "sav": "spss"}[ext]
		mustRun("export", format, "--input", f.cohort2, "--output", f.p("src."+ext))
	}
	mustRun("export", "transfer", "--input", f.cohort2, "--output", f.p("src.pulse.zst"))
	writeExitFile(t, f.p("src.csv"), "age,name\n10,alice\n20,bob\n")
	writeExitFile(t, f.p("src.tsv"), "age\tname\n10\talice\n20\tbob\n")
	writeExitFile(t, f.p("src.ndjson"), "{\"age\":10,\"name\":\"alice\"}\n{\"age\":20,\"name\":\"bob\"}\n")
	writeExitFile(t, f.p("src.json"), `[{"age":10,"name":"alice"},{"age":20,"name":"bob"}]`)
	writeExitFile(t, f.p("auto.csv"), "id,name\n1,a\n2,b\n")
	writeExitFile(t, f.p("sets.csv"), "id,picks\n1,a|b\n2,b\n")
	writeExitFile(t, f.p("sets.schema.json"), `[{"name":"id","type":"u8"},{"name":"picks","type":"set_u8"}]`)
	// A blank cell in the non-nullable set column drops row 2: a partial
	// import, PULSE_IMPORT_ROWS_DROPPED (#304).
	writeExitFile(t, f.p("partial.csv"), "id,picks\n1,a|b\n2,\n3,b\n")
	mustRun("import", "csv", "-i", f.p("sets.csv"), "-o", f.p("sets.pulse"), "--schema", f.p("sets.schema.json"))

	c := `{"filename":"` + f.cohort + `"}`
	bad := `{"filename":"` + f.missing + `"}`
	agg := `"aggregations":[{"type":"AGG_COUNT","field":"age"}]`
	writeExitFile(t, f.p("req.json"), `{"cohort":`+c+`,`+agg+`}`)
	writeExitFile(t, f.p("bad.json"), `{"cohort":`+bad+`,`+agg+`}`)
	writeExitFile(t, f.p("compose.json"), `{"requests":[{"cohort":`+c+`,`+agg+`}]}`)
	// Run-time warnings ride the envelope's top-level warnings: a target
	// encode's PULSE_FEAT_TARGET_LEAKAGE_RISK (process, compose; --strict
	// refuses it), a `return` include matching nothing (a chain stage).
	leak := `"features":[{"type":"FEAT_TARGET_ENCODE","field":"name","params":{"target":"age"}}],` + agg
	writeExitFile(t, f.p("req_warn.json"), `{"cohort":`+c+`,`+leak+`}`)
	writeExitFile(t, f.p("compose_warn.json"), `{"requests":[{"cohort":`+c+`,`+leak+`}]}`)
	writeExitFile(t, f.p("chain_warn.json"), `{"cohort":`+c+`,"stages":[{"request":{"return":{"include":["components.aggregations[*].operator.nope"]},`+agg+`}}]}`)
	// An unknown key is refused (PULSE_REQUEST_UNKNOWN_FIELD) by every
	// compose leaf, predict included, which reads no record.
	writeExitFile(t, f.p("compose_bad.json"), `{"requests":[{"cohort":`+bad+`,`+agg+`}],"bogus":1}`)
	writeExitFile(t, f.p("chain.json"), `{"cohort":`+c+`,"stages":[{"request":{`+agg+`}}]}`)
	writeExitFile(t, f.p("chain_bad.json"), `{"cohort":`+bad+`,"stages":[{"request":{`+agg+`}}]}`)
	writeExitFile(t, f.p("facet.json"), `{"cohort":`+c+`,"fields":["name"]}`)
	// discrete_top_k under 5 is a facet predict warning.
	writeExitFile(t, f.p("facet_warn.json"), `{"cohort":`+c+`,"fields":["name"],"discrete_top_k":3}`)
	writeExitFile(t, f.p("facet_bad.json"), `{"cohort":`+c+`,"fields":["nope"]}`)
	writeExitFile(t, f.p("mat.json"), `{"cohort":`+c+`,"windows":[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"age"}]}]}`)
	writeExitFile(t, f.p("spec.json"), `{"row_count":5,"fields":[{"name":"a","type":"f64","distribution":"normal"}]}`)
	writeExitFile(t, f.p("spec_bad.json"), `{"row_count":5,"fieldz":[]}`)
	writeExitFile(t, f.p("fp.json"), `{"features": ["capability:process", "AGG_COUNT"]}`)
	writeExitFile(t, f.p("fp_warn.json"), `{"features": ["capability:process", "AGG_ACME_FOO"]}`)
	writeExitFile(t, f.p("fp_bad.json"), `{"features": ["AGG_SUMM"]}`)
	if err := os.MkdirAll(f.p("docs_taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExitFile(t, f.p("docs_taken/notes.md"), "mine\n")
	return f
}

// rakeExitMargins is the rake row's one margin: every `name` of the
// fixture CSV, as counts.
const rakeExitMargins = `[{"field":"name","targets_as":"counts","targets":{"alice":1,"bob":2,"charlie":1}}]`

// exitCases is the per-leaf table, run in order (later rows read what
// earlier rows wrote: shards, the profile, the managed import).
func exitCases(f exitFixture) []exitCase {
	m := f.missing
	exportCase := func(format, ext string) exitCase {
		return exitCase{leaf: "export " + format,
			fatal: []string{"--input", m, "--output", f.p("x." + ext)},
			ok:    []string{"--input", f.cohort2, "--output", f.p("e." + ext)}}
	}
	importCase := func(format, src string) exitCase {
		c := exitCase{leaf: "import " + format,
			fatal: []string{"-i", f.p("missing." + src), "-o", f.p("x.pulse")},
			ok:    []string{"-i", f.p("src." + src), "-o", f.p("i_" + format + ".pulse")}}
		if format == "csv" {
			partial := []string{"-i", f.p("partial.csv"), "-o", f.p("partial.pulse"), "--schema", f.p("sets.schema.json")}
			c.warn, c.strict = partial, append(slices.Clone(partial), "--strict")
		}
		return c
	}
	partialSchema := []string{"--schema", f.p("sets.schema.json")}
	return []exitCase{
		{leaf: "api process", fatal: []string{"-r", f.p("bad.json")}, ok: []string{"-r", f.p("req.json")},
			warn: []string{"-r", f.p("req_warn.json")}, strict: []string{"-r", f.p("req_warn.json"), "--strict"}},
		{leaf: "api process-chain", fatal: []string{"-r", f.p("chain_bad.json")}, ok: []string{"-r", f.p("chain.json")},
			warn: []string{"-r", f.p("chain_warn.json")}},
		{leaf: "api compose", fatal: []string{"-r", f.p("compose_bad.json")}, ok: []string{"-r", f.p("compose.json")},
			warn: []string{"-r", f.p("compose_warn.json")}},
		{leaf: "api sample", fatal: []string{"--input", m}, ok: []string{"--input", f.cohort, "--count", "2"}},
		{leaf: "api facet", fatal: []string{"--input", m, "--field", "name"}, ok: []string{"--input", f.cohort, "--field", "name"},
			warn: []string{"--input", f.cohort, "--field", "name", "--top-k", "1"}},
		{leaf: "api predict", fatal: []string{"-r", f.p("bad.json")}, ok: []string{"-r", f.p("req.json")}},
		{leaf: "api predict-compose", fatal: []string{"-r", f.p("compose_bad.json")}, ok: []string{"-r", f.p("compose.json")}},
		{leaf: "api predict-facet", fatal: []string{"-r", f.p("facet_bad.json")}, ok: []string{"-r", f.p("facet.json")},
			warn: []string{"-r", f.p("facet_warn.json")}, strict: []string{"-r", f.p("facet_warn.json"), "--strict"}},
		{leaf: "api predict-chain", fatal: []string{"-r", f.p("chain_bad.json")}, ok: []string{"-r", f.p("chain.json")}},
		{leaf: "api lookup", fatal: []string{"--input", f.cohort, "--key", "age=999"}, ok: []string{"--input", f.cohort, "--key", "age=10"}},
		{leaf: "api materialize", fatal: []string{"-r", f.p("bad.json"), "--out", f.p("never.pulse")},
			ok: []string{"-r", f.p("mat.json"), "--out", f.p("mat.pulse")}},
		{leaf: "cohort inspect", fatal: []string{m}, ok: []string{f.cohort}},
		{leaf: "cohort filter", fatal: []string{"-i", m, "-o", f.p("x.pulse"), "--filter", "age > 10"},
			ok: []string{"-i", f.cohort, "-o", f.p("filtered.pulse"), "--filter", "age > 10"}},
		{leaf: "convert", fatal: []string{f.p("missing.csv"), f.p("x.tsv")}, ok: []string{f.csv, f.p("conv.tsv")},
			warn:   append(slices.Clone(partialSchema), "--keep-pulse", f.p("keep.pulse"), f.p("partial.csv"), f.p("conv_partial.tsv")),
			strict: append(slices.Clone(partialSchema), "--strict", "--keep-pulse", f.p("keep.pulse"), f.p("partial.csv"), f.p("conv_partial.tsv"))},
		{leaf: "convert predict", fatal: []string{f.p("missing.csv"), f.p("x.tsv")}, ok: []string{f.csv, f.p("convp.tsv")}},
		exportCase("csv", "csv"), exportCase("tsv", "tsv"), exportCase("ndjson", "ndjson"),
		exportCase("jsonarray", "json"), exportCase("parquet", "parquet"), exportCase("arrow", "arrow"),
		exportCase("excel", "xlsx"), exportCase("spss", "sav"),
		{leaf: "export transfer", fatal: []string{"--input", m, "--output", f.p("x.pulse.zst")},
			ok: []string{"--input", f.cohort2, "--output", f.p("e.pulse.zst")}},
		{leaf: "export predict", fatal: []string{"--input", m, "--format", "csv"}, ok: []string{"--input", f.cohort2, "--format", "csv"}},
		importCase("csv", "csv"), importCase("tsv", "tsv"), importCase("ndjson", "ndjson"),
		importCase("jsonarray", "json"), importCase("parquet", "parquet"), importCase("arrow", "arrow"),
		importCase("excel", "xlsx"), importCase("spss", "sav"),
		{leaf: "import transfer", fatal: []string{"-i", f.p("missing.pulse.zst"), "-o", f.p("x.pulse")},
			ok: []string{"-i", f.p("src.pulse.zst"), "-o", f.p("i_transfer.pulse")}},
		{leaf: "import predict", fatal: []string{"-i", f.p("missing.csv")}, ok: []string{"-i", f.csv}},
		{leaf: "import schema-template", fatal: []string{f.p("missing.csv")}, ok: []string{f.csv}},
		{leaf: "import auto", fatal: []string{"missing.csv"}, ok: []string{"auto.csv"},
			warn:   append(slices.Clone(partialSchema), "--handle", "partial", "--overwrite", "partial.csv"),
			strict: append(slices.Clone(partialSchema), "--handle", "partial", "--overwrite", "--strict", "partial.csv")},
		{leaf: "import list", noFatal: "lists the managed-imports directory; an empty or absent one is an empty list", ok: []string{}},
		{leaf: "import drop", fatal: []string{"nope"}, ok: []string{"auto"}},
		{leaf: "index build", fatal: []string{m, "--key", "age"}, ok: []string{f.cohort2, "--key", "age"}},
		{leaf: "index list", fatal: []string{m}, ok: []string{f.cohort2}},
		{leaf: "index verify", fatal: []string{m, "--key", "age"}, ok: []string{f.cohort2, "--key", "age"}},
		{leaf: "index drop", fatal: []string{m, "--key", "age"}, ok: []string{f.cohort2, "--key", "age"}},
		{leaf: "shard create", fatal: []string{f.p("x_arch.pulse"), "--include", m},
			ok: []string{f.p("arch.pulse"), "--include", f.p("a.pulse"), "--include", f.p("b.pulse")}},
		{leaf: "shard add", fatal: []string{m, f.p("c.pulse")}, ok: []string{f.p("arch.pulse"), f.p("c.pulse")}},
		{leaf: "shard list", fatal: []string{m}, ok: []string{f.p("arch.pulse")}},
		{leaf: "shard verify", fatal: []string{m}, ok: []string{f.p("arch.pulse")}},
		{leaf: "shard compact", fatal: []string{m}, ok: []string{f.p("arch.pulse")}},
		{leaf: "shard extract", fatal: []string{m, "a.pulse"}, ok: []string{f.p("arch.pulse"), "a.pulse"}},
		{leaf: "shard remove", fatal: []string{m, "a.pulse"}, ok: []string{f.p("arch.pulse"), "b.pulse"}},
		{leaf: "widen", fatal: []string{m, "--field", "picks", "--to", "set_u16"}, ok: []string{f.p("sets.pulse"), "--field", "picks", "--to", "set_u16"}},
		{leaf: "dedup", fatal: []string{m, "--suggest-groups"}, ok: []string{f.cohort2, "--suggest-groups"}},
		{leaf: "rake", fatal: []string{m, "-o", f.p("never.pulse"), "--margins", rakeExitMargins},
			ok: []string{f.cohort2, "-o", f.p("raked.pulse"), "--margins", rakeExitMargins}},
		{leaf: "profile create", fatal: []string{"-i", m, "-o", f.p("x.json")}, ok: []string{"-i", f.cohort2, "-o", f.p("profile.json")}},
		{leaf: "synth from-profile", fatal: []string{"--profile", f.p("missing.json"), "--source", f.cohort2, "--output", f.p("x.pulse"), "--rows", "5"},
			ok: []string{"--profile", f.p("profile.json"), "--source", f.cohort2, "--output", f.p("sp.pulse"), "--rows", "5", "--seed", "1"}},
		{leaf: "synth from-schema", fatal: []string{"--spec", f.p("spec_bad.json"), "--output", f.p("x.pulse")},
			ok: []string{"--spec", f.p("spec.json"), "--output", f.p("ss.pulse"), "--seed", "1"}},
		{leaf: "recommend", fatal: []string{"--intent", "nope"}, ok: []string{"--intent", "compare_groups"}},
		{leaf: "explain", fatal: []string{"--root", "bogus"}, ok: []string{"--request", f.p("req.json")}},
		{leaf: "features init", fatal: []string{"--from", "no-such-example"}, ok: []string{}},
		{leaf: "features check", fatal: []string{f.p("fp_bad.json")}, ok: []string{f.p("fp.json")}, warn: []string{f.p("fp_warn.json")}},
		{leaf: "features diff", fatal: []string{f.p("missing.json")}, ok: []string{f.p("fp.json")}},
		{leaf: "features show", fatal: []string{f.p("missing.json")}, ok: []string{f.p("fp.json")}},
		{leaf: "docs export", fatal: []string{"--out", f.p("docs_taken")}, ok: []string{"--out", f.p("docs_out")}},
		{leaf: "errors list", noFatal: "an unmatched filter is an empty list", ok: []string{}},
		{leaf: "errors lookup", fatal: []string{"NOPE"}, ok: []string{"PULSE_REQUEST_UNKNOWN_FIELD"}},
		{leaf: "examples search", noFatal: "an unmatched query is an empty list", ok: []string{"--query", "count"}},
		{leaf: "examples show", fatal: []string{"nope"}, ok: []string{"categorical_mode_count"}},
		{leaf: "skills list", noFatal: "lists the embedded pack", ok: []string{}},
		{leaf: "skills show", fatal: []string{"nope"}, ok: []string{"session-bootstrap"}},
		{leaf: "schema", noFatal: "prints the embedded payload schema", ok: []string{}},
		{leaf: "version", noFatal: "prints build info (a bad --log-level is a usage error, TestExitStatus_PersistentFlagIsUsage)", ok: []string{}},
	}
}

// leafCommand finds the leaf at path ("api process") in the tree.
func leafCommand(t *testing.T, path string) *cli.Command {
	t.Helper()
	cmds := buildApp().Commands
	var cur *cli.Command
	for _, name := range strings.Fields(path) {
		cur = nil
		for _, c := range cmds {
			if c.Name == name {
				cur = c
				break
			}
		}
		if cur == nil {
			t.Fatalf("no command %q in the tree", path)
		}
		cmds = cur.Commands
	}
	return cur
}

func hasFlag(c *cli.Command, name string) bool {
	for _, fl := range c.Flags {
		if slices.Contains(fl.Names(), name) {
			return true
		}
	}
	return false
}

// runExit runs main's body in process and returns its exit status.
func runExit(t *testing.T, args ...string) (status int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	status = run(context.Background(), append([]string{"pulse"}, args...), &out, &errb)
	return status, out.String(), errb.String()
}

func leafArgs(path string, json bool, rest []string) []string {
	args := strings.Fields(path)
	if json {
		args = append(args, "--json")
	}
	return append(args, rest...)
}

// TestExitStatus_TableCoversEveryLeaf: the table and the command tree
// name the same leaves, in both directions.
func TestExitStatus_TableCoversEveryLeaf(t *testing.T) {
	var table []string
	seen := map[string]bool{}
	for _, c := range exitCases(exitFixture{}) {
		if seen[c.leaf] {
			t.Errorf("duplicate row for %q", c.leaf)
		}
		seen[c.leaf] = true
		table = append(table, "pulse "+c.leaf)
		if c.fatal == nil && c.noFatal == "" {
			t.Errorf("%q has no fatal invocation and no reason why", c.leaf)
		}
		if c.ok == nil {
			t.Errorf("%q has no success invocation", c.leaf)
		}
	}
	for leaf := range exitExempt {
		table = append(table, leaf)
	}
	sort.Strings(table)
	if leaves := cliLeaves(); !slices.Equal(leaves, table) {
		for _, l := range leaves {
			if !slices.Contains(table, l) {
				t.Errorf("leaf %q has no exit-status row: add one to exitCases", l)
			}
		}
		for _, l := range table {
			if !slices.Contains(leaves, l) {
				t.Errorf("exit-status row %q names no leaf in the command tree", l)
			}
		}
	}
}

// TestExitStatus_EveryLeaf runs each row: a bad flag exits 2 with no
// envelope; a fatal error exits 1 in text mode (stderr) and under --json
// (an envelope with errors, nothing added to stderr); success exits 0,
// warnings included; a warning --strict escalates exits 1.
func TestExitStatus_EveryLeaf(t *testing.T) {
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

// isEnvelope reports whether stdout is a JSON document (help text is not).
func isEnvelope(out string) bool { return strings.HasPrefix(strings.TrimSpace(out), "{") }

// decodeExitEnvelope decodes out as a --json envelope; nil when out is
// not one (a leaf whose --json output is a bare document).
func decodeExitEnvelope(t *testing.T, what, out string) *descriptor.Envelope {
	t.Helper()
	if !strings.Contains(out, `"format_version"`) {
		return nil
	}
	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Errorf("%s: stdout is not one envelope: %v\n%s", what, err, out)
		return nil
	}
	return &env
}

// TestExitStatus_RootAndUsage: the root `pulse --json` follows the same
// rule, and the parser's refusals (unknown flag, missing required flag)
// and a bad persistent flag value exit 2 before any leaf runs.
func TestExitStatus_RootAndUsage(t *testing.T) {
	if s, out, _ := runExit(t, "--json", "--intent", "nope"); s != pcli.ExitFatal || !strings.Contains(out, "PULSE_RECOMMEND_INTENT_UNKNOWN") {
		t.Errorf("root --json --intent nope: exit %d (want 1)\n%s", s, out)
	}
	if s, _, _ := runExit(t, "--json"); s != pcli.ExitOK {
		t.Errorf("root --json: exit %d (want 0)", s)
	}
	for _, args := range [][]string{
		{"api", "process", "--json"},                // required --request missing
		{"api", "sample", "--json", "--count", "x"}, // malformed flag value
		{"--no-such-root-flag"},
	} {
		if s, out, _ := runExit(t, args...); s != pcli.ExitUsage || isEnvelope(out) {
			t.Errorf("%v: exit %d (want 2, no envelope)", args, s)
		}
	}
}

// TestExitStatus_PersistentFlagIsUsage: an unknown --log-level and a
// negative --shard-workers / --decode-workers (U51 E2-S2) are refused
// before the leaf runs: exit 2, still the CLI_INPUT message.
func TestExitStatus_PersistentFlagIsUsage(t *testing.T) {
	for _, args := range [][]string{
		{"--log-level", "loud"},
		{"--shard-workers", "-1"},
		{"--decode-workers", "-1"},
	} {
		s, _, errOut := runExit(t, append([]string{"version"}, args...)...)
		if s != pcli.ExitUsage || !strings.Contains(errOut, strings.Join(args, " ")) {
			t.Errorf("%v: exit %d (want 2), stderr %q", args, s, errOut)
		}
	}
}

// TestExitStatus_Binary is the acceptance case run as a real process:
// `pulse api process -r bad.json --json; echo $?` prints 1, stdout is
// the envelope alone and stderr stays empty. Text mode exits 1 with the
// error on stderr; a warning-only predict exits 0; a bad flag exits 2.
func TestExitStatus_Binary(t *testing.T) {
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
