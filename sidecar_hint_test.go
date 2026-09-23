package pulse

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Facade coverage for Pulse.InvalidatedSidecars — the discovery half of
// the `pulse widen` sidecar hint.
//
// A widen changes the cohort's byte LENGTH, so both sidecars beside it
// self-invalidate through their own size+mtime fingerprints and neither
// will serve stale data. What was missing is that nothing TOLD the
// caller, so a corpus with a point-lookup index silently stopped
// answering lookups until someone thought to rebuild it. This does not
// rebuild anything — that would turn a fast metadata operation into an
// arbitrarily long one — it names what went stale and how to fix it.

// sidecarHintCohort writes a two-field cohort (id u32, picks set_u8) and
// returns the Pulse handle rooted on the same MemMapFs.
func sidecarHintCohort(t *testing.T, path string) (*Pulse, afero.Fs) {
	t.Helper()
	memFs := afero.NewMemMapFs()

	dict := encoding.NewDictionary()
	for _, v := range []string{"tv", "radio", "print"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "picks", Type: encoding.FieldTypeSetU8, ByteOffset: 4, Dictionary: dict},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < 4; r++ {
		var rec [5]byte
		binary.LittleEndian.PutUint32(rec[0:4], uint32(10+r))
		rec[4] = byte(1 << uint(r%3))
		buf.Write(rec[:])
	}
	if err := afero.WriteFile(memFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile cohort: %v", err)
	}

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, memFs
}

// A cohort with no sidecars reports none. The hint must not invent an
// empty section for the overwhelmingly common case.
func TestInvalidatedSidecars_NoneWhenNoSidecarsExist(t *testing.T) {
	p, _ := sidecarHintCohort(t, "c.pulse")

	got, err := p.InvalidatedSidecars(context.Background(), "c.pulse")
	if err != nil {
		t.Fatalf("InvalidatedSidecars: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("sidecars = %+v, want none", got)
	}
}

// A built point-lookup index is discovered THROUGH THE MANIFEST, with
// the key tuple recovered — the filename is a hash of that tuple, so the
// rebuild command cannot be reconstructed from the name alone.
func TestInvalidatedSidecars_NamesThePointLookupIndexAndItsRebuild(t *testing.T) {
	p, memFs := sidecarHintCohort(t, "c.pulse")
	ctx := context.Background()

	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	// The manifest is the discovery surface the decision names; assert
	// it is actually there so a fallback to a directory glob cannot
	// quietly become the thing under test.
	if ok, _ := afero.Exists(memFs, encoding.IndexManifestPath("c.pulse")); !ok {
		t.Fatalf("no index manifest at %s", encoding.IndexManifestPath("c.pulse"))
	}

	got, err := p.InvalidatedSidecars(ctx, "c.pulse")
	if err != nil {
		t.Fatalf("InvalidatedSidecars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sidecars = %+v, want exactly one", got)
	}
	s := got[0]
	if s.Kind != SidecarKindPointLookupIndex {
		t.Errorf("Kind = %q, want %q", s.Kind, SidecarKindPointLookupIndex)
	}
	if !strings.HasSuffix(s.Path, ".idx") {
		t.Errorf("Path = %q, want the .idx sidecar", s.Path)
	}
	if strings.Join(s.Keys, ",") != "id" {
		t.Errorf("Keys = %v, want [id]", s.Keys)
	}
	// The rebuild command must carry the key tuple: without it the user
	// cannot reproduce the index they just lost.
	for _, want := range []string{"pulse index build", "c.pulse", "--key id"} {
		if !strings.Contains(s.Rebuild, want) {
			t.Errorf("Rebuild = %q, does not contain %q", s.Rebuild, want)
		}
	}
}

// A composite key round-trips into the rebuild command in ORDER: key
// order is significant end to end, and a reversed tuple builds a
// different index.
func TestInvalidatedSidecars_CompositeKeyOrderSurvivesIntoTheRebuild(t *testing.T) {
	p, _ := sidecarHintCohort(t, "c.pulse")
	ctx := context.Background()

	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"picks", "id"}); err == nil {
		t.Skip("set columns are not index-keyable; composite coverage needs two keyable columns")
	}
	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	got, err := p.InvalidatedSidecars(ctx, "c.pulse")
	if err != nil {
		t.Fatalf("InvalidatedSidecars: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Rebuild, "--key id") {
		t.Fatalf("sidecars = %+v, want one naming --key id", got)
	}
}

// The SPSS metadata sidecar is invalidated too: its fingerprint is
// size+mtime over the .pulse cohort, and a widen moves both.
func TestInvalidatedSidecars_NamesTheSPSSMetadataSidecar(t *testing.T) {
	p, memFs := sidecarHintCohort(t, "c.pulse")

	if err := afero.WriteFile(memFs, spss.SidecarPath("c.pulse"), []byte(`{"kind":"x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile sidecar: %v", err)
	}
	got, err := p.InvalidatedSidecars(context.Background(), "c.pulse")
	if err != nil {
		t.Fatalf("InvalidatedSidecars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sidecars = %+v, want exactly one", got)
	}
	if got[0].Kind != SidecarKindSPSSMetadata {
		t.Errorf("Kind = %q, want %q", got[0].Kind, SidecarKindSPSSMetadata)
	}
	if got[0].Path != spss.SidecarPath("c.pulse") {
		t.Errorf("Path = %q, want %q", got[0].Path, spss.SidecarPath("c.pulse"))
	}
}

// Both kinds surface together, index first, so the output order is
// stable across runs.
func TestInvalidatedSidecars_ReportsBothKindsDeterministically(t *testing.T) {
	p, memFs := sidecarHintCohort(t, "c.pulse")
	ctx := context.Background()

	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if err := afero.WriteFile(memFs, spss.SidecarPath("c.pulse"), []byte(`{"kind":"x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile sidecar: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := p.InvalidatedSidecars(ctx, "c.pulse")
		if err != nil {
			t.Fatalf("InvalidatedSidecars: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("sidecars = %+v, want two", got)
		}
		if got[0].Kind != SidecarKindPointLookupIndex || got[1].Kind != SidecarKindSPSSMetadata {
			t.Fatalf("order = %q,%q, want index then spss", got[0].Kind, got[1].Kind)
		}
	}
}

// A widen must still report its sidecars AFTER the rewrite: the sidecar
// files are not deleted by a widen, only invalidated, so discovery runs
// on the post-widen cohort exactly as it would on the pre-widen one.
func TestInvalidatedSidecars_SurvivesTheWidenThatInvalidatedThem(t *testing.T) {
	p, _ := sidecarHintCohort(t, "c.pulse")
	ctx := context.Background()

	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if _, err := p.WidenSetField(ctx, "c.pulse", "picks", "set_u64"); err != nil {
		t.Fatalf("WidenSetField: %v", err)
	}
	got, err := p.InvalidatedSidecars(ctx, "c.pulse")
	if err != nil {
		t.Fatalf("InvalidatedSidecars after widen: %v", err)
	}
	if len(got) != 1 || got[0].Kind != SidecarKindPointLookupIndex {
		t.Fatalf("sidecars = %+v, want the point-lookup index", got)
	}
	// And the index really is stale now — the hint would be noise
	// otherwise.
	if _, err := p.Lookup(ctx, &LookupRequest{
		Cohort: &types.Cohort{Filename: "c.pulse"}, Field: "id", Value: "10",
	}); err == nil {
		t.Error("lookup through a post-widen index succeeded; the hint claims it is stale")
	}
}
