// Package doccoverage generates the documentation coverage matrix — the
// book page docs/src/contract/coverage.md — and reports its gaps.
//
// Rows come from sources of truth, never a hand-kept list: the frozen public
// API dump (internal/apigolden/testdata/public_api.txt), the CLI command tree
// the binary mounts (internal/cli.App), the default instance's manifest
// (MCP tools, operators, field types, error codes), the MCP adapter's prompt
// and resource registrations, the PULSE_* environment variables the Go
// source reads, the Options fields and the Request / Response slots. Columns
// are the surfaces a reader finds documentation in: the mdBook (docs/src),
// the served skill pack, GoDoc and CLI help. The CLI tree arrives as data
// (CLISurface) from internal/cli's tests, which own the gates and the
// regeneration (`make docs` runs them with -update-coverage). Each row kind declares which
// columns it requires; a required cell that is empty is a gap, and
// TestDocsCoverageNoGaps fails on a gap its shrink-only allowlist
// (internal/doccheck/allowlists/coverage.txt) does not name.
//
// Mentions are verbatim: an identifier counts as documented in the book or
// the skill pack when its exact spelling appears there as a whole token.
// The mdBook cell is stricter for two kinds of row: operators, field types
// and MCP tools ignore the generated Analysis Guide (Kind.BookHandWritten),
// and a Request / Response slot needs a code span or the qualified
// Request.<Field> spelling (Kind.BookQualifier).
//
// Import ceiling: this package builds a default *pulse.Pulse, so it sits
// above the engine, but it never imports the CLI framework or internal/cli
// (TestRootTestGraphExcludesCLI, TestObservabilityVendorImportBoundaryModule). It is documentation tooling: no
// no-execute package (internal/descriptor, descriptor/) may import it
// (TestDocCoverageNotImportedByDescriptor).
package doccoverage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccheck"
)

// PagePath is the repo-relative path of the generated page.
const PagePath = "docs/src/contract/coverage.md"

// AllowlistName is the doccheck allowlist the no-gaps gate reconciles with.
const AllowlistName = "coverage"

// Surface is one documentation column.
type Surface int

// The columns, in page order.
const (
	Book Surface = iota
	Skill
	GoDoc
	Help
	numSurfaces
)

var surfaceNames = [numSurfaces]string{"mdBook", "Skill pack", "GoDoc", "CLI help"}

// surfaceKeys spell a surface in an allowlist entry.
var surfaceKeys = [numSurfaces]string{"book", "skill", "godoc", "help"}

func (s Surface) String() string { return surfaceNames[s] }

// Need says whether a row kind requires a column.
type Need uint8

// Needs.
const (
	NA Need = iota
	Optional
	Required
)

// Kind is one row family: where its rows come from, how an identifier is
// spelled, and which columns it requires.
type Kind struct {
	ID       string
	Title    string
	Source   string
	Spelling string
	Needs    [numSurfaces]Need
	// HelpMention: the CLI help cell is a verbatim mention in the help
	// text (flag and command usage, env-var sources); otherwise the
	// collector sets it (the command or flag carries usage text).
	HelpMention bool
	// BookHandWritten: only hand-written pages count for the mdBook cell.
	// The generated Analysis Guide (GuideDir) names every row of this
	// kind by construction, so a mention there proves nothing.
	BookHandWritten bool
	// BookQualifier: the mdBook cell needs the identifier as a whole code
	// span (`group_by`) or the qualified Go spelling <BookQualifier>.<Field>
	// (Request.GroupBy); a bare word in prose does not count, because many
	// slot keys are common words (label, sort, weight).
	BookQualifier string
}

// GuideDir is the generated Analysis Guide under the book source
// (`make docs` writes it); BookHandWritten kinds ignore its pages.
const GuideDir = "docs/src/guide"

// Kind IDs.
const (
	KindAPI          = "api"
	KindOptions      = "options"
	KindRequestSlot  = "request-slot"
	KindResponseSlot = "response-slot"
	KindCLILeaf      = "cli-leaf"
	KindCLIFlag      = "cli-flag"
	KindMCPTool      = "mcp-tool"
	KindMCPPrompt    = "mcp-prompt"
	KindMCPResource  = "mcp-resource"
	KindOperator     = "operator"
	KindFieldType    = "field-type"
	KindErrorCode    = "error-code"
	KindEnvVar       = "env-var"
)

