//go:build heavy

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	pcli "github.com/frankbardon/pulse/internal/cli"
)

func TestCompletion_ShippedScriptsMatchLeaf(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is required to exercise scripts/release-completions.sh: %v", err)
	}

	shells := pcli.CompletionShells()
	var mapped []string
	for sh := range shippedCompletionFiles {
		mapped = append(mapped, sh)
	}
	slices.Sort(mapped)
	want := slices.Sorted(slices.Values(shells))
	if !slices.Equal(mapped, want) {
		t.Fatalf("shipped completion files cover shells %v, CompletionShells() = %v", mapped, want)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "release-completions.sh"), out, self)
	cmd.Env = append(os.Environ(), runAsCLIEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("release-completions.sh: %v\n%s", err, stderr.String())
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	var wantFiles []string
	for _, f := range shippedCompletionFiles {
		wantFiles = append(wantFiles, f)
	}
	slices.Sort(got)
	slices.Sort(wantFiles)
	if !slices.Equal(got, wantFiles) {
		t.Fatalf("generated files = %v, want %v", got, wantFiles)
	}

	for _, sh := range shells {
		var leaf, leafErr bytes.Buffer
		if status := run(context.Background(), []string{"pulse", "completion", sh}, &leaf, &leafErr); status != 0 {
			t.Fatalf("pulse completion %s: status %d: %s", sh, status, leafErr.String())
		}
		shipped, err := os.ReadFile(filepath.Join(out, shippedCompletionFiles[sh]))
		if err != nil {
			t.Fatal(err)
		}
		if leaf.Len() == 0 {
			t.Fatalf("pulse completion %s printed nothing", sh)
		}
		if !bytes.Equal(shipped, leaf.Bytes()) {
			t.Errorf("completions/%s differs from `pulse completion %s` output", shippedCompletionFiles[sh], sh)
		}
	}
}
