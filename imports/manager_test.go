package imports

import (
	"context"
	stdjson "encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stderrors "errors"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// fixedClock returns a clock returning successive values from a slice,
// repeating the last value once exhausted. Used to drive sliding-window
// touch and sweep tests deterministically.
type fixedClock struct {
	t time.Time
}

func (c *fixedClock) now() time.Time { return c.t }
func (c *fixedClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newTestManager(t *testing.T) (*Manager, afero.Fs, *fixedClock) {
	t.Helper()
	afs := afero.NewMemMapFs()
	clk := &fixedClock{t: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)}
	m, err := New(afs, Options{Now: clk.now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m, afs, clk
}

func writeCSV(t *testing.T, afs afero.Fs, p string) {
	t.Helper()
	body := "id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n3,Carol,30.75\n"
	if err := afero.WriteFile(afs, p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestManager_Open_CSV_CreatesManagedHandle(t *testing.T) {
	m, afs, clk := newTestManager(t)
	writeCSV(t, afs, "data.csv")

	res, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !res.Managed {
		t.Fatalf("expected Managed=true, got false")
	}
	if res.Handle != "data" {
		t.Errorf("handle = %q, want %q", res.Handle, "data")
	}
	if res.Path != "imports/data.pulse" {
		t.Errorf("path = %q, want %q", res.Path, "imports/data.pulse")
	}
	if res.RowsImported != 3 {
		t.Errorf("rows = %d, want 3", res.RowsImported)
	}
	if res.ExpiresAt == nil {
		t.Fatalf("ExpiresAt nil; expected slot to be set")
	}
	want := clk.now().Add(DefaultTTL)
	if !res.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", *res.ExpiresAt, want)
	}

	// File + sidecar present on disk.
	if ok, _ := afero.Exists(afs, "imports/data.pulse"); !ok {
		t.Errorf("imports/data.pulse missing after Open")
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"+SidecarSuffix); !ok {
		t.Errorf("sidecar missing after Open")
	}
}

func TestManager_Open_PulsePassthrough_NoSidecar(t *testing.T) {
	m, afs, _ := newTestManager(t)
	// Stub native pulse file with no schema; we only check sidecar
	// absence, never read it back.
	if err := afero.WriteFile(afs, "curated.pulse", []byte("PULSE\x00\x00\x00\x01"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := m.Open(context.Background(), Spec{SourcePath: "curated.pulse"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Managed {
		t.Fatalf("expected Managed=false for pulse passthrough, got true")
	}
	if res.Path != "curated.pulse" {
		t.Errorf("path = %q, want %q", res.Path, "curated.pulse")
	}
	if res.ExpiresAt != nil {
		t.Errorf("ExpiresAt populated on passthrough; should be nil")
	}
	if ok, _ := afero.Exists(afs, "imports/curated.pulse"+SidecarSuffix); ok {
		t.Errorf("sidecar created for passthrough; should not exist")
	}
}

func TestManager_Open_HandleCollision_Errors(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeCSV(t, afs, "data.csv")
	if _, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"})
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_HANDLE_EXISTS {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_HANDLE_EXISTS)
	}
}

func TestManager_Open_Overwrite_Succeeds(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeCSV(t, afs, "data.csv")
	if _, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	res, err := m.Open(context.Background(), Spec{SourcePath: "data.csv", Overwrite: true})
	if err != nil {
		t.Fatalf("overwrite Open: %v", err)
	}
	if res.Handle != "data" {
		t.Errorf("handle = %q, want data", res.Handle)
	}
}

func TestManager_Open_UnknownFormat_Errors(t *testing.T) {
	m, afs, _ := newTestManager(t)
	if err := afero.WriteFile(afs, "data.bogus", []byte("foo"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := m.Open(context.Background(), Spec{SourcePath: "data.bogus"})
	if err == nil {
		t.Fatalf("expected format-unknown error, got nil")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_FORMAT_UNKNOWN {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_FORMAT_UNKNOWN)
	}
}

func TestManager_Open_SourceMissing_Errors(t *testing.T) {
	m, _, _ := newTestManager(t)
	_, err := m.Open(context.Background(), Spec{SourcePath: "ghost.csv"})
	if err == nil {
		t.Fatalf("expected source-missing error, got nil")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_SOURCE_MISSING {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_SOURCE_MISSING)
	}
}

func TestManager_Touch_SlidesExpiry(t *testing.T) {
	m, _, clk := newTestManager(t)
	afs := m.afs
	writeCSV(t, afs, "data.csv")
	res, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	originalExpiry := *res.ExpiresAt

	clk.advance(2 * time.Hour)
	if err := m.Touch(context.Background(), res.Path); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	entries, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if !entries[0].Sidecar.ExpiresAt.After(originalExpiry) {
		t.Errorf("expiry %v did not slide past %v", entries[0].Sidecar.ExpiresAt, originalExpiry)
	}
}

func TestManager_Touch_NoOpForUnmanagedPath(t *testing.T) {
	m, _, _ := newTestManager(t)
	// Touch with an arbitrary path outside the imports dir should not
	// error — it is a no-op hook the facade can fire unconditionally.
	if err := m.Touch(context.Background(), "some/other/cohort.pulse"); err != nil {
		t.Errorf("Touch on unmanaged path errored: %v", err)
	}
}

func TestManager_Sweep_RemovesExpired(t *testing.T) {
	m, afs, clk := newTestManager(t)
	writeCSV(t, afs, "data.csv")
	res, err := m.Open(context.Background(), Spec{SourcePath: "data.csv", TTL: 1 * time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	clk.advance(2 * time.Hour) // past expiry

	swept, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(swept) != 1 || swept[0] != "data" {
		t.Errorf("swept = %v, want [data]", swept)
	}
	if ok, _ := afero.Exists(afs, res.Path); ok {
		t.Errorf("expired .pulse still present after sweep")
	}
	if ok, _ := afero.Exists(afs, res.Path+SidecarSuffix); ok {
		t.Errorf("expired sidecar still present after sweep")
	}
}

func TestManager_Sweep_PinnedHandlesSurvive(t *testing.T) {
	m, afs, clk := newTestManager(t)
	writeCSV(t, afs, "pinned.csv")
	if _, err := m.Open(context.Background(), Spec{SourcePath: "pinned.csv", TTL: -1}); err != nil {
		t.Fatalf("Open pinned: %v", err)
	}

	clk.advance(365 * 24 * time.Hour) // way past any normal TTL

	swept, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(swept) != 0 {
		t.Errorf("swept pinned handles: %v", swept)
	}
	if ok, _ := afero.Exists(afs, "imports/pinned.pulse"); !ok {
		t.Errorf("pinned file removed by sweep")
	}
}

func TestManager_Drop_RemovesHandle(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeCSV(t, afs, "data.csv")
	if _, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := m.Drop(context.Background(), "data"); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"); ok {
		t.Errorf("file present after Drop")
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"+SidecarSuffix); ok {
		t.Errorf("sidecar present after Drop")
	}
}

func TestManager_Drop_UnknownHandle_Errors(t *testing.T) {
	m, _, _ := newTestManager(t)
	err := m.Drop(context.Background(), "ghost")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_SOURCE_MISSING {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_SOURCE_MISSING)
	}
}

func TestManager_List_SortedByHandle(t *testing.T) {
	m, afs, _ := newTestManager(t)
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		p := name + ".csv"
		writeCSV(t, afs, p)
		if _, err := m.Open(context.Background(), Spec{SourcePath: p}); err != nil {
			t.Fatalf("Open %s: %v", p, err)
		}
	}
	entries, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Handle
	}
	want := []string{"alpha", "bravo", "charlie"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List order = %v, want %v", got, want)
	}
}

func TestManager_Open_CustomHandle(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeCSV(t, afs, "src.csv")
	res, err := m.Open(context.Background(), Spec{SourcePath: "src.csv", Handle: "mydata"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Handle != "mydata" {
		t.Errorf("handle = %q, want mydata", res.Handle)
	}
	if res.Path != "imports/mydata.pulse" {
		t.Errorf("path = %q, want imports/mydata.pulse", res.Path)
	}
}

func TestManager_Open_InvalidHandle_Rejected(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeCSV(t, afs, "src.csv")
	cases := []string{"../escape", "sub/path", ".hidden"}
	for _, h := range cases {
		_, err := m.Open(context.Background(), Spec{SourcePath: "src.csv", Handle: h})
		if err == nil {
			t.Errorf("handle %q accepted; expected rejection", h)
		}
	}
}

func TestManager_Resolve_ReturnsManagedPath(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeCSV(t, afs, "data.csv")
	if _, err := m.Open(context.Background(), Spec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	p, err := m.Resolve(context.Background(), "data")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p != path.Join(m.ImportsDir(), "data.pulse") {
		t.Errorf("resolved = %q, want %q", p, path.Join(m.ImportsDir(), "data.pulse"))
	}
}

func TestManager_IsManagedPath(t *testing.T) {
	m, _, _ := newTestManager(t)
	if !m.IsManagedPath("imports/foo.pulse") {
		t.Errorf("imports/foo.pulse not flagged as managed")
	}
	if m.IsManagedPath("other/foo.pulse") {
		t.Errorf("other/foo.pulse flagged as managed")
	}
}

// TestManager_Open_AbsoluteCSV_FromExternalSource is the regression
// gate for the MCP-side bug: pulse_import handed an absolute source
// path that lives outside the rooted Pulse fs must still succeed. The
// Manager routes absolute reads through srcFs while writing the
// managed copy into the dst (rooted) fs.
func TestManager_Open_AbsoluteCSV_FromExternalSource(t *testing.T) {
	dst := afero.NewMemMapFs()
	src := afero.NewMemMapFs()
	body := "id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n"
	if err := afero.WriteFile(src, "/Users/alice/datasets/orders.csv", []byte(body), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	clk := &fixedClock{t: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)}
	m, err := New(dst, Options{SourceFS: src, Now: clk.now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := m.Open(context.Background(), Spec{SourcePath: "/Users/alice/datasets/orders.csv"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !res.Managed || res.Handle != "orders" || res.Path != "imports/orders.pulse" {
		t.Errorf("result = %+v, want managed handle 'orders' at imports/orders.pulse", res)
	}
	if res.RowsImported != 2 {
		t.Errorf("rows = %d, want 2", res.RowsImported)
	}
	if ok, _ := afero.Exists(dst, "imports/orders.pulse"); !ok {
		t.Errorf("managed file missing from dst fs")
	}
	// Sidecar records the absolute source path verbatim.
	entries, _ := m.List(context.Background())
	if len(entries) != 1 || entries[0].Sidecar.SourcePath != "/Users/alice/datasets/orders.csv" {
		t.Errorf("sidecar source_path = %+v, want absolute path", entries)
	}
}

// TestManager_Open_AbsolutePulse_CopiesIntoPool exercises the
// pulse-passthrough-absolute path: when the source is a .pulse file
// outside the rooted fs, the Manager copies it into the managed pool
// (the rooted fs cannot address external absolute paths via the normal
// inspect / process tools).
func TestManager_Open_AbsolutePulse_CopiesIntoPool(t *testing.T) {
	dst := afero.NewMemMapFs()
	src := afero.NewMemMapFs()
	header := []byte("PULSE\x00\x00\x00\x01")
	if err := afero.WriteFile(src, "/var/data/curated.pulse", header, 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	clk := &fixedClock{t: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)}
	m, err := New(dst, Options{SourceFS: src, Now: clk.now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := m.Open(context.Background(), Spec{SourcePath: "/var/data/curated.pulse"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !res.Managed {
		t.Errorf("absolute pulse passthrough must be managed=true (copied into pool); got %+v", res)
	}
	if res.Path != "imports/curated.pulse" {
		t.Errorf("path = %q, want imports/curated.pulse", res.Path)
	}
	if ok, _ := afero.Exists(dst, "imports/curated.pulse"); !ok {
		t.Errorf("copied file missing from dst fs")
	}
	// Original source untouched.
	if ok, _ := afero.Exists(src, "/var/data/curated.pulse"); !ok {
		t.Errorf("source file removed (should be left in place)")
	}
}

// TestManager_Open_AbsoluteMissingSource_Errors covers the case where
// the absolute path's source fs cannot find the file. Surfaces
// PULSE_IMPORT_SOURCE_MISSING just like the relative-path branch.
func TestManager_Open_AbsoluteMissingSource_Errors(t *testing.T) {
	dst := afero.NewMemMapFs()
	src := afero.NewMemMapFs() // empty
	m, err := New(dst, Options{SourceFS: src})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = m.Open(context.Background(), Spec{SourcePath: "/Users/ghost/missing.csv"})
	if err == nil {
		t.Fatalf("expected source-missing error, got nil")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_SOURCE_MISSING {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_SOURCE_MISSING)
	}
}

// TestManager_Jail_DefaultsToCWD verifies that when SourceFS is left
// unset the Manager establishes a jail at the process working
// directory. The jail accessor reports the cleaned absolute root.
func TestManager_Jail_DefaultsToCWD(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	// Real OsFs to trigger the jail-establishment branch.
	m, err := New(afero.NewOsFs(), Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := m.JailRoot()
	want, _ := filepath.Abs(tmp)
	// macOS /tmp is a symlink to /private/tmp; resolve both ends.
	gotResolved, _ := filepath.EvalSymlinks(got)
	wantResolved, _ := filepath.EvalSymlinks(want)
	if gotResolved != wantResolved {
		t.Errorf("JailRoot() = %q, want %q (cwd)", got, want)
	}
}

// TestManager_Jail_AcceptsPathInside verifies that an absolute path
// under the configured jail succeeds normally.
func TestManager_Jail_AcceptsPathInside(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "data.csv")
	if err := os.WriteFile(src, []byte("id,name\n1,Alice\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dst := afero.NewMemMapFs()
	m, err := New(dst, Options{SourceJailRoot: tmp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := m.Open(context.Background(), Spec{SourcePath: src})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !res.Managed || res.RowsImported != 1 {
		t.Errorf("result = %+v, want managed handle with 1 row", res)
	}
}

// TestManager_Jail_RejectsPathOutside verifies that an absolute path
// outside the jail returns PULSE_IMPORT_SOURCE_FORBIDDEN — no read,
// no copy, no managed handle created.
func TestManager_Jail_RejectsPathOutside(t *testing.T) {
	jail := t.TempDir()
	outside := t.TempDir() // distinct tree
	src := filepath.Join(outside, "secret.csv")
	if err := os.WriteFile(src, []byte("id,name\n1,Eve\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dst := afero.NewMemMapFs()
	m, err := New(dst, Options{SourceJailRoot: jail})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = m.Open(context.Background(), Spec{SourcePath: src})
	if err == nil {
		t.Fatalf("expected jail violation, got nil")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_SOURCE_FORBIDDEN {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_SOURCE_FORBIDDEN)
	}
	// Verify no managed file was created as a side effect.
	if ok, _ := afero.Exists(dst, "imports/secret.pulse"); ok {
		t.Errorf("managed file created despite jail rejection")
	}
}

// TestManager_Jail_RejectsTraversal verifies a ".." escape attempt is
// rejected. filepath.Clean normalises the path before the containment
// check, so "/jail/../etc/passwd" resolves to "/etc/passwd" and is
// blocked.
func TestManager_Jail_RejectsTraversal(t *testing.T) {
	jail := t.TempDir()
	dst := afero.NewMemMapFs()
	m, err := New(dst, Options{SourceJailRoot: jail})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	escape := jail + "/../" + "etc/passwd-fake.csv"
	_, err = m.Open(context.Background(), Spec{SourcePath: escape})
	if err == nil {
		t.Fatalf("traversal accepted; expected jail violation")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_SOURCE_FORBIDDEN {
		t.Errorf("error code = %v, want %v", err, perr.PULSE_IMPORT_SOURCE_FORBIDDEN)
	}
}

// TestManager_Jail_DisabledWhenSourceFSExplicit verifies that callers
// passing an explicit SourceFS opt out of the jail entirely (they are
// asserting they manage access boundaries themselves).
func TestManager_Jail_DisabledWhenSourceFSExplicit(t *testing.T) {
	dst := afero.NewMemMapFs()
	src := afero.NewMemMapFs()
	if err := afero.WriteFile(src, "/anywhere/data.csv", []byte("id,name\n1,A\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := New(dst, Options{SourceFS: src, SourceJailRoot: "/blocked"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.JailRoot() != "" {
		t.Errorf("JailRoot = %q, want empty (no jail when SourceFS set)", m.JailRoot())
	}
	_, err = m.Open(context.Background(), Spec{SourcePath: "/anywhere/data.csv"})
	if err != nil {
		t.Errorf("Open: %v — jail should be inactive when SourceFS is explicit", err)
	}
}

// writeSetCSV writes a CSV whose "issuers" column carries
// pipe-delimited tokens but only in a minority of rows; without the
// override the importer would land it as categorical. With the
// override pinning set_u8 the inference is bypassed entirely.
func writeSetCSV(t *testing.T, afs afero.Fs, p string) {
	t.Helper()
	body := "id,issuers\n1,VISA|MC\n2,VISA\n3,AMEX\n"
	if err := afero.WriteFile(afs, p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// TestManager_Open_CSV_ColumnTypeOverridesPersist confirms (a) the
// force_type sidecar override is honored at import time and (b) the
// resulting Sidecar JSON round-trips the override map so a later
// session can re-open with the same intent.
func TestManager_Open_CSV_ColumnTypeOverridesPersist(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeSetCSV(t, afs, "issuers.csv")
	res, err := m.Open(context.Background(), Spec{
		SourcePath: "issuers.csv",
		ColumnTypeOverrides: map[string]string{
			"issuers": "set_u8",
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !res.Managed {
		t.Fatal("expected Managed=true")
	}
	// Read sidecar back from disk and verify ColumnTypeOverrides
	// round-tripped.
	sidecarPath := res.Path + SidecarSuffix
	raw, err := afero.ReadFile(afs, sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var got Sidecar
	if err := stdjson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal sidecar: %v", err)
	}
	if got.ColumnTypeOverrides == nil || got.ColumnTypeOverrides["issuers"] != "set_u8" {
		t.Errorf("sidecar.ColumnTypeOverrides = %v, want issuers->set_u8",
			got.ColumnTypeOverrides)
	}
}

// TestManager_Open_CSV_ColumnTypeOverridesUnknownRejected covers the
// validation that runs when the sidecar carries an unknown FieldType
// string — parseColumnTypeOverrides should fail fast with
// SERVICE_VALIDATION rather than passing through to ImportJob.
func TestManager_Open_CSV_ColumnTypeOverridesUnknownRejected(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeSetCSV(t, afs, "issuers.csv")
	_, err := m.Open(context.Background(), Spec{
		SourcePath: "issuers.csv",
		ColumnTypeOverrides: map[string]string{
			"issuers": "not_a_real_type",
		},
	})
	if err == nil {
		t.Fatal("expected SERVICE_VALIDATION for unknown override type")
	}
}
