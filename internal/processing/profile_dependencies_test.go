package processing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestProfileDependenciesComplete is the engine-side half of the feature
// dependency gate (the descriptor cannot import the handler maps). It
// checks three things against internal/descriptor's feature table:
//
//	(a) every operator and every host-requiring capability carries at
//	    least one HOST group — the request-executing hosts for plain
//	    operators and request slots, Process alone for Process modes, and
//	    a non-empty subset of the overlay hosts for overlay kinds;
//	(b) every overlay kind's declared host set equals its membership in
//	    the per-host handler maps, in both directions;
//	(c) a source scan: a file that owns operator X (holds the factory or
//	    handler a registry maps X to) and references operator Y — through
//	    Y's types constant or through a reader of Y's component payload —
//	    requires an edge X→Y in the table, or an entry in
//	    dependencyScanAllowlist with a justification.
//
// The scan is mechanical and therefore coarse: it sees identifiers, not
// semantics. An edge expressed only in prose or in a string literal is
// invisible to it, which is why the hard edges are also pinned by name in
// TestFeatureDependenciesResolve (internal/descriptor).
func TestProfileDependenciesComplete(t *testing.T) {
	t.Run("host_groups", testDependencyHostGroups)
	t.Run("overlay_hosts_match_handler_maps", testOverlayHostsMatchHandlerMaps)
	t.Run("handler_reference_scan", testHandlerReferenceScan)
}

var (
	depRequestHosts = []string{"capability:process", "capability:compose", "capability:process_chain"}
	depOverlayHosts = map[string]bool{
		"capability:crosstab":      true,
		"capability:compose":       true,
		"capability:process_chain": true,
		"capability:facet":         true,
	}
)

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func hasGroupEqual(groups [][]string, want []string) bool {
	w := sortedCopy(want)
	for _, g := range groups {
		if reflect.DeepEqual(sortedCopy(g), w) {
			return true
		}
	}
	return false
}

