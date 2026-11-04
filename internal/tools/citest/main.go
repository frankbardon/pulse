// Command citest is the CI test-job helper (development tool only: never
// imported, never shipped). Its subcommands:
//
//	go run ./internal/tools/citest heavy-check
//	go run ./internal/tools/citest heavy-run <shard> [go test flags...]
//	go run ./internal/tools/citest fast-list
//	go run ./internal/tools/citest fast-run <shard> [go test flags...]
//	go run ./internal/tools/citest parallel-check
//	go run ./internal/tools/citest fuzz-check
//	go run ./internal/tools/citest cover-check [-root DIR] [-floors FILE] -profile FILE [-profile FILE ...]
//	go run ./internal/tools/citest cover-merge -o OUT IN...
//	go run ./internal/tools/citest cover-pkgs [-root DIR] [-floors FILE]
//	go run ./internal/tools/citest cover-shard [-root DIR] <leg> <legs>
//	go test -json ./... | go run ./internal/tools/citest filter [-json-out FILE] [-budget 1s [-recheck] [-report-only]]
//
// heavy-check validates the heavy-tier shard list (.github/heavy-shards.txt)
// against the tree and the ci.yml test-heavy matrix: no stale or duplicate
// entry, and every shard id (plus "catchall") in the matrix and vice versa.
// Heavy tests the list does not name belong to the reserved "catchall"
// shard, so the union of all shards is the whole heavy tier by construction.
//
// heavy-run runs one shard: `go test -tags heavy -run '^(A|B)$' <flags>
// <packages>`, split into several invocations when a name it owns in one
// package also names a test it does not own in another (so each heavy test
// runs in exactly one shard). It exits with the first failing status; an
// empty shard exits 0. heavy-list prints `<shard> <package> <Test>` for
// every heavy test.
//
// fast-run runs one fast-tier shard (the test-windows legs): `go test
// <flags> <packages>` over the shard's share of `go list ./...`. Every
// package lands in exactly one shard: .github/fast-shards.txt pins a few
// expensive packages, and each other package goes to the shard holding the
// fewest packages so far. Shard ids are the ci.yml `fast-shard:` matrix.
// fast-list prints `<shard> <package>` for every package.
//
// filter reads a `go test -json` stream on stdin, copies it to -json-out,
// prints the non-verbose human-readable log (package result lines, plus the
// full output of every failed or unfinished test) and appends the 10 slowest
// top-level tests to $GITHUB_STEP_SUMMARY when that is set. Without
// -budget (or with -report-only) filter never fails the job: run it under
// `set -o pipefail` so go test's status wins. -budget D lists every
// top-level test that took D or longer plus the slowest five with their
// margin, and (unless -report-only) exits 1 on an offender or a failed test;
// -recheck reruns each offender alone and clears it when that run is under
// D. `make test` enforces -budget 1s -recheck on the fast tier. A
// top-level test's time is its Elapsed plus the time of each subtest that
// called t.Parallel() (recursively): go test stops a parent's clock before
// its paused subtests run, so their cost is charged to it here.
//
// heavy-run passes -coverprofile through, but writes one part per go test
// invocation and merges them (as cover-merge does) into the named file (an
// empty shard gets a header-only profile), so a shard always yields one.
//
// cover-check is the per-package coverage gate. It merges one or more
// -coverprofile files (a block repeated across test binaries or runs
// counts once, covered if any copy is), computes each package's statement
// coverage, and compares it with the floor table
// (.github/coverage-floors.txt, parsed by ./coverfloors). It prints a
// table (and a markdown copy to $GITHUB_STEP_SUMMARY) and exits 1 when a
// package is below its floor or absent from the profiles, 2 on a usage,
// table or profile error. The profiles must come from runs of both tiers
// instrumented with -coverpkg=$(citest cover-pkgs): `make cover`, CI's
// coverage job.
//
// cover-merge dedupes one or more profiles into one `mode: set` profile
// (one line per block, covered = 1). A -coverpkg profile repeats every
// block once per test binary (~500 MB for the fast tier); merged it is a
// few MB, which is what CI's test jobs hand to the coverage job.
//
// cover-pkgs prints the -coverpkg value every coverage run uses (make
// cover, ci.yml, cache-warm.yml): the floor table's packages as
// comma-separated import paths. Only the floored packages are
// instrumented, yet every test binary still records them, so a package
// tested from elsewhere (encoding) keeps its cross-package figure.
//
// cover-shard prints leg <leg> of <legs> of the fast tier's packages, one
// per line, for CI's coverage-fast matrix: every package with a fast-tier
// test file, split heaviest-first (by test-file bytes) onto the lightest
// leg, so the legs partition the fast tier and depend only on the tree.
//
// parallel-check is the parallel-by-default gate (TestTestsParallelByDefault):
// every top-level test calls t.Parallel() or carries "// serial: <reason>".
//
// fuzz-check keeps .github/workflows/fuzz.yml in sync with the tree
// (TestRepoFuzzMatrix_InSync): every `func FuzzXxx(f *testing.F)` in the
// root module has exactly one matrix leg and every leg names a real target.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	code := 0
	switch os.Args[1] {
	case "heavy-check":
		err = heavyCheck(".")
		if err != nil {
			code = 1
		}
	case "heavy-run":
		if len(os.Args) < 3 {
			usage()
		}
		code, err = heavyRun(".", os.Args[2], os.Args[3:])
	case "heavy-list":
		err = heavyList(".", os.Stdout)
		if err != nil {
			code = 1
		}
	case "parallel-check":
		code, err = parallelCheckMain(".", os.Stdout)
	case "fuzz-check":
		err = fuzzCheck(".", os.Stdout)
	case "fast-list":
		err = fastList(".", os.Stdout)
		if err != nil {
			code = 1
		}
	case "fast-run":
		if len(os.Args) < 3 {
			usage()
		}
		code, err = fastRun(".", os.Args[2], os.Args[3:])
	case "filter":
		code = filterMain(os.Args[2:], os.Stdin, os.Stdout)
	case "cover-check":
		code = coverCheckMain(os.Args[2:], os.Stdout, os.Stderr)
	case "cover-merge":
		code = coverMergeMain(os.Args[2:], os.Stderr)
	case "cover-pkgs":
		code = coverPkgsMain(os.Args[2:], os.Stdout, os.Stderr)
	case "cover-shard":
		code = coverShardMain(os.Args[2:], os.Stdout, os.Stderr)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "citest:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: citest heavy-check | heavy-list | heavy-run <shard> [go test flags] | fast-list | fast-run <shard> [go test flags] | parallel-check | fuzz-check | filter [-json-out FILE] [-budget 1s [-recheck] [-report-only]] | cover-check [-root DIR] [-floors FILE] -profile FILE... | cover-merge -o OUT IN... | cover-pkgs [-root DIR] [-floors FILE] | cover-shard [-root DIR] <leg> <legs>")
	os.Exit(2)
}
