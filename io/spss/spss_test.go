package spss

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// TestReader_Sources covers both constructors over the same fixture bytes, and
// asserts the filesystem path really does go through afero rather than os.
func TestReader_Sources(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())

	t.Run("from bytes", func(t *testing.T) {
		r := NewReaderFromBytes(raw)
		d, err := r.loadDictionary()
		if err != nil {
			t.Fatalf("loadDictionary: %v", err)
		}
		if len(d.vars) != 3 {
			t.Errorf("len(vars) = %d, want 3", len(d.vars))
		}
	})

	t.Run("from an afero filesystem", func(t *testing.T) {
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "survey.sav", raw, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		r := NewReader(cfg.Fs(), "survey.sav")
		d, err := r.loadDictionary()
		if err != nil {
			t.Fatalf("loadDictionary: %v", err)
		}
		if d.dataOffset != 0x0174 {
			t.Errorf("dataOffset = 0x%04X, want 0x0174", d.dataOffset)
		}
	})

	t.Run("the dictionary is parsed once and memoised", func(t *testing.T) {
		r := NewReaderFromBytes(raw)
		first, err := r.loadDictionary()
		if err != nil {
			t.Fatalf("loadDictionary: %v", err)
		}
		again, err := r.loadDictionary()
		if err != nil {
			t.Fatalf("loadDictionary (second call): %v", err)
		}
		if first != again {
			t.Error("the second call re-parsed the dictionary instead of returning the memoised one")
		}
	})

	t.Run("Close releases the buffers", func(t *testing.T) {
		r := NewReaderFromBytes(raw)
		if _, err := r.loadDictionary(); err != nil {
			t.Fatalf("loadDictionary: %v", err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if r.data != nil || r.dict != nil {
			t.Error("Close left buffers behind")
		}
	})
}

// TestReader_SourceErrors covers the two ways a reader can fail before it ever
// sees a dictionary byte.
func TestReader_SourceErrors(t *testing.T) {
	t.Run("no source at all", func(t *testing.T) {
		_, err := (&Reader{}).loadDictionary()
		if err == nil || !strings.Contains(err.Error(), "no data source") {
			t.Fatalf("err = %v, want a no-data-source error", err)
		}
	})

	t.Run("a missing file", func(t *testing.T) {
		cfg := fs.NewMemMap()
		_, err := NewReader(cfg.Fs(), "absent.sav").loadDictionary()
		if err == nil || !strings.Contains(err.Error(), "absent.sav") {
			t.Fatalf("err = %v, want an error naming the missing path", err)
		}
	})

	t.Run("a file that is not a .sav", func(t *testing.T) {
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "notes.txt", []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := NewReader(cfg.Fs(), "notes.txt").loadDictionary()
		if err == nil {
			t.Fatal("a text file parsed as a system file")
		}
		ce := codedError(t, err)
		if !strings.Contains(ce.Message, "not a .sav system file") {
			t.Errorf("message = %q", ce.Message)
		}
	})
}
