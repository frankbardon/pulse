package doccoverage

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/mcp/gosdk"
)

// Collect gathers every row and corpus from the repository tree rooted at
// fsys (the module root). The manifest, MCP and skill rows describe the
// default instance — no feature profile, no table or template directory —
// so the caller clears those environment variables first, exactly as
// `make docs` does for the Analysis Guide.
func Collect(fsys afero.Fs, cliTree CLISurface) (Inputs, error) {
	var in Inputs

	f, err := fsys.Open(APIDumpPath)
	if err != nil {
		return in, err
	}
	dump, err := parseAPIDump(f)
	f.Close()
	if err != nil {
		return in, err
	}
	rows, err := apiRows(dump, newGoSource(fsys))
	if err != nil {
		return in, err
	}
	in.Rows = append(in.Rows, rows...)

	cliRows, help := cliRowsAndHelp(cliTree)
	in.Rows = append(in.Rows, cliRows...)
	in.Help = help

	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		return in, err
	}
	in.Rows = append(in.Rows, manifestRows(p)...)
	for _, name := range gosdk.RegisteredPrompts() {
		in.Rows = append(in.Rows, Row{Kind: KindMCPPrompt, ID: name})
	}
	for _, uri := range []string{gosdk.SchemaResourceURI, gosdk.CohortURIScheme, gosdk.SkillURIScheme} {
		in.Rows = append(in.Rows, Row{Kind: KindMCPResource, ID: uri})
	}

	env, err := envVars(fsys)
	if err != nil {
		return in, err
	}
	for _, v := range env {
		in.Rows = append(in.Rows, Row{Kind: KindEnvVar, ID: v})
	}

	in.Skills = map[string]string{}
	for _, m := range p.Skills() {
		if body, ok := p.Skill(m.Name); ok {
			in.Skills[m.Name] = body
		}
	}
	in.Book, err = bookPages(fsys)
	return in, err
}

// CLISurface is the CLI command tree as data. The caller that owns the
// tree (internal/cli's tests) flattens it, so this package never imports
// the CLI framework: its import graph stays inside the observability and
// test-graph boundaries.
type CLISurface struct {
	Commands []CLICommand
	Flags    []CLIFlag
}

// CLICommand is one command in the tree, root included.
type CLICommand struct {
	// Path is the full invocation path ("pulse cohort info").
	Path string
	// Runnable marks a leaf a user can run (a non-nil Action); the root
	// is never a leaf row.
	Runnable bool
	// Usage is the one-line usage text.
	Usage string
	// Text is every other help string the command prints (usage text,
	// description, args usage).
	Text []string
}

// CLIFlag is one flag mount: the names a user can type, its usage text and
// its rendered help line (env-var sources included).
type CLIFlag struct {
	Names []string
	Usage string
	Help  string
}

// cliRowsAndHelp turns the surface into leaf and flag rows plus the help
// corpus. A flag name's help cell needs usage text on every mount.
func cliRowsAndHelp(s CLISurface) ([]Row, string) {
	var rows []Row
	var help strings.Builder
	for _, c := range s.Commands {
		help.WriteString(c.Usage + "\n")
		for _, t := range c.Text {
			help.WriteString(t + "\n")
		}
		if c.Runnable && strings.Contains(c.Path, " ") {
			rows = append(rows, Row{Kind: KindCLILeaf, ID: c.Path, Help: strings.TrimSpace(c.Usage) != ""})
		}
	}
	flagUsage := map[string]bool{}
	for _, f := range s.Flags {
		help.WriteString(f.Help + "\n")
		for _, n := range f.Names {
			prev, seen := flagUsage[n]
			flagUsage[n] = (prev || !seen) && strings.TrimSpace(f.Usage) != ""
		}
	}
	for n, ok := range flagUsage {
		id := "--" + n
		if len(n) == 1 {
			id = "-" + n
		}
		rows = append(rows, Row{Kind: KindCLIFlag, ID: id, Help: ok})
	}
	return rows, help.String()
}

