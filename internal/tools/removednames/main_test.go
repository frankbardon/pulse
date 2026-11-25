package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func files(m map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = []byte(v)
	}
	return out
}

func byName(cands []*Candidate) map[string]*Candidate {
	out := map[string]*Candidate{}
	for _, c := range cands {
		out[c.Name] = c
	}
	return out
}

func TestSweepFindsRemovedNames(t *testing.T) {
	t.Parallel()
	old := files(map[string]string{
		"foo/foo.go": `package foo
func GoneHelper() {}
func KeptHelper() {}
type MovedThing struct{}
func (MovedThing) LiveMethod() {}
var Widget = 1
const unexported = 1
`,
		"foo/foo_extra.go": `package foo
func LiveMethod() {}
func LiteralName() {}
`,
		"types/types.go": `package types
type AggregationType string
const (
	AGG_GONE AggregationType = "AGG_GONE"
	AGG_KEPT AggregationType = "AGG_KEPT"
)
`,
		"errors/codes.go": `package errors
type Code string
const (
	PULSE_GONE_CODE Code = "PULSE_GONE_CODE"
	PULSE_KEPT_CODE Code = "PULSE_KEPT_CODE"
)
`,
		"proc/overlay.go": `package proc
var names = []string{"OVERLAY_GONE_ONE", "OVERLAY_KEPT_ONE"}
// OVERLAY_ONLY_IN_COMMENT is never registered.
`,
		"internal/hidden/h.go": "package hidden\nfunc InternalGone() {}\n",
		"cmd/tool/main.go":     "package main\nfunc MainGone() {}\n",
	})
	newer := files(map[string]string{
		"foo/foo.go": `package foo
func KeptHelper() {}
`,
		"internal/foo/foo.go": `package foo
type MovedThing struct{}
func (MovedThing) LiveMethod() {}
`,
		"internal/msg/msg.go": `package msg
const m = "LiteralName stays in a message"
`,
		"types/types.go": `package types
type AggregationType string
const AGG_KEPT AggregationType = "AGG_KEPT"
`,
		"errors/codes.go": `package errors
type Code string
const PULSE_KEPT_CODE Code = "PULSE_KEPT_CODE"
`,
		"proc/overlay.go": `package proc
var names = []string{"OVERLAY_KEPT_ONE"}
`,
	})
	got := byName(Sweep(old, newer))

	want := map[string]struct {
		kind Kind
		flag string // prefix; "" = kept
	}{
		"GoneHelper":       {KindIdent, ""},
		"MovedThing":       {KindIdent, "still exported as internal/foo.MovedThing"},
		"LiveMethod":       {KindIdent, "still an exported method"},
		"LiteralName":      {KindIdent, "still appears as a string literal"},
		"Widget":           {KindIdent, "single common word"},
		"AGG_GONE":         {KindOperator, ""},
		"OVERLAY_GONE_ONE": {KindOperator, ""},
		"PULSE_GONE_CODE":  {KindErrorCode, ""},
	}
	for name, w := range want {
		c := got[name]
		if c == nil {
			t.Errorf("%s: not a candidate", name)
			continue
		}
		if c.Kind != w.kind {
			t.Errorf("%s: kind %v, want %v", name, c.Kind, w.kind)
		}
		if w.flag == "" && c.Flag != "" || w.flag != "" && !strings.HasPrefix(c.Flag, w.flag) {
			t.Errorf("%s: flag %q, want prefix %q", name, c.Flag, w.flag)
		}
	}
	for _, name := range []string{"KeptHelper", "AGG_KEPT", "PULSE_KEPT_CODE", "OVERLAY_KEPT_ONE",
		"OVERLAY_ONLY_IN_COMMENT", "InternalGone", "MainGone", "unexported", "AggregationType", "Code"} {
		if got[name] != nil {
			t.Errorf("%s: unexpected candidate %+v", name, got[name])
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d candidates, want %d", len(got), len(want))
	}
}

func TestWholeWordCount(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, needle string
		want               int
	}{
		{"bare", "use WelfordTriple here", "WelfordTriple", 1},
		{"prefix of longer identifier", "WelfordTripleX", "WelfordTriple", 0},
		{"suffix of longer identifier", "aWelfordTriple", "WelfordTriple", 0},
		{"underscore joins", "AGG_GONE_X AGG_GONE", "AGG_GONE", 1},
		{"punctuation bounds", "`AGG_GONE`, (AGG_GONE).", "AGG_GONE", 2},
		{"qualified", "io.InferSchema and InferSchema", "io.InferSchema", 1},
		{"phrase", "MCP version 1.0.0 shipped", "MCP version 1.0.0", 1},
		{"phrase followed by digit", "MCP version 1.0.01", "MCP version 1.0.0", 0},
		{"overlapping", "aa aa", "aa", 2},
		{"empty needle", "x", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WholeWordCount(tc.text, tc.needle); got != tc.want {
				t.Errorf("WholeWordCount(%q, %q) = %d, want %d", tc.text, tc.needle, got, tc.want)
			}
		})
	}
}

func TestMigrationSeeds(t *testing.T) {
	t.Parallel()
	guide := "| Kind | Meaning |\n|---|---|\n| **removed** | gone |\n\n" +
		"| Old | New | Kind | How | Unit |\n|---|---|---|---|---|\n" +
		"| `io.NewConvertJob(…)` | `&io.ConvertJob{…}` | removed | x | U02 |\n" +
		"| `(*Pulse).Service()` + `service` | opt | removed / replaced | x | U02 |\n" +
		"| `KeptThing` | same | kept | none | U02 |\n" +
		"| `Gone`, the `Compare*Overlay*` helpers, `a b` | internal | removed | x | U02 |\n\n" +
		"| Before | After | Kind | Adapt | Unit |\n|---|---|---|---|---|\n" +
		"| `NotOld` | x | removed | x | U9 |\n\n" +
		"| Old | New | Notes | Adapt | Unit |\n|---|---|---|---|---|\n" +
		"| `NotKindTable` | x | removed | x | U9 |\n"
	got := MigrationSeeds(guide)
	want := []string{"io.NewConvertJob", "Service", "Gone"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("MigrationSeeds = %v, want %v", got, want)
	}
}

