package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const heavyHdr = "//go:build heavy\n\npackage p\n\nimport \"testing\"\n\n"
const fastHdr = "package p\n\nimport \"testing\"\n\n"

// fixtureTree: root + ./a + ./b packages, a perf-tagged file, a nested
// module and testdata that must all be ignored.
func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "x_heavy_test.go", heavyHdr+"func TestRootSlow(t *testing.T) {}\nfunc TestShared(t *testing.T) {}\n")
	writeFile(t, root, "x_test.go", fastHdr+"func TestRootFast(t *testing.T) {}\nfunc Testlower(t *testing.T) {}\n")
	writeFile(t, root, "a/a_heavy_test.go", heavyHdr+"func TestASlow(t *testing.T) {}\n")
	writeFile(t, root, "a/a_test.go", fastHdr+"func TestShared(t *testing.T) {}\n")
	writeFile(t, root, "b/b_heavy_test.go", heavyHdr+"func TestBSlow(t *testing.T) {}\n")
	writeFile(t, root, "b/perf_test.go", "//go:build perf\n\npackage p\n\nfunc TestPerfOnly(t *testing.T) {}\n")
	writeFile(t, root, "b/norace_test.go", "//go:build !race\n\npackage p\n\nfunc TestNoRace(t *testing.T) {}\n")
	writeFile(t, root, "nested/go.mod", "module nested\n")
	writeFile(t, root, "nested/n_heavy_test.go", heavyHdr+"func TestNested(t *testing.T) {}\n")
	writeFile(t, root, "b/testdata/t_heavy_test.go", heavyHdr+"func TestData(t *testing.T) {}\n")
	writeFile(t, root, ".hidden/h_heavy_test.go", heavyHdr+"func TestHidden(t *testing.T) {}\n")
	return root
}

func names(s testSet) map[string][]string {
	out := map[string][]string{}
	for pkg, set := range s {
		out[pkg] = sortedKeys(set)
	}
	return out
}

func TestScan_HeavyAndAllSets(t *testing.T) {
	t.Parallel()
	tr, err := scan(fixtureTree(t))
	if err != nil {
		t.Fatal(err)
	}
	wantHeavy := map[string][]string{
		".":   {"TestRootSlow", "TestShared"},
		"./a": {"TestASlow"},
		"./b": {"TestBSlow"},
	}
	if got := names(tr.heavy); !reflect.DeepEqual(got, wantHeavy) {
		t.Fatalf("heavy = %v, want %v", got, wantHeavy)
	}
	wantAll := map[string][]string{
		".":   {"TestRootFast", "TestRootSlow", "TestShared"},
		"./a": {"TestASlow", "TestShared"},
		"./b": {"TestBSlow", "TestNoRace"},
	}
	if got := names(tr.all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("all = %v, want %v", got, wantAll)
	}
}

func TestScan_RefusesExampleInHeavyFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "e_heavy_test.go", heavyHdr+"func ExampleSlow() {}\n")
	if _, err := scan(root); err == nil || !strings.Contains(err.Error(), "ExampleSlow") {
		t.Fatalf("err = %v, want an ExampleSlow refusal", err)
	}
}

