package pulse

import (
	"context"
	"sort"
	"testing"

	"github.com/spf13/afero"
)

// TestCreateShardArchive_DataDirRoot: under Options{DataDir} an archive
// at the data-dir root is written in place — the atomic-write temp file
// is staged beside the archive INSIDE the data dir (an empty temp dir
// would fall back to os.TempDir(), outside the base-path filesystem, and
// fail SERVICE_RESOURCE) — and nothing but the inputs and the archive
// is left behind. AddShard at the root takes the same path.
func TestCreateShardArchive_DataDirRoot(t *testing.T) {
	dir := t.TempDir()
	p, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rows := parityRows(t)
	buildCohort(t, p, "a.pulse", paritySchema(), CohortBuilderOptions{}, rows)
	buildCohort(t, p, "b.pulse", paritySchema(), CohortBuilderOptions{}, rows)
	buildCohort(t, p, "c.pulse", paritySchema(), CohortBuilderOptions{}, rows)

	ctx := context.Background()
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive at the data-dir root: %v", err)
	}
	if _, err := p.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatalf("AddShard at the data-dir root: %v", err)
	}
	ents, err := afero.ReadDir(afero.NewOsFs(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{"a.pulse", "arch.pulse", "b.pulse", "c.pulse"}
	if len(names) != len(want) {
		t.Fatalf("data dir holds %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("data dir holds %v, want %v", names, want)
		}
	}
	if r := openReader(t, p, "arch.pulse"); r.Len() != int64(3*len(rows)) {
		t.Fatalf("archive Len = %d, want %d", r.Len(), 3*len(rows))
	}
}
