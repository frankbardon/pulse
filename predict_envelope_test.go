package pulse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// byteTallyFs sums, per file, every byte any Read or ReadAt returned —
// re-reads included — so a test can bound the total bytes a call pulled
// from a cohort, not just the furthest offset (an archive's central
// directory sits at its END, so readSpyFs' furthest-byte figure cannot
// tell a header-only archive read from a whole-file one).
type byteTallyFs struct {
	afero.Fs
	mu    sync.Mutex
	total map[string]int64
}

func newByteTallyFs(inner afero.Fs) *byteTallyFs {
	return &byteTallyFs{Fs: inner, total: map[string]int64{}}
}

func (s *byteTallyFs) add(name string, n int) {
	s.mu.Lock()
	s.total[name] += int64(n)
	s.mu.Unlock()
}

func (s *byteTallyFs) read(name string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total[name]
}

func (s *byteTallyFs) reset() {
	s.mu.Lock()
	s.total = map[string]int64{}
	s.mu.Unlock()
}

func (s *byteTallyFs) Open(name string) (afero.File, error) {
	f, err := s.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &byteTallyFile{File: f, fs: s, name: name}, nil
}

func (s *byteTallyFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := s.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &byteTallyFile{File: f, fs: s, name: name}, nil
}

type byteTallyFile struct {
	afero.File
	fs   *byteTallyFs
	name string
}

func (f *byteTallyFile) Read(b []byte) (int, error) {
	n, err := f.File.Read(b)
	f.fs.add(f.name, n)
	return n, err
}

func (f *byteTallyFile) ReadAt(b []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(b, off)
	f.fs.add(f.name, n)
	return n, err
}

// envelopeCohort writes a cohort of rows records — 16 described f64
// measures, one UNDESCRIBED f64 (so predict warns
// PULSE_FIELD_DESCRIPTION_LOW_QUALITY, which Strict promotes) and one
// categorical — and returns its header + schema length and its payload
// length.
func envelopeCohort(t *testing.T, fsys afero.Fs, path string, rows int) (head, payload int64) {
	t.Helper()
	s := &encoding.Schema{}
	for i := range 16 {
		s.Fields = append(s.Fields, encoding.Field{Name: "m" + strconv.Itoa(i), Type: encoding.FieldTypeF64, Description: "Measured amount number " + strconv.Itoa(i)})
	}
	s.Fields = append(s.Fields, encoding.Field{Name: "bare", Type: encoding.FieldTypeF64})
	d := encoding.NewDictionary()
	for _, v := range []string{"x", "y", "z"} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	s.Fields = append(s.Fields, encoding.Field{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Description: "Category label for the row", Dictionary: d})
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	head = int64(buf.Len())
	payload = int64(rows * s.RecordByteSize())
	buf.Write(make([]byte, payload))
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return head, payload
}

// envelopeFixture lays out single.pulse, s0.pulse / s1.pulse and the
// archive arch.pulse packing the two shards, on a byte-tallying fs. It
// returns the fs, the archive's non-payload byte count (its length
// minus both shards' record payloads) and the single file's header +
// schema length.
func envelopeFixture(t *testing.T, rows int) (spy *byteTallyFs, archiveOverhead, singleHead int64) {
	t.Helper()
	spy = newByteTallyFs(afero.NewMemMapFs())
	singleHead, _ = envelopeCohort(t, spy.Fs, "single.pulse", rows)
	_, p0 := envelopeCohort(t, spy.Fs, "s0.pulse", rows)
	_, p1 := envelopeCohort(t, spy.Fs, "s1.pulse", rows)
	p, err := New(Options{FS: spy.Fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", []string{"s0.pulse", "s1.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	fi, err := spy.Fs.Stat("arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return spy, fi.Size() - p0 - p1, singleHead
}

// cohortBytes is the byte image PredictBytes is handed for path: the
// whole file, or an anchor's one archive entry.
func cohortBytes(t *testing.T, fsys afero.Fs, path string) []byte {
	t.Helper()
	archivePath, entry, anchored := splitAnchorForTest(path)
	data, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !anchored {
		return data
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := zr.Open(entry)
	if err != nil {
		t.Fatalf("open entry %s: %v", entry, err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func splitAnchorForTest(path string) (archive, entry string, ok bool) {
	for i := range len(path) {
		if path[i] == '#' {
			return path[:i], path[i+1:], true
		}
	}
	return path, "", false
}

// TestPredictEnvelope_MatchesPredictBytes: over a single file, an
// anchored shard and a whole shard archive, PredictEnvelope(path)
// returns the envelope PredictBytes returns over the same bytes —
// data, errors, warnings and the echoed request — under lax and under
// Strict + EchoRequest instances, for a valid request (which warns on
// the undescribed field) and an invalid one (unknown field).
func TestPredictEnvelope_MatchesPredictBytes(t *testing.T) {
	spy, _, _ := envelopeFixture(t, 50)
	ctx := context.Background()
	reqs := map[string]func(cohort string) *types.Request{
		"valid": func(c string) *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: c},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "bare", Label: "s"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			}
		},
		"unknown field": func(c string) *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: c},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "nope", Label: "s"}},
			}
		},
	}
	instances := map[string]Options{
		"lax":         {},
		"strict+echo": {Strict: true, EchoRequest: true},
	}
	sawWarning, sawError, sawEcho := false, false, false
	for _, cohort := range []string{"single.pulse", "arch.pulse#s0.pulse", "arch.pulse"} {
		for iname, o := range instances {
			o.FS = spy
			p, err := New(o)
			if err != nil {
				t.Fatal(err)
			}
			for rname, mk := range reqs {
				t.Run(cohort+"/"+iname+"/"+rname, func(t *testing.T) {
					env, err := p.PredictEnvelope(ctx, mk(cohort))
					if err != nil {
						t.Fatalf("PredictEnvelope: %v", err)
					}
					want, err := p.PredictBytes(ctx, cohortBytes(t, spy.Fs, cohort), mk(cohort))
					if err != nil {
						t.Fatalf("PredictBytes: %v", err)
					}
					gotJSON, err := json.Marshal(env)
					if err != nil {
						t.Fatal(err)
					}
					wantJSON, err := json.Marshal(want)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(gotJSON, wantJSON) {
						t.Fatalf("PredictEnvelope differs from PredictBytes\n got: %s\nwant: %s", gotJSON, wantJSON)
					}
					sawWarning = sawWarning || len(env.Warnings) > 0
					sawError = sawError || len(env.Errors) > 0
					sawEcho = sawEcho || env.Request != nil
				})
			}
		}
	}
	if !sawWarning || !sawError || !sawEcho {
		t.Fatalf("vacuous: warning=%v error=%v echo=%v; the fixture must exercise all three", sawWarning, sawError, sawEcho)
	}
}