func TestClassifySeeds(t *testing.T) {
	t.Parallel()
	newer := files(map[string]string{
		"io/io.go":            "package io\ntype ConvertJob struct{}\n",
		"internal/io/x.go":    "package io\nfunc InferSchema() {}\n",
		"pulse.go":            "package pulse\ntype Pulse struct{}\nfunc (*Pulse) PredictExport() {}\n",
		"errors/codes.go":     "package errors\ntype Code string\nconst PULSE_LIVE Code = \"PULSE_LIVE\"\n",
		"internal/msg/msg.go": "package msg\nconst m = \"OldWord lives\"\n",
	})
	seeds := ClassifySeeds([]string{"WelfordTriple", "PULSE_LIVE"},
		[]string{"PULSE_LIVE", "io.InferSchema", "io.ConvertJob", "InferSchema", "PredictExport",
			"OldWord", "Index", "GoneForGood"}, newer)
	want := map[string]string{
		"WelfordTriple":  "",
		"PULSE_LIVE":     "", // fixed seeds are never flagged, and not duplicated
		"io.InferSchema": "",
		"io.ConvertJob":  "still live: io.ConvertJob",
		"InferSchema":    "still live: exported as internal/io.InferSchema",
		"PredictExport":  "still live: exported method",
		"OldWord":        "still live: string literal",
		"Index":          "short or single common word",
		"GoneForGood":    "",
	}
	if len(seeds) != len(want) {
		t.Fatalf("got %d seeds %+v, want %d", len(seeds), seeds, len(want))
	}
	for _, s := range seeds {
		w, ok := want[s.Name]
		if !ok {
			t.Errorf("unexpected seed %q", s.Name)
			continue
		}
		if w == "" && s.Flag != "" || w != "" && !strings.HasPrefix(s.Flag, w) {
			t.Errorf("%s: flag %q, want prefix %q", s.Name, s.Flag, w)
		}
	}
}

// refDir is assembled from parts so the dangling-pointer gate
// (TestReferenceDocPointersResolve) does not read the fixture path as a real
// reference pointer.
var refDir = ".claude" + "/reference"

func TestServedTextScope(t *testing.T) {
	t.Parallel()
	fsys := afero.NewMemMapFs()
	for p, c := range map[string]string{
		"README.md":                              "GoneHelper",
		"CLAUDE.md":                              "GoneHelper",
		"docs/src/a.md":                          "GoneHelper GoneHelper",
		"docs/roadmap/u.md":                      "GoneHelper",
		refDir + "/r.md":                         "GoneHelper",
		"internal/skills/s.md":                   "GoneHelper",
		"internal/examples/x/1.json":             `{"_meta":{"description":"GoneHelper"}}`,
		"errors/fixup_metadata.go":               "package errors\n// GoneHelper in a comment\nvar m = \"GoneHelper in a message\"\nvar GoneHelper = 1\n",
		"errors/codes_test.go":                   "package errors\nvar s = \"GoneHelper\"\n",
		"internal/descriptor/testdata/golden.go": "package x\nvar s = \"GoneHelper\"\n",
	} {
		if err := afero.WriteFile(fsys, p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	served, err := ServedText(fsys)
	if err != nil {
		t.Fatal(err)
	}
	cands := []*Candidate{{Name: "GoneHelper", Packages: []string{"foo"}}}
	CountHits(cands, served)
	// README 1 + docs/src 2 + skills 1 + example 1 + fixup string literal 1.
	if cands[0].Hits != 6 {
		t.Errorf("hits = %d, want 6 (served: %v)", cands[0].Hits, keys(served))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRenderShape(t *testing.T) {
	t.Parallel()
	cands := []*Candidate{
		{Name: "GoneHelper", Kind: KindIdent, Packages: []string{"io/format"}, Hits: 2},
		{Name: "MovedThing", Kind: KindIdent, Packages: []string{"."}, Hits: 1, QualifiedHits: 0,
			Flag: "still exported as internal/foo.MovedThing; x"},
		{Name: "CSV", Kind: KindIdent, Packages: []string{"io/format"}, Flag: "short name"},
		{Name: "PULSE_GONE", Kind: KindErrorCode, Packages: []string{"errors"}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, "v0.39.0", "HEAD", cands, []Seed{{Name: "WelfordTriple", Hits: 3}, {Name: "Index", Flag: "short"}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, line := range []string{
		"- [x] GoneHelper — io/format/exported identifier — 2\n",
		"- [x] PULSE_GONE — errors/error code — 0\n",
		"- [ ] CSV — io/format/exported identifier — 0 — short name\n",
		"- [ ] MovedThing — pulse/exported identifier — 1 (qualified hits 0) — still exported as internal/foo.MovedThing; x\n",
		"- [x] WelfordTriple — seed — 3\n",
		"- [ ] Index — seed — 0 — short\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("render missing %q\n%s", line, out)
		}
	}
	for _, kept := range []string{"- [x] CSV", "- [x] MovedThing", "- [x] Index"} {
		if strings.Contains(out, kept) {
			t.Errorf("flagged name rendered as kept: %q", kept)
		}
	}
	if strings.Index(out, "CSV") > strings.Index(out, "MovedThing") {
		t.Error("generic flags must precede the moved-name list")
	}
}
