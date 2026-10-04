package embeddersmoke

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// Runtime flows an external embedder drives, all on an in-memory
// afero filesystem handed to pulse.Options.FS.

const salesCSV = "region,amount\nnorth,10\nsouth,20\nnorth,5\n"

// newEngine builds an engine over a fresh MemMap fs.
func newEngine(t *testing.T, opts pulse.Options) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, fs
}

// ingest imports salesCSV (from bytes) into cohort on the engine's fs.
func ingest(t *testing.T, p *pulse.Pulse, fs afero.Fs, cohort string) {
	t.Helper()
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(salesCSV), pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReaderFromBytes: %v", err)
	}
	job := pio.NewImportJob(r, cohort)
	job.FS = fs
	rep, err := p.Import(context.Background(), job)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if rep.RowsImported != 3 {
		t.Fatalf("RowsImported = %d, want 3", rep.RowsImported)
	}
}

func sumByRegion(cohort string) *pulse.Request {
	return &pulse.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
	}
}

// totals flattens a grouped response to region -> total.
func totals(t *testing.T, resp *pulse.Response) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, row := range resp.Data {
		region, _ := row["region"].(string)
		var v float64
		switch x := row["total"].(type) {
		case float64:
			v = x
		case int64:
			v = float64(x)
		case int:
			v = float64(x)
		default:
			t.Fatalf("total has type %T in row %v", row["total"], row)
		}
		out[region] = v
	}
	return out
}

