//go:build heavy

package main

import "testing"

// TestGoRecheck_RerunsRealTest reruns one of this package's own tests
// through `go test` (a compile, hence heavy) and reads its time back.
func TestGoRecheck_RerunsRealTest(t *testing.T) {
	t.Parallel()
	const pkg = "github.com/frankbardon/pulse/internal/tools/citest"
	got, err := goRecheck([]timing{{pkg: pkg, test: "TestSlowest_TopTen", secs: 5}})
	if err != nil {
		t.Fatal(err)
	}
	secs, ok := got[pkg+"\x00TestSlowest_TopTen"]
	if !ok || secs >= 1 {
		t.Fatalf("rerun times = %v; want TestSlowest_TopTen under 1s", got)
	}
}