func testDependencyHostGroups(t *testing.T) {
	processModes := map[string]bool{
		"capability:stream":         true,
		"capability:watch":          true,
		"capability:filter_to_file": true,
	}
	requestSlots := map[string]bool{
		"capability:joins":    true,
		"capability:crosstab": true,
	}
	for _, f := range descx.Features() {
		switch {
		case f.Kind == descx.FeatureKindOperator && strings.HasPrefix(f.Name, "OVERLAY_"):
			ok := false
			for _, g := range f.DependsOn {
				all := len(g) > 0
				for _, n := range g {
					if !depOverlayHosts[n] {
						all = false
					}
				}
				if all {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s: no overlay-host dependency group (want any-of a subset of crosstab/compose/process_chain/facet); got %v", f.Name, f.DependsOn)
			}
		case f.Kind == descx.FeatureKindOperator, requestSlots[f.Name]:
			if !hasGroupEqual(f.DependsOn, depRequestHosts) {
				t.Errorf("%s: missing host group any-of %v; got %v", f.Name, depRequestHosts, f.DependsOn)
			}
		case processModes[f.Name]:
			if !hasGroupEqual(f.DependsOn, []string{"capability:process"}) {
				t.Errorf("%s: missing host group {capability:process}; got %v", f.Name, f.DependsOn)
			}
		}
	}
}

// actualOverlayHosts derives each overlay kind's host capabilities from
// the engine's handler maps.
func actualOverlayHosts() map[types.OverlayKind][]string {
	out := map[types.OverlayKind][]string{}
	add := func(k types.OverlayKind, host string) {
		for _, h := range out[k] {
			if h == host {
				return
			}
		}
		out[k] = append(out[k], host)
	}
	for k := range overlayHandlers {
		add(k, "capability:crosstab")
	}
	for k := range composeOverlayHandlers {
		add(k, "capability:compose")
	}
	for k := range composeOverlayMultiLayerHandlers {
		add(k, "capability:compose")
	}
	// The series host is reached only through ApplySeriesOverlays, which
	// capability:compose gates.
	for k := range seriesOverlayHandlers {
		add(k, "capability:compose")
	}
	for k := range chainOverlayHandlers {
		add(k, "capability:process_chain")
	}
	for k := range facetOverlayHandlers {
		add(k, "capability:facet")
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func testOverlayHostsMatchHandlerMaps(t *testing.T) {
	actual := actualOverlayHosts()
	for k := range actual {
		if _, ok := descx.LookupFeature(string(k)); !ok {
			t.Errorf("%s: in a handler map but not a feature row", k)
		}
	}
	for _, k := range types.AllOverlayKinds() {
		got := actual[k]
		if len(got) == 0 {
			t.Errorf("%s: registered overlay kind is in no handler map", k)
		}
		declared := descx.OverlayHostCapabilities(string(k))
		if !reflect.DeepEqual(declared, got) {
			t.Errorf("%s: declared hosts %v, handler maps say %v — update overlayHostKinds in internal/descriptor/features.go", k, declared, got)
		}
		deps, ok := descx.FeatureDependencies(string(k))
		if !ok {
			t.Errorf("%s: not a feature row", k)
			continue
		}
		if len(got) > 0 && !hasGroupEqual(deps, got) {
			t.Errorf("%s: DependsOn %v has no host group equal to the handler-map hosts %v", k, deps, got)
		}
	}
}

// componentReaders names identifiers that read another operator's
// component payload. A file referencing one depends on that operator as
// surely as one naming its constant.
var componentReaders = map[string]string{
	"WelfordTriple":               "AGG_WELFORD", // the AGG_WELFORD rich cell value and its host-view reader
	"HasWelfordCells":             "AGG_WELFORD",
	"extractCellComponentsTriple": "AGG_WELFORD", // {mean, variance, n} off CellComponents
	"encodeSeriesRowAnyMap":       "AGG_WELFORD", // {mean, variance, n} off a series value column
}

// dependencyScanAllowlist records owner→referenced pairs the scan finds
// that are NOT dependencies. Every entry carries its justification.
var dependencyScanAllowlist = map[[2]string]string{
	// overlay_pairwise.go owns all four pairwise kinds; only WELCH_T and
	// TWO_MEANS_Z run the Welford arm (runPairwiseOverlay welford=true).
	{"OVERLAY_PAIRWISE_PROP_Z", "AGG_WELFORD"}:               "shared pairwise file; proportion arm never reads the Welford triple",
	{"OVERLAY_PAIRWISE_PROBIT_T", "AGG_WELFORD"}:             "shared pairwise file; proportion arm never reads the Welford triple",
	{"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z", "AGG_WELFORD"}: "shared pairwise file; weighted arm reads the AGG_WEIGHTED_MEAN moments, never the Welford triple",
	// applyPanelIndexVsRef calls applyIndexVsRef directly per target
	// through a shim spec: code reuse inside the engine, not a dispatch a
	// profile could hide.
	{"OVERLAY_PANEL_INDEX_VS_REF", "OVERLAY_INDEX_VS_REF"}: "direct handler reuse via shim spec, not profile-gated dispatch",
	// applyPropZPanel names the two distinct-key aggregators only in the
	// refusal message of its optional distinct-key n_source mode; the
	// default proportion path reads no particular aggregator, and the
	// admitted set is any-of, never one required name.
	{"OVERLAY_PROP_Z_PANEL", "AGG_DISTINCT_COUNT"}: "named in an error message of an optional n_source mode only",
	{"OVERLAY_PROP_Z_PANEL", "AGG_DISTINCT_SUM"}:   "named in an error message of an optional n_source mode only",
}

// typesOperatorConstants maps every types constant identifier of an
// operator type (AGG/ATTR/FILTER/GROUP/WIN/FEAT/TEST/REG/OVERLAY) to the
// operator name it spells.
func typesOperatorConstants(t *testing.T) map[string]string {
	t.Helper()
	opTypes := map[string]bool{
		"AggregationType": true, "AttributeType": true, "FiltererType": true,
		"GroupType": true, "WindowType": true, "FeatureType": true,
		"TestType": true, "RegressionType": true, "OverlayKind": true,
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, f := range parseDir(t, fset, filepath.Join("..", "..", "types")) {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var curType string
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); ok {
					curType = id.Name
				} else if vs.Type != nil {
					curType = ""
				}
				if !opTypes[curType] {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					if f, ok := descx.LookupFeature(v); ok && f.Kind == descx.FeatureKindOperator {
						out[name.Name] = v
					}
				}
			}
		}
	}
	return out
}

func parseDir(t *testing.T, fset *token.FileSet, dir string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string]*ast.File{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		p := filepath.Join(dir, n)
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		out[p] = f
	}
	return out
}