func TestParseShards(t *testing.T) {
	t.Parallel()
	got, err := parseShards(strings.NewReader("# c\n\n1 . TestA # trailing\n2 ./a TestB\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{{"1", ".", "TestA", 3}, {"2", "./a", "TestB", 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"1 . \n", "catchall . TestA\n", "x . TestA\n"} {
		if _, err := parseShards(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestParseMatrix(t *testing.T) {
	t.Parallel()
	got, err := parseMatrix([]byte("jobs:\n  x:\n    strategy:\n      matrix:\n        shard: [\"1\", '2', catchall]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1", "2", "catchall"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := parseMatrix([]byte("jobs: {}\n")); err == nil {
		t.Fatal("want an error without a shard matrix")
	}
}

func TestPlans_UnlistedHeavyGoesToCatchall(t *testing.T) {
	t.Parallel()
	tr, err := scan(fixtureTree(t))
	if err != nil {
		t.Fatal(err)
	}
	p := plans(tr, []entry{{shard: "1", pkg: ".", name: "TestRootSlow"}})
	if got, want := names(p["1"]), map[string][]string{".": {"TestRootSlow"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shard 1 = %v, want %v", got, want)
	}
	want := map[string][]string{".": {"TestShared"}, "./a": {"TestASlow"}, "./b": {"TestBSlow"}}
	if got := names(p[catchAll]); !reflect.DeepEqual(got, want) {
		t.Fatalf("catchall = %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tr, err := scan(fixtureTree(t))
	if err != nil {
		t.Fatal(err)
	}
	ok := []entry{{shard: "1", pkg: ".", name: "TestRootSlow", line: 1}}
	if probs := validate(tr, ok, []string{"1", catchAll}); len(probs) != 0 {
		t.Fatalf("valid list reported %v", probs)
	}
	cases := map[string]struct {
		entries []entry
		matrix  []string
		want    string
	}{
		"stale":            {[]entry{{shard: "1", pkg: ".", name: "TestRootFast", line: 1}}, []string{"1", catchAll}, "stale entry"},
		"gone":             {[]entry{{shard: "1", pkg: "./zz", name: "TestRootSlow", line: 1}}, []string{"1", catchAll}, "stale entry"},
		"duplicate":        {[]entry{ok[0], {shard: "2", pkg: ".", name: "TestRootSlow", line: 2}}, []string{"1", "2", catchAll}, "already listed on line 1"},
		"shard not in run": {ok, []string{catchAll}, "shard 1 is not in"},
		"no catchall":      {ok, []string{"1"}, "shard catchall is not in"},
		"empty matrix id":  {ok, []string{"1", "9", catchAll}, "matrix shard 9 has no entries"},
	}
	for name, c := range cases {
		probs := validate(tr, c.entries, c.matrix)
		if !strings.Contains(strings.Join(probs, "\n"), c.want) {
			t.Errorf("%s: problems %v, want one containing %q", name, probs, c.want)
		}
	}
}

// TestShardArgs_SplitsWhenANameCollides: catchall owns root TestShared
// (heavy) and ./a TestASlow; ./a also has a FAST TestShared, so one
// `-run ^(TestASlow|TestShared)$ . ./a` would rerun it. The shard must
// split, and each invocation selects exactly what it owns.
func TestShardArgs_SplitsWhenANameCollides(t *testing.T) {
	t.Parallel()
	tr, err := scan(fixtureTree(t))
	if err != nil {
		t.Fatal(err)
	}
	entries := []entry{{shard: "1", pkg: ".", name: "TestRootSlow"}}
	got, err := shardArgs(tr, entries, catchAll, []string{"-count=1"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"test", "-tags", "heavy", "-run", "^(TestBSlow|TestShared)$", "-count=1", ".", "./b"},
		{"test", "-tags", "heavy", "-run", "^(TestASlow)$", "-count=1", "./a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// Exactly-once: every invocation's pattern selects only owned tests.
	owned := plans(tr, entries)[catchAll]
	for _, args := range got {
		pat := args[4]
		for _, pkg := range args[6:] {
			for n := range tr.all[pkg] {
				if strings.Contains(pat, "|"+n+"|") || strings.Contains(pat, "("+n+"|") || strings.Contains(pat, "|"+n+")") || strings.Contains(pat, "("+n+")") {
					if !owned[pkg][n] {
						t.Errorf("%v selects unowned %s %s", args, pkg, n)
					}
				}
			}
		}
	}

	got, err = shardArgs(tr, entries, "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"test", "-tags", "heavy", "-run", "^(TestRootSlow)$", "."}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shard 1: got %v, want %v", got, want)
	}
	if _, err := shardArgs(tr, entries, "7", nil); err == nil {
		t.Fatal("want an error for an unknown shard")
	}
	all := append(entries, entry{shard: "2", pkg: ".", name: "TestShared"}, entry{shard: "2", pkg: "./a", name: "TestASlow"}, entry{shard: "2", pkg: "./b", name: "TestBSlow"})
	if got, err := shardArgs(tr, all, catchAll, nil); err != nil || got != nil {
		t.Fatalf("empty catchall: got %v, %v; want no invocation", got, err)
	}
}

// TestRepoHeavyShardList_Valid: the committed .github/heavy-shards.txt
// matches the tree and the ci.yml test-heavy matrix.
func TestRepoHeavyShardList_Valid(t *testing.T) {
	t.Parallel()
	tr, entries, matrix, err := load(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if probs := validate(tr, entries, matrix); len(probs) > 0 {
		t.Fatalf("heavy shard list invalid:\n%s", strings.Join(probs, "\n"))
	}
	if len(tr.heavy) == 0 {
		t.Fatal("found no heavy tests: scan is broken")
	}
}

const stream = `{"Action":"start","Package":"p/a"}
{"Action":"run","Package":"p/a","Test":"TestOK"}
{"Action":"output","Package":"p/a","Test":"TestOK","Output":"=== RUN   TestOK\n"}
{"Action":"output","Package":"p/a","Test":"TestOK/sub","Output":"ok-noise\n"}
{"Action":"pass","Package":"p/a","Test":"TestOK/sub","Elapsed":0.5}
{"Action":"pass","Package":"p/a","Test":"TestOK","Elapsed":0.7}
{"Action":"output","Package":"p/a","Test":"TestBad","Output":"=== RUN   TestBad\n"}
{"Action":"output","Package":"p/a","Test":"TestBad/sub","Output":"    bad_test.go:9: boom\n"}
{"Action":"fail","Package":"p/a","Test":"TestBad/sub","Elapsed":0.1}
{"Action":"output","Package":"p/a","Test":"TestBad","Output":"--- FAIL: TestBad (2.00s)\n"}
{"Action":"fail","Package":"p/a","Test":"TestBad","Elapsed":2}
{"Action":"skip","Package":"p/a","Test":"TestSkip","Elapsed":9}
{"Action":"output","Package":"p/a","Output":"FAIL\tp/a\t3.0s\n"}
{"Action":"fail","Package":"p/a","Elapsed":3}
not json
{"Action":"output","Package":"p/b","Test":"TestHung","Output":"panic: test timed out\n"}
{"Action":"output","Package":"p/b","Output":"ok  \tp/b\t1.0s\n"}
`

func TestFilter_LogAndSlowest(t *testing.T) {
	t.Parallel()
	var raw, out bytes.Buffer
	report := filter(strings.NewReader(stream), &raw, &out, "unit")
	if raw.String() != stream {
		t.Fatalf("raw copy differs:\n%s", raw.String())
	}
	wantLog := "=== RUN   TestBad\n    bad_test.go:9: boom\n--- FAIL: TestBad (2.00s)\n" +
		"FAIL\tp/a\t3.0s\nnot json\nok  \tp/b\t1.0s\npanic: test timed out\n"
	if out.String() != wantLog {
		t.Fatalf("log:\n%q\nwant\n%q", out.String(), wantLog)
	}
	wantReport := "### unit: 2 slowest tests\n\n| seconds | package | test |\n|---:|---|---|\n" +
		"| 2.00 | p/a | TestBad (FAIL) |\n| 0.70 | p/a | TestOK |\n\n"
	if report != wantReport {
		t.Fatalf("report:\n%q\nwant\n%q", report, wantReport)
	}
}

func TestSlowest_TopTen(t *testing.T) {
	t.Parallel()
	var ts []timing
	for i := 0; i < 12; i++ {
		ts = append(ts, timing{pkg: "p", test: "T" + string(rune('a'+i)), secs: float64(i)})
	}
	r := slowest(ts, 10, "x")
	if !strings.Contains(r, "10 slowest") || !strings.Contains(r, "| 11.00 | p | Tl |") || strings.Contains(r, "| 1.00 |") {
		t.Fatalf("report:\n%s", r)
	}
	if !strings.Contains(slowest(nil, 10, "x"), "No test results") {
		t.Fatal("empty report should say so")
	}
}
