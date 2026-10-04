package embeddersmoke

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
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
