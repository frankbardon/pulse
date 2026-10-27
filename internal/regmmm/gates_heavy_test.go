//go:build heavy

package regmmm_test

import (
	"os"
	"strings"
	"testing"
)

// TestMMMBayes_NoFMAContraction holds REG_MMM_BAYES's files — production
// and in-package test — to the FMA-free rule with the package's test
// binary cross-compiled for arm64: the pass bar pins sampler draws
// computed through the model, so a contraction anywhere in a bayes*.go
// file would make them arm64-only. TestMMMHill_NoFMAContraction's probe
// proves the flag still reports.
func TestMMMBayes_NoFMAContraction(t *testing.T) {
	t.Parallel()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(wd)
	var bayes []string
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "bayes") && strings.HasSuffix(f.Name(), ".go") {
			bayes = append(bayes, f.Name())
		}
	}
	if len(bayes) < 2 {
		t.Fatalf("found bayes files %v: the gate scans nothing", bayes)
	}
	var hit []string
	for _, l := range fmahashTest(t, wd, "github.com/frankbardon/pulse/internal/regmmm") {
		for _, f := range bayes {
			if strings.Contains(l, f+":") {
				hit = append(hit, l)
			}
		}
	}
	if len(hit) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in the bayes model; wrap each product in float64(...):\n%s",
			len(hit), strings.Join(hit, "\n"))
	}
}
