package pulse_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// The U54 structural gate (FR-19): no grouped execution path reads the
// FIRST grouper alone. A plain `groups` list with two or more entries
// once ran only Groups[0] on every path, silently; composite keys now
// run every grouper, and this gate stops a lone first-grouper read from
// creeping back. It walks every production .go file of the module and
// finds each `<x>.Groups[0]` / `groups[0]` index and `Groups[:1]`
// slice. Every site must be allow-listed below — keyed by file and
// enclosing function, with the exact read count and why the read is
// legitimate — and every allow-list entry must still match, so neither
// a new read nor a second read in a listed function slips through.
//
// Legitimate reads are guarded by len(Groups) == 1 (the single-grouper
// fast path), take Groups[0] as the outer position of a loop over every
// grouper, or belong to OVERLAY_YOY, which refuses two or more groupers
// (U46 #429 owns its multi-grouper semantics).

// groupsZeroAllow maps "<slash path>:<func>" to the expected number of
// first-grouper reads there and the reason it is legitimate.
var groupsZeroAllow = map[string]struct {
	reads int
	why   string
}{
	"internal/processing/processor.go:Processor.processStreamingGrouped": {1,
		"grp / keyer key only the single-grouper fast path (the composite branch, len(groupers) > 1, keys every grouper through keyers / CompositeKeySet); grp.Field is otherwise the Run.null_records primary-field fallback, the first-group convention every arm shares"},
	"internal/processing/processor.go:Processor.processGroupedSingle": {1,
		"single-grouper exit: processGrouped routes len(req.Groups) > 1 to processGroupedComposite first"},
	"internal/processing/grouped_tail.go:FinalizeGroupedStream": {1,
		"the non-composite branch (t.KeySet == nil, one grouper) writes the plain key; composite rows write every column from the tuple"},
	"internal/processing/matrix_grouped.go:BuildGroupedMatrices": {1,
		"nil check only (no grouped matrices without a grouper); buckets key off every grouper's composite key"},
	"internal/processing/overlay_series_response.go:promoteYoYFrequencyFromGroupParams": {1,
		"OVERLAY_YOY frequency fallback to its GROUP_DATE grouper's Params; YoY refuses len(Groups) > 1 (applyYoY / validateOverlayYoY)"},
	"internal/descriptor/overlay.go:validateOverlayYoY": {1,
		"after the len(req.Groups) > 1 refusal: the one grouper must be GROUP_DATE"},
	"internal/descriptor/overlay.go:readYoYFrequencyForPredict": {2,
		"OVERLAY_YOY frequency fallback to its GROUP_DATE grouper's Params; YoY refuses len(Groups) > 1"},
}

func TestGroupedPathsReadEveryGrouper(t *testing.T) {
	_, self, _, _ := runtime.Caller(0)
	root := filepath.Dir(self)
	got := map[string]int{}
	var where = map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "contrib", "testdata", "vendor", "node_modules", "docs", "dist", "tmp":
				return filepath.SkipDir
			}
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			site := rel + ":" + funcName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if isFirstGrouperRead(n) {
					got[site]++
					where[site] = append(where[site], fset.Position(n.Pos()).String())
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	sites := make([]string, 0, len(got))
	for s := range got {
		sites = append(sites, s)
	}
	sort.Strings(sites)
	for _, s := range sites {
		allow, ok := groupsZeroAllow[s]
		switch {
		case !ok:
			t.Errorf("%s reads the first grouper alone (%s): run every Request.Groups slot (composite keys), or allow-list the site with why the read is legitimate", s, strings.Join(where[s], ", "))
		case got[s] != allow.reads:
			t.Errorf("%s reads the first grouper %d times, allow-listed for %d (%s): %s", s, got[s], allow.reads, allow.why, strings.Join(where[s], ", "))
		}
	}
	for s := range groupsZeroAllow {
		if got[s] == 0 {
			t.Errorf("allow-list entry %s matches no first-grouper read; drop it", s)
		}
	}
}

// funcName is "Recv.Name" for a method, else "Name".
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if idx, ok := typ.(*ast.IndexExpr); ok {
		typ = idx.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// isFirstGrouperRead reports a `<...>groups[0]` index or a
// `<...>groups[:1]` / `[0:1]` slice, whatever the receiver
// (`req.Groups`, `t.Groups`, a local `groups`).
func isFirstGrouperRead(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.IndexExpr:
		return namesGroups(e.X) && isIntLit(e.Index, "0")
	case *ast.SliceExpr:
		return namesGroups(e.X) && (e.Low == nil || isIntLit(e.Low, "0")) && isIntLit(e.High, "1")
	}
	return false
}

func namesGroups(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.Ident:
		return strings.EqualFold(e.Name, "groups")
	case *ast.SelectorExpr:
		return strings.EqualFold(e.Sel.Name, "groups")
	}
	return false
}

func isIntLit(x ast.Expr, v string) bool {
	lit, ok := x.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == v
}
