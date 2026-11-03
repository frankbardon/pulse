package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// coverFixture is a module root with packages a and b/c and a floor table.
func coverFixture(t *testing.T, table string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	writeFile(t, root, "a/a.go", "package a\n")
	writeFile(t, root, "b/c/c.go", "package c\n")
	writeFile(t, root, "d/d.go", "package d\n") // never in a profile
	writeFile(t, root, ".github/coverage-floors.txt", table)
	return root
}

// Package a: 10 statements, 8 covered (80%). Package b/c: 4 statements,
// 1 covered (25%). The first a block is listed twice (as -coverpkg=./...
// makes every test binary report it): uncovered in one copy, covered in
// the other, it counts once and as covered.
const coverProfileA = `mode: set
example.com/m/a/a.go:1.1,2.2 6 0
example.com/m/a/a.go:3.1,4.2 2 1
example.com/m/a/a.go:5.1,6.2 2 0
example.com/m/b/c/c.go:1.1,2.2 1 1
example.com/m/b/c/c.go:3.1,4.2 3 0
`

const coverProfileB = `mode: set
example.com/m/a/a.go:1.1,2.2 6 1
`

func TestCoverCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		table    string
		profiles []string // file bodies
		code     int
		out      []string // substrings of stdout
		errOut   string   // substring of stderr
	}{
		{
			name:     "pass",
			table:    "a 80\nb/c 25\n",
			profiles: []string{coverProfileA, coverProfileB},
			code:     0,
			out:      []string{"a 80% 80.00% ok", "b/c 25% 25.00% ok", "all 2 package(s) at or above floor"},
		},
		{
			name:     "miss",
			table:    "a 80.5\nb/c 25\n",
			profiles: []string{coverProfileA, coverProfileB},
			code:     1,
			out:      []string{"a 80.5% 80.00% BELOW", "1 of 2 package(s) below floor"},
		},
		{
			// Without the second profile the duplicated block stays
			// uncovered: a falls to 20%.
			name:     "merge needs every profile",
			table:    "a 80\n",
			profiles: []string{coverProfileA},
			code:     1,
			out:      []string{"a 80% 20.00% BELOW"},
		},
		{
			name:     "missing package",
			table:    "a 80\nd 10\n",
			profiles: []string{coverProfileA, coverProfileB},
			code:     1,
			out:      []string{"d 10% - MISSING", "1 of 2 package(s)"},
		},
		{
			name:     "malformed profile line",
			table:    "a 80\n",
			profiles: []string{"mode: set\nexample.com/m/a/a.go:1.1,2.2 six 1\n"},
			code:     2,
			errOut:   "malformed profile line",
		},
		{
			name:     "profile without mode line",
			table:    "a 80\n",
			profiles: []string{"example.com/m/a/a.go:1.1,2.2 6 1\n"},
			code:     2,
			errOut:   "must start with a `mode:` line",
		},
		{
			name:     "empty profile",
			table:    "a 80\n",
			profiles: []string{""},
			code:     2,
			errOut:   "empty coverage profile",
		},
		{
			name:     "invalid table",
			table:    "a 80\na 90\n",
			profiles: []string{coverProfileA},
			code:     2,
			errOut:   "duplicates line 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := coverFixture(t, tc.table)
			args := []string{"-root", root}
			for i, body := range tc.profiles {
				p := filepath.Join(root, "p"+string(rune('0'+i))+".out")
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					args = append(args, "-profile", p)
				} else {
					args = append(args, p) // trailing args are profiles too
				}
			}
			var stdout, stderr bytes.Buffer
			code := coverCheckMain(args, &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.code, stdout.String(), stderr.String())
			}
			norm := strings.Join(strings.Fields(stdout.String()), " ")
			for _, s := range tc.out {
				if !strings.Contains(norm, s) {
					t.Errorf("stdout missing %q:\n%s", s, stdout.String())
				}
			}
			if tc.errOut != "" && !strings.Contains(stderr.String(), tc.errOut) {
				t.Errorf("stderr missing %q:\n%s", tc.errOut, stderr.String())
			}
		})
	}
}

