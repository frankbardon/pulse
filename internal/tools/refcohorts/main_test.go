package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// The committed R-oracle fixtures (written by `make reference`).
const (
	referenceDir = "../../processing/testdata/reference"
	fixtureDir   = referenceDir + "/fixtures"
)

// committedFixtures lists the fixture CSVs, failing when there are none
// so a moved directory cannot turn every gate below into a no-op.
func committedFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := fixtureCSVs(fixtureDir)
	if err != nil {
		t.Fatalf("listing %s: %v", fixtureDir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixture CSVs under %s (regenerate with `make reference`)", fixtureDir)
	}
	return paths
}

// TestRefCohorts_CommittedPulseCurrent is the "rerun changes nothing"
// gate for the .pulse half of the fixtures: each committed cohort must
// equal, byte for byte, what convert writes from its committed CSV, and
// no .pulse may lack a CSV.
func TestRefCohorts_CommittedPulseCurrent(t *testing.T) {
	csvs := committedFixtures(t)
	want := map[string]bool{}
	for _, path := range csvs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := convert(raw)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		dst := strings.TrimSuffix(path, ".csv") + ".pulse"
		want[filepath.Base(dst)] = true
		committed, err := os.ReadFile(dst)
		if err != nil {
			t.Errorf("%s: %v (regenerate with `make reference`)", filepath.Base(dst), err)
			continue
		}
		if !bytes.Equal(got, committed) {
			t.Errorf("%s is stale against its CSV (regenerate with `make reference`)", filepath.Base(dst))
		}
	}
	pulses, _ := filepath.Glob(filepath.Join(fixtureDir, "*.pulse"))
	for _, p := range pulses {
		if !want[filepath.Base(p)] {
			t.Errorf("%s has no CSV source", filepath.Base(p))
		}
	}
}

// TestRefCohorts_RoundTrip decodes every committed cohort with the
// public encoding primitives and checks each cell against the CSV:
// bit-identical doubles, nulls exactly where the CSV is empty, and a
// null bitmap only when some column needs one.
func TestRefCohorts_RoundTrip(t *testing.T) {
	for _, path := range committedFixtures(t) {
		name := filepath.Base(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		tbl, err := parseCSV(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, err := os.ReadFile(strings.TrimSuffix(path, ".csv") + ".pulse")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r := bytes.NewReader(data)
		v, err := encoding.ReadHeader(r)
		if err != nil {
			t.Fatalf("%s header: %v", name, err)
		}
		schema, err := encoding.ReadSchema(r, v)
		if err != nil {
			t.Fatalf("%s schema: %v", name, err)
		}
		if len(schema.Fields) != len(tbl.names) {
			t.Fatalf("%s: %d fields, CSV has %d columns", name, len(schema.Fields), len(tbl.names))
		}
		anyNull := false
		for i, f := range schema.Fields {
			if f.Name != tbl.names[i] || f.Type != encoding.FieldTypeF64 {
				t.Errorf("%s field %d = %s/%v, want %s/f64", name, i, f.Name, f.Type, tbl.names[i])
			}
			anyNull = anyNull || f.Nullable
		}
		bm := schema.BitmapByteSize()
		if (bm > 0) != anyNull {
			t.Errorf("%s: bitmap size %d with nullable=%v", name, bm, anyNull)
		}
		for ri, row := range tbl.rows {
			vals := make([]uint64, len(row))
			for fi := range schema.Fields {
				if vals[fi], err = encoding.ReadFieldValue(r, encoding.FieldTypeF64); err != nil {
					t.Fatalf("%s row %d: %v", name, ri, err)
				}
			}
			var bitmap []byte
			if bm > 0 {
				if bitmap, err = encoding.ReadBitmap(r, bm); err != nil {
					t.Fatalf("%s row %d bitmap: %v", name, ri, err)
				}
			}
			for fi, want := range row {
				isNull := bm > 0 && encoding.BitmapIsNull(bitmap, fi)
				if math.IsNaN(want) != isNull {
					t.Errorf("%s row %d %s: null=%v, CSV empty=%v", name, ri, tbl.names[fi], isNull, math.IsNaN(want))
					continue
				}
				if !isNull && vals[fi] != math.Float64bits(want) {
					t.Errorf("%s row %d %s = %v, want %v", name, ri, tbl.names[fi], math.Float64frombits(vals[fi]), want)
				}
			}
		}
		if r.Len() != 0 {
			t.Errorf("%s: %d trailing bytes after %d records", name, r.Len(), len(tbl.rows))
		}
	}
}

// fixtureManifest is mv_fixtures.json, the generator's list of every
// fixture it wrote.
type fixtureManifest struct {
	Cases []struct {
		Name     string   `json:"name"`
		Source   string   `json:"source"`
		Citation string   `json:"citation"`
		Rows     int      `json:"rows"`
		Columns  []string `json:"columns"`
		CSVMD5   string   `json:"csv_md5"`
	} `json:"cases"`
}

// TestRefCohorts_FixtureManifest pins the CSVs to the generator's own
// record of them: every CSV is listed with its md5, row count and
// columns (so a hand-edited fixture fails even though the .pulse twin
// was rebuilt from it), the two vendored datasets carry their
// citations, and nothing is drawn from a package other than R's
// public-domain `datasets` (no GPL-package data such as psych::bfi).
func TestRefCohorts_FixtureManifest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(referenceDir, "mv_fixtures.json"))
	if err != nil {
		t.Fatalf("%v (regenerate with `make reference`)", err)
	}
	if i := bytes.LastIndex(raw, []byte("\n// golden-hash: ")); i >= 0 {
		raw = raw[:i]
	}
	var m fixtureManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("mv_fixtures.json: %v", err)
	}
	listed := map[string]bool{}
	for _, c := range m.Cases {
		listed[c.Name] = true
		path := filepath.Join(fixtureDir, c.Name+".csv")
		csv, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		sum := md5.Sum(csv)
		if hex.EncodeToString(sum[:]) != c.CSVMD5 {
			t.Errorf("%s.csv md5 differs from mv_fixtures.json (hand-edited? regenerate with `make reference`)", c.Name)
		}
		tbl, err := parseCSV(csv)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if len(tbl.rows) != c.Rows || strings.Join(tbl.names, ",") != strings.Join(c.Columns, ",") {
			t.Errorf("%s: %d rows %v, manifest says %d rows %v", c.Name, len(tbl.rows), tbl.names, c.Rows, c.Columns)
		}
		switch {
		case c.Source == "synthetic":
		case strings.HasPrefix(c.Source, "datasets::"):
			if c.Citation == "" || strings.HasPrefix(c.Citation, "none") {
				t.Errorf("%s: vendored dataset without a citation", c.Name)
			}
			if !bytes.Contains(csv, []byte("# citation: "+c.Citation)) {
				t.Errorf("%s.csv: citation header missing", c.Name)
			}
		default:
			t.Errorf("%s: source %q is neither synthetic nor R's datasets package", c.Name, c.Source)
		}
	}
	for _, path := range committedFixtures(t) {
		if n := strings.TrimSuffix(filepath.Base(path), ".csv"); !listed[n] {
			t.Errorf("%s.csv is not listed in mv_fixtures.json", n)
		}
	}
	for _, want := range []string{"attitude", "mtcars"} {
		if !listed[want] {
			t.Errorf("vendored fixture %s missing", want)
		}
	}
}

