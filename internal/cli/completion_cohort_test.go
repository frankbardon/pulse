package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// Cohort and field completion read through completionBaseFs. These
// tests swap in an instrumented in-memory filesystem: countingFs counts
// every byte a completion reads and records every write it attempts,
// proving field completion reads only header + schema (never a record)
// and that completion writes nothing.

// countingFs wraps an afero.Fs: it records the byte ranges read per
// file and every mutating call that reaches it.
type countingFs struct {
	afero.Fs
	mu     sync.Mutex
	read   map[string][][2]int64 // [start, end) ranges, in read order
	writes []string
}

func newCountingFs(base afero.Fs) *countingFs {
	return &countingFs{Fs: base, read: map[string][][2]int64{}}
}

func (c *countingFs) wrote(op, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, op+" "+name)
}

func (c *countingFs) addRead(name string, off int64, n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.read[name] = append(c.read[name], [2]int64{off, off + int64(n)})
}

func (c *countingFs) ranges(name string) [][2]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.read[name])
}

// bytesRead returns how many distinct bytes of name were read (a byte
// read twice — a magic peek, then the header — counts once) and the
// highest offset read.
func (c *countingFs) bytesRead(name string) (distinct, end int64) {
	c.mu.Lock()
	rs := slices.Clone(c.read[name])
	c.mu.Unlock()
	slices.SortFunc(rs, func(a, b [2]int64) int { return int(a[0] - b[0]) })
	var covered int64 = -1
	for _, r := range rs {
		start := max(r[0], covered)
		if r[1] > start {
			distinct += r[1] - start
		}
		covered = max(covered, r[1])
		end = max(end, r[1])
	}
	return distinct, end
}

func (c *countingFs) Create(name string) (afero.File, error) {
	c.wrote("create", name)
	return c.Fs.Create(name)
}

func (c *countingFs) Mkdir(name string, perm os.FileMode) error {
	c.wrote("mkdir", name)
	return c.Fs.Mkdir(name, perm)
}

func (c *countingFs) MkdirAll(name string, perm os.FileMode) error {
	c.wrote("mkdirall", name)
	return c.Fs.MkdirAll(name, perm)
}

func (c *countingFs) Remove(name string) error {
	c.wrote("remove", name)
	return c.Fs.Remove(name)
}

func (c *countingFs) RemoveAll(name string) error {
	c.wrote("removeall", name)
	return c.Fs.RemoveAll(name)
}

func (c *countingFs) Rename(o, n string) error {
	c.wrote("rename", o)
	return c.Fs.Rename(o, n)
}

func (c *countingFs) Chmod(name string, mode os.FileMode) error {
	c.wrote("chmod", name)
	return c.Fs.Chmod(name, mode)
}

func (c *countingFs) Chtimes(name string, a, m time.Time) error {
	c.wrote("chtimes", name)
	return c.Fs.Chtimes(name, a, m)
}

func (c *countingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: f, fs: c, name: name}, nil
}

func (c *countingFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_TRUNC) != 0 {
		c.wrote("openfile", name)
	}
	f, err := c.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: f, fs: c, name: name}, nil
}

type countingFile struct {
	afero.File
	fs   *countingFs
	name string
}

func (f *countingFile) Read(p []byte) (int, error) {
	off, _ := f.File.Seek(0, io.SeekCurrent)
	n, err := f.File.Read(p)
	f.fs.addRead(f.name, off, n)
	return n, err
}

func (f *countingFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(p, off)
	f.fs.addRead(f.name, off, n)
	return n, err
}

func (f *countingFile) Write(p []byte) (int, error) {
	f.fs.wrote("write", f.name)
	return f.File.Write(p)
}

func (f *countingFile) WriteAt(p []byte, off int64) (int, error) {
	f.fs.wrote("writeat", f.name)
	return f.File.WriteAt(p, off)
}

