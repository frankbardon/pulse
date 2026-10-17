package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/spf13/afero"
)

// TestExportSPSS_LabelsBecomeValueLabels: `pulse export spss --labels` on a
// native cohort is no longer refused — the bound categorical goes out with
// the table's labels as value labels — and a binding on a numeric field is
// PULSE_LABEL_FIELD_NOT_CATEGORICAL.
func TestExportSPSS_LabelsBecomeValueLabels(t *testing.T) {
	dir := t.TempDir()
	tables := filepath.Join(dir, "tables")
	if err := os.MkdirAll(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tables, "regions.json"), []byte(`{"NE":"Northeast","SO":"South"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_LABEL_TABLES_DIR", tables)

	cohort := filepath.Join(dir, "native.pulse")
	b, err := iio.NewCohortBuild(context.Background(), afero.NewOsFs(), cohort, &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "n", Type: encoding.FieldTypeU8},
	}}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{"NE", uint64(1)}, {"SO", uint64(2)}} {
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out.sav")
	if err := runExportCLI("spss", "-i", cohort, "-o", out, "--labels", "region=regions"); err != nil {
		t.Fatalf("export spss --labels: %v", err)
	}
	sav, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Northeast", "South"} {
		if !bytes.Contains(sav, []byte(label)) {
			t.Errorf("the .sav carries no value label %q", label)
		}
	}

	err = runExportCLI("spss", "-i", cohort, "-o", filepath.Join(dir, "bad.sav"), "--labels", "n=regions")
	wantCode(t, err, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL)
}

// TestExportSPSS_LabelCodesFollowTheTableFileOrder: `--labels` codes the
// bound categorical by the label table FILE's key order (NE, MW, SO, WE),
// not by the cohort's first-seen order (SO, NE, …) and not alphabetically —
// the loader keeps the order, the resolver reports it, the export carries it
// onto the .sav, and Pulse's SPSS reader brings the codes back.
func TestExportSPSS_LabelCodesFollowTheTableFileOrder(t *testing.T) {
	dir := t.TempDir()
	tables := filepath.Join(dir, "tables")
	if err := os.MkdirAll(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	table := `{"description":"US census region","rows":{"NE":"Northeast","MW":"Midwest","SO":"South","WE":"West"}}`
	if err := os.WriteFile(filepath.Join(tables, "regions.json"), []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_LABEL_TABLES_DIR", tables)

	cohort := filepath.Join(dir, "native.pulse")
	b, err := iio.NewCohortBuild(context.Background(), afero.NewOsFs(), cohort, &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"SO", "NE", "WE", "MW"} {
		if err := b.Append([]any{r}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out.sav")
	if err := runExportCLI("spss", "-i", cohort, "-o", out, "--labels", "region=regions"); err != nil {
		t.Fatalf("export spss --labels: %v", err)
	}

	osfs := afero.NewOsFs()
	back := filepath.Join(dir, "back.pulse")
	job := iio.NewImportJob(spss.NewReader(osfs, out), back)
	job.FS = osfs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	raw, err := os.ReadFile(spss.SidecarPath(back))
	if err != nil {
		t.Fatal(err)
	}
	var doc spss.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, v := range doc.Payload.Variables {
		if v.Name != "region" {
			continue
		}
		for _, c := range v.Categories {
			if c.Labelled && c.Code != nil {
				got[int64(*c.Code)] = c.Label
			}
		}
	}
	want := map[int64]string{1: "Northeast", 2: "Midwest", 3: "South", 4: "West"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("region codes -> labels = %v, want the table file's order %v", got, want)
	}
}

// TestExportPredict_LabelsRefuseLikeTheExport pins every value-label
// refusal reachable from the CLI to the same code on `pulse export predict
// --format spss --labels` and on `pulse export spss --labels`: an unknown
// field, a non-categorical field, an unregistered table, a field bound twice,
// and a binding beside an honoured SPSS metadata sidecar. A clean binding
// predicts clean.
func TestExportPredict_LabelsRefuseLikeTheExport(t *testing.T) {
	dir := t.TempDir()
	tables := filepath.Join(dir, "tables")
	if err := os.MkdirAll(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tables, "regions.json"), []byte(`{"NE":"Northeast","SO":"South"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_LABEL_TABLES_DIR", tables)

	native := filepath.Join(dir, "native.pulse")
	b, err := iio.NewCohortBuild(context.Background(), afero.NewOsFs(), native, &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "n", Type: encoding.FieldTypeU8},
	}}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{"NE", uint64(1)}, {"SO", uint64(2)}} {
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	// An SPSS-origin cohort: the native one out to .sav and back, which
	// leaves an honoured metadata sidecar beside it.
	sav := filepath.Join(dir, "plain.sav")
	if err := runExportCLI("spss", "-i", native, "-o", sav); err != nil {
		t.Fatal(err)
	}
	spssCohort := filepath.Join(dir, "spss.pulse")
	if err := runImportCLI(t, "spss", "-i", sav, "-o", spssCohort); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		cohort string
		labels []string
		want   perrors.Code
	}{
		{"clean", native, []string{"region=regions"}, ""},
		{"unknown field", native, []string{"nope=regions"}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{"numeric field", native, []string{"n=regions"}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"unknown table", native, []string{"region=missing"}, perrors.PULSE_LABEL_TABLE_UNKNOWN},
		{"duplicate binding", native, []string{"region=regions", "region=regions:augment"}, perrors.PULSE_LABEL_DUPLICATE_BINDING},
		{"beside a sidecar", spssCohort, []string{"region=regions"}, perrors.PULSE_SPSS_EXPORT_UNSUPPORTED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var flags []string
			for _, l := range tc.labels {
				flags = append(flags, "--labels", l)
			}
			predictErr := runExportCLI(append([]string{"predict", "-i", tc.cohort, "--format", "spss"}, flags...)...)
			runErr := runExportCLI(append([]string{"spss", "-i", tc.cohort, "-o", filepath.Join(t.TempDir(), "out.sav")}, flags...)...)
			if tc.want == "" {
				if predictErr != nil || runErr != nil {
					t.Fatalf("clean binding: predict %v, export %v", predictErr, runErr)
				}
				return
			}
			wantCode(t, predictErr, tc.want)
			wantCode(t, runErr, tc.want)
		})
	}
}
