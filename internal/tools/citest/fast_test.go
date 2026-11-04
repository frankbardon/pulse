package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseFastShards(t *testing.T) {
	t.Parallel()
	got, err := parseFastShards(strings.NewReader("# c\n\n1 .  # root\n2 ./internal/x\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []fastPin{{shard: "1", pkg: ".", line: 3}, {shard: "2", pkg: "./internal/x", line: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"1 . extra\n", "one .\n", "1 internal/x\n", "1\n"} {
		if _, err := parseFastShards(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// TestParseFastMatrix: the fast-shard key is read on its own, and the
// heavy `shard:` matrix parser does not mistake it for its own key even
// when the fast matrix comes first in the file.
func TestParseFastMatrix(t *testing.T) {
	t.Parallel()
	src := []byte("jobs:\n  w:\n    strategy:\n      matrix:\n        fast-shard: [\"1\", '2']\n  h:\n    strategy:\n      matrix:\n        shard: [\"7\", catchall]\n")
	got, err := parseFastMatrix(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fast matrix %v, want %v", got, want)
	}
	heavy, err := parseMatrix(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"7", catchAll}; !reflect.DeepEqual(heavy, want) {
		t.Fatalf("heavy matrix %v, want %v", heavy, want)
	}
	if _, err := parseFastMatrix([]byte("jobs:\n  h:\n    matrix:\n      shard: [\"1\"]\n")); err == nil {
		t.Fatal("want an error without a fast-shard matrix")
	}
}

// checkPartition fails unless plan places every package of pkgs in
// exactly one shard, and nothing else.
func checkPartition(t *testing.T, pkgs []string, plan map[string][]string) {
	t.Helper()
	where := map[string]string{}
	for id, ps := range plan {
		for _, p := range ps {
			if prev, dup := where[p]; dup {
				t.Errorf("%s is in shard %s and shard %s", p, prev, id)
			}
			where[p] = id
		}
	}
	for _, p := range pkgs {
		if _, ok := where[p]; !ok {
			t.Errorf("%s is in no shard", p)
		}
	}
	if len(where) != len(pkgs) {
		t.Errorf("plan holds %d packages, want the %d listed", len(where), len(pkgs))
	}
}

// TestFastPlan_EveryPackageInExactlyOneShard: pins win, every other
// package lands in exactly one shard, and the count balances (shards
// differ by at most one package once the pins are absorbed).
func TestFastPlan_EveryPackageInExactlyOneShard(t *testing.T) {
	t.Parallel()
	var pkgs []string
	for i := range 41 {
		pkgs = append(pkgs, fmt.Sprintf("./p%02d", i))
	}
	pkgs = append(pkgs, ".")
	pins := []fastPin{{shard: "1", pkg: "."}, {shard: "1", pkg: "./p40"}}
	cases := []struct {
		pins   []fastPin
		shards []string
	}{
		{pins, []string{"1", "2"}},
		{append(pins, fastPin{shard: "10", pkg: "./p07"}), []string{"2", "10", "1"}},
	}
	for _, c := range cases {
		plan, err := fastPlan(pkgs, c.pins, c.shards)
		if err != nil {
			t.Fatal(err)
		}
		checkPartition(t, pkgs, plan)
		if len(plan) != len(c.shards) {
			t.Fatalf("plan has %d shards, want %d", len(plan), len(c.shards))
		}
		for _, pin := range c.pins {
			if !contains(plan[pin.shard], pin.pkg) {
				t.Fatalf("shard %s = %v, want the pinned %s", pin.shard, plan[pin.shard], pin.pkg)
			}
		}
		lo, hi := len(pkgs), 0
		for _, ps := range plan {
			lo, hi = min(lo, len(ps)), max(hi, len(ps))
		}
		if hi-lo > 1 {
			t.Fatalf("shard sizes range %d..%d, want within one: %v", lo, hi, plan)
		}
	}
	// Deterministic: the same input gives the same plan.
	a, _ := fastPlan(pkgs, pins, []string{"1", "2"})
	b, _ := fastPlan(append([]string(nil), pkgs...), pins, []string{"2", "1"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("plan depends on matrix order:\n%v\n%v", a, b)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestFastPlan_Refusals(t *testing.T) {
	t.Parallel()
	pkgs := []string{".", "./a", "./b"}
	cases := map[string]struct {
		pins   []fastPin
		shards []string
		want   string
	}{
		"stale":         {[]fastPin{{shard: "1", pkg: "./gone", line: 4}}, []string{"1", "2"}, "./gone is not a package"},
		"duplicate":     {[]fastPin{{shard: "1", pkg: "./a", line: 1}, {shard: "2", pkg: "./a", line: 2}}, []string{"1", "2"}, "already pinned on line 1"},
		"shard missing": {[]fastPin{{shard: "3", pkg: "./a", line: 1}}, []string{"1", "2"}, "shard 3 is not in"},
		"empty matrix":  {nil, nil, "matrix is empty"},
		"dup matrix id": {nil, []string{"1", "1"}, "shard 1 twice"},
		"bad matrix id": {nil, []string{"1", "x"}, `"x" must be a number`},
	}
	for name, c := range cases {
		_, err := fastPlan(pkgs, c.pins, c.shards)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want one containing %q", name, err, c.want)
		}
	}
}

func TestFastArgs(t *testing.T) {
	t.Parallel()
	plan := map[string][]string{"1": {".", "./a"}, "2": {"./b"}}
	got, err := fastArgs(plan, "1", []string{"-count=1", "-json"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"test", "-count=1", "-json", ".", "./a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := fastArgs(plan, "3", nil); err == nil {
		t.Fatal("want an error for an unknown shard")
	}
}

func TestWalkPackages_Fixture(t *testing.T) {
	t.Parallel()
	root := fixtureTree(t)
	writeFile(t, root, "c/c.go", "package c\n")
	writeFile(t, root, "docs/readme.md", "x\n")
	got, err := walkPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".", "./a", "./b", "./c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestRepoFastShardList_Valid: the committed .github/fast-shards.txt and
// the ci.yml test-windows `fast-shard` matrix partition the module's
// packages, each in exactly one shard and no shard empty.
func TestRepoFastShardList_Valid(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	pins, matrix, err := loadFast(root)
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := walkPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) < 50 || !contains(pkgs, ".") || !contains(pkgs, "./internal/tools/citest") {
		t.Fatalf("walked %d packages: walk is broken", len(pkgs))
	}
	plan, err := fastPlan(pkgs, pins, matrix)
	if err != nil {
		t.Fatal(err)
	}
	checkPartition(t, pkgs, plan)
	ids := sortedKeys(plan)
	sort.Strings(ids)
	for _, id := range ids {
		if len(plan[id]) == 0 {
			t.Errorf("fast shard %s is empty", id)
		}
	}
	if len(matrix) < 2 {
		t.Fatalf("fast-shard matrix %v: want at least two shards", matrix)
	}
}
