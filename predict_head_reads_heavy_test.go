//go:build heavy

package pulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// headCallFs counts every Read / ReadAt call on the handles it opens
// and the furthest byte each reached.
type headCallFs struct {
	afero.Fs
	mu      sync.Mutex
	calls   int
	maxRead int64
}

func (s *headCallFs) note(end int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if end > s.maxRead {
		s.maxRead = end
	}
}

func (s *headCallFs) reset() (int, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, m := s.calls, s.maxRead
	s.calls, s.maxRead = 0, 0
	return c, m
}

func (s *headCallFs) Open(name string) (afero.File, error) {
	f, err := s.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &headCallFile{File: f, fs: s}, nil
}

func (s *headCallFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := s.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &headCallFile{File: f, fs: s}, nil
}

type headCallFile struct {
	afero.File
	fs *headCallFs
}

func (f *headCallFile) Read(b []byte) (int, error) {
	pos, _ := f.File.Seek(0, io.SeekCurrent)
	n, err := f.File.Read(b)
	f.fs.note(pos + int64(n))
	return n, err
}

func (f *headCallFile) ReadAt(b []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(b, off)
	f.fs.note(off + int64(n))
	return n, err
}

// TestPredict_HugeDictionaryReadsBounded (archive-storage E5-S4, TODO
// #722): predict and explain over a single file whose inline dictionary
// holds 600k entries used to read the schema block straight off the
// handle — two reads per entry on every open (the predict read, the
// runtime-side DecodeMappable / schema-loader opens), ~1.2M reads each.
// Through encoding's bounded schema buffer each open is a few hundred
// reads at most, and not one byte at or past the first record is read.
//
// Falsified by returning the source unwrapped from
// encoding.newSchemaReader: the read count returns to ~2 per entry.
func TestPredict_HugeDictionaryReadsBounded(t *testing.T) {
	t.Parallel()

	const entries = 600_000
	d := encoding.NewDictionary()
	for i := range entries {
		if _, err := d.Add(fmt.Sprintf("entry-%07d", i)); err != nil {
			t.Fatal(err)
		}
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU32, Dictionary: d},
		{Name: "m", Type: encoding.FieldTypeF64, ByteOffset: 4},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	head := int64(buf.Len())
	buf.Write(make([]byte, 50_000*s.RecordByteSize()))
	spy := &headCallFs{Fs: afero.NewMemMapFs()}
	if err := afero.WriteFile(spy.Fs, "big.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: spy})
	if err != nil {
		t.Fatal(err)
	}
	req := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "big.pulse"},
			Groups:       []*types.Group{{Field: "c", Type: types.GROUP_CATEGORY}},
			Aggregations: []*types.Aggregation{{Field: "m", Type: types.AGG_SUM}},
		}
	}
	ctx := context.Background()
	for name, run := range map[string]func() (bool, error){
		"predict": func() (bool, error) {
			r, err := p.Predict(ctx, req())
			return err == nil && r.Valid, err
		},
		"explain": func() (bool, error) {
			r, err := p.Explain(ctx, descriptor.ExplainRequest{Request: req()})
			return err == nil && r.Valid != nil && *r.Valid, err
		},
	} {
		spy.reset()
		start := time.Now()
		ok, err := run()
		elapsed := time.Since(start)
		calls, maxRead := spy.reset()
		if err != nil || !ok {
			t.Fatalf("%s: valid=%v err=%v", name, ok, err)
		}
		if maxRead > head {
			t.Fatalf("%s read up to byte %d; header + schema end at %d", name, maxRead, head)
		}
		// Each open is ~110 reads for this head; a handful of opens fit,
		// one read per entry does not.
		if calls > 2000 {
			t.Fatalf("%s made %d reads over a %d-entry dictionary; want O(log entries) per open", name, calls, entries)
		}
		t.Logf("%s: %d reads, %v", name, calls, elapsed)
	}
}
