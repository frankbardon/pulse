package pulse_test

// Runnable GoDoc examples for moving data in and out of Pulse: import,
// export, point lookups and synthetic cohorts. TestFacadeExamplesPresent
// pins the set.

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Import CSV bytes into a new cohort. Field types are inferred from the
// data at the narrowest width that holds every value; the report carries
// the schema that was written.
func ExamplePulse_Import() {
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(exampleCSV), pio.ReaderOptions{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	job := pio.NewImportJob(src, "imported.pulse")
	job.FS = fsys
	rep, err := p.Import(context.Background(), job)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("rows:", rep.RowsImported)
	for _, f := range rep.Schema.Fields {
		fmt.Println(f.Name, f.Type)
	}
	// Output:
	// rows: 6
	// id u4
	// region categorical_u8
	// channel categorical_u8
	// amount u8
}

// Export a cohort to NDJSON in memory. The caller owns the writer:
// Close flushes it.
func ExamplePulse_Export() {
	p, fsys := newExampleEngine(pulse.Options{})
	buf, err := pio.NewWriterToBuffer(pio.FormatNDJSON, pio.WriterOptions{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	job := pio.NewExportJob("sales.pulse", buf)
	job.FS = fsys
	job.Includes = []string{"id", "region"}
	rep, err := p.Export(context.Background(), job)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := buf.Close(); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("rows:", rep.RowsExported)
	fmt.Print(string(buf.Bytes()))
	// Output:
	// rows: 6
	// {"id":1,"region":"north"}
	// {"id":2,"region":"south"}
	// {"id":3,"region":"north"}
	// {"id":4,"region":"east"}
	// {"id":5,"region":"south"}
	// {"id":6,"region":"north"}
}

// Build a sidecar index on a key column once, then resolve point
// lookups through it without scanning the cohort.
func ExamplePulse_Lookup() {
	p, _ := newExampleEngine(pulse.Options{})
	ctx := context.Background()
	if _, err := p.BuildIndex(ctx, "sales.pulse", []string{"id"}); err != nil {
		fmt.Println("error:", err)
		return
	}
	res, err := p.Lookup(ctx, &pulse.LookupRequest{
		Cohort:        &types.Cohort{Filename: "sales.pulse"},
		Field:         "id",
		Value:         "4",
		ReturnColumns: []string{"region", "amount"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, row := range res.Rows {
		fmt.Println(row["region"], row["amount"])
	}
	// Output:
	// east 15
}

// Generate a synthetic cohort from a spec. The seed fixes every draw,
// so the same spec and seed always write the same file.
func ExamplePulse_Synth() {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	ctx := context.Background()
	spec := &pulse.SynthSpec{RowCount: 100, Fields: []synth.FieldSpec{
		{Name: "score", Type: "u16", Distribution: "uniform", Params: map[string]any{"min": 0.0, "max": 100.0}},
	}}
	res, err := p.Synth(ctx, spec, "synth.pulse", pulse.SynthOptions{Seed: 7})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	n, err := p.CountRecords(ctx, "synth.pulse")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("generated:", res.RowsGenerated, "on disk:", n)
	// Output:
	// generated: 100 on disk: 100
}
