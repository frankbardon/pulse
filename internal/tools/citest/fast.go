package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Fast-tier sharding: the Windows legs split `go test ./...` by package,
// because a cold Windows runner spends most of the job compiling and
// linking ~140 test binaries. Each package runs in exactly one shard: a
// package .github/fast-shards.txt pins goes to its shard, and every other
// package goes to the shard holding the fewest packages so far (ties to
// the lowest id), visiting packages in sorted order. A new package needs
// no entry; pin one only to rebalance.
const (
	fastShardFile  = ".github/fast-shards.txt"
	fastMatrixK    = "fast-shard"
	fastShardsHelp = "`<shard> <package>`"
)

var fastMatrixRe = regexp.MustCompile(`(?m)^\s*` + fastMatrixK + `:\s*\[([^\]]*)\]`)

type fastPin struct {
	shard, pkg string
	line       int
}

func parseFastShards(r io.Reader) ([]fastPin, error) {
	var out []fastPin
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
			return nil, fmt.Errorf("%s:%d: want %s, got %q", fastShardFile, n, fastShardsHelp, sc.Text())
		}
		if !shardIDRe.MatchString(f[0]) {
			return nil, fmt.Errorf("%s:%d: shard id %q must be a number", fastShardFile, n, f[0])
		}
		if f[1] != "." && !strings.HasPrefix(f[1], "./") {
			return nil, fmt.Errorf("%s:%d: package %q must be spelled as go test does (\".\" or \"./dir\")", fastShardFile, n, f[1])
		}
		out = append(out, fastPin{shard: f[0], pkg: f[1], line: n})
	}
	return out, sc.Err()
}

// parseFastMatrix reads the test-windows `fast-shard: [...]` matrix ids.
func parseFastMatrix(src []byte) ([]string, error) {
	m := fastMatrixRe.FindSubmatch(src)
	if m == nil {
		return nil, fmt.Errorf("%s: no `%s: [...]` matrix line", ciWorkflow, fastMatrixK)
	}
	var ids []string
	for _, f := range strings.Split(string(m[1]), ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		if f != "" {
			ids = append(ids, f)
		}
	}
	return ids, nil
}

// numericSort orders shard ids as numbers ("2" before "10").
func numericSort(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.Atoi(ids[i])
		b, _ := strconv.Atoi(ids[j])
		return a < b
	})
}

// fastPlan partitions pkgs over shards (see the package-level comment).
// It refuses a pin naming an absent package, a package pinned twice, a
// pin to a shard the matrix lacks, and a malformed or duplicate shard id.
func fastPlan(pkgs []string, pins []fastPin, shards []string) (map[string][]string, error) {
	if len(shards) == 0 {
		return nil, fmt.Errorf("the %s matrix is empty", fastMatrixK)
	}
	ids := append([]string(nil), shards...)
	plan := map[string][]string{}
	for _, id := range ids {
		if !shardIDRe.MatchString(id) {
			return nil, fmt.Errorf("%s matrix id %q must be a number", fastMatrixK, id)
		}
		if _, dup := plan[id]; dup {
			return nil, fmt.Errorf("%s matrix lists shard %s twice", fastMatrixK, id)
		}
		plan[id] = nil
	}
	numericSort(ids)
	present := map[string]bool{}
	for _, p := range pkgs {
		present[p] = true
	}
	var probs []string
	pinned := map[string]int{}
	for _, p := range pins {
		if prev, ok := pinned[p.pkg]; ok {
			probs = append(probs, fmt.Sprintf("%s:%d: %s already pinned on line %d", fastShardFile, p.line, p.pkg, prev))
			continue
		}
		pinned[p.pkg] = p.line
		if !present[p.pkg] {
			probs = append(probs, fmt.Sprintf("%s:%d: %s is not a package of ./... (stale entry)", fastShardFile, p.line, p.pkg))
			continue
		}
		if _, ok := plan[p.shard]; !ok {
			probs = append(probs, fmt.Sprintf("%s:%d: shard %s is not in the %s %s matrix", fastShardFile, p.line, p.shard, ciWorkflow, fastMatrixK))
			continue
		}
		plan[p.shard] = append(plan[p.shard], p.pkg)
	}
	if len(probs) > 0 {
		return nil, errors.New("fast shard list invalid:\n  " + strings.Join(probs, "\n  "))
	}
	rest := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if _, ok := pinned[p]; !ok {
			rest = append(rest, p)
		}
	}
	sort.Strings(rest)
	for _, p := range rest {
		best := ids[0]
		for _, id := range ids[1:] {
			if len(plan[id]) < len(plan[best]) {
				best = id
			}
		}
		plan[best] = append(plan[best], p)
	}
	for _, id := range ids {
		sort.Strings(plan[id])
	}
	return plan, nil
}

// walkPackages lists the module's package directories the way go test
// spells them: every directory holding a .go file, skipping what
// walkTestFiles skips (testdata, vendor, dot/underscore dirs, nested
// modules). It reads no build constraint, so it can list a directory
// `go list ./...` omits on some platform; fast-run uses go list.
func walkPackages(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		pkg, err := relPkg(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		seen[pkg] = true
		return nil
	})
	return sortedKeys(seen), err
}

func relPkg(root, dir string) (string, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return ".", nil
	}
	return "./" + filepath.ToSlash(rel), nil
}

// listPackages is `go list ./...` from root, spelled as go test does.
func listPackages(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", "./...")
	cmd.Dir = abs
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./...: %v: %s", err, stderr.String())
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkg, err := relPkg(abs, line)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func loadFast(root string) ([]fastPin, []string, error) {
	f, err := os.Open(filepath.Join(root, fastShardFile))
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	pins, err := parseFastShards(f)
	if err != nil {
		return nil, nil, err
	}
	wf, err := os.ReadFile(filepath.Join(root, ciWorkflow))
	if err != nil {
		return nil, nil, err
	}
	matrix, err := parseFastMatrix(wf)
	return pins, matrix, err
}

// fastList prints `<shard> <package>` for every package of ./... .
func fastList(root string, w io.Writer) error {
	pins, matrix, err := loadFast(root)
	if err != nil {
		return err
	}
	pkgs, err := listPackages(root)
	if err != nil {
		return err
	}
	plan, err := fastPlan(pkgs, pins, matrix)
	if err != nil {
		return err
	}
	ids := sortedKeys(plan)
	numericSort(ids)
	for _, id := range ids {
		for _, p := range plan[id] {
			fmt.Fprintf(w, "%s %s\n", id, p)
		}
	}
	return nil
}

// fastArgs is the go test argument list for one shard.
func fastArgs(plan map[string][]string, shard string, flags []string) ([]string, error) {
	pkgs, ok := plan[shard]
	if !ok {
		return nil, fmt.Errorf("unknown fast shard %q", shard)
	}
	args := append([]string{"test"}, flags...)
	return append(args, pkgs...), nil
}

// fastRun runs one fast-tier shard: `go test <flags> <its packages>`.
func fastRun(root, shard string, flags []string) (int, error) {
	pins, matrix, err := loadFast(root)
	if err != nil {
		return 1, err
	}
	pkgs, err := listPackages(root)
	if err != nil {
		return 1, err
	}
	plan, err := fastPlan(pkgs, pins, matrix)
	if err != nil {
		return 1, err
	}
	args, err := fastArgs(plan, shard, flags)
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(os.Stderr, "fast shard %s: %d of %d packages\n", shard, len(plan[shard]), len(pkgs))
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return 1, err
		}
		return ee.ExitCode(), nil
	}
	return 0, nil
}
