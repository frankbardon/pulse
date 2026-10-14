package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// opReport lists every site of an existing operator.
func opReport(r *repo, name string, opts options) (*Report, error) {
	c, idx, err := r.findOperator(name)
	if err != nil {
		return nil, err
	}
	a := r.analyze(c)
	op := c.Ops[idx]
	rep := &Report{Mode: "op", Target: op.Name}
	rep.Summary = []string{
		fmt.Sprintf("category %s (%d ops, registry %s.%s) · matched as %s", c.Name, len(c.Ops), path.Base(c.Dir), c.AllFunc, strings.Join(op.aliases(c), ", ")),
		"every file below names the operator; Go sites show the enclosing declaration, Markdown the heading",
	}

	files := a.byOp[idx]
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	byClass := map[string][]Site{}
	classes := map[string]bool{}
	var regenPaths, history []string
	testsByDir := map[string][]string{}
	helpers := map[string][]string{}
	listed := map[string]bool{} // path + symbol already a site
	var testPaths, codeDirs []string
	tables := map[string][]Symbol{} // test path -> table vars naming the operator
	for _, p := range paths {
		cl := classify(p)
		classes[cl] = true
		switch cl {
		case "history":
			history = append(history, p)
			if !opts.All {
				continue
			}
		case "golden", "docs-generated":
			regenPaths = append(regenPaths, p)
		}
		syms := r.resolve(p, files[p])
		if cl == "test" {
			testPaths = append(testPaths, p)
		} else if strings.HasSuffix(p, ".go") && !contains(codeDirs, path.Dir(p)) {
			codeDirs = append(codeDirs, path.Dir(p))
		}
		site := Site{Path: p}
		n := normPath(p, op)
		if k := len(a.fileOps[n]); k > 1 {
			site.Coverage = a.cov(k)
			if n != p {
				site.Coverage += " as " + path.Base(n)
			}
		}
		for _, s := range syms {
			sym := Symbol{Name: s.Name, Lines: s.Lines}
			listed[p+"\x00"+s.Name] = true
			if s.CommentOnly {
				sym.Note = "(comment only)"
			}
			if cl == "test" && strings.HasPrefix(s.Name, "var ") && !s.CommentOnly {
				tables[p] = append(tables[p], Symbol{Name: s.Name, Lines: s.Lines})
			}
			if cl == "test" {
				if t := testName(s.Name); t != "" {
					testsByDir[path.Dir(p)] = append(testsByDir[path.Dir(p)], t)
				} else if !s.CommentOnly && s.Name != "(file comment)" {
					helpers[path.Dir(p)] = append(helpers[path.Dir(p)], strings.TrimPrefix(s.Name, "func "))
				}
			}
			site.Symbols = append(site.Symbols, sym)
		}
		if (cl == "test" || cl == "golden" || cl == "docs-generated") && !opts.All {
			// listed under Gates / Regenerate; --all shows them as sites too
			continue
		}
		byClass[cl] = append(byClass[cl], site)
	}
	rep.Sections = sectionsFrom(byClass)
	if len(a.enumerators) > 0 {
		rep.Sections = append(rep.Sections, a.enumeratorSection())
	}

	// Sites that do not spell the name, derived from the ones that do.
	if sec, ok := r.paramEnumSection(op); ok {
		rep.Sections = append(rep.Sections, sec)
	}
	derived, notes := r.derivedSection(a, idx, listed)
	if len(derived.Sites) > 0 {
		rep.Sections = append(rep.Sections, derived)
	}
	rep.Omitted = append(rep.Omitted, notes...)
	if oracle, fixtures := r.oracleSection(testPaths, regenPaths); len(oracle.Sites) > 0 {
		rep.Sections = append(rep.Sections, oracle)
		for _, f := range fixtures {
			if !contains(regenPaths, f) {
				regenPaths = append(regenPaths, f)
			}
		}
	}
	if len(tables) > 0 {
		sec := Section{Class: "test-tables", Title: "Test tables that name it — table-driven probes / fixtures: add the row a sibling operator or a new value needs"}
		var tp []string
		for p := range tables {
			tp = append(tp, p)
		}
		sort.Strings(tp)
		for _, p := range tp {
			sec.Sites = append(sec.Sites, Site{Path: p, Symbols: tables[p]})
		}
		rep.Sections = append(rep.Sections, sec)
	}

	// Category-norm sites (most operators land there) this one lacks.
	var missing []string
	for n, ops := range a.fileOps {
		cl := classify(strings.ReplaceAll(n, "<op>", "x"))
		if ops[idx] || !a.isEvery(len(ops)) || !classByID(cl).Edit {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s [%s] — absent for %s", n, a.cov(len(ops)), op.Name))
	}
	sort.Strings(missing)
	for _, m := range missing {
		rep.Warnings = append(rep.Warnings, "category-norm site missing: "+m)
	}

	rep.Gates = append(rep.Gates, gatesFromDirs("tests that name "+op.Name, testsByDir)...)
	var helperDirs []string
	for d := range helpers {
		helperDirs = append(helperDirs, d)
	}
	sort.Strings(helperDirs)
	for _, d := range helperDirs {
		hs := helpers[d]
		sort.Strings(hs)
		rep.Gates = append(rep.Gates, Gate{Why: "test helpers that name " + op.Name + " (rerun their package)", Package: d,
			Command: goTestCmd(d, nil) + "   # helpers: " + strings.Join(dedupe(hs), ", ")})
	}
	rep.Gates = append(rep.Gates, a.categoryGates(r)...)
	rep.Gates = append(rep.Gates, r.staticGates(classes)...)
	sort.Strings(codeDirs)
	rep.Gates = append(rep.Gates, r.benchGates(codeDirs)...)
	rep.Regenerate = r.regens(regenPaths)
	rep.Manual = manualFor(c.categoryDef)
	if len(history) > 0 && !opts.All {
		rep.Omitted = append(rep.Omitted, fmt.Sprintf("%d history files (docs/roadmap, CHANGELOG) also name %s — pass --all to list them", len(history), op.Name))
	}
	return rep, nil
}
