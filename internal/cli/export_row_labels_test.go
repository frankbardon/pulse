package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
)

// TestExportCSV_LabelsOnIntegerCodedField: `pulse export csv --labels` on a
// u8 field labels it (replace and augment), a table keyed by text is
// PULSE_LABEL_KEY_INVALID and a float field PULSE_LABEL_FIELD_NOT_CATEGORICAL
// — on the export leaf and on `export predict` with no --format alike, and a
// refused export leaves no output file.
func TestExportCSV_LabelsOnIntegerCodedField(t *testing.T) {
	dir := t.TempDir()
	tables := filepath.Join(dir, "tables")
	if err := os.MkdirAll(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"counts.json":  `{"1":"Single","2":"Pair"}`,
		"regions.json": `{"NE":"Northeast"}`,
	} {
		if err := os.WriteFile(filepath.Join(tables, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PULSE_LABEL_TABLES_DIR", tables)

	cohort := filepath.Join(dir, "c.pulse")
	b, err := iio.NewCohortBuild(context.Background(), afero.NewOsFs(), cohort, &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU8},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{uint64(1), 1.5}, {uint64(2), 2.5}, {uint64(3), 3.5}} {
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ binding, want string }{
		{"n=counts", "n,score\nSingle,1.5\nPair,2.5\n3,3.5\n"},
		{"n=counts:augment", "n,n_label,score\n1,Single,1.5\n2,Pair,2.5\n3,,3.5\n"},
	} {
		out := filepath.Join(dir, "out.csv")
		if err := runExportCLI("csv", "-i", cohort, "-o", out, "--labels", tc.binding); err != nil {
			t.Fatalf("export csv --labels %s: %v", tc.binding, err)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("--labels %s wrote\n%s\nwant\n%s", tc.binding, got, tc.want)
		}
	}

	for _, tc := range []struct {
		binding string
		want    perrors.Code
	}{
		{"n=regions", perrors.PULSE_LABEL_KEY_INVALID},
		{"score=counts", perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
	} {
		bad := filepath.Join(dir, "bad.csv")
		wantCode(t, runExportCLI("csv", "-i", cohort, "-o", bad, "--labels", tc.binding), tc.want)
		if _, err := os.Stat(bad); !os.IsNotExist(err) {
			t.Errorf("--labels %s: a refused export left %s behind", tc.binding, bad)
		}
		wantCode(t, runExportCLI("predict", "-i", cohort, "--labels", tc.binding), tc.want)
	}
}