func TestIngestFromFileAndBytes(t *testing.T) {
	p, fs := newEngine(t, pulse.Options{})
	ingest(t, p, fs, "bytes.pulse")

	if err := afero.WriteFile(fs, "src/sales.csv", []byte(salesCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pio.FormatFromPath("src/sales.csv"); got != pio.FormatCSV {
		t.Fatalf("FormatFromPath = %q, want %q", got, pio.FormatCSV)
	}
	r, err := pio.NewReader(pio.FormatCSV, fs, "src/sales.csv", pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	job := pio.NewImportJob(r, "file.pulse")
	job.FS = fs
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("Import: %v", err)
	}
	n, err := p.CountRecords(context.Background(), "file.pulse")
	if err != nil || n != 3 {
		t.Fatalf("CountRecords = %d, %v; want 3", n, err)
	}
}

func TestQueryProcessAndCompose(t *testing.T) {
	tests := []struct {
		name string
		opts pulse.Options
	}{
		{"defaults", pulse.Options{}},
		{"crosstab fusion disabled", pulse.Options{DisableCrosstabFusion: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, fs := newEngine(t, tc.opts)
			ingest(t, p, fs, "sales.pulse")
			ctx := context.Background()

			resp, err := p.Process(ctx, sumByRegion("sales.pulse"))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			got := totals(t, resp)
			if got["north"] != 15 || got["south"] != 20 {
				t.Fatalf("totals = %v, want north=15 south=20", got)
			}

			creq := &pulse.ComposedRequest{Requests: []*types.Request{
				sumByRegion("sales.pulse"), sumByRegion("sales.pulse"),
			}}
			seq, err := p.Compose(ctx, creq)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			par, err := p.ComposeParallel(ctx, creq, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			if err != nil {
				t.Fatalf("ComposeParallel: %v", err)
			}
			for name, cr := range map[string]*pulse.ComposedResponse{"Compose": seq, "ComposeParallel": par} {
				if len(cr.Responses) != 2 {
					t.Fatalf("%s: %d responses, want 2", name, len(cr.Responses))
				}
				if got := totals(t, cr.Responses[1]); got["north"] != 15 {
					t.Fatalf("%s: totals = %v", name, got)
				}
			}
		})
	}
}

func TestInspectPredictAndArtifacts(t *testing.T) {
	p, fs := newEngine(t, pulse.Options{})
	ingest(t, p, fs, "sales.pulse")
	ctx := context.Background()
	data, err := afero.ReadFile(fs, "sales.pulse")
	if err != nil {
		t.Fatal(err)
	}

	env, err := p.InspectBytes(ctx, data, &descriptor.InspectOptions{FullDict: true})
	if err != nil {
		t.Fatalf("InspectBytes: %v", err)
	}
	ir, ok := env.Data.(*descriptor.InspectResult)
	if !ok {
		t.Fatalf("InspectBytes data is %T, want *descriptor.InspectResult", env.Data)
	}
	if ir.RecordCount != 3 || ir.FieldCount != 2 {
		t.Fatalf("inspect = %d records / %d fields, want 3 / 2", ir.RecordCount, ir.FieldCount)
	}

	penv, err := p.PredictBytes(ctx, data, sumByRegion("sales.pulse"))
	if err != nil {
		t.Fatalf("PredictBytes: %v", err)
	}
	pr, ok := penv.Data.(*descriptor.PredictResult)
	if !ok {
		t.Fatalf("PredictBytes data is %T, want *descriptor.PredictResult", penv.Data)
	}
	if !pr.Valid {
		t.Fatalf("predict invalid: errors=%v", penv.Errors)
	}

	var bir *pulse.BuildIndexResult
	if bir, err = p.BuildIndex(ctx, "sales.pulse", []string{"region"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	fp, keys, rowIDs := walkSidecarIndex(bir.Index)
	if fp == (pulse.CohortFingerprint{}) {
		t.Fatal("SidecarIndex carries a zero cohort fingerprint")
	}
	if len(keys) != 1 || keys[0] != "region" {
		t.Fatalf("SidecarIndex keys = %v, want [region]", keys)
	}
	if len(rowIDs) != 3 {
		t.Fatalf("SidecarIndex walked %d row IDs, want 3", len(rowIDs))
	}
	arts, err := p.CohortArtifacts(ctx, "sales.pulse")
	if err != nil {
		t.Fatalf("CohortArtifacts: %v", err)
	}
	if !contains(arts, bir.IndexPath) || !contains(arts, bir.ManifestPath) {
		t.Fatalf("CohortArtifacts = %v, want index %q and manifest %q", arts, bir.IndexPath, bir.ManifestPath)
	}
}

func TestExportConversion(t *testing.T) {
	p, fs := newEngine(t, pulse.Options{})
	ingest(t, p, fs, "sales.pulse")
	ctx := context.Background()

	buf, err := pio.NewWriterToBuffer(pio.FormatNDJSON, pio.WriterOptions{})
	if err != nil {
		t.Fatalf("NewWriterToBuffer: %v", err)
	}
	job := pio.NewExportJob("sales.pulse", buf)
	job.FS = fs
	rep, err := p.Export(ctx, job)
	if err != nil {
		t.Fatalf("Export(buffer): %v", err)
	}
	if err := buf.Close(); err != nil {
		t.Fatalf("buffer Close: %v", err)
	}
	if rep.RowsExported != 3 || strings.Count(string(buf.Bytes()), "\n") != 3 {
		t.Fatalf("buffer export = %d rows, %q", rep.RowsExported, buf.Bytes())
	}

	w, err := pio.NewWriter(pio.FormatTSV, fs, "out/sales.tsv", pio.WriterOptions{})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	fjob := pio.NewExportJob("sales.pulse", w)
	fjob.FS = fs
	if _, err := p.Export(ctx, fjob); err != nil {
		t.Fatalf("Export(file): %v", err)
	}
	// The caller owns the writer: Close flushes it to the path.
	if err := w.Close(); err != nil {
		t.Fatalf("writer Close: %v", err)
	}
	out, err := afero.ReadFile(fs, "out/sales.tsv")
	if err != nil || !strings.HasPrefix(string(out), "region\tamount") {
		t.Fatalf("tsv export = %q, %v", out, err)
	}
}

// TestRawUngroupedWrite builds a .pulse by hand from the encoding
// primitives, then reads its geometry back and runs it through the
// facade.
func TestRawUngroupedWrite(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU16, ByteOffset: 0},
		{Name: "b", Type: encoding.FieldTypeU32, ByteOffset: 2},
	}}
	if schema.RequiredFormatVersion() != encoding.FormatVersionV1 {
		t.Fatalf("ungrouped schema wants version %d", schema.RequiredFormatVersion())
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	rows := [][2]uint64{{1, 100}, {2, 200}, {3, 300}}
	for _, r := range rows {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU16, r[0]); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, r[1]); err != nil {
			t.Fatal(err)
		}
	}
	data := buf.Bytes()

	loc, err := encoding.NewRecordLocator(bytes.NewReader(data), schema)
	if err != nil {
		t.Fatalf("NewRecordLocator: %v", err)
	}
	if loc.TotalRecords != 3 || loc.Stride != int64(schema.RecordByteSize()) {
		t.Fatalf("locator = %d records stride %d", loc.TotalRecords, loc.Stride)
	}
	off := loc.Offset(1)
	v, err := encoding.ReadFieldValue(bytes.NewReader(data[off+2:]), encoding.FieldTypeU32)
	if err != nil || v != 200 {
		t.Fatalf("record 1 b = %d, %v; want 200", v, err)
	}

	p, fs := newEngine(t, pulse.Options{})
	if err := afero.WriteFile(fs, "raw.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(context.Background(), &pulse.Request{
		Cohort:       &types.Cohort{Filename: "raw.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "b", Label: "total"}},
	})
	if err != nil {
		t.Fatalf("Process(raw): %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0]["total"] != float64(600) {
		t.Fatalf("raw sum = %v, want 600", resp.Data)
	}

	// Read the same cohort back record by record: exact uint64 values in
	// schema field order.
	c, err := p.Open(context.Background(), "raw.pulse")
	if err != nil {
		t.Fatalf("Open(raw): %v", err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatalf("Reader(raw): %v", err)
	}
	defer r.Close()
	if r.Len() != int64(len(rows)) || len(r.Schema().Fields) != 2 {
		t.Fatalf("reader Len %d fields %d", r.Len(), len(r.Schema().Fields))
	}
	for i, want := range rows {
		row, err := r.RecordAt(int64(i))
		if err != nil {
			t.Fatalf("RecordAt(%d): %v", i, err)
		}
		if row[0] != want[0] || row[1] != want[1] {
			t.Fatalf("RecordAt(%d) = %#v, want %v", i, row, want)
		}
	}
	if _, err := r.RecordAt(int64(len(rows))); !perrors.HasCode(err, perrors.SERVICE_VALIDATION) {
		t.Fatalf("out-of-range RecordAt error = %v, want SERVICE_VALIDATION", err)
	}
}

// TestCohortBuilderFlow builds a cohort row by row through the public
// builder, checks it is byte-identical to the raw-primitive write of
// the same rows, reads it back record by record and processes it.
func TestCohortBuilderFlow(t *testing.T) {
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU16, ByteOffset: 0, CsvColumnIdx: 0, Description: "Small counter a."},
		{Name: "b", Type: encoding.FieldTypeU32, ByteOffset: 2, CsvColumnIdx: 1, Description: "Wider counter b."},
	}}
	rows := []pulse.CohortRow{{uint64(1), uint64(100)}, {uint64(2), uint64(200)}, {uint64(3), uint64(300)}}

	var raw bytes.Buffer
	if err := encoding.WriteHeader(&raw); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := encoding.WriteFieldValue(&raw, encoding.FieldTypeU16, r[0].(uint64)); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteFieldValue(&raw, encoding.FieldTypeU32, r[1].(uint64)); err != nil {
			t.Fatal(err)
		}
	}

	p, fs := newEngine(t, pulse.Options{})
	ctx := context.Background()
	b, err := p.NewCohortBuilder(ctx, "built.pulse", schema, pulse.CohortBuilderOptions{Strict: true})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for _, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// A rejected row returns the import row code and is not written.
	if err := b.Append(pulse.CohortRow{uint64(70000), uint64(1)}); !perrors.HasCode(err, perrors.PULSE_IMPORT_ROW_ERROR) {
		t.Fatalf("overflowing row = %v, want PULSE_IMPORT_ROW_ERROR", err)
	}
	res, err := b.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if res.Records != 3 || res.FormatVersion != encoding.FormatVersionV1 {
		t.Fatalf("result = %+v", res)
	}
	built, err := afero.ReadFile(fs, "built.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, raw.Bytes()) {
		t.Fatalf("builder bytes differ from the raw-primitive write")
	}

	c, err := p.Open(ctx, "built.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i, want := range rows {
		got, err := r.RecordAt(int64(i))
		if err != nil || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("RecordAt(%d) = %v, %v; want %v", i, got, err, want)
		}
	}
	resp, err := p.Process(ctx, &pulse.Request{
		Cohort:       &types.Cohort{Filename: "built.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "b", Label: "total"}},
	})
	if err != nil || len(resp.Data) != 1 || resp.Data[0]["total"] != float64(600) {
		t.Fatalf("Process(built) = %v, %v; want total 600", resp, err)
	}
}