// writeWideCohort writes a single-file cohort of fields u32 columns and
// records rows to fsys at path, returning the header + schema size (the
// bytes before the first record) and the record payload size.
func writeWideCohort(t *testing.T, fsys afero.Fs, path string, fields, records int) (head, payload int64) {
	t.Helper()
	schema := &encoding.Schema{}
	for i := range fields {
		schema.Fields = append(schema.Fields, encoding.Field{
			Name: fmt.Sprintf("col_%03d", i), Type: encoding.FieldTypeU32, ByteOffset: 4 * i, CsvColumnIdx: i,
		})
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	head = int64(buf.Len())
	row := make([]byte, 4*fields)
	for r := range records {
		for i := range fields {
			binary.LittleEndian.PutUint32(row[4*i:], uint32(r+i))
		}
		buf.Write(row)
	}
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return head, int64(buf.Len()) - head
}

// completionFixture is an in-memory data dir: a wide single-file cohort,
// a two-shard archive of the same schema, a transfer artifact and a
// non-cohort file, behind a countingFs installed as completionBaseFs.
type completionFixture struct {
	fs              *countingFs
	single, archive string
	singleHead      int64 // header + schema bytes of the single file
	archiveSize     int64
	archivePayload  [][2]int64 // [start, end) of each shard's records
	fields          []string
}

// zipEndSearch is how far from the end archive/zip reads to find the
// end-of-central-directory record: the first window it tries. Opening an
// archive reads this tail whatever it holds (here, the last shard's
// final records), before it knows where the directory starts.
const zipEndSearch = 1024

const (
	fixtureFields  = 64
	fixtureRecords = 20000
)

func newCompletionFixture(t *testing.T) *completionFixture {
	t.Helper()
	mem := afero.NewMemMapFs()
	fx := &completionFixture{single: "/data/wide.pulse", archive: "/data/arch/wide-archive.pulse"}
	head, payload := writeWideCohort(t, mem, fx.single, fixtureFields, fixtureRecords)
	fx.singleHead = head
	_, p2 := writeWideCohort(t, mem, "/stage/shard2.pulse", fixtureFields, fixtureRecords)
	if err := mem.MkdirAll("/data/arch", 0o755); err != nil {
		t.Fatal(err)
	}
	builder, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.CreateShardArchive(context.Background(), fx.archive, []string{fx.single, "/stage/shard2.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	f, err := mem.Open(fx.archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	fx.archiveSize = info.Size()
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range zr.File {
		if e.Name == "_schema.pulse" {
			continue
		}
		off, err := e.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		end := off + int64(e.UncompressedSize64)
		fx.archivePayload = append(fx.archivePayload, [2]int64{end - payload, end})
	}
	if len(fx.archivePayload) != 2 || p2 != payload {
		t.Fatalf("archive fixture: %d shard payloads", len(fx.archivePayload))
	}
	for _, f := range []string{"/data/wide.pulse.zst", "/data/notes.csv"} {
		if err := afero.WriteFile(mem, f, []byte("not a cohort"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range fixtureFields {
		fx.fields = append(fx.fields, fmt.Sprintf("col_%03d", i))
	}
	fx.fs = newCountingFs(mem)
	saved := completionBaseFs
	completionBaseFs = func() afero.Fs { return fx.fs }
	t.Cleanup(func() { completionBaseFs = saved })
	t.Setenv("PULSE_DATA_DIR", "/data")
	return fx
}

// snapshot lists every path and size on fsys.
func snapshot(t *testing.T, fsys afero.Fs) []string {
	t.Helper()
	var out []string
	_ = afero.Walk(fsys, "/", func(path string, info os.FileInfo, err error) error {
		if err == nil {
			out = append(out, fmt.Sprintf("%s %d %v", path, info.Size(), info.IsDir()))
		}
		return nil
	})
	return out
}

// completeLeaf runs one completion request on a tree of real leaves.
func completeLeaf(t *testing.T, words ...string) []string {
	t.Helper()
	var out, errOut bytes.Buffer
	root := EnableCompletion(&cli.Command{
		Name:      "pulse",
		Writer:    &out,
		ErrWriter: &errOut,
		Commands:  []*cli.Command{APICommand(), WidenCommand(), CohortCommand(), RecommendCommand()},
	})
	args := append([]string{"pulse"}, append(words, completionFlag)...)
	ctx, _ := CompletionRequest(context.Background(), args)
	_ = root.Run(ctx, args)
	if errOut.Len() != 0 {
		t.Errorf("%v: stderr %q", words, errOut.String())
	}
	var got []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line != "" {
			got = append(got, line)
		}
	}
	return got
}

// TestCompletion_FieldsReadHeaderOnly: field completion over a wide
// cohort of many records reads no more than its header + schema, and
// over a shard archive no more than the archive's non-record bytes.
func TestCompletion_FieldsReadHeaderOnly(t *testing.T) {
	fx := newCompletionFixture(t)

	got := completeLeaf(t, "api", "facet", "--input", fx.single, "--field")
	if !slices.Equal(got, fx.fields) {
		t.Fatalf("single-file field completion = %v, want the %d schema fields", got, len(fx.fields))
	}
	if n, end := fx.fs.bytesRead(fx.single); n == 0 || end > fx.singleHead {
		t.Errorf("single file: read %d bytes up to offset %d, want none past %d (header + schema; the %d-record payload is %d bytes)",
			n, end, fx.singleHead, fixtureRecords, fixtureRecords*fixtureFields*4)
	}

	got = completeLeaf(t, "widen", fx.archive, "--field")
	if !slices.Equal(got, fx.fields) {
		t.Fatalf("archive field completion = %v, want the %d schema fields", got, len(fx.fields))
	}
	// The archive is read for its directory, schema entry and each
	// shard's header + schema — never a shard's records, bar the tail
	// window archive/zip reads to find the directory.
	if n, _ := fx.fs.bytesRead(fx.archive); n == 0 {
		t.Error("archive: nothing read")
	}
	for _, r := range fx.fs.ranges(fx.archive) {
		if r[0] >= fx.archiveSize-zipEndSearch {
			continue
		}
		for _, pl := range fx.archivePayload {
			if r[0] < pl[1] && r[1] > pl[0] {
				t.Errorf("archive: read [%d,%d) overlaps shard records [%d,%d)", r[0], r[1], pl[0], pl[1])
			}
		}
	}
}

// TestCompletion_ReadOnly: cohort and field completion attempt no write
// on the filesystem (no imports dir, no sidecar), and the Pulse
// instances completion builds refuse writes outright.
func TestCompletion_ReadOnly(t *testing.T) {
	fx := newCompletionFixture(t)
	before := snapshot(t, fx.fs.Fs)

	if got := completeLeaf(t, "api", "facet", "--input"); !slices.Equal(got, []string{fx.archive, fx.single}) {
		t.Errorf("cohort completion = %v, want [%s %s]", got, fx.archive, fx.single)
	}
	completeLeaf(t, "api", "facet", "--input", fx.single, "--field")
	completeLeaf(t, "recommend", "--cohort", fx.archive, "--field")

	if len(fx.fs.writes) != 0 {
		t.Errorf("completion attempted writes: %v", fx.fs.writes)
	}
	if after := snapshot(t, fx.fs.Fs); !slices.Equal(before, after) {
		t.Errorf("filesystem changed:\nbefore %v\nafter  %v", before, after)
	}

	p, err := completionPulse()()
	if err != nil {
		t.Fatal(err)
	}
	dp, _, err := completionDataPulse()()
	if err != nil {
		t.Fatal(err)
	}
	for name, fsys := range map[string]afero.Fs{"unrooted": p.Fs(), "data dir": dp.Fs()} {
		if err := fsys.MkdirAll("/data/imports", 0o755); err == nil {
			t.Errorf("%s completion fs accepted a mkdir", name)
		}
		if f, err := fsys.Create("/data/new.pulse"); err == nil {
			_ = f.Close()
			t.Errorf("%s completion fs accepted a create", name)
		}
	}
}

// TestCompletion_CohortSources: the field provider's cohort comes from
// --input, else the positional cohort, else --cohort; with none named
// nothing completes.
func TestCompletion_CohortSources(t *testing.T) {
	fx := newCompletionFixture(t)
	for _, words := range [][]string{
		{"api", "facet", "--input", fx.single, "--additive"},
		{"widen", fx.single, "--field"},
		{"widen", "--input", fx.single, "--field"},
		{"recommend", "--cohort", fx.single, "--field"},
	} {
		if got := completeLeaf(t, words...); !slices.Equal(got, fx.fields) {
			t.Errorf("%v: %d candidates, want the %d schema fields", words, len(got), len(fx.fields))
		}
	}
	// --input wins over the positional: a missing --input cohort
	// completes nothing even though the positional names a good one.
	if got := completeLeaf(t, "widen", fx.single, "--input", "/data/missing.pulse", "--field"); len(got) != 0 {
		t.Errorf("--input precedence: got %v, want nothing", got)
	}
	if got := completeLeaf(t, "api", "facet", "--field"); len(got) != 0 {
		t.Errorf("no cohort named: got %v, want nothing", got)
	}
}
