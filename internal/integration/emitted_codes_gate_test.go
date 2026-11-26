package integration_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/doccheck"
)

// domainCodeLiteral is a string literal spelled like an error code of
// one of the six domains.
var domainCodeLiteral = regexp.MustCompile(`^(CLI|DATA|ENCODING|PROCESSING|PULSE|SERVICE)_[A-Z0-9_]*[A-Z0-9]$`)

// screamingLiteral is any SCREAMING_SNAKE string literal (one underscore
// or more).
var screamingLiteral = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// cliPackages are the directories whose SCREAMING_SNAKE literals are all
// codes or environment variable names: the CLI leaves write every fatal
// and warning code into an envelope there.
var cliPackages = []string{"internal/cli/", "cmd/pulse/"}

// TestEmittedCodeLiteralsRegistered: every error code a non-test file
// spells as a string literal is registered in errors/codes.go, so
// `pulse errors lookup` and the manifest know every code a caller can
// receive. Two rules:
//
//   - repo-wide, a literal spelled like a domain code
//     (`<DOMAIN>_<NAME>`) is a registered code;
//   - under internal/cli and cmd/pulse, every SCREAMING_SNAKE literal is
//     a registered code (that catches a per-leaf fallback such as
//     `IMPORT_ERROR` passed to an envelope writer).
//
// Exempt: environment variable names (the options.md Environment
// variables table, and `OTEL_*`), the `PULSE_STATS_SKIP_*` pairwise
// tally reasons (a skip-reason vocabulary, never an error) and the
// removed-name denylist (internal/doccheck.RemovedNames).
func TestEmittedCodeLiteralsRegistered(t *testing.T) {
	t.Parallel()
	registered := map[string]bool{}
	for _, c := range perrors.AllCodes() {
		registered[string(c)] = true
	}
	exempt := envVarNames(t)
	for _, d := range doccheck.RemovedNames {
		exempt[d.Name] = true
	}
	exemptName := func(s string) bool {
		return exempt[s] || strings.HasPrefix(s, "PULSE_STATS_SKIP_") || strings.HasPrefix(s, "OTEL_")
	}

	fset := token.NewFileSet()
	for _, path := range findGoFiles(t, ".") {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		slash := filepath.ToSlash(path)
		cli := false
		for _, p := range cliPackages {
			cli = cli || strings.HasPrefix(slash, p)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !cli && !domainCodePrefix.Match(src) {
			continue
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || registered[s] || exemptName(s) {
				return true
			}
			if domainCodeLiteral.MatchString(s) || (cli && screamingLiteral.MatchString(s)) {
				t.Errorf("%s: %q is spelled like an error code but is not registered in errors/codes.go (add the constant, a codeMetadata entry and an owner, or use a registered code)",
					fset.Position(lit.Pos()), s)
			}
			return true
		})
	}
}

// domainCodePrefix pre-filters files: only a file holding a quoted
// domain-prefixed word can hold a domain code literal.
var domainCodePrefix = regexp.MustCompile(`"(CLI|DATA|ENCODING|PROCESSING|PULSE|SERVICE)_`)

// envVarNames returns the variables the options.md Environment variables
// table lists (TestEnvVarsDocumented keeps that table complete).
func envVarNames(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("docs/src/library/options.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, "## Environment variables")
	if start < 0 {
		t.Fatal("docs/src/library/options.md has no Environment variables section")
	}
	section := doc[start+len("## Environment variables"):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile("`(PULSE_[A-Z0-9_]+)`").FindAllStringSubmatch(section, -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("found no PULSE_* variable in the options.md Environment variables table")
	}
	return out
}
