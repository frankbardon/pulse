package io

import (
	"context"
	"fmt"
	"testing"

	"github.com/spf13/afero"
)

// wideOrderLines is a wide join-shaped source: nParent parent attributes
// determined by p_id (12 lines per parent) plus nChild child columns.
func wideOrderLines(rows, nParent, nChild int) ([]string, [][]string) {
	cols := []string{"line_id", "p_id"}
	for k := 0; k < nParent; k++ {
		cols = append(cols, fmt.Sprintf("pa_%02d", k))
	}
	for k := 0; k < nChild; k++ {
		cols = append(cols, fmt.Sprintf("ch_%02d", k))
	}
	out := make([][]string, rows)
	for i := range out {
		p := i / 12
		r := []string{fmt.Sprint(i), fmt.Sprint(100000 + p)}
		for k := 0; k < nParent; k++ {
			r = append(r, fmt.Sprint(70000+(p*(k+3))%50000))
		}
		for k := 0; k < nChild; k++ {
			r = append(r, fmt.Sprint(70000+(i*(k+7))%40000))
		}
		out[i] = r
	}
	return cols, out
}

// BenchmarkImportPredictDetection measures what --suggest-groups costs
// import predict: plain predict (no conversion), the measured pass with
// detection, and the import it previews, over the same source.
func BenchmarkImportPredictDetection(b *testing.B) {
	shapes := []struct {
		name                string
		rows, parent, child int
	}{
		{"cols=12", 100_000, 6, 4},
		{"cols=102", 50_000, 66, 34},
	}
	for _, sh := range shapes {
		cols, rows := wideOrderLines(sh.rows, sh.parent, sh.child)
		run := func(name string, f func(j *ImportJob) error, mutate func(*ImportJob)) {
			b.Run(sh.name+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					j := NewImportJob(newMockReader(cols, rows), "out.pulse")
					j.FS = afero.NewMemMapFs()
					if mutate != nil {
						mutate(j)
					}
					if err := f(j); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		predict := func(j *ImportJob) error { _, err := j.Predict(context.Background()); return err }
		importRun := func(j *ImportJob) error { _, err := j.Run(context.Background()); return err }
		run("predict", predict, nil)
		run("predict_suggest", predict, func(j *ImportJob) { j.SuggestGroups = true })
		run("import", importRun, nil)
	}
}