// typesConst returns the operator name a `types.X` selector spells.
func typesConst(e ast.Expr, consts map[string]string) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "types" {
		return "", false
	}
	v, ok := consts[sel.Sel.Name]
	return v, ok
}

// ownership is one owner unit: the operators a registry maps to one
// factory/handler, and the file declaring it.
type ownership struct {
	file string
	fn   string       // the named package func, when the value names one
	lit  *ast.FuncLit // the inline factory, when the value is a literal
	ops  []string
}

// ownedOperators returns the owner units of one package directory. The
// owner of operator X is the package-level function a registry maps X to
// — through a map literal keyed by a types constant, a
// register(types.X, f) call, or a fooRegistry[types.X] = f assignment
// (f, or the function f(...) calls) — or the inline func literal the
// registry holds. Any other value owns nothing: there is no handler body
// to scan.
func ownedOperators(files map[string]*ast.File, consts map[string]string) []ownership {
	funcFile := map[string]string{}
	funcDecl := map[string]*ast.FuncDecl{}
	for p, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				funcFile[fd.Name.Name] = p
				funcDecl[fd.Name.Name] = fd
			}
		}
	}
	byKey := map[ast.Node]*ownership{}
	var order []ast.Node
	own := func(opName string, value ast.Expr, file string) {
		var key ast.Node
		o := ownership{file: file}
		switch v := value.(type) {
		case *ast.FuncLit:
			key, o.lit = v, v
		case *ast.Ident:
			o.fn = v.Name
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok {
				o.fn = id.Name
			}
		}
		if key == nil {
			p, ok := funcFile[o.fn]
			if !ok {
				return
			}
			o.file = p
			key = funcDecl[o.fn]
		}
		cur := byKey[key]
		if cur == nil {
			cur = &o
			byKey[key] = cur
			order = append(order, key)
		}
		for _, existing := range cur.ops {
			if existing == opName {
				return
			}
		}
		cur.ops = append(cur.ops, opName)
	}
	for p, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				mt, ok := x.Type.(*ast.MapType)
				if !ok {
					return true
				}
				if sel, ok := mt.Key.(*ast.SelectorExpr); !ok || !isTypesSel(sel) {
					return true
				}
				for _, elt := range x.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if name, ok := typesConst(kv.Key, consts); ok {
							own(name, kv.Value, p)
						}
					}
				}
			case *ast.CallExpr:
				id, ok := x.Fun.(*ast.Ident)
				if !ok || id.Name != "register" || len(x.Args) != 2 {
					return true
				}
				if name, ok := typesConst(x.Args[0], consts); ok {
					own(name, x.Args[1], p)
				}
			case *ast.AssignStmt:
				if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
					return true
				}
				ix, ok := x.Lhs[0].(*ast.IndexExpr)
				if !ok {
					return true
				}
				if reg, ok := ix.X.(*ast.Ident); !ok || !strings.HasSuffix(reg.Name, "Registry") {
					return true
				}
				if name, ok := typesConst(ix.Index, consts); ok {
					own(name, x.Rhs[0], p)
				}
			}
			return true
		})
	}
	out := make([]ownership, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// handlerBodies returns the owner function's declaration plus every
// package-level function it reaches through calls declared in the SAME
// file. Staying inside the owner's file keeps the scan on the handler's
// own code: shared dispatch helpers elsewhere switch over every kind and
// would drown the signal.
func handlerBodies(f *ast.File, o ownership) []ast.Node {
	decls := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			decls[fd.Name.Name] = fd
		}
	}
	seen := map[string]bool{}
	var out []ast.Node
	var visit func(ast.Node)
	walk := func(name string) {
		fd, ok := decls[name]
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		visit(fd)
	}
	visit = func(root ast.Node) {
		out = append(out, root)
		ast.Inspect(root, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				walk(id.Name)
			}
			return true
		})
	}
	if o.lit != nil {
		visit(o.lit)
	} else {
		walk(o.fn)
	}
	return out
}

