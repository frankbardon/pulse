package doccoverage

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestCorpusVerbatimMention(t *testing.T) {
	t.Parallel()
	c := newCorpus([]string{
		"Use `--json` for output. See otelpulse.New and pulse cohort info.",
		"Read pulse://schema; skills live under pulse-skill://op-agg-count. Flag -h shows help. AGG_COUNT_X is not it.",
		"group_by-ish and type-uint8 do not count.",
	})
	cases := []struct {
		id   string
		want bool
	}{
		{"--json", true},
		{"--js", false},         // a prefix of a longer flag
		{"json", false},         // preceded by '-'
		{"pulse.New", false},    // only otelpulse.New is there
		{"otelpulse.New", true}, //
		{"pulse cohort info", true},
		{"pulse cohort", true},   // a whole-token prefix of a leaf path is still a mention
		{"pulse://schema", true}, //
		{"pulse://", true},       // a scheme ends in a non-word byte: no trailing boundary
		{"pulse-skill://", true},
		{"-h", true},
		{"AGG_COUNT", false}, // AGG_COUNT_X only
		{"group_by", false},  // followed by '-'
		{"uint8", false},     // preceded by '-'
		{"absent", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := c.has(tc.id); got != tc.want {
			t.Errorf("has(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
	// A match never spans two documents.
	if newCorpus([]string{"pulse", "New"}).has("pulse New") {
		t.Error("a match spanned two documents")
	}
}

func TestBuildAndGaps(t *testing.T) {
	t.Parallel()
	in := Inputs{
		Rows: []Row{
			{Kind: KindOperator, Group: "aggregators", ID: "AGG_DOCUMENTED"},
			{Kind: KindOperator, Group: "aggregators", ID: "AGG_BOOK_ONLY"},
			{Kind: KindErrorCode, ID: "PULSE_SKILL_ONLY"},
			{Kind: KindAPI, Group: "pulse", ID: "pulse.Undocumented", GoDoc: false},
			{Kind: KindAPI, Group: "pulse", ID: "pulse.Documented", GoDoc: true},
			{Kind: KindCLIFlag, ID: "--quiet", Help: false},
			{Kind: KindEnvVar, ID: "PULSE_X_DIR"},
		},
		Book:   map[string]string{"a.md": "AGG_DOCUMENTED AGG_BOOK_ONLY --quiet PULSE_X_DIR"},
		Skills: map[string]string{"s": "AGG_DOCUMENTED PULSE_SKILL_ONLY"},
		Help:   "--quiet [$PULSE_X_DIR]",
	}
	m, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range m.Gaps() {
		got = append(got, v.Path+"\t"+v.Entry)
		if !strings.Contains(v.Message, "make docs") {
			t.Errorf("message %q does not say how to fix", v.Message)
		}
	}
	want := []string{
		"api\tpulse.Undocumented godoc",     // GoDoc required for API
		"cli-flag\t--quiet help",            // no usage text
		"operator\tAGG_BOOK_ONLY skill",     // skill required for operators
		"error-code\tPULSE_SKILL_ONLY book", // book required for codes; skill optional
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gaps =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Env var help is a verbatim mention in the help corpus, and optional.
	for _, l := range m.Lines {
		if l.ID == "PULSE_X_DIR" && l.Cells[Help] != CellPresent {
			t.Errorf("env var help cell = %v, want present", l.Cells[Help])
		}
	}

	page := string(m.Render())
	for _, frag := range []string{
		"# Documentation Coverage", "Verbatim mention", "required", "| `AGG_BOOK_ONLY` | yes | **gap** |",
		"### Operators aggregators", "| [Operators](#operators) | 2 | 1 |",
	} {
		if !strings.Contains(page, frag) {
			t.Errorf("page lacks %q", frag)
		}
	}

	in.Rows = append(in.Rows, Row{Kind: KindOperator, ID: "AGG_DOCUMENTED"})
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate row: err = %v", err)
	}
	if _, err := Build(Inputs{Rows: []Row{{Kind: "nope", ID: "x"}}}); err == nil {
		t.Fatal("unknown kind must be an error")
	}
}

// TestKindsDeclareRequiredColumns: every kind requires at least one column
// and names its source and spelling (they are printed as the page's rules).
func TestKindsDeclareRequiredColumns(t *testing.T) {
	t.Parallel()
	for _, k := range Kinds() {
		req := false
		for _, n := range k.Needs {
			req = req || n == Required
		}
		if !req || k.Source == "" || k.Spelling == "" || k.Title == "" {
			t.Errorf("kind %s: needs a required column, a source, a spelling and a title", k.ID)
		}
	}
}

const testDump = `pulse: package pulse
pulse: type Alias = pulse/internal/impl.Thing
pulse: type Alias method (*Thing) Run() error
pulse: type Alias method (*Thing) Inherited() int
pulse: type Options struct
pulse: type Options field DataDir string
pulse: type Options field Bare int
pulse: func Documented() error
pulse: func Bare()
pulse: const Grouped pulse.Kind = "g"
pulse: const Lone pulse.Kind = "l"
pulse: type Box[T any] struct
pulse: type Box[T any] field V T
pulse/types: package types
pulse/types: type Request struct
pulse/types: type Request field GroupBy []string "json:\"group_by,omitempty\""
pulse/types: type Request field Skip int "json:\"-\""

// golden-hash: x
`

var testSources = map[string]string{
	"pulse.go": `package pulse

import "github.com/frankbardon/pulse/internal/impl"

// Alias is documented.
type Alias = impl.Thing

// Options configures.
type Options struct {
	// DataDir is the root.
	DataDir string
	Bare int
}

// Documented does it.
func Documented() error { return nil }

func Bare() {}

// Kinds of things.
const (
	Grouped Kind = "g"
)

const Lone Kind = "l"

type Box[T any] struct{ V T }
`,
	"internal/impl/impl.go": `package impl

// Thing embeds Base.
type Thing struct{ Base }

// Run runs.
func (t *Thing) Run() error { return nil }
`,
	"internal/impl/base.go": `package impl

type Base struct{}

// Inherited is promoted.
func (b *Base) Inherited() int { return 0 }
`,
	"types/types.go": `package types

type Request struct {
	GroupBy []string ` + "`json:\"group_by,omitempty\"`" + ` // trailing comment counts
	Skip int
}
`,
}

func TestAPIRowsGoDoc(t *testing.T) {
	t.Parallel()
	fsys := afero.NewMemMapFs()
	for p, s := range testSources {
		if err := afero.WriteFile(fsys, p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := parseAPIDump(strings.NewReader(testDump))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := apiRows(d, newGoSource(fsys))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Kind+" "+r.ID] = r.GoDoc
	}
	want := map[string]bool{
		"api pulse.Alias":           true,
		"api pulse.Alias.Run":       true, // through the alias
		"api pulse.Alias.Inherited": true, // promoted from the embedded Base
		"api pulse.Options":         true,
		"api pulse.Documented":      true,
		"api pulse.Bare":            false,
		"api pulse.Grouped":         true, // group doc
		"api pulse.Lone":            false,
		"api pulse.Box":             false, // generic name parsed
		"api types.Request":         false,
		"options DataDir":           true,
		"options Bare":              false,
		"request-slot group_by":     true, // trailing comment
	}
	if len(got) != len(want) {
		t.Errorf("rows = %v, want keys of %v (json:\"-\" skipped)", got, want)
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s: GoDoc = %v (present %v), want %v", k, g, ok, w)
		}
	}
}

func TestCLIRowsAndHelp(t *testing.T) {
	t.Parallel()
	rows, help := cliRowsAndHelp(CLISurface{
		Commands: []CLICommand{
			{Path: "pulse", Runnable: true, Usage: "root"},
			{Path: "pulse cohort", Usage: "group"},
			{Path: "pulse cohort info", Runnable: true, Usage: "Show info", Text: []string{"long text"}},
			{Path: "pulse bare", Runnable: true},
		},
		Flags: []CLIFlag{
			{Names: []string{"input", "i"}, Usage: "file", Help: "--input value, -i value  file [$PULSE_INPUT]"},
			{Names: []string{"input"}, Usage: ""}, // a second mount without usage
			{Names: []string{"json"}, Usage: "JSON"},
			{Names: []string{"out"}}, // the bare mount comes first
			{Names: []string{"out"}, Usage: "output"},
		},
	})
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Kind+" "+r.ID] = r.Help
	}
	want := map[string]bool{
		"cli-leaf pulse cohort info": true,
		"cli-leaf pulse bare":        false, // no usage
		"cli-flag --input":           false, // one mount lacks usage
		"cli-flag -i":                true,
		"cli-flag --json":            true,
		"cli-flag --out":             false, // a later mount's usage does not cover the bare one
	}
	if len(got) != len(want) {
		t.Errorf("rows = %v, want %v (the root and non-runnable groups are no leaves)", got, want)
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s: Help = %v (present %v), want %v", k, g, ok, w)
		}
	}
	for _, frag := range []string{"Show info", "long text", "[$PULSE_INPUT]"} {
		if !strings.Contains(help, frag) {
			t.Errorf("help corpus lacks %q", frag)
		}
	}
}
