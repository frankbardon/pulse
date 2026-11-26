package integration_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// refusalWireKeyRules pair the source files whose string literals are
// request-facing refusal prose with the Go spellings that prose must not
// use: a request author writes `params.window`, `ref.margin`,
// `max_iters`, never the Go struct path.
var refusalWireKeyRules = []struct {
	globs []string
	bad   *regexp.Regexp
}{
	{
		globs: []string{"internal/overlayhost/*.go", "internal/processing/overlay*.go", "internal/descriptor/overlay*.go", "internal/service/facet_overlay.go", "internal/formulaident/request_spec.go"},
		bad:   regexp.MustCompile(`Params\[|\bParams\b|\bRef\.[A-Z]|\bRef\b|\b(Composed|Chain|Facet)?Request\.[A-Z]|MaxPanelTargets|Level / Within`),
	},
	{
		globs: []string{"internal/processing/regression/*.go", "internal/regbayes/*.go", "internal/regpenalty/*.go", "internal/regresample/*.go", "internal/reglink/*.go", "internal/regbounds/*.go", "internal/descriptor/predict_regression*.go"},
		bad:   regexp.MustCompile(`\b(MaxIters|L1Ratio|Unpenalized|Selection|Criterion|Resample|PriorShape|PriorRate|PriorMu|PriorPrecision|CredibleLevel|Predictors)\b|\bPenalty\b|\bAlpha\b|requires a Target\b`),
	},
	{
		globs: []string{"internal/processing/refusal_prose.go", "internal/processing/filterer.go", "internal/processing/crosstab.go", "internal/encoding/join_keys.go", "internal/descriptor/join.go"},
		bad:   regexp.MustCompile(`SplitBy|requires a Field\b|Values\[0\]|\bValues entry|Cell aggregation|\bNormalize\b|JoinSpec|OnPair|LeftField|RightField`),
	},
}

// TestRefusalMessagesUseWireKeys: request-facing refusal prose in the
// overlay, regression, test, filter, crosstab and join validators names
// request keys as the request spells them (`params.field`, `ref.margin`,
// `max_iters`, `split_by`), never the Go field path. Only literals with a
// space are prose; identifiers and struct tags are not checked.
func TestRefusalMessagesUseWireKeys(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	for _, rule := range refusalWireKeyRules {
		for _, g := range rule.globs {
			files, err := filepath.Glob(g)
			if err != nil || len(files) == 0 {
				t.Fatalf("glob %s matched nothing (%v): re-point the rule", g, err)
			}
			for _, path := range files {
				if strings.HasSuffix(path, "_test.go") {
					continue
				}
				f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
				if err != nil {
					t.Fatalf("parsing %s: %v", path, err)
				}
				tags := map[*ast.BasicLit]bool{}
				ast.Inspect(f, func(n ast.Node) bool {
					if fl, ok := n.(*ast.Field); ok && fl.Tag != nil {
						tags[fl.Tag] = true
					}
					return true
				})
				ast.Inspect(f, func(n ast.Node) bool {
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING || tags[lit] {
						return true
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil || !strings.Contains(s, " ") {
						return true
					}
					if m := rule.bad.FindString(s); m != "" {
						t.Errorf("%s: refusal prose names the Go spelling %q: %q (use the wire key)", fset.Position(lit.Pos()), m, s)
					}
					return true
				})
			}
		}
	}
}