// Kinds returns every row kind in page order.
func Kinds() []Kind {
	const o, r, n = Optional, Required, NA
	return []Kind{
		{ID: KindAPI, Title: "Go API symbols", Source: "`internal/apigolden/testdata/public_api.txt` (types, funcs, consts, vars, methods; struct fields are covered by their type)",
			Spelling: "`<package>.<Name>`, methods `<package>.<Type>.<Method>`", Needs: [numSurfaces]Need{o, o, r, n}},
		{ID: KindOptions, Title: "Options fields", Source: "the `pulse.Options` fields in `public_api.txt`",
			Spelling: "the Go field name (`DataDir`)", Needs: [numSurfaces]Need{r, o, r, n}},
		{ID: KindRequestSlot, Title: "Request slots", Source: "the `types.Request` fields in `public_api.txt`",
			Spelling: "the JSON key (`group_by`); in the mdBook a code span or `Request.<Field>`", Needs: [numSurfaces]Need{r, r, r, n}, BookQualifier: "Request"},
		{ID: KindResponseSlot, Title: "Response slots", Source: "the `types.Response` fields in `public_api.txt`",
			Spelling: "the JSON key; in the mdBook a code span or `Response.<Field>`", Needs: [numSurfaces]Need{r, o, r, n}, BookQualifier: "Response"},
		{ID: KindCLILeaf, Title: "CLI leaves", Source: "every runnable command in the tree `internal/cli.App` builds",
			Spelling: "the full invocation path (`pulse cohort inspect`)", Needs: [numSurfaces]Need{r, o, n, r}},
		{ID: KindCLIFlag, Title: "CLI flags", Source: "every flag name (aliases included) mounted anywhere in that tree",
			Spelling: "`--name`, or `-x` for a one-letter name", Needs: [numSurfaces]Need{r, o, n, r}},
		{ID: KindMCPTool, Title: "MCP tools", Source: "the default instance's manifest `mcp_tools`",
			Spelling: "the tool name", Needs: [numSurfaces]Need{r, r, n, n}, BookHandWritten: true},
		{ID: KindMCPPrompt, Title: "MCP prompts", Source: "`gosdk.RegisteredPrompts()`",
			Spelling: "the prompt name", Needs: [numSurfaces]Need{r, o, n, n}},
		{ID: KindMCPResource, Title: "MCP resources", Source: "the `mcp/gosdk` resource URI and scheme constants",
			Spelling: "the URI or scheme", Needs: [numSurfaces]Need{r, o, n, n}},
		{ID: KindOperator, Title: "Operators", Source: "the default instance's manifest (components, tests, post-tests, regressions, matrices, overlays, synth distributions)",
			Spelling: "the operator name", Needs: [numSurfaces]Need{r, r, n, n}, BookHandWritten: true},
		{ID: KindFieldType, Title: "Field types", Source: "the default instance's manifest `cohort_types`",
			Spelling: "the type name", Needs: [numSurfaces]Need{r, r, n, n}, BookHandWritten: true},
		{ID: KindErrorCode, Title: "Error codes", Source: "the default instance's manifest `error_codes`",
			Spelling: "the code", Needs: [numSurfaces]Need{r, o, n, n}},
		{ID: KindEnvVar, Title: "Environment variables", Source: "the `PULSE_*` names non-test Go source reads (`os.Getenv`, `os.LookupEnv`, `cli.EnvVars`, and constants in files that read the environment)",
			Spelling: "the variable name", Needs: [numSurfaces]Need{r, o, n, o}, HelpMention: true},
	}
}

