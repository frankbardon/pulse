package tsv

import (
	"context"
	"testing"

	"github.com/spf13/afero"
)

func TestTsvReader_FromFilesystem(t *testing.T) {
	fs := afero.NewMemMapFs()
	afero.WriteFile(fs, "data.tsv", []byte("a\tb\n1\t2\n3\t4\n"), 0644)

	r := NewReader(fs, "data.tsv")
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 {
		t.Fatalf("header len = %d", len(header))
	}

	var rows int
	err = r.ReadRows(context.Background(), func(row []string) error {
		rows++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if rows != 2 {
		t.Errorf("rows = %d, want 2", rows)
	}
	r.Close()
}

func TestTsvReader_FromFilesystem_Missing(t *testing.T) {
	fs := afero.NewMemMapFs()
	r := NewReader(fs, "missing.tsv")
	_, err := r.ReadHeader()
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestTsvReader_NoDataSource(t *testing.T) {
	r := &Reader{} // no data, no fs, no path
	_, err := r.ReadHeader()
	if err == nil {
		t.Error("expected error for no data source")
	}
}

func TestTsvReader_ReadRows_ContextCancel(t *testing.T) {
	data := "a\n1\n2\n3\n4\n5\n"
	r := NewReaderFromBytes([]byte(data))
	r.ReadHeader()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := r.ReadRows(ctx, func(row []string) error {
		return nil
	})
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestTsvReader_ReadRows_AutoHeader(t *testing.T) {
	data := "a\tb\n1\t2\n3\t4\n"
	r := NewReaderFromBytes([]byte(data))

	var rows int
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if rows != 2 {
		t.Errorf("rows = %d, want 2", rows)
	}
}

func TestTsvReader_HeaderCached(t *testing.T) {
	data := "a\tb\n1\t2\n"
	r := NewReaderFromBytes([]byte(data))

	h1, _ := r.ReadHeader()
	h2, _ := r.ReadHeader() // cached
	if len(h1) != len(h2) {
		t.Errorf("headers differ: %v vs %v", h1, h2)
	}
}

func TestTsvWriter_CloseFlush(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"a"})
	w.WriteRow([]any{"val"})
	err := w.Close()
	if err != nil {
		t.Errorf("Close: %v", err)
	}
}
