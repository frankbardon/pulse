package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestCliImportElideConstants: `--elide-constants` on an import leaf
// writes the full-pass constants once (format 0x02) and says so; without
// the flag the same source imports as 0x01.
func TestCliImportElideConstants(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("id,kind,score\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "%d,fixed,%d\n", i+1, i%7)
	}
	csvPath := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(csvPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flag    []string
		version byte
		elided  bool
	}{
		{nil, encoding.FormatVersion, false},
		{[]string{"--elide-constants"}, encoding.FormatVersionV2, true},
	} {
		out := filepath.Join(dir, fmt.Sprintf("out%d.pulse", tc.version))
		args := append([]string{"import", "csv", "--input", csvPath, "--output", out}, tc.flag...)
		text, err := runApp(t, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, text)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if raw[encoding.HeaderSize-1] != tc.version {
			t.Fatalf("%v: version 0x%02x, want 0x%02x", tc.flag, raw[encoding.HeaderSize-1], tc.version)
		}
		if got := strings.Contains(text, "Elided constant fields") && strings.Contains(text, "kind"); got != tc.elided {
			t.Fatalf("%v: output %q, elision reported = %v, want %v", tc.flag, text, got, tc.elided)
		}
	}
}
