package fingerprint_test

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// generatedCohorts maps a cohort filename (as named in a request) to a
// builder of its bytes, for a shape no in-repo fixture has. Each is
// derived from a checked-in fixture's CSV, so its records are fixed.
var generatedCohorts = map[string]func(*testing.T) []byte{
	"mtcars_am.pulse":     mtcarsAMCohort,
	"mtcars_groups.pulse": mtcarsGroupsCohort,
	"mtcars_rm.pulse":     mtcarsRMCohort,
}

// mtcarsAMCohort is mtcars' mpg (f64), am as a categorical_u8 split
// ("0" automatic, "1" manual) and w_prob (f64): the two-sample (Welch)
// TEST_T shape, which needs a categorical split_by mtcars lacks.
func mtcarsAMCohort(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../processing/testdata/reference/fixtures/mtcars.csv")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	var header []string
	var rows [][]string
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, ",")
		if header == nil {
			header = cells
			continue
		}
		rows = append(rows, cells)
	}
	col := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		t.Fatalf("mtcars.csv has no column %q", name)
		return -1
	}
	mpg, am, wp := col("mpg"), col("am"), col("w_prob")

	dict := encoding.NewDictionary()
	for _, v := range []string{"0", "1"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "mpg", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "am", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, Dictionary: dict},
		{Name: "w_prob", Type: encoding.FieldTypeF64, ByteOffset: 9},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("mtcars.csv: %v", err)
		}
		return v
	}
	row := make([]byte, 17)
	for _, r := range rows {
		binary.LittleEndian.PutUint64(row[0:], math.Float64bits(num(r[mpg])))
		switch r[am] {
		case "0":
			row[8] = 0
		case "1":
			row[8] = 1
		default:
			t.Fatalf("mtcars.csv: am %q", r[am])
		}
		binary.LittleEndian.PutUint64(row[9:], math.Float64bits(num(r[wp])))
		buf.Write(row)
	}
	return buf.Bytes()
}

// genColumn is one column of a generated cohort: an f64 when levels is
// nil, else a categorical_u8 over levels (in that dictionary order).
type genColumn struct {
	name   string
	levels []string
}

// readMtcarsCSV returns the mtcars fixture's header and data rows.
func readMtcarsCSV(t *testing.T) ([]string, [][]string) {
	t.Helper()
	raw, err := os.ReadFile("../processing/testdata/reference/fixtures/mtcars.csv")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	var header []string
	var rows [][]string
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, ",")
		if header == nil {
			header = cells
			continue
		}
		rows = append(rows, cells)
	}
	return header, rows
}

// buildCohort writes a .pulse cohort of cols over rows (each row one
// cell string per column, in cols order).
func buildCohort(t *testing.T, cols []genColumn, rows [][]string) []byte {
	t.Helper()
	schema := &encoding.Schema{}
	offset := 0
	for _, c := range cols {
		f := encoding.Field{Name: c.name, Type: encoding.FieldTypeF64, ByteOffset: offset}
		width := 8
		if c.levels != nil {
			dict := encoding.NewDictionary()
			for _, v := range c.levels {
				if _, err := dict.Add(v); err != nil {
					t.Fatal(err)
				}
			}
			f.Type, f.Dictionary, width = encoding.FieldTypeCategoricalU8, dict, 1
		}
		schema.Fields = append(schema.Fields, f)
		offset += width
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, offset)
	for _, r := range rows {
		at := 0
		for i, c := range cols {
			if c.levels == nil {
				v, err := strconv.ParseFloat(r[i], 64)
				if err != nil {
					t.Fatalf("column %s: %v", c.name, err)
				}
				binary.LittleEndian.PutUint64(row[at:], math.Float64bits(v))
				at += 8
				continue
			}
			idx := -1
			for j, l := range c.levels {
				if l == r[i] {
					idx = j
				}
			}
			if idx < 0 {
				t.Fatalf("column %s: level %q not in %v", c.name, r[i], c.levels)
			}
			row[at] = byte(idx)
			at++
		}
		buf.Write(row)
	}
	return buf.Bytes()
}

// mtcarsGroupsCohort is mtcars' continuous columns (f64) beside cyl,
// am and gear as categorical_u8 splits: the k-sample (ANOVA, Brown-
// Forsythe, MANOVA, Box's M), two-sample (z, CUPED) and multivariate
// (Mardia, Bartlett) test shapes.
func mtcarsGroupsCohort(t *testing.T) []byte {
	t.Helper()
	header, rows := readMtcarsCSV(t)
	cols := []genColumn{
		{name: "mpg"}, {name: "disp"}, {name: "hp"}, {name: "drat"}, {name: "wt"}, {name: "qsec"},
		{name: "w_freq"}, {name: "w_prob"},
		{name: "cyl", levels: []string{"4", "6", "8"}},
		{name: "am", levels: []string{"0", "1"}},
		{name: "gear", levels: []string{"3", "4", "5"}},
	}
	idx := make([]int, len(cols))
	for i, c := range cols {
		idx[i] = -1
		for j, h := range header {
			if h == c.name {
				idx[i] = j
			}
		}
		if idx[i] < 0 {
			t.Fatalf("mtcars.csv has no column %q", c.name)
		}
	}
	out := make([][]string, len(rows))
	for r, row := range rows {
		out[r] = make([]string, len(cols))
		for i := range cols {
			out[r][i] = row[idx[i]]
		}
	}
	return buildCohort(t, cols, out)
}

// mtcarsRMCohort is a balanced repeated-measures layout derived from
// mtcars: subject s (8 levels) × condition c (3 levels), y = the mpg of
// mtcars row 3·s + c — the TEST_ANOVA_RM shape.
func mtcarsRMCohort(t *testing.T) []byte {
	t.Helper()
	header, rows := readMtcarsCSV(t)
	mpg := -1
	for j, h := range header {
		if h == "mpg" {
			mpg = j
		}
	}
	subjects := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}
	conds := []string{"c1", "c2", "c3"}
	var out [][]string
	for s := range subjects {
		for c := range conds {
			out = append(out, []string{subjects[s], conds[c], rows[3*s+c][mpg]})
		}
	}
	return buildCohort(t, []genColumn{
		{name: "subject", levels: subjects},
		{name: "cond", levels: conds},
		{name: "y"},
	}, out)
}
