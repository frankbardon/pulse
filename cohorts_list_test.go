package pulse

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// cohortsListFixture is a data root holding a plain cohort, one in a
// subdirectory, a shard archive, a managed import, a corrupt .pulse
// file and the files ListCohorts must skip: sidecars, a profile, a
// hidden temp file and a hidden directory.
func cohortsListFixture(t *testing.T, fsys afero.Fs) *Pulse {
	t.Helper()
	createTestPulseFile(t, fsys, "plain.pulse", []string{"id", "region"}, [][]string{{"1", "north"}, {"2", "south"}, {"3", "north"}})
	createTestPulseFile(t, fsys, "waves/w1.pulse", []string{"id"}, [][]string{{"1"}})
	createTestPulseFile(t, fsys, "shard_a.tmp", []string{"id"}, [][]string{{"1"}, {"2"}})
	createTestPulseFile(t, fsys, "shard_b.tmp", []string{"id"}, [][]string{{"3"}})
	createTestPulseFile(t, fsys, ".hidden/h.pulse", []string{"id"}, [][]string{{"1"}})
	createTestPulseFile(t, fsys, ".w1.pulse", []string{"id"}, [][]string{{"1"}})
	for name, body := range map[string]string{
		"src.csv":                          "id,amount\n1,2.5\n2,3.5\n",
		"broken.pulse":                     "not a pulse file",
		"plain.pulse.indexes.json":         "{}",
		"plain.pulse.0123456789abcdef.idx": "x",
		"plain.pulse.profile.json":         "{}",
	} {
		if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	// A shard archive from two single-file shards (written as .tmp so
	// the shards themselves are not cohorts of the root).
	for _, n := range []string{"shard_a", "shard_b"} {
		if err := fsys.Rename(n+".tmp", n+".pulse"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", []string{"shard_a.pulse", "shard_b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	for _, n := range []string{"shard_a.pulse", "shard_b.pulse"} {
		if err := fsys.Remove(n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.ImportFile(context.Background(), ImportSpec{SourcePath: "src.csv", Handle: "sales", TTL: time.Hour}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return p
}

// TestListCohorts_ListsEveryCohortAndSkipsTheRest pins the listing: every
// .pulse file under the root, recursively, sorted by path, with its
// record count, fields, archive and managed flags; sidecars, profiles,
// hidden entries and the shards inside an archive are not listed; a
// corrupt .pulse file is listed with its coded error.
func TestListCohorts_ListsEveryCohortAndSkipsTheRest(t *testing.T) {
	t.Parallel()

	p := cohortsListFixture(t, afero.NewMemMapFs())
	got, err := p.ListCohorts(context.Background(), ListCohortsOptions{})
	if err != nil {
		t.Fatalf("ListCohorts: %v", err)
	}
	var paths []string
	for _, e := range got {
		paths = append(paths, e.Path)
	}
	want := []string{"arch.pulse", "broken.pulse", "imports/sales.pulse", "plain.pulse", "waves/w1.pulse"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
	byPath := map[string]CohortListEntry{}
	for _, e := range got {
		byPath[e.Path] = e
	}

	plain := byPath["plain.pulse"]
	if plain.RecordCount != 3 || len(plain.Fields) != 2 || plain.Fields[0].Name != "id" || plain.Fields[1].Name != "region" ||
		plain.Fields[1].Type == "" || plain.ShardArchive || plain.Managed || plain.Error != nil {
		t.Errorf("plain.pulse = %+v", plain)
	}
	arch := byPath["arch.pulse"]
	if !arch.ShardArchive || arch.ShardCount != 2 || arch.RecordCount != 3 || len(arch.Fields) != 1 {
		t.Errorf("arch.pulse = %+v, want a 2-shard archive of 3 records", arch)
	}
	sales := byPath["imports/sales.pulse"]
	if !sales.Managed || sales.Handle != "sales" || sales.RecordCount != 2 || len(sales.Fields) != 2 {
		t.Errorf("imports/sales.pulse = %+v, want managed handle sales with 2 records", sales)
	}
	if byPath["waves/w1.pulse"].Managed {
		t.Error("waves/w1.pulse flagged managed")
	}
	broken := byPath["broken.pulse"]
	if broken.Error == nil || broken.Error.Code != string(errors.ENCODING_INVALID) || broken.Fields == nil || len(broken.Fields) != 0 {
		t.Errorf("broken.pulse = %+v, want an ENCODING_INVALID error and empty fields", broken)
	}
}

// TestListCohorts_Dir scopes the walk to one subdirectory and refuses a
// path outside the data root or a missing directory.
func TestListCohorts_Dir(t *testing.T) {
	t.Parallel()

	p := cohortsListFixture(t, afero.NewMemMapFs())
	got, err := p.ListCohorts(context.Background(), ListCohortsOptions{Dir: "waves"})
	if err != nil || len(got) != 1 || got[0].Path != "waves/w1.pulse" {
		t.Fatalf("ListCohorts(waves) = %+v, %v; want waves/w1.pulse alone", got, err)
	}
	for dir, code := range map[string]errors.Code{
		"../elsewhere": errors.SERVICE_VALIDATION,
		"/abs":         errors.SERVICE_VALIDATION,
		"nope":         errors.SERVICE_RESOURCE,
	} {
		_, err := p.ListCohorts(context.Background(), ListCohortsOptions{Dir: dir})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != code {
			t.Errorf("ListCohorts(%q) err = %v, want %s", dir, err, code)
		}
	}
}

// TestListCohorts_DataDirPathsAreRootRelative runs over an OS data
// directory: every path is relative to it, with no leading slash, so it
// opens as given.
func TestListCohorts_DataDirPathsAreRootRelative(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := afero.NewBasePathFs(afero.NewOsFs(), dir)
	if err := base.MkdirAll("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	createTestPulseFile(t, base, "a.pulse", []string{"id"}, [][]string{{"1"}})
	createTestPulseFile(t, base, "sub/b.pulse", []string{"id"}, [][]string{{"1"}, {"2"}})
	p, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.ListCohorts(context.Background(), ListCohortsOptions{})
	if err != nil || len(got) != 2 || got[0].Path != "a.pulse" || got[1].Path != "sub/b.pulse" {
		t.Fatalf("ListCohorts = %+v, %v; want a.pulse, sub/b.pulse", got, err)
	}
	for _, e := range got {
		if _, err := p.Inspect(context.Background(), e.Path); err != nil {
			t.Errorf("Inspect(%q): %v", e.Path, err)
		}
	}
}

// TestListCohorts_DoesNotSlideManagedExpiry: listing is not a use of a
// managed import, so the handle's expires_at is unchanged.
func TestListCohorts_DoesNotSlideManagedExpiry(t *testing.T) {
	t.Parallel()

	p := cohortsListFixture(t, afero.NewMemMapFs())
	before, err := p.Imports(context.Background())
	if err != nil || len(before) != 1 {
		t.Fatalf("Imports = %v, %v", before, err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := p.ListCohorts(context.Background(), ListCohortsOptions{}); err != nil {
		t.Fatal(err)
	}
	after, err := p.Imports(context.Background())
	if err != nil || len(after) != 1 {
		t.Fatalf("Imports = %v, %v", after, err)
	}
	if !after[0].Sidecar.ExpiresAt.Equal(before[0].Sidecar.ExpiresAt) {
		t.Errorf("expires_at moved %v -> %v: listing slid the managed expiry", before[0].Sidecar.ExpiresAt, after[0].Sidecar.ExpiresAt)
	}
}

// TestListCohorts_EmptyRootIsEmptyNotNil: nothing to list is [], so the
// JSON is an empty array.
func TestListCohorts_EmptyRootIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.ListCohorts(context.Background(), ListCohortsOptions{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ListCohorts = %#v, %v; want an empty, non-nil slice", got, err)
	}
}
