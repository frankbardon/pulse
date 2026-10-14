package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// categoryDef names a registry the operators of a category are read
// from. Only the registry location and the manual reading list are
// declared here; the operator list itself is read from code.
type categoryDef struct {
	Name    string
	Aliases []string
	Prefix  string // wire-name prefix ("" for synth distributions)
	Dir     string // package directory declaring the registry function
	AllFunc string // registry function returning every operator
	Skill   string // atomic-skill stem prefix
	Quoted  bool   // wire names are lower-case words: match only as "quoted" strings
	// Manual is the reading list for this category — inherently manual,
	// the one hand-kept part (paths pinned by TestManualPathsExist).
	Manual []string
}

const (
	udIndex     = ".claude/reference/update-demand.md"
	udOperators = ".claude/reference/update-demand/operators-and-guidance.md"
	udWeighting = ".claude/reference/update-demand/weighting-and-multiplicity.md"
	udComps     = ".claude/reference/update-demand/components.md"
	refGuided   = ".claude/reference/guided-analysis.md"
	refFeatures = ".claude/reference/feature-profiles.md"
	refSkills   = ".claude/reference/skill-pack.md"
	refWeight   = ".claude/reference/weighting.md"
	refComps    = ".claude/reference/response-components.md"
)

var opManual = []string{udIndex, udOperators, refGuided, refFeatures, refSkills}

var categoryTable = []categoryDef{
	{Name: "aggregator", Aliases: []string{"agg", "aggregation", "aggregators"}, Prefix: "AGG_", Dir: "types", AllFunc: "AllAggregationTypes", Skill: "op-agg-",
		Manual: append(append([]string{}, opManual...), udWeighting, refWeight, udComps, refComps)},
	{Name: "attribute", Aliases: []string{"attr", "attributes"}, Prefix: "ATTR_", Dir: "types", AllFunc: "AllAttributeTypes", Skill: "op-attr-",
		Manual: append(append([]string{}, opManual...), udWeighting, refWeight, udComps, refComps, ".claude/reference/update-demand/vectors-and-matrices.md")},
	{Name: "filterer", Aliases: []string{"filter", "filterers"}, Prefix: "FILTER_", Dir: "types", AllFunc: "AllFiltererTypes", Skill: "op-filter-",
		Manual: append(append([]string{}, opManual...), udComps, refComps)},
	{Name: "grouper", Aliases: []string{"group", "groupers"}, Prefix: "GROUP_", Dir: "types", AllFunc: "AllGroupTypes", Skill: "op-group-",
		Manual: append(append([]string{}, opManual...), udComps, refComps, ".claude/reference/execution-modes/time-and-defaults.md")},
	{Name: "window", Aliases: []string{"win", "windows"}, Prefix: "WIN_", Dir: "types", AllFunc: "AllWindowTypes", Skill: "op-win-",
		Manual: append(append([]string{}, opManual...), udWeighting, refWeight)},
	{Name: "feature", Aliases: []string{"feat", "features"}, Prefix: "FEAT_", Dir: "types", AllFunc: "AllFeatureTypes", Skill: "op-feat-",
		Manual: append(append([]string{}, opManual...), udWeighting, refWeight)},
	{Name: "test", Aliases: []string{"tests", "stattest"}, Prefix: "TEST_", Dir: "types", AllFunc: "AllTestTypes", Skill: "op-test-",
		Manual: append(append([]string{}, opManual...), udWeighting, refWeight, ".claude/reference/execution-modes/inference.md")},
	{Name: "regression", Aliases: []string{"reg", "regressions"}, Prefix: "REG_", Dir: "types", AllFunc: "AllRegressionTypes", Skill: "op-reg-",
		Manual: append(append([]string{}, opManual...), udWeighting, refWeight, ".claude/reference/matrix-and-vectors/regression-vcov.md")},
	{Name: "matrix", Aliases: []string{"mat", "matrices"}, Prefix: "MAT_", Dir: "types", AllFunc: "AllMatrixTypes", Skill: "op-mat-",
		Manual: append(append([]string{}, opManual...), ".claude/reference/update-demand/vectors-and-matrices.md",
			".claude/reference/matrix-and-vectors.md", ".claude/reference/matrix-and-vectors/matrix-slot.md", udWeighting, refWeight, udComps, refComps)},
	{Name: "overlay", Aliases: []string{"overlays"}, Prefix: "OVERLAY_", Dir: "types", AllFunc: "AllOverlayKinds", Skill: "op-overlay-",
		Manual: append(append([]string{}, opManual...), ".claude/reference/update-demand/overlays.md",
			".claude/reference/execution-modes/overlays.md", ".claude/reference/execution-modes/overlays-matrix-kinds.md", udWeighting, refWeight, udComps, refComps)},
	{Name: "synth", Aliases: []string{"distribution", "synth-distribution", "dist"}, Dir: "internal/synth", AllFunc: "AllDistributions", Skill: "op-synth-", Quoted: true,
		Manual: []string{udIndex, ".claude/reference/update-demand/synth-model.md", ".claude/reference/synthetic-data.md", refFeatures, refSkills}},
}

// operator is one registered operator of a category.
type operator struct {
	Name   string   // wire name (MAT_PCA, normal)
	Consts []string // Go constant identifiers spelling it, when different from Name
	Kebab  string
	Snake  string
	Skill  string // atomic-skill stem
}

type category struct {
	categoryDef
	Ops []operator
}

func findCategoryDef(name string) (categoryDef, bool) {
	n := strings.ToLower(name)
	for _, c := range categoryTable {
		if c.Name == n || contains(c.Aliases, n) {
			return c, true
		}
	}
	return categoryDef{}, false
}