// manifestRows are the MCP tool, operator, field-type and error-code rows
// of the default instance's manifest.
func manifestRows(p *pulse.Pulse) []Row {
	m := p.Manifest(context.Background())
	var rows []Row
	op := func(group, name string) {
		if name != "" && !strings.Contains(name, "/") {
			rows = append(rows, Row{Kind: KindOperator, Group: group, ID: name})
		}
	}
	c := m.Components
	for _, g := range []struct {
		name string
		ops  []string
	}{
		{"aggregators", opNames(c.Aggregators)}, {"attributes", opNames(c.Attributes)},
		{"filterers", opNames(c.Filterers)}, {"groupers", opNames(c.Groupers)},
		{"windows", opNames(c.Windows)}, {"features", opNames(c.Features)},
	} {
		for _, n := range g.ops {
			op(g.name, n)
		}
	}
	seen := map[string]bool{}
	for _, t := range append(append([]descriptor.TestMeta(nil), m.Tests...), m.PostTests...) {
		if !seen[t.Name] {
			seen[t.Name] = true
			op("tests", t.Name)
		}
	}
	for _, r := range m.Regressions {
		op("regressions", r.Name)
	}
	for _, x := range m.Matrices {
		op("matrices", x.Name)
	}
	for _, o := range m.Overlays {
		op("overlays", string(o.Kind))
	}
	for _, d := range m.SynthDistributions {
		op("synth distributions", d.Name)
	}
	for _, t := range m.MCPTools {
		rows = append(rows, Row{Kind: KindMCPTool, ID: t.Name})
	}
	for _, ft := range m.CohortTypes {
		rows = append(rows, Row{Kind: KindFieldType, ID: ft.Name})
	}
	for _, code := range m.ErrorCodes {
		rows = append(rows, Row{Kind: KindErrorCode, ID: code})
	}
	return rows
}

func opNames(ops []descriptor.Operator) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.Name
	}
	return out
}

// envPatterns find the PULSE_* names Go source reads from the environment.
var (
	envDirectRe = regexp.MustCompile(`(?:os\.Getenv|os\.LookupEnv|EnvVars)\("(PULSE_[A-Z0-9_]+)"`)
	envConstRe  = regexp.MustCompile(`=\s*"(PULSE_[A-Z0-9_]+)"`)
)

// envVars scans non-test Go source for environment-variable names: a
// direct os.Getenv / os.LookupEnv / cli.EnvVars literal, and a PULSE_*
// constant in a file that reads the environment (the rule
// TestEnvVarsDocumented applies). Build output, testdata and dot or
// scratch directories are skipped.
func envVars(fsys afero.Fs) ([]string, error) {
	var files []string
	err := afero.Walk(fsys, ".", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if info.IsDir() {
			if p != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" ||
				name == "tmp" || name == "docs" || name == "bin" || name == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Read in parallel; the result is a sorted set, so order never leaks.
	var (
		mu   sync.Mutex
		seen = map[string]bool{}
		errs = make([]error, len(files))
		wg   sync.WaitGroup
		next = make(chan int)
	)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				src, err := afero.ReadFile(fsys, files[i])
				if err != nil {
					errs[i] = err
					continue
				}
				found := envNames(string(src))
				mu.Lock()
				for _, v := range found {
					seen[v] = true
				}
				mu.Unlock()
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

// envNames applies the env-var rule to one source file.
func envNames(s string) []string {
	if !strings.Contains(s, "PULSE_") {
		return nil
	}
	var out []string
	for _, m := range envDirectRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	if strings.Contains(s, "os.Getenv") || strings.Contains(s, "os.LookupEnv") {
		for _, m := range envConstRe.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// bookPages reads every markdown page under docs/src except the coverage
// page itself.
func bookPages(fsys afero.Fs) (map[string]string, error) {
	out := map[string]string{}
	err := afero.Walk(fsys, doccheck.BookRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		p = path.Clean(filepath.ToSlash(p))
		if info.IsDir() || !strings.HasSuffix(p, ".md") || p == PagePath {
			return nil
		}
		b, err := afero.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out[p] = string(b)
		return nil
	})
	return out, err
}
