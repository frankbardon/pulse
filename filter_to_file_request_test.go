package pulse

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestFilterToFileWithRequest_Deterministic(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "src.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"}, {"40"},
	})
	_ = memFs.MkdirAll("out", 0o755)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &FilterToFileRequest{
		SourcePath: "src.pulse",
		Expression: "age >= 20",
		OutputDir:  "out",
	}
	first, err := p.FilterToFileWithRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first.Reused {
		t.Errorf("first call should not be marked Reused")
	}
	if first.RowCount != 3 {
		t.Errorf("RowCount = %d, want 3", first.RowCount)
	}
	if !strings.HasSuffix(first.OutputPath, ".pulse") {
		t.Errorf("OutputPath %q should end in .pulse", first.OutputPath)
	}
	if first.OutputHash == "" {
		t.Errorf("OutputHash empty")
	}

	// Second identical call: should dedup.
	second, err := p.FilterToFileWithRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !second.Reused {
		t.Errorf("second call should be Reused")
	}
	if second.OutputPath != first.OutputPath || second.OutputHash != first.OutputHash {
		t.Errorf("second call path/hash diverged: %+v vs %+v", second, first)
	}
}

func TestFilterToFileWithRequest_DistinctPredicates(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "src.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"},
	})
	_ = memFs.MkdirAll("out", 0o755)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := &FilterToFileRequest{SourcePath: "src.pulse", Expression: "age >= 10", OutputDir: "out"}
	b := &FilterToFileRequest{SourcePath: "src.pulse", Expression: "age >= 20", OutputDir: "out"}

	ra, err := p.FilterToFileWithRequest(context.Background(), a)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	rb, err := p.FilterToFileWithRequest(context.Background(), b)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if ra.OutputPath == rb.OutputPath {
		t.Fatalf("distinct predicates should resolve to distinct output paths (%s)", ra.OutputPath)
	}
}

func TestFilterToFileWithRequest_RejectsMutualExclusion(t *testing.T) {
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &FilterToFileRequest{
		SourcePath: "x.pulse",
		Expression: "a > 0",
		OutputDir:  "out",
	}
	req.Filterers = nil
	// Both unset.
	bad := &FilterToFileRequest{SourcePath: "x.pulse", OutputDir: "out"}
	if _, err := p.FilterToFileWithRequest(context.Background(), bad); err == nil {
		t.Errorf("expected error when neither Expression nor Filterers set")
	}
}
