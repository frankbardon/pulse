package main

import (
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
)

// newOpReport derives the checklist for a NEW operator of a category
// from where the category's existing operators appear.
func newOpReport(r *repo, catName string, opts options) (*Report, error) {
	def, ok := findCategoryDef(catName)
	if !ok {
		var names []string
		for _, c := range categoryTable {
			names = append(names, c.Name)
		}
		return nil, fmt.Errorf("unknown category %q (one of: %s)", catName, strings.Join(names, ", "))
	}
	c, err := r.loadCategory(def)
	if err != nil {
		return nil, err
	}
	a := r.analyze(c)
	ex := c.Ops[a.exemplar]
	rep := &Report{Mode: "new-op", Target: c.Name}
	minSym := int(math.Max(2, math.Ceil(0.25*float64(a.n()))))
	rep.Summary = []string{
		fmt.Sprintf("template from the %d registered %s operators (registry %s.%s); exemplar %s (most sites) — open its lines to copy the shape",
			a.n(), c.Name, path.Base(c.Dir), c.AllFunc, ex.Name),
		"<op> = the operator name minus its prefix, lower-case, joined with '_' or '-' like its neighbours; NN_ = next free example number",
		fmt.Sprintf("[k/%d ops] = how many existing operators land there; every-op sites (>= %d%%) are mandatory, the rest conditional on what the operator does", a.n(), int(everyThreshold*100)),
	}

	var norms []string
	for n, ops := range a.fileOps {
		if len(ops) >= 2 || opts.All {
			norms = append(norms, n)
		}
	}
	sort.Slice(norms, func(i, j int) bool {
		ki, kj := len(a.fileOps[norms[i]]), len(a.fileOps[norms[j]])
		if ki != kj {
			return ki > kj
		}
		return norms[i] < norms[j]
	})
	// symbols per norm path
	symsOf := map[string][]siteKey{}
	for k := range a.symOps {
		symsOf[k.norm] = append(symsOf[k.norm], k)
	}

	byClass := map[string][]Site{}
	classes := map[string]bool{}
	var regenPaths []string
	tableTests := map[string][]string{}
	// a per-operator file of one operator that names another is a
	// cross-reference, not a site the new operator needs
	perOpFile := map[string]bool{}
	for n, m := range a.actual {
		if strings.Contains(n, "<op>") {
			for _, p := range m {
				perOpFile[p] = true
			}
		}
	}
	var single, crossRefs, quietTests int
	for _, ops := range a.fileOps {
		if len(ops) < 2 {
			single++
		}
	}
	for _, n := range norms {
		ops := a.fileOps[n]
		cl := classify(strings.ReplaceAll(n, "<op>", "x"))
		pattern := strings.Contains(n, "<op>")
		share := float64(len(ops)) / float64(a.n())
		switch {
		case cl == "history" && !opts.All:
			continue
		case !pattern && perOpFile[n] && !opts.All:
			crossRefs++
			continue
		}
		if cl == "golden" || cl == "docs-generated" {
			// listed once, under Regenerate
			regenPaths = append(regenPaths, n)
			classes[cl] = true
			continue
		}
		site := Site{Path: n, Coverage: a.cov(len(ops))}
		exOp := pickOp(ops, a.exemplar)
		detailed := pattern || share >= mostThreshold || opts.All
		if pattern {
			site.Note = "e.g. " + a.actual[n][exOp]
			if strings.Contains(path.Base(n), "NN_") {
				site.Note = "next free NN_ = " + r.nextExampleNumber(path.Dir(n)) + "; " + site.Note
			}
		} else if !a.isEvery(len(ops)) {
			site.Note = "conditional — only: " + opList(c, ops, 5)
		}
		keys := symsOf[n]
		sort.Slice(keys, func(i, j int) bool {
			ki, kj := len(a.symOps[keys[i]]), len(a.symOps[keys[j]])
			if ki != kj {
				return ki > kj
			}
			return keys[i].sym < keys[j].sym
		})
		var tableHere []string
		rest := 0
		for _, k := range keys {
			kops := a.symOps[k]
			if cl == "test" && !pattern {
				if t := testName(k.sym); t != "" && float64(len(kops)) >= mostThreshold*float64(a.n()) {
					tableHere = append(tableHere, t)
				}
			}
			if !detailed {
				continue
			}
			if pattern {
				// per-operator file: show the exemplar's own symbols
				if !kops[exOp] {
					continue
				}
			} else if len(kops) < minSym && !opts.All {
				rest++
				continue
			}
			o := pickOp(kops, a.exemplar)
			sym := Symbol{Name: k.sym, Coverage: a.cov(len(kops)), Note: fmt.Sprintf("e.g. %s %s", c.Ops[o].Name, lineRefs(a.symLine[k][o]))}
			if pattern {
				sym.Coverage = ""
				sym.Note = fmt.Sprintf("%s %s", c.Ops[o].Name, lineRefs(a.symLine[k][o]))
			}
			if a.symCmnt[k] {
				sym.Note += " (comment only)"
			}
			site.Symbols = append(site.Symbols, sym)
		}
		if cl == "test" && !pattern {
			if len(tableHere) == 0 && !opts.All {
				quietTests++
				continue
			}
			tableTests[path.Dir(n)] = append(tableTests[path.Dir(n)], tableHere...)
		}
		if rest > 0 {
			site.Symbols = append(site.Symbols, Symbol{Name: fmt.Sprintf("+%d operator-specific symbols (< %d ops each; --all lists them)", rest, minSym)})
		}
		classes[cl] = true
		byClass[cl] = append(byClass[cl], site)
	}
	rep.Sections = sectionsFrom(byClass)
	if len(a.enumerators) > 0 {
		rep.Sections = append(rep.Sections, a.enumeratorSection())
	}
	rep.Gates = append(rep.Gates, gatesFromDirs(fmt.Sprintf("table-driven tests naming >= %d%% of the category — add a row for the new operator", int(mostThreshold*100)), tableTests)...)
	rep.Gates = append(rep.Gates, a.categoryGates(r)...)
	rep.Gates = append(rep.Gates, r.staticGates(classes)...)
	rep.Regenerate = r.regens(regenPaths)
	rep.Manual = manualFor(c.categoryDef)
	if !opts.All {
		rep.Omitted = append(rep.Omitted, fmt.Sprintf("not listed (--all lists them): %d single-operator sites, %d cross-references inside other operators' own files, %d tests naming < %d%% of the category, history files",
			single, crossRefs, quietTests, int(mostThreshold*100)))
	}
	return rep, nil
}

// pickOp prefers the exemplar, else the lowest index present.
func pickOp(ops map[int]bool, exemplar int) int {
	if ops[exemplar] {
		return exemplar
	}
	best := -1
	for i := range ops {
		if best < 0 || i < best {
			best = i
		}
	}
	return best
}

func opList(c *category, ops map[int]bool, n int) string {
	var names []string
	for i := range ops {
		names = append(names, c.Ops[i].Name)
	}
	sort.Strings(names)
	return abbreviate(names, n)
}