func TestCoverCheck_NoProfile(t *testing.T) {
	t.Parallel()
	root := coverFixture(t, "a 80\n")
	var stdout, stderr bytes.Buffer
	if code := coverCheckMain([]string{"-root", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestCoverCheck_RootPackage(t *testing.T) {
	t.Parallel()
	root := coverFixture(t, ". 50\n")
	writeFile(t, root, "m.go", "package m\n")
	p := filepath.Join(root, "p.out")
	if err := os.WriteFile(p, []byte("mode: set\nexample.com/m/m.go:1.1,2.2 2 1\nexample.com/m/m.go:3.1,4.2 2 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := coverCheckMain([]string{"-root", root, "-profile", p}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, want 0\n%s%s", code, stdout.String(), stderr.String())
	}
}

func TestCoverProfileFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		flags []string
		want  string
		repl  []string
	}{
		{[]string{"-count=1", "-coverprofile=c.out", "-json"}, "c.out", []string{"-count=1", "-coverprofile=X", "-json"}},
		{[]string{"--coverprofile=c.out"}, "c.out", []string{"--coverprofile=X"}},
		{[]string{"-coverprofile", "c.out", "-json"}, "c.out", []string{"-coverprofile", "X", "-json"}},
		{[]string{"-count=1", "-json"}, "", []string{"-count=1", "-json"}},
		{[]string{"-coverpkg=./..."}, "", []string{"-coverpkg=./..."}},
	}
	for _, tc := range cases {
		got, _ := coverProfileFlag(tc.flags)
		if got != tc.want {
			t.Errorf("coverProfileFlag(%q) = %q, want %q", tc.flags, got, tc.want)
		}
		if r := withCoverProfile(tc.flags, "X"); !reflect.DeepEqual(r, tc.repl) {
			t.Errorf("withCoverProfile(%q) = %q, want %q", tc.flags, r, tc.repl)
		}
	}
}

func TestMergeProfileFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p0 := filepath.Join(dir, "c.out.part0")
	p2 := filepath.Join(dir, "c.out.part2")
	if err := os.WriteFile(p0, []byte(coverProfileA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte(strings.TrimSuffix(coverProfileB, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "c.out")
	// part1 never got written (its go test failed to build): skipped.
	if err := mergeProfileFiles(dst, []string{p0, filepath.Join(dir, "c.out.part1"), p2}, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	// One sorted line per block; the a block listed in both inputs is
	// covered because one copy is.
	want := `mode: set
example.com/m/a/a.go:1.1,2.2 6 1
example.com/m/a/a.go:3.1,4.2 2 1
example.com/m/a/a.go:5.1,6.2 2 0
example.com/m/b/c/c.go:1.1,2.2 1 1
example.com/m/b/c/c.go:3.1,4.2 3 0
`
	if string(data) != want {
		t.Fatalf("merged profile:\n%s\nwant:\n%s", data, want)
	}
	for _, p := range []string{p0, p2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("part %s not removed", p)
		}
	}
	got, err := parseProfiles([]string{dst}, []io.Reader{bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	if c := got["example.com/m/a"]; c != (pkgCover{stmts: 10, covered: 8}) {
		t.Errorf("merged a = %+v, want 8/10", c)
	}

	empty := filepath.Join(dir, "empty.out")
	if err := mergeProfileFiles(empty, nil, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(empty); string(data) != "mode: set\n" {
		t.Errorf("empty shard profile = %q, want a header-only profile", data)
	}

	// cover-merge keeps its inputs and refuses a missing one.
	in := filepath.Join(dir, "in.out")
	if err := os.WriteFile(in, []byte(coverProfileA), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := coverMergeMain([]string{"-o", filepath.Join(dir, "m.out"), in}, &stderr); code != 0 {
		t.Fatalf("cover-merge exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(in); err != nil {
		t.Errorf("cover-merge removed its input: %v", err)
	}
	if code := coverMergeMain([]string{"-o", filepath.Join(dir, "m.out"), filepath.Join(dir, "absent.out")}, &stderr); code != 2 {
		t.Errorf("cover-merge of a missing input: exit %d, want 2", code)
	}
}
