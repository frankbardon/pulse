package pulse

import (
	"context"
	"reflect"
	"testing"
	"time"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Materialize and Rake CARRY the source's SPSS metadata sidecar onto the
// cohort they write, rewritten for its schema, so value labels survive a
// weighting step into a .sav export.

// labelledSavSpec: ID, a value-labelled Q1 (a categorical holding the codes
// 1 / 2 in the cohort) and a weight WT.
func labelledSavSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID", Label: "Respondent identifier", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "Q1", Label: "Satisfaction", Measure: spsstest.MeasureNominal,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "WT", Label: "Design weight", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 2}},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(1), spsstest.Num(1)},
			{spsstest.Num(2), spsstest.Num(2), spsstest.Num(2)},
			{spsstest.Num(3), spsstest.Num(1), spsstest.Num(1.5)},
			{spsstest.Num(4), spsstest.Num(2), spsstest.Num(0.5)},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars:   []string{"Q1"},
			Labels: []spsstest.ValueLabel{{Value: spsstest.Num(1), Label: "Low"}, {Value: spsstest.Num(2), Label: "High"}},
		}},
		WeightVar: "WT",
	}
}

// exportedQ1Labels exports cohort to .sav through Pulse.Export (asserting
// the sidecar was honoured — no PULSE_SPSS_SIDECAR_ABSENT), re-imports the
// .sav and returns Q1's code -> label map and the emitted variable names.
func exportedQ1Labels(t *testing.T, p *Pulse, afs afero.Fs, cohort string) (map[float64]string, []string) {
	t.Helper()
	w, err := pio.NewWriter(pio.FormatSPSS, afs, cohort+".sav", pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob(cohort, w)
	job.FS = afs
	rep, err := p.Export(context.Background(), job)
	if err != nil {
		t.Fatalf("export %s: %v", cohort, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, warn := range rep.TargetWarnings {
		if warn.Code == perrors.PULSE_SPSS_SIDECAR_ABSENT {
			t.Fatalf("export of %s found no sidecar: %v", cohort, warn)
		}
	}
	r, err := pio.NewReader(pio.FormatSPSS, afs, cohort+".sav", pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	back := cohort + ".back.pulse"
	imp := pio.NewImportJob(r, back)
	imp.FS = afs
	if _, err := p.Import(context.Background(), imp); err != nil {
		t.Fatal(err)
	}
	// Q1's cells come back as the codes the cohort held, row for row.
	_, srcRows := readAll(t, p, cohort)
	_, backRows := readAll(t, p, back)
	for i := range srcRows {
		if srcRows[i][1] != backRows[i][1] {
			t.Errorf("row %d Q1 = %v after the .sav round trip, want %v", i, backRows[i][1], srcRows[i][1])
		}
	}
	res, err := spss.LoadSidecar(afs, back, spss.WriterOptions{})
	if err != nil || res.Document == nil {
		t.Fatalf("re-imported sidecar: %v", err)
	}
	labels := map[float64]string{}
	var names []string
	for _, v := range res.Document.Payload.Variables {
		names = append(names, v.Name)
		if v.Name != "Q1" {
			continue
		}
		for _, c := range v.Categories {
			if c.Labelled && c.Code != nil {
				labels[float64(*c.Code)] = c.Label
			}
		}
	}
	return labels, names
}

func assertCarried(t *testing.T, p *Pulse, afs afero.Fs, target string, wantVars []string) {
	t.Helper()
	res, err := spss.LoadSidecar(afs, target, spss.WriterOptions{})
	if err != nil || res == nil || res.Document == nil {
		t.Fatalf("no fresh sidecar beside %s: %v", target, err)
	}
	labels, names := exportedQ1Labels(t, p, afs, target)
	if want := map[float64]string{1: "Low", 2: "High"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("Q1 labels after export of %s = %v, want %v", target, labels, want)
	}
	if !reflect.DeepEqual(names, wantVars) {
		t.Errorf("exported variables = %v, want %v", names, wantVars)
	}
}

func TestMaterialize_CarriesSPSSSidecar(t *testing.T) {
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	res, err := p.Materialize(context.Background(), &Request{
		Cohort:     &types.Cohort{Filename: suggestCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "ID", Label: "z"}},
	}, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v", res.Warnings)
	}
	assertCarried(t, p, afs, "derived.pulse", []string{"ID", "Q1", "WT", "z"})
}

func TestRake_CarriesSPSSSidecar(t *testing.T) {
	q1 := []RakeMargin{{Field: "Q1", Targets: map[string]float64{"1": 0.4, "2": 0.6}}}
	t.Run("new weight column", func(t *testing.T) {
		afs := afero.NewMemMapFs()
		p := importSav(t, afs, labelledSavSpec(), Options{})
		res, err := p.Rake(context.Background(), RakeRequest{Cohort: suggestCohort, Out: "raked.pulse", Margins: q1})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Warnings) != 0 {
			t.Fatalf("warnings %v", res.Warnings)
		}
		assertCarried(t, p, afs, "raked.pulse", []string{"ID", "Q1", "WT", RakeDefaultWeightField})
	})
	// Replacing the recorded weight variable, over a target whose own
	// sidecar the overwrite would otherwise leave stale.
	t.Run("replaced weight over an existing target", func(t *testing.T) {
		afs := afero.NewMemMapFs()
		p := importSav(t, afs, labelledSavSpec(), Options{})
		if _, err := p.Rake(context.Background(), RakeRequest{Cohort: suggestCohort, Out: "raked.pulse", Margins: q1}); err != nil {
			t.Fatal(err)
		}
		res, err := p.Rake(context.Background(), RakeRequest{Cohort: suggestCohort, Out: "raked.pulse",
			WeightField: "WT", Margins: q1, Overwrite: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Warnings) != 0 || len(res.InvalidatedSidecars) != 0 {
			t.Fatalf("warnings %v invalidated %v: a carried sidecar is fresh", res.Warnings, res.InvalidatedSidecars)
		}
		assertCarried(t, p, afs, "raked.pulse", []string{"ID", "Q1", "WT"})
	})
}

// TestMaterialize_StaleSidecarIsNotCarried: a stale source sidecar is
// reported (its own code) and the derived cohort is written without one.
func TestMaterialize_StaleSidecarIsNotCarried(t *testing.T) {
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	if err := afs.Chtimes(suggestCohort, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := p.Materialize(context.Background(), &Request{
		Cohort:     &types.Cohort{Filename: suggestCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "ID", Label: "z"}},
	}, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != perrors.PULSE_SPSS_SIDECAR_STALE {
		t.Fatalf("warnings %v, want one PULSE_SPSS_SIDECAR_STALE", res.Warnings)
	}
	if ok, _ := afero.Exists(afs, spss.SidecarPath("derived.pulse")); ok {
		t.Fatal("a stale sidecar was carried")
	}
}
