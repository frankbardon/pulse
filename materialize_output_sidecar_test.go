package pulse

import (
	"context"
	"reflect"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/spsstest"
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

// TestMaterializeOutput_CastColumnSidecarRebuilt: a cast column's
// variable is rebuilt from its written type (spss.RebuildVariable) —
// never the source variable's stale description — while the uncast
// columns keep theirs (Q1 its value labels).
func TestMaterializeOutput_CastColumnSidecarRebuilt(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	req := absentOutputRequest()
	req.Output = &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "ID", As: "id16", Type: "u16"}, {From: "Q1"}, {From: "WT"},
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
		t.Fatalf("no fresh sidecar: %v", err)
	}
	labelled := map[string]int{}
	pulseTypes := map[string]string{}
	for _, v := range sc.Document.Payload.Variables {
		pulseTypes[v.Name] = v.PulseType
		for _, c := range v.Categories {
			if c.Labelled {
				labelled[v.Name]++
			}
		}
	}
	if pulseTypes["id16"] != "u16" || labelled["Q1"] != 2 {
		t.Errorf("pulse types %v, labelled categories %v: want id16 rebuilt as u16, Q1 keeping its 2 labels", pulseTypes, labelled)
	}
}

// sidecarVars loads the carried sidecar beside cohort and returns its
// variables by name.
func sidecarVars(t *testing.T, afs afero.Fs, cohort string) map[string]spss.Variable {
	t.Helper()
	sc, err := spss.LoadSidecar(afs, cohort, spss.WriterOptions{})
	if err != nil || sc == nil || sc.Document == nil {
		t.Fatalf("no fresh sidecar beside %s: %v", cohort, err)
	}
	out := map[string]spss.Variable{}
	for _, v := range sc.Document.Payload.Variables {
		out[v.Name] = v
	}
	return out
}

// labelledCount is the number of v's categories carrying a value label.
func labelledCount(v spss.Variable) int {
	n := 0
	for _, c := range v.Categories {
		if c.Labelled {
			n++
		}
	}
	return n
}

// TestMaterializeOutput_CategoricalCastSidecarRebuilt: Q1 (a categorical
// of the SPSS codes "1" / "2", value-labelled Low / High) cast to u8 and
// to categorical_u16, ID and the derived row number cast to a
// categorical: each cast variable
// is rebuilt from its written type — a plain numeric, or an unlabelled
// string variable of the written dictionary — so no Low / High label
// lands on the new values, while the uncast Q1 keeps both; a .sav
// export round-trips every cell.
func TestMaterializeOutput_CategoricalCastSidecarRebuilt(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	req := absentOutputRequest()
	// A cast to a number: a plain numeric (its .sav round trip reads back
	// as f64, as any numeric variable does, so it runs apart).
	req.Output = &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "Q1", As: "q1n", Type: "u8"}, {From: "Q1"},
	}}
	res, err := p.Materialize(context.Background(), req, "numeric.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v, want the sidecar carried", res.Warnings)
	}
	vars := sidecarVars(t, afs, "numeric.pulse")
	if v := vars["q1n"]; v.PulseType != "u8" || v.Kind != "numeric" || len(v.Categories) != 0 {
		t.Errorf("q1n = %s %s %d categories, want a plain u8 numeric", v.PulseType, v.Kind, len(v.Categories))
	}

	req.Output = &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "Q1", As: "q1w", Type: "categorical_u16"},
		{From: "ID", As: "idc", Type: "categorical_u8"},
		{From: "rn", As: "rnc", Type: "categorical_u8"},
		{From: "Q1"},
	}}
	res, err = p.Materialize(context.Background(), req, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v, want the sidecar carried", res.Warnings)
	}
	vars = sidecarVars(t, afs, "derived.pulse")
	for name, want := range map[string][]string{"q1w": {"1", "2"}, "idc": {"1", "2", "3", "4"}, "rnc": {"1", "2", "3", "4"}} {
		v := vars[name]
		var values []string
		for _, c := range v.Categories {
			values = append(values, c.Value)
		}
		if v.Kind != "categorical" || labelledCount(v) != 0 || !reflect.DeepEqual(values, want) {
			t.Errorf("%s = %s, values %v, %d labelled; want an unlabelled categorical of %v", name, v.Kind, values, labelledCount(v), want)
		}
	}
	if n := labelledCount(vars["Q1"]); n != 2 {
		t.Errorf("uncast Q1 keeps %d labels, want 2", n)
	}
	_, labels := savRoundTrip(t, p, afs, "derived.pulse")
	if want := map[string]map[float64]string{"Q1": {1: "Low", 2: "High"}}; !reflect.DeepEqual(labels, want) {
		t.Errorf("value labels after the round trip = %v, want only Q1's %v", labels, want)
	}
}

// TestMaterializeOutput_RebuiltPreviousStateDropsMirror: an
// ATTR_PREVIOUS_STATE column mirrors its state variable's codes and
// value labels — unless the output casts it: then the projection passes
// no mirror and the variable is rebuilt from the written type, so Q1's
// Low / High never land on the cast values. (The state is a categorical
// of SPSS codes: an SPSS import never writes a packed_bool.)
func TestMaterializeOutput_RebuiltPreviousStateDropsMirror(t *testing.T) {
	t.Parallel()

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
		Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "ID"}, {From: "Q1"}, {From: "prev_q1"}, {From: "prev_q1", As: "prev_n", Type: "u8"},
		}},
	}, "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v, want the sidecar carried", res.Warnings)
	}
	vars := sidecarVars(t, afs, "derived.pulse")
	if n := labelledCount(vars["prev_q1"]); n != 2 {
		t.Errorf("uncast prev_q1 mirrors %d labels, want Q1's 2", n)
	}
	if v := vars["prev_n"]; v.PulseType != "u8" || v.Kind != "numeric" || len(v.Categories) != 0 {
		t.Errorf("prev_n = %s %s %+v, want a rebuilt plain u8 numeric", v.PulseType, v.Kind, v.Categories)
	}
	_, rows := readAll(t, p, "derived.pulse")
	for i, want := range []any{nil, uint64(1), nil, uint64(2)} {
		if rows[i][3] != want {
			t.Errorf("row %d prev_n = %v, want %v", i, rows[i][3], want)
		}
	}
}
