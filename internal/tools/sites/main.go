// Command sites prints the change-site checklist for a Pulse change:
// every file + symbol a change must touch, the gate tests to rerun and
// the goldens to regenerate. It is a development tool only (never
// imported, never shipped) — run it before reading code for a story.
//
//	go run ./internal/tools/sites op MAT_PCA          # an existing operator
//	go run ./internal/tools/sites new-op overlay      # a NEW operator of a category
//	go run ./internal/tools/sites slot request matrices
//	go run ./internal/tools/sites categories          # list categories + operators
//	make sites ARGS="op OVERLAY_MARKOV"
//
// Flags (anywhere on the line): --json (machine output), --all (also
// list history/roadmap files and single-operator sites), --root DIR.
//
// Everything is derived from code, never from a hand-maintained site
// list, so it stays current:
//
//   - operators come from the types registries (types.All*Types,
//     types.AllOverlayKinds, synth.AllDistributions) read by AST;
//   - an operator's sites are the classified occurrences of its
//     identifier (plus every Go const aliasing its wire name, e.g.
//     types.OverlayKindMarkov, plus its atomic-skill stem) across the
//     tracked tree, each resolved to its enclosing Go declaration or
//     Markdown heading;
//   - new-op templates are the sites shared by the existing operators
//     of the category, with per-operator file names folded into
//     <snake> / <kebab> placeholders;
//   - gates are the test functions that name the operator, iterate the
//     category registry, or reference a category-wide table;
//   - slot sites are go/types-resolved references to the types.Request /
//     types.Response field, plus its wire-name string literals and the
//     update-demand rows that name it.
//
// Only the inherently manual part — which contract to read — is a
// table (categoryTable.Manual / slotManual), and its paths are pinned
// by this package's tests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: sites [--json] [--all] [--root DIR] <mode> ...
modes:
  op <TYPE>                       sites of an existing operator (AGG_SUM, MAT_PCA, OVERLAY_MARKOV, normal)
  new-op <category>               template checklist for a NEW operator of a category
  slot <request|response> <name> sites of a Request/Response slot (Go field or json name; unknown = new-slot template)
  categories                      list categories and their operators`

type options struct {
	JSON bool
	All  bool
	Root string
}

func run(args []string, stdout, stderr io.Writer) int {
	var opts options
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch strings.TrimLeft(a, "-") {
		case "json":
			opts.JSON = true
			continue
		case "all":
			opts.All = true
			continue
		case "root":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, usage)
				return 2
			}
			i++
			opts.Root = args[i]
			continue
		case "h", "help":
			fmt.Fprintln(stdout, usage)
			return 0
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "sites: unknown flag %s\n%s\n", a, usage)
			return 2
		}
		pos = append(pos, a)
	}
	if len(pos) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	r, err := openRepo(opts.Root)
	if err != nil {
		fmt.Fprintf(stderr, "sites: %v\n", err)
		return 1
	}
	var rep *Report
	switch pos[0] {
	case "op":
		if len(pos) != 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		rep, err = opReport(r, pos[1], opts)
	case "new-op":
		if len(pos) != 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		rep, err = newOpReport(r, pos[1], opts)
	case "slot":
		if len(pos) != 3 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		rep, err = slotReport(r, pos[1], pos[2], opts)
	case "categories":
		rep, err = categoriesReport(r)
	default:
		fmt.Fprintf(stderr, "sites: unknown mode %q\n%s\n", pos[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "sites: %v\n", err)
		return 1
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(stderr, "sites: %v\n", err)
			return 1
		}
		return 0
	}
	rep.render(stdout)
	return 0
}
