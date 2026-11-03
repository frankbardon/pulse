// Package coverfloors parses and validates the per-package coverage floor
// table (.github/coverage-floors.txt). The table is the ONE source of the
// floors: `citest cover-check` (make cover, CI's coverage job) measures
// against it and TestPerPackageCoverageFloors validates it structurally.
// Development tooling only: never imported by shipped code.
package coverfloors

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// File is the floor table's path relative to the module root.
const File = ".github/coverage-floors.txt"

// Floor is one table line: a package directory relative to the module root
// (slash-separated, e.g. "internal/io/csv") and its minimum statement
// coverage in percent.
type Floor struct {
	Dir     string
	Percent float64
	Line    int
}

// Parse reads the table: one `<package-dir> <percent>` per line; blank
// lines and `#` comments are ignored. Parse reports syntax errors only;
// Validate checks the content.
func Parse(r io.Reader) ([]Floor, error) {
	var out []Floor
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("%s:%d: want `<package-dir> <percent>`, got %q", File, n, sc.Text())
		}
		p, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: floor %q is not a number", File, n, f[1])
		}
		out = append(out, Floor{Dir: f[0], Percent: p, Line: n})
	}
	return out, sc.Err()
}

// Load parses the table under root.
func Load(root string) ([]Floor, error) {
	f, err := os.Open(filepath.Join(root, File))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Validate returns one problem per structural fault: an empty table, a
// floor outside 0..100, a duplicate or non-canonical package dir, or a dir
// under root that is not a Go package (no non-test .go file).
func Validate(root string, floors []Floor) []string {
	var probs []string
	if len(floors) == 0 {
		probs = append(probs, File+": no floors")
	}
	seen := map[string]int{}
	for _, f := range floors {
		at := fmt.Sprintf("%s:%d", File, f.Line)
		if f.Percent < 0 || f.Percent > 100 {
			probs = append(probs, fmt.Sprintf("%s: %s floor %v outside 0..100", at, f.Dir, f.Percent))
		}
		if prev, ok := seen[f.Dir]; ok {
			probs = append(probs, fmt.Sprintf("%s: %s duplicates line %d", at, f.Dir, prev))
			continue
		}
		seen[f.Dir] = f.Line
		if f.Dir != filepath.ToSlash(filepath.Clean(f.Dir)) || f.Dir == ".." || strings.HasPrefix(f.Dir, "../") || filepath.IsAbs(f.Dir) {
			probs = append(probs, fmt.Sprintf("%s: %q is not a clean module-relative package dir", at, f.Dir))
			continue
		}
		if !isPackage(filepath.Join(root, filepath.FromSlash(f.Dir))) {
			probs = append(probs, fmt.Sprintf("%s: %s is not a Go package directory", at, f.Dir))
		}
	}
	return probs
}

func isPackage(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			return true
		}
	}
	return false
}
