// Command citest is the CI test-job helper (development tool only: never
// imported, never shipped). It has three subcommands.
//
//	go run ./internal/tools/citest heavy-check
//	go run ./internal/tools/citest heavy-run <shard> [go test flags...]
//	go test -json ./... | go run ./internal/tools/citest filter -json-out FILE
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
// filter reads a `go test -json` stream on stdin, copies it to -json-out,
// prints the non-verbose human-readable log (package result lines, plus the
// full output of every failed or unfinished test) and appends the 10 slowest
// top-level tests to $GITHUB_STEP_SUMMARY when that is set. filter never
// fails the job: run it under `set -o pipefail` so go test's status wins.
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
	case "filter":
		filterMain(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "usage: citest heavy-check | heavy-list | heavy-run <shard> [go test flags] | filter -json-out FILE")
	os.Exit(2)
}
