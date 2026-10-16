// Command semfit imports a CSV into a Pulse cohort and fits a SEM over
// it through the MAT_LAB_SEM extension operator, printing the matrix
// result as JSON.
//
//	semfit -csv data.csv -model model.txt [-solver slsqp|fisher] [-information dense|rank2] [-ftol F] [-max-iter N] [-grad-tol G]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/experiments/sem/sem"
	"github.com/frankbardon/pulse/types"
)

func main() {
	csvPath := flag.String("csv", "", "source CSV (header row, one column per variable)")
	modelPath := flag.String("model", "", "model description file (semopy / lavaan syntax)")
	dir := flag.String("cohort-dir", "", "data directory for the imported cohort (default: a temp dir)")
	solver := flag.String("solver", "", "slsqp (default, semopy's SciPy SLSQP) | fisher")
	information := flag.String("information", "", "dense (default) | rank2")
	ftol := flag.Float64("ftol", 0, "SLSQP ftol (default 1e-6)")
	maxIter := flag.Int("max-iter", 0, "iteration cap (default 10000 slsqp, 500 fisher)")
	gradTol := flag.Float64("grad-tol", 0, "Fisher-scoring gradient tolerance (default 1e-10)")
	flag.Parse()
	opts = map[string]any{}
	if *solver != "" {
		opts["solver"] = *solver
	}
	if *information != "" {
		opts["information"] = *information
	}
	if *ftol > 0 {
		opts["ftol"] = *ftol
	}
	if *maxIter > 0 {
		opts["max_iter"] = *maxIter
	}
	if *gradTol > 0 {
		opts["grad_tol"] = *gradTol
	}
	if *csvPath == "" || *modelPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*csvPath, *modelPath, *dir); err != nil {
		fmt.Fprintln(os.Stderr, "semfit:", err)
		os.Exit(1)
	}
}

var opts map[string]any

func run(csvPath, modelPath, dir string) error {
	desc, err := os.ReadFile(modelPath)
	if err != nil {
		return err
	}
	if dir == "" {
		if dir, err = os.MkdirTemp("", "semfit"); err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	}
	src, err := os.ReadFile(csvPath)
	if err != nil {
		return err
	}
	name := filepath.Base(csvPath)
	if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
		return err
	}
	p, err := pulse.New(pulse.Options{
		DataDir:    dir,
		Extensions: pulse.Extensions{MatrixOps: []pulse.MatrixOpRegistration{sem.Registration()}},
	})
	if err != nil {
		return err
	}
	ctx := context.Background()
	imp, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: name})
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	model, err := sem.NewModel(string(desc), false)
	if err != nil {
		return err
	}
	opts["model"] = string(desc)
	params, _ := json.Marshal(opts)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: imp.Path},
		Matrices: []types.MatrixSpec{{
			Type:   sem.TypeName,
			Name:   "sem",
			Fields: model.Observed,
			Params: params,
		}},
	}
	start := time.Now()
	resp, err := p.Process(ctx, req)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	if len(resp.Matrices) != 1 {
		return fmt.Errorf("expected one matrix result, got %d", len(resp.Matrices))
	}
	out, err := types.MarshalFinite(map[string]any{
		"seconds":  elapsed.Seconds(),
		"rows":     imp.RowsImported,
		"result":   resp.Matrices[0],
		"warnings": resp.Warnings,
	})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(out, '\n'))
	return err
}