// TestCohortBuilderGroupedFlow builds a grouped, constant-elided
// cohort through the public builder and checks it is byte-identical to
// an explicit-schema import with the same --group / --elide-constants,
// reads back the appended rows and processes like its ungrouped twin.
func TestCohortBuilderGroupedFlow(t *testing.T) {
	newSchema := func() encoding.Schema {
		return encoding.Schema{Fields: []encoding.Field{
			{Name: "order", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Description: "Order number, unique per row."},
			{Name: "cust", Type: encoding.FieldTypeU32, ByteOffset: 4, CsvColumnIdx: 1, Description: "Customer number, the group key."},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 2, Description: "Customer score, set by the key."},
			{Name: "site", Type: encoding.FieldTypeU16, ByteOffset: 16, CsvColumnIdx: 3, Description: "Site code, the same on every row."},
		}}
	}
	var csv strings.Builder
	csv.WriteString("order,cust,score,site\n")
	var rows []pulse.CohortRow
	scores := []float64{1.5, 2.5, 4}
	for i := 0; i < 90; i++ {
		c := i % len(scores)
		rows = append(rows, pulse.CohortRow{uint64(i + 1), uint64(10 + c), scores[c], uint64(7)})
		csv.WriteString(strings.Join([]string{itoa(i + 1), itoa(10 + c), []string{"1.5", "2.5", "4"}[c], "7"}, ",") + "\n")
	}
	groups := []pio.GroupDecl{{Key: []string{"cust"}, Members: []string{"score"}}}

	p, fs := newEngine(t, pulse.Options{})
	ctx := context.Background()
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(csv.String()), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := newSchema()
	job := pio.NewImportJob(src, "imported.pulse")
	job.Schema, job.Groups, job.ElideConstants = &s, groups, true
	if _, err := p.Import(ctx, job); err != nil {
		t.Fatalf("Import: %v", err)
	}

	build := func(target string, opts pulse.CohortBuilderOptions) *pulse.CohortBuildResult {
		b, err := p.NewCohortBuilder(ctx, target, newSchema(), opts)
		if err != nil {
			t.Fatalf("NewCohortBuilder: %v", err)
		}
		for _, r := range rows {
			if err := b.Append(r); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		res, err := b.Close()
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		return res
	}
	res := build("grouped.pulse", pulse.CohortBuilderOptions{Groups: groups, ElideConstants: true, Strict: true})
	build("flat.pulse", pulse.CohortBuilderOptions{})
	if res.FormatVersion != encoding.FormatVersionV2 || len(res.ElidedConstants) != 1 || res.ElidedConstants[0] != "site" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Groups) != 1 || res.Groups[0].Verdict != "admitted" {
		t.Fatalf("group reports = %+v", res.Groups)
	}
	built, _ := afero.ReadFile(fs, "grouped.pulse")
	imported, _ := afero.ReadFile(fs, "imported.pulse")
	if len(built) == 0 || !bytes.Equal(built, imported) {
		t.Fatalf("grouped build (%d bytes) differs from the grouped import (%d bytes)", len(built), len(imported))
	}

	c, err := p.Open(ctx, "grouped.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i, want := range rows {
		got, err := r.RecordAt(int64(i))
		if err != nil || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
			t.Fatalf("RecordAt(%d) = %v, %v; want %v", i, got, err, want)
		}
	}
	sum := func(path string) any {
		resp, err := p.Process(ctx, &pulse.Request{
			Cohort:       &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "total"}},
		})
		if err != nil || len(resp.Data) != 1 {
			t.Fatalf("Process(%s) = %v, %v", path, resp, err)
		}
		return resp.Data[0]["total"]
	}
	if g, f := sum("grouped.pulse"), sum("flat.pulse"); g != f || g != float64(240) {
		t.Fatalf("sum over grouped = %v, flat twin = %v, want 240", g, f)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestSynthFixture(t *testing.T) {
	p, fs := newEngine(t, pulse.Options{})
	spec := &synth.Spec{RowCount: 25, Fields: []synth.FieldSpec{
		{Name: "x", Type: "u16", Distribution: "uniform", Params: map[string]any{"min": 0.0, "max": 100.0}},
	}}
	res, err := synth.Synth(fs, spec, "synth.pulse", synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("synth.Synth: %v", err)
	}
	if res.RowsGenerated != 25 {
		t.Fatalf("RowsGenerated = %d", res.RowsGenerated)
	}
	if n, err := p.CountRecords(context.Background(), "synth.pulse"); err != nil || n != 25 {
		t.Fatalf("CountRecords = %d, %v", n, err)
	}
	raw, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil || len(raw) == 0 {
		t.Fatalf("SynthBytes: %d bytes, %v", len(raw), err)
	}
}

func TestMCPMount(t *testing.T) {
	p, _ := newEngine(t, pulse.Options{})
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "embedder-host", Version: "0.0.1"}, nil)
	if err := gosdk.Register(server, p, gosdk.Config{DisableCohortScan: true}); err != nil {
		t.Fatalf("gosdk.Register: %v", err)
	}
	if len(gosdk.RegisteredTools()) == 0 {
		t.Fatal("no tools registered")
	}
}