// TestPredictEnvelope_ReadsHeaderOnly: PredictEnvelope over a single
// file reads no more than its header + schema, and over a shard archive
// no more than the archive's non-payload bytes (zip headers, central
// directory, schema entry, shard headers) — never a record, however
// large the payload. Falsified by reading the whole file and handing
// the bytes to descriptor predict, as `pulse api predict` used to.
func TestPredictEnvelope_ReadsHeaderOnly(t *testing.T) {
	const rows = 4000 // ~ 560 KB of payload per cohort
	spy, archiveOverhead, singleHead := envelopeFixture(t, rows)
	p, err := New(Options{FS: spy, Strict: true, EchoRequest: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := []struct {
		cohort string
		file   string
		budget int64
	}{
		{"single.pulse", "single.pulse", singleHead},
		{"arch.pulse", "arch.pulse", archiveOverhead},
		{"arch.pulse#s1.pulse", "arch.pulse", archiveOverhead},
	}
	// The tally counts re-reads (the zip-magic sniff re-reads the first
	// bytes; an archive's directory structures are read more than
	// once), so each budget is THREE times the non-payload bytes — still a small fraction of one cohort's
	// payload, which a whole-file read pays in full.
	for i := range cases {
		cases[i].budget *= 3
	}
	for _, tc := range cases {
		t.Run(tc.cohort, func(t *testing.T) {
			spy.reset()
			env, err := p.PredictEnvelope(ctx, &types.Request{
				Cohort:       &types.Cohort{Filename: tc.cohort},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m0", Label: "s"}},
			})
			if err != nil {
				t.Fatalf("PredictEnvelope: %v", err)
			}
			if env.Data == nil {
				t.Fatal("no predict result")
			}
			got := spy.read(tc.file)
			if got == 0 || got > tc.budget {
				t.Fatalf("PredictEnvelope(%s) read %d bytes of %s; header/schema (+ central directory) budget %d",
					tc.cohort, got, tc.file, tc.budget)
			}
		})
	}
}

// TestPredictEnvelope_ReadFailuresAreErrors: a nil request, a request
// without a cohort, a missing file and an unresolvable anchor are
// returned errors (READ failures), never an envelope; a cancelled ctx
// likewise.
func TestPredictEnvelope_ReadFailuresAreErrors(t *testing.T) {
	spy, _, _ := envelopeFixture(t, 5)
	p, err := New(Options{FS: spy})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, req := range map[string]*types.Request{
		"nil request":   nil,
		"no cohort":     {},
		"missing file":  {Cohort: &types.Cohort{Filename: "nope.pulse"}},
		"missing entry": {Cohort: &types.Cohort{Filename: "arch.pulse#nope.pulse"}},
	} {
		if env, err := p.PredictEnvelope(ctx, req); err == nil {
			t.Errorf("%s: want an error, got envelope %+v", name, env)
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.PredictEnvelope(cctx, &types.Request{Cohort: &types.Cohort{Filename: "single.pulse"}}); err == nil {
		t.Error("cancelled ctx: want an error")
	}
}

// TestPredictEnvelope_CarriesSidecarFacts: over an SPSS-imported cohort
// PredictEnvelope's verdict carries the sidecar-fed advisories (and
// suggested weight) Predict reports — the path read keeps Predict's
// sidecar facts, which the byte-level PredictBytes never has.
func TestPredictEnvelope_CarriesSidecarFacts(t *testing.T) {
	p := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{})
	ctx := context.Background()
	want, err := p.Predict(ctx, measuredRequest(nil))
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	env, err := p.PredictEnvelope(ctx, measuredRequest(nil))
	if err != nil {
		t.Fatalf("PredictEnvelope: %v", err)
	}
	got, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		t.Fatalf("Data is %T", env.Data)
	}
	if !envelopeHasAdvisory(got, advWeight) {
		t.Fatalf("PredictEnvelope advisories = %+v; want the sidecar weight advisory %s", got.Advisories, advWeight)
	}
	gotJSON, _ := json.Marshal(got.Advisories)
	wantJSON, _ := json.Marshal(want.Advisories)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("advisories differ from Predict\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
	gotW, _ := json.Marshal(got.SuggestedWeight)
	wantW, _ := json.Marshal(want.SuggestedWeight)
	if !bytes.Equal(gotW, wantW) {
		t.Errorf("suggested weight differs from Predict: got %s want %s", gotW, wantW)
	}
}

func envelopeHasAdvisory(r *descriptor.PredictResult, code string) bool {
	for _, a := range r.Advisories {
		if a.Code == code {
			return true
		}
	}
	return false
}
