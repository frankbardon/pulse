package pulse_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// artifactsFixture imports the reference .sav into a/survey.pulse (which
// writes the SPSS metadata sidecar) and builds a point-lookup index on
// NAME (which writes the .idx and the discovery manifest).
func artifactsFixture(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	raw, err := spsstest.Build(spsstest.ReferenceSpec())
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	if err := afero.WriteFile(afs, "src/in.sav", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	ctx := context.Background()
	reader, err := pio.NewReader(pio.FormatSPSS, afs, "src/in.sav", pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	job := pio.NewImportJob(reader, "a/survey.pulse")
	job.FS = afs
	if _, err := p.Import(ctx, job); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := p.BuildIndex(ctx, "a/survey.pulse", []string{"NAME"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	return p, afs
}

// moveCopy copies src to dst on afs and carries src's modification
// time across, as a rename or `cp -p` does. Both sidecars fingerprint
// the cohort's size AND mtime, and the SPSS sidecar refuses a cohort
// whose mtime moved (PULSE_SPSS_SIDECAR_STALE), so a mover that resets
// the cohort's mtime has invalidated what it carried.
func moveCopy(t *testing.T, afs afero.Fs, src, dst string) {
	t.Helper()
	b, err := afero.ReadFile(afs, src)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", src, err)
	}
	if err := afero.WriteFile(afs, dst, b, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", dst, err)
	}
	fi, err := afs.Stat(src)
	if err != nil {
		t.Fatalf("Stat %s: %v", src, err)
	}
	if err := afs.Chtimes(dst, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatalf("Chtimes %s: %v", dst, err)
	}
}

func exportSav(t *testing.T, p *pulse.Pulse, afs afero.Fs, cohort, out string) ([]byte, *pio.ExportReport) {
	t.Helper()
	w := spss.NewWriter(afs, out, spss.WriterOptions{})
	job := pio.NewExportJob(cohort, w)
	job.FS = afs
	report, err := p.Export(context.Background(), job)
	if err != nil {
		t.Fatalf("Export %s: %v", cohort, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	b, err := afero.ReadFile(afs, out)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", out, err)
	}
	return b, report
}

func reportHasCode(report *pio.ExportReport, code perrors.Code) bool {
	for _, w := range report.TargetWarnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// Moving a cohort together with every path CohortArtifacts returns
// keeps Lookup hitting and SPSS export faithful at the new location.
func TestCohortArtifacts_MovedWithCohortKeepsLookupAndSPSSExport(t *testing.T) {
	p, afs := artifactsFixture(t)
	ctx := context.Background()

	arts, err := p.CohortArtifacts(ctx, "a/survey.pulse")
	if err != nil {
		t.Fatalf("CohortArtifacts: %v", err)
	}
	var sawIdx, sawManifest, sawSPSS bool
	for _, a := range arts {
		switch {
		case strings.HasSuffix(a, ".idx"):
			sawIdx = true
		case strings.HasSuffix(a, ".indexes.json"):
			sawManifest = true
		case a == spss.SidecarPath("a/survey.pulse"):
			sawSPSS = true
		}
		if a == "a/survey.pulse" {
			t.Errorf("artifacts include the cohort itself: %v", arts)
		}
	}
	if !sawIdx || !sawManifest || !sawSPSS {
		t.Fatalf("artifacts = %v; want the .idx, .indexes.json and .spss.json sidecars", arts)
	}

	wantSav, srcReport := exportSav(t, p, afs, "a/survey.pulse", "a/out.sav")
	if reportHasCode(srcReport, perrors.PULSE_SPSS_SIDECAR_ABSENT) {
		t.Fatal("fixture export at the source already lacks its sidecar; the test proves nothing")
	}

	// Move: cohort + every returned path, renamed under b/.
	moveCopy(t, afs, "a/survey.pulse", "b/survey.pulse")
	for _, a := range arts {
		moveCopy(t, afs, a, "b/"+strings.TrimPrefix(a, "a/"))
	}
	for _, f := range append([]string{"a/survey.pulse"}, arts...) {
		if err := afs.Remove(f); err != nil {
			t.Fatalf("Remove %s: %v", f, err)
		}
	}

	moved, err := p.CohortArtifacts(ctx, "b/survey.pulse")
	if err != nil {
		t.Fatalf("CohortArtifacts(moved): %v", err)
	}
	if len(moved) != len(arts) {
		t.Errorf("moved artifacts = %v, want %d entries", moved, len(arts))
	}

	res, err := p.Lookup(ctx, &pulse.LookupRequest{
		Cohort: &types.Cohort{Filename: "b/survey.pulse"},
		Field:  "NAME",
		Value:  "BOB",
	})
	if err != nil {
		t.Fatalf("Lookup at the new location: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("Lookup rows = %v, want exactly BOB", res.Rows)
	}

	gotSav, report := exportSav(t, p, afs, "b/survey.pulse", "b/out.sav")
	if reportHasCode(report, perrors.PULSE_SPSS_SIDECAR_ABSENT) || reportHasCode(report, perrors.PULSE_SPSS_SIDECAR_STALE) {
		t.Errorf("export at the new location lost its sidecar: %+v", report.TargetWarnings)
	}
	if string(gotSav) != string(wantSav) {
		t.Errorf("moved export = %d bytes, source export = %d; want the same file", len(gotSav), len(wantSav))
	}

	// Control: a cohort moved WITHOUT its artifacts degrades, so the
	// assertions above are about the listing, not luck.
	moveCopy(t, afs, "b/survey.pulse", "c/survey.pulse")
	_, bare := exportSav(t, p, afs, "c/survey.pulse", "c/out.sav")
	if !reportHasCode(bare, perrors.PULSE_SPSS_SIDECAR_ABSENT) {
		t.Errorf("bare move: warnings %+v, want PULSE_SPSS_SIDECAR_ABSENT", bare.TargetWarnings)
	}
	if _, err := p.Lookup(ctx, &pulse.LookupRequest{
		Cohort: &types.Cohort{Filename: "c/survey.pulse"}, Field: "NAME", Value: "BOB",
	}); err == nil {
		t.Error("bare move: Lookup succeeded without an index")
	}
}

// Every kind is listed, sorted, existing-only, and a file that merely
// shares the cohort's name as a prefix is not swept up.
func TestCohortArtifacts_ListsEveryKindSortedAndNothingElse(t *testing.T) {
	afs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	ctx := context.Background()
	write := func(name string) {
		if err := afero.WriteFile(afs, name, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	write("d/c.pulse")
	empty, err := p.CohortArtifacts(ctx, "d/c.pulse")
	if err != nil {
		t.Fatalf("CohortArtifacts: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("no sidecars: got %#v, want an empty non-nil slice", empty)
	}

	for _, n := range []string{
		"d/c.pulse.spss.json",
		"d/c.pulse.meta.json",
		"d/c.pulse.indexes.json",
		"d/c.pulse.00112233aabbccdd.idx",
		"d/c.pulse.ffffffffffffffff.idx",
		// decoys
		"d/c.pulse.bak",
		"d/c.pulse.notahexhash00000.idx",
		"d/c.pulse.0011.idx",
		"d/c.pulse2.spss.json",
		"d/other.pulse.meta.json",
		"e/c.pulse.spss.json",
	} {
		write(n)
	}
	got, err := p.CohortArtifacts(ctx, "d/c.pulse")
	if err != nil {
		t.Fatalf("CohortArtifacts: %v", err)
	}
	want := []string{
		"d/c.pulse.00112233aabbccdd.idx",
		"d/c.pulse.ffffffffffffffff.idx",
		"d/c.pulse.indexes.json",
		"d/c.pulse.meta.json",
		"d/c.pulse.spss.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CohortArtifacts =\n  %v\nwant\n  %v", got, want)
	}

	// A root-level cohort spells its sidecars without a directory.
	write("top.pulse")
	write("top.pulse.spss.json")
	top, err := p.CohortArtifacts(ctx, "top.pulse")
	if err != nil {
		t.Fatalf("CohortArtifacts(top): %v", err)
	}
	if !reflect.DeepEqual(top, []string{"top.pulse.spss.json"}) {
		t.Errorf("top-level = %v, want [top.pulse.spss.json]", top)
	}
}

func TestCohortArtifacts_MissingCohortIsCoded(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	_, err = p.CohortArtifacts(context.Background(), "nope.pulse")
	if !perrors.HasCode(err, perrors.SERVICE_RESOURCE) {
		t.Errorf("err = %v, want SERVICE_RESOURCE", err)
	}
}
