package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestCoverPkgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		table  string
		args   []string
		code   int
		out    string // exact stdout
		errOut string // substring of stderr
	}{
		{
			name:  "table order, root package",
			table: "b/c 25\na 80\n. 50\n",
			code:  0,
			out:   "example.com/m/b/c,example.com/m/a,example.com/m\n",
		},
		{
			name:  "single package",
			table: "# header\na 80 # trailing\n",
			code:  0,
			out:   "example.com/m/a\n",
		},
		{
			name:   "invalid table",
			table:  "a 80\na 90\n",
			code:   2,
			errOut: "duplicates line 1",
		},
		{
			name:   "not a package",
			table:  "nope 80\n",
			code:   2,
			errOut: "nope is not a Go package directory",
		},
		{
			name:   "syntax error",
			table:  "a eighty\n",
			code:   2,
			errOut: "is not a number",
		},
		{
			name:   "stray argument",
			table:  "a 80\n",
			args:   []string{"extra"},
			code:   2,
			errOut: "usage: citest cover-pkgs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := coverFixture(t, tc.table)
			writeFile(t, root, "m.go", "package m\n")
			var stdout, stderr bytes.Buffer
			code := coverPkgsMain(append([]string{"-root", root}, tc.args...), &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.code, stdout.String(), stderr.String())
			}
			if tc.code == 0 && stdout.String() != tc.out {
				t.Errorf("stdout = %q, want %q", stdout.String(), tc.out)
			}
			if tc.errOut != "" && !strings.Contains(stderr.String(), tc.errOut) {
				t.Errorf("stderr missing %q:\n%s", tc.errOut, stderr.String())
			}
		})
	}
}