func kindByID(id string) (Kind, bool) {
	for _, k := range Kinds() {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// Row is one documented thing.
type Row struct {
	Kind  string
	Group string // optional sub-section (package, operator family)
	ID    string // the identifier, spelled verbatim
	// Field is the Go field name of a Request / Response slot row (the
	// qualified mdBook spelling Request.<Field>).
	Field string
	// GoDoc / Help are the intrinsic cells the collector decides (GoDoc for
	// every kind that needs it; Help unless the kind is HelpMention).
	GoDoc bool
	Help  bool
}

// Inputs is what the matrix is built from: the rows and the text corpora
// the verbatim mentions are looked up in.
type Inputs struct {
	Rows []Row
	// Book maps a docs/src page path to its text (the coverage page itself
	// excluded); Skills a skill name to its served body.
	Book   map[string]string
	Skills map[string]string
	// Help is the CLI help text: every command's usage and description and
	// every flag's rendered help line.
	Help string
}

// Cell is one matrix cell.
type Cell uint8

// Cells.
const (
	CellNA Cell = iota
	CellPresent
	CellMissing
)

// Line is one built row.
type Line struct {
	Row
	Cells [numSurfaces]Cell
}

// Matrix is the built coverage matrix.
type Matrix struct {
	Lines []Line
}

// Build looks every row up in every surface its kind applies to. Rows are
// sorted by kind (page order), group, then identifier; a duplicate
// identifier within a kind is an error.
func Build(in Inputs) (*Matrix, error) {
	book := newCorpus(sortedValues(in.Book))
	bookHand := newCorpus(sortedValues(handWritten(in.Book)))
	skill := newCorpus(sortedValues(in.Skills))
	help := newCorpus([]string{in.Help})
	order := map[string]int{}
	for i, k := range Kinds() {
		order[k.ID] = i
	}
	seen := map[[2]string]bool{}
	m := &Matrix{}
	for _, r := range in.Rows {
		k, ok := kindByID(r.Kind)
		if !ok {
			return nil, fmt.Errorf("doccoverage: row %q has unknown kind %q", r.ID, r.Kind)
		}
		key := [2]string{r.Kind, r.ID}
		if seen[key] {
			return nil, fmt.Errorf("doccoverage: duplicate %s row %q", r.Kind, r.ID)
		}
		seen[key] = true
		l := Line{Row: r}
		for s := Surface(0); s < numSurfaces; s++ {
			if k.Needs[s] == NA {
				continue
			}
			var has bool
			switch s {
			case Book:
				c := book
				if k.BookHandWritten {
					c = bookHand
				}
				if k.BookQualifier != "" {
					has = c.has("`"+r.ID+"`") || r.Field != "" && c.has(k.BookQualifier+"."+r.Field)
				} else {
					has = c.has(r.ID)
				}
			case Skill:
				has = skill.has(r.ID)
			case GoDoc:
				has = r.GoDoc
			case Help:
				if k.HelpMention {
					has = help.has(r.ID)
				} else {
					has = r.Help
				}
			}
			l.Cells[s] = CellMissing
			if has {
				l.Cells[s] = CellPresent
			}
		}
		m.Lines = append(m.Lines, l)
	}
	sort.SliceStable(m.Lines, func(i, j int) bool {
		a, b := m.Lines[i], m.Lines[j]
		if a.Kind != b.Kind {
			return order[a.Kind] < order[b.Kind]
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.ID < b.ID
	})
	return m, nil
}

// handWritten drops the generated Analysis Guide's pages.
func handWritten(book map[string]string) map[string]string {
	out := make(map[string]string, len(book))
	for p, text := range book {
		if p != GuideDir && !strings.HasPrefix(p, GuideDir+"/") {
			out[p] = text
		}
	}
	return out
}

func sortedValues(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

// Gaps returns one violation per required cell that is empty. The
// allowlist key is (kind, "<identifier> <surface>").
func (m *Matrix) Gaps() []doccheck.Violation {
	var vs []doccheck.Violation
	for _, l := range m.Lines {
		k, _ := kindByID(l.Kind)
		for s := Surface(0); s < numSurfaces; s++ {
			if k.Needs[s] != Required || l.Cells[s] != CellMissing {
				continue
			}
			vs = append(vs, doccheck.Violation{
				Path:    l.Kind,
				Entry:   l.ID + " " + surfaceKeys[s],
				Message: fmt.Sprintf("%s %s has no %s documentation; %s, then run `make docs`", k.Title, l.ID, s, fixHint(k, s)),
			})
		}
	}
	return vs
}

func fixHint(k Kind, s Surface) string {
	switch s {
	case Book:
		return "name it verbatim on a page under docs/src"
	case Skill:
		return "name it verbatim in a skill under internal/skills"
	case GoDoc:
		return "give its declaration a doc comment"
	default:
		if k.HelpMention {
			return "name it in a flag or command's help text"
		}
		return "give the command or flag a Usage string"
	}
}

// Render writes the page.
func (m *Matrix) Render() []byte {
	var b strings.Builder
	b.WriteString("# Documentation Coverage\n\n")
	b.WriteString("<!-- Generated by `make docs` (internal/doccoverage). Do not edit: TestDocsCoverageCurrent fails when this page is stale. -->\n\n")
	b.WriteString("This page shows, for every public thing Pulse ships, where it is documented. Rows come from sources of truth (the frozen public API dump, the CLI command tree, the manifest, the MCP registrations, the Go source), so a new thing appears here the moment it exists.\n\n")
	b.WriteString("## Rules\n\n")
	b.WriteString("- **Verbatim mention.** A row counts as documented in the **mdBook** (every page under `docs/src`, the generated Analysis Guide included, this page excluded) or the **Skill pack** (every skill as the default instance serves it) when its identifier appears there exactly as spelled below, as a whole token: no letter, digit, `_` or `-` directly before or after it.\n")
	b.WriteString("- **Stricter mdBook rule.** Operators, field types and MCP tools count only hand-written pages: the generated Analysis Guide (`docs/src/guide`) names every one by construction. A Request or Response slot counts only as a whole code span (`` `group_by` ``) or the qualified Go spelling (`Request.GroupBy`, `Response.Data`), never as a bare word in prose.\n")
	b.WriteString("- **GoDoc** is present when the declaration carries a non-empty doc comment. A declaration inside a grouped `const` / `var` / `type` block also counts the group's doc comment and its own trailing line comment; a method on an alias resolves to the aliased type, a promoted method to the embedded type that declares it.\n")
	b.WriteString("- **CLI help** is present for a leaf or a flag when it carries usage text (a flag on every command that mounts it); for an environment variable, when it appears verbatim in the help text.\n")
	b.WriteString("- Each row kind declares its required columns. A required cell that is empty is a **gap**; `TestDocsCoverageNoGaps` fails on a gap the shrink-only allowlist `internal/doccheck/allowlists/coverage.txt` does not name, and on an entry whose gap is closed.\n\n")
	b.WriteString("Legend: `yes` documented; **gap** required and missing; blank optional and missing; `—` not applicable.\n\n")
	b.WriteString("| Row kind | Source of truth | Spelled as | mdBook | Skill pack | GoDoc | CLI help |\n|---|---|---|---|---|---|---|\n")
	for _, k := range Kinds() {
		fmt.Fprintf(&b, "| %s | %s | %s |", k.Title, k.Source, k.Spelling)
		for s := Surface(0); s < numSurfaces; s++ {
			b.WriteString(" " + needText(k.Needs[s]) + " |")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Summary\n\n| Row kind | Rows | Gaps |\n|---|---:|---:|\n")
	byKind := map[string][]Line{}
	for _, l := range m.Lines {
		byKind[l.Kind] = append(byKind[l.Kind], l)
	}
	gaps := map[string]int{}
	for _, v := range m.Gaps() {
		gaps[v.Path]++
	}
	for _, k := range Kinds() {
		fmt.Fprintf(&b, "| [%s](#%s) | %d | %d |\n", k.Title, doccheck.Slug(k.Title), len(byKind[k.ID]), gaps[k.ID])
	}
	for _, k := range Kinds() {
		lines := byKind[k.ID]
		fmt.Fprintf(&b, "\n## %s\n", k.Title)
		var cols []Surface
		for s := Surface(0); s < numSurfaces; s++ {
			if k.Needs[s] != NA {
				cols = append(cols, s)
			}
		}
		group := "\x00"
		for _, l := range lines {
			if l.Group != group {
				group = l.Group
				if group != "" {
					fmt.Fprintf(&b, "\n### %s %s\n", k.Title, group)
				}
				b.WriteString("\n| Identifier |")
				for _, s := range cols {
					b.WriteString(" " + s.String() + " |")
				}
				b.WriteString("\n|---|")
				for range cols {
					b.WriteString("---|")
				}
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "| `%s` |", l.ID)
			for _, s := range cols {
				b.WriteString(" " + cellText(l.Cells[s], k.Needs[s]) + " |")
			}
			b.WriteString("\n")
		}
		if len(lines) == 0 {
			b.WriteString("\nNo rows.\n")
		}
	}
	return []byte(b.String())
}

func needText(n Need) string {
	switch n {
	case Required:
		return "required"
	case Optional:
		return "optional"
	default:
		return "—"
	}
}

func cellText(c Cell, n Need) string {
	switch c {
	case CellPresent:
		return "yes"
	case CellMissing:
		if n == Required {
			return "**gap**"
		}
		return ""
	default:
		return "—"
	}
}

// Generate collects from the module tree at fsys and the CLI surface and
// renders the page.
func Generate(fsys afero.Fs, cliTree CLISurface) ([]byte, error) {
	m, err := BuildFrom(fsys, cliTree)
	if err != nil {
		return nil, err
	}
	return m.Render(), nil
}

// BuildFrom collects from the module tree at fsys and the CLI surface and
// builds the matrix.
func BuildFrom(fsys afero.Fs, cliTree CLISurface) (*Matrix, error) {
	in, err := Collect(fsys, cliTree)
	if err != nil {
		return nil, err
	}
	return Build(in)
}
