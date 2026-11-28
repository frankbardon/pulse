package pulse

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsssidecar"
)

// U51 E4-S3: a filter-to-file output that REPLACES a cohort reports the
// sidecars the replacement invalidated — every per-shard sidecar of an
// archive index — exactly as a CohortBuilder ShardSplit + Overwrite
// does (TestArchiveIndexUpkeep_OverwriteNamesEveryShardSidecar), on the
// archive arm, the flatten arm and the dedup-aware request form.

// filterSidecarFixture is indexedSetArchive (sets.pulse, two shards,
// indexed on id) plus src.pulse, the same rows unindexed, and the
// per-shard sidecars sets.pulse's index names.
func filterSidecarFixture(t *testing.T, ext Extensions) (*Pulse, []StaleSidecar) {
	t.Helper()
	p := indexedSetArchive(t)
	label := func(i int) string { return string(rune('a' + i)) }
	var rows []CohortRow
	for i := range 8 {
		rows = append(rows, CohortRow{uint64(i), []string{label(i)}})
	}
	buildCohort(t, p, "src.pulse", setSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 4}}, rows)
	want, err := p.InvalidatedSidecars(context.Background(), "sets.pulse")
	if err != nil || len(want) != 2 {
		t.Fatalf("InvalidatedSidecars = %+v, %v; want the two shard sidecars", want, err)
	}
	if len(ext.ExprFunctions) > 0 {
		q, err := New(Options{FS: p.fsys, Extensions: ext})
		if err != nil {
			t.Fatal(err)
		}
		p = q
	}
	return p, want
}

func TestFilterToFile_ReplacingAnIndexedArchiveReportsSidecars(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, flatten := range []bool{false, true} {
		name := map[bool]string{false: "archive_arm", true: "flatten"}[flatten]
		t.Run(name, func(t *testing.T) {
			p, want := filterSidecarFixture(t, Extensions{})
			rep, err := p.FilterToFileWithOptions(ctx, "src.pulse", "sets.pulse", FilterToFileOptions{Expression: "id >= 2", Flatten: flatten})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Records != 6 || rep.Flattened != flatten || len(rep.Warnings) != 0 {
				t.Fatalf("report = %+v", rep)
			}
			if sidecarJSON(t, rep.InvalidatedSidecars) != sidecarJSON(t, want) {
				t.Errorf("InvalidatedSidecars = %s\nwant %s", sidecarJSON(t, rep.InvalidatedSidecars), sidecarJSON(t, want))
			}
		})
	}

	t.Run("fresh_output_reports_nothing", func(t *testing.T) {
		p, _ := filterSidecarFixture(t, Extensions{})
		rep, err := p.FilterToFileWithOptions(ctx, "sets.pulse", "new.pulse", FilterToFileOptions{Flatten: true})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Records != 8 || !rep.Flattened || len(rep.InvalidatedSidecars) != 0 {
			t.Fatalf("report = %+v, want 8 flattened records and no sidecars", rep)
		}
	})

	t.Run("request_replace", func(t *testing.T) {
		// A predicate running embedder code is never reused: it re-runs
		// and replaces the named output.
		p, want := filterSidecarFixture(t, Extensions{ExprFunctions: []ExprFunction{{
			Name: "ftf_min", Fn: func() float64 { return 2 },
		}}})
		res, err := p.FilterToFileWithRequest(ctx, &FilterToFileRequest{
			SourcePath: "src.pulse", Expression: "id >= ftf_min()", OutputDir: ".", OutputName: "sets.pulse",
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Reused || res.RowCount != 6 {
			t.Fatalf("result = %+v, want 6 fresh rows", res)
		}
		if sidecarJSON(t, res.InvalidatedSidecars) != sidecarJSON(t, want) {
			t.Errorf("InvalidatedSidecars = %s\nwant %s", sidecarJSON(t, res.InvalidatedSidecars), sidecarJSON(t, want))
		}
	})
}

// sidecarStatFailFs fails Stat on one path with a non-NotExist error, so
// the advisory sidecar enumeration after a write cannot complete.
type sidecarStatFailFs struct {
	afero.Fs
	fail string
}

func (f sidecarStatFailFs) Stat(name string) (os.FileInfo, error) {
	if filepath.Clean(name) == filepath.Clean(f.fail) {
		return nil, stderrors.New("injected stat failure")
	}
	return f.Fs.Stat(name)
}

// TestFilterToFileWithRequest_SidecarScanFailureIsAWarning (#497): when
// the sidecar enumeration after a replacing write fails, the write
// stands and FilterToFileResult.Warnings carries the failure (it used to
// be dropped silently).
func TestFilterToFileWithRequest_SidecarScanFailureIsAWarning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base, _ := filterSidecarFixture(t, Extensions{})
	p, err := New(Options{
		FS: sidecarStatFailFs{Fs: base.fsys, fail: spsssidecar.Path("sets.pulse")},
		Extensions: Extensions{ExprFunctions: []ExprFunction{{
			Name: "ftf_min", Fn: func() float64 { return 2 },
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.FilterToFileWithRequest(ctx, &FilterToFileRequest{
		SourcePath: "src.pulse", Expression: "id >= ftf_min()", OutputDir: ".", OutputName: "sets.pulse",
	})
	if err != nil {
		t.Fatalf("the write must stand: %v", err)
	}
	if res.RowCount != 6 || len(res.InvalidatedSidecars) != 0 {
		t.Fatalf("result = %+v, want 6 rows and no enumerated sidecars", res)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != errors.SERVICE_RESOURCE ||
		res.Warnings[0].Details["cohort"] != res.OutputPath {
		t.Fatalf("warnings = %+v, want one SERVICE_RESOURCE naming %s", res.Warnings, res.OutputPath)
	}
}