func isTypesSel(sel *ast.SelectorExpr) bool {
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "types"
}

// referencedOperators returns every operator the handler bodies reference
// through a types constant or a component reader. A constant used only as
// a `case` label is skipped: a switch over kinds branches on the operator
// being handled, it does not read another operator's output.
func referencedOperators(fns []ast.Node, consts map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, fn := range fns {
		caseLabels := map[ast.Expr]bool{}
		ast.Inspect(fn, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					caseLabels[e] = true
				}
			}
			return true
		})
		ast.Inspect(fn, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok && caseLabels[e] {
				return false
			}
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if name, ok := typesConst(x, consts); ok {
					out[name] = true
				}
				if op, ok := componentReaders[x.Sel.Name]; ok {
					out[op] = true
				}
			case *ast.Ident:
				if op, ok := componentReaders[x.Name]; ok {
					out[op] = true
				}
			}
			return true
		})
	}
	return out
}

func dependsOnTarget(groups [][]string, target string) bool {
	for _, g := range groups {
		for _, n := range g {
			if n == target {
				return true
			}
		}
	}
	return false
}

// handlerScanDirs are the package directories whose registries own the
// built-in operators.
var handlerScanDirs = []string{".", "window", "feature", "regression"}

func testHandlerReferenceScan(t *testing.T) {
	consts := typesOperatorConstants(t)
	if len(consts) == 0 {
		t.Fatal("found no operator constants in types/ — the scan is blind")
	}
	findings, owned := scanHandlerReferences(t, consts)
	// Every operator must have a scanned handler, or the scan is blind
	// to whatever that operator's code reads.
	for _, f := range descx.Features() {
		if f.Kind == descx.FeatureKindOperator && !owned[f.Name] {
			t.Errorf("%s: no registry entry maps it to a scannable factory/handler — teach ownedOperators the new registration form", f.Name)
		}
	}
	used := map[[2]string]bool{}
	for _, fd := range findings {
		pair := [2]string{fd.owner, fd.target}
		if _, ok := dependencyScanAllowlist[pair]; ok {
			used[pair] = true
			continue
		}
		deps, _ := descx.FeatureDependencies(fd.owner)
		if !dependsOnTarget(deps, fd.target) {
			t.Errorf("%s owns %s and references %s, but %s has no dependency edge to %s: add it to internal/descriptor/features.go hardEdges, or allowlist the pair in dependencyScanAllowlist with a justification",
				fd.file, fd.owner, fd.target, fd.owner, fd.target)
		}
	}
	for pair := range dependencyScanAllowlist {
		if !used[pair] {
			t.Errorf("dependencyScanAllowlist entry %v no longer matches any finding — remove it", pair)
		}
	}
}

type scanFinding struct {
	file, owner, target string
}

func scanHandlerReferences(t *testing.T, consts map[string]string) ([]scanFinding, map[string]bool) {
	t.Helper()
	var out []scanFinding
	owned := map[string]bool{}
	for _, dir := range handlerScanDirs {
		fset := token.NewFileSet()
		files := parseDir(t, fset, dir)
		for _, o := range ownedOperators(files, consts) {
			for _, op := range o.ops {
				owned[op] = true
			}
			refs := referencedOperators(handlerBodies(files[o.file], o), consts)
			mine := map[string]bool{}
			for _, op := range o.ops {
				mine[op] = true
			}
			for _, op := range o.ops {
				for r := range refs {
					if mine[r] {
						continue // a handler referencing the operators it owns
					}
					out = append(out, scanFinding{file: o.file + ":" + o.fn, owner: op, target: r})
				}
			}
		}
	}
	if len(owned) == 0 {
		t.Fatal("scan attributed no operator to any handler — the ownership walk is blind")
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.owner != b.owner {
			return a.owner < b.owner
		}
		return a.target < b.target
	})
	return out, owned
}
