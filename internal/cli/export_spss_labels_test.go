package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
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