// loadCategory reads a category's operators from its registry function
// by AST: the identifiers listed in the function's slice literal,
// resolved through the package's string constants.
func (r *repo) loadCategory(def categoryDef) (*category, error) {
	consts := map[string]string{} // ident -> value
	var all *ast.FuncDecl
	for _, p := range r.files {
		if path.Dir(p) != def.Dir || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == def.AllFunc {
					all = d
				}
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					for i, id := range vs.Names {
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if v, err := strconv.Unquote(lit.Value); err == nil {
									consts[id.Name] = v
								}
							}
						}
					}
				}
			}
		}
	}
	if all == nil {
		return nil, fmt.Errorf("registry function %s not found in %s/", def.AllFunc, def.Dir)
	}
	c := &category{categoryDef: def}
	seen := map[string]bool{}
	ast.Inspect(all.Body, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, e := range cl.Elts {
			id, ok := e.(*ast.Ident)
			if !ok {
				continue
			}
			v, ok := consts[id.Name]
			if !ok || seen[v] {
				continue
			}
			seen[v] = true
			op := newOperator(def, v)
			if id.Name != v {
				op.Consts = append(op.Consts, id.Name)
			}
			c.Ops = append(c.Ops, op)
		}
		return true
	})
	if len(c.Ops) == 0 {
		return nil, fmt.Errorf("registry function %s lists no constants", def.AllFunc)
	}
	sort.Slice(c.Ops, func(i, j int) bool { return c.Ops[i].Name < c.Ops[j].Name })
	return c, nil
}

func newOperator(def categoryDef, name string) operator {
	snake := strings.ToLower(strings.TrimPrefix(name, def.Prefix))
	kebab := strings.ReplaceAll(snake, "_", "-")
	return operator{Name: name, Snake: snake, Kebab: kebab, Skill: def.Skill + kebab}
}

// findOperator resolves a user-supplied name to its category and
// operator: an exact wire name (case-insensitive), or "synth:<name>".
func (r *repo) findOperator(name string) (*category, int, error) {
	synthOnly := strings.HasPrefix(name, "synth:")
	want := strings.TrimPrefix(name, "synth:")
	for _, def := range categoryTable {
		switch {
		case synthOnly && !def.Quoted:
			continue
		case def.Prefix != "" && !strings.HasPrefix(strings.ToUpper(want), def.Prefix):
			continue
		}
		c, err := r.loadCategory(def)
		if err != nil {
			return nil, 0, err
		}
		for i, op := range c.Ops {
			if strings.EqualFold(op.Name, want) {
				return c, i, nil
			}
		}
	}
	return nil, 0, fmt.Errorf("no registered operator %q (run `sites categories`; for a new one run `sites new-op <category>`)", name)
}

// discoverConstAliases adds every non-test Go constant anywhere in the
// tree whose value is an operator's wire name (types.OverlayKindMarkov,
// metric.ConsumerVecDistance, …) to that operator's Consts.
func (r *repo) discoverConstAliases(c *category) {
	quoted := map[string]int{}
	for i, op := range c.Ops {
		quoted[op.Name] = i
	}
	m := &matcher{quoted: map[string][]int{}}
	for i, op := range c.Ops {
		m.quoted[op.Name] = append(m.quoted[op.Name], i)
	}
	files := map[string]bool{}
	for _, h := range r.scan(m, func(p string) bool {
		return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go")
	}) {
		files[h.Path] = true
	}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		g := r.parse(p)
		if g == nil {
			continue
		}
		for _, d := range g.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				for k, id := range vs.Names {
					if k >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[k].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					if i, ok := quoted[v]; ok && id.Name != v && !contains(c.Ops[i].Consts, id.Name) {
						c.Ops[i].Consts = append(c.Ops[i].Consts, id.Name)
					}
				}
			}
		}
	}
}

// matcherFor builds the token matcher for a set of operator indexes.
func matcherFor(c *category, idx []int) *matcher {
	m := &matcher{words: map[string][]int{}, stems: map[string][]int{}, quoted: map[string][]int{}}
	for _, i := range idx {
		op := c.Ops[i]
		if c.Quoted {
			m.quoted[op.Name] = append(m.quoted[op.Name], i)
		} else {
			m.words[op.Name] = append(m.words[op.Name], i)
		}
		for _, k := range op.Consts {
			m.words[k] = append(m.words[k], i)
		}
		m.stems[op.Skill] = append(m.stems[op.Skill], i)
	}
	return m
}

func (op operator) aliases(c *category) []string {
	var out []string
	if c.Quoted {
		out = append(out, strconv.Quote(op.Name))
	} else {
		out = append(out, op.Name)
	}
	out = append(out, op.Consts...)
	return append(out, op.Skill)
}

func categoriesReport(r *repo) (*Report, error) {
	rep := &Report{Mode: "categories"}
	sec := Section{Class: "categories", Title: "Categories (registry -> operators)"}
	for _, def := range categoryTable {
		c, err := r.loadCategory(def)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, op := range c.Ops {
			names = append(names, op.Name)
		}
		sec.Sites = append(sec.Sites, Site{
			Path: fmt.Sprintf("%s (aliases %s; %s/%s; %d ops)", def.Name, strings.Join(def.Aliases, ", "), def.Dir, def.AllFunc, len(c.Ops)),
			Note: strings.Join(names, " "),
		})
	}
	rep.Sections = append(rep.Sections, sec)
	return rep, nil
}
