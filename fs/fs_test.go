package fs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// TestOsFs_ReadWrite verifies basic file operations with OsFs on a temp dir.
func TestOsFs_ReadWrite(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := fs.New(fs.WithDataDir(tmpDir))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	afs := cfg.Fs()

	// Write a file.
	content := []byte("hello pulse")
	if err := afero.WriteFile(afs, "test.txt", content, 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Read it back.
	got, err := afero.ReadFile(afs, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(got) != "hello pulse" {
		t.Errorf("ReadFile = %q, want %q", string(got), "hello pulse")
	}

	// Verify the file exists on disk at the expected path.
	diskPath := filepath.Join(tmpDir, "test.txt")
	if _, err := os.Stat(diskPath); err != nil {
		t.Errorf("file should exist on disk at %s: %v", diskPath, err)
	}
}

// TestMemMapFs_ReadWrite verifies the same operations with MemMapFs.
func TestMemMapFs_ReadWrite(t *testing.T) {
	cfg := fs.NewMemMap()
	afs := cfg.Fs()

	// Write a file.
	content := []byte("in-memory data")
	if err := afero.WriteFile(afs, "data.txt", content, 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Read it back.
	got, err := afero.ReadFile(afs, "data.txt")
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(got) != "in-memory data" {
		t.Errorf("ReadFile = %q, want %q", string(got), "in-memory data")
	}

	// Stat should work.
	info, err := afs.Stat("data.txt")
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Errorf("Size = %d, want %d", info.Size(), len(content))
	}
}

// TestCustomFs_Injection verifies a custom afero.Fs can be injected and used.
func TestCustomFs_Injection(t *testing.T) {
	custom := afero.NewMemMapFs()

	// Pre-populate the custom FS.
	if err := afero.WriteFile(custom, "preexisting.txt", []byte("already here"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	cfg, err := fs.New(fs.WithFs(custom))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Should be able to read the preexisting file.
	got, err := afero.ReadFile(cfg.Fs(), "preexisting.txt")
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(got) != "already here" {
		t.Errorf("ReadFile = %q, want %q", string(got), "already here")
	}

	// The returned FS should be the exact same instance.
	if cfg.Fs() != custom {
		t.Error("injected FS should be the exact instance provided")
	}
}

// TestDataDirFromEnv verifies that PULSE_DATA_DIR resolves correctly.
func TestDataDirFromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", tmpDir)

	cfg, err := fs.Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	if cfg.DataDir() != tmpDir {
		t.Errorf("DataDir() = %q, want %q", cfg.DataDir(), tmpDir)
	}
}

// TestDataDirMissing returns a clear error when env var not set and no explicit dir.
func TestDataDirMissing(t *testing.T) {
	t.Setenv("PULSE_DATA_DIR", "")

	_, err := fs.Default()
	if err == nil {
		t.Fatal("Default() should return error when PULSE_DATA_DIR is empty")
	}

	if !strings.Contains(err.Error(), "PULSE_DATA_DIR") {
		t.Errorf("error should mention PULSE_DATA_DIR, got: %v", err)
	}
}

// TestNoGcsReferences greps the fs package to ensure no GCS-related code.
func TestNoGcsReferences(t *testing.T) {
	// Get all .go files in the fs/ directory.
	fsDir := filepath.Join(".")
	entries, err := os.ReadDir(fsDir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}

	var goFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			goFiles = append(goFiles, e.Name())
		}
	}

	if len(goFiles) == 0 {
		t.Skip("no .go source files found")
	}

	// Search for GCS-related terms.
	gcsTerms := []string{"gcs", "GcsFs", "CacheOnReadFs", "ForwardFs", "cloud.google", "storage.Client"}
	for _, term := range gcsTerms {
		args := append([]string{"-l", "-i", term}, goFiles...)
		cmd := exec.Command("grep", args...)
		out, err := cmd.Output()
		if err != nil {
			// grep exit 1 means no match — that is success.
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				continue
			}
			t.Fatalf("grep for %q failed: %v", term, err)
		}
		matches := strings.TrimSpace(string(out))
		if matches != "" {
			t.Errorf("found GCS-related term %q in: %s", term, matches)
		}
	}
}

// TestDataDir_Explicit verifies that an explicitly set DataDir takes precedence
// over the environment variable.
func TestDataDir_Explicit(t *testing.T) {
	envDir := t.TempDir()
	explicitDir := t.TempDir()

	t.Setenv("PULSE_DATA_DIR", envDir)

	cfg, err := fs.New(fs.WithDataDir(explicitDir))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if cfg.DataDir() != explicitDir {
		t.Errorf("DataDir() = %q, want explicit %q", cfg.DataDir(), explicitDir)
	}
}

// TestNewMemMap_DataDir verifies MemMapFs gets an empty DataDir.
func TestNewMemMap_DataDir(t *testing.T) {
	cfg := fs.NewMemMap()
	if cfg.DataDir() != "" {
		t.Errorf("DataDir() = %q, want empty for MemMapFs", cfg.DataDir())
	}
}

// TestOsFs_BasePath verifies that OsFs is rooted at the data dir.
func TestOsFs_BasePath(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := fs.New(fs.WithDataDir(tmpDir))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	afs := cfg.Fs()

	// Create a subdirectory.
	if err := afs.MkdirAll("sub/dir", 0755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}

	// Verify on disk.
	diskPath := filepath.Join(tmpDir, "sub", "dir")
	info, err := os.Stat(diskPath)
	if err != nil {
		t.Fatalf("subdirectory should exist on disk at %s: %v", diskPath, err)
	}
	if !info.IsDir() {
		t.Errorf("expected directory at %s", diskPath)
	}
}
