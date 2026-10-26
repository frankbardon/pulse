//go:build mmmcoverage

package mmmpulse

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/types"
)

// bayes_coverage_study_test.go is the coverage study's fit loop, behind
// the mmmcoverage build tag (see bayes_coverage_test.go for the design,
// the bar and the report).

// coverageReplicates is the full study's size; coverageFile its
// committed results file.
const coverageReplicates = 50

var coverageFile = filepath.Join("testdata", "coverage-study.md")

var (
	coverageWorkers = flag.Int("mmm.coverage-workers", 1,
		"replicate fits run concurrently (each fit already runs its chains in goroutines); results merge in replicate order")
	coverageN = flag.Int("mmm.coverage-replicates", coverageReplicates,
		"replicates to run; only the full study rewrites the results file unless -mmm.coverage-out is set")
	coverageOut = flag.String("mmm.coverage-out", "",
		"write the report here (default: testdata/coverage-study.md, for the full study only)")
)

// coverageFit fits replicate i and records it.
func coverageFit(t testing.TB, i int) coverageRep {
	rep := coverageRep{dataSeed: uint64(coverageDataSeed0 + i), rngSeed: uint64(i + 1)}
	nc := len(mmmfixture.JointChannels)
	rep.roiIn, rep.contribIn = make([]bool, nc), make([]bool, nc)
	rep.roiErr, rep.roiWidth = make([]float64, nc), make([]float64, nc)

	p, d := jointInstanceFor(t, mmmfixture.JointWithSeed(rep.dataSeed))
	s := jointBayesSpec(t, "")
	s.RNGSeed = int64(rep.rngSeed)
	start := time.Now()
	resp, err := p.Process(t.Context(), jointBayesRequest(s))
	rep.elapsed = time.Since(start)
	if err != nil {
		rep.failed = err.Error()
		return rep
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		rep.failed = mustJSON(t, resp.Regressions)
		return rep
	}
	r := resp.Regressions[0]
	for _, w := range append(append([]*types.ResponseWarning{}, resp.Warnings...), r.Warnings...) {
		reason, _ := w.Details["reason"].(string)
		rep.warnings = append(rep.warnings, w.Code+":"+reason)
	}
	if dg, ok := r.Outputs["diagnostics"].(map[string]any); ok {
		rep.maxRHat, _ = dg["max_rhat"].(float64)
		rep.minESS, _ = dg["min_ess_bulk"].(float64)
		rep.divergences, _ = dg["divergences"].(int)
	}
	roi, _ := r.Outputs["roi"].(map[string]any)
	contrib, _ := r.Outputs["contribution"].(map[string]any)
	for ci, c := range mmmfixture.JointChannels {
		sr, ok1 := roi[c.Name].(map[string]float64)
		sc, ok2 := contrib[c.Name].(map[string]float64)
		if !ok1 || !ok2 {
			rep.failed = "no outputs.roi / outputs.contribution for " + c.Name
			return rep
		}
		truth := d.TrueROI(c.Name)
		total := 0.0
		for _, v := range d.Contribution[c.Name] {
			total += v
		}
		rep.roiIn[ci] = sr["lower"] <= truth && truth <= sr["upper"]
		rep.contribIn[ci] = sc["lower"] <= total && total <= sc["upper"]
		rep.roiErr[ci] = sr["median"]/truth - 1
		rep.roiWidth[ci] = (sr["upper"] - sr["lower"]) / truth
	}
	return rep
}

// TestMMMBayes_CoverageStudy runs the study, writes the results file and
// fails when the overall coverage leaves the band.
func TestMMMBayes_CoverageStudy(t *testing.T) {
	n, workers := *coverageN, max(*coverageWorkers, 1)
	reps := make([]coverageRep, n)
	sem := make(chan struct{}, workers)
	var mu sync.Mutex
	done := 0
	start := time.Now()
	t.Run("replicates", func(t *testing.T) {
		for i := range n {
			t.Run(fmt.Sprintf("r%02d", i), func(t *testing.T) {
				t.Parallel()
				sem <- struct{}{}
				defer func() { <-sem }()
				reps[i] = coverageFit(t, i)
				mu.Lock()
				done++
				t.Logf("replicate %d (data seed %d): %d/%d done, ROI in %v, contribution in %v, %v %s",
					i, reps[i].dataSeed, done, n, reps[i].roiIn, reps[i].contribIn, reps[i].elapsed.Round(time.Millisecond), reps[i].failed)
				mu.Unlock()
			})
		}
	})
	wall := time.Since(start)
	s := summarizeCoverage(reps)
	report := coverageReport(reps, s, wall, workers)
	t.Logf("\n%s", report)

	out := *coverageOut
	if out == "" && n == coverageReplicates {
		out = coverageFile
	}
	if out != "" {
		if err := os.WriteFile(out, report, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", out)
	}
	if !s.pass {
		t.Errorf("coverage outside [%.0f%%, %.0f%%]: ROI %s, contribution %s",
			100*coverageBandLow, 100*coverageBandHigh, pct(s.roiAll, s.trials), pct(s.contribAll, s.trials))
	}
}
