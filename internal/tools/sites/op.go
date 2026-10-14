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
			if s.CommentOnly {
				sym.Note = "(comment only)"
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
	rep.Regenerate = r.regens(regenPaths)
	rep.Manual = c.Manual
	if len(history) > 0 && !opts.All {
		rep.Omitted = append(rep.Omitted, fmt.Sprintf("%d history files (docs/roadmap, CHANGELOG) also name %s — pass --all to list them", len(history), op.Name))
	}
	return rep, nil
}