func TestConvert(t *testing.T) {
	cases := []struct {
		name    string
		csv     string
		wantErr string
		bitmap  bool
	}{
		{name: "complete", csv: "# c\na,b\n1,2\n3.5,-4\n"},
		{name: "null cell makes the column nullable", csv: "a,b\n1,\n3,4\n", bitmap: true},
		{name: "no header", csv: "# only comments\n", wantErr: "no header"},
		{name: "no records", csv: "a,b\n", wantErr: "no records"},
		{name: "ragged row", csv: "a,b\n1\n", wantErr: "1 cells, want 2"},
		{name: "not a number", csv: "a\nx\n", wantErr: "not a finite number"},
		{name: "infinite", csv: "a\nInf\n", wantErr: "not a finite number"},
		{name: "NaN literal", csv: "a\nNaN\n", wantErr: "not a finite number"},
		{name: "duplicate column", csv: "a,a\n1,2\n", wantErr: "duplicate column"},
		{name: "empty column name", csv: "a,\n1,2\n", wantErr: "empty column name"},
		{name: "blank record", csv: "a\n1\n\n2\n", wantErr: "blank record"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := convert([]byte(tc.csv))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r := bytes.NewReader(out)
			v, err := encoding.ReadHeader(r)
			if err != nil {
				t.Fatal(err)
			}
			s, err := encoding.ReadSchema(r, v)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.BitmapByteSize() > 0; got != tc.bitmap {
				t.Errorf("bitmap = %v, want %v", got, tc.bitmap)
			}
			if !tc.bitmap {
				// Two f64 rows, no bitmap: exactly 32 record bytes.
				if r.Len() != 2*8*len(s.Fields) {
					t.Errorf("record bytes = %d", r.Len())
				}
			}
		})
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir); err == nil || !strings.Contains(err.Error(), "no fixture CSVs") {
		t.Fatalf("empty dir err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.csv"), []byte("x\n1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := convert([]byte("x\n1\n"))
	if !bytes.Equal(got, want) {
		t.Error("run wrote different bytes than convert")
	}
	if err := os.WriteFile(filepath.Join(dir, "b.csv"), []byte("x\nzz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(dir); err == nil || !strings.Contains(err.Error(), "b.csv") {
		t.Fatalf("bad csv err = %v", err)
	}
}
