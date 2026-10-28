package pulse

import (
	"context"
	"reflect"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An `output` block over an SPSS-backed source: the carried sidecar
// follows the reshape column for column (spss.SidecarCarry.WriteProjected).

// savRoundTrip exports cohort to .sav (asserting the carried sidecar was
// honoured), re-imports it and returns the re-imported variable names in
// order and each variable's code -> value label map; every cell of the
// round trip equals the cohort's.
func savRoundTrip(t *testing.T, p *Pulse, afs afero.Fs, cohort string) ([]string, map[string]map[float64]string) {
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
	_, srcRows := readAll(t, p, cohort)
	_, backRows := readAll(t, p, back)
	if !reflect.DeepEqual(srcRows, backRows) {
		t.Errorf("rows after the .sav round trip = %v, want %v", backRows, srcRows)
	}
	res, err := spss.LoadSidecar(afs, back, spss.WriterOptions{})
	if err != nil || res.Document == nil {
		t.Fatalf("re-imported sidecar: %v", err)
	}
	var names []string
	labels := map[string]map[float64]string{}
	for _, v := range res.Document.Payload.Variables {
		names = append(names, v.Name)
		for _, c := range v.Categories {
			if c.Labelled && c.Code != nil {
				if labels[v.Name] == nil {
					labels[v.Name] = map[float64]string{}
				}
				labels[v.Name][float64(*c.Code)] = c.Label
			}
		}
	}
	return names, labels
}

// TestMaterializeOutput_SidecarFollowsDropRenameDuplicate: dropping WT,
// renaming Q1 and writing it twice leaves a fresh sidecar holding exactly
// the output variables — the dropped one (and its weight) gone, both Q1
// columns keeping Q1's codes and value labels — and a .sav export puts
// the labels on the right variables, cells unchanged.
func TestMaterializeOutput_SidecarFollowsDropRenameDuplicate(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	req := absentOutputRequest()
	req.Output = &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "ID"}, {From: "Q1", As: "satisfaction"}, {From: "rn"}, {From: "Q1", As: "Q1_copy"},
	}}
	res, err := p.Materialize(context.Background(), req, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v, want the sidecar carried", res.Warnings)
	}
	sc, err := spss.LoadSidecar(afs, "derived.pulse", spss.WriterOptions{})
	if err != nil || sc == nil || sc.Document == nil {
		t.Fatalf("no fresh sidecar beside derived.pulse: %v", err)
	}
	var got []string
	for _, v := range sc.Document.Payload.Variables {
		got = append(got, v.Name)
	}
	want := []string{"ID", "satisfaction", "rn", "Q1_copy"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sidecar variables = %v, want %v", got, want)
	}
	if sc.Document.Payload.Weight != nil {
		t.Errorf("weight %+v survives the dropped WT", sc.Document.Payload.Weight)
	}

	names, labels := savRoundTrip(t, p, afs, "derived.pulse")
	if !reflect.DeepEqual(names, want) {
		t.Errorf("exported variables = %v, want %v", names, want)
	}
	q1 := map[float64]string{1: "Low", 2: "High"}
	if wantLabels := map[string]map[float64]string{"satisfaction": q1, "Q1_copy": q1}; !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("value labels after the round trip = %v, want %v", labels, wantLabels)
	}
}

// TestMaterializeOutput_SidecarFollowsReorderAndRedescribe: every column
// kept but reordered and one redescribed — the carried variables follow
// the output order, the redescribed one takes the new label, the renamed
// weight stays the weight, and the export round-trips.
func TestMaterializeOutput_SidecarFollowsReorderAndRedescribe(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	req := absentOutputRequest()
	req.Output = &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "WT", As: "weight"}, {From: "Q1", Description: strPtr("Overall satisfaction score")}, {From: "ID"}, {From: "rn"},
	}}
	res, err := p.Materialize(context.Background(), req, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v", res.Warnings)
	}
	sc, err := spss.LoadSidecar(afs, "derived.pulse", spss.WriterOptions{})
	if err != nil || sc == nil || sc.Document == nil {
		t.Fatalf("no fresh sidecar: %v", err)
	}
	vars := sc.Document.Payload.Variables
	if vars[0].Name != "weight" || vars[1].Name != "Q1" || vars[1].Label != "Overall satisfaction score" {
		t.Errorf("variables %+v", vars[:2])
	}
	if w := sc.Document.Payload.Weight; w == nil || w.Variable != "weight" || w.Index != vars[0].Index {
		t.Errorf("weight = %+v, want the renamed weight", w)
	}
	names, labels := savRoundTrip(t, p, afs, "derived.pulse")
	if want := []string{"weight", "Q1", "ID", "rn"}; !reflect.DeepEqual(names, want) {
		t.Errorf("exported variables = %v, want %v", names, want)
	}
	if want := map[string]map[float64]string{"Q1": {1: "Low", 2: "High"}}; !reflect.DeepEqual(labels, want) {
		t.Errorf("value labels = %v, want %v", labels, want)
	}
}