func TestCodedErrorsAndEnvelope(t *testing.T) {
	p, _ := newEngine(t, pulse.Options{})
	_, err := p.Process(context.Background(), sumByRegion("missing.pulse"))
	if err == nil {
		t.Fatal("Process on a missing cohort succeeded")
	}
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %T is not a *errors.CodedError: %v", err, err)
	}
	meta, ok := perrors.Lookup(string(ce.Code))
	if !ok || meta.Message == "" {
		t.Fatalf("Lookup(%q) = %+v, %v", ce.Code, meta, ok)
	}

	env := descriptor.NewEnvelope(map[string]string{"k": "v"})
	if env.FormatVersion == "" || env.Errors == nil || env.Warnings == nil {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestVersionAndMemberSet(t *testing.T) {
	if pulse.Version() == "" {
		t.Fatal("pulse.Version() is empty")
	}
	dict := encoding.NewDictionary()
	for _, v := range []string{"north", "south"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}}
	res, err := pulse.LoadMemberSetFromReader(strings.NewReader("north\nwest\n"), schema, "region")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	var set pulse.MemberSet = res.Set
	if set == nil || set.Len() != 1 || res.NotInDictionary != 1 {
		t.Fatalf("member set = %+v", res)
	}

	p, _ := newEngine(t, pulse.Options{Extensions: pulse.Extensions{
		RangeTables: map[string]pulse.RangeTable{"halves": {Ranges: []pulse.DateRangeSpec{
			{Label: "h1", Start: ptr("2025-01-01"), End: ptr("2025-06-30")},
			{Label: "h2", Start: ptr("2025-07-01"), End: ptr("2025-12-31")},
		}}},
	}})
	if tables := p.RangeTables(); len(tables) != 1 || len(tables[0].Ranges) != 2 {
		t.Fatalf("RangeTables = %+v", tables)
	}
}

func ptr(s string) *string { return &s }

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestTemplateConstants loads one template from disk and matches its
// target and variable type against the root constants.
func TestTemplateConstants(t *testing.T) {
	dir := t.TempDir()
	doc := `{"target": "request",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": {"cohort": {"filename": "sales.pulse"},
    "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}}]}}`
	if err := os.WriteFile(filepath.Join(dir, "revenue.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := newEngine(t, pulse.Options{TemplateDirs: []string{dir}})
	tpl, err := p.GetTemplate("revenue")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if tpl.Target != pulse.TemplateTargetRequest || tpl.Variables[0].Type != pulse.TemplateVarField {
		t.Fatalf("template = target %q, var type %q", tpl.Target, tpl.Variables[0].Type)
	}
	rendered, err := p.RenderTemplate("revenue", map[string]any{"metric": "amount"})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if rendered.Target != pulse.TemplateTargetRequest || rendered.Request == nil {
		t.Fatalf("rendered = %+v", rendered)
	}
}
