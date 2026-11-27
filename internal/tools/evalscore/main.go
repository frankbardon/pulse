// Command evalscore scores an agent evaluation run: every
// <task-id>.json submission in the run directory is executed on fresh
// fixture cohorts and checked against its task's assertions
// (internal/eval). It prints one PASS / FAIL line per task (an
// unanswered task fails) and a total, and exits 1 when any task fails.
// Run it from the repository root; it is a development tool only (never
// shipped).
//
//	go run ./internal/tools/evalscore internal/eval/runs/<date>-r<N>
//	go run ./internal/tools/evalscore -json internal/eval/runs/<date>-r<N>
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/eval"
)

func main() {
	fixtures := flag.String("fixtures", "internal/examples/fixtures", "the example fixture directory (CSVs and schemas/)")
	asJSON := flag.Bool("json", false, "print the reports as a JSON array instead of lines")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: evalscore [-fixtures dir] [-json] <run-dir>")
		os.Exit(2)
	}
	ok, err := run(context.Background(), os.Stdout, *fixtures, flag.Arg(0), *asJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evalscore:", err)
		os.Exit(2)
	}
	if !ok {
		os.Exit(1)
	}
}

// run scores runDir and writes the reports to w; ok is false when any
// task failed.
func run(ctx context.Context, w io.Writer, fixtures, runDir string, asJSON bool) (ok bool, err error) {
	fs := afero.NewOsFs()
	work, err := afero.TempDir(fs, "", "pulse-evalscore-")
	if err != nil {
		return false, err
	}
	defer func() { _ = fs.RemoveAll(work) }()
	reports, err := eval.ScoreRunDir(ctx, fixtures, work, runDir)
	if err != nil {
		return false, err
	}
	passed := 0
	for _, r := range reports {
		if r.Pass {
			passed++
		}
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return false, err
		}
	} else {
		for _, r := range reports {
			if r.Pass {
				fmt.Fprintf(w, "PASS %s\n", r.Task)
				continue
			}
			fmt.Fprintf(w, "FAIL %s\n", r.Task)
			for _, f := range r.Failures {
				fmt.Fprintf(w, "     %s\n", strings.ReplaceAll(f, "\n", "\n     "))
			}
		}
		fmt.Fprintf(w, "%d/%d tasks passed\n", passed, len(reports))
	}
	return passed == len(reports), nil
}
