package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/internal/tools/citest/coverfloors"
)

// loadFloors parses and validates the floor table (default
// <root>/.github/coverage-floors.txt).
func loadFloors(root, path string) ([]coverfloors.Floor, error) {
	if path == "" {
		path = filepath.Join(root, coverfloors.File)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	floors, err := coverfloors.Parse(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if probs := coverfloors.Validate(root, floors); len(probs) > 0 {
		sort.Strings(probs)
		return nil, fmt.Errorf("floor table invalid:\n  %s", strings.Join(probs, "\n  "))
	}
	return floors, nil
}

// coverPkgList is the -coverpkg value: every floored package as an import
// path, in table order, comma-separated. Instrumenting only the floored
// packages (not ./...) keeps cross-package attribution for exactly the
// packages cover-check measures — every test binary still records them —
// while sparing the rest of the tree the instrumentation.
func coverPkgList(module string, floors []coverfloors.Floor) string {
	out := make([]string, 0, len(floors))
	for _, f := range floors {
		if f.Dir == "." {
			out = append(out, module)
		} else {
			out = append(out, module+"/"+f.Dir)
		}
	}
	return strings.Join(out, ",")
}

// coverPkgsMain implements `citest cover-pkgs [-root DIR] [-floors FILE]`:
// it prints the -coverpkg list (coverPkgList) on one line.
func coverPkgsMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cover-pkgs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "module root")
	floorsPath := fs.String("floors", "", "floor table (default <root>/"+coverfloors.File+")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: citest cover-pkgs [-root DIR] [-floors FILE]")
		return 2
	}
	floors, err := loadFloors(*root, *floorsPath)
	if err != nil {
		fmt.Fprintln(stderr, "citest cover-pkgs:", err)
		return 2
	}
	module, err := modulePath(*root)
	if err != nil {
		fmt.Fprintln(stderr, "citest cover-pkgs:", err)
		return 2
	}
	fmt.Fprintln(stdout, coverPkgList(module, floors))
	return 0
}

// fastTestWeights maps every package with a fast-tier test file (one the
// CI runner compiles without -tags heavy) to the total bytes of those
// files: a tree-derived proxy for its share of the fast tier's compile
// and run cost. Keys are spelled as go test takes them ("." or "./x").
func fastTestWeights(root string) (map[string]int64, error) {
	w := map[string]int64{}
	err := walkTestFiles(root, func(path string) error {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		x, err := buildExpr(src)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !tagged(x, false) {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "."
		if rel != "." {
			pkg = "./" + filepath.ToSlash(rel)
		}
		w[pkg] += int64(len(src))
		return nil
	})
	return w, err
}

// coverLegs partitions the packages into n legs, heaviest first onto the
// currently lightest leg (ties: the lower leg; packages of equal weight in
// path order), so every package lands in exactly one leg and the result
// depends only on the tree. Each leg's packages are returned sorted.
func coverLegs(weights map[string]int64, n int) [][]string {
	pkgs := make([]string, 0, len(weights))
	for p := range weights {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		if weights[pkgs[i]] != weights[pkgs[j]] {
			return weights[pkgs[i]] > weights[pkgs[j]]
		}
		return pkgs[i] < pkgs[j]
	})
	legs := make([][]string, n)
	load := make([]int64, n)
	for _, p := range pkgs {
		best := 0
		for i := 1; i < n; i++ {
			if load[i] < load[best] {
				best = i
			}
		}
		legs[best] = append(legs[best], p)
		load[best] += weights[p]
	}
	for _, l := range legs {
		sort.Strings(l)
	}
	return legs
}

// coverShardMain implements `citest cover-shard [-root DIR] <leg> <legs>`:
// it prints leg <leg> (1-based) of the fast tier's packages split into
// <legs> legs (coverLegs), one package per line, for CI's coverage-fast
// matrix. The legs partition every package that has a fast-tier test
// file, so their union is the fast tier of `go test ./...`.
func coverShardMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cover-shard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "module root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usageErr := func(msg string) int {
		fmt.Fprintln(stderr, "citest cover-shard:", msg)
		fmt.Fprintln(stderr, "usage: citest cover-shard [-root DIR] <leg> <legs>")
		return 2
	}
	if fs.NArg() != 2 {
		return usageErr("want <leg> <legs>")
	}
	leg, err1 := strconv.Atoi(fs.Arg(0))
	legs, err2 := strconv.Atoi(fs.Arg(1))
	if err1 != nil || err2 != nil || legs < 1 || leg < 1 || leg > legs {
		return usageErr(fmt.Sprintf("leg %q of %q: want 1 <= leg <= legs", fs.Arg(0), fs.Arg(1)))
	}
	weights, err := fastTestWeights(*root)
	if err != nil {
		fmt.Fprintln(stderr, "citest cover-shard:", err)
		return 2
	}
	if len(weights) == 0 {
		fmt.Fprintln(stderr, "citest cover-shard: no fast-tier test files under", *root)
		return 2
	}
	parts := coverLegs(weights, legs)
	if len(parts[leg-1]) == 0 {
		fmt.Fprintf(stderr, "citest cover-shard: leg %d of %d is empty (more legs than packages)\n", leg, legs)
		return 2
	}
	for _, p := range parts[leg-1] {
		fmt.Fprintln(stdout, p)
	}
	return 0
}
