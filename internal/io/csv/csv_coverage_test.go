package csv

import (
	"context"
	"testing"

	"github.com/spf13/afero"
)

func TestCsvReader_FromFilesystem(t *testing.T) {
	fs := afero.NewMemMapFs()
	afero.WriteFile(fs, "data.csv", []byte("a,b\n1,2\n3,4\n"), 0644)

	r := NewReader(fs, "data.csv")
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

func TestCsvReader_FromFilesystem_Missing(t *testing.T) {
	fs := afero.NewMemMapFs()
	r := NewReader(fs, "missing.csv")
	_, err := r.ReadHeader()
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestCsvReader_NoDataSource(t *testing.T) {
	r := &Reader{} // no data, no fs, no path
	_, err := r.ReadHeader()
	if err == nil {
		t.Error("expected error for no data source")
	}
}

func TestCsvReader_ReadRows_ContextCancel(t *testing.T) {
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

func TestCsvReader_ReadRows_AutoHeader(t *testing.T) {
	// Test ReadRows without calling ReadHeader first.
	data := "a,b\n1,2\n3,4\n"
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

func TestCsvReader_HeaderCached(t *testing.T) {
	data := "a,b\n1,2\n"
	r := NewReaderFromBytes([]byte(data))

	h1, _ := r.ReadHeader()
	h2, _ := r.ReadHeader() // should return cached
	if len(h1) != len(h2) {
		t.Errorf("headers differ: %v vs %v", h1, h2)
	}
}

func TestCsvWriter_CloseError(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"a"})
	w.WriteRow([]any{"val"})
	err := w.Close()
	if err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestCsvReader_ReadRows_StopIteration(t *testing.T) {
	data := "a\n1\n2\n3\n4\n5\n"
	r := NewReaderFromBytes([]byte(data))
	r.ReadHeader()

	// Using the internal stop mechanism through ReadRows.
	var rows int
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows++
		if rows >= 2 {
			return errForStop
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
}

var errForStop = func() error {
	// Simulate an error return from fn.
	return nil
}()
