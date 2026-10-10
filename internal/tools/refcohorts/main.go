// Command refcohorts converts the R oracle's vendored fixture CSVs into
// the `.pulse` cohorts service tests read. `make reference` runs it
// after scripts/reference/gen_reference.R has written the CSVs; it is a
// development tool only (never imported, never shipped).
//
//	go run ./internal/tools/refcohorts -dir internal/processing/testdata/reference/fixtures
//
// The CSV dialect is the one the generator writes and nothing more:
// leading `#` lines are the citation header (skipped), the first other
// line names the columns, every cell is a decimal number written with
// `%.17g` (so it round-trips the double R held) and an EMPTY cell is
// null. Every column becomes an `f64` field; a column is nullable iff
// it holds at least one empty cell, so a complete fixture writes no
// null bitmap. The output is a pure function of the CSV bytes, which is
// what lets TestRefCohorts_CommittedPulseCurrent prove a rerun changes
// nothing.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
)

func main() {
	dir := flag.String("dir", "internal/processing/testdata/reference/fixtures", "directory of fixture CSVs; each <name>.csv writes <name>.pulse beside it")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "refcohorts:", err)
		os.Exit(1)
	}
}

// run converts every *.csv in dir, in name order.
func run(dir string) error {
	csvs, err := fixtureCSVs(dir)
	if err != nil {
		return err
	}
	if len(csvs) == 0 {
		return fmt.Errorf("no fixture CSVs under %s", dir)
	}
	for _, path := range csvs {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := convert(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		dst := strings.TrimSuffix(path, ".csv") + ".pulse"
		if err := os.WriteFile(dst, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// fixtureCSVs lists dir's *.csv files, sorted.
func fixtureCSVs(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// table is a parsed fixture: column names and row-major values, NaN
// marking a null cell.
type table struct {
	names []string
	rows  [][]float64
}

// parseCSV reads the generator's dialect (see the package comment).
func parseCSV(raw []byte) (*table, error) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	var t *table
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if t == nil {
			if strings.HasPrefix(text, "#") {
				continue
			}
			names := strings.Split(text, ",")
			seen := make(map[string]bool, len(names))
			for _, n := range names {
				if n == "" {
					return nil, fmt.Errorf("line %d: empty column name", line)
				}
				if seen[n] {
					return nil, fmt.Errorf("line %d: duplicate column %q", line, n)
				}
				seen[n] = true
			}
			t = &table{names: names}
			continue
		}
		if text == "" {
			return nil, fmt.Errorf("line %d: blank record", line)
		}
		cells := strings.Split(text, ",")
		if len(cells) != len(t.names) {
			return nil, fmt.Errorf("line %d: %d cells, want %d", line, len(cells), len(t.names))
		}
		row := make([]float64, len(cells))
		for i, c := range cells {
			if c == "" {
				row[i] = math.NaN()
				continue
			}
			v, err := strconv.ParseFloat(c, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("line %d column %q: %q is not a finite number", line, t.names[i], c)
			}
			row[i] = v
		}
		t.rows = append(t.rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("no header line")
	}
	if len(t.rows) == 0 {
		return nil, fmt.Errorf("no records")
	}
	return t, nil
}

// schemaFor builds the all-f64 schema; a column is nullable iff it has
// a null cell.
func schemaFor(t *table) *encoding.Schema {
	fields := make([]encoding.Field, len(t.names))
	for i, n := range t.names {
		nullable := false
		for _, r := range t.rows {
			if math.IsNaN(r[i]) {
				nullable = true
				break
			}
		}
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: 8 * i, CsvColumnIdx: i, Nullable: nullable}
	}
	return &encoding.Schema{Fields: fields}
}

// convert turns one fixture CSV into `.pulse` bytes.
func convert(raw []byte) ([]byte, error) {
	t, err := parseCSV(raw)
	if err != nil {
		return nil, err
	}
	schema := schemaFor(t)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		return nil, err
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return nil, err
	}
	bm := schema.BitmapByteSize()
	for _, r := range t.rows {
		bitmap := make([]byte, bm)
		for i, v := range r {
			bits := uint64(0)
			if math.IsNaN(v) {
				encoding.BitmapSetNull(bitmap, i)
			} else {
				bits = math.Float64bits(v)
			}
			if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, bits); err != nil {
				return nil, err
			}
		}
		if bm == 0 {
			continue
		}
		if err := encoding.WriteBitmap(&buf, bitmap); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
