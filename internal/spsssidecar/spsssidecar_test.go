package spsssidecar

import "testing"

func TestPath(t *testing.T) {
	if got := Path("dir/data.pulse"); got != "dir/data.pulse.spss.json" {
		t.Fatalf("Path = %q, want dir/data.pulse.spss.json", got)
	}
	if Suffix == ".meta.json" {
		t.Fatal("Suffix collides with the managed-import sidecar suffix")
	}
}
