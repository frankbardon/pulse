package spss

// The baseline E3-S1 noted was missing.
//
// Every neighbouring adapter has one, and the `.sav` format has three data
// encodings that differ by construction rather than by tuning: an
// uncompressed section is eight bytes per element, a bytecode section is
// one command byte per element plus an escape payload for anything the
// commands cannot express, and ZSAV is that same command stream cut into
// blocks and deflated. There is no CI threshold on any of this — the point
// is to have a number to compare against when one of the three changes.
//
// The read benchmarks decode from a byte slice, so no filesystem is in the
// measurement. The write benchmarks run the whole cohort out through the
// encoder, which is what an export actually costs.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// benchSpec builds a fixture of n cases in the given encoding.
//
// The columns are chosen so the two compressed encodings are not trivially
// favoured: a column of small integers compresses to one command byte each,
// a column of full-precision doubles escapes to a payload every time, and a
// string column exercises the segment path. A fixture of only compressible
// values would report a speed-up the format does not have in general.
func benchSpec(n int, c spsstest.Compression) spsstest.Spec {
	spec := spsstest.Spec{
		Compression: c,
		Vars: []spsstest.Var{
			{Name: "ID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "SMALL", Print: spsstest.Format{Type: spsstest.FormatF, Width: 3}},
			{Name: "PRECISE", Print: spsstest.Format{Type: spsstest.FormatF, Width: 12, Decimals: 6}},
			{Name: "REGION", Width: 8, Measure: spsstest.MeasureNominal},
			{Name: "WHEN", Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"SMALL"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(0), Label: "None"},
				{Value: spsstest.Num(1), Label: "Some"},
				{Value: spsstest.Num(2), Label: "Many"},
			},
		}},
		CharacterEncoding: "UTF-8",
		Cases:             make([][]spsstest.Value, 0, n),
	}
	regions := []string{"north", "south", "east", "west"}
	day := savInstant(2024, 1, 1)
	for i := range n {
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i + 1)),
			spsstest.Num(float64(i % 3)),
			spsstest.Num(float64(i)*1.0000001 + 0.3333333),
			spsstest.Text(regions[i%len(regions)]),
			spsstest.Num(day + float64(i%365)*86400),
		})
	}
	return spec
}

// benchEncodings is the axis every benchmark below runs over.
var benchEncodings = []struct {
	name string
	comp spsstest.Compression
}{
	{"uncompressed", spsstest.CompressionNone},
	{"bytecode", spsstest.CompressionBytecode},
	{"zsav", spsstest.CompressionZSAV},
}

const benchCases = 5000

// BenchmarkSPSSRead measures a whole read — dictionary parse, data-section
// decode and row rendering — per encoding.
func BenchmarkSPSSRead(b *testing.B) {
	for _, e := range benchEncodings {
		spec := benchSpec(benchCases, e.comp)
		sav, err := spsstest.Build(spec)
		if err != nil {
			b.Fatalf("spsstest.Build: %v", err)
		}
		b.Run(e.name, func(b *testing.B) {
			b.SetBytes(int64(len(sav)))
			b.ReportAllocs()
			for b.Loop() {
				r := NewReaderFromBytes(sav)
				if _, err := r.ReadHeader(); err != nil {
					b.Fatalf("ReadHeader: %v", err)
				}
				if err := r.ReadRows(context.Background(), func([]string) error { return nil }); err != nil {
					b.Fatalf("ReadRows: %v", err)
				}
			}
		})
	}
}

// BenchmarkSPSSImport measures the whole shared import path, which is what
// `pulse import spss` costs: read, schema, encode the cohort and write the
// metadata sidecar.
func BenchmarkSPSSImport(b *testing.B) {
	for _, e := range benchEncodings {
		spec := benchSpec(benchCases, e.comp)
		sav, err := spsstest.Build(spec)
		if err != nil {
			b.Fatalf("spsstest.Build: %v", err)
		}
		b.Run(e.name, func(b *testing.B) {
			b.SetBytes(int64(len(sav)))
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				fs := afero.NewMemMapFs()
				if err := afero.WriteFile(fs, "in.sav", sav, 0o644); err != nil {
					b.Fatalf("writing the fixture: %v", err)
				}
				job := pio.NewImportJob(NewReader(fs, "in.sav"), "out"+strconv.Itoa(i)+".pulse")
				job.FS = fs
				if _, err := job.Run(context.Background()); err != nil {
					b.Fatalf("ImportJob.Run: %v", err)
				}
			}
		})
	}
}

// BenchmarkSPSSExport measures the write half, over the two encodings the
// writer emits. ZSAV emission is not implemented
// (PULSE_SPSS_COMPRESSION_UNSUPPORTED), so it has no arm here — which is
// itself worth seeing in the benchmark list rather than inferring.
func BenchmarkSPSSExport(b *testing.B) {
	fs := afero.NewMemMapFs()
	sav, err := spsstest.Build(benchSpec(benchCases, spsstest.CompressionBytecode))
	if err != nil {
		b.Fatalf("spsstest.Build: %v", err)
	}
	if err := afero.WriteFile(fs, "in.sav", sav, 0o644); err != nil {
		b.Fatalf("writing the fixture: %v", err)
	}
	job := pio.NewImportJob(NewReader(fs, "in.sav"), "cohort.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		b.Fatalf("ImportJob.Run: %v", err)
	}
	size, err := fs.Stat("cohort.pulse")
	if err != nil {
		b.Fatalf("Stat: %v", err)
	}

	for _, mode := range []struct {
		name string
		opts WriterOptions
	}{
		{"bytecode", WriterOptions{}},
		{"uncompressed", WriterOptions{Uncompressed: true}},
	} {
		b.Run(mode.name, func(b *testing.B) {
			b.SetBytes(size.Size())
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				w := NewWriter(fs, "out"+strconv.Itoa(i)+".sav", mode.opts)
				ej := pio.NewExportJob("cohort.pulse", w)
				ej.FS = fs
				if _, err := ej.Run(context.Background()); err != nil {
					b.Fatalf("ExportJob.Run: %v", err)
				}
				if err := w.Close(); err != nil {
					b.Fatalf("Close: %v", err)
				}
			}
		})
	}
}

// BenchmarkSPSSVeryLongStringRead is the one column shape whose cost is not
// linear in the case count: a very long string is reassembled from N
// physical segments per case, so it is measured on its own rather than
// being averaged into the mixed fixture above.
func BenchmarkSPSSVeryLongStringRead(b *testing.B) {
	const width = 600
	spec := longStringSpec(width)
	spec.Compression = spsstest.CompressionBytecode
	for i := range 2000 {
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i + 1)),
			spsstest.Text(strings.Repeat("x", 1+i%width)),
		})
	}
	sav, err := spsstest.Build(spec)
	if err != nil {
		b.Fatalf("spsstest.Build: %v", err)
	}
	b.SetBytes(int64(len(sav)))
	b.ReportAllocs()
	for b.Loop() {
		r := NewReaderFromBytes(sav)
		if _, err := r.ReadHeader(); err != nil {
			b.Fatalf("ReadHeader: %v", err)
		}
		if err := r.ReadRows(context.Background(), func([]string) error { return nil }); err != nil {
			b.Fatalf("ReadRows: %v", err)
		}
	}
}
