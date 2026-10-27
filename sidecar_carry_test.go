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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// TestMaterialize_CarriesPreviousStateValueLabels: an ATTR_PREVIOUS_STATE
// column inherits its state field's type and dictionary, so the carried
// sidecar describes it as that state variable does — the same codes and
// value labels — instead of refusing a categorical column (which would
// drop the whole sidecar); a .sav export carries the labels and the
// re-imported column holds the previous codes.
func TestMaterialize_CarriesPreviousStateValueLabels(t *testing.T) {
	t.Parallel()

	// SPSS DATE counts seconds from 1582-10-14; epoch day d is
	// 12219379200 + 86400·d.
	day := func(d float64) spsstest.Value { return spsstest.Num(12219379200 + 86400*d) }
	spec := labelledSavSpec()
	spec.Vars = append(spec.Vars, spsstest.Var{Name: "DAY", Label: "Interview day",
		Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}})
	spec.Cases = [][]spsstest.Value{
		{spsstest.Num(1), spsstest.Num(1), spsstest.Num(1), day(1)},
		{spsstest.Num(1), spsstest.Num(2), spsstest.Num(1), day(2)},
		{spsstest.Num(2), spsstest.Num(2), spsstest.Num(1), day(1)},
		{spsstest.Num(2), spsstest.Num(1), spsstest.Num(1), day(2)},
	}
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, spec, Options{})
	res, err := p.Materialize(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: suggestCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "Q1",
			SubjectField: "ID", OrderField: "DAY", Label: "prev_q1"}},
	}, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v", res.Warnings)
	}
	sc, err := spss.LoadSidecar(afs, "derived.pulse", spss.WriterOptions{})
	if err != nil || sc == nil || sc.Document == nil {
		t.Fatalf("no fresh sidecar beside derived.pulse: %v", err)
	}
	var q1, prev *spss.Variable
	for i := range sc.Document.Payload.Variables {
		switch v := &sc.Document.Payload.Variables[i]; v.Name {
		case "Q1":
			q1 = v
		case "prev_q1":
			prev = v
		}
	}
	if q1 == nil || prev == nil {
		t.Fatalf("sidecar variables Q1 %v prev_q1 %v", q1, prev)
	}
	if !reflect.DeepEqual(prev.Categories, q1.Categories) || prev.Kind != q1.Kind || !prev.Nullable {
		t.Errorf("prev_q1 = %+v, want Q1's categories %+v (nullable)", prev, q1.Categories)
	}
	if prev.ShortName == q1.ShortName || prev.Index == q1.Index {
		t.Errorf("prev_q1 short name %q index %d collide with Q1's", prev.ShortName, prev.Index)
	}

	w, err := pio.NewWriter(pio.FormatSPSS, afs, "derived.sav", pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob("derived.pulse", w)
	job.FS = afs
	rep, err := p.Export(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, warn := range rep.TargetWarnings {
		if warn.Code == perrors.PULSE_SPSS_SIDECAR_ABSENT {
			t.Fatalf("export found no sidecar: %v", warn)
		}
	}
	r, err := pio.NewReader(pio.FormatSPSS, afs, "derived.sav", pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	imp := pio.NewImportJob(r, "back.pulse")
	imp.FS = afs
	if _, err := p.Import(context.Background(), imp); err != nil {
		t.Fatal(err)
	}
	back, err := spss.LoadSidecar(afs, "back.pulse", spss.WriterOptions{})
	if err != nil || back.Document == nil {
		t.Fatalf("re-imported sidecar: %v", err)
	}
	labels := map[float64]string{}
	for _, v := range back.Document.Payload.Variables {
		if v.Name != "prev_q1" {
			continue
		}
		for _, c := range v.Categories {
			if c.Labelled && c.Code != nil {
				labels[float64(*c.Code)] = c.Label
			}
		}
	}
	if want := map[float64]string{1: "Low", 2: "High"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("prev_q1 labels after the .sav round trip = %v, want %v", labels, want)
	}
	srcSchema, _ := readAll(t, p, "derived.pulse")
	pi := len(srcSchema.Fields) - 1
	_, srcRows := readAll(t, p, "derived.pulse")
	_, backRows := readAll(t, p, "back.pulse")
	for i := range srcRows {
		if srcRows[i][pi] != backRows[i][pi] {
			t.Errorf("row %d prev_q1 = %v after the round trip, want %v", i, backRows[i][pi], srcRows[i][pi])
		}
	}
	if srcRows[1][pi] != "1" || srcRows[0][pi] != nil {
		t.Errorf("prev_q1 cells %v / %v, want nil / the first wave's code", srcRows[0][pi], srcRows[1][pi])
	}
}

// TestMaterialize_PreviousStateObservedRecomputed: the carried
// previous-state variable copies its state variable's codes and labels,
// but each category's Observed flag is recomputed from the column
// written — a code the state field carried only at a subject's LAST
// wave never occurs as a previous state, so it is unobserved there
// (and a declared label nothing used stays unobserved), while the
// state variable keeps its own flags.
func TestMaterialize_PreviousStateObservedRecomputed(t *testing.T) {
	t.Parallel()

	day := func(d float64) spsstest.Value { return spsstest.Num(12219379200 + 86400*d) }
	spec := labelledSavSpec()
	spec.Vars = append(spec.Vars, spsstest.Var{Name: "DAY", Label: "Interview day",
		Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}})
	spec.ValueLabels[0].Labels = append(spec.ValueLabels[0].Labels, spsstest.ValueLabel{Value: spsstest.Num(3), Label: "Mid"})
	// Code 2 only ever closes a subject's history; code 3 is declared
	// and never carried.
	spec.Cases = [][]spsstest.Value{
		{spsstest.Num(1), spsstest.Num(1), spsstest.Num(1), day(1)},
		{spsstest.Num(1), spsstest.Num(2), spsstest.Num(1), day(2)},
		{spsstest.Num(2), spsstest.Num(1), spsstest.Num(1), day(1)},
		{spsstest.Num(2), spsstest.Num(1), spsstest.Num(1), day(2)},
	}
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, spec, Options{})
	res, err := p.Materialize(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: suggestCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "Q1",
			SubjectField: "ID", OrderField: "DAY", Label: "prev_q1"}},
	}, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v", res.Warnings)
	}
	sc, err := spss.LoadSidecar(afs, "derived.pulse", spss.WriterOptions{})
	if err != nil || sc == nil || sc.Document == nil {
		t.Fatalf("no fresh sidecar beside derived.pulse: %v", err)
	}
	observed := map[string]map[string]bool{}
	for _, v := range sc.Document.Payload.Variables {
		if v.Name != "Q1" && v.Name != "prev_q1" {
			continue
		}
		observed[v.Name] = map[string]bool{}
		for _, c := range v.Categories {
			observed[v.Name][c.Label] = c.Observed
		}
	}
	if want := map[string]bool{"Low": true, "High": true, "Mid": false}; !reflect.DeepEqual(observed["Q1"], want) {
		t.Errorf("Q1 observed %v, want %v", observed["Q1"], want)
	}
	if want := map[string]bool{"Low": true, "High": false, "Mid": false}; !reflect.DeepEqual(observed["prev_q1"], want) {
		t.Errorf("prev_q1 observed %v, want %v (recomputed from the written column)", observed["prev_q1"], want)
	}
}