func TestCoverPkgs_FloorsFlag(t *testing.T) {
	t.Parallel()
	root := coverFixture(t, "a 80\n")
	alt := filepath.Join(root, "alt.txt")
	if err := os.WriteFile(alt, []byte("b/c 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := coverPkgsMain([]string{"-root", root, "-floors", alt}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "example.com/m/b/c\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestCoverLegs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		weights map[string]int64
		n       int
		want    [][]string
	}{
		{
			name:    "one leg takes everything",
			weights: map[string]int64{"./b": 1, ".": 5, "./a": 3},
			n:       1,
			want:    [][]string{{".", "./a", "./b"}},
		},
		{
			// 10 -> leg1; 6 -> leg2; 5 -> leg2 (6 < 10); 4 -> leg1
			// (10 < 11); 1 -> leg2 (11 < 14).
			name:    "heaviest first onto the lightest leg",
			weights: map[string]int64{"./a": 10, "./b": 6, "./c": 5, "./d": 4, "./e": 1},
			n:       2,
			want:    [][]string{{"./a", "./d"}, {"./b", "./c", "./e"}},
		},
		{
			// Equal weights: path order, ties to the lower leg.
			name:    "ties are deterministic",
			weights: map[string]int64{"./z": 2, "./y": 2, "./x": 2, "./w": 2},
			n:       2,
			want:    [][]string{{"./w", "./y"}, {"./x", "./z"}},
		},
		{
			name:    "more legs than packages",
			weights: map[string]int64{"./a": 1},
			n:       3,
			want:    [][]string{{"./a"}, nil, nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := coverLegs(tc.weights, tc.n)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("coverLegs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCoverShard(t *testing.T) {
	t.Parallel()
	// fixtureTree: "." and "./a" have fast test files; "./b" has a
	// heavy file, a perf-only file and a !race file (fast on CI); nested
	// modules, testdata and dot dirs are skipped.
	root := fixtureTree(t)
	writeFile(t, root, "c/c_heavy_test.go", heavyHdr+"func TestOnlyHeavy(t *testing.T) {}\n")
	weights, err := fastTestWeights(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedKeys(weights), []string{".", "./a", "./b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fast-tier packages = %q, want %q", got, want)
	}
	cases := []struct {
		name   string
		args   []string
		code   int
		errOut string
	}{
		{name: "leg 1 of 1", args: []string{"1", "1"}},
		{name: "leg 1 of 2", args: []string{"1", "2"}},
		{name: "leg 2 of 2", args: []string{"2", "2"}},
		{name: "leg 3 of 3", args: []string{"3", "3"}},
		{name: "leg beyond legs", args: []string{"3", "2"}, code: 2, errOut: "want 1 <= leg <= legs"},
		{name: "leg zero", args: []string{"0", "2"}, code: 2, errOut: "want 1 <= leg <= legs"},
		{name: "not a number", args: []string{"one", "2"}, code: 2, errOut: "want 1 <= leg <= legs"},
		{name: "missing legs", args: []string{"1"}, code: 2, errOut: "want <leg> <legs>"},
		{name: "empty leg", args: []string{"4", "4"}, code: 2, errOut: "leg 4 of 4 is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := coverShardMain(append([]string{"-root", root}, tc.args...), &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.code, stdout.String(), stderr.String())
			}
			if tc.errOut != "" && !strings.Contains(stderr.String(), tc.errOut) {
				t.Errorf("stderr missing %q:\n%s", tc.errOut, stderr.String())
			}
		})
	}
	// Every leg count partitions the fast tier: each package in exactly
	// one leg, nothing else printed.
	for legs := 1; legs <= 3; legs++ {
		seen := map[string]int{}
		for leg := 1; leg <= legs; leg++ {
			var stdout, stderr bytes.Buffer
			if code := coverShardMain([]string{"-root", root, itoa(leg), itoa(legs)}, &stdout, &stderr); code != 0 {
				t.Fatalf("leg %d of %d: exit %d: %s", leg, legs, code, stderr.String())
			}
			for _, p := range strings.Fields(stdout.String()) {
				seen[p]++
			}
		}
		want := map[string]int{".": 1, "./a": 1, "./b": 1}
		if !reflect.DeepEqual(seen, want) {
			t.Errorf("%d legs: packages printed %v, want each fast-tier package once %v", legs, seen, want)
		}
	}
}

func itoa(i int) string { return string(rune('0' + i)) }

// TestCoverpkgFlagsDeriveFromFloorTable pins the one-source rule: every
// -coverpkg in the Makefile and the workflows is the `citest cover-pkgs`
// list (held in a COVERPKG variable), never a hand-written pattern, so
// ci.yml's test jobs, cache-warm.yml's compiles (which must match them to
// warm the right cache entries) and `make cover` instrument the same
// packages.
func TestCoverpkgFlagsDeriveFromFloorTable(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	// file -> COVERPKG=$(... cover-pkgs) settings, -coverpkg uses
	want := map[string][2]int{
		"Makefile":                         {1, 1}, // cover
		".github/workflows/ci.yml":         {2, 3}, // coverage-fast (compile + run), test-heavy
		".github/workflows/cache-warm.yml": {1, 2}, // fast + heavy coverage compiles
	}
	for f, n := range want {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		uses, runs := 0, 0
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // comments may name the flags freely
			}
			if strings.Contains(line, "COVERPKG=") && strings.Contains(line, " cover-pkgs)") {
				runs++
			}
			if !strings.Contains(line, "-coverpkg") {
				continue
			}
			uses++
			if !regexp.MustCompile(`-coverpkg="\$\$?COVERPKG"`).MatchString(line) {
				t.Errorf("%s:%d: -coverpkg must be the cover-pkgs list (\"$COVERPKG\"): %s", f, i+1, strings.TrimSpace(line))
			}
		}
		if runs != n[0] {
			t.Errorf("%s: %d COVERPKG=$(citest cover-pkgs) settings, want %d", f, runs, n[0])
		}
		if uses != n[1] {
			t.Errorf("%s: %d -coverpkg uses, want %d", f, uses, n[1])
		}
	}
}
