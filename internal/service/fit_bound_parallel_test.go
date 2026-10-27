package service

import (
	"bytes"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
)

// writeParallelXYCohort writes a 512-row f64 (x, y) cohort to an OS temp
// dir (the parallel decoder mmaps a real file) and returns its path and
// a config rooted there.
func writeParallelXYCohort(t *testing.T, name string) (string, *fs.Config) {
	t.Helper()
	const rows = 512
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/" + name
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rows; r++ {
		x := float64((r*37)%101) + 0.25
		y := 0.5*x + float64((r*11)%13)
		for _, v := range []float64{x, y} {
			if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, float64ToBits(v)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return path, cfg
}
